package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/owners"
)

func verdict(t *testing.T, srv *Server, testID int64, body map[string]any, want int) db.Verdict {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(testID)+"/verdict", string(raw))
	if rec.Code != want {
		t.Fatalf("verdict %v: %d %s", body, rec.Code, rec.Body)
	}
	var v db.Verdict
	json.Unmarshal(rec.Body.Bytes(), &v)
	return v
}

func runDetail(t *testing.T, srv *Server, runID int64) map[string]db.Test {
	t.Helper()
	var d db.RunDetail
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(runID), "").Body.Bytes(), &d)
	out := map[string]db.Test{}
	for _, tt := range d.Tests {
		out[tt.Name] = tt
	}
	return out
}

func TestOwnersAndVerdictsAcrossRuns(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	rules, err := owners.Parse("* @qa\n*::test_checkout Equipo pagos")
	if err != nil {
		t.Fatal(err)
	}
	srv.Owners = rules
	run1, fail1 := runWith(t, srv, "shop")

	tests := runDetail(t, srv, run1)
	if tests["checkout"].Owner != "Equipo pagos" || tests["login"].Owner != "@qa" {
		t.Fatalf("owners in the run: %q %q", tests["checkout"].Owner, tests["login"].Owner)
	}

	v := verdict(t, srv, fail1, map[string]any{"verdict": "product_bug", "comment": "el pago devuelve 500 (token=SECRETO1)", "author": "Ana"}, 201)
	if v.Verdict != "product_bug" || v.Author != "Ana" || strings.Contains(v.Comment, "SECRETO1") {
		t.Fatalf("verdict (comment masked): %+v", v)
	}
	var one db.Test
	json.Unmarshal(call(t, srv, "GET", "/api/v1/tests/"+itoa(fail1), "").Body.Bytes(), &one)
	if one.Verdict == nil || one.Verdict.Verdict != "product_bug" || one.Owner != "Equipo pagos" || one.PreviousVerdict != nil {
		t.Fatalf("test detail: %+v", one)
	}
	// un veredicto más reciente reemplaza al anterior
	verdict(t, srv, fail1, map[string]any{"verdict": "environment", "comment": "era el ambiente"}, 201)

	// la ejecución siguiente del mismo test lo recuerda; otro proyecto no
	run2, fail2 := runWith(t, srv, "shop")
	next := runDetail(t, srv, run2)["checkout"]
	if next.Verdict != nil || next.PreviousVerdict == nil || next.PreviousVerdict.Verdict != "environment" || next.PreviousVerdict.RunID != run1 {
		t.Fatalf("remembered in the next run: %+v / %+v", next.Verdict, next.PreviousVerdict)
	}
	other, _ := runWith(t, srv, "billing")
	if runDetail(t, srv, other)["checkout"].PreviousVerdict != nil {
		t.Fatal("verdicts do not cross projects")
	}
	_ = fail2

	// el dueño de las reglas llega a escalar (y por ahí a los tickets)
	body, _ := json.Marshal(map[string]any{"run_id": run2, "test_id": fail2, "audience": "dev", "lang": "es"})
	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(body)).Body.Bytes(), &e)
	if e.Owner != "Equipo pagos" {
		t.Fatalf("escalation owner: %q", e.Owner)
	}
	body, _ = json.Marshal(map[string]any{"run_id": run2, "audience": "qa", "lang": "es"})
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(body)).Body.Bytes(), &e)
	if e.Owner != "Equipo pagos" { // la ejecución: los dueños de sus fallos
		t.Fatalf("run escalation owner: %q", e.Owner)
	}
}

func TestVerdictValidationAndAccess(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	_, fail := runWith(t, srv, "shop")
	verdict(t, srv, fail, map[string]any{"verdict": "whatever"}, 400)
	verdict(t, srv, 99999, map[string]any{"verdict": "data"}, 404)
	raw, _ := json.Marshal(map[string]any{"verdict": "data"})
	if rec := call(t, srv, "POST", "/api/v1/ui/tests/"+itoa(fail)+"/verdict", string(raw), fromAddr("10.0.0.9:1")); rec.Code != 403 {
		t.Fatalf("remote without login: %d", rec.Code)
	}
	// sin reglas de dueños no hay dueño (y nada se rompe)
	if o := runDetail(t, srv, 1); len(o) > 0 {
		for _, tt := range o {
			if tt.Owner != "" {
				t.Fatalf("no rules, no owner: %+v", tt)
			}
		}
	}
}
