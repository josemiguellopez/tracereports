// Escenarios de TraceReports grabados con el cliente de Go: pruebas de API (go test) contra la
// tienda de la demo. `go run ./test/scenarios/record` los ejecuta uno a uno.
package shop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	tracereports "github.com/josemiguellopez/tracereports/client/go"
)

var (
	shop     = getenv("SHOP_URL", "http://127.0.0.1:18180")
	password = getenv("SHOP_PASSWORD", "demo")
	tr       *tracereports.Client
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestMain(m *testing.M) {
	tr = tracereports.New("")
	_, _ = tr.StartRun(getenv("TRACEREPORTS_RUN_NAME", "Shop API · Go"), os.Getenv("TRACEREPORTS_ENV"))
	code := m.Run()
	_ = tr.FinishRun()
	os.Exit(code)
}

// call makes a request to the shop and records it as a network call of the test.
func call(t *tracereports.Test, conns *[]tracereports.Conn, method, path, token, body string) (int, string) {
	req, _ := http.NewRequest(method, shop+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	ms := time.Since(start).Milliseconds()
	c := tracereports.Conn{Method: method, URL: shop + path, StartedAt: start.UnixMilli(), DurationMs: &ms, ResourceType: "fetch",
		RequestHeaders: map[string]string{"content-type": "application/json"}, PostData: body}
	if token != "" {
		c.RequestHeaders["authorization"] = "Bearer " + token
	}
	if err != nil {
		c.Failed, c.ErrorText = true, err.Error()
		*conns = append(*conns, c)
		return 0, ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	c.Status, c.StatusText, c.ResponseBody, c.MimeType = res.StatusCode, http.StatusText(res.StatusCode), string(b), res.Header.Get("Content-Type")
	c.ResponseHeaders = map[string]string{}
	for k := range res.Header {
		c.ResponseHeaders[strings.ToLower(k)] = res.Header.Get(k)
	}
	*conns = append(*conns, c)
	return res.StatusCode, string(b)
}

func TestCatalog(t *testing.T) {
	rt, _ := tr.StartTestWithKey(tracereports.Key("demoshop/api", t.Name()), "Catalog lists the products", "api, catalog", "")
	var conns []tracereports.Conn
	st, body := call(rt, &conns, http.MethodGet, "/api/products", "", "")
	_ = rt.Network(conns)
	if st != http.StatusOK || !strings.Contains(body, "P-1") {
		_ = rt.Finish(tracereports.Fail, fmt.Sprintf("GET /api/products: got %d", st), "")
		t.Fatalf("GET /api/products: got %d", st)
	}
	_ = rt.Pass("GET /api/products → 200 with 3 products")
	_ = rt.Finish(tracereports.Pass, "", "")
}

func TestCreateOrder(t *testing.T) {
	rt, _ := tr.StartTestWithKey(tracereports.Key("demoshop/api", t.Name()), "Create an order", "api, orders", "")
	var conns []tracereports.Conn
	_, body := call(rt, &conns, http.MethodPost, "/api/login", "", fmt.Sprintf(`{"email":"qa@demo.shop","password":%q}`, password))
	var login struct{ AccessToken string }
	_ = json.Unmarshal([]byte(body), &login)
	_ = rt.Pass("POST /api/login → token")
	payload := new(bytes.Buffer)
	_ = json.NewEncoder(payload).Encode(map[string]any{"items": []map[string]any{{"id": "P-3", "qty": 2}}})
	st, body := call(rt, &conns, http.MethodPost, "/api/orders", login.AccessToken, strings.TrimSpace(payload.String()))
	if st != http.StatusCreated {
		msg := fmt.Sprintf("shop_test.go: POST /api/orders: got status %d, want 201: %s", st, body)
		_ = rt.Fail(fmt.Sprintf("POST /api/orders → %d", st))
		_ = rt.Network(conns)
		_ = rt.Finish(tracereports.Fail, msg, msg)
		t.Fatal(msg)
	}
	_ = rt.Pass("POST /api/orders → 201")
	_ = rt.Network(conns)
	_ = rt.Finish(tracereports.Pass, "", "")
}
