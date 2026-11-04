package api

import (
	"errors"
	"net/http"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// closeRunByHand closes from the interface a run left RUNNING (the process of the tests was
// killed without calling finish): its unfinished tests become INTERRUPTED with the reason, and
// the run remembers who closed it. Admin only, like every action of the interface. What follows
// a close (run diagnosis, Teams/Slack notice) runs as for any first close.
func (s *Server) closeRunByHand(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	run, err := s.Store.CloseRunByHand(id, s.uiUser(r), s.redactor().Text(in.Reason))
	if errors.Is(err, db.ErrRunClosed) {
		writeError(w, http.StatusConflict, "the run is already closed")
		return
	}
	if respondErr(w, err, "run") {
		return
	}
	s.runClosed(id, true)
	writeJSON(w, http.StatusOK, run)
}
