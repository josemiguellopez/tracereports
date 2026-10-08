package api

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// discardWriter es una respuesta HTTP que no guarda nada: mide lo que asigna la exportación,
// no el buffer de la respuesta.
type discardWriter struct {
	h http.Header
	n int64
}

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) WriteHeader(int)             {}
func (d *discardWriter) Write(p []byte) (int, error) { d.n += int64(len(p)); return len(p), nil }

func TestExportStreamsFilesInsteadOfLoadingThem(t *testing.T) {
	srv, testID := newTestServer(t)
	const size = 24 << 20
	name := fmt.Sprintf("a%d_1.webm", testID)
	f, err := os.Create(filepath.Join(srv.ScreenshotsDir, name))
	if err != nil {
		t.Fatal(err)
	}
	io.CopyN(f, rand.Reader, size) // aleatorio: no se comprime
	f.Close()
	srv.Store.AddArtifact(&db.Artifact{TestID: testID, Kind: "video", Name: "v.webm", URL: "/screenshots/" + name, Size: size})

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out := &discardWriter{h: http.Header{}}
	req := httptest.NewRequest("GET", "/api/v1/runs/1/export", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	srv.Router().ServeHTTP(out, req)
	runtime.ReadMemStats(&after)
	if out.n < size {
		t.Fatalf("the artifact must be in the ZIP: %d bytes written", out.n)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > size/2 {
		t.Fatalf("export allocated %d MiB for a %d MiB file: it must stream it", alloc>>20, size>>20)
	}
}

func TestExportKeepsNetworkBodiesRedactionAndMissingFiles(t *testing.T) {
	srv, testID := newTestServer(t)
	big := `{"token":"` + strings.Repeat("A", 10) + `","data":"` + strings.Repeat("x", bodyFileMinChars+100) + `"}`
	srv.Store.AddNetwork(testID, []db.NetConn{{Method: "GET", URL: "https://api/x", Status: 500, ResponseBody: big,
		RequestHeaders: map[string]string{"Authorization": "Bearer SECRET-EXPORT-TOKEN"}}})
	srv.Store.AddLog(testID, "INFO", "captura borrada", 0, "/screenshots/missing.png") // el archivo no existe
	rec := call(t, srv, "GET", "/api/v1/runs/1/export", "")
	if rec.Code != 200 {
		t.Fatalf("export: %d", rec.Code)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var netJS, body string
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(b), "SECRET-EXPORT-TOKEN") {
			t.Fatalf("%s leaks the secret", f.Name)
		}
		switch {
		case strings.HasSuffix(f.Name, fmt.Sprintf("network/test_%d.js", testID)):
			netJS = string(b)
		case strings.Contains(f.Name, "network/bodies/"):
			body = string(b)
		case strings.Contains(f.Name, "missing.png"):
			t.Fatal("a missing screenshot is skipped, not added empty")
		}
	}
	if !strings.Contains(netJS, "body_file") || !strings.Contains(body, strings.Repeat("x", 100)) {
		t.Fatalf("network file and its body file: net=%d body=%d", len(netJS), len(body))
	}
}
