package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func junitFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "junit", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type importResult struct {
	RunID   int64  `json:"run_id"`
	Status  string `json:"status"`
	Tests   int    `json:"tests"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
	Report  string `json:"report"`
}

func importXML(t *testing.T, srv *Server, query, body string, opts ...reqOpt) (importResult, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/import/junit"+query, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/xml")
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var out importResult
	if rec.Code == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, rec
}

func runTests(t *testing.T, srv *Server, runID int64) map[string]db.Test {
	t.Helper()
	d, err := srv.Store.GetRunDetail(runID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]db.Test{}
	for _, tt := range d.Tests {
		full, err := srv.Store.GetTest(tt.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[tt.Name] = *full
	}
	return out
}

func TestImportJUnitPytest(t *testing.T) {
	srv, _ := newTestServer(t)
	res, rec := importXML(t, srv, "?name=Nightly&project=shop&branch=dev&commit=abc123&environment=qa", junitFixture(t, "pytest.xml"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if res.Tests != 4 || res.Passed != 1 || res.Failed != 2 || res.Skipped != 1 || res.Status != "FAIL" {
		t.Fatalf("counts: %+v", res)
	}
	if res.Report != "/#run="+itoa(res.RunID)+"&view=tests" {
		t.Fatalf("report link: %q", res.Report)
	}

	run, err := srv.Store.GetRun(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Name != "Nightly" || run.Project != "shop" || run.Branch != "dev" || run.Commit != "abc123" || run.Environment != "qa" || run.Framework != "junit" {
		t.Fatalf("run context: %+v", run)
	}
	// las horas del reporte, no la de la subida
	suiteStart := time.Date(2026, 10, 6, 10, 0, 0, 123_000_000, time.UTC).UnixMilli()
	if run.StartedAt != suiteStart || run.EndedAt == nil || *run.EndedAt != suiteStart+1200+2001+10+301 {
		t.Fatalf("run times: %d %v", run.StartedAt, run.EndedAt)
	}

	tests := runTests(t, srv, res.RunID)
	ok := tests["test_login_ok"]
	if ok.Status != "PASS" || ok.Key != "tests.test_login#test_login_ok" || ok.Suite != "pytest" || ok.StartedAt != suiteStart || *ok.EndedAt != suiteStart+1200 {
		t.Fatalf("pass test: %+v", ok)
	}
	fail := tests["test_login_admin[chromium]"]
	if fail.Status != "FAIL" || fail.ErrorMessage != "AssertionError: assert 500 == 200" || !strings.Contains(fail.ErrorTrace, "tests/test_login.py:12") {
		t.Fatalf("failed test: %+v", fail)
	}
	if fail.StartedAt != suiteStart+1200 {
		t.Fatalf("tests run one after the other: %d", fail.StartedAt)
	}
	// pasos: la salida estándar y el resultado
	if len(fail.Logs) != 2 || fail.Logs[0].Status != "INFO" || !strings.HasPrefix(fail.Logs[0].Message, "stdout:\nabriendo login") ||
		fail.Logs[1].Status != "FAIL" || fail.Logs[1].Message != fail.ErrorMessage {
		t.Fatalf("steps: %+v", fail.Logs)
	}
	skip := tests["test_search"]
	if skip.Status != "SKIP" || skip.ErrorMessage != "" || len(skip.Logs) != 1 || skip.Logs[0].Message != "Sin datos del mes" {
		t.Fatalf("skipped test: %+v", skip)
	}
	if tests["test_setup"].Status != "FAIL" {
		t.Fatalf("<error> is a failure: %+v", tests["test_setup"])
	}
}

func TestImportJUnitMultipartSeveralFiles(t *testing.T) {
	srv, _ := newTestServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, name := range []string{"pytest.xml", "surefire.xml"} {
		fw, _ := mw.CreateFormFile("file", name)
		fw.Write([]byte(junitFixture(t, name)))
	}
	mw.Close()
	res, rec := importXML(t, srv, "", body.String(), header("Content-Type", mw.FormDataContentType()))
	if rec.Code != http.StatusCreated || res.Tests != 7 || res.Failed != 3 {
		t.Fatalf("multipart: %d %s", rec.Code, rec.Body)
	}
	run, _ := srv.Store.GetRun(res.RunID)
	if run.Name != "JUnit" {
		t.Fatalf("default name with several suites: %q", run.Name)
	}
	tests := runTests(t, srv, res.RunID)
	if tests["loginFlaky"].Status != "PASS" || tests["loginFlaky"].Attempts != 2 || tests["loginBroken"].Attempts != 3 {
		t.Fatalf("attempts: flaky=%+v broken=%+v", tests["loginFlaky"], tests["loginBroken"])
	}
	if tests["loginOk"].Key != "com.acme.LoginTest#loginOk" {
		t.Fatalf("key: %q", tests["loginOk"].Key)
	}
}

func TestImportJUnitSingleSuiteNamesTheRun(t *testing.T) {
	srv, _ := newTestServer(t)
	res, rec := importXML(t, srv, "?framework=maven", junitFixture(t, "surefire.xml"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	run, _ := srv.Store.GetRun(res.RunID)
	if run.Name != "com.acme.LoginTest" || run.Framework != "maven" {
		t.Fatalf("run: %+v", run)
	}
	// sin timestamp en el reporte: termina ahora y dura lo que suman sus tests
	if run.EndedAt == nil || time.Since(time.UnixMilli(*run.EndedAt)) > time.Minute || time.Until(time.UnixMilli(*run.EndedAt)) > time.Second ||
		*run.EndedAt-run.StartedAt != 1_003_750 {
		t.Fatalf("times without timestamp: %d %v", run.StartedAt, run.EndedAt)
	}
}

func TestImportJUnitMasksSecrets(t *testing.T) {
	srv, _ := newTestServer(t)
	doc := `<testsuite name="s"><testcase classname="c" name="login password=HUNTER2SECRET">
		<failure message="401 with Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.SIGNATURESECRET">token=TRACESECRET</failure>
		<system-out>POST /login {"password":"OUTSECRET"}</system-out></testcase></testsuite>`
	res, rec := importXML(t, srv, "?name=run%20token%3DNAMESECRET", doc)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	run, _ := srv.Store.GetRun(res.RunID)
	all, _ := json.Marshal(runTests(t, srv, res.RunID))
	stored := run.Name + string(all)
	for _, s := range []string{"HUNTER2SECRET", "SIGNATURESECRET", "TRACESECRET", "OUTSECRET", "NAMESECRET"} {
		if strings.Contains(stored, s) {
			t.Errorf("secret %s was stored", s)
		}
	}
}

func TestImportJUnitKeepsHistoryAcrossImports(t *testing.T) {
	srv, _ := newTestServer(t)
	first, _ := importXML(t, srv, "", junitFixture(t, "surefire.xml"))
	second, _ := importXML(t, srv, "", junitFixture(t, "surefire.xml"))
	a, b := runTests(t, srv, first.RunID)["loginBroken"], runTests(t, srv, second.RunID)["loginBroken"]
	if a.Key != b.Key {
		t.Fatalf("same test, same identity: %q vs %q", a.Key, b.Key)
	}
	hist, err := srv.Store.TestHistory(b.Key, second.RunID, 10)
	if err != nil || len(hist) < 2 {
		t.Fatalf("history: %v %+v", err, hist)
	}
}

func TestImportJUnitRejectsBadInput(t *testing.T) {
	srv, _ := newTestServer(t)
	for name, tc := range map[string]struct {
		body string
		opts []reqOpt
		code int
	}{
		"not xml":       {body: `{"tests":[]}`, code: 400},
		"html":          {body: `<html></html>`, code: 400},
		"no cases":      {body: `<testsuites><testsuite name="empty"/></testsuites>`, code: 400},
		"no file field": {body: "--b\r\nContent-Disposition: form-data; name=\"other\"\r\n\r\nx\r\n--b--\r\n", opts: []reqOpt{header("Content-Type", "multipart/form-data; boundary=b")}, code: 400},
		"too big":       {body: "<testsuite>" + strings.Repeat(" ", maxImportBody) + "</testsuite>", code: 413},
	} {
		_, rec := importXML(t, srv, "", tc.body, tc.opts...)
		if rec.Code != tc.code {
			t.Errorf("%s: got %d %s, want %d", name, rec.Code, rec.Body, tc.code)
		}
	}
	runs, _ := srv.Store.ListRuns(50)
	if len(runs) != 1 { // solo la de newTestServer: un import inválido no crea nada
		t.Fatalf("invalid imports must not create runs: %d", len(runs))
	}
}

func TestImportJUnitRequiresTheWriteToken(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "s3cret"}
	doc := junitFixture(t, "surefire.xml")
	if _, rec := importXML(t, srv, "", doc); rec.Code != http.StatusUnauthorized {
		t.Fatalf("without token: %d", rec.Code)
	}
	if _, rec := importXML(t, srv, "", doc, header("Authorization", "Bearer s3cret")); rec.Code != http.StatusCreated {
		t.Fatalf("with token: %d %s", rec.Code, rec.Body)
	}
}
