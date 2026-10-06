package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

// La llamada fallida lleva su traceparent y su X-Request-Id: la API, el reporte exportado y el
// ticket los muestran con los links a los logs y a la traza.
func TestNetworkCorrelationLinks(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.LogsURL = "https://grafana.acme/explore?q={request_id}&from={from}&to={to}"
	srv.TraceURL = "https://tempo.acme/trace/{trace_id}"
	runID, _ := srv.Store.CreateRun("Checkout", "")
	testID, _ := srv.Store.CreateTest(runID, "pago", "", "")
	dur := int64(500)
	srv.Store.AddNetwork(testID, []db.NetConn{
		{Method: "POST", URL: "https://api.shop/pay", Status: 500, StartedAt: 1_790_000_000_000, DurationMs: &dur,
			RequestHeaders:  map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
			ResponseHeaders: map[string]string{"X-Request-Id": "req-42"}},
		{Method: "GET", URL: "https://api.shop/ok", Status: 200}, // sin ids ni hora: sin links
	})
	srv.Store.FinishTest(testID, "FAIL", "boom", "")
	srv.Store.FinishRun(runID)

	var conns []db.NetConn
	json.Unmarshal(call(t, srv, "GET", "/api/v1/tests/"+itoa(testID)+"/network", "").Body.Bytes(), &conns)
	c := conns[0]
	if c.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || c.RequestID != "req-42" {
		t.Fatalf("ids: %+v", c)
	}
	if c.TraceURL != "https://tempo.acme/trace/4bf92f3577b34da6a3ce929d0e0e4736" ||
		c.LogsURL != "https://grafana.acme/explore?q=req-42&from=1789999880000&to=1790000120500" {
		t.Fatalf("links: %q %q", c.LogsURL, c.TraceURL)
	}
	if conns[1].LogsURL != "" || conns[1].TraceURL != "" || conns[1].TraceID != "" {
		t.Fatalf("a call without ids gets no links: %+v", conns[1])
	}

	// el reporte exportado (sin servidor) conserva los links
	rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(runID)+"/export", "")
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		if strings.Contains(f.Name, "network/test_") {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			found = found || strings.Contains(string(b), "https://tempo.acme/trace/4bf92f3577b34da6a3ce929d0e0e4736")
		}
	}
	if !found {
		t.Fatal("the exported report must keep the trace link")
	}

	// el ticket lleva la traza y los links
	gh := &fakeTracker{id: "github"}
	srv.Trackers = []tracker.Provider{gh}
	ticket(t, srv, map[string]any{"run_id": runID, "test_id": testID, "provider": "github", "lang": "en"}, 201)
	md := tracker.Markdown(gh.issues[0])
	for _, want := range []string{"trace 4bf92f3577b34da6a3ce929d0e0e4736", "request req-42", "trace: https://tempo.acme/trace/", "logs: https://grafana.acme/explore?q=req-42"} {
		if !strings.Contains(md, want) {
			t.Errorf("ticket misses %q:\n%s", want, md)
		}
	}
}
