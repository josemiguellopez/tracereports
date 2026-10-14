package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const labPassword = "s3cret!"

// labBackend is the app under test: POST /login gives a token, GET /orders needs it and fails
// with 500 while broken is set.
func labBackend(t *testing.T) (*httptest.Server, *atomic.Bool, *atomic.Int32) {
	var broken atomic.Bool
	var hits atomic.Int32
	broken.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), labPassword) {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"accessToken":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.fresh-signature"}`)
		case "/orders":
			if r.Header.Get("Authorization") != "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.fresh-signature" {
				w.WriteHeader(401)
				return
			}
			if broken.Load() {
				w.WriteHeader(500)
				fmt.Fprint(w, `{"error":"pool exhausted"}`)
				return
			}
			fmt.Fprint(w, `{"orders":[1,2]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &broken, &hits
}

// recordLab stores, through the API (so the server masks it), what a test recorded against the
// backend: the login and the call that failed. It returns the failed call's id.
func recordLab(t *testing.T, srv *Server, testID int64, backend string) int64 {
	t.Helper()
	hdr := map[string]string{"date": "Wed, 14 Oct 2026 21:00:00 GMT", "server": "shop"}
	payload, _ := json.Marshal(map[string]any{"connections": []map[string]any{
		{"method": "POST", "url": backend + "/login", "status": 200, "resource_type": "fetch",
			"request_headers": map[string]string{"content-type": "application/json"},
			"post_data":       `{"email":"qa@shop.test","password":"` + labPassword + `"}`,
			"response_body":   `{"accessToken":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.old-signature"}`, "response_headers": hdr},
		{"method": "GET", "url": backend + "/orders", "status": 500, "resource_type": "fetch",
			"request_headers": map[string]string{"authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.old-signature"},
			"response_body":   `{"error":"pool exhausted"}`, "response_headers": hdr},
	}})
	if rec := do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/network", testID), string(payload)); rec.Code != http.StatusCreated {
		t.Fatalf("store network: %d %s", rec.Code, rec.Body)
	}
	conns, _ := srv.Store.ListNetwork(testID)
	if strings.Contains(conns[0].PostData, labPassword) {
		t.Fatal("the stored evidence must be masked")
	}
	return conns[1].ID
}

func TestLabPlanAndSend(t *testing.T) {
	srv, testID := newTestServer(t)
	backend, broken, _ := labBackend(t)
	id := recordLab(t, srv, testID, backend.URL)

	rec := call(t, srv, "GET", fmt.Sprintf("/api/v1/network/%d/lab", id), "")
	var got struct {
		Plan struct {
			Vars  []struct{ Name, Kind string }
			Login *struct {
				TokenPath []string `json:"token_path"`
			}
		}
		CanSend bool `json:"can_send"`
		AnyHost bool `json:"any_host"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	if !got.CanSend || !got.AnyHost || got.Plan.Login == nil || strings.Join(got.Plan.Login.TokenPath, ".") != "accessToken" {
		t.Fatalf("plan = %s", rec.Body)
	}

	send := func(values string) *httptest.ResponseRecorder {
		return call(t, srv, "POST", "/api/v1/ui/lab/send", fmt.Sprintf(`{"conn_id":%d,"values":%s}`, id, values))
	}
	rec = send(`{"password":"` + labPassword + `"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"verdict":"reproduced"`) {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), labPassword) || strings.Contains(rec.Body.String(), "fresh-signature") {
		t.Fatalf("the result must not show the password nor the new token: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `{{password}}`) && !strings.Contains(rec.Body.String(), `{{authorization}}`) {
		t.Fatalf("the sent request keeps its variables: %s", rec.Body)
	}

	broken.Store(false)
	if rec = send(`{"password":"` + labPassword + `"}`); !strings.Contains(rec.Body.String(), `"verdict":"fixed"`) {
		t.Fatalf("after the fix: %s", rec.Body)
	}

	rec = send(`{}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"missing":["password"]`) {
		t.Fatalf("missing value: %d %s", rec.Code, rec.Body)
	}
}

func TestLabSendAccess(t *testing.T) {
	srv, testID := newTestServer(t)
	backend, _, hits := labBackend(t)
	id := recordLab(t, srv, testID, backend.URL)
	body := fmt.Sprintf(`{"conn_id":%d,"values":{"password":%q}}`, id, labPassword)

	// sin login ni token, otra máquina no puede hacer que el servidor envíe peticiones
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", body, fromAddr("10.0.0.5:4000")); rec.Code != 403 {
		t.Fatalf("remote without auth: %d %s", rec.Code, rec.Body)
	}
	// detrás de un proxy, el loopback es el proxy
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", body, header("X-Forwarded-For", "203.0.113.9")); rec.Code != 403 {
		t.Fatalf("proxied: %d", rec.Code)
	}
	// otra página no puede disparar el envío (CSRF)
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", body, header("Origin", "https://evil.example")); rec.Code != 403 {
		t.Fatalf("cross-origin: %d", rec.Code)
	}
	if hits.Load() != 0 {
		t.Fatalf("nothing must reach the backend, got %d requests", hits.Load())
	}

	// servidor compartido con login de la UI: solo los hosts del test y TRACEREPORTS_LAB_HOSTS
	srv.Auth = Auth{UIUser: "qa", UIPass: "pw", Token: "admin-token", IngestToken: "ingest"}
	remote := []reqOpt{fromAddr("10.0.0.5:4000"), basicAuth("qa", "pw")}
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", body, remote...); rec.Code != 200 {
		t.Fatalf("shared server, recorded host: %d %s", rec.Code, rec.Body)
	}
	other := fmt.Sprintf(`{"conn_id":%d,"values":{"password":%q},"base_url":"http://intranet.local:9000"}`, id, labPassword)
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", other, remote...); rec.Code != 403 || !strings.Contains(rec.Body.String(), "TRACEREPORTS_LAB_HOSTS") {
		t.Fatalf("shared server, other host: %d %s", rec.Code, rec.Body)
	}
	srv.Lab.Hosts = ParseLabHosts(" *.local ,staging.example:8443")
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", other, remote...); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"verdict":"network"`) && !strings.Contains(rec.Body.String(), `"verdict":"reproduced"`) {
		// intranet.local no existe: llega a intentarlo (ya no lo bloquea la regla de hosts)
		t.Fatalf("allowed by TRACEREPORTS_LAB_HOSTS: %d %s", rec.Code, rec.Body)
	}
	// el token de ingesta (CI) nunca administra
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", body, fromAddr("10.0.0.5:4000"), header("Authorization", "Bearer ingest")); rec.Code != 403 {
		t.Fatalf("ingest token: %d", rec.Code)
	}

	// TRACEREPORTS_LAB=off
	srv.Lab.Disabled = true
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", body, remote...); rec.Code != 403 {
		t.Fatalf("disabled: %d", rec.Code)
	}
	rec := call(t, srv, "GET", fmt.Sprintf("/api/v1/network/%d/lab", id), "", remote...)
	if !strings.Contains(rec.Body.String(), `"can_send":false`) || !strings.Contains(rec.Body.String(), `"reason":"disabled"`) {
		t.Fatalf("plan when disabled: %s", rec.Body)
	}
}

func TestLabUnknownCall(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := call(t, srv, "GET", "/api/v1/network/999/lab", ""); rec.Code != 404 {
		t.Fatalf("unknown call: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/lab/send", `{"conn_id":999}`); rec.Code != 404 {
		t.Fatalf("unknown call: %d", rec.Code)
	}
}

func TestParseLabHosts(t *testing.T) {
	if got := strings.Join(ParseLabHosts(" A.example , ,*.Corp.local:8080"), "|"); got != "a.example|*.corp.local:8080" {
		t.Fatalf("got %q", got)
	}
}
