package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
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
		// y el test, para "sin señal" en En vivo (hora del servidor, no la del cliente)
		if testID := testForWrite(r.URL.Path); testID > 0 {
			if err := s.Store.TouchTest(testID); err != nil {
				slog.Warn("activity: touch test", "test_id", testID, "err", err)
			}
		}
	})
}

// idleText is an inactivity as the interrupted tests explain it: "45 min", "24 h", "1 h 30 min".
func idleText(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	switch {
	case m < 60:
		return fmt.Sprintf("%d min", max(m, 1))
	case m%60 == 0:
		return fmt.Sprintf("%d h", m/60)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}

// CloseStaleRuns closes, as incomplete, the runs still RUNNING that received nothing for idle
// (the client died without calling finish). It is the same close as an interrupted finish: the
// tests left RUNNING become INTERRUPTED, and the run diagnosis and the Teams/Slack
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
		ok, first, err := s.Store.CloseIfIdle(id, cutoff, db.IdleMessage(idleText(idle)))
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
