package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
	"github.com/josemiguellopez/tracereports/internal/repro"
)

// Límites del detalle técnico: alcanza para diagnosticar sin volver ilegible un ticket.
const (
	devMaxTests     = 5 // resumen de la ejecución: sus primeros fallos
	devMaxCalls     = 5
	devMaxRunCalls  = 3 // por test, en el resumen de la ejecución
	devMaxBody      = 4000
	devMaxReqBody   = 2000
	devMaxTrace     = 6000
	devMaxConsole   = 10
	devMaxConsoleLn = 500
)

// withDev adds the technical detail to a summary for developers: of the chosen test, or of the
// first failed tests when it is about the whole run. It is built on every response from the
// stored evidence (not cached), so it never goes stale.
func (s *Server) withDev(e *ai.Escalation) {
	if e == nil || e.Audience != "dev" {
		return
	}
	ids, calls := []int64{e.TestID}, devMaxCalls
	if e.TestID == 0 {
		ids, calls = nil, devMaxRunCalls
		for _, t := range e.Facts.FailedTests {
			if len(ids) < devMaxTests {
				ids = append(ids, t.ID)
			}
		}
	}
	e.Facts.Dev = nil
	for _, id := range ids {
		d, err := s.devFacts(e.RunID, id, calls)
		if err != nil {
			slog.Warn("escalation: dev detail", "test", id, "err", err)
			continue
		}
		e.Facts.Dev = append(e.Facts.Dev, d)
	}
}

func (s *Server) devFacts(runID, testID int64, maxCalls int) (*ai.DevFacts, error) {
	run, err := s.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	t, err := s.Store.GetTest(testID)
	if err != nil {
		return nil, err
	}
	if t.RunID != runID {
		return nil, db.ErrNotFound
	}
	d := &ai.DevFacts{TestID: t.ID, TestName: t.Name, TestKey: t.Key, Suite: t.Suite, Params: t.Params, Worker: t.Worker, Attempts: t.Attempts,
		Framework: run.Framework, Branch: run.Branch, Commit: run.Commit,
		Error: clipText(t.ErrorMessage, devMaxTrace), ErrorTrace: clipText(t.ErrorTrace, devMaxTrace), Artifacts: t.Artifacts}
	if !t.KeyApprox {
		d.Repro = repro.Commands(run.Framework, t.Key, run.Commit)
	}
	for _, c := range t.Console {
		if (c.Level == "error" || c.Level == "pageerror") && len(d.Console) < devMaxConsole {
			c.Text = clipText(c.Text, devMaxConsoleLn)
			d.Console = append(d.Console, c)
		}
	}
	conns, err := s.Store.ListNetworkErrors(testID, maxCalls)
	if err != nil {
		return nil, err
	}
	s.linkCalls(conns)
	policy := s.redactor()
	for i := range conns {
		c := &conns[i]
		call := ai.DevCall{ID: c.ID, Method: c.Method, URL: c.URL, Status: c.Status, Outcome: fmt.Sprintf("HTTP %d %s", c.Status, c.StatusText),
			StartedAt: c.StartedAt, MimeType: c.MimeType, RequestHeaders: c.RequestHeaders, RequestBody: clipText(strings.ReplaceAll(c.PostData, redact.Mask, repro.Mask), devMaxReqBody),
			ResponseHeaders: c.ResponseHeaders, Curl: repro.Curl(c, policy), TraceID: c.TraceID, RequestID: c.RequestID,
			LogsURL: c.LogsURL, TraceURL: c.TraceURL}
		call.Outcome = strings.TrimSpace(call.Outcome)
		if c.Failed || c.Status == 0 {
			call.Outcome = "net::" + strings.TrimPrefix(c.ErrorText, "net::")
			if c.ErrorText == "" {
				call.Outcome = "sin respuesta"
			}
		}
		if c.DurationMs != nil {
			call.DurationMs = *c.DurationMs
		}
		body := prettyJSON(c.ResponseBody)
		call.ResponseBody = clipText(body, devMaxBody)
		call.BodyClipped = c.BodyTruncated || len(body) > devMaxBody
		if b, err := s.Store.BaselineFor(c.ID); err == nil && b != nil {
			call.Baseline = &ai.DevBaseline{RunID: b.RunID, TestID: b.TestID, Status: b.Conn.Status, SameContext: b.SameContext}
			if b.Conn.DurationMs != nil {
				call.Baseline.DurationMs = *b.Conn.DurationMs
			}
		}
		d.Calls = append(d.Calls, call)
	}
	return d, nil
}

// prettyJSON indents a JSON body so it can be read in a ticket; anything else stays as is.
func prettyJSON(s string) string {
	t := strings.TrimSpace(s)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return s
	}
	var b bytes.Buffer
	if json.Indent(&b, []byte(t), "", "  ") != nil {
		return s
	}
	return b.String()
}

// clipText cuts s to n bytes without splitting a UTF-8 character.
func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n] + "\n…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
