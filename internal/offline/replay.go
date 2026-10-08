package offline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Doer sends a request: an *http.Client for a remote server, or Handler for one in process.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Handler adapts an http.Handler (the API router) into a Doer, without a network.
type Handler struct{ http.Handler }

// Do serves req in process.
func (h Handler) Do(req *http.Request) (*http.Response, error) {
	rec := &recorder{header: http.Header{}, code: http.StatusOK}
	if req.RemoteAddr == "" {
		req.RemoteAddr = "127.0.0.1:0"
	}
	if req.Host == "" {
		req.Host = "localhost" // el servidor solo atiende Host conocidos (DNS rebinding)
	}
	h.ServeHTTP(rec, req)
	return &http.Response{StatusCode: rec.code, Header: rec.header, Body: io.NopCloser(&rec.body), Request: req}, nil
}

type recorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
	wrote  bool
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.body.Write(b)
}
func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.code, r.wrote = code, true
	}
}

// Target is where a recording is replayed.
type Target struct {
	Doer    Doer
	BaseURL string // "" for an in-process Handler
	Token   string // TRACEREPORTS_TOKEN of a remote server
}

// Result says what a replay did.
type Result struct {
	Runs     []int64  // runs created, in order
	Sent     int      // events accepted by the server
	Skipped  int      // events of a run or test whose creation was rejected
	Rejected int      // events the server answered with an error
	Errors   []string // first errors, for the user
}

const maxErrors = 10

var localRef = regexp.MustCompile(`/(runs|tests)/(-\d+)(/|$)`)

// Replay sends every event of rec to t. A remote server can be pushed to again safely: each
// event carries an Idempotency-Key derived from the recording, so nothing is duplicated.
func (rec *Recording) Replay(t Target) (*Result, error) {
	res := &Result{}
	ids := map[int64]int64{}     // id local -> id real
	open := map[int64]int64{}    // ejecuciones creadas sin su cierre -> ts del último evento
	failed := map[int64]bool{}   // ids locales cuya creación falló
	testRun := map[int64]int64{} // test real -> su ejecución (para saber cuándo fue su último evento)
	for _, e := range rec.Events() {
		path, missing := rewrite(e.Path, ids)
		if missing != 0 {
			res.Skipped++
			if !failed[missing] {
				failed[missing] = true
				res.addErr(fmt.Sprintf("%s: %s %s refers to %d, which was never created", e.file, e.Method, e.Path, missing))
			}
			continue
		}
		body, err := rec.body(e)
		if err != nil {
			res.Rejected++
			res.addErr(fmt.Sprintf("%s seq %d: %v", e.file, e.Seq, err))
			continue
		}
		code, out, err := t.send(e.Method, path, e.ContentType, body, e.TS, rec.key(e))
		if err != nil {
			return res, fmt.Errorf("%s %s: %w", e.Method, path, err)
		}
		if code >= 300 {
			res.Rejected++
			if e.LocalID != 0 {
				failed[e.LocalID] = true
			}
			res.addErr(fmt.Sprintf("%s %s -> HTTP %d %s", e.Method, e.Path, code, strings.TrimSpace(string(out))))
			continue
		}
		res.Sent++
		if e.LocalID != 0 {
			var created struct {
				RunID  int64 `json:"run_id"`
				TestID int64 `json:"test_id"`
			}
			_ = json.Unmarshal(out, &created)
			switch {
			case created.RunID > 0:
				ids[e.LocalID] = created.RunID
				res.Runs = append(res.Runs, created.RunID)
				open[created.RunID] = e.TS
			case created.TestID > 0:
				ids[e.LocalID] = created.TestID
				testRun[created.TestID] = runOf(path)
			}
		}
		id := runOf(path)
		if id == 0 {
			id = testRun[testOf(path)]
		}
		if id > 0 {
			if _, ok := open[id]; ok {
				open[id] = e.TS
			}
			if e.Method == http.MethodPatch && strings.HasSuffix(path, "/finish") && strings.Contains(path, "/runs/") {
				delete(open, id)
			}
		}
	}
	// el proceso terminó sin cerrar la ejecución (se cortó, se mató): se cierra como incompleta
	for _, id := range res.Runs {
		ts, ok := open[id]
		if !ok {
			continue
		}
		path := fmt.Sprintf("/api/v1/runs/%d/finish", id)
		code, out, err := t.send(http.MethodPatch, path, "application/json", []byte(`{"interrupted":true}`), ts, rec.ID+"/close/"+strconv.FormatInt(id, 10))
		if err != nil {
			return res, err
		}
		if code >= 300 {
			// cuenta como rechazado: push no termina bien y el CI se entera. Repetirlo es seguro
			// (misma Idempotency-Key; una respuesta 5xx no queda guardada en el servidor)
			res.Rejected++
			res.addErr(fmt.Sprintf("closing run %d -> HTTP %d %s (push again to close it)", id, code, strings.TrimSpace(string(out))))
		}
	}
	return res, nil
}

func (r *Result) addErr(msg string) {
	if len(r.Errors) < maxErrors {
		r.Errors = append(r.Errors, msg)
	}
}

// key is the Idempotency-Key of an event: stable across pushes of the same recording.
func (rec *Recording) key(e Event) string {
	return "offline/" + rec.ID + "/" + e.file + "/" + strconv.FormatInt(e.Seq, 10)
}

// rewrite replaces local ids in path; missing is the first local id without a real one.
func rewrite(path string, ids map[int64]int64) (out string, missing int64) {
	out = localRef.ReplaceAllStringFunc(path, func(m string) string {
		sub := localRef.FindStringSubmatch(m)
		local, _ := strconv.ParseInt(sub[2], 10, 64)
		real, ok := ids[local]
		if !ok {
			if missing == 0 {
				missing = local
			}
			return m
		}
		return "/" + sub[1] + "/" + strconv.FormatInt(real, 10) + sub[3]
	})
	return out, missing
}

var (
	runPath  = regexp.MustCompile(`^/api/v1/runs/(\d+)(/|$)`)
	testPath = regexp.MustCompile(`^/api/v1/tests/(\d+)(/|$)`)
)

// runOf and testOf return the run or test id of an API path (0 if it has none).
func runOf(path string) int64  { return idIn(runPath, path) }
func testOf(path string) int64 { return idIn(testPath, path) }

func idIn(re *regexp.Regexp, path string) int64 {
	m := re.FindStringSubmatch(path)
	if m == nil {
		return 0
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id
}

func (t Target) send(method, path, contentType string, body []byte, ts int64, key string) (int, []byte, error) {
	req, err := http.NewRequest(method, strings.TrimRight(t.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Idempotency-Key", key)
	if ts > 0 {
		req.Header.Set("X-TraceReports-Timestamp", strconv.FormatInt(ts, 10))
	}
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	resp, err := t.Doer.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, out, err
}
