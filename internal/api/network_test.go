package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
)

func newTestServer(t *testing.T) (*Server, int64) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runID, _ := store.CreateRun("Red", "")
	testID, _ := store.CreateTest(runID, "Login", "", "")
	t.Setenv("GEMINI_API_KEY", "")
	return &Server{Store: store, AI: ai.New(store), ScreenshotsDir: dir, Web: fstest.MapFS{
		"index.html": {Data: []byte("<!-- tracereports:static-data -->")}, "style.css": {}, "app.js": {},
	}}, testID
}

func do(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestNetworkStoreAndList(t *testing.T) {
	srv, testID := newTestServer(t)
	big := strings.Repeat("x", maxResponseBodyChars+100)
	payload, _ := json.Marshal(map[string]any{"connections": []map[string]any{
		{"method": "get", "url": "https://app/api/ok", "status": 200, "duration_ms": 35, "response_body": `{"ok":true}`},
		{"method": "POST", "url": "https://app/api/save", "status": 500, "status_text": "Internal Server Error", "response_body": big},
		{"method": "GET", "url": "https://app/api/slow", "failed": true, "error_text": "net::ERR_TIMED_OUT"},
	}})

	rec := do(t, srv, http.MethodPost, "/api/v1/tests/1/network", string(payload))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"errors":2`) {
		t.Fatalf("POST network: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, http.MethodGet, "/api/v1/tests/1/network", "")
	var conns []db.NetConn
	if err := json.Unmarshal(rec.Body.Bytes(), &conns); err != nil || len(conns) != 3 {
		t.Fatalf("GET network: %v %d", err, len(conns))
	}
	if conns[0].Method != "GET" || conns[0].Seq != 1 || conns[2].Seq != 3 {
		t.Errorf("normalization/order: %+v", conns[0])
	}
	if !conns[1].BodyTruncated || len(conns[1].ResponseBody) != maxResponseBodyChars || conns[1].BodySize != int64(len(big)) {
		t.Errorf("body cap: truncated=%v len=%d size=%d", conns[1].BodyTruncated, len(conns[1].ResponseBody), conns[1].BodySize)
	}

	test, _ := srv.Store.GetTest(testID)
	if test.NetworkTotal != 3 || test.NetworkErrors != 2 {
		t.Errorf("counters: total=%d errors=%d", test.NetworkTotal, test.NetworkErrors)
	}

	if rec := do(t, srv, http.MethodPost, "/api/v1/tests/99/network", `{"connections":[]}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown test: want 404, got %d", rec.Code)
	}

	// The exported ZIP ships the network as a lazily loaded script.
	rec = do(t, srv, http.MethodGet, "/api/v1/runs/1/export", "")
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		found = found || strings.HasSuffix(f.Name, "/network/test_1.js")
	}
	if !found {
		t.Error("export is missing network/test_1.js")
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	if got := truncate("añb", 2); got != "a" {
		t.Errorf("truncate split a rune: %q", got)
	}
}

func TestNetworkBodyEndpointAndEvidencePaths(t *testing.T) {
	srv, _ := newTestServer(t)
	big := `{"items":[` + strings.Repeat(`"x",`, 6000) + `"end"]}`
	payload, _ := json.Marshal(map[string]any{"connections": []map[string]any{
		{"method": "GET", "url": "https://app/api/catalog", "status": 200, "mime_type": "application/json",
			"response_body": big, "evidence_file": `C:\evidence\network\network_test_001_login_1790.json`},
		{"method": "GET", "url": "https://app/page", "status": 500, "mime_type": "text/html",
			"response_body": "<html><script>alert(1)</script></html>"},
	}})
	if rec := do(t, srv, http.MethodPost, "/api/v1/tests/1/network", string(payload)); rec.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}

	rec := do(t, srv, http.MethodGet, "/api/v1/network/1/body", "")
	if rec.Code != 200 || rec.Body.String() != big || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("json body: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = do(t, srv, http.MethodGet, "/api/v1/network/2/body", "")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || rec.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("html body must be served as sandboxed text, got %q", ct)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/network/99/body", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing connection: want 404, got %d", rec.Code)
	}

	conns, _ := srv.Store.ListNetwork(1)
	if conns[0].EvidenceFile != `C:\evidence\network\network_test_001_login_1790.json` || conns[0].BodyFile != "" {
		t.Errorf("evidence_file/body_file: %+v", conns[0].EvidenceFile)
	}

	// The ZIP ships large bodies as files and points to them from the connection.
	rec = do(t, srv, http.MethodGet, "/api/v1/runs/1/export", "")
	zr, _ := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	var netJS string
	hasBody := false
	for _, f := range zr.File {
		hasBody = hasBody || strings.HasSuffix(f.Name, "/network/bodies/1.json")
		if strings.HasSuffix(f.Name, "/network/test_1.js") {
			rc, _ := f.Open()
			b := new(bytes.Buffer)
			b.ReadFrom(rc)
			rc.Close()
			netJS = b.String()
		}
	}
	if !hasBody || !strings.Contains(netJS, `"body_file":"network/bodies/1.json"`) {
		t.Errorf("export: body file present=%v, referenced=%v", hasBody, strings.Contains(netJS, "body_file"))
	}
}
