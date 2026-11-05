package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// storageRun creates a finished run of `project` started `daysAgo` days ago, with one test and a
// screenshot of `size` bytes in the screenshots folder.
func storageRun(t *testing.T, srv *Server, project string, daysAgo, size int) (int64, string) {
	t.Helper()
	id, err := srv.Store.CreateRunWithMeta("suite "+project, "qa", db.RunMeta{Project: project, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := srv.Store.CreateTest(id, "test_"+project, "", "")
	name := project + "-" + time.Now().Format("150405.000000000") + ".png"
	if err := os.WriteFile(filepath.Join(srv.ScreenshotsDir, name), bytes.Repeat([]byte{1}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Store.AddLog(tid, "FAIL", "captura", 0, "/screenshots/"+name)
	srv.Store.FinishTest(tid, "FAIL", "x", "")
	srv.Store.FinishRun(id)
	start := time.Now().AddDate(0, 0, -daysAgo).UnixMilli()
	if err := srv.Store.SetRunTimes(id, start, start+1000); err != nil {
		t.Fatal(err)
	}
	return id, name
}

var storageAdmin, storageViewer = basicAuth("admin", "clave-admin"), basicAuth("equipo", "clave-lectura")

// storageServer: capturas en su propia carpeta (como DATA_DIR/screenshots: la base va aparte) y
// un usuario administrador y uno de lectura.
func storageServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestServer(t)
	srv.ScreenshotsDir = t.TempDir()
	srv.Auth = Auth{UIUser: "admin", UIPass: "clave-admin", ViewerUser: "equipo", ViewerPass: "clave-lectura"}
	return srv
}

func getJSON(t *testing.T, srv *Server, path string, out any) int {
	t.Helper()
	rec := call(t, srv, http.MethodGet, path, "", storageAdmin)
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code
}

func TestStorageReportsSizesProjectsAndRetention(t *testing.T) {
	t.Setenv("TRACEREPORTS_RETENTION_DAYS", "")
	srv := storageServer(t)
	storageRun(t, srv, "grande", 10, 50_000)
	storageRun(t, srv, "grande", 5, 50_000)
	storageRun(t, srv, "chico", 1, 1_000)
	os.WriteFile(filepath.Join(srv.ScreenshotsDir, "huerfana.png"), bytes.Repeat([]byte{1}, 7_000), 0o600)

	var st struct {
		DBBytes     int64 `json:"db_bytes"`
		FilesBytes  int64 `json:"files_bytes"`
		OrphanBytes int64 `json:"orphan_bytes"`
		TotalBytes  int64 `json:"total_bytes"`
		Runs        int   `json:"runs"`
		BytesPerDay int64 `json:"bytes_per_day"`
		Disk        *struct {
			Total, Free uint64
		} `json:"disk"`
		Projects []projectUsage `json:"projects"`
		Retention struct {
			Days     int    `json:"days"`
			Source   string `json:"source"`
			Editable bool   `json:"editable"`
		} `json:"retention"`
	}
	if code := getJSON(t, srv, "/api/v1/maintenance/storage", &st); code != http.StatusOK {
		t.Fatalf("storage: %d", code)
	}
	if st.DBBytes <= 0 || st.FilesBytes != 108_000 || st.OrphanBytes != 7_000 || st.TotalBytes != st.DBBytes+st.FilesBytes {
		t.Fatalf("sizes: %+v", st)
	}
	if st.Runs != 4 || st.BytesPerDay <= 0 || st.Disk == nil || st.Disk.Total == 0 { // 4: la del servidor de prueba
		t.Fatalf("counts, rate, disk: %+v", st)
	}
	if len(st.Projects) < 2 || st.Projects[0].Project != "grande" || st.Projects[0].Runs != 2 || st.Projects[0].Bytes < 100_000 {
		t.Fatalf("projects by size: %+v", st.Projects)
	}
	if st.Retention.Days != 0 || st.Retention.Source != "default" || !st.Retention.Editable {
		t.Fatalf("retention: %+v", st.Retention)
	}
}

func TestRetentionPreviewSetAndRunNow(t *testing.T) {
	t.Setenv("TRACEREPORTS_RETENTION_DAYS", "")
	srv := storageServer(t)
	oldRun, oldShot := storageRun(t, srv, "p", 40, 20_000)
	keep, keepShot := storageRun(t, srv, "p", 2, 3_000)

	var pv struct {
		Runs       int   `json:"runs"`
		FilesBytes int64 `json:"files_bytes"`
		Freed      int64 `json:"freed_bytes_approx"`
	}
	if code := getJSON(t, srv, "/api/v1/maintenance/storage/preview?days=30", &pv); code != http.StatusOK {
		t.Fatalf("preview: %d", code)
	}
	if pv.Runs != 1 || pv.FilesBytes != 20_000 || pv.Freed < 20_000 {
		t.Fatalf("preview: %+v", pv)
	}
	if code := getJSON(t, srv, "/api/v1/maintenance/storage/preview?days=0", &pv); code != http.StatusBadRequest {
		t.Fatalf("days=0 preview: %d", code)
	}

	if rec := call(t, srv, http.MethodPut, "/api/v1/ui/maintenance/retention", `{"days":30}`, storageViewer); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer cannot change the retention: %d", rec.Code)
	}
	if rec := call(t, srv, http.MethodPost, "/api/v1/ui/maintenance/retention/run", `{"days":30}`, storageViewer); rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer cannot clean up: %d", rec.Code)
	}
	if rec := call(t, srv, http.MethodPut, "/api/v1/ui/maintenance/retention", `{"days":30}`, storageAdmin); rec.Code != http.StatusOK {
		t.Fatalf("set retention: %d %s", rec.Code, rec.Body.String())
	}
	if d, src := srv.RetentionDays(); d != 30 || src != "ui" {
		t.Fatalf("retention: %d %s", d, src)
	}
	rec := call(t, srv, http.MethodPost, "/api/v1/ui/maintenance/retention/run", "{}", storageAdmin)
	var run RetentionRun
	json.Unmarshal(rec.Body.Bytes(), &run)
	if rec.Code != http.StatusOK || run.Runs != 1 || run.Files != 1 || run.FreedBytes < 20_000 || run.Trigger != "manual" {
		t.Fatalf("run now: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(srv.ScreenshotsDir, oldShot)); !os.IsNotExist(err) {
		t.Fatal("the old screenshot is deleted")
	}
	if _, err := os.Stat(filepath.Join(srv.ScreenshotsDir, keepShot)); err != nil {
		t.Fatal("the recent screenshot stays")
	}
	if _, err := srv.Store.GetRun(oldRun); err == nil {
		t.Fatal("the old run is deleted")
	}
	if _, err := srv.Store.GetRun(keep); err != nil {
		t.Fatal("the recent run stays")
	}
	if last := srv.lastRetention(); last == nil || last.Runs != 1 || last.Days != 30 {
		t.Fatalf("last cleanup recorded: %+v", last)
	}
}

func TestRetentionFromTheEnvironmentWins(t *testing.T) {
	t.Setenv("TRACEREPORTS_RETENTION_DAYS", "90")
	srv := storageServer(t)
	if rec := call(t, srv, http.MethodPut, "/api/v1/ui/maintenance/retention", `{"days":30}`, storageAdmin); rec.Code != http.StatusConflict {
		t.Fatalf("env wins: %d", rec.Code)
	}
	if d, src := srv.RetentionDays(); d != 90 || src != "env" {
		t.Fatalf("%d %s", d, src)
	}
	for _, body := range []string{`{"days":-1}`, `{"days":99999}`, `{}`} {
		t.Setenv("TRACEREPORTS_RETENTION_DAYS", "")
		if rec := call(t, srv, http.MethodPut, "/api/v1/ui/maintenance/retention", body, storageAdmin); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, rec.Code)
		}
	}
	// limpiar ahora sin retención ni días: no hay qué borrar
	if rec := call(t, srv, http.MethodPost, "/api/v1/ui/maintenance/retention/run", "{}", storageAdmin); rec.Code != http.StatusBadRequest {
		t.Fatalf("run without days: %d", rec.Code)
	}
}

// Sin retención el arranque avisa que la evidencia no se borra nunca (antes en cmd/hardening_test.go).
func TestRetentionWarnsWhenNotSet(t *testing.T) {
	srv := storageServer(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	for value, want := range map[string]string{
		"":     "TRACEREPORTS_RETENTION_DAYS not set",
		"diez": "not a positive number of days",
		"0":    "not a positive number of days",
	} {
		buf.Reset()
		t.Setenv("TRACEREPORTS_RETENTION_DAYS", value)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		srv.StartRetention(ctx)
		if !strings.Contains(buf.String(), want) || !strings.Contains(buf.String(), "level=WARN") {
			t.Errorf("%q: %s", value, buf.String())
		}
	}
}
