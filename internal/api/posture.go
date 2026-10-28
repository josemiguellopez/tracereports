package api

import "net/http"

// securityPosture answers GET /api/v1/security/posture: the checks of `doctor --security` on the
// configuration this server runs with. Only who administers sees it (it describes the defenses);
// it never carries a credential value, only whether each one is set.
func (s *Server) securityPosture(w http.ResponseWriter, r *http.Request) {
	if ok, reason := s.adminAccess(r); !ok {
		writeError(w, http.StatusForbidden, "the security posture needs TRACEREPORTS_TOKEN or the UI login ("+reason+")")
		return
	}
	if s.Posture == nil {
		writeError(w, http.StatusNotFound, "security posture not available")
		return
	}
	writeJSON(w, http.StatusOK, s.Posture())
}
