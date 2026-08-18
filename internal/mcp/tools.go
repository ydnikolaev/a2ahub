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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// ErrLegacyContractWriteUnavailable reports an unavailable legacy write path.
var ErrLegacyContractWriteUnavailable = errors.New("mcp: legacy contract write unavailable while space write dependencies are offline")

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

// registerSpaceFree registers every tool that needs NOTHING but the local
// cache — `a2a_read` and `a2a_whatsnew`. It is the single home for both,
// called by BuildRegistry and by wire.go's two degraded paths (no space
// connected yet; the space unreachable at startup).
//
// One home, because two was a fork and the fork shipped. wire.go used to
// carry its own `registerReadOnly`, written before P15 folded the six
// per-verb read tools into `a2a_read`'s `view` enum — and it was never
// migrated. So an agent with no connected space was served `a2a_inbox`,
// `a2a_outbox`, `a2a_show`, `a2a_thread`, `a2a_search` and `a2a_contracts`:
// six names that tools_test's own `removed` list asserts must be ABSENT once
// a space IS connected, and that `skill/a2ahub/reference/commands.md` (the
// skill-drift-gated catalogue an agent actually reads) does not document at
// all. The onboarding case — the first session in a fresh repo — got the one
// tool surface nothing describes, and `a2a_whatsnew` was missing from it
// entirely, so the agent best placed to ask "what changed?" could not.
//
// The invariant this function exists to make structural: the no-space
// surface is a strict SUBSET of the connected one, never a different set of
// names. TestSpaceFreeToolsAreASubsetOfTheConnectedSurface holds it.
func registerSpaceFree(r *Registry, store *cache.Store) {
	r.Register(ToolSpec{
		Name:        "a2a_read",
		Description: "read the local cache: view=inbox|outbox|show|thread|search|contracts",
		InputSchema: groupedSchema("view", ReadViews, map[string]string{
			"actionable": "boolean", "attention": "boolean", "overdue": "boolean",
			"ref": "string", "thread_id": "string", "query": "string", "type": "string",
			"space": "string", "state": "string", "provider": "string",
		}),
		Handler: newReadDispatch(store),
	})
	r.Register(ToolSpec{
		Name:        "a2a_whatsnew",
		Description: "release directives and current known issues — optional since=<version>",
		InputSchema: rawSchema(map[string]string{"since": "string"}),
		Handler: newWhatsnewHandler(
			func() ([]notes.ReleaseNotes, error) { return notes.Load(releasenotes.FS) },
			func() ([]notes.Change, error) { return notes.LoadCurrentKnownIssues(releasenotes.FS) },
		),
	})
}

// BuildRegistry assembles the P15 capability-grouped tool set. store backs
// a2a_read; write backs every write tool's shared plumbing (funnel, mirror,
// manifest, actor resolution, clock/entropy); newDeps backs a2a_new and
// a2a_contract action=new's draft path; submitStaging/legality back
// a2a_submit's own idempotency short-circuit. Production passes exactly one
// cmd/a2a-composed work dependency graph; the omitted form is retained only
// for isolated registry tests and exposes an explicit unavailable backend.
func BuildRegistry(store *cache.Store, write WriteDeps, submitStagingDir string, legality *LegalityAdapter, newDeps NewDeps, workDeps ...WorkToolDeps) *Registry {
	return BuildRegistryWithContractOperations(store, write, submitStagingDir, legality, newDeps, ContractToolOperations{}, workDeps...)
}

// BuildRegistryWithOperations is the full production registry assembly,
// threading BOTH contract and data operations — the seam cmd/a2a uses to
// hand this package a production DataOperations implementation, mirroring
// how contractOperations is already threaded. dataOperations' zero value
// (DataToolDeps{}) registers a2a_data degraded (every action refuses
// "service is not configured", NewDataTool's own precedent), exactly like
// ContractToolOperations{}'s zero value degrades a2a_contract.
//
// BuildRegistryWithContractOperations remains the source-compatible
// constructor for callers not yet supplying data operations (it now calls
// through here with DataToolDeps{}) — it is called by internal/mcp/wire.go
// with a fixed positional argument list (outside this wave's allowlist), so
// this new parameter could not be inserted there without breaking that
// call; a variadic cannot follow another variadic, so this is a new
// function rather than a widened signature.
func BuildRegistryWithOperations(store *cache.Store, write WriteDeps, submitStagingDir string, legality *LegalityAdapter, newDeps NewDeps, contractOperations ContractToolOperations, dataOperations DataToolDeps, workDeps ...WorkToolDeps) *Registry {
	r := NewRegistry()
	if len(workDeps) > 1 {
		panic("mcp: BuildRegistry accepts at most one work dependency set")
	}
	// P37 Wave I: `a2a_contract` publish needs the SAME staging dir
	// `a2a_submit` already receives here, to fold a staged schema/fixture
	// edit into the version it is declaring (ContractDeps.StagingDir's own
	// doc comment) — no new BuildRegistry parameter, this one is already
	// threaded through for submit's own idempotency short-circuit.
	contractDeps := ContractDeps{
		WriteDeps: write, StagingDir: submitStagingDir,
		Publication: contractOperations.Publication, Materialize: contractOperations.Materialize,
		Check: contractOperations.Check, Inspection: contractOperations.Inspection,
	}
	submitDeps := SubmitDeps{WriteDeps: write, StagingDir: submitStagingDir, Legality: legality}

	// --- a2a_read + a2a_whatsnew (need no space; see registerSpaceFree) --
	registerSpaceFree(r, store)
	work := unavailableWorkToolDeps()
	if len(workDeps) == 1 {
		work = workDeps[0]
	}
	workTool, err := NewWorkTool(work)
	if err != nil {
		panic(fmt.Sprintf("mcp: build a2a_work: %v", err))
	}
	r.Register(workTool)

	// --- new / submit (action-free tools, unchanged shape) ---------------
	r.Register(ToolSpec{Name: "a2a_new", Description: "draft one or more new artifacts (items[]) on one thread; an item's fields key may be a dotted path into a nested field, e.g. expected_response.shape", InputSchema: rawSchema(map[string]string{"items": "array", "thread": "string"}, "items"), Handler: newNewHandler(newDeps)})
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

	registerContractTool(r, newDeps, contractDeps)

	dataTool, err := NewDataTool(dataOperations)
	if err != nil {
		panic(fmt.Sprintf("mcp: build a2a_data: %v", err))
	}
	r.Register(dataTool)

	// --- a2a_notify (action: render|send|setup|discover|verify) ----------
	// space-notify-2026-08 P6: registered with the zero value in this pass
	// (Operations == nil, every action answers ErrNotifyOperationsUnavailable)
	// — see tools_notify.go's own doc comment for the reasoning and the
	// follow-up this leaves.
	notifyTool, err := NewNotifyTool(NotifyToolDeps{})
	if err != nil {
		panic(fmt.Sprintf("mcp: build a2a_notify: %v", err))
	}
	r.Register(notifyTool)

	return r
}

// ContractToolOperations is the cmd/a2a-composed P6 dependency set. Keeping
// these consumer-side seams in MCP while constructing their production
// implementations only at cmd/a2a preserves ADR-001's single DI point.
type ContractToolOperations struct {
	Publication ContractPublicationOperations
	Materialize ContractMaterializeOperation
	Check       ContractCheckOperation
	Inspection  ContractInspectionOperations
}

func (o ContractToolOperations) complete() bool {
	return o.Publication != nil && o.Materialize != nil && o.Check != nil && o.Inspection != nil
}

// BuildRegistryWithContractOperations is the production registry assembly
// for callers not yet supplying data operations (internal/mcp/wire.go's own
// call site, fixed-arity, outside this wave's allowlist) — it delegates to
// BuildRegistryWithOperations with DataToolDeps{} (a2a_data registered
// degraded). BuildRegistry remains the source-compatible isolated-test
// constructor.
func BuildRegistryWithContractOperations(store *cache.Store, write WriteDeps, submitStagingDir string, legality *LegalityAdapter, newDeps NewDeps, contractOperations ContractToolOperations, workDeps ...WorkToolDeps) *Registry {
	return BuildRegistryWithOperations(store, write, submitStagingDir, legality, newDeps, contractOperations, DataToolDeps{}, workDeps...)
}

// registerContractTool keeps the grouped contract surface available when the
// MCP session is offline but its local mirror exists. P6 read/preflight paths
// remain usable; remote mutations fail through their own injected boundaries.
func registerContractTool(r *Registry, newDeps NewDeps, contractDeps ContractDeps, degraded ...bool) {
	handler := newContractDispatch(newDeps, contractDeps)
	if len(degraded) > 1 {
		panic("mcp: registerContractTool accepts at most one degraded flag")
	}
	if len(degraded) == 1 && degraded[0] {
		handler = rejectDegradedLegacyContractWrites(handler)
	}
	// --- a2a_contract (action: the contract sub-verbs) -------------------
	r.Register(ToolSpec{
		Name:        "a2a_contract",
		Description: "contract family: action=new|preflight|publish|materialize|check|deprecate|retire|diff|verify-export|adopt|activate",
		InputSchema: groupedSchema("action", ContractActions, map[string]string{
			"space": "string", "slug": "string", "fields": "object", "body": "string",
			"thread": "string", "id": "string", "version": "string",
			"bump":    "string",
			"staging": "string", "expect_plan": "string", "to": "string",
			"mode": "string", "payload": "string", "schema": "string",
			"successor": "string", "sunset": "string", "override": "boolean",
			"v1": "string", "v2": "string", "local": "string",
			"ref": "string", "actor": "object",
			"major": "integer", "note": "string", "satisfies": "array",
		}),
		Handler: handler,
	})
}

func rejectDegradedLegacyContractWrites(next HandlerFunc) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var input struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(args, &input); err == nil &&
			(input.Action == "deprecate" || input.Action == "retire" || input.Action == "adopt" || input.Action == "activate") {
			return nil, "", ErrLegacyContractWriteUnavailable
		}
		return next(ctx, args)
	}
}
