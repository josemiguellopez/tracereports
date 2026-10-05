package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// Regresiones de la auditoría de producción.

func createTestVia(t *testing.T, srv *Server, runID int64, body string) db.Test {
	t.Helper()
	var out struct {
		TestID int64 `json:"test_id"`
	}
	rec := call(t, srv, "POST", "/api/v1/runs/"+itoa(runID)+"/tests", body)
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("create test: %d %s", rec.Code, rec.Body)
	}
	got, err := srv.Store.GetTest(out.TestID)
	if err != nil {
		t.Fatal(err)
	}
	return *got
}

// Un secreto en la identidad se enmascara, pero dos tests que solo difieren en el secreto no
// comparten historial y el mismo test conserva el suyo entre ejecuciones.
func TestSecretIdentityIsMaskedButStable(t *testing.T) {
	srv, _ := newTestServer(t)
	r1, _ := srv.Store.CreateRun("r1", "")
	r2, _ := srv.Store.CreateRun("r2", "")
	a := createTestVia(t, srv, r1, `{"name":"password=PROD_SENTINEL","key":"tests/test_login.py::test_login[token=PROD_SENTINEL]"}`)
	b := createTestVia(t, srv, r1, `{"name":"login","key":"tests/test_login.py::test_login[token=OTHER_SENTINEL]"}`)
	again := createTestVia(t, srv, r2, `{"name":"login","key":"tests/test_login.py::test_login[token=PROD_SENTINEL]"}`)
	noKey := createTestVia(t, srv, r2, `{"name":"login password=PROD_SENTINEL"}`)
	for _, tt := range []db.Test{a, b, again, noKey} {
		if strings.Contains(tt.Name+tt.Key, "SENTINEL") {
			t.Fatalf("secret persisted in the identity: name=%q key=%q", tt.Name, tt.Key)
		}
	}
	if !strings.HasPrefix(a.Key, "tests/test_login.py::test_login[token=<masked>] #") {
		t.Fatalf("readable masked key: %q", a.Key)
	}
	if a.Key == b.Key {
		t.Fatal("different secret parameters must keep different identities")
	}
	if a.Key != again.Key {
		t.Fatalf("the same test must keep its identity: %q vs %q", a.Key, again.Key)
	}
	if !noKey.KeyApprox || !strings.HasPrefix(noKey.Key, "name:login password=<masked> #") {
		t.Fatalf("name identity: %+v", noKey.TestMeta)
	}
	plain := createTestVia(t, srv, r2, `{"name":"pay","key":"tests/test_pay.py::test_pay[visa]"}`)
	if plain.Key != "tests/test_pay.py::test_pay[visa]" {
		t.Fatalf("keys without secrets stay as they are: %q", plain.Key)
	}
}

func TestRunMetadataIsMasked(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"nightly token=PROD_SENTINEL","environment":"qa?secret=PROD_SENTINEL","project":"shop?password=PROD_SENTINEL","branch":"main"}`)
	var out struct {
		RunID int64 `json:"run_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	got := call(t, srv, "GET", "/api/v1/runs/"+itoa(out.RunID), "").Body.String()
	var r db.Run
	json.Unmarshal([]byte(got), &r)
	if strings.Contains(got, "PROD_SENTINEL") || r.Name != "nightly token=<masked>" || r.Project != "shop?password=<masked>" {
		t.Fatalf("run metadata: %s", got)
	}
}

// Un reenvío con la misma Idempotency-Key días después (confirmación perdida, reinicio, spool)
// no duplica pasos, capturas, red ni tests.
func TestReplayAfterDaysDoesNotDuplicate(t *testing.T) {
	clearAIEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")
	store, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{}}

	var run struct {
		RunID int64 `json:"run_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"r"}`, header("Idempotency-Key", "run-1")).Body.Bytes(), &run)
	rid := itoa(run.RunID)
	var created struct {
		TestID int64 `json:"test_id"`
	}
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs/"+rid+"/tests", `{"name":"t"}`, header("Idempotency-Key", "test-1")).Body.Bytes(), &created)
	tid := itoa(created.TestID)

	shot := func() *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("file", "s.png")
		fw.Write([]byte("\x89PNG\r\n\x1a\n0000"))
		mw.Close()
		return call(t, srv, "POST", "/api/v1/tests/"+tid+"/screenshot", b.String(),
			header("Content-Type", mw.FormDataContentType()), header("Idempotency-Key", "shot-1"))
	}
	send := func() {
		t.Helper()
		for _, rec := range []*httptest.ResponseRecorder{
			call(t, srv, "POST", "/api/v1/runs", `{"name":"r"}`, header("Idempotency-Key", "run-1")),
			call(t, srv, "POST", "/api/v1/runs/"+rid+"/tests", `{"name":"t"}`, header("Idempotency-Key", "test-1")),
			call(t, srv, "POST", "/api/v1/tests/"+tid+"/logs", `{"status":"INFO","message":"first"}`, header("Idempotency-Key", "log-1")),
			call(t, srv, "POST", "/api/v1/tests/"+tid+"/network", `{"connections":[{"method":"GET","url":"https://x.test/a","status":200}]}`, header("Idempotency-Key", "net-1")),
			shot(),
		} {
			if rec.Code >= 300 {
				t.Fatalf("send: %d %s", rec.Code, rec.Body)
			}
		}
	}
	send()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`UPDATE idempotency SET created_at=?`, db.NowMs()-3*24*3600*1000) // hace 3 días
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	call(t, srv, "POST", "/api/v1/runs/"+rid+"/tests", `{"name":"otro"}`, header("Idempotency-Key", "trigger-prune"))
	send() // reenvío del spool

	runs, _ := store.ListRuns(10)
	got, _ := store.GetTest(created.TestID)
	conns, _ := store.ListNetwork(created.TestID)
	if len(runs) != 1 || runs[0].Total != 2 || len(got.Logs) != 2 || len(conns) != 1 {
		t.Fatalf("replay after days duplicated evidence: runs=%d tests=%d logs=%d network=%d",
			len(runs), runs[0].Total, len(got.Logs), len(conns))
	}
}

// Detrás de un proxy local, con solo token: sin credenciales no se cambian ajustes ni se usan
// las acciones de la UI; el mismo equipo tampoco, salvo TRACEREPORTS_LOCAL_ADMIN.
func TestTokenOnlyServerBehindLocalProxy(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	settings := `{"language":"en"}`
	escalate := `{"run_id":1,"audience":"qa","lang":"es","no_ai":true}`
	xff := header("X-Forwarded-For", "203.0.113.10")
	cases := []struct {
		name string
		auth Auth
		opts []reqOpt
		want int
	}{
		{"proxy, no credentials", Auth{Token: "tok"}, []reqOpt{xff}, 403},
		{"proxy, invalid token", Auth{Token: "tok"}, []reqOpt{xff, header("Authorization", "Bearer bad")}, 403},
		{"proxy, valid token", Auth{Token: "tok"}, []reqOpt{xff, header("Authorization", "Bearer tok")}, 200},
		{"proxy, UI login", Auth{Token: "tok", UIUser: "qa", UIPass: "pw"}, []reqOpt{xff, basicAuth("qa", "pw")}, 200},
		{"same machine with token", Auth{Token: "tok"}, nil, 403},
		{"same machine, LOCAL_ADMIN", Auth{Token: "tok", LocalAdmin: true}, nil, 200},
		{"proxy, LOCAL_ADMIN", Auth{Token: "tok", LocalAdmin: true}, []reqOpt{xff}, 403},
		{"proxy, no auth configured", Auth{}, []reqOpt{xff}, 403},
		{"local mode", Auth{}, nil, 200},
	}
	for _, c := range cases {
		srv.Auth = c.auth
		if rec := call(t, srv, "PUT", "/api/v1/settings", settings, c.opts...); rec.Code != c.want {
			t.Errorf("settings, %s: %d (want %d)", c.name, rec.Code, c.want)
		}
		if rec := call(t, srv, "POST", "/api/v1/ui/escalate", escalate, c.opts...); rec.Code != c.want {
			t.Errorf("UI action, %s: %d (want %d) %s", c.name, rec.Code, c.want, rec.Body)
		}
	}
}

// Por la API: un resultado tardío pone la ejecución en rojo y un cierre repetido no la vuelve
// verde ni repite el análisis de IA.
func TestLateResultAndRepeatedCloseThroughAPI(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"message":{"content":"{\"category\":\"OTHER\",\"summary\":\"s\",\"suggestion\":\"x\",\"headline\":\"h\",\"incidents\":[]}"}}`))
	}))
	defer llm.Close()
	srv.AI.SetConfig(ai.Config{Provider: "ollama", BaseURL: llm.URL})
	wait := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.AI.Wait(ctx)
	}

	rid, _ := srv.Store.CreateRun("api", "qa")
	tt := createTestVia(t, srv, rid, `{"name":"t"}`)
	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(tt.ID)+"/finish", `{"status":"PASS"}`)
	var run db.Run
	json.Unmarshal(call(t, srv, "PATCH", "/api/v1/runs/"+itoa(rid)+"/finish", `{"interrupted":true}`).Body.Bytes(), &run)
	if run.Status != "WARNING" || !run.Incomplete {
		t.Fatalf("interrupted close: %+v", run)
	}
	wait()
	first := calls.Load()

	json.Unmarshal(call(t, srv, "PATCH", "/api/v1/runs/"+itoa(rid)+"/finish", `{}`).Body.Bytes(), &run)
	wait()
	if run.Status == "PASS" || !run.Incomplete {
		t.Fatalf("repeated close turned the run green: %+v", run)
	}
	if calls.Load() != first {
		t.Fatalf("a repeated close must not analyze again: %d -> %d calls", first, calls.Load())
	}

	call(t, srv, "PATCH", "/api/v1/tests/"+itoa(tt.ID)+"/finish", `{"status":"FAIL","error_message":"late"}`)
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs/"+itoa(rid), "").Body.Bytes(), &run)
	if run.Status != "FAIL" || run.Failed != 1 {
		t.Fatalf("late failure through the API: %+v", run)
	}
	wait()
}
