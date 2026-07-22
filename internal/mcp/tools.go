package mcp

// tools.go is the registry-construction file: BuildRegistry assembles
// every §7.7-designated tool (read + new + submit + the 19 lifecycle verbs
// + the 6 contract sub-verbs) into one Registry. This is the ONE place
// the full §7.7 tool set is enumerated — cmd/a2a's parity test reads
// Registry.ToolNames() as the other half of its bijection check.

import (
	"encoding/json"
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

var lifecycleSchema = rawSchema(map[string]string{
	"ids": "array", "reason": "string", "reason_code": "string",
	"refs": "array", "findings": "string", "actor": "object",
}, "ids")

// BuildRegistry assembles the full §7.7 tool set. store backs the six
// read tools; write backs every write tool's shared plumbing (funnel,
// mirror, manifest, actor resolution, clock/entropy); newDeps backs
// a2a_new and a2a_contract_new's draft path; submitStaging/legality back
// a2a_submit's own idempotency short-circuit.
func BuildRegistry(store *cache.Store, write WriteDeps, submitStagingDir string, legality *LegalityAdapter, newDeps NewDeps) *Registry {
	r := NewRegistry()

	// --- read tools (§7.7: inbox/outbox/show/thread/search/contracts) ---
	r.Register(ToolSpec{Name: "a2a_inbox", Description: "computed inbox across every connected space", InputSchema: rawSchema(map[string]string{"actionable": "boolean"}), Handler: newInboxHandler(store)})
	r.Register(ToolSpec{Name: "a2a_outbox", Description: "own open items and their states", InputSchema: rawSchema(map[string]string{"attention": "boolean"}), Handler: newOutboxHandler(store)})
	r.Register(ToolSpec{Name: "a2a_show", Description: "artifact + folded state + events + validation flags", InputSchema: rawSchema(map[string]string{"ref": "string"}, "ref"), Handler: newShowHandler(store)})
	r.Register(ToolSpec{Name: "a2a_thread", Description: "conversation view", InputSchema: rawSchema(map[string]string{"thread_id": "string"}, "thread_id"), Handler: newThreadHandler(store)})
	r.Register(ToolSpec{Name: "a2a_search", Description: "search the local cache (hub-less by design)", InputSchema: rawSchema(map[string]string{"query": "string", "type": "string", "space": "string", "state": "string"}), Handler: newSearchHandler(store)})
	r.Register(ToolSpec{Name: "a2a_contracts", Description: "known contracts from the local cache", InputSchema: rawSchema(map[string]string{"provider": "string"}), Handler: newContractsHandler(store)})

	// --- new / submit ----------------------------------------------------
	r.Register(ToolSpec{Name: "a2a_new", Description: "draft one or more new artifacts (items[]) on one thread", InputSchema: rawSchema(map[string]string{"items": "array", "thread": "string"}, "items"), Handler: newNewHandler(newDeps)})

	submitDeps := SubmitDeps{WriteDeps: write, StagingDir: submitStagingDir, Legality: legality}
	r.Register(ToolSpec{Name: "a2a_submit", Description: "validate (V2) and submit staged draft(s); accepts an id array (OP-220 batch) or a single id", InputSchema: rawSchema(map[string]string{"ids": "array"}, "ids"), Handler: newSubmitHandler(submitDeps)})

	// --- lifecycle verbs (OP-211): 15 generic table-driven + 4 bespoke ---
	for _, spec := range LifecycleVerbTable {
		name := "a2a_" + strings.ReplaceAll(spec.Verb, "-", "_")
		r.Register(ToolSpec{Name: name, Description: "lifecycle verb: " + spec.Verb, InputSchema: lifecycleSchema, Handler: newLifecycleHandler(spec, write)})
	}
	r.Register(ToolSpec{Name: "a2a_respond", Description: "respond to one or more parents", InputSchema: rawSchema(map[string]string{"parent_ids": "array", "result": "string", "fields": "object", "body_override": "string", "actor": "object"}, "parent_ids", "result"), Handler: newRespondHandler(write)})
	r.Register(ToolSpec{Name: "a2a_verify", Description: "verify one or more responses (D-024 single-response convenience close)", InputSchema: rawSchema(map[string]string{"targets": "array", "refs": "string", "actor": "object"}, "targets"), Handler: newVerifyHandler(write)})
	r.Register(ToolSpec{Name: "a2a_dispute", Description: "dispute a response", InputSchema: rawSchema(map[string]string{"ids": "array", "reason": "string", "reason_code": "string", "actor": "object"}, "ids", "reason"), Handler: newDisputeHandler(write)})
	r.Register(ToolSpec{Name: "a2a_note", Description: "annotate one or more artifacts (transition-free, D-025)", InputSchema: rawSchema(map[string]string{"ids": "array", "note": "string", "actor": "object"}, "ids", "note"), Handler: newNoteHandler(write)})

	// --- contract family (OP-212/OP-213/OP-221 3rd clause) ---------------
	contractDeps := ContractDeps{WriteDeps: write}
	r.Register(ToolSpec{Name: "a2a_contract_new", Description: "draft a new contract (alias for a2a_new type=contract)", InputSchema: rawSchema(map[string]string{"slug": "string", "fields": "object", "body": "string", "thread": "string", "actor": "object"}, "slug"), Handler: newContractNewHandler(newDeps)})
	r.Register(ToolSpec{Name: "a2a_contract_publish", Description: "publish a contract version (version/bump, digest tree)", InputSchema: rawSchema(map[string]string{"id": "string", "version": "string", "bump": "string", "generated_from_digest": "string", "actor": "object"}, "id"), Handler: newContractPublishHandler(contractDeps)})
	r.Register(ToolSpec{Name: "a2a_contract_deprecate", Description: "deprecate a contract with a linked announcement", InputSchema: rawSchema(map[string]string{"id": "string", "version": "string", "successor": "string", "sunset": "string", "actor": "object"}, "id", "successor", "sunset"), Handler: newContractDeprecateHandler(contractDeps)})
	r.Register(ToolSpec{Name: "a2a_contract_retire", Description: "retire a contract (consumer-ack precondition, override)", InputSchema: rawSchema(map[string]string{"id": "string", "version": "string", "override": "boolean", "actor": "object"}, "id"), Handler: newContractRetireHandler(contractDeps)})
	r.Register(ToolSpec{Name: "a2a_contract_diff", Description: "diff two contract versions", InputSchema: rawSchema(map[string]string{"id": "string", "v1": "string", "v2": "string"}, "id", "v1", "v2"), Handler: newContractDiffHandler(contractDeps)})
	r.Register(ToolSpec{Name: "a2a_contract_verify_export", Description: "verify a local export's digest tree", InputSchema: rawSchema(map[string]string{"local": "string", "ref": "string"}, "local", "ref"), Handler: newContractVerifyExportHandler(contractDeps)})

	return r
}
