package mcp

import "encoding/json"

// JSON-RPC 2.0 wire types (stdlib encoding/json only). MCP's stdio
// transport frames one JSON-RPC message per line (newline-delimited, no
// embedded newlines) — see server.go's Serve.

// rpcRequest is one incoming JSON-RPC 2.0 request or notification. A
// notification (no ID) gets no response, per the JSON-RPC 2.0 spec.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is one outgoing JSON-RPC 2.0 response (result XOR error).
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object. Codes follow the JSON-RPC 2.0
// reserved range (-32700 parse error, -32600 invalid request, -32601
// method not found, -32602 invalid params, -32603 internal error).
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// --- MCP-specific payload shapes -----------------------------------------

// initializeResult is `initialize`'s minimal result (protocolVersion +
// capabilities + serverInfo — the fields every MCP client checks before
// proceeding to tools/list).
type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// toolDescriptor is one `tools/list` entry.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// toolsListResult is `tools/list`'s result.
type toolsListResult struct {
	Tools []toolDescriptor `json:"tools"`
}

// toolsCallParams is `tools/call`'s params shape.
type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// contentBlock is one `tools/call` result content entry (MCP's own
// content-block shape; this server only ever emits "text" blocks).
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolsCallResult is `tools/call`'s result: structured data (envelope +
// folded state, or the write result) PLUS the body verbatim as its own
// content block when the tool has one (§7.7 structured-returns contract,
// AC #2) — never markdown-only.
type toolsCallResult struct {
	Content           []contentBlock `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}
