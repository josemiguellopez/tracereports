package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func serve(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad response line %q: %v", l, err)
		}
		res = append(res, m)
	}
	return res
}

func testServer() *Server {
	return NewServer("tracereports", "test", "use the tools", Tool{
		Name: "echo", Description: "echoes", InputSchema: map[string]any{"type": "object"},
		Call: func(_ context.Context, args json.RawMessage) (string, error) {
			var a struct{ Text string }
			_ = json.Unmarshal(args, &a)
			if a.Text == "fail" {
				return "", errors.New("it failed")
			}
			return "echo: " + a.Text, nil
		},
	})
}

func TestHandshakeAndTools(t *testing.T) {
	res := serve(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hola"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"text":"fail"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`,
	)
	if len(res) != 5 {
		t.Fatalf("responses: %v (the notification has no response)", res)
	}
	init := res[0]["result"].(map[string]any)
	if init["protocolVersion"] != "2024-11-05" || init["instructions"] != "use the tools" {
		t.Fatalf("initialize: %v", init)
	}
	tools := res[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "echo" || tools[0].(map[string]any)["inputSchema"] == nil {
		t.Fatalf("tools/list: %v", tools)
	}
	text := res[2]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if text != "echo: hola" {
		t.Fatalf("tools/call: %v", res[2])
	}
	if res[3]["result"].(map[string]any)["isError"] != true {
		t.Fatalf("a tool error is a result with isError: %v", res[3])
	}
	if res[4]["result"] == nil {
		t.Fatalf("ping: %v", res[4])
	}
}

func TestProtocolErrors(t *testing.T) {
	res := serve(t, testServer(),
		`not json`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"1.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
	)
	codes := []float64{-32700, -32601, -32602, -32600}
	for i, want := range codes {
		e, _ := res[i]["error"].(map[string]any)
		if e == nil || e["code"] != want {
			t.Errorf("response %d: %v, want code %v", i, res[i], want)
		}
	}
	if v := res[4]["result"].(map[string]any)["protocolVersion"]; v != versions[0] {
		t.Fatalf("unknown version offers the newest: %v", v)
	}
}
