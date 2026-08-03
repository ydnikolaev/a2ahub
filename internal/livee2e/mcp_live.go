//go:build livee2e

package livee2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

type mcpSession struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Scanner
	stderr bytes.Buffer
	nextID int64
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Structured json.RawMessage `json:"structuredContent"`
	IsError    bool            `json:"isError"`
}

func startMCPSession(ctx context.Context, c *checkout) (*mcpSession, error) {
	cmd := exec.CommandContext(ctx, c.Bin, "mcp") //nolint:gosec // reason: c.Bin is the exact attested candidate binary and the subcommand is fixed.
	cmd.Dir = c.Dir
	cmd.Env = checkoutEnv(c.Token, c.SpaceSlug)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	s := &mcpSession{cmd: cmd, in: in, out: bufio.NewScanner(stdout), nextID: 1}
	s.out.Buffer(make([]byte, 64*1024), 8<<20)
	cmd.Stderr = &s.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	result, err := s.rpc(map[string]any{
		"jsonrpc": "2.0", "id": s.nextID, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "a2ahub-live-e2e", "version": "1"},
		},
	})
	s.nextID++
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil ||
		initialized.ProtocolVersion != "2024-11-05" ||
		initialized.ServerInfo.Name == "" {
		s.Close()
		return nil, fmt.Errorf("mcp initialize returned invalid shape: %s", result)
	}
	return s, nil
}

func (s *mcpSession) rpc(request map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	requestID, err := json.Marshal(request["id"])
	if err != nil || string(requestID) == "null" {
		return nil, fmt.Errorf("JSON-RPC request requires an id")
	}
	if _, err := s.in.Write(append(raw, '\n')); err != nil {
		return nil, fmt.Errorf("write JSON-RPC request: %w", err)
	}
	if !s.out.Scan() {
		if err := s.out.Err(); err != nil {
			return nil, fmt.Errorf("read JSON-RPC response: %w", err)
		}
		return nil, fmt.Errorf("MCP server closed stdout: %s", strings.TrimSpace(s.stderr.String()))
	}
	var response mcpRPCResponse
	if err := json.Unmarshal(s.out.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("decode JSON-RPC response: %w: %s", err, s.out.Text())
	}
	if response.JSONRPC != "2.0" || !bytes.Equal(response.ID, requestID) {
		return nil, fmt.Errorf("invalid JSON-RPC response shape: %s", s.out.Text())
	}
	if response.Error != nil && len(response.Result) > 0 {
		return nil, fmt.Errorf("invalid JSON-RPC response has result and error: %s", s.out.Text())
	}
	if response.Error != nil {
		return nil, fmt.Errorf("JSON-RPC error %d: %s", response.Error.Code, response.Error.Message)
	}
	if len(response.Result) == 0 {
		return nil, fmt.Errorf("invalid JSON-RPC response has neither result nor error: %s", s.out.Text())
	}
	return response.Result, nil
}

func (s *mcpSession) Call(tool string, arguments any) (mcpToolResult, error) {
	result, err := s.rpc(map[string]any{
		"jsonrpc": "2.0", "id": s.nextID, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": arguments},
	})
	s.nextID++
	if err != nil {
		return mcpToolResult{}, err
	}
	var call mcpToolResult
	if err := json.Unmarshal(result, &call); err != nil {
		return mcpToolResult{}, fmt.Errorf("decode tools/call result: %w: %s", err, result)
	}
	if call.Content == nil {
		return mcpToolResult{}, fmt.Errorf("tools/call %s returned no content array", tool)
	}
	return call, nil
}

func (s *mcpSession) Close() {
	if s == nil {
		return
	}
	_ = s.in.Close()
	_ = s.cmd.Wait()
}

func mcpCallOK(s *mcpSession, tool string, arguments any, out any) error {
	call, err := s.Call(tool, arguments)
	if err != nil {
		return err
	}
	if call.IsError {
		return fmt.Errorf("%s returned tool error: %s", tool, mcpText(call))
	}
	if len(call.Structured) == 0 || string(call.Structured) == "null" {
		return fmt.Errorf("%s returned no structuredContent", tool)
	}
	if out != nil {
		if err := json.Unmarshal(call.Structured, out); err != nil {
			return fmt.Errorf("%s structuredContent: %w: %s", tool, err, call.Structured)
		}
	}
	return nil
}

func mcpText(call mcpToolResult) string {
	var parts []string
	for _, block := range call.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type mcpWriteResult struct {
	Verb   string   `json:"verb"`
	IDs    []string `json:"ids"`
	Branch string   `json:"branch"`
	PRURL  string   `json:"pr_url"`
	State  string   `json:"state"`
}

func (r mcpWriteResult) PRNumber() (int, error) {
	u, err := url.Parse(r.PRURL)
	if err != nil {
		return 0, err
	}
	lastSlash := strings.LastIndex(u.Path, "/")
	if lastSlash < 0 || lastSlash == len(u.Path)-1 {
		return 0, fmt.Errorf("MCP write returned unusable pr_url %q", r.PRURL)
	}
	n, err := strconv.Atoi(u.Path[lastSlash+1:])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("MCP write returned unusable pr_url %q", r.PRURL)
	}
	return n, nil
}

func mcpDraft(s *mcpSession, c *checkout, typ, slug string, overrides map[string]string) (string, error) {
	args, ok := draftFieldArgs(typ, c.SpaceSlug, c.System, c.Peer)
	if !ok {
		return "", fmt.Errorf("no draft fields for %s", typ)
	}
	fields := map[string]string{}
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--field" {
			continue
		}
		key, value, found := strings.Cut(args[i+1], "=")
		if found {
			fields[key] = value
		}
		i++
	}
	for key, value := range overrides {
		fields[key] = value
	}
	item := map[string]any{
		"type": typ, "fields": fields,
		"actor": map[string]any{"kind": "agent", "name": "live-e2e"},
	}
	if typ == "decision" {
		item["actor"] = map[string]any{"kind": "human", "name": "live-e2e-operator"}
	}
	if slug != "" {
		item["slug"] = slug
	}
	var drafted []struct {
		ID string `json:"id"`
	}
	if err := mcpCallOK(s, "a2a_new", map[string]any{"items": []any{item}}, &drafted); err != nil {
		return "", err
	}
	if len(drafted) == 0 || drafted[0].ID == "" {
		return "", fmt.Errorf("a2a_new returned no drafted artifact id")
	}
	return drafted[0].ID, nil
}

func mcpDraftAndSubmit(s *mcpSession, c *checkout, typ, slug string, overrides map[string]string) (mcpWriteResult, error) {
	id, err := mcpDraft(s, c, typ, slug, overrides)
	if err != nil {
		return mcpWriteResult{}, err
	}
	var result mcpWriteResult
	if err := mcpCallOK(s, "a2a_submit", map[string]any{"ids": []string{id}}, &result); err != nil {
		return mcpWriteResult{}, err
	}
	if len(result.IDs) != 1 || result.IDs[0] != id {
		return mcpWriteResult{}, fmt.Errorf("a2a_submit ids=%v, want [%s]", result.IDs, id)
	}
	return result, nil
}
