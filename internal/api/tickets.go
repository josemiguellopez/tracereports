package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/correlate"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

var ticketLabels = map[string]map[string]string{
	"es": {"error": "Error", "network": "Llamadas al backend que fallaron", "test": "Test", "run": "Ejecución", "logs": "logs", "trace": "traza",
		"footer":  "Creado desde TraceReports con la evidencia del reporte.",
		"context": "Contexto técnico", "stack": "Stack trace", "repro": "Reproducir en local", "response": "Respuesta",
		"console": "Consola del navegador", "baseline": "La última vez que el test pasó (ejecución #%d) respondió HTTP %d", "artifacts": "Trace y video",
		"attempts": "Intentos", "key": "Identidad del test"},
	"en": {"error": "Error", "network": "Backend calls that failed", "test": "Test", "run": "Run", "logs": "logs", "trace": "trace",
		"footer":  "Created from TraceReports with the report evidence.",
		"context": "Technical context", "stack": "Stack trace", "repro": "Run it locally", "response": "Response",
		"console": "Browser console", "baseline": "The last time the test passed (run #%d) it answered HTTP %d", "artifacts": "Trace and video",
		"attempts": "Attempts", "key": "Test identity"},
}

// trackerByID returns a configured tracker.
func (s *Server) trackerByID(id string) tracker.Provider {
	for _, p := range s.Trackers {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// createTicket opens a ticket in a tracker from a failure (a test, or the whole run), written
// from its escalation summary (for developers by default). If the same failure already has a
// ticket in that tracker (the same test in an earlier run, e.g. last night's), it returns that
// one instead of a duplicate, unless force.
func (s *Server) createTicket(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		escalationRef
		Provider string `json:"provider"`
		NoAI     bool   `json:"no_ai"`
		Force    bool   `json:"force"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Audience == "" {
		in.Audience = "dev"
	}
	if err := in.valid(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := s.trackerByID(in.Provider)
	if p == nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("tracker %q is not configured (see TRACEREPORTS_GITHUB_*, TRACEREPORTS_JIRA_* or TRACEREPORTS_AZURE_*)", in.Provider))
		return
	}
	testKey := ""
	run, err := s.Store.GetRun(in.RunID)
	if respondErr(w, err, "run") {
		return
	}
	if in.TestID != 0 {
		t, err := s.Store.GetTest(in.TestID)
		if respondErr(w, err, "test") {
			return
		}
		if t.RunID != in.RunID {
			writeError(w, http.StatusBadRequest, "the test does not belong to that run")
			return
		}
		testKey = t.Key
	}
	if !in.Force {
		existing, err := s.Store.ExistingTicket(in.RunID, in.TestID, testKey, run.Project, p.ID(), p.Target())
		if err != nil {
			serverError(w, err)
			return
		}
		if existing != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ticket": existing, "existing": true})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	e, err := s.escalationFor(ctx, in.escalationRef, in.NoAI)
	if respondErr(w, err, "run or test") {
		return
	}
	created, err := p.Create(ctx, s.issueFrom(e))
	if err != nil {
		writeError(w, http.StatusBadGateway, p.Name()+": "+err.Error())
		return
	}
	t := &db.Ticket{RunID: in.RunID, TestID: in.TestID, TestKey: testKey, Project: run.Project, Target: p.Target(),
		Provider: p.ID(), Key: created.Key, URL: created.URL}
	if err := s.Store.SaveTicket(t); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": t, "existing": false})
}

// listTickets returns the tickets of a run's failures (including those opened from earlier runs
// of the same tests).
func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	ts, err := s.Store.TicketsOfRun(id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

// issueFrom turns an escalation summary into a ticket: its sections, the evidence, the failed
// backend calls, the error, the report link (with PUBLIC_URL) and the failure screenshot.
func (s *Server) issueFrom(e *ai.Escalation) *tracker.Issue {
	l, tl := escalationLabels[e.Lang], ticketLabels[e.Lang]
	if l == nil {
		l, tl = escalationLabels["es"], ticketLabels["es"]
	}
	is := &tracker.Issue{
		Title:    e.Title,
		Headline: e.Headline,
		Sections: [][2]string{{l["what"], e.WhatHappened}, {l["impact"], e.Impact}, {l["cause"], e.RootCause}, {l["owner"], e.Owner}},
		Lists:    []tracker.List{{Title: l["evidence"], Items: e.Evidence}, {Title: l["next"], Items: e.NextSteps}},
		LinkText: l["link"],
		Footer:   tl["footer"],
	}
	f := e.Facts
	if is.Title == "" {
		is.Title = f.TestName
	}
	var calls []string
	for _, n := range f.Network {
		c := fmt.Sprintf("%s %s%s → %s", n.Method, n.Host, n.Path, n.Outcome)
		if n.DurationMs > 0 {
			c += fmt.Sprintf(" (%d ms)", n.DurationMs)
		}
		if n.TraceID != "" {
			c += " · trace " + n.TraceID
		}
		if n.RequestID != "" {
			c += " · request " + n.RequestID
		}
		call := correlate.Call{IDs: correlate.IDs{TraceID: n.TraceID, RequestID: n.RequestID}, Method: n.Method,
			URL: "https://" + n.Host + n.Path, Status: n.Status, StartedAt: n.StartedAt, Duration: n.DurationMs}
		for _, l := range []struct{ label, url string }{{tl["logs"], correlate.Link(s.LogsURL, call)}, {tl["trace"], correlate.Link(s.TraceURL, call)}} {
			if l.url != "" {
				c += " · " + l.label + ": " + l.url
			}
		}
		calls = append(calls, c)
	}
	if len(calls) > 0 {
		is.Lists = append(is.Lists, tracker.List{Title: tl["network"], Items: calls})
	}
	if f.TestName != "" {
		is.Sections = append([][2]string{{tl["test"], f.TestName}}, is.Sections...)
	} else if f.RunName != "" {
		is.Sections = append([][2]string{{tl["run"], f.RunName}}, is.Sections...)
	}
	if f.Error != "" {
		is.Code = [][2]string{{tl["error"], f.Error}}
	}
	if base := s.publicURL(); base != "" && f.ReportPath != "" {
		is.Link = base + "/" + strings.TrimPrefix(f.ReportPath, "/")
	}
	for _, d := range f.Dev {
		devIssue(is, d, tl, s.publicURL(), len(f.Dev) > 1 || f.TestName == "")
	}
	if name := filepath.Base(f.Screenshot); strings.HasPrefix(f.Screenshot, "/screenshots/") && name != "." {
		if img, err := os.ReadFile(filepath.Join(s.ScreenshotsDir, name)); err == nil && len(img) <= maxScreenshotBody {
			is.Image = img
		}
	}
	return is
}

func (s *Server) publicURL() string {
	if s.Notify == nil {
		return ""
	}
	return strings.TrimRight(s.Notify.PublicURL(), "/")
}

// devIssue adds the technical detail of a summary for developers: context, stack trace, the
// command to run the test, each failed call with its cURL and response, and the browser console.
// With named, each title says which test it belongs to (summary of the whole run).
func devIssue(is *tracker.Issue, d *ai.DevFacts, tl map[string]string, base string, named bool) {
	codeStart, listStart := len(is.Code), len(is.Lists)
	defer func() {
		if !named {
			return
		}
		for i := codeStart; i < len(is.Code); i++ {
			is.Code[i][0] = d.TestName + " · " + is.Code[i][0]
		}
		for i := listStart; i < len(is.Lists); i++ {
			is.Lists[i].Title = d.TestName + " · " + is.Lists[i].Title
		}
	}()
	var ctx []string
	for _, kv := range [][2]string{{"Framework", d.Framework}, {"Commit", d.Commit}, {"Branch", d.Branch}, {tl["key"], d.TestKey},
		{"Suite", d.Suite}, {"Params", d.Params}, {"Worker", d.Worker}} {
		if kv[1] != "" {
			ctx = append(ctx, kv[0]+": "+kv[1])
		}
	}
	if d.Attempts > 1 {
		ctx = append(ctx, fmt.Sprintf("%s: %d", tl["attempts"], d.Attempts))
	}
	if len(ctx) > 0 {
		is.Lists = append(is.Lists, tracker.List{Title: tl["context"], Items: ctx})
	}
	if d.ErrorTrace != "" {
		is.Code = append(is.Code, [2]string{tl["stack"], d.ErrorTrace})
	}
	if len(d.Repro) > 0 {
		cmds := make([]string, len(d.Repro))
		for i, c := range d.Repro {
			cmds[i] = c.Cmd
		}
		is.Code = append(is.Code, [2]string{tl["repro"], strings.Join(cmds, "\n")})
	}
	for _, c := range d.Calls {
		title := c.Method + " " + c.URL
		is.Code = append(is.Code, [2]string{"cURL · " + title, c.Curl})
		resp := c.Outcome
		if c.ResponseBody != "" {
			resp += "\n\n" + c.ResponseBody
		}
		if c.Baseline != nil {
			resp += "\n\n" + fmt.Sprintf(tl["baseline"], c.Baseline.RunID, c.Baseline.Status)
		}
		is.Code = append(is.Code, [2]string{tl["response"] + " · " + title, resp})
	}
	if len(d.Console) > 0 {
		lines := make([]string, len(d.Console))
		for i, c := range d.Console {
			lines[i] = "[" + c.Level + "] " + c.Text
			if c.Location != "" {
				lines[i] += "  (" + c.Location + ")"
			}
		}
		is.Code = append(is.Code, [2]string{tl["console"], strings.Join(lines, "\n")})
	}
	if base != "" && len(d.Artifacts) > 0 {
		var items []string
		for _, a := range d.Artifacts {
			items = append(items, a.Kind+": "+base+"/"+strings.TrimPrefix(a.URL, "/"))
		}
		is.Lists = append(is.Lists, tracker.List{Title: tl["artifacts"], Items: items})
	}
}
