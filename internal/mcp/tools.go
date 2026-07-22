package mcp

// tools.go is the registry-construction file: BuildRegistry assembles the
// P15 capability-grouped tool set (a2a_read + a2a_new + a2a_submit +
// a2a_lifecycle + a2a_exchange + a2a_contract = 6 tools) into one Registry.
// Each grouped tool dispatches a CLOSED action/view enum to the EXISTING
// per-verb handlers (tools_dispatch.go); this file only wires the
// registrations and builds each grouped tool's superset input schema. This
// is the ONE place the grouped tool set is enumerated — cmd/a2a's parity
// test reads Registry.ToolNames() + the tools_dispatch.go enum slices as
// the other half of its capability-parity check.

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// genericSchema is a permissive (additionalProperties allowed) object
// schema — every tool's InputSchema embeds a JSON Schema descriptor per
// §7.7 ("schemas embedded"); this package does not re-implement a JSON
// Schema validator (internal/schema already owns that machinery and this
// phase adds zero new validation rules, R-018) — the schema here is
// documentation for the MCP client, not an enforcement gate: enforcement
// is the handler's own field checks (mirroring the CLI's own flag/usage
// checks) plus the SAME V2 pipeline every write verb already runs.
func rawSchema(props map[string]string, required ...string) json.RawMessage {
	var b strings.Builder
	b.WriteString(`{"type":"object","properties":{`)
	first := true
	for name, typ := range props {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(`"` + name + `":{"type":"` + typ + `"}`)
	}
	b.WriteString(`}`)
	if len(required) > 0 {
		b.WriteString(`,"required":[`)
		for i, r := range required {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"` + r + `"`)
		}
		b.WriteString(`]`)
	}
	b.WriteString(`}`)
	return json.RawMessage(b.String())
}

// groupedSchema builds a capability-grouped tool's superset input-schema
// descriptor: a closed-enum discriminator (discKey, values FROM the
// exported enum slice — the single source) plus the UNION of every folded
// action/view's own fields, with the discriminator marked required. Like
// rawSchema this is documentation for the MCP client, not an enforcement
// gate — each per-verb handler enforces its own per-action required fields
// exactly as P14 shipped.
func groupedSchema(discKey string, enum []string, props map[string]string) json.RawMessage {
	var b strings.Builder
	b.WriteString(`{"type":"object","properties":{`)
	b.WriteString(`"` + discKey + `":{"type":"string","enum":[`)
	for i, e := range enum {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + e + `"`)
	}
	b.WriteString(`]}`)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(`,"` + k + `":{"type":"` + props[k] + `"}`)
	}
	b.WriteString(`},"required":["` + discKey + `"]}`)
	return json.RawMessage(b.String())
}

// BuildRegistry assembles the P15 capability-grouped tool set. store backs
// a2a_read; write backs every write tool's shared plumbing (funnel, mirror,
// manifest, actor resolution, clock/entropy); newDeps backs a2a_new and
// a2a_contract action=new's draft path; submitStaging/legality back
// a2a_submit's own idempotency short-circuit.
func BuildRegistry(store *cache.Store, write WriteDeps, submitStagingDir string, legality *LegalityAdapter, newDeps NewDeps) *Registry {
	r := NewRegistry()
	contractDeps := ContractDeps{WriteDeps: write}
	submitDeps := SubmitDeps{WriteDeps: write, StagingDir: submitStagingDir, Legality: legality}

	// --- a2a_read (view: the 6 folded read tools) ------------------------
	r.Register(ToolSpec{
		Name:        "a2a_read",
		Description: "read the local cache: view=inbox|outbox|show|thread|search|contracts",
		InputSchema: groupedSchema("view", ReadViews, map[string]string{
			"actionable": "boolean", "attention": "boolean", "ref": "string",
			"thread_id": "string", "query": "string", "type": "string",
			"space": "string", "state": "string", "provider": "string",
		}),
		Handler: newReadDispatch(store),
	})

	// --- new / submit (action-free write tools, unchanged) ---------------
	r.Register(ToolSpec{Name: "a2a_new", Description: "draft one or more new artifacts (items[]) on one thread", InputSchema: rawSchema(map[string]string{"items": "array", "thread": "string"}, "items"), Handler: newNewHandler(newDeps)})
	r.Register(ToolSpec{Name: "a2a_submit", Description: "validate (V2) and submit staged draft(s); accepts an id array (OP-220 batch) or a single id", InputSchema: rawSchema(map[string]string{"ids": "array"}, "ids"), Handler: newSubmitHandler(submitDeps)})

	// --- a2a_lifecycle (action: the 15 generic OP-211 verbs) -------------
	r.Register(ToolSpec{
		Name:        "a2a_lifecycle",
		Description: "generic lifecycle transition: action=ack|accept|decline|start|block|unblock|cancel|close|withdraw|supersede|satisfy|approve|reject|verify-pass|verify-fail",
		InputSchema: groupedSchema("action", LifecycleActions, map[string]string{
			"ids": "array", "reason": "string", "reason_code": "string",
			"refs": "array", "findings": "string", "actor": "object",
		}),
		Handler: newLifecycleDispatch(write),
	})

	// --- a2a_exchange (action: respond|verify|dispute|note) --------------
	r.Register(ToolSpec{
		Name:        "a2a_exchange",
		Description: "exchange verbs: action=respond|verify|dispute|note",
		InputSchema: groupedSchema("action", ExchangeActions, map[string]string{
			"parent_ids": "array", "result": "string", "fields": "object",
			"body_override": "string", "targets": "array", "refs": "string",
			"ids": "array", "reason": "string", "reason_code": "string",
			"note": "string", "actor": "object",
		}),
		Handler: newExchangeDispatch(write),
	})

	// --- a2a_contract (action: the 6 contract sub-verbs) -----------------
	r.Register(ToolSpec{
		Name:        "a2a_contract",
		Description: "contract family: action=new|publish|deprecate|retire|diff|verify-export",
		InputSchema: groupedSchema("action", ContractActions, map[string]string{
			"slug": "string", "fields": "object", "body": "string",
			"thread": "string", "id": "string", "version": "string",
			"bump": "string", "generated_from_digest": "string",
			"successor": "string", "sunset": "string", "override": "boolean",
			"v1": "string", "v2": "string", "local": "string",
			"ref": "string", "actor": "object",
		}),
		Handler: newContractDispatch(newDeps, contractDeps),
	})

	return r
}
