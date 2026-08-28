package mcp

// OP-211 lifecycle verb tools (a2a_ack .. a2a_note) — the §7.7-enumerated
// tool set's largest family. Mirrors internal/cli's cmd_lifecycle.go verb
// set exactly (19 verbs: 15 generic table-driven + respond/verify/dispute/
// note), duplicating only the OUTER shape (structured JSON input instead
// of flag.FlagSet, HandlerFunc instead of cli.Command) — the inner
// legality/event-authoring/funnel-submit logic is this file's own call
// into eventdoc.go's shared helpers, itself a projection of the SAME
// event/v1 schema the CLI projects (plan 14 Placement decisions).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// lifecycleVerbSpec is one OP-211 generic verb's own row (mirrors
// internal/cli's lifecycleVerbSpec).
type lifecycleVerbSpec struct {
	Verb              string
	Transition        string
	RequireReason     bool
	RequireReasonCode bool
	RequireRefs       bool
	RequireFindings   bool
	GateMarker        bool
	// SupportsVerdicts is P1's own per-row switch (one-answer-2026-08 spec
	// 01, mirroring internal/cli's cmd_lifecycle.go lifecycleVerbSpec's own
	// field of the same name, B24): true on EXACTLY the `close` row.
	// newLifecycleHandler reads it to decide, once per invocation, whether
	// to honour LifecycleInput.Verdicts at all, floor-gate the authored
	// schema to event/v2, and derive an operation.Close key — the SAME
	// resolution (resolveVerdicts) newVerifyHandler already carries for its
	// own verify/D-024-close pair. Every other row leaves this false, which
	// keeps their own path byte-identical to before this field existed:
	// verdicts left unset produces no schema change and no operation key.
	SupportsVerdicts bool
}

// LifecycleVerbTable is the SSOT of every OP-211 generic (table-driven)
// verb this package registers a tool for — exported so tools.go (the
// registry-construction file) can iterate it without a second, drifting
// copy.
var LifecycleVerbTable = []lifecycleVerbSpec{
	{Verb: "ack", Transition: fold.TAcknowledge},
	{Verb: "accept", Transition: fold.TAccept},
	{Verb: "decline", Transition: fold.TDecline, RequireReason: true, RequireReasonCode: true},
	{Verb: "start", Transition: fold.TStart},
	{Verb: "block", Transition: fold.TBlock, RequireRefs: true},
	{Verb: "unblock", Transition: fold.TUnblock},
	{Verb: "cancel", Transition: fold.TCancel},
	{Verb: "close", Transition: fold.TClose, SupportsVerdicts: true},
	{Verb: "withdraw", Transition: fold.TWithdraw},
	{Verb: "supersede", Transition: fold.TSupersede, RequireRefs: true},
	{Verb: "satisfy", Transition: fold.TSatisfy, RequireRefs: true},
	{Verb: "approve", Transition: fold.TApprove, GateMarker: true},
	{Verb: "reject", Transition: fold.TReject, RequireReason: true, GateMarker: true},
	{Verb: "verify-pass", Transition: fold.TVerifyPass},
	{Verb: "verify-fail", Transition: fold.TVerifyFail, RequireFindings: true},
}

// LifecycleInput is the structured input every generic OP-211 verb tool
// takes (structured form replaces the CLI's flag parsing, per plan 14
// Brief item 4): N ids batched into one commit/one PR.
type LifecycleInput struct {
	// Action is a2a_lifecycle's own discriminator — never read here (the
	// handler's behaviour is spec, closed over by newLifecycleDispatch's
	// per-verb map, not this decoded value); it exists purely so this
	// struct stays decodable under decodeStrict's DisallowUnknownFields
	// when newDispatch forwards the full raw args, discriminator included
	// (tools_dispatch.go's own doc comment).
	Action     string     `json:"action,omitempty"`
	IDs        []string   `json:"ids"`
	Reason     string     `json:"reason,omitempty"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Refs       []string   `json:"refs,omitempty"`
	Findings   string     `json:"findings,omitempty"`
	Actor      ActorInput `json:"actor,omitempty"`
	// Verdicts is P1's own field (one-answer-2026-08 spec 01), honoured
	// ONLY when spec.SupportsVerdicts (the close row) — every other row
	// refuses it by name rather than silently dropping it, mirroring
	// internal/cli's structural refusal (--verdict registered on no other
	// row's FlagSet, so Go's own flag.ContinueOnError refuses it there).
	Verdicts []VerdictInputEntry `json:"verdicts,omitempty"`
}

// newLifecycleHandler builds the table-driven generic verb handler for
// spec (mirrors internal/cli's LifecycleCommand.Run).
func newLifecycleHandler(spec lifecycleVerbSpec, deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in LifecycleInput
		if err := decodeStrict(args, &in, spec.Verb, 0); err != nil {
			return nil, "", err
		}
		if len(in.IDs) == 0 {
			return nil, "", fmt.Errorf("%s: ids is required", spec.Verb)
		}
		if spec.RequireReason && in.Reason == "" {
			return nil, "", fmt.Errorf("%s: reason is required", spec.Verb)
		}
		if spec.RequireRefs && len(in.Refs) == 0 {
			return nil, "", fmt.Errorf("%s: refs is required", spec.Verb)
		}
		if spec.RequireFindings && in.Findings == "" {
			return nil, "", fmt.Errorf("%s: findings is required", spec.Verb)
		}
		// verdicts (P1): a row that does NOT support verdicts refuses one by
		// name rather than silently dropping it — see LifecycleInput's own
		// doc comment on why this cannot be a Go-level "flag not
		// registered" refusal the way internal/cli's FlagSet achieves it.
		if len(in.Verdicts) > 0 && !spec.SupportsVerdicts {
			return nil, "", fmt.Errorf("%s: does not support verdicts (only close does)", spec.Verb)
		}

		// Resolve this call's target space from the ids it already
		// carries, BEFORE the first deps.MirrorDir/deps.Manifest read
		// below — a single-space session (deps.ResolveSpace nil) is
		// unchanged; shadows the captured deps, never assigns to it, so
		// no call ever poisons a later one through the same closure.
		deps, err := resolveWriteSpace(deps, "", in.IDs)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", spec.Verb, err)
		}

		// eventSchema/verdicts (P1, close row only): mirrors
		// internal/cli's LifecycleCommand.Run exactly — floor-gated the
		// SAME way newVerifyHandler's own identical block is, resolved
		// ONCE against ids[0] (close's ids ARE the criteria-bearing
		// parents directly, no response indirection) and applied
		// uniformly to every id's close event in the batch loop below.
		eventSchema := "event/v1"
		var verdicts []VerdictInputEntry
		if spec.SupportsVerdicts {
			eventSchema = lifecycleEventSchema(deps.Manifest.MinBinaryVersion)
			if len(in.Verdicts) > 0 && eventSchema != "event/v2" {
				return nil, "", fmt.Errorf(
					"%s: verdicts requires this space's min_binary_version to be at or above %s (event/v2); this space's floor is %q",
					spec.Verb, contract.ContractPublicationFloor, deps.Manifest.MinBinaryVersion)
			}
			if len(in.Verdicts) > 0 {
				criteria, cerr := parentAcceptanceCriteria(deps.MirrorDir, in.IDs[0])
				if cerr != nil {
					return nil, "", fmt.Errorf("%s: %s: %w", spec.Verb, in.IDs[0], cerr)
				}
				var rerr error
				verdicts, rerr = resolveVerdicts(in.Verdicts, criteria)
				if rerr != nil {
					return nil, "", fmt.Errorf("%s: %w", spec.Verb, rerr)
				}
			}
		}
		// verdictsPtr: nil (omitted) below the floor or on a row that does
		// not support verdicts at all; non-nil (present, even when empty)
		// at/above the floor on the close row — schemas/event/v2/
		// event.schema.json's own conditional requires the key regardless
		// of whether this invocation named any verdicts.
		var verdictsPtr *[]VerdictInputEntry
		if eventSchema == "event/v2" {
			verdictsPtr = &verdicts
		}

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("%s: %w", spec.Verb, actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		// operationKey (P1, close row only): derived only when verdicts
		// actually carry content — below the floor, on a row that does not
		// support verdicts, or above the floor with none supplied, this
		// verb keeps its EXISTING dedup mechanism unchanged.
		// operation.Close, not operation.Verify: see operation.Close's own
		// doc comment for why a standalone close mints its own key domain.
		var operationKey string
		if len(verdicts) > 0 {
			opVerdicts := make([]operation.VerdictEntry, len(verdicts))
			for i, v := range verdicts {
				opVerdicts[i] = operation.VerdictEntry{Index: v.resolvedIndex, Verdict: v.Verdict, CauseOwner: v.CauseOwner}
			}
			operationKey = operation.Close(deps.OwnSystem, actor.Kind, actor.Name, in.IDs, opVerdicts)
		}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", spec.Verb, err)
		}

		// parsedRefs (D-3, mirroring internal/cli's LifecycleCommand.Run):
		// resolved ONCE, before the batch loop, and reused for BOTH
		// evaluateCandidate' own successor resolution (the supersede
		// row's only consumer, evaluateCandidate/
		// resolveSuccessorFacts) and the event's own `refs[]` field below —
		// never parsed a second time.
		parsedRefs := refsFromList(in.Refs)

		var files []space.FileWrite
		for _, id := range in.IDs {
			evaluation, env, successor, err := evaluateCandidate(deps.MirrorDir, deps.Manifest, id, fold.Event{
				Transition: spec.Transition, Actor: actor,
			}, parsedRefs)
			if err != nil {
				return nil, "", fmt.Errorf("%s: %s: %w", spec.Verb, id, err)
			}
			if evaluation.Verdict != fold.VerdictLegal {
				// D-3 (wave 2c-mcp): the ONE local gate that resolves real
				// SuccessorFacts (evaluateCandidate, above) learns
				// the SAME decision-supersede discrimination checkLifecycle
				// already applies — see decisionSupersedeRefusalError's own
				// doc comment for the exact coarseness this reuses (mirrors
				// internal/cli's LifecycleCommand.Run's own identical
				// call-site check verbatim).
				verdictErr := verdictError(id, evaluation.Verdict)
				if evaluation.Verdict == fold.VerdictUnauthorizedActor && spec.Transition == fold.TSupersede && env.Kind == fold.KindDecision {
					verdictErr = decisionSupersedeRefusalError(id, successor != nil)
				}
				return nil, "", fmt.Errorf("%s: %w", spec.Verb, verdictErr)
			}
			_, probe, err := loadEnvelope(deps.MirrorDir, id)
			if err != nil {
				return nil, "", fmt.Errorf("%s: %s: %w", spec.Verb, id, err)
			}

			eventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("%s: cannot mint event id: %w", spec.Verb, err)
			}
			ev := eventDoc{
				Schema: eventSchema, Event: eventID.String(), Space: probe.Space,
				Subject: id, Transition: spec.Transition, State: eventReceiptState(evaluation),
				Actor: eventActorFrom(resolved, actor.System),
				At:    now.UTC().Format(time.RFC3339),
				// Verdicts: nil (omitted) on every row except close, and
				// nil below the floor even on close — see verdictsPtr's
				// own assignment above.
				Verdicts: verdictsPtr,
			}
			if in.Reason != "" {
				ev.Note = in.Reason
			}
			if in.ReasonCode != "" {
				ev.ReasonCode = in.ReasonCode
			}
			if len(in.Refs) > 0 {
				ev.Refs = parsedRefs
			}
			if in.Findings != "" {
				ev.Note = in.Findings
			}
			raw, merr := yaml.Marshal(ev)
			if merr != nil {
				return nil, "", fmt.Errorf("%s: cannot encode event for %s: %w", spec.Verb, id, merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
		}

		req := deps.buildRequest(in.IDs, files, spec.Verb, spec.GateMarker)
		req.OperationKey = operationKey
		result, err := deps.submit(ctx, req, spec.Verb, in.IDs)
		return result, "", err
	}
}

// refsFromList builds event/v1 refs entries from a structured refs array
// (the CLI's comma-separated --refs flag's structured-input equivalent).
func refsFromList(refs []string) []refEntry {
	out := make([]refEntry, 0, len(refs))
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, refEntry{Ref: r})
		}
	}
	return out
}

// --- respond --------------------------------------------------------------

// RespondInput is a2a_respond's structured input (mirrors internal/cli's
// RespondCommand flags).
//
// Unmet/Standing/BlockedBy (defects-fix-2026-08 P2, spec 02) mirror
// internal/cli's `--unmet`/`--standing`/`--blocked-by` flags — same three
// fields, same envelope/v2/response.schema.json enums, but native JSON
// shapes (an int array, a struct) rather than a CLI flag's colon-separated
// string, since this surface's wire format already has structure a flag
// string doesn't. ADR-001 forbids internal/mcp importing internal/cli, so
// the validation (the enums, the required-together rules) is duplicated
// below rather than shared — the established idiom this file's own
// respondSeed/parentThread/thread-conflict duplication already follows.
type RespondInput struct {
	// Action is a2a_exchange's own discriminator — never read here; see
	// LifecycleInput's identical field for why it exists.
	Action       string              `json:"action,omitempty"`
	ParentIDs    []string            `json:"parent_ids"`
	Result       string              `json:"result"`
	Fields       map[string]string   `json:"fields,omitempty"`
	BodyOverride string              `json:"body_override,omitempty"`
	Actor        ActorInput          `json:"actor,omitempty"`
	Unmet        []RespondUnmetEntry `json:"unmet,omitempty"`
	Standing     string              `json:"standing,omitempty"`
	BlockedBy    *RespondBlockedBy   `json:"blocked_by,omitempty"`
	// Delivers (judge-the-thing-2026-08 P2, spec 02, closing half of B22)
	// mirrors internal/cli's repeatable `--delivers` flag: the data package
	// ids (DP-...) this response announces as delivered. A native JSON
	// array, the same "this surface's wire format already has structure a
	// flag string doesn't" reason Unmet/BlockedBy above are native shapes
	// rather than a colon-separated string.
	Delivers []string `json:"delivers,omitempty"`
}

// RespondUnmetEntry is one `unmet[]` input entry (defects-fix-2026-08 P3):
// EITHER a bare non-negative JSON integer (an ordinal-only parent) or a
// `{"criterion": "<id>"}` object (a parent whose acceptance_criteria[]
// declares ids) — mirrors internal/cli's lifecycleUnmetToken/
// lifecycleUnmetEntry exactly (ADR-001 forbids internal/mcp importing
// internal/cli, so this is this package's own copy, the same duplication
// idiom RespondInput's own doc comment above already establishes for the
// whole Unmet field). Index is a pointer for the same "distinguish absent
// from zero" reason internal/cli's lifecycleVerdictEntry.Index is: a bare
// int can't tell "ordinal index 0" from "not this form" apart.
// resolvedIndex is set by respondResolveUnmet, never by UnmarshalJSON — it
// is the one stable identity both wire forms share, for operation.Respond's
// key and the canonical sort, mirroring internal/cli's own resolvedIndex.
type RespondUnmetEntry struct {
	Index         *int
	Criterion     string
	resolvedIndex int
}

// MarshalJSON is UnmarshalJSON's own inverse, needed so a RespondUnmetEntry
// built directly in Go (this package's own tests; a caller that constructs
// the struct rather than parsing wire JSON) round-trips to the SAME wire
// shape UnmarshalJSON expects back — WITHOUT it, Go's default struct
// marshaling emits BOTH fields ({"Index":2,"Criterion":""}), and
// UnmarshalJSON's own object branch then reads the empty "Criterion" key
// as a real (but empty) criterion rather than "this wasn't the criterion
// form at all", tripping its own "must set a non-empty criterion" refusal
// on a perfectly valid bare-index entry.
func (e RespondUnmetEntry) MarshalJSON() ([]byte, error) {
	if e.Criterion != "" {
		return json.Marshal(struct {
			Criterion string `json:"criterion"`
		}{Criterion: e.Criterion})
	}
	if e.Index != nil {
		return json.Marshal(*e.Index)
	}
	return []byte("null"), nil
}

// UnmarshalJSON accepts BOTH of unmet[]'s input shapes: a bare JSON number
// (decoded as the ordinal index) or an object carrying `criterion`.
func (e *RespondUnmetEntry) UnmarshalJSON(data []byte) error {
	var asInt int
	if err := json.Unmarshal(data, &asInt); err == nil {
		if asInt < 0 {
			return fmt.Errorf("unmet entry %d must be a non-negative integer", asInt)
		}
		e.Index = &asInt
		return nil
	}
	var obj struct {
		Criterion string `json:"criterion"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("unmet entry must be a non-negative integer or {\"criterion\": \"<id>\"}: %w", err)
	}
	if obj.Criterion == "" {
		return fmt.Errorf("unmet entry object must set a non-empty \"criterion\"")
	}
	e.Criterion = obj.Criterion
	return nil
}

// RespondCriterion is one parsed entry of a parent's own
// `acceptance_criteria[]` — mirrors internal/cli's
// lifecycleAcceptanceCriterion exactly (same ADR-001 duplication). ID is ""
// for the plain-string (ordinal) form.
type RespondCriterion struct {
	ID   string
	Text string
}

// UnmarshalYAML accepts BOTH of acceptance_criteria[]'s item shapes: a bare
// string (ordinal form) or a `{id, text}` mapping (P3's id form).
func (c *RespondCriterion) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		c.ID = ""
		c.Text = value.Value
		return nil
	}
	var obj struct {
		ID   string `yaml:"id"`
		Text string `yaml:"text"`
	}
	if err := value.Decode(&obj); err != nil {
		return err
	}
	c.ID = obj.ID
	c.Text = obj.Text
	return nil
}

// RespondBlockedBy is envelope/v2/response.schema.json's own `blocked_by`
// shape, `{reason_code, owner, needs}` — ALL THREE required
// (`"required": ["reason_code", "owner", "needs"]`, `"additionalProperties":
// false`). See internal/cli's lifecycleParseBlockedBy doc comment for why
// this phase's own spec text (`<reason_code>:<owner>`, two segments) is a
// deviation this wave's report records: `needs` is required and gets no
// default (P-1 — absence must stay distinct from a declared value).
type RespondBlockedBy struct {
	ReasonCode string `json:"reason_code"`
	Owner      string `json:"owner"`
	Needs      string `json:"needs"`
}

// respondBlockedByReasonEnum/respondBlockedByNeedsEnum/respondStandingEnum
// are envelope/v2/response.schema.json's own closed vocabularies, mirrored
// (not $ref'd — that schema's own description explains why) identically to
// internal/cli's lifecycleBlockedByReasonEnum/lifecycleBlockedByNeedsEnum/
// lifecycleStandingEnum.
var respondBlockedByReasonEnum = map[string]bool{
	"split-required": true, "security-concern": true, "out-of-scope": true,
	"duplicate": true, "other": true,
}

var respondBlockedByNeedsEnum = map[string]bool{
	"bytes": true, "judgement": true, "decision": true,
}

var respondStandingEnum = map[string]bool{
	"authoritative": true, "provisional": true, "advisory": true,
}

// respondDeclaredCriterionIDs returns criteria's own `id` values, in array
// order — used only to NAME what a parent declares in a refusal message,
// mirrors internal/cli's lifecycleDeclaredCriterionIDs.
func respondDeclaredCriterionIDs(criteria []RespondCriterion) []string {
	var ids []string
	for _, c := range criteria {
		if c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// respondResolveUnmet resolves each RespondUnmetEntry against a specific
// parent's `acceptance_criteria[]` — mirrors internal/cli's
// lifecycleResolveUnmet exactly (same resolution rule, same refusal shape,
// same canonical resolvedIndex sort/dedupe): a criterion-id entry resolves
// to that criterion's array position; a bare-index entry is used directly.
// Each direction is refused, by name, when it does not match the parent's
// own shape — including a bare index against an id-declaring parent, which
// is what actually closes the mis-binding class this phase answers rather
// than merely documenting it.
func respondResolveUnmet(tokens []RespondUnmetEntry, criteria []RespondCriterion) ([]RespondUnmetEntry, error) {
	idsDeclared := len(respondDeclaredCriterionIDs(criteria)) > 0
	idPosition := map[string]int{}
	for i, c := range criteria {
		if c.ID != "" {
			idPosition[c.ID] = i
		}
	}
	out := make([]RespondUnmetEntry, 0, len(tokens))
	seen := map[int]bool{}
	for _, tok := range tokens {
		var entry RespondUnmetEntry
		switch {
		case tok.Criterion != "":
			if !idsDeclared {
				return nil, fmt.Errorf("respond: unmet %s: this parent declares no criterion ids; use a 0-based index into its acceptance_criteria[] instead", tok.Criterion)
			}
			pos, ok := idPosition[tok.Criterion]
			if !ok {
				return nil, fmt.Errorf("respond: unmet %s: no such criterion id; this parent declares: %s", tok.Criterion, strings.Join(respondDeclaredCriterionIDs(criteria), ", "))
			}
			entry.Criterion = tok.Criterion
			entry.resolvedIndex = pos
		case tok.Index != nil:
			if idsDeclared {
				return nil, fmt.Errorf("respond: unmet %d: this parent declares criterion ids; use one of them instead of a bare index (declares: %s)", *tok.Index, strings.Join(respondDeclaredCriterionIDs(criteria), ", "))
			}
			index := *tok.Index
			entry.Index = &index
			entry.resolvedIndex = index
		default:
			return nil, fmt.Errorf("respond: unmet entry carries neither an index nor a criterion id")
		}
		if seen[entry.resolvedIndex] {
			return nil, fmt.Errorf("respond: unmet: two entries resolve to the same criterion (position %d) — one entry per unmet criterion", entry.resolvedIndex)
		}
		seen[entry.resolvedIndex] = true
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].resolvedIndex < out[j].resolvedIndex })
	return out, nil
}

// --- verdicts (P1: one-answer-2026-08 spec 01) ----------------------------

// VerdictInputEntry is one `verdicts[]` input entry for a2a_exchange verify
// and a2a_lifecycle close — mirrors RespondUnmetEntry's dual-shape pattern
// (index XOR criterion) plus `verdict` and `cause_owner`, both required on
// EVERY entry, including a `met` one (schemas/event/v2/event.schema.json's
// own requirement — spec 01 §11's amendment: spec 06 §7's prose calls
// cause_owner optional, the shipped schema does not, and the schema wins).
//
// This SAME type is eventDoc's own `verdicts[]` entry (spec 01 §11's other
// amendment): field order and yaml tags mirror internal/cli's
// lifecycleVerdictEntry exactly, and Index is a POINTER for the identical
// reason RespondUnmetEntry's own Index is — `omitempty` on a bare int would
// silently drop `index: 0`, the FIRST criterion. resolvedIndex is set by
// resolveVerdicts, never by UnmarshalJSON — the one stable identity both
// wire forms share, for operation.VerdictEntry and the canonical sort.
type VerdictInputEntry struct {
	Index         *int   `yaml:"index,omitempty"`
	Criterion     string `yaml:"criterion,omitempty"`
	Verdict       string `yaml:"verdict"`
	CauseOwner    string `yaml:"cause_owner"`
	resolvedIndex int
}

// MarshalJSON is UnmarshalJSON's own inverse — see RespondUnmetEntry's own
// doc comment for the round-trip trap this closes: without it, Go's default
// struct marshaling would emit Index, Criterion, Verdict AND CauseOwner
// together, and UnmarshalJSON's object branch would then read the empty
// "criterion" key as a real (but empty) criterion, tripping the "neither an
// index nor a criterion id" refusal on a perfectly valid bare-index entry.
func (e VerdictInputEntry) MarshalJSON() ([]byte, error) {
	if e.Criterion != "" {
		return json.Marshal(struct {
			Criterion  string `json:"criterion"`
			Verdict    string `json:"verdict"`
			CauseOwner string `json:"cause_owner"`
		}{Criterion: e.Criterion, Verdict: e.Verdict, CauseOwner: e.CauseOwner})
	}
	if e.Index != nil {
		return json.Marshal(struct {
			Index      int    `json:"index"`
			Verdict    string `json:"verdict"`
			CauseOwner string `json:"cause_owner"`
		}{Index: *e.Index, Verdict: e.Verdict, CauseOwner: e.CauseOwner})
	}
	return []byte("null"), nil
}

// UnmarshalJSON accepts ONE object shape carrying either "index" or
// "criterion" (never both), plus "verdict" and "cause_owner" — shape only;
// the verdict enum and cause_owner requiredness are checked by
// resolveVerdicts, mirroring internal/cli's own parse/resolve split
// (lifecycleParseVerdicts/lifecycleResolveVerdicts), folded into one
// resolution step here since this surface's wire format is already
// structured JSON, not a colon-separated flag string needing a separate
// parse phase.
func (e *VerdictInputEntry) UnmarshalJSON(data []byte) error {
	var obj struct {
		Index      *int   `json:"index"`
		Criterion  string `json:"criterion"`
		Verdict    string `json:"verdict"`
		CauseOwner string `json:"cause_owner"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("verdict entry must be an object carrying either \"index\" or \"criterion\", plus \"verdict\" and \"cause_owner\": %w", err)
	}
	if obj.Index != nil && obj.Criterion != "" {
		return fmt.Errorf("verdict entry must carry either \"index\" or \"criterion\", not both")
	}
	if obj.Index == nil && obj.Criterion == "" {
		return fmt.Errorf("verdict entry must carry either \"index\" or \"criterion\"")
	}
	if obj.Index != nil && *obj.Index < 0 {
		return fmt.Errorf("verdict entry index %d must be a non-negative integer", *obj.Index)
	}
	e.Index = obj.Index
	e.Criterion = obj.Criterion
	e.Verdict = obj.Verdict
	e.CauseOwner = obj.CauseOwner
	return nil
}

// verdictEnum is schemas/event/v2/event.schema.json's own closed
// `verdicts[].verdict` vocabulary — mirrors internal/cli's
// lifecycleVerdictEnum (ADR-001 duplication).
var verdictEnum = map[string]bool{
	"met": true, "unmet": true, "not_warranted": true, "not_exercised": true,
}

// resolveVerdicts resolves each VerdictInputEntry against a specific
// parent's `acceptance_criteria[]` — the SHARED resolution both
// a2a_exchange verify (against the response's own parent) and
// a2a_lifecycle close (against the id it closes directly, "no response
// indirection", spec 01 T1) use, rather than a third variant. Mirrors
// respondResolveUnmet exactly (same four refusals, dedupe, canonical
// resolvedIndex sort): a criterion-id entry resolves to that criterion's
// array position; a bare-index entry is used directly; each direction is
// refused, by name, when it does not match the parent's own shape.
// Additionally checks the verdict enum and cause_owner requiredness (P1
// §11's amendment; internal/cli performs the same two checks in
// lifecycleParseVerdicts, before resolution). Error messages carry no verb
// prefix — callers wrap with their own (`fmt.Errorf("%s: %w", verb, err)`),
// since this one function serves two different verbs.
func resolveVerdicts(tokens []VerdictInputEntry, criteria []RespondCriterion) ([]VerdictInputEntry, error) {
	idsDeclared := len(respondDeclaredCriterionIDs(criteria)) > 0
	idPosition := map[string]int{}
	for i, c := range criteria {
		if c.ID != "" {
			idPosition[c.ID] = i
		}
	}
	out := make([]VerdictInputEntry, 0, len(tokens))
	seen := map[int]bool{}
	for _, tok := range tokens {
		if !verdictEnum[tok.Verdict] {
			return nil, fmt.Errorf("verdict must be one of met|unmet|not_warranted|not_exercised")
		}
		if strings.TrimSpace(tok.CauseOwner) == "" {
			return nil, fmt.Errorf("cause_owner is required on every verdict entry, including met (schemas/event/v2/event.schema.json requires it)")
		}
		entry := VerdictInputEntry{Verdict: tok.Verdict, CauseOwner: tok.CauseOwner}
		switch {
		case tok.Criterion != "":
			if !idsDeclared {
				return nil, fmt.Errorf("verdict %s: this parent declares no criterion ids; use a 0-based index into its acceptance_criteria[] instead", tok.Criterion)
			}
			pos, ok := idPosition[tok.Criterion]
			if !ok {
				return nil, fmt.Errorf("verdict %s: no such criterion id; this parent declares: %s", tok.Criterion, strings.Join(respondDeclaredCriterionIDs(criteria), ", "))
			}
			entry.Criterion = tok.Criterion
			entry.resolvedIndex = pos
		case tok.Index != nil:
			if idsDeclared {
				return nil, fmt.Errorf("verdict %d: this parent declares criterion ids; use one of them instead of a bare index (declares: %s)", *tok.Index, strings.Join(respondDeclaredCriterionIDs(criteria), ", "))
			}
			index := *tok.Index
			entry.Index = &index
			entry.resolvedIndex = index
		default:
			return nil, fmt.Errorf("verdict entry carries neither an index nor a criterion id")
		}
		if seen[entry.resolvedIndex] {
			return nil, fmt.Errorf("verdicts: two entries resolve to the same criterion (position %d) — one verdict per judged criterion", entry.resolvedIndex)
		}
		seen[entry.resolvedIndex] = true
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].resolvedIndex < out[j].resolvedIndex })
	return out, nil
}

// lifecycleEventSchema is this package's own verify/close floor selector
// (P1: one-answer-2026-08 spec 01) — mirrors internal/cli's cmd_lifecycle.go
// lifecycleEventSchema and this package's own contractActivateEventSchema
// (tools_contract.go) exactly, including the FAIL-CLOSED direction: a space
// whose min_binary_version (floor) is at or above
// contract.ContractPublicationFloor authors event/v2; below it,
// unparseable, or absent, event/v1 — the conservative direction
// version.OlderThan's own doc comment names. contractActivateEventSchema's
// own doc comment used to claim this package's lifecycle path had no such
// selector; this phase is what makes that sentence obsolete.
func lifecycleEventSchema(floor string) string {
	belowFloor, err := version.OlderThan(floor, contract.ContractPublicationFloor)
	if err != nil || belowFloor {
		return "event/v1"
	}
	return "event/v2"
}

// respondValidateBlockedBy checks blockedBy's three fields against
// envelope/v2/response.schema.json's own `blocked_by` requirements — a nil
// blockedBy (the field omitted) is valid, matching CLI's "flag not given at
// all" case.
func respondValidateBlockedBy(blockedBy *RespondBlockedBy) error {
	if blockedBy == nil {
		return nil
	}
	if !respondBlockedByReasonEnum[blockedBy.ReasonCode] {
		return fmt.Errorf("respond: blocked_by.reason_code must be one of split-required|security-concern|out-of-scope|duplicate|other")
	}
	if strings.TrimSpace(blockedBy.Owner) == "" {
		return fmt.Errorf("respond: blocked_by.owner is required")
	}
	if !respondBlockedByNeedsEnum[blockedBy.Needs] {
		return fmt.Errorf("respond: blocked_by.needs must be one of bytes|judgement|decision")
	}
	return nil
}

func newRespondHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in RespondInput
		if err := decodeStrict(args, &in, "respond", 0); err != nil {
			return nil, "", err
		}
		if len(in.ParentIDs) == 0 {
			return nil, "", fmt.Errorf("respond: parent_ids is required")
		}
		switch in.Result {
		case "answered", "delivered", "partial", "cannot":
		default:
			return nil, "", fmt.Errorf("respond: result must be one of answered|delivered|partial|cannot")
		}
		if in.Standing != "" && !respondStandingEnum[in.Standing] {
			return nil, "", fmt.Errorf("respond: standing must be one of authoritative|provisional|advisory")
		}
		if berr := respondValidateBlockedBy(in.BlockedBy); berr != nil {
			return nil, "", berr
		}
		// delivers (judge-the-thing-2026-08 P2): emptiness is the ONLY thing
		// checked here, deliberately, mirroring internal/cli's own
		// --delivers validation (cmd_lifecycle.go) — the id's grammar and
		// whether it has landed are ONE question with ONE authority
		// (space.ResolveDataPackage, reached through the funnel), and a
		// second DP- parser here would be exactly the duplicated "is this
		// package here" this epic is named after. A malformed id therefore
		// refuses at submit, naming REF-024, on both surfaces.
		for _, d := range in.Delivers {
			if strings.TrimSpace(d) == "" {
				return nil, "", fmt.Errorf("respond: delivers must not be empty")
			}
		}

		// Resolve this call's target space from parent_ids, BEFORE the
		// first deps.MirrorDir read below (parentAcceptanceCriteria, then
		// the per-parent loop's loadEnvelope/parentThread calls) — see
		// newLifecycleHandler's identical comment for the shadowing
		// discipline this repeats.
		deps, err := resolveWriteSpace(deps, "", in.ParentIDs)
		if err != nil {
			return nil, "", fmt.Errorf("respond: %w", err)
		}

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("respond: %w", actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("respond: %w", err)
		}

		// A nil bodyOverride (not merely an empty-but-non-nil slice)
		// leaves the canonical template's own default placeholder body
		// untouched (template.Render's own contract: `in.Body != nil`
		// triggers an override) — an unconditional []byte(in.BodyOverride)
		// conversion of an empty string still yields a non-nil slice and
		// would silently WIPE the template's default body whenever the
		// caller omits body_override, unlike the CLI's own respond verb
		// (which only sets bodyOverride when --body-file is given).
		var bodyOverride []byte
		if in.BodyOverride != "" {
			bodyOverride = []byte(in.BodyOverride)
		}
		// nil refs: this surface has no `--ref` equivalent yet — the CLI
		// gained one with AC9's submit half and MCP did not (epic-backlog
		// B22). A nil slice encodes as nothing, so an MCP respond keeps the
		// exact key it had before that flag existed.
		//
		// unmet id-form resolution (defects-fix-2026-08 P3): resolved
		// against ParentIDs[0]'s own acceptance_criteria[] — the same "one
		// set applies to the whole batch" simplification this handler
		// already made pre-P3 (one `unmet` set, reused identically for
		// every parent in the loop below), extended one step earlier so a
		// criterion id has a parent to resolve against before the loop
		// even starts.
		var unmet []RespondUnmetEntry
		if len(in.Unmet) > 0 {
			criteria, cerr := parentAcceptanceCriteria(deps.MirrorDir, in.ParentIDs[0])
			if cerr != nil {
				return nil, "", fmt.Errorf("respond: %s: %w", in.ParentIDs[0], cerr)
			}
			unmet, err = respondResolveUnmet(in.Unmet, criteria)
			if err != nil {
				return nil, "", err
			}
		}
		opUnmet := make([]int, len(unmet))
		for i, u := range unmet {
			opUnmet[i] = u.resolvedIndex
		}
		// CLOSED by the lead the same wave this gap was reported (see
		// internal/operation/key.go's own comment on why this WIDENED the
		// signature instead of adding a second entry point): a key minted
		// without these three is not narrower, it is COLLIDING — two responses
		// differing only in `unmet[]` would mint one key, so a corrected
		// response dedups as already-done and its correction never commits.
		respondFacts := operation.RespondIncompleteness{Unmet: opUnmet, Standing: in.Standing}
		if in.BlockedBy != nil {
			respondFacts.BlockedByReason = in.BlockedBy.ReasonCode
			respondFacts.BlockedByOwner = in.BlockedBy.Owner
			respondFacts.BlockedByNeeds = in.BlockedBy.Needs
		}
		// delivers (judge-the-thing-2026-08 P2, closing HALF of B22): this
		// surface now authors the field, threaded into operation.Respond's
		// key the SAME way internal/cli's own call site does (dbdd7257) — an
		// empty/absent in.Delivers keeps deriving the pre-field key
		// (Respond's own "written only when non-empty" discipline), so no
		// existing MCP-authored branch moves. B22's OTHER half — an MCP
		// `--ref`/`refs[]` equivalent — stays open; this phase's spec (§0
		// T1) scopes it out by name.
		operationKey := operation.Respond(
			deps.OwnSystem, actor.Kind, actor.Name, in.ParentIDs, in.Result, in.Fields, nil, bodyOverride, respondFacts,
			in.Delivers,
		)

		var files []space.FileWrite
		var ids []string
		for _, parentID := range in.ParentIDs {
			parentEnv, parentProbe, err := loadEnvelope(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("respond: %s: %w", parentID, err)
			}
			srcThread, err := parentThread(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("respond: %s: %w", parentID, err)
			}

			respFields := map[string]string{}
			for k, v := range in.Fields {
				respFields[k] = v
			}
			respFields["parent"] = parentID
			respFields["result"] = in.Result
			if _, has := respFields["from"]; !has {
				respFields["from"] = deps.OwnSystem
			}

			// §T1 "Explicit conflict refuses": an explicit thread that
			// differs from the SOURCE's is an ERROR naming both values —
			// never a silent precedence, never a guess. A pure
			// error-or-continue branch, so its placement relative to the
			// seed below is not load-bearing the way the propagation
			// fill (after respFields["title"]) is.
			if explicit, has := respFields["thread"]; has && explicit != "" && srcThread != "" && explicit != srcThread {
				return nil, "", fmt.Errorf("respond: %s: thread conflict: explicit thread %q differs from parent thread %q", parentID, explicit, srcThread)
			}
			// A parent with NO thread is refused by name, identically to
			// internal/cli's RespondCommand — see its comment for the full
			// rationale. The short version: `a2a new` always mints, so the
			// only artifacts in this state predate P46, spec 46 carries no
			// legacy path (greenfield, the spaces are reseeded), and both
			// alternatives were observed to be worse — the CLI half wrote
			// YAML `null` and died later on an opaque SCH-006, this half
			// left the field unset and let the canonical template's own
			// placeholder text survive into a committed response.
			if srcThread == "" {
				return nil, "", fmt.Errorf(
					"respond: %s: the parent carries no thread, so this reply has no conversation to join; "+
						"that artifact predates thread propagation — reseed the space or reply to an artifact drafted by this version",
					parentID)
			}

			seed := respondSeed(parentID, in.Result, respFields, bodyOverride, actor, unmet, in.Standing, in.BlockedBy, in.Delivers)

			// Wave K, MCP half. internal/cli's RespondCommand.Run carries
			// the same three fills with the full rationale; ADR-001 keeps
			// these copies deliberate (internal/mcp may never import
			// internal/cli) and cmd/a2a/mcp_equivalence_test.go's
			// TestEquivRespond is what keeps them honest — it went red the
			// moment only one side was fixed, which is the parity suite
			// doing exactly its job.
			//
			// Short version: response.md leaves `space` and `title` as
			// unfilled SCALAR placeholders that nothing in V2 rejects, so a
			// committed response would carry the literal `<space-id>`
			// forever; both are set below and flow through applyFills
			// normally. `to` is SEQUENCE-valued, which applyFills/setScalar
			// cannot rewrite at all (P18's deferred "Fix C"), so the
			// rendered draft still names `<requester-system>` and the
			// funnel refuses it with REF-006 — that one is assigned
			// structurally after the render.
			//
			// Placed AFTER respondSeed, deliberately: both are derived
			// defaults, and folding them into the seed would change every
			// already-computed responseID's hash input. `thread` (§T1
			// "Propagate") joins them here for the same reason — folding
			// it into the seed would silently change every
			// already-computed response ID, the hazard this comment
			// already documents.
			respFields["space"] = parentProbe.Space
			if _, has := respFields["title"]; !has {
				respFields["title"] = fmt.Sprintf("Response to %s", parentID)
			}
			if _, has := respFields["thread"]; !has {
				respFields["thread"] = srcThread // non-empty: the refusal above guarantees it
			}
			responseID, err := artifact.MintExchangeIDAt("XS", deps.OwnSystem, now, bytes.NewReader(seed))
			if err != nil {
				return nil, "", fmt.Errorf("respond: cannot mint response id: %w", err)
			}
			evaluation, _, _, err := evaluateCandidate(deps.MirrorDir, deps.Manifest, parentID, fold.Event{
				Transition: fold.TRespond, ResponseID: responseID, Actor: actor,
			}, nil)
			if err != nil {
				return nil, "", fmt.Errorf("respond: %s: %w", parentID, err)
			}
			if evaluation.Verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("respond: %w", verdictError(parentID, evaluation.Verdict))
			}
			// EnvelopeSchema: "envelope/v2" — see internal/cli's identical
			// RespondCommand.Run comment (cmd_lifecycle.go): a response is
			// generationV2Unconditional, and this call never goes through
			// RenderNew/selectGeneration, so it must set the override
			// directly (internal/space/work_preparer.go:322's own precedent).
			draft, err := template.Render(template.Input{
				Type: "response", EnvelopeSchema: "envelope/v2", ID: responseID, Actor: resolved, Created: now,
				Fields: respFields, Body: bodyOverride,
			})
			if err != nil {
				return nil, "", fmt.Errorf("respond: render failed for %s: %w", parentID, err)
			}

			// A response answers the system that asked (§3.4.3: `to` carries
			// EXACTLY one entry). The requester is already in hand as
			// parentEnv.From from this loop's own legality check — never
			// re-derived. Same decode/assign/re-encode idiom runPublish uses
			// for a contract's `version`; never text surgery on a structured
			// document.
			respFm, err := artifact.ParseFrontmatter(draft)
			if err != nil {
				return nil, "", fmt.Errorf("respond: parse rendered response for %s: %w", parentID, err)
			}
			var respDoc map[string]any
			if err := yaml.Unmarshal(respFm.YAML, &respDoc); err != nil {
				return nil, "", fmt.Errorf("respond: decode rendered response for %s: %w", parentID, err)
			}
			respDoc["to"] = []string{parentEnv.From}
			// delivers (judge-the-thing-2026-08 P2, closing HALF of B22):
			// same decode/assign/re-encode idiom as `to` above, and the same
			// "written only when the caller actually named a package"
			// discipline internal/cli's RespondCommand.Run uses for its own
			// identical `respDoc["delivers"]` assignment — an unconditional
			// `delivers: []` would declare an announcement nobody made, and
			// its ABSENCE is the ordinary answer shape the schema's own
			// description turns on.
			if len(in.Delivers) > 0 {
				respDoc["delivers"] = append([]string(nil), in.Delivers...)
			}
			// unmet/standing/blocked_by (defects-fix-2026-08 P2): same
			// decode/assign/re-encode idiom as `to` above, and the same
			// "written only when the caller actually gave it" discipline as
			// internal/cli's RespondCommand.Run (P-1 — absence stays
			// absence). P3 widening: HOMOGENEOUS by construction —
			// respondResolveUnmet already guarantees every entry shares one
			// form (the same parent, the same idsDeclared flag), so which
			// branch fires is decided once, from the first entry.
			if len(unmet) > 0 {
				if unmet[0].Criterion != "" {
					unmetSeq := make([]map[string]string, len(unmet))
					for i, u := range unmet {
						unmetSeq[i] = map[string]string{"criterion": u.Criterion}
					}
					respDoc["unmet"] = unmetSeq
				} else {
					unmetSeq := make([]int, len(unmet))
					for i, u := range unmet {
						unmetSeq[i] = *u.Index
					}
					respDoc["unmet"] = unmetSeq
				}
			}
			if in.Standing != "" {
				respDoc["standing"] = in.Standing
			}
			if in.BlockedBy != nil {
				respDoc["blocked_by"] = map[string]string{
					"reason_code": in.BlockedBy.ReasonCode,
					"owner":       in.BlockedBy.Owner,
					"needs":       in.BlockedBy.Needs,
				}
			}
			respYAML, err := yaml.Marshal(respDoc)
			if err != nil {
				return nil, "", fmt.Errorf("respond: encode rendered response for %s: %w", parentID, err)
			}
			draft = artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: respYAML, Body: respFm.Body})

			files = append(files, space.FileWrite{Path: layout.Exchange(responseID), Content: draft})

			respondEventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("respond: cannot mint event id: %w", err)
			}
			respondEvent := eventDoc{
				Schema: "event/v1", Event: respondEventID.String(), Space: parentProbe.Space,
				Subject: parentID, Transition: fold.TRespond, State: eventReceiptState(evaluation),
				Actor: eventActorFrom(resolved, actor.System),
				At:    now.UTC().Format(time.RFC3339),
				Refs:  []refEntry{{Ref: responseID}},
			}
			respondRaw, merr := yaml.Marshal(respondEvent)
			if merr != nil {
				return nil, "", fmt.Errorf("respond: cannot encode respond event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), respondEventID.String()), Content: respondRaw})

			ids = append(ids, parentID, responseID)
		}

		req := deps.buildRequest(ids, files, "respond", false)
		req.OperationKey = operationKey
		result, err := deps.submit(ctx, req, "respond", ids)
		return result, "", err
	}
}

// parentThread reads parentID's committed envelope frontmatter for its
// `thread` field — §T1 "Propagate"'s source of truth. loadEnvelope's own
// envelopeProbe (eventdoc.go) does not decode `thread` and eventdoc.go is
// outside this wave's allowlist, so this is a narrow, package-local
// re-decode of the SAME committed file using the SAME unexported helpers
// loadEnvelope already calls (artifactPath, readBoundedFile,
// maxMirrorEventBytes) — a second file read, not a second layout/read
// implementation. Flagged in this wave's deviations as a candidate for
// folding into envelopeProbe as a one-field, lead-side cleanup.
func parentThread(mirrorDir, id string) (string, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return "", fmt.Errorf("mcp: %s: %w", id, err)
	}
	relPath, err := artifactPath(parsed)
	if err != nil {
		return "", err
	}
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if err != nil {
		return "", fmt.Errorf("mcp: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return "", fmt.Errorf("mcp: %s: %w", id, err)
	}
	var probe struct {
		Thread string `yaml:"thread"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return "", fmt.Errorf("mcp: %s: cannot decode envelope: %w", id, err)
	}
	return probe.Thread, nil
}

// parentAcceptanceCriteria reads id's committed envelope frontmatter for
// its `acceptance_criteria[]` field (defects-fix-2026-08 P3: the id-form
// `--unmet`/verify-`criterion` resolution needs the parent's own declared
// criteria, exactly as parentThread above needs `thread`) — the SAME
// narrow, package-local re-decode idiom parentThread's own doc comment
// already established and flags as a lead-side envelopeProbe-folding
// candidate: eventdoc.go's envelopeProbe is off this phase's allowlist, so
// this is a second file read via the SAME unexported helpers loadEnvelope
// already calls (artifactPath, readBoundedFile, maxMirrorEventBytes),
// never a second layout/read implementation.
func parentAcceptanceCriteria(mirrorDir, id string) ([]RespondCriterion, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: %w", id, err)
	}
	relPath, err := artifactPath(parsed)
	if err != nil {
		return nil, err
	}
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if err != nil {
		return nil, fmt.Errorf("mcp: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: %w", id, err)
	}
	var probe struct {
		AcceptanceCriteria []RespondCriterion `yaml:"acceptance_criteria"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil, fmt.Errorf("mcp: %s: cannot decode envelope: %w", id, err)
	}
	return probe.AcceptanceCriteria, nil
}

// respondSeed builds respond's own canonical, content-derived seed
// (mirrors internal/cli's lifecycleRespondSeed exactly — fixed-order join,
// SORTED field keys, no `now`).
//
// unmet/standing/blockedBy (defects-fix-2026-08 P2) join the seed for the
// same reason internal/cli's lifecycleRespondSeed folds them in: they are
// content, not a derived default, and a retry differing ONLY in one of the
// three must mint a DIFFERENT responseID, not collide with a prior response
// that named none of them. Written after respFields/before body, matching
// where CLI writes them relative to ITS OWN structure (CLI additionally has
// a refs segment there; this surface has no `--ref` equivalent yet, B22).
// An absent unmet/standing/blockedBy (every caller before this phase)
// writes exactly the bytes respondSeed always wrote — no existing
// responseID changes.
//
// delivers (judge-the-thing-2026-08 P2) joins LAST, in GIVEN order, exactly
// where internal/cli's own lifecycleRespondSeed writes it (after
// blocked_by, before body) — CONTENT, not a derived default, so two
// responses on one parent differing ONLY in delivers must mint different
// response ids. Written only when non-empty, so every caller that names
// none (every MCP respond before this phase) writes exactly the bytes this
// function always wrote and no existing responseID moves.
func respondSeed(parentID, result string, respFields map[string]string, bodyOverride []byte, actor fold.Actor, unmet []RespondUnmetEntry, standing string, blockedBy *RespondBlockedBy, delivers []string) []byte {
	keys := make([]string, 0, len(respFields))
	for k := range respFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("parent=" + parentID + "\n")
	buf.WriteString("result=" + result + "\n")
	for _, k := range keys {
		buf.WriteString(k + "=" + respFields[k] + "\n")
	}
	// The ordinal branch writes the EXACT byte format every pre-P3 caller
	// already hashed ("unmet=<n>\n") — an existing bare-integer `unmet`
	// call must keep minting the same responseID it always did. The
	// criterion branch ("unmet=<id>\n") is new content no legacy caller
	// could ever have produced, so it cannot collide with anything already
	// committed.
	for _, u := range unmet {
		switch {
		case u.Criterion != "":
			buf.WriteString("unmet=" + u.Criterion + "\n")
		case u.Index != nil:
			fmt.Fprintf(&buf, "unmet=%d\n", *u.Index)
		}
	}
	if standing != "" {
		buf.WriteString("standing=" + standing + "\n")
	}
	if blockedBy != nil {
		buf.WriteString("blocked_by=" + blockedBy.ReasonCode + ":" + blockedBy.Owner + ":" + blockedBy.Needs + "\n")
	}
	for _, packageID := range delivers {
		buf.WriteString("delivers=" + packageID + "\n")
	}
	buf.WriteString("body=")
	buf.Write(bodyOverride)
	buf.WriteString("\n")
	buf.WriteString("actor.kind=" + actor.Kind + "\n")
	buf.WriteString("actor.name=" + actor.Name + "\n")
	buf.WriteString("actor.system=" + actor.System + "\n")

	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

// --- verify (response-scoped, D-024 single-response convenience close) --

// VerifyInput is a2a_verify's structured input.
type VerifyInput struct {
	// Action is a2a_exchange's own discriminator — never read here; see
	// LifecycleInput's identical field for why it exists.
	Action  string   `json:"action,omitempty"`
	Targets []string `json:"targets"`
	// Refs disambiguates which of a target's own response candidates
	// resolveResponseID should resolve — genuinely read (unlike
	// respond/dispute/note, which carry no such field at all), but no
	// longer PUBLISHED on a2a_exchange's own schema (spec 04 D4): see
	// this package's tools.go a2a_exchange registration comment for the
	// withdrawal and its real, reported cost to this field's own
	// discoverability.
	Refs  string     `json:"refs,omitempty"`
	Actor ActorInput `json:"actor,omitempty"`
	// Verdicts is P1's own field (one-answer-2026-08 spec 01): the
	// verifier's per-criterion judgement, resolved ONCE against
	// targets[0]'s own parent and applied uniformly to every target's
	// verify event AND its D-024 paired close event in the batch loop
	// below — mirrors internal/cli's VerifyCommand.Run exactly, including
	// its own identical "one verdict set, uniform across the whole batch"
	// assumption.
	Verdicts []VerdictInputEntry `json:"verdicts,omitempty"`
}

func newVerifyHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in VerifyInput
		if err := decodeStrict(args, &in, "verify", 0); err != nil {
			return nil, "", err
		}
		if len(in.Targets) == 0 {
			return nil, "", fmt.Errorf("verify: targets is required")
		}

		// Resolve this call's target space from targets, BEFORE the first
		// deps.MirrorDir read below (resolveResponseID inside the loop) —
		// the resolved deps is then used for every subsequent read in this
		// call, including resolveResponseID's own deps.MirrorDir argument.
		// See newLifecycleHandler's identical comment for the shadowing
		// discipline this repeats.
		deps, err := resolveWriteSpace(deps, "", in.Targets)
		if err != nil {
			return nil, "", fmt.Errorf("verify: %w", err)
		}

		// eventSchema/verdicts (P1): mirrors internal/cli's
		// VerifyCommand.Run exactly — floor-gated the SAME way
		// newLifecycleHandler's own identical close-row block is, resolved
		// ONCE against targets[0]'s own parent (an indirection through the
		// response, unlike close's own "no response indirection").
		eventSchema := lifecycleEventSchema(deps.Manifest.MinBinaryVersion)
		if len(in.Verdicts) > 0 && eventSchema != "event/v2" {
			return nil, "", fmt.Errorf(
				"verify: verdicts requires this space's min_binary_version to be at or above %s (event/v2); this space's floor is %q",
				contract.ContractPublicationFloor, deps.Manifest.MinBinaryVersion)
		}
		var verdicts []VerdictInputEntry
		if len(in.Verdicts) > 0 {
			firstResponseID, rerr := resolveResponseID(deps.MirrorDir, in.Targets[0], in.Refs)
			if rerr != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", in.Targets[0], rerr)
			}
			_, firstResponseProbe, rerr := loadEnvelope(deps.MirrorDir, firstResponseID)
			if rerr != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", firstResponseID, rerr)
			}
			criteria, cerr := parentAcceptanceCriteria(deps.MirrorDir, firstResponseProbe.Parent)
			if cerr != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", firstResponseProbe.Parent, cerr)
			}
			verdicts, rerr = resolveVerdicts(in.Verdicts, criteria)
			if rerr != nil {
				return nil, "", fmt.Errorf("verify: %w", rerr)
			}
		}
		// verdictsPtr: nil (omitted) below the floor; non-nil (present,
		// even when empty) at/above it — see newLifecycleHandler's
		// identical comment for why the schema's own conditional requires
		// this.
		var verdictsPtr *[]VerdictInputEntry
		if eventSchema == "event/v2" {
			verdictsPtr = &verdicts
		}

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("verify: %w", actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		// operationKey (P1): derived only when verdicts actually carry
		// content — below the floor, or above it with none supplied,
		// verify keeps its EXISTING dedup mechanism unchanged. Covers the
		// D-024 paired close too (operation.Verify's own doc comment):
		// both ride the ONE branch this key names, since they commit
		// together.
		var operationKey string
		if len(verdicts) > 0 {
			opVerdicts := make([]operation.VerdictEntry, len(verdicts))
			for i, v := range verdicts {
				opVerdicts[i] = operation.VerdictEntry{Index: v.resolvedIndex, Verdict: v.Verdict, CauseOwner: v.CauseOwner}
			}
			operationKey = operation.Verify(deps.OwnSystem, actor.Kind, actor.Name, in.Targets, opVerdicts)
		}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("verify: %w", err)
		}

		var files []space.FileWrite
		var ids []string
		for _, target := range in.Targets {
			responseID, err := resolveResponseID(deps.MirrorDir, target, in.Refs)
			if err != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", target, err)
			}

			evaluation, parentEnv, parentID, result, err := evaluateResponseCandidate(deps.MirrorDir, deps.Manifest, responseID, fold.Event{
				Transition: fold.TVerify, Actor: actor,
			})
			if err != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", responseID, err)
			}
			if evaluation.Verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("verify: %w", verdictError(responseID, evaluation.Verdict))
			}
			_, parentProbe, err := loadEnvelope(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", parentID, err)
			}

			verifyEventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("verify: cannot mint event id: %w", err)
			}
			verifyEvent := eventDoc{
				Schema: eventSchema, Event: verifyEventID.String(), Space: parentProbe.Space,
				Subject: responseID, Transition: fold.TVerify, State: eventReceiptState(evaluation),
				Actor: eventActorFrom(resolved, actor.System),
				At:    now.UTC().Format(time.RFC3339),
				// Verdicts (P1): present, even empty, on EVERY event/v2
				// verify — schemas/event/v2/event.schema.json's conditional
				// requires the key regardless of whether this invocation
				// named any verdicts.
				Verdicts: verdictsPtr,
			}
			verifyRaw, merr := yaml.Marshal(verifyEvent)
			if merr != nil {
				return nil, "", fmt.Errorf("verify: cannot encode event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), verifyEventID.String()), Content: verifyRaw})
			ids = append(ids, responseID)

			// D-024 convenience: single-response exchange also closes the
			// parent in the SAME PR.
			if len(result.Responses) == 1 {
				closeEvaluation := fold.EvaluateCandidate(parentEnv.Kind, evaluation.Result, fold.Event{
					Subject: parentID, Transition: fold.TClose, Actor: actor,
				}, parentEnv, membership(deps.Manifest))
				if closeEvaluation.Verdict != fold.VerdictLegal {
					continue
				}
				closeEventID, err := artifact.MintULIDAt(now, deps.Entropy)
				if err != nil {
					return nil, "", fmt.Errorf("verify: cannot mint event id: %w", err)
				}
				closeEvent := eventDoc{
					Schema: eventSchema, Event: closeEventID.String(), Space: parentProbe.Space,
					Subject: parentID, Transition: fold.TClose, State: eventReceiptState(closeEvaluation),
					Actor: eventActorFrom(resolved, actor.System),
					At:    now.UTC().Format(time.RFC3339),
					// Same verdicts as the paired verify above (D-024: it is
					// the SAME verification act) — the verifier's judgement
					// of the parent's acceptance criteria does not change
					// because the convenience close rides in the same PR.
					Verdicts: verdictsPtr,
				}
				closeRaw, merr := yaml.Marshal(closeEvent)
				if merr != nil {
					return nil, "", fmt.Errorf("verify: cannot encode close event: %w", merr)
				}
				files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), closeEventID.String()), Content: closeRaw})
				ids = append(ids, parentID)
			}
		}

		req := deps.buildRequest(ids, files, "verify", false)
		req.OperationKey = operationKey
		result, err := deps.submit(ctx, req, "verify", ids)
		return result, "", err
	}
}

// --- dispute (response-scoped) -------------------------------------------

// DisputeInput is a2a_dispute's structured input.
type DisputeInput struct {
	// Action is a2a_exchange's own discriminator — never read here; see
	// LifecycleInput's identical field for why it exists.
	Action     string     `json:"action,omitempty"`
	IDs        []string   `json:"ids"`
	Reason     string     `json:"reason"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Actor      ActorInput `json:"actor,omitempty"`
}

func newDisputeHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in DisputeInput
		if err := decodeStrict(args, &in, "dispute", 0); err != nil {
			return nil, "", err
		}
		if len(in.IDs) == 0 || in.Reason == "" {
			return nil, "", fmt.Errorf("dispute: ids and reason are required")
		}

		// Resolve this call's target space from ids, BEFORE the first
		// deps.MirrorDir read below (evaluateResponseCandidate inside the
		// loop) — see newLifecycleHandler's identical comment for the
		// shadowing discipline this repeats.
		deps, err := resolveWriteSpace(deps, "", in.IDs)
		if err != nil {
			return nil, "", fmt.Errorf("dispute: %w", err)
		}

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("dispute: %w", actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("dispute: %w", err)
		}

		var files []space.FileWrite
		for _, responseID := range in.IDs {
			evaluation, _, parentID, _, err := evaluateResponseCandidate(deps.MirrorDir, deps.Manifest, responseID, fold.Event{
				Transition: fold.TDispute, Actor: actor,
			})
			if err != nil {
				return nil, "", fmt.Errorf("dispute: %s: %w", responseID, err)
			}
			if evaluation.Verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("dispute: %w", verdictError(responseID, evaluation.Verdict))
			}
			_, parentProbe, err := loadEnvelope(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("dispute: %s: %w", parentID, err)
			}

			eventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("dispute: cannot mint event id: %w", err)
			}
			ev := eventDoc{
				Schema: "event/v1", Event: eventID.String(), Space: parentProbe.Space,
				Subject: responseID, Transition: fold.TDispute, State: eventReceiptState(evaluation),
				Actor: eventActorFrom(resolved, actor.System),
				At:    now.UTC().Format(time.RFC3339),
				Note:  in.Reason, ReasonCode: in.ReasonCode,
			}
			raw, merr := yaml.Marshal(ev)
			if merr != nil {
				return nil, "", fmt.Errorf("dispute: cannot encode event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
		}

		req := deps.buildRequest(in.IDs, files, "dispute", false)
		result, err := deps.submit(ctx, req, "dispute", in.IDs)
		return result, "", err
	}
}

// --- note (transition-free, D-025) ---------------------------------------

// NoteInput is a2a_note's structured input.
type NoteInput struct {
	// Action is a2a_exchange's own discriminator — never read here; see
	// LifecycleInput's identical field for why it exists.
	Action string     `json:"action,omitempty"`
	IDs    []string   `json:"ids"`
	Note   string     `json:"note"`
	Actor  ActorInput `json:"actor,omitempty"`
}

func newNoteHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in NoteInput
		if err := decodeStrict(args, &in, "note", 0); err != nil {
			return nil, "", err
		}
		if len(in.IDs) == 0 || in.Note == "" {
			return nil, "", fmt.Errorf("note: ids and note are required")
		}

		// Resolve this call's target space from ids, BEFORE the first
		// deps.MirrorDir read below (evaluateCandidate/loadEnvelope inside
		// the loop) — see newLifecycleHandler's identical comment for the
		// shadowing discipline this repeats.
		deps, err := resolveWriteSpace(deps, "", in.IDs)
		if err != nil {
			return nil, "", fmt.Errorf("note: %w", err)
		}

		resolved, actorErr := deps.ResolveActor(in.Actor)
		if actorErr != nil {
			return nil, "", fmt.Errorf("note: %w", actorErr)
		}
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("note: %w", err)
		}

		var files []space.FileWrite
		for _, id := range in.IDs {
			evaluation, _, _, err := evaluateCandidate(deps.MirrorDir, deps.Manifest, id, fold.Event{
				Transition: fold.TNote, Actor: actor,
			}, nil)
			if err != nil {
				return nil, "", fmt.Errorf("note: %s: %w", id, err)
			}
			if evaluation.Verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("note: %w", verdictError(id, evaluation.Verdict))
			}

			_, probe, err := loadEnvelope(deps.MirrorDir, id)
			if err != nil {
				return nil, "", fmt.Errorf("note: %s: %w", id, err)
			}
			eventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("note: cannot mint event id: %w", err)
			}
			ev := eventDoc{
				Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
				Subject: id, Transition: fold.TNote, State: eventReceiptState(evaluation),
				Actor: eventActorFrom(resolved, actor.System),
				At:    now.UTC().Format(time.RFC3339),
				Note:  in.Note,
			}
			raw, merr := yaml.Marshal(ev)
			if merr != nil {
				return nil, "", fmt.Errorf("note: cannot encode event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
		}

		req := deps.buildRequest(in.IDs, files, "note", false)
		result, err := deps.submit(ctx, req, "note", in.IDs)
		return result, "", err
	}
}
