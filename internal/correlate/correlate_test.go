package correlate

import (
	"strings"
	"testing"
)

func TestExtractTraceFormats(t *testing.T) {
	for name, tc := range map[string]struct {
		req, resp map[string]string
		trace     string
	}{
		"w3c request":     {req: map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}, trace: "4bf92f3577b34da6a3ce929d0e0e4736"},
		"w3c response":    {resp: map[string]string{"Traceresponse": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}, trace: "0af7651916cd43dd8448eb211c80319c"},
		"w3c all zeros":   {req: map[string]string{"traceparent": "00-00000000000000000000000000000000-00f067aa0ba902b7-01"}},
		"w3c malformed":   {req: map[string]string{"traceparent": "garbage"}},
		"b3 multi":        {req: map[string]string{"X-B3-TraceId": "463ac35c9f6413ad48485a3953bb6124"}, trace: "463ac35c9f6413ad48485a3953bb6124"},
		"b3 single":       {req: map[string]string{"b3": "80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1"}, trace: "80f198ee56343ba864fe8b2a57d3eff7"},
		"jaeger":          {resp: map[string]string{"uber-trace-id": "5c2d0e3f6a7b8c9d:1a2b3c4d5e6f7a8b:0:1"}, trace: "5c2d0e3f6a7b8c9d"},
		"aws x-ray":       {resp: map[string]string{"X-Amzn-Trace-Id": "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"}, trace: "1-5759e988-bd862e3fe1be46a994272793"},
		"datadog":         {req: map[string]string{"x-datadog-trace-id": "1234567890123456789"}, trace: "1234567890123456789"},
		"google cloud":    {resp: map[string]string{"X-Cloud-Trace-Context": "105445aa7843bc8bf206b12000100000/1;o=1"}, trace: "105445aa7843bc8bf206b12000100000"},
		"generic":         {resp: map[string]string{"X-Trace-Id": "abc-123"}, trace: "abc-123"},
		"response wins":   {req: map[string]string{"traceparent": "00-11111111111111111111111111111111-00f067aa0ba902b7-01"}, resp: map[string]string{"traceparent": "00-22222222222222222222222222222222-00f067aa0ba902b7-01"}, trace: "22222222222222222222222222222222"},
		"nothing":         {req: map[string]string{"Accept": "*/*"}},
		"masked is no id": {resp: map[string]string{"x-trace-id": "<masked>"}},
	} {
		if got := Extract(tc.req, tc.resp).TraceID; got != tc.trace {
			t.Errorf("%s: trace %q, want %q", name, got, tc.trace)
		}
	}
}

func TestExtractRequestID(t *testing.T) {
	ids := Extract(map[string]string{"X-Correlation-Id": "from-page"}, map[string]string{"X-Request-ID": "req-42", "cf-ray": "7d1"})
	if ids.RequestID != "req-42" { // la respuesta (lo que usó el backend) y x-request-id primero
		t.Fatalf("request id: %+v", ids)
	}
	if got := Extract(map[string]string{"X-Correlation-Id": "from-page"}, nil).RequestID; got != "from-page" {
		t.Fatalf("from the request: %q", got)
	}
	if got := Extract(nil, map[string]string{"x-request-id": "has space"}).RequestID; got != "" {
		t.Fatalf("unusable id: %q", got)
	}
	if got := Extract(nil, map[string]string{"x-request-id": strings.Repeat("a", 201)}).RequestID; got != "" {
		t.Fatal("too long")
	}
}

func TestLink(t *testing.T) {
	c := Call{IDs: IDs{TraceID: "4bf92f35", RequestID: "req 42"}, Method: "POST", URL: "https://api.shop.test/v1/pay?x=1",
		Status: 500, StartedAt: 1_790_000_000_000, Duration: 800}
	got := Link("https://grafana/explore?trace={trace_id}&req={request_id}&from={from}&to={to}&h={host}&p={path}&m={method}&s={status}", c)
	want := "https://grafana/explore?trace=4bf92f35&req=req+42&from=1789999880000&to=1790000120800&h=api.shop.test&p=%2Fv1%2Fpay&m=POST&s=500"
	if got != want {
		t.Fatalf("link:\n%s\n%s", got, want)
	}
	iso := Link("kibana?from={from_iso}&to={to_iso}&s={from_s}", c)
	if iso != "kibana?from=2026-09-21T14%3A11%3A20Z&to=2026-09-21T14%3A15%3A20Z&s=1789999880" {
		t.Fatalf("iso: %s", iso)
	}
	if Link("x?t={trace_id}", Call{}) != "" || Link("x?r={request_id}", Call{IDs: IDs{TraceID: "t"}}) != "" {
		t.Fatal("a link that needs a missing id is not offered")
	}
	if Link("x?from={from}", Call{IDs: IDs{TraceID: "t"}}) != "" {
		t.Fatal("no time, no time window")
	}
	if Link("  ", c) != "" || Link("https://logs/static", Call{}) != "https://logs/static" {
		t.Fatal("empty and static templates")
	}
}
