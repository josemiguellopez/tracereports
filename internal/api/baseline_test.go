package api

import (
	"encoding/json"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// runWithCall closes a run of project "shop" with test "checkout" (status) that made one call.
func runWithCall(t *testing.T, srv *Server, branch, status, url string, code int, body string) (runID, testID int64) {
	t.Helper()
	runID, _ = srv.Store.CreateRunWithMeta("E2E", "qa", db.RunMeta{Project: "shop", Branch: branch})
	testID, _ = srv.Store.CreateTestWithMeta(runID, "checkout", "", "", db.TestMeta{Key: "t.py::test_checkout"})
	srv.Store.AddNetwork(testID, []db.NetConn{
		{Method: "GET", URL: "https://api.shop/config", Status: 200},
		{Method: "POST", URL: url, Status: code, ResponseBody: body, ResponseHeaders: map[string]string{"content-type": "application/json"}},
	})
	srv.Store.FinishTest(testID, status, "", "")
	srv.Store.CloseRun(runID, false)
	return runID, testID
}

func baseline(t *testing.T, srv *Server, testID int64) (*db.Baseline, int) {
	t.Helper()
	conns, _ := srv.Store.ListNetwork(testID)
	rec := call(t, srv, "GET", "/api/v1/network/"+itoa(conns[1].ID)+"/baseline", "")
	if rec.Code != 200 {
		return nil, rec.Code
	}
	var b db.Baseline
	json.Unmarshal(rec.Body.Bytes(), &b)
	return &b, 200
}

func TestBaselineIsTheSameCallWhenTheTestPassed(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	old, _ := runWithCall(t, srv, "main", "PASS", "https://api.shop/orders/111/pay", 200, `{"ok":true}`)
	green, _ := runWithCall(t, srv, "main", "PASS", "https://api.shop/orders/222/pay", 201, `{"ok":true,"id":222}`)
	runWithCall(t, srv, "main", "FAIL", "https://api.shop/orders/333/pay", 500, `{"ok":false}`) // falló: no sirve de base
	_, failed := runWithCall(t, srv, "main", "FAIL", "https://api.shop/orders/444/pay", 500, `{"error":"db"}`)

	b, code := baseline(t, srv, failed)
	if code != 200 || b.RunID != green || b.Conn.Status != 201 || b.Conn.URL != "https://api.shop/orders/222/pay" || !b.SameContext {
		t.Fatalf("the latest green run, same call with another id: %d %+v", code, b)
	}
	_ = old
}

func TestBaselineFallsBackToTheProjectAndSaysSo(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	other, _ := runWithCall(t, srv, "main", "PASS", "https://api.shop/orders/1/pay", 200, `{}`)
	_, failed := runWithCall(t, srv, "feature/x", "FAIL", "https://api.shop/orders/2/pay", 500, `{}`)
	b, code := baseline(t, srv, failed)
	if code != 200 || b.RunID != other || b.SameContext {
		t.Fatalf("from another branch: %d %+v", code, b)
	}
}

func TestBaselineNoneOrUnknown(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runWithCall(t, srv, "main", "PASS", "https://api.shop/other/endpoint", 200, `{}`) // otra llamada
	_, failed := runWithCall(t, srv, "main", "FAIL", "https://api.shop/orders/9/pay", 500, `{}`)
	if _, code := baseline(t, srv, failed); code != 204 {
		t.Fatalf("no green run with that call: %d", code)
	}
	if rec := call(t, srv, "GET", "/api/v1/network/99999/baseline", ""); rec.Code != 404 {
		t.Fatalf("unknown call: %d", rec.Code)
	}
}

func TestCallSignature(t *testing.T) {
	for _, c := range [][3]string{
		{"post", "https://API.shop/orders/123/pay?x=1", "POST api.shop/orders/:id/pay"},
		{"GET", "https://api.shop/users/6f1c2a9e-1b2c-4d5e-8f90-123456789abc", "GET api.shop/users/:id"},
		{"GET", "https://api.shop/v2/items", "GET api.shop/v2/items"},
	} {
		if got := db.CallSignature(c[0], c[1]); got != c[2] {
			t.Errorf("CallSignature(%s %s) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
