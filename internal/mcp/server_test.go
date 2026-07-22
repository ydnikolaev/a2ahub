package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testRegistry() *Registry {
	r := NewRegistry()
	r.Register(ToolSpec{
		Name:        "a2a_echo",
		Description: "echoes its input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, args json.RawMessage) (any, string, error) {
			var in map[string]any
			_ = json.Unmarshal(args, &in)
			return in, "", nil
		},
	})
	r.Register(ToolSpec{
		Name: "a2a_fail",
		Handler: func(_ context.Context, _ json.RawMessage) (any, string, error) {
			return nil, "", errors.New("boom")
		},
	})
	r.Register(ToolSpec{
		Name: "a2a_panic",
		Handler: func(_ context.Context, _ json.RawMessage) (any, string, error) {
			panic("handler exploded")
		},
	})
	r.Register(ToolSpec{
		Name: "a2a_body",
		Handler: func(_ context.Context, _ json.RawMessage) (any, string, error) {
			return map[string]string{"id": "X-1"}, "the verbatim body", nil
		},
	})
	return r
}

func decodeLines(t *testing.T, out *bytes.Buffer) []rpcResponse {
	t.Helper()
	var resps []rpcResponse
	dec := json.NewDecoder(out)
	for {
		var r rpcResponse
		if err := dec.Decode(&r); err != nil {
			break
		}
		resps = append(resps, r)
	}
	return resps
}

func TestServerInitialize(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("unexpected error: %+v", resps[0].Error)
	}
}

func TestServerToolsList(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("unexpected response: %+v", resps)
	}
	raw, _ := json.Marshal(resps[0].Result)
	var listResult toolsListResult
	if err := json.Unmarshal(raw, &listResult); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(listResult.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d: %+v", len(listResult.Tools), listResult.Tools)
	}
}

func TestServerToolsCallSuccess(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"a2a_echo","arguments":{"x":1}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("unexpected response: %+v", resps)
	}
}

func TestServerToolsCallHandlerErrorIsNotRPCError(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	req := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"a2a_fail","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("a tool failure must be a well-formed result with isError, not an RPC error: %+v", resps[0].Error)
	}
	raw, _ := json.Marshal(resps[0].Result)
	var callResult toolsCallResult
	_ = json.Unmarshal(raw, &callResult)
	if !callResult.IsError {
		t.Fatalf("expected isError:true, got %+v", callResult)
	}
}

func TestServerToolsCallUnknownTool(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	req := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"a2a_does_not_exist","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("expected an RPC error for an unknown tool, got %+v", resps)
	}
}

// TestServerMalformedRequestStaysAlive is spec 14 §8 AC #7: a malformed
// JSON-RPC line yields a well-formed error response, and the session
// keeps processing subsequent, well-formed requests — the process never
// crashes.
func TestServerMalformedRequestStaysAlive(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader("{not valid json\n" + `{"jsonrpc":"2.0","id":6,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses (parse error + tools/list), got %d: %+v", len(resps), resps)
	}
	if resps[0].Error == nil || resps[0].Error.Code != codeParseError {
		t.Fatalf("expected a parse-error response first, got %+v", resps[0])
	}
	if resps[1].Error != nil {
		t.Fatalf("expected the second (well-formed) request to succeed, got %+v", resps[1])
	}
}

// TestServerHandlerPanicRecovered proves a handler panic never crashes
// the session (AC #7's spirit extended to handler bugs, not just
// malformed input) — the server recovers and returns an internal-error
// response, then keeps serving.
func TestServerHandlerPanicRecovered(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"a2a_panic","arguments":{}}}` + "\n" +
			`{"jsonrpc":"2.0","id":8,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != codeInternalError {
		t.Fatalf("expected an internal-error response for the panicking tool, got %+v", resps[0])
	}
	if resps[1].Error != nil {
		t.Fatalf("expected the session to keep serving after the panic, got %+v", resps[1])
	}
}

// TestServerNotificationGetsNoResponse: a JSON-RPC notification (no id)
// must never produce a response line.
func TestServerNotificationGetsNoResponse(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no response for a notification, got %q", out.String())
	}
}

func TestServerMethodNotFound(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"resources/list"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeMethodNotFound {
		t.Fatalf("expected method-not-found, got %+v", resps)
	}
}

// TestServerBodyVerbatim proves a tool with a single body (e.g. show)
// emits it as its own content block, not folded into structured JSON.
func TestServerBodyVerbatim(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	req := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"a2a_body","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(req), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	raw, _ := json.Marshal(resps[0].Result)
	var callResult toolsCallResult
	_ = json.Unmarshal(raw, &callResult)
	if len(callResult.Content) != 1 || callResult.Content[0].Text != "the verbatim body" {
		t.Fatalf("expected the body verbatim as a content block, got %+v", callResult.Content)
	}
	if callResult.StructuredContent == nil {
		t.Fatalf("expected structuredContent to also be present")
	}
}

func TestServerCtxCancellation(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := strings.NewReader(`{"jsonrpc":"2.0","id":11,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	err := s.Serve(ctx, in, &out)
	if err == nil {
		t.Fatalf("expected an error from a cancelled context")
	}
}

func TestServerBlankLinesTolerated(t *testing.T) {
	t.Parallel()
	s := NewServer(testRegistry(), "a2a-mcp", "0.0.1-test", nil)
	in := strings.NewReader("\n\n" + `{"jsonrpc":"2.0","id":12,"method":"tools/list"}` + "\n\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 {
		t.Fatalf("expected 1 response ignoring blank lines, got %d", len(resps))
	}
}

// TestServerA2AShowOverStdio is spec 14 §8 AC #2, composed end to end
// (not just the handler in isolation, and not just the server's generic
// body-block mechanism against a fake tool): a REAL a2a_show tool call
// over the actual JSON-RPC stdio transport against a fixture artifact,
// asserting the response is structured JSON (envelope + folded state)
// PLUS the body verbatim as its own content block.
func TestServerA2AShowOverStdio(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-stdio1"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	registry := NewRegistry()
	registry.Register(ToolSpec{Name: "a2a_show", Handler: newShowHandler(testStore(t, mirrorDir))})
	s := NewServer(registry, "a2a-mcp", "0.0.1-test", nil)

	reqLine := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a2a_show","arguments":{"ref":"` + id + `"}}}` + "\n"
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(reqLine), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeLines(t, &out)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("unexpected response: %+v", resps)
	}

	raw, _ := json.Marshal(resps[0].Result)
	var callResult toolsCallResult
	if err := json.Unmarshal(raw, &callResult); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("expected a successful call, got isError:true: %+v", callResult)
	}
	if callResult.StructuredContent == nil {
		t.Fatal("expected structuredContent (envelope + folded state)")
	}
	structuredRaw, _ := json.Marshal(callResult.StructuredContent)
	if !strings.Contains(string(structuredRaw), id) {
		t.Fatalf("expected the structured content to carry the artifact id %q, got: %s", id, structuredRaw)
	}
	if len(callResult.Content) != 1 || !strings.Contains(callResult.Content[0].Text, "body") {
		t.Fatalf("expected the body verbatim as its own content block, got: %+v", callResult.Content)
	}
}

func TestServerNilRegistryPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil registry")
		}
	}()
	NewServer(nil, "a2a-mcp", "0.0.1-test", nil)
}
