package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

// secrets sent straight to the REST API (no client-side masking) must not reach the database,
// the AI prompts, the exported ZIP or the Teams/Slack messages.
var testSecrets = []string{"hunter2", "SEKRET-QUERY", "SEKRET-BODY", "SEKRETBEARER123", "SEKRET-COOKIE", "SEKRET-ARR", "SEKRET-OBJ", "SEKRET-ENC", "SEKRET-META"}

func TestSecretsAreMaskedEverywhere(t *testing.T) {
	clearAIEnv(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var mu sync.Mutex
	var prompts []string
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		prompts = append(prompts, string(b))
		mu.Unlock()
		w.Write([]byte(`{"message":{"content":"{\"category\":\"BACKEND_TIMEOUT\",\"summary\":\"s\",\"suggestion\":\"x\",\"headline\":\"h\",\"summary\":\"s\",\"incidents\":[]}"}}`))
	}))
	defer llm.Close()
	analyzer := ai.New(store)
	analyzer.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})
	srv := &Server{Store: store, AI: analyzer, ScreenshotsDir: dir, Web: fstest.MapFS{
		"index.html": {Data: []byte(`<html><link rel="stylesheet" href="vendor/fonts/fonts.css"><!-- tracereports:static-data --></html>`)},
		"app.js":     {Data: []byte("//")}, "vendor/chart.umd.min.js": {Data: []byte("// chart")},
		"vendor/fonts/fonts.css": {Data: []byte("@font-face{}")}, "vendor/fonts/a.woff2": {Data: []byte("wOF2")},
	}}

	var run struct {
		RunID int64 `json:"run_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"Pagos password=SEKRET-META","environment":"qa","project":"shop","branch":"main","commit":"abc123"}`).Body.Bytes(), &run)
	var test struct {
		TestID int64 `json:"test_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs/"+itoa(run.RunID)+"/tests",
		`{"name":"Pagar","key":"tests/test_pay.py::test_pay[visa]","suite":"tests/test_pay.py","params":"visa"}`).Body.Bytes(), &test)
	tid := itoa(test.TestID)
	// metadatos visibles e identidad con un secreto (parámetro de pytest, nombre, suite, categoría)
	var meta struct {
		TestID int64 `json:"test_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs/"+itoa(run.RunID)+"/tests",
		`{"name":"Login password=SEKRET-META","key":"tests/test_login.py::test_login[token=SEKRET-META]","suite":"suite?token=SEKRET-META","category":"auth secret=SEKRET-META"}`).Body.Bytes(), &meta)
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(meta.TestID)+"/finish", `{"status":"FAIL","error_message":"boom"}`)
	call(t, srv, "POST", "/api/v1/tests/"+tid+"/logs", `{"status":"INFO","message":"login con password=hunter2 y {\"token\":[\"SEKRET-ARR\"]}"}`)
	net := `{"connections":[{"method":"POST","url":"https://pay.test/api/charge?token=SEKRET-QUERY&%74oken=SEKRET-ENC&amount=10","status":503,
		"request_headers":{"Authorization":"Bearer SEKRETBEARER123","Cookie":"sid=SEKRET-COOKIE","Accept":"application/json"},
		"post_data":"{\"card\":\"visa\",\"password\":{\"value\":\"SEKRET-OBJ\"}}","response_body":"{\"error\":\"down\",\"access_token\":\"SEKRET-BODY\",\"token\":[\"SEKRET-ARR\"]}"}]}`
	call(t, srv, "POST", "/api/v1/tests/"+tid+"/network", net)
	call(t, srv, "PATCH", "/api/v1/tests/"+tid+"/finish", `{"status":"FAIL","error_message":"HTTPError: 503 with Authorization: Bearer SEKRETBEARER123","attempts":2}`)
	if rec := call(t, srv, "PATCH", "/api/v1/runs/"+itoa(run.RunID)+"/finish", `{"interrupted":true}`); rec.Code != 200 {
		t.Fatalf("finish run: %d", rec.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	analyzer.Wait(ctx)

	check := func(where, s string) {
		t.Helper()
		for _, secret := range testSecrets {
			if strings.Contains(s, secret) {
				t.Errorf("%s contains the secret %q", where, secret)
			}
		}
	}
	// base de datos: el archivo mismo (y su WAL)
	for _, f := range []string{dbPath, dbPath + "-wal"} {
		if b, err := os.ReadFile(f); err == nil {
			check(filepath.Base(f), string(b))
		}
	}
	// lo que la API devuelve sigue siendo útil
	var got db.Test
	json.Unmarshal(call(t, srv, "GET", "/api/v1/tests/"+tid, "").Body.Bytes(), &got)
	var conns []db.NetConn
	json.Unmarshal(call(t, srv, "GET", "/api/v1/tests/"+tid+"/network", "").Body.Bytes(), &conns)
	if len(conns) != 1 || conns[0].RequestHeaders["Accept"] != "application/json" || !strings.Contains(conns[0].URL, "amount=10") ||
		!strings.Contains(conns[0].ResponseBody, `"error":"down"`) || conns[0].RequestHeaders["Authorization"] != "<masked>" {
		t.Fatalf("innocuous fields must stay: %+v", conns)
	}
	if got.Key != "tests/test_pay.py::test_pay[visa]" || got.Suite != "tests/test_pay.py" || got.Attempts != 2 || got.KeyApprox {
		t.Fatalf("identity through the API: %+v", got.TestMeta)
	}
	var r db.Run
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(run.RunID), "").Body.Bytes(), &r)
	if r.Project != "shop" || r.Branch != "main" || r.Commit != "abc123" || !r.Incomplete {
		t.Fatalf("run context through the API: %+v", r)
	}
	if rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(run.RunID)+"/baselines", ""); rec.Code != 200 {
		t.Fatalf("baselines: %d", rec.Code)
	}

	// prompts enviados a la IA
	mu.Lock()
	if len(prompts) == 0 {
		t.Fatal("the AI was not called")
	}
	for _, p := range prompts {
		check("AI prompt", p)
	}
	mu.Unlock()

	// ZIP exportado: sin secretos y sin dependencias externas
	rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(run.RunID)+"/export", "")
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		check("zip "+f.Name, string(b))
		names[f.Name[strings.Index(f.Name, "/")+1:]] = true
	}
	for _, want := range []string{"vendor/chart.umd.min.js", "vendor/fonts/fonts.css", "vendor/fonts/a.woff2"} {
		if !names[want] {
			t.Errorf("the ZIP must include %s to work offline", want)
		}
	}

	// mensaje de Slack (escalamiento)
	var slack []byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { slack, _ = io.ReadAll(r.Body) }))
	defer hook.Close()
	t.Setenv("SLACK_WEBHOOK_URL", hook.URL)
	srv.Notify = notify.New(store)
	body, _ := json.Marshal(map[string]any{"run_id": run.RunID, "test_id": test.TestID, "audience": "dev", "lang": "es", "channel": "slack", "no_ai": true})
	if rec := call(t, srv, "POST", "/api/v1/ui/escalate/send", string(body)); rec.Code != 200 || len(slack) == 0 {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	check("slack message", string(slack))
}

func TestIdempotentWritesAreNotDuplicated(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	path := "/api/v1/tests/" + itoa(testID) + "/logs"
	key := header("Idempotency-Key", "evt-123")
	first := call(t, srv, "POST", path, `{"status":"INFO","message":"paso 1"}`, key)
	again := call(t, srv, "POST", path, `{"status":"INFO","message":"paso 1"}`, key)
	if first.Code != 201 || again.Code != 201 || first.Body.String() != again.Body.String() || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry must replay the first response: %d %s / %d %s", first.Code, first.Body, again.Code, again.Body)
	}
	call(t, srv, "POST", path, `{"status":"INFO","message":"paso 2"}`, header("Idempotency-Key", "evt-124"))
	got, _ := srv.Store.GetTest(testID)
	if len(got.Logs) != 2 {
		t.Fatalf("want 2 steps (one per key), got %d", len(got.Logs))
	}
}

func TestIdempotencyLockKeepsExclusion(t *testing.T) {
	var l idemLocks
	var inside, maxInside int32
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := l.lock("k")
			mu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("requests with the same key must never run at the same time (max %d)", maxInside)
	}
	if len(l.busy) != 0 {
		t.Fatalf("entries must be removed when the last user leaves: %d left", len(l.busy))
	}
}

func TestConcurrentRetriesApplyOnce(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	path := "/api/v1/tests/" + itoa(testID) + "/logs"
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = call(t, srv, "POST", path, `{"status":"INFO","message":"paso"}`, header("Idempotency-Key", "same-evt")).Code
		}(i)
	}
	wg.Wait()
	got, _ := srv.Store.GetTest(testID)
	if len(got.Logs) != 1 {
		t.Fatalf("10 concurrent retries must apply the step once, got %d (codes %v)", len(got.Logs), codes)
	}
}

func TestFailedWriteIsRetriedUnderTheLock(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	// la primera respuesta es 5xx (no se guarda): los reintentos vuelven a ejecutar, de a uno
	var calls, running, overlap int32
	var mu sync.Mutex
	h := srv.idempotent(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		running++
		if running > 1 {
			overlap++
		}
		first := calls == 1
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"ok":true}`))
	}))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/x", strings.NewReader("{}"))
			req.Header.Set("Idempotency-Key", "k5xx")
			h.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
	if overlap != 0 || calls != 2 {
		t.Fatalf("after a 5xx, exactly one retry applies and the rest replay it: calls=%d overlap=%d", calls, overlap)
	}
}
