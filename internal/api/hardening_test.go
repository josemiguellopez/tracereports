package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// /healthz responde sin credenciales (el healthcheck no las tiene) y no dice nada de los datos.
func TestHealthzNeedsNoCredentialsAndShowsNoData(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "tok", UIUser: "qa", UIPass: "clave"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.RemoteAddr = "10.0.0.5:1234" // remoto y sin login
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("healthz: %d %s", rec.Code, rec.Body)
	}
	// lo demás sigue protegido
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/runs", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("runs sin login: %d", rec.Code)
	}
	// solo GET
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest("POST", "/healthz", nil))
	if rec.Code == 200 {
		t.Fatal("POST /healthz no es el healthcheck")
	}
}

// Las capturas y los artefactos se escriben solo para el usuario del servidor.
func TestEvidenceFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permisos POSIX")
	}
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	if rec := uploadShot(t, srv, testID, "INFO"); rec.Code != 201 {
		t.Fatalf("captura: %d %s", rec.Code, rec.Body)
	}
	if rec := uploadArtifact(t, srv, testID, "video", "", webm); rec.Code != 201 {
		t.Fatalf("video: %d %s", rec.Code, rec.Body)
	}
	files := storedFiles(t, srv)
	if len(files) != 2 {
		t.Fatalf("archivos: %v", files)
	}
	for _, f := range files {
		info, err := os.Stat(filepath.Join(srv.ScreenshotsDir, f))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, info.Mode(), err)
		}
	}
}
