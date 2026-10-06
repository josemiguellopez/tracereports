package offline_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/offline"
)

// PNG de 1x1: lo mínimo que el servidor acepta como captura.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func newServer(t *testing.T) (*api.Server, offline.Target) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	t.Setenv("GEMINI_API_KEY", "")
	analyzer := ai.New(store)
	_ = analyzer.SetConfig(ai.Config{})
	srv := &api.Server{Store: store, AI: analyzer, ScreenshotsDir: dir, Web: fstest.MapFS{"index.html": {}}}
	return srv, offline.Target{Doer: offline.Handler{Handler: srv.Router()}}
}

// recording writes a recording folder: files maps events file name -> events.
type ev = map[string]any

func recording(t *testing.T, files map[string][]ev, extra map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, offline.Marker), []byte(`{"format":"tracereports-offline","version":1,"id":"rec-`+t.Name()+`"}`), 0o644)
	for name, evs := range files {
		var b bytes.Buffer
		for i, e := range evs {
			if _, ok := e["seq"]; !ok {
				e["seq"] = i + 1
			}
			line, _ := json.Marshal(e)
			b.Write(line)
			b.WriteByte('\n')
		}
		os.WriteFile(filepath.Join(dir, name), b.Bytes(), 0o644)
	}
	for name, data := range extra {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}
	return dir
}

func shotBody(t *testing.T) ([]byte, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	mw.WriteField("message", "Pantalla al fallar")
	mw.WriteField("status", "FAIL")
	fw, _ := mw.CreateFormFile("file", "shot.png")
	fw.Write(png)
	mw.Close()
	return b.Bytes(), mw.FormDataContentType()
}

const t0 = int64(1_790_000_000_000) // hora fija del pasado: el reporte debe conservarla

func fullRun(t *testing.T) string {
	shot, ctype := shotBody(t)
	return recording(t, map[string][]ev{"events-100.jsonl": {
		{"ts": t0, "method": "POST", "path": "/api/v1/runs", "body": ev{"name": "Offline", "project": "shop"}, "local_id": -1},
		{"ts": t0 + 10, "method": "POST", "path": "/api/v1/runs/-1/tests", "body": ev{"name": "test_login", "key": "t.py::test_login"}, "local_id": -2},
		{"ts": t0 + 20, "method": "POST", "path": "/api/v1/tests/-2/logs", "body": ev{"status": "INFO", "message": "abrir login"}},
		{"ts": t0 + 30, "method": "POST", "path": "/api/v1/tests/-2/screenshot", "content_type": ctype, "body_file": "bodies/1.bin"},
		{"ts": t0 + 40, "method": "POST", "path": "/api/v1/tests/-2/network", "body": ev{"connections": []ev{
			{"method": "POST", "url": "https://api/x", "status": 500, "request_headers": ev{"Authorization": "Bearer SECRET-TOKEN"}}}}},
		{"ts": t0 + 2000, "method": "PATCH", "path": "/api/v1/tests/-2/finish", "body": ev{"status": "FAIL", "error_message": "boom"}},
		{"ts": t0 + 2100, "method": "PATCH", "path": "/api/v1/runs/-1/finish", "body": ev{}},
	}}, map[string][]byte{"bodies/1.bin": shot})
}

func TestReplayFullRunKeepsTimesAndEvidence(t *testing.T) {
	srv, target := newServer(t)
	rec, err := offline.Open(fullRun(t))
	if err != nil {
		t.Fatal(err)
	}
	res, err := rec.Replay(target)
	if err != nil || len(res.Runs) != 1 || res.Sent != 7 || res.Rejected+res.Skipped != 0 {
		t.Fatalf("replay: %v %+v", err, res)
	}
	run, _ := srv.Store.GetRun(res.Runs[0])
	if run.Name != "Offline" || run.Project != "shop" || run.Status != "FAIL" || run.Incomplete {
		t.Fatalf("run: %+v", run)
	}
	if run.StartedAt != t0 || run.EndedAt == nil || *run.EndedAt != t0+2100 {
		t.Fatalf("run times: %d %v", run.StartedAt, run.EndedAt)
	}
	d, _ := srv.Store.GetRunDetail(run.ID)
	tt, _ := srv.Store.GetTest(d.Tests[0].ID)
	if tt.StartedAt != t0+10 || *tt.EndedAt != t0+2000 || tt.ErrorMessage != "boom" || tt.Key != "t.py::test_login" {
		t.Fatalf("test: %+v", tt)
	}
	if len(tt.Logs) != 2 || tt.Logs[0].Timestamp != t0+20 || tt.Logs[1].Timestamp != t0+30 || tt.Logs[1].Screenshot == "" {
		t.Fatalf("steps and screenshot keep their times: %+v", tt.Logs)
	}
	conns, _ := srv.Store.ListNetwork(tt.ID)
	if len(conns) != 1 || strings.Contains(fmt.Sprint(conns[0].RequestHeaders), "SECRET-TOKEN") {
		t.Fatalf("network stored and masked: %+v", conns)
	}
}

func TestReplayTwiceDoesNotDuplicate(t *testing.T) {
	srv, target := newServer(t)
	dir := fullRun(t)
	for i := 0; i < 2; i++ {
		rec, _ := offline.Open(dir)
		if _, err := rec.Replay(target); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := srv.Store.ListRuns(10)
	if len(runs) != 1 || runs[0].Total != 1 {
		t.Fatalf("a second push must not duplicate: %+v", runs)
	}
	tt, _ := srv.Store.GetTest(1)
	if len(tt.Logs) != 2 {
		t.Fatalf("steps duplicated: %d", len(tt.Logs))
	}
}

func TestReplayMergesProcessesAndClosesUnfinishedRuns(t *testing.T) {
	srv, target := newServer(t)
	// pytest-xdist: el controlador crea la ejecución; cada worker escribe su propio archivo
	dir := recording(t, map[string][]ev{
		"events-1.jsonl": {{"ts": t0, "method": "POST", "path": "/api/v1/runs", "body": ev{"name": "xdist"}, "local_id": -1}},
		"events-2.jsonl": {
			{"ts": t0 + 5, "method": "POST", "path": "/api/v1/runs/-1/tests", "body": ev{"name": "a"}, "local_id": -200001},
			{"ts": t0 + 50, "method": "PATCH", "path": "/api/v1/tests/-200001/finish", "body": ev{"status": "PASS"}},
		},
		"events-3.jsonl": {
			{"ts": t0 + 7, "method": "POST", "path": "/api/v1/runs/-1/tests", "body": ev{"name": "b"}, "local_id": -300001},
			{"ts": t0 + 60, "method": "PATCH", "path": "/api/v1/tests/-300001/finish", "body": ev{"status": "PASS"}},
		},
		// sin PATCH /runs/-1/finish: el proceso murió antes de cerrar
	}, nil)
	rec, _ := offline.Open(dir)
	res, err := rec.Replay(target)
	if err != nil || res.Sent != 5 {
		t.Fatalf("replay: %v %+v", err, res)
	}
	run, _ := srv.Store.GetRun(res.Runs[0])
	if run.Total != 2 || !run.Incomplete || run.EndedAt == nil || *run.EndedAt != t0+60 {
		t.Fatalf("unfinished run closed as incomplete at its last event: %+v", run)
	}
}

func TestReplaySkipsWhatDependsOnARejectedCreation(t *testing.T) {
	_, target := newServer(t)
	dir := recording(t, map[string][]ev{"events-1.jsonl": {
		{"ts": t0, "method": "POST", "path": "/api/v1/runs", "body": ev{"name": ""}, "local_id": -1}, // sin nombre: 400
		{"ts": t0 + 1, "method": "POST", "path": "/api/v1/runs/-1/tests", "body": ev{"name": "a"}, "local_id": -2},
		{"ts": t0 + 2, "method": "PATCH", "path": "/api/v1/tests/-2/finish", "body": ev{"status": "PASS"}},
	}}, nil)
	rec, _ := offline.Open(dir)
	res, err := rec.Replay(target)
	if err != nil || res.Rejected != 1 || res.Skipped != 2 || len(res.Runs) != 0 || len(res.Errors) == 0 {
		t.Fatalf("replay: %v %+v", err, res)
	}
}

func TestOpenValidation(t *testing.T) {
	good := ev{"ts": t0, "method": "POST", "path": "/api/v1/runs", "body": ev{"name": "x"}, "local_id": -1}
	// la última línea a medias (el proceso murió escribiéndola) se ignora
	dir := recording(t, map[string][]ev{"events-1.jsonl": {good}}, nil)
	f, _ := os.OpenFile(filepath.Join(dir, "events-1.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"ts":1,"method":"PO`)
	f.Close()
	if rec, err := offline.Open(dir); err != nil || len(rec.Events()) != 1 {
		t.Fatalf("truncated last line must be tolerated: %v", err)
	}
	// una línea rota en medio es un error
	dir = recording(t, nil, map[string][]byte{"events-1.jsonl": []byte("{bad\n" + `{"ts":1,"method":"POST","path":"/api/v1/runs"}` + "\n")})
	if _, err := offline.Open(dir); err == nil {
		t.Fatal("a broken line in the middle must fail")
	}
	// rutas fuera de la API
	dir = recording(t, map[string][]ev{"events-1.jsonl": {{"ts": 1, "method": "GET", "path": "/etc/passwd"}}}, nil)
	if _, err := offline.Open(dir); err == nil {
		t.Fatal("paths outside /api/v1/ must be rejected")
	}
	// formato más nuevo que este binario
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, offline.Marker), []byte(`{"format":"tracereports-offline","version":99}`), 0o644)
	os.WriteFile(filepath.Join(dir, "events-1.jsonl"), []byte("\n"), 0o644)
	if _, err := offline.Open(dir); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer format: %v", err)
	}
	// carpeta sin eventos
	if _, err := offline.Open(t.TempDir()); err == nil {
		t.Fatal("empty folder must fail")
	}
}

func TestBodyFileCannotLeaveTheRecording(t *testing.T) {
	_, target := newServer(t)
	dir := recording(t, map[string][]ev{"events-1.jsonl": {
		{"ts": t0, "method": "POST", "path": "/api/v1/runs", "body": ev{"name": "x"}, "local_id": -1},
		{"ts": t0 + 1, "method": "POST", "path": "/api/v1/runs/-1/tests", "content_type": "application/json", "body_file": "../../secret.json", "local_id": -2},
	}}, nil)
	rec, _ := offline.Open(dir)
	res, err := rec.Replay(target)
	if err != nil || res.Rejected != 1 || !strings.Contains(strings.Join(res.Errors, " "), "inside the recording") {
		t.Fatalf("path traversal: %v %+v", err, res)
	}
}

func TestIsRecording(t *testing.T) {
	if offline.IsRecording(t.TempDir()) {
		t.Fatal("empty folder is not a recording")
	}
	if !offline.IsRecording(fullRun(t)) {
		t.Fatal("recording not detected")
	}
}
