package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/lab"
	"github.com/josemiguellopez/tracereports/internal/repro"
)

// FixLab, fase 3: la cápsula lleva fuera del reporte lo necesario para reproducir un fallo (el
// contexto, cómo correr el test y el plan de cada llamada con sus checks). La leen
// `tracereports repro` y `tracereports verify`. Solo trae evidencia enmascarada: es una lectura más
// del reporte, con la misma regla de acceso.

// capsuleCall builds one call of a capsule with the checks saved for it.
func (s *Server) capsuleCall(connID int64, checks []db.LabCheck) (*lab.CapsuleCall, *db.Test, error) {
	conn, err := s.Store.GetNetConn(connID)
	if err != nil {
		return nil, nil, err
	}
	t, err := s.Store.GetTest(conn.TestID)
	if err != nil {
		return nil, nil, err
	}
	conns, err := s.Store.ListNetwork(conn.TestID)
	if err != nil {
		return nil, nil, err
	}
	plan, err := lab.Prepare(conns, connID)
	if err != nil {
		return nil, nil, err
	}
	call := &lab.CapsuleCall{Plan: plan, Test: lab.CapsuleTest{ID: t.ID, Name: t.Name, Key: t.Key,
		Error: truncate(s.redactor().Text(strings.TrimSpace(strings.SplitN(t.ErrorMessage, "\n", 2)[0])), 500)}}
	for _, c := range checks {
		if c.ConnID != connID {
			continue
		}
		cc := lab.CapsuleCheck{Name: c.Name, BaseURL: c.BaseURL}
		_ = json.Unmarshal(c.Expect, &cc.Expect)
		if req, err := checkRequest(&c); err == nil {
			cc.Request = req
		}
		call.Checks = append(call.Checks, cc)
	}
	return call, t, nil
}

func (s *Server) newCapsule(runID int64) (*lab.Capsule, error) {
	run, err := s.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	c := &lab.Capsule{Format: lab.CapsuleFormat, Version: lab.CapsuleVersion, Calls: []lab.CapsuleCall{},
		Run: lab.CapsuleRun{ID: run.ID, Name: run.Name, Project: run.Project, Environment: run.Environment,
			Branch: run.Branch, Commit: repro.NormalizeCommit(run.Commit), Framework: run.Framework}}
	if base := s.publicURL(); base != "" {
		c.Source = fmt.Sprintf("%s/#run=%d&view=ai", base, run.ID)
	}
	return c, nil
}

// addCommands fills how to run the test locally (from the run's framework and the test key).
func addCommands(c *lab.Capsule, call *lab.CapsuleCall) {
	call.Test.Commands = repro.Commands(c.Run.Framework, call.Test.Key, c.Run.Commit)
}

func writeCapsule(w http.ResponseWriter, name string, c *lab.Capsule) {
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	writeJSON(w, http.StatusOK, c)
}

// connCapsule is the capsule of one captured call (with the checks saved for it).
func (s *Server) connCapsule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "conn_id")
	if !ok {
		return
	}
	conn, err := s.Store.GetNetConn(id)
	if respondErr(w, err, "network call") {
		return
	}
	t, err := s.Store.GetTest(conn.TestID)
	if respondErr(w, err, "test") {
		return
	}
	checks, err := s.Store.LabChecks(t.RunID, "")
	if err != nil {
		serverError(w, err)
		return
	}
	c, err := s.newCapsule(t.RunID)
	if respondErr(w, err, "run") {
		return
	}
	call, _, err := s.capsuleCall(id, checks)
	if err != nil {
		serverError(w, err)
		return
	}
	addCommands(c, call)
	c.Calls = append(c.Calls, *call)
	writeCapsule(w, fmt.Sprintf("tracereports-capsule-call-%d.json", id), c)
}

// incidentCapsule is the capsule of an incident: its causal call and every call with checks.
func (s *Server) incidentCapsule(w http.ResponseWriter, r *http.Request) {
	runID, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	key := r.URL.Query().Get("incident")
	c, err := s.newCapsule(runID)
	if respondErr(w, err, "run") {
		return
	}
	rt, err := s.Store.GetRunTriage(runID)
	if err != nil {
		serverError(w, err)
		return
	}
	var inc *db.Incident
	if rt != nil {
		for i := range rt.Incidents {
			if rt.Incidents[i].Key == key {
				inc = &rt.Incidents[i]
			}
		}
	}
	all, err := s.Store.LabChecks(runID, "")
	if err != nil {
		serverError(w, err)
		return
	}
	// los checks del incidente: guardados con su clave o desde uno de sus tests
	inTests := map[int64]bool{}
	if inc != nil {
		c.Incident, c.IncidentKey = inc.Title, inc.Key
		for _, id := range inc.TestIDs {
			inTests[id] = true
		}
	}
	var checks []db.LabCheck
	var conns []int64
	seen := map[int64]bool{}
	add := func(id int64) {
		if !seen[id] {
			seen[id] = true
			conns = append(conns, id)
		}
	}
	if inc != nil {
		for _, ev := range inc.Evidence {
			if ev.Kind == "network" && ev.Ref != 0 {
				add(ev.Ref)
			}
		}
	}
	for _, ch := range all {
		if (key != "" && ch.IncidentKey == key) || inTests[ch.TestID] {
			checks = append(checks, ch)
			add(ch.ConnID)
		}
	}
	if len(conns) == 0 {
		writeError(w, http.StatusNotFound, "this incident has no call to resend")
		return
	}
	for _, id := range conns {
		call, _, err := s.capsuleCall(id, checks)
		if err != nil {
			continue // una llamada borrada no impide la cápsula
		}
		addCommands(c, call)
		c.Calls = append(c.Calls, *call)
	}
	writeCapsule(w, fmt.Sprintf("tracereports-capsule-run-%d.json", runID), c)
}
