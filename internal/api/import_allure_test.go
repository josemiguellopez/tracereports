package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allureZip zips the allure-results fixture (prefix: folder inside the ZIP, "" = at its root).
func allureZip(t *testing.T, prefix string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	root := filepath.Join("..", "allure", "testdata", "allure-results")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(root, e.Name()))
		w, _ := zw.Create(prefix + e.Name())
		w.Write(data)
	}
	for name, content := range extra {
		w, _ := zw.Create(prefix + name)
		w.Write([]byte(content))
	}
	zw.Close()
	return buf.Bytes()
}

func importAllureZip(t *testing.T, srv *Server, query string, body []byte, contentType string) (importResult, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import/allure"+query, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var out importResult
	if rec.Code == http.StatusCreated {
		json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return out, rec
}

func TestImportAllure(t *testing.T) {
	srv, _ := newTestServer(t)
	res, rec := importAllureZip(t, srv, "?project=shop&branch=dev", allureZip(t, "", nil), "application/zip")
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if res.Tests != 4 || res.Passed != 1 || res.Failed != 2 || res.Skipped != 1 || res.Status != "FAIL" {
		t.Fatalf("counts: %+v", res)
	}
	run, _ := srv.Store.GetRun(res.RunID)
	// nombre del build (executor.json) y framework de los labels
	if run.Name != "Nightly #42" || run.Framework != "pytest" || run.Project != "shop" {
		t.Fatalf("run: %+v", run)
	}
	if run.StartedAt != 1790000000000 || run.EndedAt == nil || *run.EndedAt != 1790000007000 {
		t.Fatalf("run times from the results: %d %v", run.StartedAt, run.EndedAt)
	}
	tests := runTests(t, srv, res.RunID)
	pay := tests["test_pay_with_card"]
	if pay.Status != "FAIL" || pay.ErrorMessage != "AssertionError: expected 200, got 500" || !strings.Contains(pay.ErrorTrace, "test_checkout.py:42") {
		t.Fatalf("failed test: %+v", pay)
	}
	if pay.Key != "tests.test_checkout.TestPay#test_pay_with_card [card=visa]" || pay.Suite != "tests / test_checkout / TestPay" ||
		pay.Params != "card=visa" || pay.Category != "smoke,Checkout,Pago" || pay.Description != "El pago con tarjeta confirma la orden" {
		t.Fatalf("identity and labels: %+v", pay)
	}
	if pay.StartedAt != 1790000000000 || *pay.EndedAt != 1790000004000 {
		t.Fatalf("test times: %d %v", pay.StartedAt, pay.EndedAt)
	}
	var steps []string
	var shot string
	for _, l := range pay.Logs {
		steps = append(steps, l.Status+" "+strings.SplitN(l.Message, "\n", 2)[0])
		if l.Screenshot != "" {
			shot = l.Screenshot
		}
	}
	want := []string{
		"PASS Abrir el carrito",
		"PASS   Login", // anidado: indentado
		"FAIL Pagar: 500 en /api/pay",
		"FAIL Pantalla al fallar", // la captura del paso que falló
		"INFO log de red:",        // adjunto de texto con su contenido
		"FAIL AssertionError: expected 200, got 500",
	}
	if strings.Join(steps, "|") != strings.Join(want, "|") {
		t.Fatalf("steps:\n%s", strings.Join(steps, "\n"))
	}
	if shot == "" {
		t.Fatal("the screenshot was not stored")
	}
	if _, err := os.Stat(filepath.Join(srv.ScreenshotsDir, filepath.Base(shot))); err != nil {
		t.Fatalf("screenshot file: %v", err)
	}
	if tests["test_db"].Status != "FAIL" || tests["test_refund"].Status != "SKIP" {
		t.Fatalf("broken and skipped: %+v / %+v", tests["test_db"], tests["test_refund"])
	}
	if c := tests["test_coupon"]; c.Status != "PASS" || c.Attempts != 2 {
		t.Fatalf("passed on retry: %+v", c)
	}
}

func TestImportAllureZipWithFolderAndMultipart(t *testing.T) {
	srv, _ := newTestServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "allure.zip")
	fw.Write(allureZip(t, "allure-results/", nil))
	mw.Close()
	res, rec := importAllureZip(t, srv, "?name=Manual&framework=junit5", body.Bytes(), mw.FormDataContentType())
	if rec.Code != http.StatusCreated || res.Tests != 4 {
		t.Fatalf("multipart: %d %s", rec.Code, rec.Body)
	}
	run, _ := srv.Store.GetRun(res.RunID)
	if run.Name != "Manual" || run.Framework != "junit5" {
		t.Fatalf("query wins: %+v", run)
	}
}

func TestImportAllureMasksSecrets(t *testing.T) {
	srv, _ := newTestServer(t)
	secret := `{"name":"login password=HUNTER2SECRET","fullName":"t#login","status":"failed",
		"statusDetails":{"message":"401 Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.SIGSECRET"},
		"parameters":[{"name":"token","value":"PARAMSECRET"}],
		"steps":[{"name":"enviar {\"password\":\"STEPSECRET\"}","status":"passed"}],
		"attachments":[{"name":"req","source":"s-attachment.json","type":"application/json"}]}`
	zipped := allureZip(t, "", map[string]string{"zz-result.json": secret, "s-attachment.json": `{"api_key":"ATTSECRET"}`})
	res, rec := importAllureZip(t, srv, "", zipped, "application/zip")
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	all, _ := json.Marshal(runTests(t, srv, res.RunID))
	for _, s := range []string{"HUNTER2SECRET", "SIGSECRET", "PARAMSECRET", "STEPSECRET", "ATTSECRET"} {
		if strings.Contains(string(all), s) {
			t.Errorf("secret %s was stored", s)
		}
	}
}

func TestImportAllureRejectsBadInput(t *testing.T) {
	srv, _ := newTestServer(t)
	var empty bytes.Buffer
	zw := zip.NewWriter(&empty)
	w, _ := zw.Create("readme.txt")
	w.Write([]byte("hi"))
	zw.Close()
	for name, tc := range map[string]struct {
		body []byte
		code int
	}{
		"not a zip":      {[]byte("<testsuite/>"), 400},
		"zip without it": {empty.Bytes(), 400},
		"invalid result": {allureZip(t, "", map[string]string{"bad-result.json": "{"}), 400},
	} {
		if _, rec := importAllureZip(t, srv, "", tc.body, "application/zip"); rec.Code != tc.code {
			t.Errorf("%s: got %d %s", name, rec.Code, rec.Body)
		}
	}
	runs, _ := srv.Store.ListRuns(50)
	if len(runs) != 1 {
		t.Fatalf("invalid imports must not create runs: %d", len(runs))
	}
}

func TestImportAllureRequiresTheWriteToken(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "s3cret"}
	if _, rec := importAllureZip(t, srv, "", allureZip(t, "", nil), "application/zip"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without token: %d", rec.Code)
	}
}
