package api

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Idempotent writes. A client that retries a request after a timeout cannot know whether the
// server applied it. With the header "Idempotency-Key: <unique id>" the first response is stored
// and a retry with the same key (same method and path) gets it back without writing again: no
// duplicated steps, screenshots or network batches.
//
// Guarantees, by kind of write:
//   - Writes that add rows (create run/test, steps, network, console, DOM, screenshot and artifact
//     records, verdicts) go through Server.commit: the rows and the stored response are committed
//     in one SQLite transaction. A crash leaves both or neither, so a retry never duplicates them.
//   - Files (screenshots, artifacts) are written before that transaction: a crash in between
//     leaves an unreferenced file (no duplicated evidence); the retry writes a new one.
//   - Writes that set a state (finish test/run, quarantine) store their response after
//     applying it. A crash in between makes the retry apply the same state again, which gives
//     the same result.
//   - Imports (JUnit, Allure) span several transactions: a crash in the middle leaves an
//     incomplete run and the retry imports again. Effects outside the server (AI calls, Teams,
//     Slack, tickets) are never "exactly once": see the docs (api.md, Idempotency-Key).

// idemReq is the Idempotency-Key of the request being served (set by the middleware).
type idemReq struct {
	key, method, path string
	saved             bool // commit ya guardó la respuesta junto con la escritura
}

type idemCtxKey struct{}

func idemOf(r *http.Request) *idemReq {
	v, _ := r.Context().Value(idemCtxKey{}).(*idemReq)
	return v
}

// commit runs the database part of a write in one transaction, storing in it the idempotent
// response when the request carries an Idempotency-Key, and then answers. fn must use only the
// Store it receives (see db.Store.Atomic); side effects go after commit returns nil. On error
// nothing was written and nothing was answered.
func (s *Server) commit(w http.ResponseWriter, r *http.Request, fn func(tx *db.Store) (status int, out any, err error)) error {
	var status int
	var body []byte
	ir := idemOf(r)
	err := s.Store.Atomic(func(tx *db.Store) (*db.Idem, error) {
		st, out, err := fn(tx)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		status, body = st, append(b, '\n') // igual que writeJSON
		if ir == nil || st >= 500 {
			return nil, nil
		}
		return &db.Idem{Key: ir.key, Method: ir.method, Path: ir.path, Status: st, Body: body}, nil
	})
	if err != nil {
		return err
	}
	if ir != nil {
		ir.saved = true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return nil
}

const maxIdempotencyKey = 200

// idemLocks serializes the requests that share an Idempotency-Key. Each entry counts its users
// (the owner and everyone waiting): it is removed only when the last one leaves, so a new request
// can never get a second mutex for a key that is still in use.
type idemLocks struct {
	mu   sync.Mutex
	busy map[string]*idemLock
}

type idemLock struct {
	m    sync.Mutex
	refs int
}

func (l *idemLocks) lock(key string) func() {
	l.mu.Lock()
	if l.busy == nil {
		l.busy = map[string]*idemLock{}
	}
	e := l.busy[key]
	if e == nil {
		e = &idemLock{}
		l.busy[key] = e
	}
	e.refs++
	l.mu.Unlock()
	e.m.Lock()
	return func() {
		e.m.Unlock()
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.busy, key)
		}
		l.mu.Unlock()
	}
}

// recentResponses keeps in memory the responses that could not be saved in the database, so a
// retry still gets them back instead of applying the write again. Bounded by entries and bytes,
// oldest out first; a response is kept whole or not at all (never truncated).
//
// It is only a fallback for writes that did not go through Server.commit (those save the
// response in the same transaction, or fail without writing): writes that set a state, where
// applying a retry again gives the same result. An evicted entry therefore does not duplicate
// rows; it is logged so it can be noticed.
type recentResponses struct {
	mu    sync.Mutex
	byKey map[string]*list.Element
	order list.List // de la más vieja a la más nueva; Value: *recentEntry
	bytes int
}

type recentEntry struct {
	key string
	storedResponse
}

type storedResponse struct {
	status int
	body   []byte
}

const (
	maxRecentResponses = 2000
	maxRecentBytes     = 8 << 20 // 8 MiB entre todas
)

func (r *recentResponses) get(key string) (storedResponse, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.byKey[key]; ok {
		return e.Value.(*recentEntry).storedResponse, true
	}
	return storedResponse{}, false
}

// put keeps v; false when it is larger than the whole budget and was not kept.
func (r *recentResponses) put(key string, v storedResponse) bool {
	size := len(key) + len(v.body)
	if size > maxRecentBytes {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey = map[string]*list.Element{}
	}
	if e, ok := r.byKey[key]; ok {
		r.remove(e)
	}
	r.byKey[key] = r.order.PushBack(&recentEntry{key: key, storedResponse: v})
	r.bytes += size
	for r.order.Len() > maxRecentResponses || r.bytes > maxRecentBytes {
		old := r.order.Front()
		slog.Warn("idempotency: in-memory fallback full, forgetting the oldest response", "key", old.Value.(*recentEntry).key)
		r.remove(old)
	}
	return true
}

func (r *recentResponses) remove(e *list.Element) {
	v := e.Value.(*recentEntry)
	r.order.Remove(e)
	delete(r.byKey, v.key)
	r.bytes -= len(v.key) + len(v.body)
}

var recent recentResponses

var idem idemLocks

type capture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capture) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *capture) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}

// idemRun is the run a write belongs to (0 if none): its stored response lives as long as the run.
func (s *Server) idemRun(r *http.Request, c *capture) int64 {
	if c.status >= 300 {
		return 0
	}
	return s.Store.RunForWrite(r.Method, r.URL.Path, c.body.Bytes())
}

// idempotent replays the stored response of a POST/PATCH already applied with the same
// Idempotency-Key. Responses with 5xx are not stored, so the retry runs again.
func (s *Server) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || (r.Method != http.MethodPost && r.Method != http.MethodPatch) {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > maxIdempotencyKey {
			writeError(w, http.StatusBadRequest, "Idempotency-Key too long")
			return
		}
		full := r.Method + " " + r.URL.Path + " " + key
		unlock := idem.lock(full)
		defer unlock()
		status, body, found, err := s.Store.IdempotentResponse(full)
		if err != nil {
			serverError(w, err)
			return
		}
		if !found {
			if v, ok := recent.get(full); ok {
				status, body, found = v.status, v.body, true
			}
		}
		if found {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		ir := &idemReq{key: full, method: r.Method, path: r.URL.Path}
		c := &capture{ResponseWriter: w}
		next.ServeHTTP(c, r.WithContext(context.WithValue(r.Context(), idemCtxKey{}, ir)))
		if ir.saved {
			return // ya quedó guardada en la misma transacción que la escritura
		}
		if c.status > 0 && c.status < 500 {
			if err := s.Store.SaveIdempotentResponse(full, c.status, c.body.Bytes(), s.idemRun(r, c)); err != nil {
				// la escritura ya se aplicó: sin la respuesta guardada, un reintento la repetiría
				slog.Warn("idempotency: save response, kept in memory", "err", err)
				if !recent.put(full, storedResponse{c.status, append([]byte(nil), c.body.Bytes()...)}) {
					slog.Warn("idempotency: response too large to keep in memory; a retry applies the write again", "key", full)
				}
			}
		}
	})
}
