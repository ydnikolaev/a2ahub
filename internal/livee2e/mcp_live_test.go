//go:build livee2e

package livee2e

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

type recordingWriteCloser struct {
	bytes.Buffer
}

func (w *recordingWriteCloser) Close() error { return nil }

func protocolSession(response string) (*mcpSession, *recordingWriteCloser) {
	in := &recordingWriteCloser{}
	return &mcpSession{
		in: in, out: bufio.NewScanner(strings.NewReader(response + "\n")), nextID: 1,
	}, in
}

func TestMCPRPCValidatesResponseEnvelope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response string
		wantErr  string
	}{
		{"valid", `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`, ""},
		{"malformed", `{`, "decode JSON-RPC response"},
		{"wrong protocol", `{"jsonrpc":"1.0","id":1,"result":{}}`, "invalid JSON-RPC response shape"},
		{"wrong id", `{"jsonrpc":"2.0","id":2,"result":{}}`, "invalid JSON-RPC response shape"},
		{"missing result", `{"jsonrpc":"2.0","id":1}`, "neither result nor error"},
		{"protocol error", `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad params"}}`, "JSON-RPC error -32602"},
		{"result and error", `{"jsonrpc":"2.0","id":1,"result":{},"error":{"code":-32603,"message":"broken"}}`, "result and error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session, written := protocolSession(tc.response)
			_, err := session.rpc(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("rpc: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("rpc error=%v, want containing %q", err, tc.wantErr)
			}
			if !strings.Contains(written.String(), `"method":"tools/call"`) {
				t.Fatalf("request not written as JSON line: %q", written.String())
			}
		})
	}
}

func TestMCPCallDistinguishesToolRefusalFromProtocolFailure(t *testing.T) {
	t.Parallel()
	session, _ := protocolSession(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"[CC-002] refused"}],"structuredContent":{"code":"CC-002"},"isError":true}}`)
	call, err := session.Call("a2a_submit", map[string]any{"ids": []string{"XA-bravo-20260729-ab12"}})
	if err != nil {
		t.Fatalf("tool refusal must remain a result, not a protocol error: %v", err)
	}
	if !call.IsError || !strings.Contains(mcpText(call), "CC-002") {
		t.Fatalf("tool refusal shape lost: %+v", call)
	}

	broken, _ := protocolSession(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"missing tool"}}`)
	if _, err := broken.Call("missing", nil); err == nil || !strings.Contains(err.Error(), "JSON-RPC error") {
		t.Fatalf("protocol error=%v, want JSON-RPC error", err)
	}
}

func TestMCPCallRequiresContentArray(t *testing.T) {
	t.Parallel()
	session, _ := protocolSession(`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"ok":true},"isError":false}}`)
	if _, err := session.Call("a2a_read", map[string]any{"view": "inbox"}); err == nil {
		t.Fatal("missing content array must be rejected")
	}
}

var _ io.WriteCloser = (*recordingWriteCloser)(nil)
