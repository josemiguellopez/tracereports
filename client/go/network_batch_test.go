package tracereports

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// networkServer acepta lotes de red como el servidor real: hasta 48 MiB por request (si no, 400).
type networkServer struct {
	mu      sync.Mutex
	sizes   []int64
	conns   []Conn
	refused int
}

func newNetworkServer(t *testing.T) (*networkServer, *Client) {
	ns := &networkServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/runs":
			w.Write([]byte(`{"run_id":7}`))
		case strings.HasSuffix(r.URL.Path, "/tests"):
			w.Write([]byte(`{"test_id":3}`))
		case strings.HasSuffix(r.URL.Path, "/network"):
			var in struct{ Connections []Conn }
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 48<<20)).Decode(&in); err != nil {
				ns.mu.Lock()
				ns.refused++
				ns.mu.Unlock()
				http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
				return
			}
			ns.mu.Lock()
			ns.sizes = append(ns.sizes, r.ContentLength)
			ns.conns = append(ns.conns, in.Connections...)
			ns.mu.Unlock()
			w.Write([]byte(`{}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return ns, New(srv.URL)
}

func startNetworkTest(t *testing.T, c *Client) *Test {
	t.Helper()
	if _, err := c.StartRun("suite", "qa"); err != nil {
		t.Fatal(err)
	}
	tc, err := c.StartTest("Red", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return tc
}

func urlsOf(conns []Conn) []string {
	out := make([]string, len(conns))
	for i, c := range conns {
		out[i] = c.URL
	}
	return out
}

func TestNetworkBatchesFitTheServerLimit(t *testing.T) {
	ns, c := newNetworkServer(t)
	tc := startNetworkTest(t, c)
	// 200 conexiones con bodies de 256 KB: un solo request de ~52 MB antes del arreglo
	conns := make([]Conn, 200)
	for i := range conns {
		conns[i] = Conn{Method: "GET", URL: fmt.Sprintf("https://app/api/%d", i), Status: 200, ResponseBody: strings.Repeat("x", 256<<10)}
	}
	want := urlsOf(conns)
	if err := tc.Network(conns); err != nil {
		t.Fatalf("Network: %v (refused %d)", err, ns.refused)
	}
	if ns.refused != 0 || strings.Join(urlsOf(ns.conns), " ") != strings.Join(want, " ") {
		t.Fatalf("all, once, in order: refused %d, got %d connections", ns.refused, len(ns.conns))
	}
	for _, n := range ns.sizes {
		if n > 48<<20 {
			t.Fatalf("request of %d bytes", n)
		}
	}
}

func TestNetworkBatchesMeasureTheRealJSON(t *testing.T) {
	ns, c := newNetworkServer(t)
	tc := startNetworkTest(t, c)
	// json.Marshal escapa <, >, & y los controles (6 bytes cada uno): el tamaño no es len(body)
	body := strings.Repeat("<\u0001\"\\\n😀ñ&", 20000)
	conns := make([]Conn, 60)
	for i := range conns {
		conns[i] = Conn{Method: "POST", URL: fmt.Sprintf("https://app/api/%d", i), Status: 500, ResponseBody: body,
			PostData: strings.Repeat("é", 30000), RequestHeaders: map[string]string{"x-trace": strings.Repeat("ü", 2000)}}
	}
	want := urlsOf(conns)
	if err := tc.Network(conns); err != nil {
		t.Fatalf("Network: %v", err)
	}
	if len(ns.sizes) < 2 || strings.Join(urlsOf(ns.conns), " ") != strings.Join(want, " ") {
		t.Fatalf("split in order: %d requests, %d connections", len(ns.sizes), len(ns.conns))
	}
	got := ns.conns[3]
	if got.ResponseBody != body { // 240 KB: no se recorta
		t.Errorf("body changed by the split")
	}
	if got.PostData != conns[3].PostData || got.RequestHeaders["x-trace"] != conns[3].RequestHeaders["x-trace"] {
		t.Errorf("values changed by the split")
	}
}

func TestNetworkConnectionTooBigAloneIsSentWithoutBodies(t *testing.T) {
	ns, c := newNetworkServer(t)
	tc := startNetworkTest(t, c)
	huge := Conn{Method: "POST", URL: "https://app/upload", Status: 413, PostData: strings.Repeat("p", 60<<20),
		RequestHeaders: map[string]string{"big": strings.Repeat("h", 1<<20)}}
	if err := tc.Network([]Conn{{URL: "https://app/ok", Status: 200, ResponseBody: "ok"}, huge, {URL: "https://app/ok2", Status: 200}}); err != nil {
		t.Fatalf("Network: %v", err)
	}
	if got := strings.Join(urlsOf(ns.conns), " "); got != "https://app/ok https://app/upload https://app/ok2" {
		t.Fatalf("all connections, in order: %q", got)
	}
	if u := ns.conns[1]; u.PostData != "" || !u.BodyTruncated || u.Status != 413 {
		t.Fatalf("the oversized connection is kept, without bodies: %+v", u.Status)
	}
	if ns.conns[0].ResponseBody != "ok" {
		t.Fatalf("the others are untouched")
	}
}
