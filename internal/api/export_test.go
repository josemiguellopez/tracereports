package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestExportRunZip(t *testing.T) {
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	shots := filepath.Join(dir, "shots")
	os.MkdirAll(shots, 0o755)
	os.WriteFile(filepath.Join(shots, "t1_1.png"), []byte("PNGDATA"), 0o644)

	runID, _ := store.CreateRun("Regresión Web", "qa")
	testID, _ := store.CreateTest(runID, "Login", "", "")
	store.AddLog(testID, "INFO", "captura", 0, "/screenshots/t1_1.png")
	store.FinishTest(testID, "PASS", "", "")

	t.Setenv("GEMINI_API_KEY", "")
	srv := &Server{Store: store, AI: ai.New(store), ScreenshotsDir: shots, Web: fstest.MapFS{
		"index.html":               {Data: []byte("<html><!-- tracereports:static-data --><script src=\"app.js\"></script></html>")},
		"style.css":                {Data: []byte("body{}")},
		"themes.css":               {Data: []byte("/* themes */")},
		"features.css":             {Data: []byte("/* features */")},
		"tracereports_features.js": {Data: []byte("// features")},
		"i18n.js":                  {Data: []byte("// i18n")},
		"i18n.en.js":               {Data: []byte("// en")},
		"app.js":                   {Data: []byte("//app")},
	}}

	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runs/1/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "tracereports_regresion_web_") {
		t.Errorf("unexpected filename: %s", cd)
	}

	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name[strings.Index(f.Name, "/")+1:]] = string(b)
	}
	for _, want := range []string{"index.html", "style.css", "themes.css", "features.css", "i18n.js", "i18n.en.js", "tracereports_features.js", "app.js", "data.js", "README.txt", "screenshots/t1_1.png"} {
		if _, ok := files[want]; !ok {
			t.Errorf("zip missing %s (has %v)", want, len(files))
		}
	}
	if !strings.Contains(files["index.html"], `<script src="data.js"></script>`) {
		t.Error("index.html does not load data.js")
	}
	if !strings.Contains(files["data.js"], `"screenshot":"screenshots/t1_1.png"`) {
		t.Error("screenshot path not rewritten to relative")
	}

	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runs/99/export", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing run: want 404, got %d", rec.Code)
	}
}

func TestSlugAndScreenshotFile(t *testing.T) {
	if got := slug("Regresión Web - Checkout ñandú"); got != "regresion_web_checkout_nandu" {
		t.Errorf("slug = %q", got)
	}
	if slug("!!!") != "reporte" {
		t.Error("empty slug fallback")
	}
	for in, want := range map[string]string{
		"/screenshots/a.png":       "a.png",
		"/screenshots/../etc/pass": "",
		"/other/a.png":             "",
		"":                         "",
	} {
		if got := screenshotFile(in); got != want {
			t.Errorf("screenshotFile(%q) = %q, want %q", in, got, want)
		}
	}
}
