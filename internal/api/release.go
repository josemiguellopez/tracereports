package api

import (
	"net/http"

	"github.com/josemiguellopez/tracereports/internal/release"
)

// runRelease answers "can we ship this?" for a run: go / risk / no_go with the checks behind it
// (TRACEREPORTS_RELEASE_GATE) and the state of each functional area (the tests' tags).
func (s *Server) runRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	d, err := s.Store.GetRunDetail(id)
	if respondErr(w, err, "run") {
		return
	}
	var newFailures []string // nil: sin ejecución anterior con qué comparar
	cmp, err := s.Store.CompareRuns(id, 0)
	if err != nil {
		serverError(w, err)
		return
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
	writeJSON(w, http.StatusOK, release.Evaluate(gate, d, newFailures))
}
