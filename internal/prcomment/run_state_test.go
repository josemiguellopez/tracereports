package prcomment

import (
	"strings"
	"testing"
)

// Sin fallos no es "todo en verde": una ejecución en curso (o con tests sin terminar, cuando la
// espera del CLI vence), incompleta, con advertencias, sin tests o con todo omitido se publica con
// su estado. Los contadores de la tabla no cambian.
func TestMarkdownFollowsTheRunState(t *testing.T) {
	for _, c := range []struct{ raw, heading, table string }{
		{`{"name":"S","status":"RUNNING","total":1}`, "### ⏳ TraceReports · S — run in progress", "| 0 | 0 | 0 | — |"},
		{`{"name":"S","status":"RUNNING","total":3,"passed":2,"running":1}`, "### ⏳ TraceReports · S — run in progress · 1 not finished", "| 2 | 0 | 0 | — |"},
		{`{"name":"S","status":"WARNING","total":2,"passed":2,"incomplete":true,"ended_at":5000}`, "### ⚠️ TraceReports · S — no failures · incomplete run", "| 2 | 0 | 0 |"},
		{`{"name":"S","status":"WARNING","total":3,"passed":2,"warning":1,"ended_at":5000}`, "### ⚠️ TraceReports · S — no failures · 1 with warnings", "| 2 | 0 | 0 |"},
		{`{"name":"S","status":"SKIP","total":2,"skipped":2,"ended_at":5000}`, "### ⚠️ TraceReports · S — every test skipped", "| 0 | 0 | 2 |"},
		{`{"name":"S","status":"PASS","total":0,"ended_at":5000}`, "### ⚠️ TraceReports · S — no tests", "| 0 | 0 | 0 |"},
		{`{"name":"S","status":"PASS","total":3,"passed":2,"skipped":1,"ended_at":5000}`, "### ✅ TraceReports · S — all green", "| 2 | 0 | 1 |"},
		{`{"name":"S","status":"FAIL","total":3,"passed":2,"failed":1,"ended_at":5000}`, "### ❌ TraceReports · S — 1 of 3 failed", "| 2 | 1 | 0 |"},
	} {
		md := Markdown(run(t, c.raw), nil, "en", "")
		if !strings.Contains(md, c.heading+"\n") || !strings.Contains(md, c.table) {
			t.Errorf("%s:\n%s", c.raw, md)
		}
	}
	if md := Markdown(run(t, `{"name":"S","status":"RUNNING","total":1,"running":1}`), nil, "es", ""); !strings.Contains(md, "— ejecución en curso · 1 sin terminar") {
		t.Errorf("es:\n%s", md)
	}
}
