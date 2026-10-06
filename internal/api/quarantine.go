package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

const maxQuarantineDays = 180

// setQuarantine quarantines the test of a failure in its project: its next failures (and this
// run's) do not turn runs red until it expires. Reason is required; days defaults to 14.
func (s *Server) setQuarantine(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		TestID int64  `json:"test_id"`
		Reason string `json:"reason"`
		Owner  string `json:"owner"`
		Days   int    `json:"days"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		writeError(w, http.StatusBadRequest, "reason is required: say why the test is quarantined")
		return
	}
	if in.Days == 0 {
		in.Days = 14
	}
	if in.Days < 1 || in.Days > maxQuarantineDays {
		writeError(w, http.StatusBadRequest, "days must be between 1 and 180")
		return
	}
	t, run, ok := s.testAndRun(w, in.TestID)
	if !ok {
		return
	}
	q := &db.Quarantine{Project: run.Project, Key: t.Key, Reason: clean(s.redactor().Text(in.Reason), 500),
		Owner: clean(s.label(in.Owner), 200), Until: time.Now().Add(time.Duration(in.Days) * 24 * time.Hour).UnixMilli()}
	if err := s.Store.SetQuarantine(q); err != nil {
		serverError(w, err)
		return
	}
	s.afterQuarantine(w, run.ID, q)
}

// removeQuarantine lifts the quarantine of a test.
func (s *Server) removeQuarantine(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, run, ok := s.testAndRun(w, id)
	if !ok {
		return
	}
	if _, err := s.Store.RemoveQuarantine(run.Project, t.Key); err != nil {
		serverError(w, err)
		return
	}
	s.afterQuarantine(w, run.ID, nil)
}

// listQuarantine lists the quarantines of a project (?project=; ?all=1 for every project).
func (s *Server) listQuarantine(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.Store.ListQuarantine(q.Get("project"), q.Get("all") == "1")
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) testAndRun(w http.ResponseWriter, testID int64) (*db.Test, *db.Run, bool) {
	t, err := s.Store.GetTest(testID)
	if respondErr(w, err, "test") {
		return nil, nil, false
	}
	run, err := s.Store.GetRun(t.RunID)
	if respondErr(w, err, "run") {
		return nil, nil, false
	}
	return t, run, true
}

// afterQuarantine recomputes the status of the run being looked at and answers with it.
func (s *Server) afterQuarantine(w http.ResponseWriter, runID int64, q *db.Quarantine) {
	if err := s.Store.RefreshRun(runID); err != nil {
		serverError(w, err)
		return
	}
	run, err := s.Store.GetRun(runID)
	if err != nil {
		serverError(w, err)
		return
	}
	s.publish("run", runID, 0, map[string]string{"action": "quarantine"})
	writeJSON(w, http.StatusOK, map[string]any{"quarantine": q, "run": run})
}
