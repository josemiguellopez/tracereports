package api

import (
	"net/http"
	"strconv"
)

const historyLimit = 20

// testHistory returns the last executions of a test: same identity and context, up to its run.
func (s *Server) testHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, err := s.Store.GetTest(id)
	if respondErr(w, err, "test") {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 60 {
		limit = historyLimit
	}
	hist, err := s.Store.TestHistory(t.Key, t.RunID, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hist)
}

// runBaselines lists the runs a comparison can use as base (same project and environment).
func (s *Server) runBaselines(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	if _, err := s.Store.GetRun(id); respondErr(w, err, "run") {
		return
	}
	runs, err := s.Store.CompatibleRuns(id, 30)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// compareRun compares a run with ?base=<run_id> (default: the previous run of the same context).
func (s *Server) compareRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	base, _ := strconv.ParseInt(r.URL.Query().Get("base"), 10, 64)
	cmp, err := s.Store.CompareRuns(id, base)
	if respondErr(w, err, "run") {
		return
	}
	writeJSON(w, http.StatusOK, cmp)
}

// runEndpoints ranks the backend endpoints called during a run.
func (s *Server) runEndpoints(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	if _, err := s.Store.GetRun(id); respondErr(w, err, "run") {
		return
	}
	eps, err := s.Store.RunEndpoints(id, 30)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, eps)
}
