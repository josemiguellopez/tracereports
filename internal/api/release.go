package api

import (
	"net/http"

	"github.com/josemiguellopez/tracereports/internal/release"
)

// releaseOf evaluates the release decision of a run (also embedded in the exported report).
func (s *Server) releaseOf(id int64) (*release.Decision, error) {
	d, err := s.Store.GetRunDetail(id)
	if err != nil {
		return nil, err
	}
	var newFailures []string // nil: sin ejecución anterior con qué comparar
	cmp, err := s.Store.CompareRuns(id, 0)
	if err != nil {
		return nil, err
	}
	if cmp.BaseRun != nil {
		newFailures = []string{}
		for _, it := range cmp.NewFailures {
			newFailures = append(newFailures, it.Name)
		}
	}
	gate := release.Default()
	if s.ReleaseGate != nil {
		gate = *s.ReleaseGate
	}
	return release.Evaluate(gate, d, newFailures), nil
}

// runRelease answers "can we ship this?" for a run: go / risk / no_go with the checks behind it
// (TRACEREPORTS_RELEASE_GATE) and the state of each functional area (the tests' tags).
func (s *Server) runRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	d, err := s.releaseOf(id)
	if respondErr(w, err, "run") {
		return
	}
	writeJSON(w, http.StatusOK, d)
}
