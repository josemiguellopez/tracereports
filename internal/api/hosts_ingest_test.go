package api

import "testing"

// Sin login de UI, un host externo no listado atiende las solicitudes con un token válido (CI):
// el de administración y también el de ingesta, que sigue sin poder administrar.
func TestHostGuardAcceptsBothValidTokens(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Hosts = NewHostPolicy("reportes.lan", "")
	srv.Auth = Auth{Token: "admin-token", IngestToken: "ci-token"}
	ext := host("ci-runner.internal:8080")
	bearer := func(tok string) reqOpt { return header("Authorization", "Bearer "+tok) }
	createRun := func(opts ...reqOpt) int { return call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, opts...).Code }

	for _, tok := range []string{"admin-token", "ci-token"} {
		if c := createRun(ext, bearer(tok)); c != 201 {
			t.Errorf("token %s ingests from an external host: %d", tok, c)
		}
		if c := call(t, srv, "GET", "/api/v1/runs", "", ext, bearer(tok)).Code; c != 200 {
			t.Errorf("token %s reads from an external host: %d", tok, c)
		}
	}
	if c := createRun(ext, bearer("wrong")); c == 201 {
		t.Errorf("invalid token: %d", c)
	}
	if c := call(t, srv, "GET", "/api/v1/runs", "", ext).Code; c != 403 {
		t.Errorf("no credentials from an unknown host: %d", c)
	}
	if c := createRun(host("reportes.lan"), bearer("ci-token")); c != 201 {
		t.Errorf("allowed host: %d", c)
	}
	for _, h := range []reqOpt{ext, host("reportes.lan")} {
		if c := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`, h, bearer("ci-token")).Code; c != 403 {
			t.Errorf("the ingest token still cannot administer: %d", c)
		}
	}
	if c := call(t, srv, "PUT", "/api/v1/settings", `{"language":"en"}`, ext, bearer("admin-token")).Code; c != 200 {
		t.Errorf("the admin token administers: %d", c)
	}
}
