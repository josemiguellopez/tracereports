package api

import (
	"log/slog"
	"net/http"
	"time"
)

// trackActivity records, after each successful write of a client to a run (/runs/{id}/...,
// /tests/{id}/... or creating it), that the run is alive: see CloseStaleRuns.
func (s *Server) trackActivity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPatch {
			next.ServeHTTP(w, r)
			return
		}
		c := &capture{ResponseWriter: w}
		next.ServeHTTP(c, r)
		if c.status < 200 || c.status >= 300 {
			return
		}
		if runID := s.Store.RunForWrite(r.Method, r.URL.Path, c.body.Bytes()); runID > 0 {
			if err := s.Store.TouchRun(runID); err != nil {
				slog.Warn("activity: touch run", "run_id", runID, "err", err)
			}
		}
	})
}

// CloseStaleRuns closes, as incomplete, the runs still RUNNING that received nothing for idle
// (the client died without calling finish). It is the same close as an interrupted finish: the
// tests left RUNNING become interrupted failures, and the run diagnosis and the Teams/Slack
// notice follow once. Evidence or a finish arriving later is accepted as late evidence. Returns
// how many runs it closed.
func (s *Server) CloseStaleRuns(idle time.Duration) (int, error) {
	cutoff := time.Now().Add(-idle).UnixMilli()
	ids, err := s.Store.StaleRuns(cutoff)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range ids {
		ok, first, err := s.Store.CloseIfIdle(id, cutoff)
		if err != nil {
			slog.Error("stale runs: close", "run_id", id, "err", err)
			continue
		}
		if ok {
			closed++
			slog.Info("stale runs: closed a run without activity as incomplete", "run_id", id, "idle", idle.String())
			s.runClosed(id, first)
		}
	}
	return closed, nil
}
