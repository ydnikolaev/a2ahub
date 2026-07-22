package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// HandlerFunc is one tool's implementation. args is the raw JSON
// `arguments` object from a `tools/call` request (already extracted, not
// re-wrapped). It returns:
//   - result: the structured data to marshal into StructuredContent (the
//     §7.7 "envelope + folded state as structured data" contract);
//   - body: the artifact body verbatim, when the tool has one (e.g.
//     a2a_show) — emitted as its own text content block, never folded into
//     result/prose. Empty for tools with no single body (read-list tools,
//     write tools).
//   - err: a non-nil error is surfaced as an isError:true tools/call result
//     (never a JSON-RPC protocol-level error — a tool failure is a normal,
//     well-formed MCP response, per the MCP spec's own error convention).
type HandlerFunc func(ctx context.Context, args json.RawMessage) (result any, body string, err error)

// ToolSpec is one registered tool: its §7.7 name, description, embedded
// JSON input schema, and handler.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handler     HandlerFunc
}

// Registry is the mcp tool registry: name -> ToolSpec. It is the SSOT the
// server's tools/list and tools/call methods read, and the ONE thing the
// cmd/a2a parity test enumerates (ToolNames) to prove the §7.7 bijection.
type Registry struct {
	tools map[string]ToolSpec
	order []string
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]ToolSpec{}}
}

// Register adds spec to the registry. A duplicate Name is a programmer
// error (panic, caught at server-construction time — never silently
// overwritten, which would make the §7.7 bijection unreliable).
func (r *Registry) Register(spec ToolSpec) {
	if spec.Name == "" {
		panic("mcp: Registry.Register: empty tool name")
	}
	if spec.Handler == nil {
		panic(fmt.Sprintf("mcp: Registry.Register: tool %q has a nil handler", spec.Name))
	}
	if _, exists := r.tools[spec.Name]; exists {
		panic(fmt.Sprintf("mcp: Registry.Register: tool %q already registered", spec.Name))
	}
	r.tools[spec.Name] = spec
	r.order = append(r.order, spec.Name)
}

// Get returns the named tool's spec, or ok=false if unregistered.
func (r *Registry) Get(name string) (ToolSpec, bool) {
	spec, ok := r.tools[name]
	return spec, ok
}

// ToolNames returns every registered tool name, sorted — the exported seam
// cmd/a2a's parity test enumerates (spec 14 §8 AC 1/3/6).
func (r *Registry) ToolNames() []string {
	names := make([]string, len(r.order))
	copy(names, r.order)
	sort.Strings(names)
	return names
}

// List returns every registered ToolSpec, sorted by name (tools/list's own
// deterministic order).
func (r *Registry) List() []ToolSpec {
	names := r.ToolNames()
	out := make([]ToolSpec, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}
