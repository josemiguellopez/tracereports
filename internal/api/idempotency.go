package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"sync"
)

// Idempotent writes. A client that retries a request after a timeout cannot know whether the
// server applied it. With the header "Idempotency-Key: <unique id>" the first response is stored
// and a retry with the same key (same method and path) gets it back without writing again: no
// duplicated steps, screenshots or network batches.

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
// retry still gets them back instead of applying the write again (bounded, oldest out first).
type recentResponses struct {
	mu    sync.Mutex
	byKey map[string]storedResponse
	order []string
}

type storedResponse struct {
	status int
	body   []byte
}

const maxRecentResponses = 2000

func (r *recentResponses) get(key string) (storedResponse, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.byKey[key]
	return v, ok
}

func (r *recentResponses) put(key string, v storedResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey = map[string]storedResponse{}
	}
	if _, ok := r.byKey[key]; !ok {
		r.order = append(r.order, key)
	}
	r.byKey[key] = v
	for len(r.order) > maxRecentResponses {
		delete(r.byKey, r.order[0])
		r.order = r.order[1:]
	}
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
		c := &capture{ResponseWriter: w}
		next.ServeHTTP(c, r)
		if c.status > 0 && c.status < 500 {
			if err := s.Store.SaveIdempotentResponse(full, c.status, c.body.Bytes(), s.idemRun(r, c)); err != nil {
				// la escritura ya se aplicó: sin la respuesta guardada, un reintento la repetiría
				slog.Warn("idempotency: save response, kept in memory", "err", err)
				recent.put(full, storedResponse{c.status, append([]byte(nil), c.body.Bytes()...)})
			}
		}
	})
}
