package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// protocolVersion is the MCP protocol version this minimal server speaks.
const protocolVersion = "2024-11-05"

// Server is the stdio JSON-RPC 2.0 MCP server (OP-216, `a2a mcp`): a thin
// dispatcher over a Registry. It handles exactly the methods a tool-only
// MCP session needs: initialize, tools/list, tools/call. Everything else
// (resources, prompts, sampling) is out of this phase's scope (§7.7 lists
// only tools) and returns "method not found", never a crash.
type Server struct {
	registry *Registry
	name     string
	version  string
	log      *slog.Logger
}

// NewServer constructs a Server over registry. name/version populate the
// initialize response's serverInfo. registry must not be nil (rails
// anti-pattern #10). A nil logger defaults to slog.Default() — this
// package logs protocol-level anomalies (malformed request, decode
// failure) but NEVER writes to stdout (that is the JSON-RPC channel).
func NewServer(registry *Registry, name, version string, log *slog.Logger) *Server {
	if registry == nil {
		panic("mcp: NewServer: nil registry")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{registry: registry, name: name, version: version, log: log}
}

// Serve runs the stdio session: reads one JSON-RPC message per line from
// in, dispatches it, and writes one JSON-RPC response per line to out (a
// notification — no `id` — gets no response, per JSON-RPC 2.0). Serve
// processes one request at a time (the plan's own "one in-flight request
// is fine" allowance) — a handler panic is recovered per-request so a
// single bad tool call can never take the whole session down (AC #7:
// malformed input returns a JSON-RPC error, the process STAYS ALIVE).
// Serve returns nil on a clean EOF (harness closed stdin) or ctx
// cancellation; it never calls os.Exit.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// A committed artifact draft or a large batch of ids can legitimately
	// exceed bufio.Scanner's 64KiB default token size; raise the cap
	// (bounded, not unbounded) rather than silently truncating/erroring a
	// large-but-legitimate tools/call request.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8<<20) // 8 MiB per-line ceiling

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue // blank line between messages — tolerated, not an error
		}
		resp := s.handleLine(ctx, line)
		if resp == nil {
			continue // notification: no response
		}
		if err := writeResponse(out, resp); err != nil {
			return fmt.Errorf("mcp: write response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		// A single frame over the 8 MiB ceiling (bufio.ErrTooLong) ends the
		// session by design: an 8 MiB single request is a protocol violation,
		// not the recoverable malformed-JSON case (AC #7, handled per-line in
		// handleLine) — the stream framing is no longer trustworthy, so we
		// stop rather than guess where the next frame begins.
		return fmt.Errorf("mcp: read request: %w", err)
	}
	return nil
}

// handleLine decodes and dispatches one JSON-RPC message. A malformed
// request (invalid JSON, missing method) yields a well-formed JSON-RPC
// error response with a null id — never a crash (AC #7).
func (s *Server) handleLine(ctx context.Context, line []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.log.Warn("mcp: malformed JSON-RPC request", "error", err)
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{
			Code: codeParseError, Message: "parse error: " + err.Error(),
		}}
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return &rpcResponse{JSONRPC: "2.0", ID: nullIfEmpty(req.ID), Error: &rpcError{
			Code: codeInvalidRequest, Message: "invalid request: missing jsonrpc/method",
		}}
	}

	isNotification := len(req.ID) == 0
	result, rpcErr := s.dispatch(ctx, req)
	if isNotification {
		return nil
	}
	if rpcErr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func nullIfEmpty(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// dispatch recovers from any handler panic (rails: a handler bug must
// never crash the whole stdio session) and maps it to an internal-error
// JSON-RPC response.
func (s *Server) dispatch(ctx context.Context, req rpcRequest) (result any, rpcErr *rpcError) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("mcp: recovered handler panic", "method", req.Method, "panic", r)
			rpcErr = &rpcError{Code: codeInternalError, Message: fmt.Sprintf("internal error: %v", r)}
		}
	}()

	switch req.Method {
	case "initialize":
		return initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities:    capabilities{Tools: &toolsCapability{ListChanged: false}},
			ServerInfo:      serverInfo{Name: s.name, Version: s.version},
		}, nil
	case "notifications/initialized", "ping":
		return map[string]any{}, nil
	case "tools/list":
		descs := make([]toolDescriptor, 0, len(s.registry.List()))
		for _, t := range s.registry.List() {
			descs = append(descs, toolDescriptor{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		return toolsListResult{Tools: descs}, nil
	case "tools/call":
		return s.callTool(ctx, req.Params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
	}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p toolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}
	spec, ok := s.registry.Get(p.Name)
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool: " + p.Name}
	}

	structured, body, err := spec.Handler(ctx, p.Arguments)
	if err != nil {
		return toolsCallResult{
			Content: []contentBlock{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}

	content := make([]contentBlock, 0, 2)
	if body != "" {
		content = append(content, contentBlock{Type: "text", Text: body})
	} else if structured != nil {
		raw, merr := json.Marshal(structured)
		if merr == nil {
			content = append(content, contentBlock{Type: "text", Text: string(raw)})
		}
	}
	return toolsCallResult{Content: content, StructuredContent: structured}, nil
}

func writeResponse(out io.Writer, resp *rpcResponse) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = out.Write(raw)
	return err
}
