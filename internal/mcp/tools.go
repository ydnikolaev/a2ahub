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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// ErrLegacyContractWriteUnavailable reports an unavailable legacy write path.
var ErrLegacyContractWriteUnavailable = errors.New("mcp: legacy contract write unavailable while space write dependencies are offline")

// propSpec is one published property's JSON Schema type PLUS its
// human-readable description (agent-exchange-2026-08 spec 04 AC 4: every
// published property must carry a non-empty description) — the value type
// rawSchema/groupedSchema's props map now carries. A bare `map[string]string`
// (name -> type alone) had nowhere for a description to live; widening the
// value is a signature change that touches every tool registration below,
// not an addition.
type propSpec struct {
	Type        string
	Description string
}

func (p propSpec) schema() map[string]any {
	return map[string]any{"type": p.Type, "description": p.Description}
}

// rawSchema builds a CLOSED (additionalProperties: false) object schema for
// an action-free tool: every entry in props becomes a described property,
// required names the JSON Schema `required` array.
//
// Until spec 04 (agent-exchange-2026-08) this schema was permissive
// documentation only ("not an enforcement gate") and the actual refusal
// lived solely in each handler's own decode. That silent-drop trap is
// exactly what spec 04 closes: additionalProperties:false now makes the
// PUBLISHED schema itself a real client-side refusal, paired with
// decodeStrict's own server-side DisallowUnknownFields below — a schema-
// validating MCP client refuses an unknown field before the call is even
// sent, and a client that skips validation still meets the same refusal at
// decode. Building the schema as a Go value and marshaling it (rather than
// a hand-rolled strings.Builder) also fixes a real, separate defect: the
// old implementation iterated props WITHOUT sorting, so its emitted
// property order was non-deterministic run to run (groupedSchema, below,
// already sorted; this one did not) — anything that ever compared schema
// bytes was flaky by construction. encoding/json sorts map keys on marshal,
// so this is deterministic for free, and correctly escapes every
// description string, which raw string-building did not.
func rawSchema(props map[string]propSpec, required ...string) json.RawMessage {
	properties := make(map[string]any, len(props))
	for name, spec := range props {
		properties[name] = spec.schema()
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("mcp: marshal schema: %v", err))
	}
	return json.RawMessage(raw)
}

// groupedSchema builds a capability-grouped tool's superset input-schema
// descriptor: a closed-enum discriminator (discKey, values FROM the
// exported enum slice — the single source, discDescription describing the
// discriminator ITSELF, which is a published property like any other and so
// also needs one under AC 4) plus the UNION of every folded action/view's
// own fields, with the discriminator marked required. The schema is CLOSED
// (additionalProperties: false, spec 04 AC 1/2) — see rawSchema's own doc
// comment for what that changes and why; each per-verb handler still
// enforces its own per-action required fields exactly as P14 shipped, and
// this schema's own "documentation only" framing is retired along with
// rawSchema's.
func groupedSchema(discKey, discDescription string, enum []string, props map[string]propSpec) json.RawMessage {
	properties := make(map[string]any, len(props)+1)
	properties[discKey] = map[string]any{"type": "string", "enum": enum, "description": discDescription}
	for name, spec := range props {
		properties[name] = spec.schema()
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "properties": properties, "required": []string{discKey},
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("mcp: marshal schema: %v", err))
	}
	return json.RawMessage(raw)
}

// decodeStrict is the ONE decode path every MCP tool input site now calls
// (agent-exchange-2026-08 spec 04 D4/§11: DisallowUnknownFields is the
// decode default at every input site, extracted into a single helper rather
// than re-implemented — ADR-019 forbids a second, divergent copy of a rule
// this package already owns, and tools_data.go's decodeDataInput /
// tools_work.go's decodeWorkInput were exactly that: two independent,
// byte-identical copies of this same shape, now folded into this one and
// converted to call it).
//
// raw is normalized to `{}` when empty so every caller can decode
// unconditionally rather than guarding on len(args) > 0 first — decoding
// `{}` into a zero-initialized struct is indistinguishable from skipping
// the decode. maxBytes, when > 0, bounds the input size the way
// tools_data.go/tools_work.go already did (their own maximumDataToolInput/
// maximumWorkToolInput constants become this call's maxBytes argument); 0
// means no site-specific bound applies, matching every OTHER site's prior
// behaviour (no size check at all). tool names the calling tool/verb for
// the error text ("%s: invalid input: %w"/"%s: input exceeds %d bytes"),
// matching the wording every site already used before this extraction.
//
// The trailing json.Decoder.Decode(&extra) != io.EOF check refuses a
// SECOND JSON value concatenated onto the first (a decoder reading from a
// stream, not json.Unmarshal, would otherwise silently accept and ignore
// it) — carried over unchanged from decodeDataInput/decodeWorkInput.
func decodeStrict(raw json.RawMessage, out any, tool string, maxBytes int) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		return fmt.Errorf("%s: input exceeds %d bytes", tool, maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%s: invalid input: %w", tool, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s: invalid input: multiple JSON values", tool)
		}
		return fmt.Errorf("%s: invalid input: %w", tool, err)
	}
	return nil
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
		InputSchema: groupedSchema("view", "which read to run: inbox|outbox|show|thread|search|contracts", ReadViews, map[string]propSpec{
			"actionable": {"boolean", "inbox only: filter to items OP-207 says need this system's move now"},
			"attention":  {"boolean", "outbox only: filter to items awaiting a counterparty's move"},
			"overdue":    {"boolean", "inbox only: filter to open items addressed to this system whose needed_by has passed"},
			"ref":        {"string", "show only: the artifact id or ref to display"},
			"thread_id":  {"string", "thread only: a thread id (thread:...) or any member artifact id to resolve its conversation"},
			"thread":     {"string", "search only: narrow results to one thread's own conversation"},
			"query":      {"string", "search only: the free-text query to search for"},
			"type":       {"string", "search only: filter results to one artifact type"},
			"space":      {"string", "search/thread only: filter or disambiguate results by connected space id"},
			"state":      {"string", "search only: filter results to one lifecycle state"},
			"provider":   {"string", "contracts only: filter to contracts provided by one participant"},
		}),
		Handler: newReadDispatch(store),
	})
	r.Register(ToolSpec{
		Name:        "a2a_whatsnew",
		Description: "release directives and current known issues — optional since=<version>",
		InputSchema: rawSchema(map[string]propSpec{
			"since": {"string", "a version to query release notes/known issues since; omitted returns the newest corpus entry"},
		}),
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
//
// It takes the whole SubmitDeps rather than its (staging dir, legality)
// pieces — NARROWING the signature, not widening it. Rebuilding SubmitDeps
// here from pieces silently dropped whatever else the caller had put on it,
// and once SubmitDeps grew its own per-space ResolveSpace closure that
// omission had teeth: wire.go installed the resolver, this function threw it
// away, and a2a_submit went on refusing every multi-space write while its
// unit tests — which construct SubmitDeps directly — stayed green.
func BuildRegistryWithOperations(store *cache.Store, write WriteDeps, submit SubmitDeps, newDeps NewDeps, contractOperations ContractToolOperations, dataOperations DataToolDeps, workDeps ...WorkToolDeps) *Registry {
	r := NewRegistry()
	r.Decorate(updateNoticeDecorator(store))
	if len(workDeps) > 1 {
		panic("mcp: BuildRegistry accepts at most one work dependency set")
	}
	// P37 Wave I: `a2a_contract` publish needs the SAME staging dir
	// `a2a_submit` already receives here, to fold a staged schema/fixture
	// edit into the version it is declaring (ContractDeps.StagingDir's own
	// doc comment) — no new BuildRegistry parameter, this one is already
	// threaded through for submit's own idempotency short-circuit.
	contractDeps := ContractDeps{
		WriteDeps: write, StagingDir: submit.StagingDir,
		Publication: contractOperations.Publication, Materialize: contractOperations.Materialize,
		Check: contractOperations.Check, Inspection: contractOperations.Inspection,
	}
	// write, not submit.WriteDeps: the caller mutates the WriteDeps it
	// passes here after building submit (wire.go's ambiguousSpaceFunnel
	// install), and every non-submit handler must see that mutation.
	// submit keeps its own embedded copy plus its own ResolveSpace.
	submitDeps := submit
	submitDeps.WriteDeps = write

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
	r.Register(ToolSpec{Name: "a2a_new", Description: "draft one or more new artifacts (items[]) on one thread; an item's fields key may be a dotted path into a nested field, e.g. expected_response.shape", InputSchema: rawSchema(map[string]propSpec{
		"items":  {"array", "the artifact(s) to draft on this call's thread — each item's own field map may key a dotted path into a nested field, e.g. expected_response.shape"},
		"thread": {"string", "the thread this batch of drafts belongs to, when it is not implied by the drafted item(s) themselves"},
	}, "items"), Handler: newNewHandler(newDeps)})
	r.Register(ToolSpec{Name: "a2a_submit", Description: "validate (V2) and submit staged draft(s); accepts an id array (OP-220 batch) or a single id", InputSchema: rawSchema(map[string]propSpec{
		"ids": {"array", "the staged draft id(s) to validate and submit — an array is an OP-220 all-or-nothing batch; a single id also flows through this field"},
	}, "ids"), Handler: newSubmitHandler(submitDeps)})

	// --- a2a_lifecycle (action: the 15 generic OP-211 verbs) -------------
	r.Register(ToolSpec{
		Name:        "a2a_lifecycle",
		Description: "generic lifecycle transition: action=ack|accept|decline|start|block|unblock|cancel|close|withdraw|supersede|satisfy|approve|reject|verify-pass|verify-fail",
		InputSchema: groupedSchema("action", "the generic lifecycle transition to apply", LifecycleActions, map[string]propSpec{
			"ids":         {"array", "the artifact id(s) this transition applies to (batched: one commit, one PR)"},
			"reason":      {"string", "why this transition is happening (required by some verbs, e.g. block/decline/withdraw)"},
			"reason_code": {"string", "a closed reason vocabulary code accompanying reason, where the verb supports one"},
			"refs":        {"array", "supporting reference id(s) this transition cites (required by block/supersede/satisfy)"},
			"findings":    {"string", "verify-fail's own findings narrative"},
			"actor":       {"object", "the identity performing this transition, when it differs from this session's default"},
			"verdicts":    {"array", "close/verify-pass's own per-acceptance-criterion verdicts (event/v2 floor-gated)"},
		}),
		Handler: newLifecycleDispatch(write),
	})

	// --- a2a_exchange (action: respond|verify|dispute|note) --------------
	//
	// `refs` is DELIBERATELY ABSENT (agent-exchange-2026-08 spec 04 D4,
	// closed fork): it used to be published here as a bare string even
	// though respond/dispute/note's own input structs carry no such field
	// at all — a caller that read this schema and sent refs to
	// action=respond got a SUCCESSFUL response with the reference silently
	// discarded, plain json.Unmarshal never having refused the extra key.
	// verify's own VerifyInput DOES genuinely honour a `refs` disambiguator
	// (resolveResponseID) — withdrawing this property therefore also
	// removes verify's own refs from client-side discoverability, which is
	// a real, reported cost, not a free fix (see this package's own spec 04
	// report). Implementing the field respond/dispute/note actually need
	// (MCP `respond --ref`) is agent-exchange-2026-08's own B22, which that
	// epic still holds; withdrawing stops today's silent drop without
	// claiming another epic's row.
	r.Register(ToolSpec{
		Name:        "a2a_exchange",
		Description: "exchange verbs: action=respond|verify|dispute|note",
		InputSchema: groupedSchema("action", "the exchange verb to run", ExchangeActions, map[string]propSpec{
			"parent_ids":    {"array", "respond only: the parent request id(s) this response answers"},
			"result":        {"string", "respond only: the response's own §5.2 result classification"},
			"fields":        {"object", "respond only: the response body's structured fields"},
			"body_override": {"string", "respond only: a literal response body, bypassing the templated fields"},
			"unmet":         {"array", "respond only: which of the parent's acceptance criteria this response leaves unmet"},
			"standing":      {"string", "respond only: this response's own standing classification"},
			"blocked_by":    {"object", "respond only: the block this response reports, when standing=blocked"},
			"delivers":      {"array", "respond only: data package id(s) (DP-...) this response announces as delivered"},
			"targets":       {"array", "verify only: the response id(s) being verified"},
			"ids":           {"array", "dispute/note only: the id(s) this call applies to"},
			"reason":        {"string", "dispute only: why the response is being disputed"},
			"reason_code":   {"string", "dispute only: a closed reason vocabulary code accompanying reason"},
			"note":          {"string", "note only: the note's own free-text body"},
			"verdicts":      {"array", "verify only: per-acceptance-criterion verdicts (event/v2 floor-gated)"},
			"actor":         {"object", "the identity performing this action, when it differs from this session's default"},
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
	return BuildRegistryWithOperations(store, write,
		SubmitDeps{WriteDeps: write, StagingDir: submitStagingDir, Legality: legality},
		newDeps, contractOperations, DataToolDeps{}, workDeps...)
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
		InputSchema: groupedSchema("action", "the contract family sub-verb to run", ContractActions, map[string]propSpec{
			"space":       {"string", "the connected space this call targets (required once more than one space is connected)"},
			"slug":        {"string", "new only: the contract's own slug"},
			"fields":      {"object", "new only: the drafted contract's structured fields"},
			"body":        {"string", "new only: a literal drafted contract body, bypassing the templated fields"},
			"thread":      {"string", "new only: the thread this draft belongs to"},
			"id":          {"string", "the contract id this call targets"},
			"version":     {"string", "the contract version this call targets or declares"},
			"bump":        {"string", "preflight/publish only: the semver bump kind to apply"},
			"staging":     {"string", "preflight/publish only: the staged draft directory to publish from"},
			"expect_plan": {"string", "publish only: the plan digest this publish must match (idempotency guard)"},
			// allow_empty_bump: T1's own words — "An agent that can only
			// reach the MCP surface must not meet a refusal it has no way
			// to satisfy." decodeStrict()s into ContractPreflightInput
			// (this package) and ContractPublishInput (tools_contract.go);
			// both carry the matching field.
			"allow_empty_bump": {"boolean", "preflight/publish only: acknowledge and proceed past a bump whose mutations touch no normative artifact"},
			// generated_from_digest is DELIBERATELY ABSENT: ContractPublishInput
			// carries the field (honoured ⊆ published is a one-directional
			// AC, so this is not a violation) but newP6ContractPublishHandler
			// REFUSES any non-empty value outright ("generated_from.
			// source_digest is finalized by the shared publication planner")
			// — publishing a property whose only real behaviour is refusal
			// is a worse trap than leaving it undocumented: a schema-reading
			// caller would be invited to send it and always get an error.
			// Reported to the lead rather than resolved here (see this
			// package's spec 04 report).
			"to":        {"string", "materialize only: the destination path to materialize into"},
			"mode":      {"string", "check only: the conformance check mode to run"},
			"payload":   {"string", "check only: a payload file path for single-case conformance checks"},
			"schema":    {"string", "check only: a schema override for conformance checks"},
			"successor": {"string", "deprecate only: the contract id/version superseding this one"},
			"sunset":    {"string", "deprecate only: the date this contract's support ends"},
			"override":  {"boolean", "retire only: force retirement past an active-consumer refusal"},
			"v1":        {"string", "diff only: the first version to diff"},
			"v2":        {"string", "diff only: the second version to diff"},
			"local":     {"string", "verify-export only: the local export path to verify"},
			"ref":       {"string", "materialize/verify-export only: the contract ref (id@version) this call targets"},
			"actor":     {"object", "the identity performing this action, when it differs from this session's default"},
			"major":     {"integer", "adopt only: the major version this adoption plan targets"},
			"note":      {"string", "adopt/activate only: a free-text note attached to this call"},
			"satisfies": {"array", "activate only: the acceptance criteria this activation satisfies"},
		}),
		Handler: handler,
	})
}

func rejectDegradedLegacyContractWrites(next HandlerFunc) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		// This probe deliberately stays a plain, lenient json.Unmarshal
		// rather than decodeStrict: it decodes into a MINIMAL struct
		// (just Action) to route degraded-write refusal, then hands the
		// FULL, UNTOUCHED args to next — which is the grouped a2a_contract
		// dispatch, and reaches the real per-action handler's own
		// decodeStrict call. Routing this probe itself through
		// DisallowUnknownFields would refuse EVERY real call before next
		// ever ran (a genuine `{"action":"deprecate","id":...,
		// "successor":...}` payload carries fields this tiny probe struct
		// does not declare), silently disabling the very guard it exists
		// to install. Refusal for an unknown top-level key on THIS tool
		// still happens — just one frame further in, at the per-action
		// handler's own decode.
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
