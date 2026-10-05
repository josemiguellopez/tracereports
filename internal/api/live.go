package api

import (
	"encoding/json"
	"net/http"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/live"
	"github.com/josemiguellopez/tracereports/internal/locator"
)

const (
	maxDOMBody     = 4 << 20 // tamaño máximo del snapshot de la página
	maxDOMElements = 2000
)

// publish sends a live event (SSE) for a run.
func (s *Server) publish(kind string, runID, testID int64, data any) {
	s.Live.Publish(live.Event{Type: kind, RunID: runID, TestID: testID, Data: data})
}

// publishTest resolves the run of a test and publishes the event.
func (s *Server) publishTest(kind string, testID int64, data any) {
	if s.Live == nil {
		return
	}
	if runID, err := s.Store.TestRunID(testID); err == nil {
		s.publish(kind, runID, testID, data)
	}
}

// addDOM stores the page snapshot taken when the test failed (input of the locator recommender).
func (s *Server) addDOM(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDOMBody)
	var snap locator.Snapshot
	if !decodeLimited(w, r, &snap) {
		return
	}
	if len(snap.Elements) > maxDOMElements {
		snap.Elements = snap.Elements[:maxDOMElements]
	}
	p := s.redactor()
	snap.URL, snap.Title = p.Text(snap.URL), p.Text(snap.Title)
	for i := range snap.Elements {
		e := &snap.Elements[i]
		e.Text, e.Label, e.Placeholder = p.Text(truncate(e.Text, 120)), truncate(e.Label, 120), truncate(e.Placeholder, 120)
	}
	raw, _ := json.Marshal(snap)
	if err := s.Store.SaveDOM(id, string(raw)); respondErr(w, err, "test") {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int{"elements": len(snap.Elements)})
}

// LocatorReport is what the UI needs to show the locator recommender of a failed test.
type LocatorReport struct {
	FailedSelector string               `json:"failed_selector"`
	Suggestions    []locator.Suggestion `json:"suggestions"`
	AIPick         string               `json:"ai_pick,omitempty"`
	AIReason       string               `json:"ai_reason,omitempty"`
	StepLogID      int64                `json:"step_log_id,omitempty"` // paso donde falló
	Screenshot     string               `json:"screenshot,omitempty"`  // captura del fallo (para el bounding box)
	Viewport       locator.Size         `json:"viewport"`
	PageURL        string               `json:"page_url,omitempty"`
}

func (s *Server) locatorReport(t *db.Test) (*LocatorReport, error) {
	cands, failed, err := ai.LocatorCandidates(s.Store, t)
	if err != nil || (failed == "" && len(cands) == 0) {
		return nil, err
	}
	rep := &LocatorReport{FailedSelector: failed, Suggestions: cands}
	if rep.Suggestions == nil {
		rep.Suggestions = []locator.Suggestion{}
	}
	if t.Triage != nil {
		rep.AIPick, rep.AIReason = t.Triage.LocatorPick, t.Triage.LocatorReason
	}
	for i := len(t.Logs) - 1; i >= 0; i-- {
		l := t.Logs[i]
		if rep.StepLogID == 0 && l.Status == "FAIL" {
			rep.StepLogID = l.ID
		}
		if rep.Screenshot == "" && l.Screenshot != "" {
			rep.Screenshot = l.Screenshot
		}
	}
	if raw, err := s.Store.GetDOM(t.ID); err == nil && raw != "" {
		var snap locator.Snapshot
		if json.Unmarshal([]byte(raw), &snap) == nil {
			rep.Viewport, rep.PageURL = snap.Viewport, snap.URL
		}
	}
	return rep, nil
}

// testLocator returns the broken selector and the recommended replacements (204 if the
// failure is not a locator problem).
func (s *Server) testLocator(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, err := s.Store.GetTest(id)
	if respondErr(w, err, "test") {
		return
	}
	rep, err := s.locatorReport(t)
	if err != nil {
		serverError(w, err)
		return
	}
	if rep == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// testDrift compares the latency of each endpoint of a test with its history.
func (s *Server) testDrift(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	eps, err := s.Store.TestDrift(id)
	if respondErr(w, err, "test") {
		return
	}
	writeJSON(w, http.StatusOK, eps)
}

// stream is the SSE endpoint (GET /api/v1/stream?run=<id>). Without a hub it answers 404 and
// the UI falls back to polling.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	if s.Live == nil {
		writeError(w, http.StatusNotFound, "live stream disabled")
		return
	}
	s.Live.ServeHTTP(w, r)
}
