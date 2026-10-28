package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Un agente (o un texto que lo engañó) no puede mandar la llamada reproducida, con los secretos de
// TRACEREPORTS_LAB_<NOMBRE>, a un host que el test no llamó.
func TestMCPReproduceStaysOnTheTestHosts(t *testing.T) {
	trURL, _, callID := mcpFixture(t)
	var hits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer evil.Close()
	env := map[string]string{"TRACEREPORTS_URL": trURL, "TRACEREPORTS_LAB_PASSWORD": cliPassword}
	res := agent(t, env,
		fmt.Sprintf(`{"name":"reproduce","arguments":{"call_id":%d,"base_url":%q}}`, callID, evil.URL),
		fmt.Sprintf(`{"name":"verify_fix","arguments":{"run_id":1,"incident_key":"backend:GET /orders 500","base_url":%q}}`, evil.URL),
	)
	for i, r := range res {
		if !strings.Contains(r.Text, "TRACEREPORTS_LAB_HOSTS") {
			t.Errorf("call %d: debe rechazar el host: %s", i+1, r.Text)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("el host ajeno recibió %d llamadas", hits.Load())
	}
	// declarado a propósito en TRACEREPORTS_LAB_HOSTS, sí
	env["TRACEREPORTS_LAB_HOSTS"] = strings.TrimPrefix(evil.URL, "http://")
	agent(t, env, fmt.Sprintf(`{"name":"reproduce","arguments":{"call_id":%d,"base_url":%q}}`, callID, evil.URL))
	if hits.Load() == 0 {
		t.Fatal("un host de TRACEREPORTS_LAB_HOSTS debe poder usarse")
	}
}

// El agente y la CLI de Lab leen con la credencial más chica que haya.
func TestReadCredentialsPreferLeastPrivilege(t *testing.T) {
	env := map[string]string{"TRACEREPORTS_TOKEN": "admin", "TRACEREPORTS_INGEST_TOKEN": "ingest",
		"TRACEREPORTS_UI_USER": "admin", "TRACEREPORTS_UI_PASSWORD": "a", "TRACEREPORTS_UI_VIEWER_USER": "equipo", "TRACEREPORTS_UI_VIEWER_PASSWORD": "v"}
	req := httptest.NewRequest("GET", "/", nil)
	readCredentials(req, func(k string) string { return env[k] })
	if req.Header.Get("X-TraceReports-Token") != "ingest" {
		t.Fatalf("token: %s", req.Header.Get("X-TraceReports-Token"))
	}
	if u, _, _ := req.BasicAuth(); u != "equipo" {
		t.Fatalf("login: %s", u)
	}
	delete(env, "TRACEREPORTS_INGEST_TOKEN")
	delete(env, "TRACEREPORTS_UI_VIEWER_USER")
	req = httptest.NewRequest("GET", "/", nil)
	readCredentials(req, func(k string) string { return env[k] })
	if u, _, _ := req.BasicAuth(); req.Header.Get("X-TraceReports-Token") != "admin" || u != "admin" {
		t.Fatal("sin las de menor privilegio, usa las que hay")
	}
}
