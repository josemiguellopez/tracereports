package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// isolateTemp hace que os.CreateTemp use una carpeta propia del test, para ver qué queda en ella.
func isolateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, dir)
	}
	return dir
}

func runCount(t *testing.T, srv *Server) int {
	t.Helper()
	var runs []map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs", "").Body.Bytes(), &runs)
	return len(runs)
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAllureImportStreamsToATempFileAndCleansIt(t *testing.T) {
	tmp := isolateTemp(t)
	srv, _ := newTestServer(t)
	// válido, en el body y como multipart
	if _, rec := importAllureZip(t, srv, "", allureZip(t, "", nil), "application/zip"); rec.Code != http.StatusCreated {
		t.Fatalf("body: %d %s", rec.Code, rec.Body)
	}
	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	mw.WriteField("note", "a field before the file")
	fw, _ := mw.CreateFormFile("file", "allure-results.zip")
	fw.Write(allureZip(t, "allure-results/", nil))
	mw.Close()
	if _, rec := importAllureZip(t, srv, "", mp.Bytes(), mw.FormDataContentType()); rec.Code != http.StatusCreated {
		t.Fatalf("multipart: %d %s", rec.Code, rec.Body)
	}
	// inválido: también se limpia
	if _, rec := importAllureZip(t, srv, "", []byte("not a zip"), "application/zip"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid: %d", rec.Code)
	}
	if left := leftovers(t, tmp); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestAllureImportDecompressedBudget(t *testing.T) {
	tmp := isolateTemp(t)
	srv, _ := newTestServer(t)
	before := runCount(t, srv)

	defer func(total, js int64) { maxAllureUnzipped, maxAllureJSON = total, js }(maxAllureUnzipped, maxAllureJSON)
	// un adjunto que se comprime muy bien (como una bomba ZIP, en chico): el ZIP pesa poco
	big := strings.Repeat("A", 3<<20)
	zipped := allureZip(t, "", map[string]string{"zz-big-attachment.txt": big,
		"zz-result.json": `{"uuid":"zz","name":"big","status":"passed","attachments":[{"name":"big","source":"zz-big-attachment.txt","type":"text/plain"}]}`})
	if len(zipped) > 1<<20 {
		t.Fatalf("fixture should compress well: %d", len(zipped))
	}
	maxAllureUnzipped, maxAllureJSON = 2<<20, 1<<20
	_, rec := importAllureZip(t, srv, "", zipped, "application/zip")
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "decompress") {
		t.Fatalf("over the decompressed budget: %d %s", rec.Code, rec.Body)
	}
	// el adjunto se lee después de crear la ejecución: queda cerrada como incompleta (igual que
	// cualquier importación que falla a mitad), nunca en curso
	var runs []map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/runs", "").Body.Bytes(), &runs)
	if len(runs) != before+1 || runs[0]["status"] == "RUNNING" || runs[0]["incomplete"] != true {
		t.Fatalf("the aborted import must be closed as incomplete: %+v", runs[0])
	}
	// presupuesto de JSON: falla al leer, antes de crear la ejecución
	before = runCount(t, srv)
	maxAllureUnzipped, maxAllureJSON = 1<<30, 100
	if _, rec := importAllureZip(t, srv, "", allureZip(t, "", nil), "application/zip"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the JSON budget: %d %s", rec.Code, rec.Body)
	}
	if runCount(t, srv) != before {
		t.Fatal("rejected while parsing: no run is created")
	}
	if left := leftovers(t, tmp); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestAllureImportWaitsForASlotAndGivesUpWhenTheClientLeaves(t *testing.T) {
	srv, _ := newTestServer(t)
	before := runCount(t, srv)
	for i := 0; i < cap(allureSlots); i++ { // todas ocupadas
		allureSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(allureSlots); i++ {
			<-allureSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import/allure", bytes.NewReader(allureZip(t, "", nil))).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/zip")
	done := make(chan struct{})
	go func() { srv.Router().ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	cancel()
	<-done
	if runCount(t, srv) != before {
		t.Fatal("an import that never got a slot must not create a run")
	}
}

// el tamaño que declara el ZIP no cuenta: se mide lo que se lee
func TestAllureImportDoesNotTrustDeclaredSizes(t *testing.T) {
	isolateTemp(t)
	srv, _ := newTestServer(t)
	defer func(total int64) { maxAllureUnzipped = total }(maxAllureUnzipped)
	maxAllureUnzipped = 1 << 20
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "1-result.json", Method: zip.Deflate, UncompressedSize64: 10})
	w.Write([]byte(`{"uuid":"1","name":"t","status":"passed","description":"` + strings.Repeat("d", 2<<20) + `"}`))
	zw.Close()
	if _, rec := importAllureZip(t, srv, "", buf.Bytes(), "application/zip"); rec.Code == http.StatusCreated {
		t.Fatalf("2 MiB decompressed must not pass a 1 MiB budget: %d", rec.Code)
	}
}
