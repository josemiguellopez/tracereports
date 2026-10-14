package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/lab"
)

// FixLab, fase 2: una llamada del Lab se guarda como check (qué respuesta cuenta como buena) y
// "Verificar" corre los checks de un incidente y deja constancia de si quedó resuelto, con el antes
// y el después. Los secretos nunca se guardan: la petición conserva sus {{variables}}.

// labCheckView is a saved check plus the secrets it needs to run (names only).
type labCheckView struct {
	db.LabCheck
	Needs []string `json:"needs"`
}

// checkResult is the outcome of one check in a verification.
type checkResult struct {
	CheckID  int64         `json:"check_id"`
	Name     string        `json:"name"`
	OK       bool          `json:"ok"`
	Failures []lab.Failure `json:"failures,omitempty"`
	Verdict  string        `json:"verdict"`
	Before   string        `json:"before"` // status (o error) en la ejecución
	Status   int           `json:"status"`
	Error    string        `json:"error,omitempty"`
	Body     string        `json:"body,omitempty"` // extracto enmascarado de la respuesta de ahora
}

// incidentOf finds the incident a failed test belongs to in its run's diagnosis: the one whose
// evidence is this very call, else any incident with the test. "" when the run has no diagnosis.
func (s *Server) incidentOf(runID, testID, connID int64) string {
	rt, err := s.Store.GetRunTriage(runID)
	if err != nil || rt == nil {
		return ""
	}
	for _, inc := range rt.Incidents {
		for _, ev := range inc.Evidence {
			if ev.Kind == "network" && ev.Ref == connID {
				return inc.Key
			}
		}
	}
	for _, inc := range rt.Incidents {
		for _, id := range inc.TestIDs {
			if id == testID {
				return inc.Key
			}
		}
	}
	return ""
}

// checkRequest is the request a check sends: the edited one, or the captured call.
func checkRequest(c *db.LabCheck) (*lab.Request, error) {
	if len(c.Request) == 0 {
		return nil, nil
	}
	var r lab.Request
	if err := json.Unmarshal(c.Request, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// labRun returns the saved checks of a run and the last verification of each incident.
func (s *Server) labRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	if _, err := s.Store.GetRun(id); respondErr(w, err, "run") {
		return
	}
	checks, err := s.Store.LabChecks(id, "")
	if err != nil {
		serverError(w, err)
		return
	}
	views := make([]labCheckView, 0, len(checks))
	for i := range checks {
		v := labCheckView{LabCheck: checks[i], Needs: []string{}}
		if conns, err := s.Store.ListNetwork(checks[i].TestID); err == nil {
			if plan, err := lab.Prepare(conns, checks[i].ConnID); err == nil {
				req, _ := checkRequest(&checks[i])
				if n := plan.Needs(req); n != nil {
					v.Needs = n
				}
			}
		}
		views = append(views, v)
	}
	verifs, err := s.Store.LatestLabVerifications(id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": views, "verifications": verifs})
}

// labSaveCheck saves a check: {"conn_id", "name", "request", "base_url", "expect"}.
func (s *Server) labSaveCheck(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		ConnID  int64        `json:"conn_id"`
		Name    string       `json:"name"`
		Request *lab.Request `json:"request"`
		BaseURL string       `json:"base_url"`
		Expect  lab.Expect   `json:"expect"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := in.Expect.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	conn, err := s.Store.GetNetConn(in.ConnID)
	if respondErr(w, err, "network call") {
		return
	}
	t, err := s.Store.GetTest(conn.TestID)
	if respondErr(w, err, "test") {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = conn.Method + " " + shortURL(conn.URL)
	}
	c := db.LabCheck{RunID: t.RunID, TestID: t.ID, ConnID: conn.ID, IncidentKey: s.incidentOf(t.RunID, t.ID, conn.ID),
		Name: truncate(name, 200), BaseURL: truncate(strings.TrimSpace(in.BaseURL), 512)}
	if in.Request != nil {
		// lo escrito a mano en la petición pasa por el enmascarado: un secreto pegado no se guarda
		red := s.redactor()
		in.Request.URL = red.Text(in.Request.URL)
		in.Request.Body = red.Text(in.Request.Body)
		in.Request.Headers = keepVars(red.Headers, in.Request.Headers)
		c.Request, _ = json.Marshal(in.Request)
	}
	c.Expect, _ = json.Marshal(in.Expect)
	if err := s.Store.SaveLabCheck(&c); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

// keepVars masks the headers with the policy but keeps the ones that are only {{variables}}.
func keepVars(mask func(map[string]string) map[string]string, h map[string]string) map[string]string {
	vars := map[string]string{}
	rest := map[string]string{}
	for k, v := range h {
		if t := strings.TrimSpace(v); strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") && strings.Count(t, "{{") == 1 {
			vars[k] = v
		} else {
			rest[k] = v
		}
	}
	rest = mask(rest)
	for k, v := range vars {
		rest[k] = v
	}
	return rest
}

func shortURL(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
		if j := strings.Index(raw, "/"); j >= 0 {
			raw = raw[j:]
		}
	}
	return truncate(raw, 120)
}

func (s *Server) labDeleteCheck(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	id, ok := pathID(w, r, "check_id")
	if !ok {
		return
	}
	if respondErr(w, s.Store.DeleteLabCheck(id), "check") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// labVerify runs the checks of an incident: {"run_id", "incident_key", "check_ids", "values", "base_url"}.
func (s *Server) labVerify(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	if s.Lab.Disabled {
		writeError(w, http.StatusForbidden, "the Lab is disabled (TRACEREPORTS_LAB=off)")
		return
	}
	var in struct {
		RunID       int64             `json:"run_id"`
		IncidentKey string            `json:"incident_key"`
		CheckIDs    []int64           `json:"check_ids"`
		Values      map[string]string `json:"values"`
		BaseURL     string            `json:"base_url"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	all, err := s.Store.LabChecks(in.RunID, "")
	if err != nil {
		serverError(w, err)
		return
	}
	want := map[int64]bool{}
	for _, id := range in.CheckIDs {
		want[id] = true
	}
	var checks []db.LabCheck
	for _, c := range all {
		if want[c.ID] || (len(want) == 0 && c.IncidentKey == in.IncidentKey) {
			checks = append(checks, c)
		}
	}
	if len(checks) == 0 {
		writeError(w, http.StatusNotFound, "no checks to verify")
		return
	}
	if len(checks) > 20 {
		writeError(w, http.StatusBadRequest, "at most 20 checks per verification")
		return
	}

	type prepared struct {
		check  db.LabCheck
		plan   *lab.Plan
		target *lab.Request
	}
	var runs []prepared
	missing := map[string]bool{}
	for _, c := range checks {
		conns, err := s.Store.ListNetwork(c.TestID)
		if err != nil {
			serverError(w, err)
			return
		}
		plan, err := lab.Prepare(conns, c.ConnID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		target, err := checkRequest(&c)
		if err != nil {
			serverError(w, err)
			return
		}
		for _, n := range plan.Needs(target) {
			if in.Values[n] == "" {
				missing[n] = true
			}
		}
		runs = append(runs, prepared{c, plan, target})
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing values: " + strings.Join(names, ", "), "missing": names})
		return
	}

	red := s.redactor()
	results := make([]checkResult, 0, len(runs))
	allOK := true
	for _, p := range runs {
		base := strings.TrimSpace(in.BaseURL)
		if base == "" {
			base = p.check.BaseURL
		}
		cr := checkResult{CheckID: p.check.ID, Name: p.check.Name, Before: beforeOf(p.plan.Original)}
		res, err := lab.Run(r.Context(), p.plan, lab.Options{Values: in.Values, BaseURL: base, Target: p.target, Allow: s.labAllow(r, p.plan)})
		switch {
		case errors.Is(err, lab.ErrBlocked):
			writeError(w, http.StatusForbidden, err.Error())
			return
		case err != nil:
			cr.Error = err.Error()
			cr.Failures = []lab.Failure{{Kind: "network", Got: err.Error()}}
		default:
			var exp lab.Expect
			_ = json.Unmarshal(p.check.Expect, &exp)
			cr.Failures = exp.Eval(res.Response)
			cr.Verdict, cr.Status = res.Verdict, res.Response.Status
			cr.Error = red.Text(res.Response.Error)
			cr.Body = truncate(red.Text(res.Response.Body), 2000)
			for _, v := range in.Values {
				if len(v) >= 4 {
					cr.Body = strings.ReplaceAll(cr.Body, v, "<masked>")
				}
			}
		}
		cr.OK = len(cr.Failures) == 0
		allOK = allOK && cr.OK
		raw, _ := json.Marshal(cr)
		if err := s.Store.LabCheckResult(p.check.ID, cr.OK, raw); err != nil {
			serverError(w, err)
			return
		}
		results = append(results, cr)
	}
	raw, _ := json.Marshal(results)
	v := db.LabVerification{RunID: in.RunID, IncidentKey: in.IncidentKey, OK: allOK, Results: raw, Author: s.uiUser(r)}
	if v.IncidentKey == "" {
		v.IncidentKey = runs[0].check.IncidentKey
	}
	if err := s.Store.SaveLabVerification(&v); err != nil {
		serverError(w, err)
		return
	}
	s.publish("lab", in.RunID, 0, map[string]any{"incident_key": v.IncidentKey, "ok": allOK})
	writeJSON(w, http.StatusOK, v)
}

func beforeOf(o lab.Original) string {
	if o.Failed || o.Status == 0 {
		if o.Error != "" {
			return o.Error
		}
		return "no response"
	}
	return strconv.Itoa(o.Status)
}

// uiUser is the UI login of the request ("" without one).
func (s *Server) uiUser(r *http.Request) string {
	if u, _, ok := r.BasicAuth(); ok && s.Auth.basicOK(r) {
		return u
	}
	return ""
}
