package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/notify"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// texto único que no puede aparecer en claro en ningún archivo de DATA_DIR con el cifrado activo
const atRestMarker = "ATREST_MARKER_9c1e57"

func encryptedServer(t *testing.T) (*Server, int64) {
	t.Helper()
	srv, testID := newTestServer(t)
	v, err := atrest.New(bytes.Repeat([]byte{7}, 32), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	srv.Vault = v
	srv.Store.SetVault(v)
	return srv, testID
}

func uploadShotData(t *testing.T, srv *Server, testID int64, data []byte) string {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "s.png")
	fw.Write(data)
	mw.Close()
	rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/screenshot", b.String(), header("Content-Type", mw.FormDataContentType()))
	if rec.Code != 201 {
		t.Fatalf("screenshot: %d %s", rec.Code, rec.Body)
	}
	var out struct{ URL string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out.URL
}

// grepDir reports the files of dir (the database and its WAL included) that contain needle.
func grepDir(t *testing.T, dir, needle string) []string {
	t.Helper()
	var hits []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if raw, _ := os.ReadFile(p); bytes.Contains(raw, []byte(needle)) {
				hits = append(hits, filepath.Base(p))
			}
		}
		return nil
	})
	return hits
}

// Con TRACEREPORTS_ENCRYPT_AT_REST la captura, el video y el DOM no quedan legibles en el disco, y
// la UI, el export y los tickets los siguen viendo como siempre.
func TestEvidenceEncryptedAtRest(t *testing.T) {
	clearAIEnv(t)
	srv, testID := encryptedServer(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("pantalla "+atRestMarker)...)
	url := uploadShotData(t, srv, testID, png)

	video := append(append([]byte{}, webm...), bytes.Repeat([]byte(atRestMarker), 9000)...) // varios bloques
	if rec := uploadArtifact(t, srv, testID, "video", "", video); rec.Code != 201 {
		t.Fatalf("video: %d %s", rec.Code, rec.Body)
	}
	var art struct{ URL string }
	json.Unmarshal(uploadArtifact(t, srv, testID, "trace", "", traceZip).Body.Bytes(), &art)

	el, _ := json.Marshal(map[string]any{"tag": "div", "text": "tarjeta " + atRestMarker, "visible": true, "w": 80, "h": 20})
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/dom", `{"url":"https://app.test/","title":"t","elements":[`+string(el)+`]}`); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}

	if hits := grepDir(t, srv.ScreenshotsDir, atRestMarker); len(hits) > 0 {
		t.Fatalf("evidencia en claro en el disco: %v", hits)
	}
	if keys, _ := srv.Store.DOMKeys(); keys[srv.Vault.KeyID()] != 1 || len(keys) != 1 {
		t.Fatalf("DOM sin sellar: %v", keys)
	}

	// la UI la ve descifrada
	if rec := call(t, srv, "GET", url, ""); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), png) || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("captura: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if dom, err := srv.Store.GetDOM(testID); err != nil || !strings.Contains(dom, atRestMarker) {
		t.Fatalf("DOM: %v", err)
	}
	// un Range en medio del video (cruza el borde de un bloque cifrado)
	files, _ := filepath.Glob(filepath.Join(srv.ScreenshotsDir, "a*.webm"))
	if len(files) != 1 {
		t.Fatalf("video: %v", files)
	}
	rec := call(t, srv, "GET", "/screenshots/"+filepath.Base(files[0]), "", header("Range", "bytes=65530-65545"))
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), video[65530:65546]) {
		t.Fatalf("Range: %d %q", rec.Code, rec.Body)
	}

	// el ZIP exportado lleva la evidencia legible: quien exporta ya la ve en la UI
	srv.Store.FinishTest(testID, "FAIL", "boom", "")
	srv.Store.FinishRun(1)
	rec = call(t, srv, "GET", "/api/v1/runs/1/export", "")
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(rec.Code, err)
	}
	found := false
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, filepath.Base(url)) {
			r, _ := f.Open()
			var b bytes.Buffer
			b.ReadFrom(r)
			found = bytes.Equal(b.Bytes(), png)
		}
	}
	if !found {
		t.Fatal("la captura no está (descifrada) en el ZIP")
	}
}

// Sin la clave (o con otra) la evidencia cifrada no se sirve, ni como bytes cifrados.
func TestEvidenceWithoutKeyIsNotServed(t *testing.T) {
	clearAIEnv(t)
	srv, testID := encryptedServer(t)
	url := uploadShotData(t, srv, testID, []byte("\x89PNG\r\n\x1a\nsecreta"))
	for _, v := range []*atrest.Vault{nil, mustVault(t, 9)} {
		srv.Vault = v
		rec := call(t, srv, "GET", url, "")
		if rec.Code != 500 || bytes.Contains(rec.Body.Bytes(), []byte("TRENC")) || strings.Contains(rec.Body.String(), "secreta") {
			t.Fatalf("sin la clave: %d %q", rec.Code, rec.Body)
		}
	}
}

func mustVault(t *testing.T, b byte) *atrest.Vault {
	v, err := atrest.New(bytes.Repeat([]byte{b}, 32), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Con el cifrado apagado nada cambia: el archivo en el disco es el que llegó y el DOM queda en claro.
func TestEvidenceOffIsUnchanged(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	png := []byte("\x89PNG\r\n\x1a\nen claro")
	url := uploadShotData(t, srv, testID, png)
	if raw, _ := os.ReadFile(filepath.Join(srv.ScreenshotsDir, filepath.Base(url))); !bytes.Equal(raw, png) {
		t.Fatal("con el cifrado apagado el archivo cambió")
	}
	call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/dom", `{"url":"https://app.test/","title":"t","elements":[]}`)
	if keys, _ := srv.Store.DOMKeys(); keys[""] != 1 {
		t.Fatalf("DOM: %v", keys)
	}
	if rec := call(t, srv, "GET", url, ""); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("GET: %d", rec.Code)
	}
	for _, bad := range []string{"/screenshots/", "/screenshots/..%2ft.db", "/screenshots/nada.png"} {
		if rec := call(t, srv, "GET", bad, ""); rec.Code != 404 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
}

// Activar el cifrado con capturas viejas en claro: se siguen viendo.
func TestEvidenceOldPlainFilesStillServed(t *testing.T) {
	clearAIEnv(t)
	srv, _ := encryptedServer(t)
	os.WriteFile(filepath.Join(srv.ScreenshotsDir, "vieja.png"), []byte("\x89PNG\r\n\x1a\nvieja"), 0o600)
	if rec := call(t, srv, "GET", "/screenshots/vieja.png", ""); rec.Code != 200 || rec.Body.String() != "\x89PNG\r\n\x1a\nvieja" {
		t.Fatalf("%d", rec.Code)
	}
}

// La captura del fallo viaja descifrada en el ticket.
func TestEvidenceEncryptedTicketImage(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("PUBLIC_URL", "https://reports.acme")
	srv, _ := encryptedServer(t)
	srv.Notify = notify.New(srv.Store)
	gh := &fakeTracker{id: "github"}
	srv.Trackers = []tracker.Provider{gh}
	runID, testID := failingRun(t, srv)
	srv.shots().WriteFile("pago.png", []byte("\x89PNG\r\n\x1a\nshot"))
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "lang": "es"}, 201)
	if string(gh.issues[0].Image) != "\x89PNG\r\n\x1a\nshot" {
		t.Fatal("the failure screenshot travels decrypted with the issue")
	}
}
