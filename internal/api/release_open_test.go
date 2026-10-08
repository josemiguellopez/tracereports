package api

import (
	"encoding/json"
	"fmt"
	"testing"
)

func releaseDecision(t *testing.T, srv *Server, runID int64) (string, map[string]map[string]any) {
	t.Helper()
	rec := call(t, srv, "GET", fmt.Sprintf("/api/v1/runs/%d/release", runID), "")
	if rec.Code != 200 {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Decision string           `json:"decision"`
		Checks   []map[string]any `json:"checks"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	checks := map[string]map[string]any{}
	for _, c := range out.Checks {
		checks[c["id"].(string)] = c
	}
	return out.Decision, checks
}

func TestReleaseEndpointOpenAndClosedRuns(t *testing.T) {
	srv, _ := newTestServer(t)
	post := func(path, body string) map[string]int64 {
		var out map[string]int64
		json.Unmarshal(call(t, srv, "POST", path, body).Body.Bytes(), &out)
		return out
	}
	// recién creada, sin tests
	runID := post("/api/v1/runs", `{"name":"release"}`)["run_id"]
	if d, c := releaseDecision(t, srv, runID); d == "go" || c["complete"]["ok"] != false || c["complete"]["detail"] != "open" {
		t.Fatalf("new empty run: %s %v", d, c["complete"])
	}
	// un test PASS recibido, ninguno corriendo, pero la ejecución sigue abierta
	testID := post(fmt.Sprintf("/api/v1/runs/%d/tests", runID), `{"name":"login"}`)["test_id"]
	call(t, srv, "PATCH", fmt.Sprintf("/api/v1/tests/%d/finish", testID), `{"status":"PASS"}`)
	if d, _ := releaseDecision(t, srv, runID); d == "go" {
		t.Fatalf("open run with all PASS: %s", d)
	}
	// al cerrarla, sí
	call(t, srv, "PATCH", fmt.Sprintf("/api/v1/runs/%d/finish", runID), `{}`)
	if d, c := releaseDecision(t, srv, runID); d != "go" || c["complete"]["ok"] != true || c["evidence"]["ok"] != true {
		t.Fatalf("closed and complete: %s %v", d, c)
	}
	// cerrada sin tests: sin evidencia
	empty := post("/api/v1/runs", `{"name":"vacía"}`)["run_id"]
	call(t, srv, "PATCH", fmt.Sprintf("/api/v1/runs/%d/finish", empty), `{}`)
	if d, c := releaseDecision(t, srv, empty); d != "no_go" || c["evidence"]["ok"] != false {
		t.Fatalf("closed empty run: %s %v", d, c["evidence"])
	}
}
