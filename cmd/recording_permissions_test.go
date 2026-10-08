package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
)

const fakeEnvKey = "sk-fake-env-key-for-recordings-123456"

// receiver is a server that must never be contacted: it counts requests and whether the fake
// environment key reached it.
func receiver(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Bool) {
	var hits atomic.Int32
	var gotKey atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.Header.Get("Authorization"), fakeEnvKey) {
			gotKey.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &gotKey
}

// recordingWith writes a recording: a run, then the extra events.
func recordingWith(t *testing.T, extra ...string) string {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tracereports-offline.json"), []byte(`{"format":"tracereports-offline","version":1,"id":"perm"}`), 0o644)
	lines := []string{`{"seq":1,"ts":1790000000000,"method":"POST","path":"/api/v1/runs","content_type":"application/json","body":{"name":"perm"},"local_id":-1}`}
	for i, e := range extra {
		lines = append(lines, strings.Replace(e, `"seq":0`, `"seq":`+strconv.Itoa(i+2), 1))
	}
	os.WriteFile(filepath.Join(dir, "events-1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	return dir
}

// Una grabación es evidencia, no instrucciones: un evento administrativo (o cualquier llamada que
// los clientes no graban) invalida la grabación entera. report (con y sin --ai) y push fallan sin
// contactar a nadie, y la key del entorno nunca sale.
func TestRecordingsCannotCarryAdministrativeCalls(t *testing.T) {
	llm, llmHits, gotKey := receiver(t)
	t.Setenv("OPENAI_API_KEY", fakeEnvKey)
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("AI_BASE_URL", llm.URL)
	t.Setenv("GEMINI_API_KEY", "")
	bad := map[string]string{
		"test connection":  `{"seq":0,"ts":1790000000100,"method":"POST","path":"/api/v1/settings/ai/test","content_type":"application/json","body":{"provider":"openai","base_url":"` + llm.URL + `","model":"m"}}`,
		"settings":         `{"seq":0,"ts":1790000000100,"method":"PUT","path":"/api/v1/settings","content_type":"application/json","body":{"language":"en"}}`,
		"ui action":        `{"seq":0,"ts":1790000000100,"method":"POST","path":"/api/v1/ui/escalate","content_type":"application/json","body":{"run_id":-1}}`,
		"delete":           `{"seq":0,"ts":1790000000100,"method":"DELETE","path":"/api/v1/ui/quarantine/1","content_type":"application/json","body":{}}`,
		"encoded path":     `{"seq":0,"ts":1790000000100,"method":"POST","path":"/api/v1/runs/-1/%74ests","content_type":"application/json","body":{"name":"x"}}`,
		"dot segments":     `{"seq":0,"ts":1790000000100,"method":"POST","path":"/api/v1/tests/-1/../../settings/ai/test","content_type":"application/json","body":{}}`,
		"query":            `{"seq":0,"ts":1790000000100,"method":"POST","path":"/api/v1/runs?x=1","content_type":"application/json","body":{"name":"x"}}`,
		"lowercase method": `{"seq":0,"ts":1790000000100,"method":"post","path":"/api/v1/runs","content_type":"application/json","body":{"name":"x"}}`,
		"read":             `{"seq":0,"ts":1790000000100,"method":"GET","path":"/api/v1/runs","content_type":"application/json"}`,
		"import":           `{"seq":0,"ts":1790000000100,"method":"POST","path":"/api/v1/import/junit","content_type":"application/xml"}`,
	}
	// push con una credencial administrativa: el servidor tampoco recibe nada
	store, err := db.Open(filepath.Join(t.TempDir(), "push.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: t.TempDir(), Web: fstest.MapFS{"index.html": {}}, Auth: api.Auth{Token: "admin-token"}}
	var pushHits atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushHits.Add(1)
		srv.Router().ServeHTTP(w, r)
	}))
	defer remote.Close()

	for name, ev := range bad {
		rec := recordingWith(t, ev)
		for _, args := range [][]string{{rec}, {"-ai", rec}} {
			err := runReport(append([]string{"-o", filepath.Join(t.TempDir(), "rep")}, args...))
			if err == nil || !strings.Contains(err.Error(), "not an evidence event") {
				t.Errorf("%s, report %v: %v", name, args[:len(args)-1], err)
			}
		}
		if err := runPush([]string{"-url", remote.URL, "-token", "admin-token", rec}); err == nil {
			t.Errorf("%s, push: accepted", name)
		}
	}
	if llmHits.Load() != 0 || gotKey.Load() || pushHits.Load() != 0 {
		t.Fatalf("contacted: llm=%d key sent=%v push=%d", llmHits.Load(), gotKey.Load(), pushHits.Load())
	}
	if runs, _ := store.ListRuns(10); len(runs) != 0 {
		t.Fatalf("nothing stored: %d runs", len(runs))
	}
}

// Las grabaciones legítimas siguen funcionando: dos workers (dos archivos) que comparten la
// ejecución creada por uno de ellos, con toda la evidencia y el cierre.
func TestLegitimateRecordingWithSharedWorkers(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("AI_PROVIDER", "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tracereports-offline.json"), []byte(`{"format":"tracereports-offline","version":1,"id":"workers"}`), 0o644)
	w1 := strings.Join([]string{
		`{"seq":1,"ts":1790000000000,"method":"POST","path":"/api/v1/runs","content_type":"application/json","body":{"name":"Compartida"},"local_id":-100001}`,
		`{"seq":2,"ts":1790000000100,"method":"POST","path":"/api/v1/runs/-100001/tests","content_type":"application/json","body":{"name":"worker 1"},"local_id":-100002}`,
		`{"seq":3,"ts":1790000000200,"method":"POST","path":"/api/v1/tests/-100002/logs","content_type":"application/json","body":{"status":"INFO","message":"paso w1"}}`,
		`{"seq":4,"ts":1790000000300,"method":"POST","path":"/api/v1/tests/-100002/network","content_type":"application/json","body":{"connections":[{"method":"GET","url":"https://app/api","status":200}]}}`,
		`{"seq":5,"ts":1790000000400,"method":"POST","path":"/api/v1/tests/-100002/console","content_type":"application/json","body":{"entries":[{"level":"error","text":"boom"}]}}`,
		`{"seq":6,"ts":1790000000500,"method":"POST","path":"/api/v1/tests/-100002/dom","content_type":"application/json","body":{"url":"https://app","elements":[]}}`,
		`{"seq":7,"ts":1790000000600,"method":"PATCH","path":"/api/v1/tests/-100002/finish","content_type":"application/json","body":{"status":"PASS"}}`,
		`{"seq":8,"ts":1790000002000,"method":"PATCH","path":"/api/v1/runs/-100001/finish","content_type":"application/json","body":{}}`,
	}, "\n") + "\n"
	w2 := strings.Join([]string{ // otro worker: usa la ejecución del primero
		`{"seq":1,"ts":1790000000150,"method":"POST","path":"/api/v1/runs/-100001/tests","content_type":"application/json","body":{"name":"worker 2"},"local_id":-200001}`,
		`{"seq":2,"ts":1790000000250,"method":"POST","path":"/api/v1/tests/-200001/logs","content_type":"application/json","body":{"status":"FAIL","message":"paso w2"}}`,
		`{"seq":3,"ts":1790000000700,"method":"PATCH","path":"/api/v1/tests/-200001/finish","content_type":"application/json","body":{"status":"FAIL","error_message":"x"}}`,
	}, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "events-w1.jsonl"), []byte(w1), 0o644)
	os.WriteFile(filepath.Join(dir, "events-w2.jsonl"), []byte(w2), 0o644)
	out := filepath.Join(t.TempDir(), "rep")
	if err := runReport([]string{"-o", out, dir}); err != nil {
		t.Fatal(err)
	}
	data := readReport(t, out)
	for _, want := range []string{"Compartida", "worker 1", "worker 2", "paso w1", "paso w2"} {
		if !strings.Contains(data, want) {
			t.Errorf("report misses %q", want)
		}
	}
}

// El servidor temporal de report y pr-comment solo atiende evidencia, importaciones y lecturas:
// Ajustes y acciones de la UI responden 403 aunque se les llame directamente.
func TestReportServerOnlyServesEvidenceAndReads(t *testing.T) {
	ls, err := newLocalServer(false)
	if err != nil {
		t.Fatal(err)
	}
	defer ls.close()
	call := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := ls.target.Doer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/settings/ai/test"}, {"PUT", "/api/v1/settings"}, {"GET", "/api/v1/settings"},
		{"POST", "/api/v1/ui/escalate"}, {"POST", "/api/v1/ui/tickets"}, {"DELETE", "/api/v1/settings/ai"},
		{"POST", "/api/v1/runs/1/%74ests"},
	} {
		if code := call(c.method, c.path, `{}`); code != http.StatusForbidden {
			t.Errorf("%s %s: %d", c.method, c.path, code)
		}
	}
	if code := call("POST", "/api/v1/runs", `{"name":"ok"}`); code != http.StatusCreated {
		t.Fatalf("evidence still works: %d", code)
	}
	for _, p := range []string{"/api/v1/runs", "/api/v1/runs/1", "/api/v1/runs/1/compare", "/api/v1/config"} {
		if code := call("GET", p, ""); code != http.StatusOK {
			t.Errorf("read %s: %d", p, code)
		}
	}
}
