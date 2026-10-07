package api

import (
	"encoding/json"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/release"
)

func TestRunRelease(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	gate, _ := release.Parse("critical=checkout; min_pass_rate=50")
	srv.ReleaseGate = &gate
	mk := func(pay string) int64 {
		run, _ := srv.Store.CreateRunWithMeta("E2E", "qa", db.RunMeta{Project: "shop"})
		a, _ := srv.Store.CreateTestWithMeta(run, "pagar", "checkout", "", db.TestMeta{Key: "pagar"})
		srv.Store.FinishTest(a, pay, "", "")
		b, _ := srv.Store.CreateTestWithMeta(run, "buscar", "pim", "", db.TestMeta{Key: "buscar"})
		srv.Store.FinishTest(b, "PASS", "", "")
		srv.Store.CloseRun(run, false)
		return run
	}
	green := mk("PASS")
	red := mk("FAIL")

	get := func(id int64) release.Decision {
		var d release.Decision
		rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(id)+"/release", "")
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &d) != nil {
			t.Fatalf("release: %d %s", rec.Code, rec.Body)
		}
		return d
	}
	if d := get(green); d.Decision != release.Go {
		t.Fatalf("green run: %+v", d)
	}
	d := get(red)
	if d.Decision != release.NoGo || d.Gate.Critical[0] != "checkout" {
		t.Fatalf("critical failure: %+v", d)
	}
	var newF *release.Check
	for i := range d.Checks {
		if d.Checks[i].ID == "new_failures" {
			newF = &d.Checks[i]
		}
	}
	if newF == nil || newF.OK || newF.Tests[0] != "pagar" {
		t.Fatalf("new failure against the previous run: %+v", d.Checks)
	}
	if rec := call(t, srv, "GET", "/api/v1/runs/9999/release", ""); rec.Code != 404 {
		t.Fatalf("unknown run: %d", rec.Code)
	}
}
