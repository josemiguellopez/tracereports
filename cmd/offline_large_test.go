package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// largeRecording is what a client writes for a network call with a 20 MiB request body sent alone
// (clients send one connection by itself up to ~40 MiB): one line well over 16 MiB, between two
// normal events. The body mixes Unicode and characters that grow when escaped in JSON.
func largeRecording(t *testing.T, postData string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tracereports-offline.json"), []byte(`{"format":"tracereports-offline","version":1,"id":"large"}`), 0o644)
	conn, _ := json.Marshal(map[string]any{"connections": []map[string]any{{
		"method": "POST", "url": "https://app/api/upload", "status": 500, "post_data": postData, "response_body": `{"error":"boom"}`}}})
	events := strings.Join([]string{
		`{"seq":1,"ts":1790000000000,"method":"POST","path":"/api/v1/runs","content_type":"application/json","body":{"name":"Grande"},"local_id":-1}`,
		`{"seq":2,"ts":1790000000100,"method":"POST","path":"/api/v1/runs/-1/tests","content_type":"application/json","body":{"name":"upload"},"local_id":-2}`,
		`{"seq":3,"ts":1790000000200,"method":"POST","path":"/api/v1/tests/-2/logs","content_type":"application/json","body":{"status":"INFO","message":"antes del upload"}}`,
		`{"seq":4,"ts":1790000000300,"method":"POST","path":"/api/v1/tests/-2/network","content_type":"application/json","body":` + string(conn) + `}`,
		`{"seq":5,"ts":1790000000400,"method":"POST","path":"/api/v1/tests/-2/logs","content_type":"application/json","body":{"status":"FAIL","message":"después del upload"}}`,
		`{"seq":6,"ts":1790000001000,"method":"PATCH","path":"/api/v1/tests/-2/finish","content_type":"application/json","body":{"status":"FAIL","error_message":"HTTP 500"}}`,
		`{"seq":7,"ts":1790000001100,"method":"PATCH","path":"/api/v1/runs/-1/finish","content_type":"application/json","body":{}}`,
	}, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "events-1.jsonl"), []byte(events), 0o644)
	return dir
}

func bigPostData(n int) string {
	// "ñ" 2 bytes, '"' y '\u0001' crecen al escaparse (2 y 6 bytes)
	unit := "ñ\"\u0001x"
	return strings.Repeat(unit, n/len(unit))
}

func TestReportAndPushReadAnEventOver16MiB(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	rec := largeRecording(t, bigPostData(20<<20))
	if st, _ := os.Stat(filepath.Join(rec, "events-1.jsonl")); st.Size() < 20<<20 {
		t.Fatalf("the fixture must have a line over 16 MiB: %d", st.Size())
	}
	out := filepath.Join(t.TempDir(), "rep")
	if err := runReport([]string{"-o", out, rec}); err != nil {
		t.Fatalf("report: %v", err)
	}
	data := readReport(t, out)
	before, after := strings.Index(data, "antes del upload"), strings.Index(data, "después del upload")
	if before < 0 || after < 0 || !strings.Contains(data, "https://app/api/upload") {
		t.Fatal("report misses events around the large one")
	}

	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}}
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	for i := 0; i < 2; i++ { // repetir no duplica
		if err := runPush([]string{"-url", ts.URL, rec}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	runs, _ := store.ListRuns(10)
	if len(runs) != 1 {
		t.Fatalf("one run: %d", len(runs))
	}
	got, err := store.GetTest(1) // base nueva: el único test
	if err != nil || got.RunID != runs[0].ID {
		t.Fatalf("the test: %+v %v", got, err)
	}
	if len(got.Logs) != 2 || got.Logs[0].Message != "antes del upload" || got.Logs[1].Message != "después del upload" {
		t.Fatalf("logs in order, once: %+v", got.Logs)
	}
	conns, _ := store.ListNetwork(got.ID)
	if len(conns) != 1 || conns[0].URL != "https://app/api/upload" || !strings.HasPrefix(conns[0].PostData, "ñ\"\u0001x") {
		t.Fatalf("the large connection, once and intact at its start: %d", len(conns))
	}
}

// Una línea más grande que lo que el servidor aceptaría falla con un error claro (archivo,
// línea, tamaño), sin recortar el JSON ni saltarla en silencio; las grabaciones pequeñas siguen igual.
func TestAnEventOverTheLimitIsReportedClearly(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	rec := largeRecording(t, strings.Repeat("x", 50<<20))
	err := runReport([]string{"-o", filepath.Join(t.TempDir(), "rep"), rec})
	if err == nil || !strings.Contains(err.Error(), "events-1.jsonl") || !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("clear error: %v", err)
	}
	if err := runReport([]string{"-o", filepath.Join(t.TempDir(), "rep"), smallRecording(t)}); err != nil {
		t.Fatalf("small recordings still work: %v", err)
	}
}
