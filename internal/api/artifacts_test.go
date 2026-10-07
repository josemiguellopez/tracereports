package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Los primeros bytes reales de cada formato.
var (
	traceZip = append([]byte("PK\x03\x04"), bytes.Repeat([]byte("t"), 100)...)
	webm     = append([]byte{0x1A, 0x45, 0xDF, 0xA3}, bytes.Repeat([]byte{0}, 100)...)
)

func uploadArtifact(t *testing.T, srv *Server, testID int64, kind, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("kind", kind)
	if name != "" {
		mw.WriteField("name", name)
	}
	fw, _ := mw.CreateFormFile("file", "upload")
	fw.Write(data)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+itoa(testID)+"/artifact", &body)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestArtifactsTraceAndVideo(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	rec := uploadArtifact(t, srv, testID, "trace", "trace de Playwright", traceZip)
	if rec.Code != 201 {
		t.Fatalf("trace: %d %s", rec.Code, rec.Body)
	}
	var tr db.Artifact
	json.Unmarshal(rec.Body.Bytes(), &tr)
	if tr.Kind != "trace" || tr.Name != "trace de Playwright" || !strings.HasSuffix(tr.URL, ".zip") || tr.Size != int64(len(traceZip)) {
		t.Fatalf("trace artifact: %+v", tr)
	}
	if rec := uploadArtifact(t, srv, testID, "video", "", webm); rec.Code != 201 || !strings.Contains(rec.Body.String(), ".webm") {
		t.Fatalf("video: %d %s", rec.Code, rec.Body)
	}
	got, _ := os.ReadFile(filepath.Join(srv.ScreenshotsDir, filepath.Base(tr.URL)))
	if !bytes.Equal(got, traceZip) {
		t.Fatal("the stored file must be the uploaded one")
	}

	var tt db.Test
	json.Unmarshal(call(t, srv, "GET", "/api/v1/tests/"+itoa(testID), "").Body.Bytes(), &tt)
	if len(tt.Artifacts) != 2 || tt.Artifacts[0].Kind != "trace" || tt.Artifacts[1].Name != "video.webm" {
		t.Fatalf("test artifacts: %+v", tt.Artifacts)
	}

	// el Trace Viewer oficial puede descargar el trace (y solo el trace)
	req := httptest.NewRequest("GET", tr.URL, nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Origin", "https://trace.playwright.dev")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://trace.playwright.dev" {
		t.Fatalf("trace viewer CORS: %d %v", w.Code, w.Header())
	}
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no CORS for other origins")
	}
}

func TestArtifactsRejectWhatTheReportCannotShowSafely(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	html := []byte("<!doctype html><script>alert(1)</script>")
	for name, c := range map[string]struct {
		kind string
		data []byte
		code int
	}{
		"html as video":  {"video", html, 415},
		"html as trace":  {"trace", html, 415},
		"unknown kind":   {"har", traceZip, 400},
		"no kind":        {"", traceZip, 400},
		"missing test":   {"trace", traceZip, 404},
		"video as trace": {"trace", webm, 415},
	} {
		id := testID
		if name == "missing test" {
			id = 99999
		}
		if rec := uploadArtifact(t, srv, id, c.kind, "", c.data); rec.Code != c.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	entries, _ := os.ReadDir(srv.ScreenshotsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "a") {
			t.Fatalf("a rejected upload leaves no file: %s", e.Name())
		}
	}
	// con token, subir un artefacto lo exige como cualquier escritura
	srv.Auth = Auth{Token: "tok"}
	if rec := uploadArtifact(t, srv, testID, "trace", "", traceZip); rec.Code != 401 {
		t.Fatalf("without token: %d", rec.Code)
	}
}

func TestArtifactsInExportAndRetention(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	uploadArtifact(t, srv, testID, "video", "", webm)
	tt, _ := srv.Store.GetTest(testID)
	srv.Store.FinishTest(testID, "FAIL", "x", "")
	srv.Store.FinishRun(tt.RunID)

	rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(tt.RunID)+"/export", "")
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var video, data bool
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".webm") {
			video = true
		}
		if strings.HasSuffix(f.Name, "data.js") {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			data = strings.Contains(string(b), `"url":"screenshots/a`) // relativa: funciona desde file://
		}
	}
	if !video || !data {
		t.Fatalf("export: video=%v relative url=%v", video, data)
	}

	n, files, err := srv.Store.PurgeRunsBefore(db.NowMs() + 1000)
	if err != nil || n != 1 || len(files) != 1 || !strings.HasSuffix(files[0], ".webm") {
		t.Fatalf("retention returns the artifact to delete: %d %v %v", n, files, err)
	}
}
