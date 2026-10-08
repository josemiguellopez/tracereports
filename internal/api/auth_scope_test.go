package api

import (
	"encoding/json"
	"testing"
)

// Reglas actuales, fijadas por compatibilidad: TRACEREPORTS_TOKEN escribe y también administra
// (ajustes, acciones de la UI); sin login de UI, las lecturas no piden token.
func TestCurrentTokenRulesAreKept(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "full-token"}
	remote := fromAddr("10.0.0.9:5000")
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`, remote, header("Authorization", "Bearer full-token")); rec.Code != 200 {
		t.Fatalf("the full token administers settings (compat): %d", rec.Code)
	}
	if rec := call(t, srv, "GET", "/api/v1/runs", "", remote); rec.Code != 200 {
		t.Fatalf("without UI login reads are open (a token alone does not protect them): %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, remote); rec.Code != 401 {
		t.Fatalf("writes need the token: %d", rec.Code)
	}
}

func TestIngestTokenWritesAndReadsButNeverAdministers(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "full-token", IngestToken: "ci-token", UIUser: "qa", UIPass: "pw"}
	ci := header("Authorization", "Bearer ci-token")
	remote := fromAddr("10.0.0.9:5000")

	rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"ci"}`, remote, ci)
	if rec.Code != 201 {
		t.Fatalf("ingest token creates runs: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, "GET", "/api/v1/runs", "", remote, ci); rec.Code != 200 {
		t.Fatalf("ingest token reads even with UI login: %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`, remote, ci); rec.Code != 403 {
		t.Fatalf("ingest token must not change settings: %d", rec.Code)
	}
	// ni desde el mismo equipo
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`, ci); rec.Code != 403 {
		t.Fatalf("not from loopback either: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/quarantine", `{"test_id":1,"reason":"x"}`, remote, ci); rec.Code != 403 {
		t.Fatalf("ingest token must not use UI actions: %d %s", rec.Code, rec.Body)
	}
	// el token completo sigue igual
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`, remote, header("Authorization", "Bearer full-token")); rec.Code != 200 {
		t.Fatalf("full token unchanged: %d", rec.Code)
	}
	// uno equivocado no entra
	if rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, remote, header("X-TraceReports-Token", "nope"), basicAuth("qa", "pw")); rec.Code != 401 {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	var chk map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/auth/check", "", remote, header("X-TraceReports-Token", "ci-token"), basicAuth("qa", "pw")).Body.Bytes(), &chk)
	if chk["token_valid"] != true || chk["token_scope"] != "ingest" {
		t.Fatalf("check reports the scope: %v", chk)
	}
}

// Solo con el token de ingesta (sin TRACEREPORTS_TOKEN): las escrituras lo exigen y el mismo equipo
// ya no administra sin credenciales, como con el token completo.
func TestIngestTokenAloneMakesTheServerDeployed(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{IngestToken: "ci-token"}
	if rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`); rec.Code != 401 {
		t.Fatalf("writes need a token: %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`); rec.Code != 403 {
		t.Fatalf("loopback no longer administers without credentials: %d", rec.Code)
	}
	srv.Auth.LocalAdmin = true
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`); rec.Code != 200 {
		t.Fatalf("TRACEREPORTS_LOCAL_ADMIN keeps working: %d", rec.Code)
	}
}
