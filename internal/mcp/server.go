// Package mcp is a minimal Model Context Protocol server over stdio (JSON-RPC 2.0, one message per
// line): enough for an agent such as Claude Code to list and call tools. `tracereports mcp`
// registers the FixLab tools on it (see cmd/mcp.go and docs/rfc/fixlab.md).
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Protocol versions this server speaks; the newest is offered when the client asks for another.
var versions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one callable tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// Call runs the tool with its arguments and returns the text for the agent. An error is
	// returned to the agent as a tool error (isError), not as a protocol error.
	Call func(ctx context.Context, args json.RawMessage) (string, error) `json:"-"`
}

// Server dispatches requests to its tools.
type Server struct {
	Name, Version string
	Instructions  string
	tools         []Tool
	byName        map[string]*Tool
}

// NewServer creates a server with its tools.
func NewServer(name, version, instructions string, tools ...Tool) *Server {
	s := &Server{Name: name, Version: version, Instructions: instructions, tools: tools, byName: map[string]*Tool{}}
	for i := range s.tools {
		s.byName[s.tools[i].Name] = &s.tools[i]
	}
	return s
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve reads requests from in and writes responses to out until in ends.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var mu sync.Mutex
	write := func(r response) {
		b, _ := json.Marshal(r)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error: " + err.Error()}})
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			if len(req.ID) > 0 {
				write(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{codeInvalidRequest, "invalid request"}})
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // notificación (notifications/initialized, cancelled...): no lleva respuesta
		}
		result, rerr := s.handle(ctx, req)
		if rerr != nil {
			write(response{JSONRPC: "2.0", ID: req.ID, Error: rerr})
			continue
		}
		write(response{JSONRPC: "2.0", ID: req.ID, Result: result})
	}
	return sc.Err()
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := versions[0]
		for _, v := range versions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		res := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
		if s.Instructions != "" {
			res["instructions"] = s.Instructions
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "invalid params: " + err.Error()}
		}
		t := s.byName[p.Name]
		if t == nil {
			return nil, &rpcError{codeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name)}
		}
		if len(p.Arguments) == 0 || string(p.Arguments) == "null" {
			p.Arguments = json.RawMessage("{}")
		}
		text, err := t.Call(ctx, p.Arguments)
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}, nil
	}
	return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
}
