package api

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseScreenshotPolicy(t *testing.T) {
	for in, want := range map[string]string{"": "all", "all": "all", " ALL ": "all", "failures": "failures", "Off": "off"} {
		if got, err := ParseScreenshotPolicy(in); err != nil || got != want {
			t.Errorf("%q → %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"none", "fail", "false", "0"} {
		if _, err := ParseScreenshotPolicy(bad); err == nil || !strings.Contains(err.Error(), "all, failures or off") {
			t.Errorf("%q debe ser un error: %v", bad, err)
		}
	}
}

// storedFiles lists what the server wrote to ScreenshotsDir besides the database.
func storedFiles(t *testing.T, srv *Server) []string {
	t.Helper()
	entries, err := os.ReadDir(srv.ScreenshotsDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "t.db") {
			out = append(out, e.Name())
		}
	}
	return out
}

func uploadShot(t *testing.T, srv *Server, testID int64, status string) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", "s.png")
	fw.Write([]byte("\x89PNG\r\n\x1a\n0000"))
	if status != "" {
		mw.WriteField("status", status)
	}
	mw.Close()
	return call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/screenshot", b.String(), header("Content-Type", mw.FormDataContentType()))
}

// Con TRACEREPORTS_SCREENSHOTS=off ninguna imagen (ni trace ni video) llega al disco, aunque el
// test falle o el cliente la mande como captura del fallo.
func TestScreenshotPolicyOffStoresNothing(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	srv.Screenshots = ScreenshotsOff
	srv.Store.FinishTest(testID, "FAIL", "boom", "")
	for _, status := range []string{"", "INFO", "FAIL"} {
		if rec := uploadShot(t, srv, testID, status); rec.Code != 403 || !strings.Contains(rec.Body.String(), "TRACEREPORTS_SCREENSHOTS=off") {
			t.Fatalf("status %q: %d %s", status, rec.Code, rec.Body)
		}
	}
	for _, a := range []struct {
		kind string
		data []byte
	}{{"trace", traceZip}, {"video", webm}} {
		if rec := uploadArtifact(t, srv, testID, a.kind, "", a.data); rec.Code != 403 {
			t.Fatalf("%s: %d %s", a.kind, rec.Code, rec.Body)
		}
	}
	// Allure: el import sigue (pasos, errores), pero sin las capturas
	res, rec := importAllureZip(t, srv, "", allureZip(t, "", nil), "application/zip")
	if rec.Code != 201 || res.Tests != 4 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if files := storedFiles(t, srv); len(files) != 0 {
		t.Fatalf("no debe quedar ningún archivo: %v", files)
	}
	got, _ := srv.Store.GetTest(testID)
	for _, l := range got.Logs {
		if l.Screenshot != "" {
			t.Fatalf("un paso con captura: %+v", l)
		}
	}
}

// Con failures solo se guardan las capturas de un paso o un test que falló.
func TestScreenshotPolicyFailures(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	srv.Screenshots = ScreenshotsFailures
	// test en curso: un paso INFO no se guarda, la captura del fallo sí
	if rec := uploadShot(t, srv, testID, "INFO"); rec.Code != 403 || !strings.Contains(rec.Body.String(), "failures") {
		t.Fatalf("INFO en un test en curso: %d %s", rec.Code, rec.Body)
	}
	if files := storedFiles(t, srv); len(files) != 0 {
		t.Fatalf("rechazada sin archivo: %v", files)
	}
	if rec := uploadArtifact(t, srv, testID, "video", "", webm); rec.Code != 403 {
		t.Fatalf("video de un test que no falló: %d", rec.Code)
	}
	if rec := uploadShot(t, srv, testID, "FAIL"); rec.Code != 201 {
		t.Fatalf("captura del fallo: %d %s", rec.Code, rec.Body)
	}
	// el test ya falló: sus capturas y su video se guardan
	srv.Store.FinishTest(testID, "FAIL", "boom", "")
	if rec := uploadShot(t, srv, testID, ""); rec.Code != 201 {
		t.Fatalf("captura de un test fallido: %d %s", rec.Code, rec.Body)
	}
	if rec := uploadArtifact(t, srv, testID, "video", "", webm); rec.Code != 201 {
		t.Fatalf("video de un test fallido: %d %s", rec.Code, rec.Body)
	}
	if files := storedFiles(t, srv); len(files) != 3 {
		t.Fatalf("dos capturas y un video: %v", files)
	}

	// Allure: solo las capturas de los tests que fallaron
	res, rec := importAllureZip(t, srv, "", allureZip(t, "", nil), "application/zip")
	if rec.Code != 201 {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	kept := 0
	for name, tt := range runTests(t, srv, res.RunID) {
		full, _ := srv.Store.GetTest(tt.ID)
		for _, l := range full.Logs {
			if l.Screenshot == "" {
				continue
			}
			kept++
			if full.Status != "FAIL" && l.Status != "FAIL" {
				t.Fatalf("%s (%s) guardó la captura de un paso %s", name, full.Status, l.Status)
			}
		}
	}
	if kept == 0 {
		t.Fatal("la captura del test que falló sí se guarda")
	}
}

// Sin política (o all) todo sigue como antes.
func TestScreenshotPolicyAllKeepsEverything(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	for _, p := range []string{"", ScreenshotsAll} {
		srv.Screenshots = p
		if rec := uploadShot(t, srv, testID, "INFO"); rec.Code != 201 {
			t.Fatalf("%q: %d %s", p, rec.Code, rec.Body)
		}
	}
	if rec := uploadArtifact(t, srv, testID, "trace", "", traceZip); rec.Code != 201 {
		t.Fatalf("trace: %d", rec.Code)
	}
}
