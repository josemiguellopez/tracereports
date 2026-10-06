package main

import (
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

// smallRecording is what a client writes when there is no server: a run with one failed test.
func smallRecording(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tracereports-offline.json"), []byte(`{"format":"tracereports-offline","version":1,"id":"cmd-test"}`), 0o644)
	events := strings.Join([]string{
		`{"seq":1,"ts":1790000000000,"method":"POST","path":"/api/v1/runs","content_type":"application/json","body":{"name":"Sin servidor"},"local_id":-1}`,
		`{"seq":2,"ts":1790000000100,"method":"POST","path":"/api/v1/runs/-1/tests","content_type":"application/json","body":{"name":"test_offline_login"},"local_id":-2}`,
		`{"seq":3,"ts":1790000000200,"method":"POST","path":"/api/v1/tests/-2/logs","content_type":"application/json","body":{"status":"FAIL","message":"el dashboard no apareció"}}`,
		`{"seq":4,"ts":1790000001000,"method":"PATCH","path":"/api/v1/tests/-2/finish","content_type":"application/json","body":{"status":"FAIL","error_message":"Timeout"}}`,
		`{"seq":5,"ts":1790000001100,"method":"PATCH","path":"/api/v1/runs/-1/finish","content_type":"application/json","body":{}}`,
	}, "\n") + "\n"
	os.WriteFile(filepath.Join(dir, "events-1.jsonl"), []byte(events), 0o644)
	return dir
}

func readReport(t *testing.T, dir string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Fatalf("index.html: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "data.js"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReportFromRecording(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	out := filepath.Join(t.TempDir(), "rep")
	zipPath := filepath.Join(t.TempDir(), "rep.zip")
	if err := runReport([]string{"-o", out, "-zip", zipPath, smallRecording(t)}); err != nil {
		t.Fatal(err)
	}
	data := readReport(t, out)
	for _, want := range []string{"Sin servidor", "test_offline_login", "el dashboard no apareció"} {
		if !strings.Contains(data, want) {
			t.Errorf("report misses %q", want)
		}
	}
	if st, err := os.Stat(zipPath); err != nil || st.Size() == 0 {
		t.Fatalf("zip: %v", err)
	}
}

func TestReportFromJUnitFolderAndFiles(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	xmls := t.TempDir()
	for _, f := range []string{"pytest.xml", "surefire.xml"} {
		b, _ := os.ReadFile(filepath.Join("..", "internal", "junit", "testdata", f))
		os.WriteFile(filepath.Join(xmls, f), b, 0o644)
	}
	out := filepath.Join(t.TempDir(), "rep")
	// una carpeta de XML (una ejecución) + un archivo suelto (otra): una subcarpeta por ejecución
	if err := runReport([]string{"-o", out, "-name", "Nightly", xmls, filepath.Join(xmls, "pytest.xml")}); err != nil {
		t.Fatal(err)
	}
	first, second := readReport(t, filepath.Join(out, "run-1")), readReport(t, filepath.Join(out, "run-2"))
	if !strings.Contains(first, "loginBroken") || !strings.Contains(first, "test_login_ok") || !strings.Contains(second, "Nightly") {
		t.Fatal("JUnit runs missing from the report")
	}
}

func TestReportErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no input":      {},
		"missing input": {"does-not-exist.xml"},
		"empty folder":  {t.TempDir()},
		"--ai needs AI": {"-ai", smallRecording(t)},
	} {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("AI_PROVIDER", "")
		if err := runReport(append([]string{"-o", filepath.Join(t.TempDir(), "x")}, args...)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestPushWithTheRightTokenOnly(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	analyzer := ai.New(store)
	srv := &api.Server{Store: store, AI: analyzer, ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}, Auth: api.Auth{Token: "right"}}
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	rec := smallRecording(t)

	// la clave equivocada (lo que pasó en el CI) no sube nada y lo dice
	if err := runPush([]string{"-url", ts.URL, "-token", "wrong", rec}); err == nil {
		t.Fatal("push with a wrong token must fail")
	}
	if runs, _ := store.ListRuns(10); len(runs) != 0 {
		t.Fatalf("nothing stored with a wrong token: %d runs", len(runs))
	}
	// con la clave correcta sube todo; repetirlo no duplica
	for i := 0; i < 2; i++ {
		if err := runPush([]string{"-url", ts.URL, "-token", "right", rec}); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := store.ListRuns(10)
	if len(runs) != 1 || runs[0].Name != "Sin servidor" || runs[0].Failed != 1 || runs[0].StartedAt != 1790000000000 {
		t.Fatalf("pushed run: %+v", runs)
	}
}

// Regresión: las opciones pueden ir después de la entrada ("report rec -o out"), que es como lo
// imprimen los clientes; el paquete flag solo las leería antes.
func TestReportFlagsAfterTheInput(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	out := filepath.Join(t.TempDir(), "rep")
	if err := runReport([]string{smallRecording(t), "-o", out, "-name", "x"}); err != nil {
		t.Fatal(err)
	}
	readReport(t, out)
}
