package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/lab"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// FixLab: reenviar una llamada capturada (ver docs/rfc/fixlab.md). Hace que el servidor envíe
// peticiones HTTP, así que sigue la regla de las acciones de la UI (guardUIWrite) y, salvo en la
// máquina del dev, solo llega a los hosts del test y a los de TRACEREPORTS_LAB_HOSTS.

// LabConfig configures the Request Lab.
type LabConfig struct {
	// Disabled (TRACEREPORTS_LAB=off) turns the Lab off.
	Disabled bool
	// Hosts (TRACEREPORTS_LAB_HOSTS) are extra hosts a shared server may send to: "host",
	// "host:port" or "*.domain".
	Hosts []string
}

// ParseLabHosts splits TRACEREPORTS_LAB_HOSTS (comma separated).
func ParseLabHosts(s string) []string { return lab.ParseHosts(s) }

// labPlan returns the plan to resend one captured call, and whether this person may send it.
func (s *Server) labPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "conn_id")
	if !ok {
		return
	}
	plan, ok := s.planFor(w, id)
	if !ok {
		return
	}
	canSend, reason := s.uiAccess(r)
	if s.Lab.Disabled {
		canSend, reason = false, "disabled"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan": plan, "can_send": canSend, "reason": reason,
		// en la máquina del dev se puede apuntar a cualquier host; si no, a estos
		"any_host": s.labLocal(r), "lab_hosts": s.Lab.Hosts,
	})
}

func (s *Server) planFor(w http.ResponseWriter, connID int64) (*lab.Plan, bool) {
	c, err := s.Store.GetNetConn(connID)
	if respondErr(w, err, "network call") {
		return nil, false
	}
	conns, err := s.Store.ListNetwork(c.TestID)
	if err != nil {
		serverError(w, err)
		return nil, false
	}
	plan, err := lab.Prepare(conns, connID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return nil, false
	}
	return plan, true
}

// labLocal reports whether the request comes from the server's own machine (no proxy): the server
// is the dev's, so the Lab may call any host, like the dev's own terminal.
func (s *Server) labLocal(r *http.Request) bool { return fromLoopback(r) && !proxied(r) }

// labAllow builds the host rule of one send.
func (s *Server) labAllow(r *http.Request, plan *lab.Plan) func(*url.URL) error {
	if s.labLocal(r) {
		return nil // el transporte igual bloquea direcciones link-local
	}
	return lab.HostRule(plan, s.Lab.Hosts)
}

// labSend resends a captured call: {"conn_id", "values", "base_url", "request"}.
func (s *Server) labSend(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	if s.Lab.Disabled {
		writeError(w, http.StatusForbidden, "the Lab is disabled (TRACEREPORTS_LAB=off)")
		return
	}
	var in struct {
		ConnID  int64             `json:"conn_id"`
		Values  map[string]string `json:"values"`
		BaseURL string            `json:"base_url"`
		Request *lab.Request      `json:"request"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	plan, ok := s.planFor(w, in.ConnID)
	if !ok {
		return
	}
	res, err := lab.Run(r.Context(), plan, lab.Options{Values: in.Values, BaseURL: in.BaseURL, Target: in.Request, Allow: s.labAllow(r, plan)})
	var missing *lab.ErrMissing
	switch {
	case errors.As(err, &missing):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "missing": missing.Names})
		return
	case errors.Is(err, lab.ErrBlocked):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// lo que vuelve a la pantalla pasa por la misma política que la evidencia guardada: un token
	// nuevo, o un secreto escrito a mano en la petición editada, no queda a la vista
	red := s.redactor()
	res.Response.Headers = red.Headers(res.Response.Headers)
	res.Response.Body = red.Text(res.Response.Body)
	res.Response.Error = red.Text(res.Response.Error)
	for i := range res.Steps {
		st := &res.Steps[i]
		st.URL, st.Location, st.Error = red.Text(st.URL), red.Text(st.Location), red.Text(st.Error)
	}
	// res.Sent es la plantilla con sus {{variables}}: nunca lleva los valores, y enmascararla
	// borraría las variables del cURL. Un servidor que devuelve lo recibido (eco) no muestra los
	// valores escritos
	for _, v := range in.Values {
		if len(v) >= 4 {
			res.Response.Body = strings.ReplaceAll(res.Response.Body, v, redact.Mask)
		}
	}
	writeJSON(w, http.StatusOK, res)
}
