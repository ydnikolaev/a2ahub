// OP-211 generic lifecycle verbs (spec 08 T1): ack/accept/decline/start/
// block/unblock/cancel/respond/verify/dispute/close/supersede/withdraw/
// satisfy/approve/reject/verify-pass/verify-fail/note. Every mutating verb
// batches N ids into one commit/one PR, runs V2 legality locally (via
// internal/fold, reused — never re-derived) BEFORE the funnel, and ships
// through the SAME uniform write funnel (auto-merge always on; no verb
// passes a gate/review parameter — approve/reject add an advisory PR
// marker only, per this phase's plan Placement decisions).
//
// This file's only package-level symbols are the per-verb command types
// (LifecycleCommand, RespondCommand, VerifyCommand, DisputeCommand,
// NoteCommand) + their NewXCommand constructors, the lifecycleVerbTable
// (Future-proofing table, §9) and file-private, uniquely-named helpers
// (lifecycle* prefix) — no shared helper, no package var beyond that
// table, per this phase's plan Placement decision (avoids collision with
// P7/P9's parallel verb files in this package). It never touches or
// imports P7's cmd_inbox/outbox/show/thread/search/statusline files, nor
// internal/cache.
package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// lifecycleFunnel is this file's own narrow consumer-side seam over
// *space.WriteFunnel (rails ISP/DI; own name, deliberately not shared with
// cmd_submit.go's submitFunnel — "disjoint files" per this phase's plan
// Placement decision) — tests inject a hand-written fake.
type lifecycleFunnel interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// lifecycleEnvelopeProbe is this file's own minimal envelope decode — the
// fields every OP-211 verb's legality/event-authoring path needs (id,
// type-derived facts are resolved from the §3.3 id prefix instead, see
// lifecyclePrefixInfo). A response's own `parent` field is the closure
// model's linkage (§3.4.6): "a response MUST reference its parent
// exchange ID."
type lifecycleEnvelopeProbe struct {
	ID                string   `yaml:"id"`
	Space             string   `yaml:"space"`
	From              string   `yaml:"from"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
	Parent            string   `yaml:"parent"` // response only
	// Thread is the source artifact's §3.8 thread id — spec 46 §T1 R2:
	// `respond` propagates the PARENT's thread onto its response, never
	// minting or inventing a new one for a derived artifact.
	Thread string `yaml:"thread"`
	// AcceptanceCriteria is defects-fix-2026-08 P3's own read of
	// schemas/envelope/v2/base.schema.json's `acceptance_criteria[]` —
	// this file's own probe already decodes a parent's frontmatter for
	// every other field above, so the pre-mint echo (T1) and the
	// `--verdict`/`--unmet` id-form resolution below ride that SAME read
	// rather than a second one through internal/validate's
	// ParentCriteriaCounter/ResponseParentResolver (REF-019's own
	// resolver, off this phase's allowlist — see this phase's own
	// Deviations report for why widening it was not needed here). Absent
	// or empty on any parent that carries no `acceptance_criteria[]` at
	// all (a decision, most requirements) — never an error, since this
	// probe already treats every field as "whatever the document has".
	AcceptanceCriteria []lifecycleAcceptanceCriterion `yaml:"acceptance_criteria"`
}

// lifecycleAcceptanceCriterion is one parsed entry of a parent's own
// `acceptance_criteria[]` — schemas/envelope/v2/base.schema.json's P3
// widening accepts a HOMOGENEOUS array of either bare strings (the
// original, ordinal-addressed form) or `{id, text}` objects (the new
// id-addressed form); this type reads whichever shape a committed
// document actually carries. ID is "" for the plain-string form (the
// array has no ids to resolve against — --verdict/--unmet stay
// integer-only for that parent); schema-side homogeneity/shape
// enforcement is server/CI's job (internal/validate), not this decode's.
type lifecycleAcceptanceCriterion struct {
	ID   string
	Text string
}

// UnmarshalYAML implements yaml.Unmarshaler so a single field can decode
// EITHER of acceptance_criteria[]'s two item shapes without a second
// probe type or a second parse pass.
func (c *lifecycleAcceptanceCriterion) UnmarshalYAML(value *yaml.Node) error {
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

// lifecyclePrefixInfo maps a §3.3 id prefix to its fold.Kind — the same
// table-driven idiom as cmd_submit.go's submitFirstTransition (Future-
// proofing table, §9: no per-type branch to hand-edit).
var lifecyclePrefixInfo = map[string]fold.Kind{
	"XC": fold.KindContract,
	"XR": fold.KindRequirement,
	"XQ": fold.KindQuestion,
	"XW": fold.KindWorkRequest,
	"XD": fold.KindDecision,
	"XH": fold.KindHandoff,
	"XS": fold.KindResponse,
	"XA": fold.KindAnnouncement,
}

// lifecycleArtifactPath resolves parsed's committed space-relative path
// per §4.2's layout (internal/space/layout.go) — this file's own copy of
// cmd_submit.go's submitSectionPath, keyed by id PREFIX rather than
// envelope `type` (an OP-211 verb reads an EXISTING artifact by id alone,
// before any envelope is available, unlike submit which already holds a
// parsed draft) — the same §4.2 shapes, including the contract's fixed
// provides/<slug>/contract.md filename, which V2's placement table now
// validates as such (fb-20260723-9ae145).
func lifecycleArtifactPath(parsed artifact.ID) (string, error) {
	switch parsed.Prefix {
	case "XC":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.ProvidesContract(parsed.Slug), nil
	case "XR":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.Requires(parsed.Raw), nil
	case "XD":
		return space.Decision(parsed.Raw), nil
	case "XQ", "XW", "XH", "XA", "XS":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.Exchange(parsed.Raw), nil
	default:
		return "", fmt.Errorf("cli: unknown artifact id prefix %q", parsed.Prefix)
	}
}

// lifecycleLoadEnvelope reads and parses id's committed artifact file from
// mirrorDir, returning fold's own minimal Envelope projection alongside
// this file's richer probe (for the `space`/`parent` fields fold.Envelope
// does not carry).
func lifecycleLoadEnvelope(mirrorDir, id string) (fold.Envelope, lifecycleEnvelopeProbe, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: %w", id, err)
	}
	kind, ok := lifecyclePrefixInfo[parsed.Prefix]
	if !ok {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: unknown artifact id prefix %q", id, parsed.Prefix)
	}
	relPath, err := lifecycleArtifactPath(parsed)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, err
	}
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: %w", id, err)
	}
	var probe lifecycleEnvelopeProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: cannot decode envelope: %w", id, err)
	}
	env := fold.Envelope{
		ID: id, Kind: kind, From: probe.From,
		To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
	}
	return env, probe, nil
}

// lifecycleEventDoc is this file's own minimal event/v1 document decode
// (reading back committed events for folding) AND encode (authoring new
// events) — a richer sibling of adapters.go's mirrorEvent/cmd_submit.go's
// submitEventDoc: this phase's verbs additionally need `refs` (the
// respond event's response-id linkage, since event/v1 has no dedicated
// `response_id` field — this phase's own resolution, see this phase's
// Deviations report) and `state`/`note`/`reason_code` (annotation and
// closed-enum reason fields §5.2.2).
type lifecycleEventDoc struct {
	Schema     string              `yaml:"schema"`
	Event      string              `yaml:"event"`
	Space      string              `yaml:"space"`
	Subject    string              `yaml:"subject"`
	Transition string              `yaml:"transition"`
	State      string              `yaml:"state,omitempty"`
	Actor      lifecycleEventActor `yaml:"actor"`
	At         string              `yaml:"at"`
	Note       string              `yaml:"note,omitempty"`
	Refs       []lifecycleRefEntry `yaml:"refs,omitempty"`
	ReasonCode string              `yaml:"reason_code,omitempty"`
	// Version is the contract-scope §5.2.2 optional field (cmd_contract.go
	// publish/deprecate/retire read and author it — shared here, not
	// duplicated, since both lifecycle and contract verbs decode/encode
	// the same event/v1 shape).
	Version string `yaml:"version,omitempty"`
	// Digest is the publish event's D-023 content identity and is computed
	// before the write. Commit is a legacy/reserved optional field that current
	// writers deliberately leave unset: the enclosing SHA exists only AFTER
	// the funnel commits the already-authored event. Version resolution derives
	// that SHA from the descriptor's Git history instead of backfilling a
	// second commit.
	Commit string `yaml:"commit,omitempty"`
	Digest string `yaml:"digest,omitempty"`
	// Verdicts is P6 wave C's own field (docs/features/active/
	// agent-exchange-2026-08/specs/06-incompleteness.md §7/§11's 2026-08-10
	// "wave C" amendment, threat-model.md T5): the verifier's per-criterion
	// mirror of a response's `unmet[]`, conditionally required by
	// schemas/event/v2/event.schema.json on `verify`/`close` — WITH a
	// pointer, not a bare slice: yaml.v3's `omitempty` drops an empty slice
	// exactly the same as a nil one (a bare `[]lifecycleVerdictEntry` field
	// with `omitempty` cannot express "the key is present and empty" versus
	// "the key is absent", and the schema's own description is explicit that
	// a close over a parent with no acceptance_criteria[] at all must stay
	// expressible with an empty array, not an absent key). Non-lifecycle
	// event authoring sites in this file (respond/dispute/note/the generic
	// table) leave this nil, which `omitempty` on the POINTER still omits —
	// so v1 writers are unaffected and the v1 schema's additionalProperties:
	// false is never violated.
	Verdicts *[]lifecycleVerdictEntry `yaml:"verdicts,omitempty"`
}

// lifecycleVerdictEntry is one RESOLVED entry of `a2a verify`'s `--verdict`
// flag and of the `verdicts[]` it authors — schemas/event/v2/
// event.schema.json's own shape, EITHER `{index, verdict, cause_owner}` (a
// parent with no declared ids) OR `{criterion, verdict, cause_owner}` (P3: a
// parent whose acceptance_criteria[] declares ids), never both on one entry.
// `cause_owner` is required on EVERY entry, including `met` ones ("so the
// array cannot mix attributed and unattributed judgements", that schema's
// own description) — spec 06 §7's prose calls it optional, but the shipped
// schema (this phase's ground truth) does not, so this type follows the
// schema.
//
// Index is a POINTER (the same "distinguish absent from zero" shape this
// file's own lifecycleEventDoc.Verdicts field doc comment already explains
// for the identical reason): `omitempty` on a bare `int` would silently
// drop `index: 0` — the FIRST criterion — while writing every other index
// correctly, exactly the class of bug a truncated array would hide.
// resolvedIndex is never nil/empty: lifecycleResolveVerdicts always fills it
// (from Index directly, or from Criterion's resolved array position) so
// operation.VerdictEntry and the canonical sort below have one stable
// identity regardless of which wire form an entry actually carries —
// internal/operation/key.go keeps reading a plain int and needs no change.
type lifecycleVerdictEntry struct {
	Index         *int   `yaml:"index,omitempty"`
	Criterion     string `yaml:"criterion,omitempty"`
	Verdict       string `yaml:"verdict"`
	CauseOwner    string `yaml:"cause_owner"`
	resolvedIndex int
}

// lifecycleVerdictEnum is the closed vocabulary schemas/event/v2/
// event.schema.json's `verdicts[].verdict` enum carries — checked here so a
// malformed --verdict is refused locally (exit 2) rather than shipped to a
// PR the schema then rejects.
var lifecycleVerdictEnum = map[string]bool{
	"met": true, "unmet": true, "not_warranted": true, "not_exercised": true,
}

// lifecycleVerdictToken is `--verdict <index-or-criterion-id>:<verdict>:
// <cause_owner>`, parsed but NOT YET resolved against a parent —
// lifecycleParseVerdicts' own output. Exactly one of IndexToken/
// CriterionToken is set: a token that parses as a non-negative integer is
// an index; anything else is a criterion id. Resolution (which needs the
// parent's own acceptance_criteria[], not yet in hand at parse time) is
// lifecycleResolveVerdicts' job, below.
type lifecycleVerdictToken struct {
	IndexToken     *int
	CriterionToken string
	Verdict        string
	CauseOwner     string
}

// lifecycleParseVerdicts parses each `--verdict` flag value (repeatable;
// newStringList — the same DI/flag shape `--ref` uses, cmd_new.go) into
// unresolved tokens. It validates everything parse alone can check (shape,
// the verdict enum, cause_owner) but does NOT resolve a criterion id to a
// position, sort, or dedupe by resolved identity — lifecycleResolveVerdicts
// does that once the caller has loaded the parent's own acceptance_criteria[]
// (T1: the same read the pre-mint echo needs).
func lifecycleParseVerdicts(raw []string) ([]lifecycleVerdictToken, error) {
	out := make([]lifecycleVerdictToken, 0, len(raw))
	for _, v := range raw {
		parts := strings.SplitN(v, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("--verdict %q: want <index-or-criterion-id>:<met|unmet|not_warranted|not_exercised>:<cause_owner>", v)
		}
		verdict := parts[1]
		if !lifecycleVerdictEnum[verdict] {
			return nil, fmt.Errorf("--verdict %q: verdict must be one of met|unmet|not_warranted|not_exercised", v)
		}
		causeOwner := strings.TrimSpace(parts[2])
		if causeOwner == "" {
			return nil, fmt.Errorf("--verdict %q: cause_owner is required (schemas/event/v2/event.schema.json requires it on every entry, including met)", v)
		}
		token := lifecycleVerdictToken{Verdict: verdict, CauseOwner: causeOwner}
		if index, err := strconv.Atoi(parts[0]); err == nil {
			if index < 0 {
				return nil, fmt.Errorf("--verdict %q: index must be a non-negative integer", v)
			}
			token.IndexToken = &index
		} else {
			if parts[0] == "" {
				return nil, fmt.Errorf("--verdict %q: criterion id must not be empty", v)
			}
			token.CriterionToken = parts[0]
		}
		out = append(out, token)
	}
	return out, nil
}

// lifecycleDeclaredCriterionIDs returns criteria's own `id` values, in
// array order — used only to NAME what a parent declares in a refusal
// message (AC4: "naming what the parent DOES declare"), never to resolve
// anything itself.
func lifecycleDeclaredCriterionIDs(criteria []lifecycleAcceptanceCriterion) []string {
	var ids []string
	for _, c := range criteria {
		if c.ID != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// lifecycleResolveVerdicts resolves lifecycleParseVerdicts' raw tokens
// against a specific parent's `acceptance_criteria[]` (criteria may be nil
// or carry no ids at all — the ordinal-only case every caller before P3
// already used): a criterion-id token resolves to that criterion's array
// position; a bare-index token is used directly. Each direction is refused,
// locally and by name (AC4), when it does not match the parent's own shape:
// an id token against a parent that declares no ids, an unresolvable id, or
// a bare-index token against a parent that DOES declare ids — the last
// refusal is what actually closes the mis-binding fb-20260818-76f29d
// reported, rather than merely documenting the base.
//
// The returned entries are sorted by resolvedIndex — lifecycleParseVerdicts
// used to sort by Index directly (its own doc comment explains why: the
// SAME judgement set given via --verdict in a different flag order must
// mint the identical operation.Verify/Close key); resolvedIndex is what
// makes that guarantee hold for the id form too, since two entries naming
// the same criterion by different identities (an id and its own equivalent
// index) collide here exactly like two entries naming the same bare index
// always did.
func lifecycleResolveVerdicts(tokens []lifecycleVerdictToken, criteria []lifecycleAcceptanceCriterion) ([]lifecycleVerdictEntry, error) {
	idsDeclared := len(lifecycleDeclaredCriterionIDs(criteria)) > 0
	idPosition := map[string]int{}
	for i, c := range criteria {
		if c.ID != "" {
			idPosition[c.ID] = i
		}
	}
	out := make([]lifecycleVerdictEntry, 0, len(tokens))
	seen := map[int]bool{}
	for _, tok := range tokens {
		entry := lifecycleVerdictEntry{Verdict: tok.Verdict, CauseOwner: tok.CauseOwner}
		switch {
		case tok.CriterionToken != "":
			if !idsDeclared {
				return nil, fmt.Errorf("--verdict %s: this parent declares no criterion ids; use a 0-based index into its acceptance_criteria[] instead", tok.CriterionToken)
			}
			pos, ok := idPosition[tok.CriterionToken]
			if !ok {
				return nil, fmt.Errorf("--verdict %s: no such criterion id; this parent declares: %s", tok.CriterionToken, strings.Join(lifecycleDeclaredCriterionIDs(criteria), ", "))
			}
			entry.Criterion = tok.CriterionToken
			entry.resolvedIndex = pos
		case tok.IndexToken != nil:
			if idsDeclared {
				return nil, fmt.Errorf("--verdict %d: this parent declares criterion ids; use one of them instead of a bare index (declares: %s)", *tok.IndexToken, strings.Join(lifecycleDeclaredCriterionIDs(criteria), ", "))
			}
			index := *tok.IndexToken
			entry.Index = &index
			entry.resolvedIndex = index
		default:
			return nil, fmt.Errorf("--verdict: token carries neither an index nor a criterion id")
		}
		if seen[entry.resolvedIndex] {
			return nil, fmt.Errorf("--verdict: two entries resolve to the same criterion (position %d) — one verdict per judged criterion", entry.resolvedIndex)
		}
		seen[entry.resolvedIndex] = true
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].resolvedIndex < out[j].resolvedIndex })
	return out, nil
}

// lifecycleVerdictEntryKey identifies WHICH --verdict token a resolved
// entry came from — the identity a batch's per-target resolutions must
// agree on, whatever POSITION it lands at in each parent's own
// acceptance_criteria[] (spec 04 T1: "the same id may sit at different
// positions" is legal; only a different referent is refused).
func lifecycleVerdictEntryKey(v lifecycleVerdictEntry) string {
	if v.Criterion != "" {
		return "criterion:" + v.Criterion
	}
	return "index:" + strconv.Itoa(*v.Index)
}

// lifecycleVerdictEntryLabel is lifecycleVerdictEntryKey's human-facing
// form — what a batch refusal names as "the token" (B31 refusal standard).
func lifecycleVerdictEntryLabel(v lifecycleVerdictEntry) string {
	if v.Criterion != "" {
		return v.Criterion
	}
	return strconv.Itoa(*v.Index)
}

// lifecycleCriterionTextAt returns criteria[idx].Text and true, or ("",
// false) when idx does not name an existing criterion — the same
// "never guess" bounds check lifecycleEchoVerdicts already applies to its
// own printed text, reused here so a batch's uniformity check treats an
// out-of-range resolution as exactly what it is: this parent cannot bind
// that token, which is what AC2 ("an index in range for one [parent] only")
// means in practice.
func lifecycleCriterionTextAt(criteria []lifecycleAcceptanceCriterion, idx int) (string, bool) {
	if idx < 0 || idx >= len(criteria) {
		return "", false
	}
	return criteria[idx].Text, true
}

// lifecycleVerdictParentBinding is one target's own resolved verdict set,
// the parent id it was resolved against (for the refusal message and the
// per-target echo prefix), and the parent's own acceptance_criteria[] (for
// both the uniformity check and the per-target echo's text lookup).
type lifecycleVerdictParentBinding struct {
	TargetID string
	ParentID string
	Criteria []lifecycleAcceptanceCriterion
	Verdicts []lifecycleVerdictEntry
}

// lifecycleVerdictsBindUniformly implements spec 04 T1's forced rule
// (§11 Amendments: operation.Verify/Close take ONE verdict slice for the
// whole batch — internal/operation/key.go:251/:317 — so independent
// per-target verdict sets are structurally unavailable without widening
// that package first, which is off this allowlist). Every target in
// bindings must resolve the SAME tokens to the SAME referent: a criterion
// that EXISTS in every target's own parent, with IDENTICAL text, whatever
// POSITION it sits at in each parent's own acceptance_criteria[] array.
//
// Called only for len(bindings) > 1 — a single target has nothing to
// disagree with, and this file's own non-negotiable keeps that path's
// pre-P4 behaviour (including its pre-P4 TOLERANCE of an out-of-range
// ordinal index — no bounds check ever ran for exactly one target, and
// this function must not retroactively add one) byte-identical.
func lifecycleVerdictsBindUniformly(flagName string, bindings []lifecycleVerdictParentBinding) error {
	for _, refEntry := range bindings[0].Verdicts {
		label := lifecycleVerdictEntryLabel(refEntry)
		key := lifecycleVerdictEntryKey(refEntry)
		var reports []string
		var refText string
		haveRef := false
		disagree := false
		for _, b := range bindings {
			var entry *lifecycleVerdictEntry
			for i := range b.Verdicts {
				if lifecycleVerdictEntryKey(b.Verdicts[i]) == key {
					entry = &b.Verdicts[i]
					break
				}
			}
			if entry == nil {
				// Every binding resolved the SAME tokens (this function only
				// runs once every target's own lifecycleResolveVerdicts has
				// already succeeded for that target) — defensive, should
				// never actually fire.
				return fmt.Errorf("%s: --%s %s: internal: not resolved against this target's parent %s", b.TargetID, flagName, label, b.ParentID)
			}
			text, ok := lifecycleCriterionTextAt(b.Criteria, entry.resolvedIndex)
			if !ok {
				reports = append(reports, fmt.Sprintf("%s (parent %s, %d criteria): no criterion at that position", b.TargetID, b.ParentID, len(b.Criteria)))
				disagree = true
				continue
			}
			reports = append(reports, fmt.Sprintf("%s (parent %s): %q", b.TargetID, b.ParentID, text))
			if !haveRef {
				refText, haveRef = text, true
			} else if text != refText {
				disagree = true
			}
		}
		if disagree {
			return fmt.Errorf("--%s %s: batch cannot bind uniformly — %s", flagName, label, strings.Join(reports, "; "))
		}
	}
	return nil
}

// lifecycleTruncateCriterionText implements AC1's "first 80 characters":
// []rune-bounded, never a byte slice — a byte-length truncation would split
// a multi-byte UTF-8 criterion mid-character (P3's own constraint: an
// ASCII-only fixture would never catch that class of corruption).
func lifecycleTruncateCriterionText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// lifecycleEchoVerdicts implements T1/US-1 (AC1, AC2): prints each resolved
// verdict's binding — the criterion it judges, truncated to its first 80
// characters, the verdict, and the cause_owner — BEFORE anything is minted,
// so the author sees exactly what they are asserting rather than trusting
// an unseen array's order (the incident fb-20260818-76f29d reported). Runs
// for BOTH parent shapes (AC2): an ordinal parent prints the bare index
// (`2 -> "…"`), an id-declaring parent prints the criterion id
// (`ac3 -> "…"`). criteria may be shorter than a resolved index actually
// needs (should not happen post-resolution, but this never guesses at
// missing text — it says so plainly instead, per this file's own "never
// print a guess" discipline).
func lifecycleEchoVerdicts(w io.Writer, verdicts []lifecycleVerdictEntry, criteria []lifecycleAcceptanceCriterion) {
	for _, v := range verdicts {
		key := v.Criterion
		if key == "" && v.Index != nil {
			key = strconv.Itoa(*v.Index)
		}
		text := "(criterion text unavailable)"
		if v.resolvedIndex >= 0 && v.resolvedIndex < len(criteria) {
			text = lifecycleTruncateCriterionText(criteria[v.resolvedIndex].Text, 80)
		}
		_, _ = fmt.Fprintf(w, "  %s -> %q  %s  (%s)\n", key, text, v.Verdict, v.CauseOwner)
	}
}

// lifecycleBlockedByReasonEnum is envelope/v2/response.schema.json's own
// `blocked_by.reason_code` enum, mirrored here (not $ref'd — that schema's
// own description explains why: internal/schema/corpus.go registers only
// refTarget entries, and a cross-family $ref from response.schema.json has
// nothing to resolve against without a corpus.go change off this wave's
// allowlist). The SAME vocabulary event/v2's own `reason_code` carries.
var lifecycleBlockedByReasonEnum = map[string]bool{
	"split-required": true, "security-concern": true, "out-of-scope": true,
	"duplicate": true, "other": true,
}

// lifecycleBlockedByNeedsEnum is envelope/v2/response.schema.json's own
// `blocked_by.needs` closed vocabulary.
var lifecycleBlockedByNeedsEnum = map[string]bool{
	"bytes": true, "judgement": true, "decision": true,
}

// lifecycleStandingEnum is envelope/v2/response.schema.json's own `standing`
// enum. Absence (the flag not given at all) means undeclared, which is NOT
// `authoritative` (P-1) — this table only validates a VALUE the caller
// actually supplied.
var lifecycleStandingEnum = map[string]bool{
	"authoritative": true, "provisional": true, "advisory": true,
}

// lifecycleBlockedByEntry is `a2a respond`'s `--blocked-by` flag, parsed —
// envelope/v2/response.schema.json's own `blocked_by` shape,
// `{reason_code, owner, needs}`, ALL THREE required
// (`"required": ["reason_code", "owner", "needs"]`,
// `"additionalProperties": false`). blocked_by is a single OBJECT on that
// schema, not an array, so unlike `--unmet`/`--verdict` this flag is not
// repeatable — a second `--blocked-by` overwrites the first, exactly like
// `--result`/`--standing`.
type lifecycleBlockedByEntry struct {
	ReasonCode string
	Owner      string
	Needs      string
}

// lifecycleParseBlockedBy parses one `--blocked-by
// <reason_code>:<owner>:<needs>` flag value.
//
// DEVIATION from this phase's own spec text, recorded here rather than left
// implicit: the spec and defect doc both write the flag's argument form as
// `<reason_code>:<owner>` (two segments). envelope/v2/response.schema.json's
// `blocked_by` requires THREE properties — `reason_code`, `owner`, AND
// `needs` — and forbids any other key
// (`additionalProperties: false`), so a two-segment flag can never produce
// an object that validates: the anyOf branch that wants `unmet`+`blocked_by`
// would still refuse the draft for `blocked_by` itself being incomplete.
// `needs` gets no default here (P-1: absence must stay distinct from a
// declared value; silently defaulting to `bytes` would assert something the
// caller never claimed). Three segments, matching `--verdict`'s own
// `<index>:<verdict>:<cause_owner>` shape (this file's own precedent).
func lifecycleParseBlockedBy(raw string) (lifecycleBlockedByEntry, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 {
		return lifecycleBlockedByEntry{}, fmt.Errorf(
			"--blocked-by %q: want <reason_code>:<owner>:<needs> — needs is required by envelope/v2/response.schema.json (bytes|judgement|decision)", raw)
	}
	reasonCode := parts[0]
	if !lifecycleBlockedByReasonEnum[reasonCode] {
		return lifecycleBlockedByEntry{}, fmt.Errorf(
			"--blocked-by %q: reason_code must be one of split-required|security-concern|out-of-scope|duplicate|other", raw)
	}
	owner := strings.TrimSpace(parts[1])
	if owner == "" {
		return lifecycleBlockedByEntry{}, fmt.Errorf("--blocked-by %q: owner is required", raw)
	}
	needs := parts[2]
	if !lifecycleBlockedByNeedsEnum[needs] {
		return lifecycleBlockedByEntry{}, fmt.Errorf(
			"--blocked-by %q: needs must be one of bytes|judgement|decision", raw)
	}
	return lifecycleBlockedByEntry{ReasonCode: reasonCode, Owner: owner, Needs: needs}, nil
}

// lifecycleUnmetToken is `--unmet <index-or-criterion-id>`, parsed but not
// yet resolved — the same token shape lifecycleVerdictToken carries, for
// the same reason (resolution needs the parent's own acceptance_criteria[],
// not yet in hand at parse time).
type lifecycleUnmetToken struct {
	IndexToken     *int
	CriterionToken string
}

// lifecycleParseUnmet parses each `--unmet <index-or-criterion-id>` flag
// value (repeatable; newStringList — the same DI/flag shape `--ref`/
// `--verdict` use) into unresolved tokens — a token that parses as a
// non-negative integer is an index; anything else is a criterion id.
// lifecycleResolveUnmet (below) resolves, sorts, and dedupes them once the
// parent is in hand, mirroring lifecycleResolveVerdicts exactly (spec 03
// T1: "both parsers go through lifecycleParseVerdicts' resolution, not a
// second reader").
func lifecycleParseUnmet(raw []string) ([]lifecycleUnmetToken, error) {
	out := make([]lifecycleUnmetToken, 0, len(raw))
	for _, v := range raw {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, fmt.Errorf("--unmet %q: must not be empty", v)
		}
		if index, err := strconv.Atoi(trimmed); err == nil {
			if index < 0 {
				return nil, fmt.Errorf("--unmet %q: must be a non-negative integer", v)
			}
			out = append(out, lifecycleUnmetToken{IndexToken: &index})
			continue
		}
		out = append(out, lifecycleUnmetToken{CriterionToken: trimmed})
	}
	return out, nil
}

// lifecycleUnmetEntry is one RESOLVED `--unmet` entry — envelope/v2/
// response.schema.json's own widened `unmet[]` item shape (P3): EITHER a
// bare non-negative integer (a parent with no declared ids) OR
// `{criterion: <id>}` (a parent whose acceptance_criteria[] declares ids),
// resolved the same way lifecycleVerdictEntry is.
type lifecycleUnmetEntry struct {
	Index         *int
	Criterion     string
	resolvedIndex int
}

// lifecycleResolveUnmet mirrors lifecycleResolveVerdicts exactly (same
// resolution rule, same refusal shape, same canonical resolvedIndex sort) —
// `unmet[]` and `verdicts[]` both index the SAME parent
// acceptance_criteria[] array (this file's own long-standing comment on
// `--unmet`'s flag registration), so a criterion id or a bare index must
// resolve identically for both.
func lifecycleResolveUnmet(tokens []lifecycleUnmetToken, criteria []lifecycleAcceptanceCriterion) ([]lifecycleUnmetEntry, error) {
	idsDeclared := len(lifecycleDeclaredCriterionIDs(criteria)) > 0
	idPosition := map[string]int{}
	for i, c := range criteria {
		if c.ID != "" {
			idPosition[c.ID] = i
		}
	}
	out := make([]lifecycleUnmetEntry, 0, len(tokens))
	seen := map[int]bool{}
	for _, tok := range tokens {
		var entry lifecycleUnmetEntry
		switch {
		case tok.CriterionToken != "":
			if !idsDeclared {
				return nil, fmt.Errorf("--unmet %s: this parent declares no criterion ids; use a 0-based index into its acceptance_criteria[] instead", tok.CriterionToken)
			}
			pos, ok := idPosition[tok.CriterionToken]
			if !ok {
				return nil, fmt.Errorf("--unmet %s: no such criterion id; this parent declares: %s", tok.CriterionToken, strings.Join(lifecycleDeclaredCriterionIDs(criteria), ", "))
			}
			entry.Criterion = tok.CriterionToken
			entry.resolvedIndex = pos
		case tok.IndexToken != nil:
			if idsDeclared {
				return nil, fmt.Errorf("--unmet %d: this parent declares criterion ids; use one of them instead of a bare index (declares: %s)", *tok.IndexToken, strings.Join(lifecycleDeclaredCriterionIDs(criteria), ", "))
			}
			index := *tok.IndexToken
			entry.Index = &index
			entry.resolvedIndex = index
		default:
			return nil, fmt.Errorf("--unmet: token carries neither an index nor a criterion id")
		}
		if seen[entry.resolvedIndex] {
			return nil, fmt.Errorf("--unmet: two entries resolve to the same criterion (position %d) — one entry per unmet criterion", entry.resolvedIndex)
		}
		seen[entry.resolvedIndex] = true
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].resolvedIndex < out[j].resolvedIndex })
	return out, nil
}

// lifecycleUnmetEntryKey/lifecycleUnmetEntryLabel mirror
// lifecycleVerdictEntryKey/lifecycleVerdictEntryLabel exactly, for `--unmet`.
func lifecycleUnmetEntryKey(u lifecycleUnmetEntry) string {
	if u.Criterion != "" {
		return "criterion:" + u.Criterion
	}
	return "index:" + strconv.Itoa(*u.Index)
}

func lifecycleUnmetEntryLabel(u lifecycleUnmetEntry) string {
	if u.Criterion != "" {
		return u.Criterion
	}
	return strconv.Itoa(*u.Index)
}

// lifecycleUnmetParentBinding mirrors lifecycleVerdictParentBinding exactly,
// for `--unmet` — one parent's own resolved unmet set, the parent id it was
// resolved against, and its own acceptance_criteria[].
type lifecycleUnmetParentBinding struct {
	TargetID string
	ParentID string
	Criteria []lifecycleAcceptanceCriterion
	Unmet    []lifecycleUnmetEntry
}

// lifecycleUnmetsBindUniformly mirrors lifecycleVerdictsBindUniformly
// exactly, for `--unmet` — see that function's own doc comment for the
// forced rule this implements (spec 04 §11 Amendments: operation.Respond
// takes ONE unmet slice for the whole batch, the same structural
// constraint) and why it is gated on len(bindings) > 1.
func lifecycleUnmetsBindUniformly(bindings []lifecycleUnmetParentBinding) error {
	for _, refEntry := range bindings[0].Unmet {
		label := lifecycleUnmetEntryLabel(refEntry)
		key := lifecycleUnmetEntryKey(refEntry)
		var reports []string
		var refText string
		haveRef := false
		disagree := false
		for _, b := range bindings {
			var entry *lifecycleUnmetEntry
			for i := range b.Unmet {
				if lifecycleUnmetEntryKey(b.Unmet[i]) == key {
					entry = &b.Unmet[i]
					break
				}
			}
			if entry == nil {
				return fmt.Errorf("%s: --unmet %s: internal: not resolved against this target's parent %s", b.TargetID, label, b.ParentID)
			}
			text, ok := lifecycleCriterionTextAt(b.Criteria, entry.resolvedIndex)
			if !ok {
				reports = append(reports, fmt.Sprintf("%s (parent %s, %d criteria): no criterion at that position", b.TargetID, b.ParentID, len(b.Criteria)))
				disagree = true
				continue
			}
			reports = append(reports, fmt.Sprintf("%s (parent %s): %q", b.TargetID, b.ParentID, text))
			if !haveRef {
				refText, haveRef = text, true
			} else if text != refText {
				disagree = true
			}
		}
		if disagree {
			return fmt.Errorf("--unmet %s: batch cannot bind uniformly — %s", label, strings.Join(reports, "; "))
		}
	}
	return nil
}

// lifecycleEventSchema mirrors internal/contract/publication_plan.go's own
// authoring-floor split (PlanPublication, lines 396-403): a space whose
// `min_binary_version` (floor) is at or above contract.ContractPublicationFloor
// authors event/v2; below it, event/v1 — the SAME floor and the SAME
// direction, so a space that has already crossed the line for contract
// publication does not separately have to cross a second, unrelated one for
// verify/close to gain `verdicts[]`.
//
// An unparseable or absent floor (a space that has never set
// min_binary_version) fails CLOSED to event/v1 — version.OlderThan's own doc
// comment names this direction ("an unparseable version is treated as
// 'cannot verify', never as 'not older'"), and it is the conservative choice
// here too: event/v1 is always legal, while a floor this binary cannot
// verify has no business being trusted to author the newer, stricter shape.
func lifecycleEventSchema(floor string) string {
	belowFloor, err := version.OlderThan(floor, contract.ContractPublicationFloor)
	if err != nil || belowFloor {
		return "event/v1"
	}
	return "event/v2"
}

type lifecycleEventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"` // Model and Session are DETECTED (schemas/fill-classes.yaml) and were
	// structurally unreachable from this writer until P3: both event schemas
	// allow them, internal/validate's POL-016 bound-checks them, and no
	// first-party writer could produce either — so the policy was dead code
	// against everything that actually writes events.
	Model   string `yaml:"model,omitempty"`
	Session string `yaml:"session,omitempty"`
}

// eventActorFrom builds an event's actor block from the RESOLVED actor plus
// this project's own system id.
//
// It exists so the mapping lives in one place. Every call site used to write
// `lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System}`
// from a fold.Actor — and fold.Actor deliberately carries only what the fold
// needs to resolve a role, so `model` and `session` had nowhere to come from.
// Copying the mapping to eight sites is also how a ninth gets forgotten.
func eventActorFrom(resolved template.Actor, system string) lifecycleEventActor {
	return lifecycleEventActor{
		Kind:    resolved.Kind,
		Name:    resolved.Name,
		System:  system,
		Model:   resolved.Model,
		Session: resolved.Session,
	}
}

type lifecycleRefEntry struct {
	Ref string `yaml:"ref"`
}

// lifecycleReadAllEvents walks every <system>/events/<year>/<ulid>.yaml
// under mirrorDir — EVERY participating system's own section, not just
// this binary's configured own system: a lifecycle event is committed to
// the ACTING system's own section (§3.5), which for many OP-211
// transitions (ack/accept/decline/block/respond/...) is the OTHER party,
// not the artifact's owning system. Folding a subject's current state
// correctly therefore requires the full cross-system event set, not one
// system's own slice (the narrower scope adapters.go's LegalityAdapter
// needed for P6's entry-transitions-only scope).
func lifecycleReadAllEvents(mirrorDir string) ([]lifecycleEventDoc, error) {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "events", "*", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("cli: list committed events: %w", err)
	}
	out := make([]lifecycleEventDoc, 0, len(matches))
	for _, m := range matches {
		raw, err := readBoundedFile(m, maxMirrorEventBytes)
		if err != nil {
			return nil, err
		}
		var ev lifecycleEventDoc
		if err := yaml.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("cli: decode committed event %s: %w", m, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// lifecycleFoldEvents selects, from every committed event, the ones
// relevant to primaryID's own fold: events whose Subject is primaryID
// itself, UNION events whose Subject is a response id attached to
// primaryID via a `respond` event's refs[0] (verify/dispute target the
// response, D-024 — Fold needs both the parent's own primary events and
// the response-scoped ones to compute Result.Responses correctly).
// Ordering falls back to ULID only (ascending) — this phase inherits
// P6/adapters.go's own simplification of never threading real commit
// order (CommitSeq) through the mirror read; ULIDs are monotonic on mint
// time, an accepted approximation for one still-open batch, documented as
// a deviation.
func lifecycleFoldEvents(all []lifecycleEventDoc, primaryID string) []fold.Event {
	responseIDs := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == primaryID && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			responseIDs[ev.Refs[0].Ref] = true
		}
	}
	var out []fold.Event
	for _, ev := range all {
		if ev.Subject != primaryID && !responseIDs[ev.Subject] {
			continue
		}
		fe := fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition,
			ClaimedState: fold.State(ev.State),
			Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
			// contractCanonicalVersion (cmd_contract.go): fold.Result.Versions
			// is a map[string]State keyed on the raw string with no
			// canonicalization of its own — two spellings of one version
			// ("1.0.0" and "01.0.0") must reformat identically here, at the
			// ONE place every committed event's `version` field enters
			// fold's own input, or a non-canonically-spelled event (however
			// it was authored — this predates cmd_contract.go's own P4
			// write-side canonicalization) mismatches forever.
			Version: contractCanonicalVersion(ev.Version),
		}
		if ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			fe.ResponseID = ev.Refs[0].Ref
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ULID < out[j].ULID })
	return out
}

// lifecycleMembership adapts a space.Manifest into a fold.MembershipView —
// this file's own copy of adapters.go's LegalityAdapter.membershipView
// (same tiny logic, kept file-private per the "disjoint files" Placement
// decision rather than exported cross-file).
func lifecycleMembership(manifest space.Manifest) fold.MembershipView {
	return func(system string) fold.MembershipStatus {
		for _, p := range manifest.Participants {
			if p.System == system {
				if p.Status == "left" {
					return fold.MembershipLeft
				}
				return fold.MembershipMember
			}
		}
		return fold.MembershipUnknown
	}
}

// lifecycleEvaluateCandidate is the generic (non-response-scoped) pre-write
// evaluator every OP-211 verb except verify/dispute uses: read id's own
// committed envelope + full event history, fold to its full prior Result, and
// delegate legality, receipt applicability and outcome to
// fold.EvaluateCandidate — never re-deriving §3.4 locally.
//
// candidate.Version is "" for every non-contract-version transition (the fallback
// this leans on lands unchanged on the legacy version-less path — see
// fold.EvaluateCandidate's own doc comment); a contract publish/deprecate/
// retire caller supplies the version the candidate event itself names
// (P4, 04-per-version-lifecycle.plan.md). Passing "" for a contract that
// already has ANY recorded version is a caller bug, not this function's
// to guess around: contractVersionVerdict refuses a version-less publish
// outright once any version is recorded (fold/contract.go), so a caller
// must resolve its own version BEFORE calling this — never after.
// refs is the batch's own §5.2.2 `refs[].ref` values (lifecycleRefsFromFlag)
// — nil for every OP-211 caller but LifecycleCommand.Run's own supersede
// row, threaded through so this function can resolve the SUCCESSOR facts a
// decision-supersede row's declared Precondition checks (table.go), the
// same way SubmitValidatorAdapter's resolveSuccessorEnvelope (adapters.go)
// already does for the SUBMIT path — never a second, independently typed
// successor reader (this phase's own non-negotiable). See
// lifecycleResolveSuccessorFacts' own doc comment for the resolution rule.
//
// The returned *fold.SuccessorFacts is the SAME value this function
// already resolves internally and passes to
// fold.EvaluateCandidateWithSuccessor — surfaced to the caller (wave 2c,
// D-3) so a refusal on a decision-supersede candidate can discriminate
// "successor resolved, precondition failed" (LFC-005 alone) from
// "successor unresolvable" (LFC-005 paired with an LFC-006 advisory),
// mirroring internal/validate's own checkLifecycle discrimination
// (`ev.SuccessorEnvelope == nil`) at this package's own local gate. nil
// for every non-supersede/non-decision/ref-less call, exactly as
// lifecycleResolveSuccessorFacts' own doc comment already documents.
func lifecycleEvaluateCandidate(mirrorDir string, manifest space.Manifest, id string, candidate fold.Event, refs []lifecycleRefEntry) (fold.CandidateEvaluation, fold.Envelope, *fold.SuccessorFacts, error) {
	env, _, err := lifecycleLoadEnvelope(mirrorDir, id)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, nil, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, env, nil, err
	}
	events := lifecycleFoldEvents(all, id)
	membership := lifecycleMembership(manifest)

	// prior carries the FULL fold.Result (not just its .State) so a
	// contract on the per-version path answers per-version rather than
	// per-subject — see fold.EvaluateCandidate's own doc comment.
	prior := fold.NewResult(env.Kind)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, membership)
	}
	candidate.Subject = id
	successor := lifecycleResolveSuccessorFacts(mirrorDir, manifest, env.Kind, candidate.Transition, refs)
	return fold.EvaluateCandidateWithSuccessor(env.Kind, prior, candidate, env, membership, successor), env, successor, nil
}

// lifecycleResolveSuccessorFacts resolves the caller-supplied facts about
// the SUCCESSOR artifact a decision-supersede row's declared
// SuccessorPrecondition checks (table.go, fold.CheckCandidateWithSuccessor)
// — the CLI's own pre-write UX gate's read of the SAME capability
// internal/validate's SUBMIT path already uses via resolveSuccessorEnvelope
// (adapters.go): MirrorResolver.Successor (validate.SuccessorResolver),
// never a second successor reader (this phase's own non-negotiable — see
// this phase's report for why an off-limits-file duplicate was rejected).
//
// Gated exactly like resolveSuccessorEnvelope: only a decision's own
// supersede transition, with at least one ref, ever resolves — every other
// transition/kind/ref-less call returns nil (unresolved). Every row but
// the two decision-supersede rows carries PreconditionNone and never
// consults this value regardless (preconditionTable, table.go), so this
// gate is a cost optimization (skip the mirror read) rather than a
// correctness requirement — CheckCandidateWithSuccessor's own dispatch
// already ignores successor facts for a PreconditionNone row.
//
// A nil return (successorID absent from THIS caller's own local mirror,
// unparsable, or the resolver call failing) is D9's own "unresolved"
// case (types.go's SuccessorPrecondition doc comment) — CheckCandidateWith
// Successor refuses a Precondition-bearing row uniformly rather than
// silently granting on a resolution failure.
func lifecycleResolveSuccessorFacts(mirrorDir string, manifest space.Manifest, kind fold.Kind, transition string, refs []lifecycleRefEntry) *fold.SuccessorFacts {
	if transition != fold.TSupersede || kind != fold.KindDecision || len(refs) == 0 {
		return nil
	}
	resolver := NewMirrorResolver(mirrorDir, manifest)
	author, state, ok := resolver.Successor(refs[0].Ref)
	if !ok {
		return nil
	}
	return &fold.SuccessorFacts{Author: author, State: fold.State(state)}
}

// lifecycleEvaluateResponseCandidate is the verify/dispute pre-write
// evaluator: response closure state lives in the parent's full fold, while
// fold.EvaluateCandidate remains the sole owner of legality and receipts.
func lifecycleEvaluateResponseCandidate(mirrorDir string, manifest space.Manifest, responseID string, candidate fold.Event) (fold.CandidateEvaluation, fold.Envelope, string, fold.Result, error) {
	_, responseProbe, err := lifecycleLoadEnvelope(mirrorDir, responseID)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, err
	}
	parentID := responseProbe.Parent
	if parentID == "" {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, fmt.Errorf("cli: response %s carries no `parent` link", responseID)
	}
	parentEnv, _, err := lifecycleLoadEnvelope(mirrorDir, parentID)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, parentEnv, parentID, fold.Result{}, err
	}
	events := lifecycleFoldEvents(all, parentID)
	membership := lifecycleMembership(manifest)
	result := fold.Fold(parentEnv.Kind, parentEnv, events, membership)
	candidate.Subject = responseID
	evaluation := fold.EvaluateCandidate(parentEnv.Kind, result, candidate, parentEnv, membership)
	return evaluation, parentEnv, parentID, result, nil
}

func lifecycleReceiptState(evaluation fold.CandidateEvaluation) string {
	if !evaluation.Applicable {
		return ""
	}
	return string(evaluation.Outcome)
}

// lifecycleRefsFromFlag splits a comma-separated --refs value into
// event/v1 refs entries (§5.2.2's `refs[].ref`); an empty value yields no
// entries.
func lifecycleRefsFromFlag(v string) []lifecycleRefEntry {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]lifecycleRefEntry, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, lifecycleRefEntry{Ref: p})
		}
	}
	return out
}

// lifecycleValidateSatisfyRefs enforces §3.4.2's satisfy proof bundle at
// the CLI boundary: one resolvable, version-pinned contract plus one
// verified response whose parent is the requirement being satisfied. The
// lifecycle table deliberately owns only state transitions; cross-artifact
// reference integrity belongs here, where the committed mirror is available.
func lifecycleValidateSatisfyRefs(mirrorDir string, manifest space.Manifest, requirementID string, refs []lifecycleRefEntry) ([]lifecycleRefEntry, error) {
	if len(refs) != 2 {
		return nil, fmt.Errorf("--refs must contain exactly one XC contract@version and one XS response")
	}

	var contractID, contractVersion, responseID string
	normalizedRefs := make([]lifecycleRefEntry, len(refs))
	for i, entry := range refs {
		ref := entry.Ref
		if at := strings.LastIndex(ref, "@"); at >= 0 {
			id, rawVersion := ref[:at], ref[at+1:]
			parsed, err := artifact.ParseID(id)
			if err != nil || parsed.Prefix != "XC" {
				return nil, fmt.Errorf("invalid contract ref %q: want XC-...@<semver>", ref)
			}
			version, err := contractParseSemver(rawVersion)
			if err != nil {
				return nil, fmt.Errorf("invalid contract ref %q: %w", ref, err)
			}
			if contractID != "" {
				return nil, fmt.Errorf("--refs contains more than one contract ref")
			}
			contractID, contractVersion = id, version.String()
			normalizedRefs[i] = lifecycleRefEntry{Ref: contractID + "@" + contractVersion}
			continue
		}

		parsed, err := artifact.ParseID(ref)
		if err == nil && parsed.Prefix == "XC" {
			return nil, fmt.Errorf("contract ref %q must pin an explicit semantic version", ref)
		}
		if err != nil || parsed.Prefix != "XS" {
			return nil, fmt.Errorf("invalid response ref %q: want XS-prefixed response", ref)
		}
		if responseID != "" {
			return nil, fmt.Errorf("--refs contains more than one response ref")
		}
		responseID = ref
		normalizedRefs[i] = entry
	}
	if contractID == "" || responseID == "" {
		return nil, fmt.Errorf("--refs must contain one XC contract@version and one XS response")
	}

	contractEnv, _, err := lifecycleLoadEnvelope(mirrorDir, contractID)
	if err != nil {
		return nil, fmt.Errorf("contract ref %s@%s does not resolve: %w", contractID, contractVersion, err)
	}
	if contractEnv.Kind != fold.KindContract {
		return nil, fmt.Errorf("contract ref %s@%s resolves to %s, want contract", contractID, contractVersion, contractEnv.Kind)
	}
	responseEnv, responseProbe, err := lifecycleLoadEnvelope(mirrorDir, responseID)
	if err != nil {
		return nil, fmt.Errorf("response ref %s does not resolve: %w", responseID, err)
	}
	if responseEnv.Kind != fold.KindResponse {
		return nil, fmt.Errorf("response ref %s resolves to %s, want response", responseID, responseEnv.Kind)
	}
	if responseProbe.Parent != requirementID {
		return nil, fmt.Errorf("response ref %s has parent %q, want %q", responseID, responseProbe.Parent, requirementID)
	}

	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return nil, err
	}
	membership := lifecycleMembership(manifest)
	contractResult := fold.Fold(contractEnv.Kind, contractEnv, lifecycleFoldEvents(all, contractID), membership)
	if _, ok := contractResult.Versions[contractVersion]; !ok {
		return nil, fmt.Errorf("contract ref %s@%s does not resolve to a recorded version", contractID, contractVersion)
	}
	requirementEnv, _, err := lifecycleLoadEnvelope(mirrorDir, requirementID)
	if err != nil {
		return nil, err
	}
	requirementResult := fold.Fold(requirementEnv.Kind, requirementEnv, lifecycleFoldEvents(all, requirementID), membership)
	// A requirement does not have a parent-level `respond` transition. Its
	// response is attached by the XS envelope's `parent` field, so seed that
	// response's documented submitted state and replay its own closure events
	// through fold.Apply (the canonical authorization/transition engine).
	if requirementResult.Responses == nil {
		requirementResult.Responses = map[string]fold.State{}
	}
	requirementResult.Responses[responseID] = fold.StateSubmitted
	var responseEvents []lifecycleEventDoc
	for _, ev := range all {
		if ev.Subject == responseID {
			responseEvents = append(responseEvents, ev)
		}
	}
	sort.Slice(responseEvents, func(i, j int) bool { return responseEvents[i].Event < responseEvents[j].Event })
	for _, ev := range responseEvents {
		requirementResult = fold.Apply(requirementEnv.Kind, requirementEnv, requirementResult, fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition,
			ClaimedState: fold.State(ev.State),
			Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
		}, membership)
	}
	if got := requirementResult.Responses[responseID]; got != fold.StateVerified {
		return nil, fmt.Errorf("response ref %s is %q, want %q", responseID, got, fold.StateVerified)
	}

	return normalizedRefs, nil
}

// verdictRefusalMessage renders a human-readable local-refusal message for
// a non-legal fold.Verdict, naming the LFC- registry code (rails: "every
// violation carries a non-empty registry code" applies to any V2-class
// refusal surfaced to a human, CLI or otherwise).
func verdictRefusalMessage(id string, verdict fold.Verdict) string {
	switch verdict {
	case fold.VerdictIllegalTransition:
		return fmt.Sprintf("%s: refused: illegal transition for the current folded state (LFC-001)", id)
	case fold.VerdictUnauthorizedActor:
		return fmt.Sprintf("%s: refused: actor is not authorized for this transition (LFC-002)", id)
	default:
		return fmt.Sprintf("%s: refused: unknown verdict %v", id, verdict)
	}
}

// decisionSupersedeRefusalMessage renders the LFC-005 (optionally paired
// with an LFC-006 advisory) local-refusal message for a decision
// `supersede` candidate that CheckCandidateWithSuccessor refused as
// VerdictUnauthorizedActor (wave 2c, D-3 — this wave's own report).
//
// verdictRefusalMessage's own generic VerdictUnauthorizedActor branch
// mislabels this LFC-002 — the code this epic minted (LFC-005/LFC-006,
// internal/validate/lifecycle.go) was reachable only through the SUBMIT
// validation path (checkLifecycle), never through the verb a human or
// agent actually runs. This function is the CLI's own local gate learning
// the SAME discrimination checkLifecycle already applies:
// isDecisionSupersedeCandidate's own coarse test (transition ==
// "supersede" && kind == "decision" — table.go's THIRD decision-supersede
// row is included too: a plain wrong-owner refusal on a `proposed`
// decision's supersede relabels to LFC-005 as well, exactly the
// coarseness isDecisionSupersedeCandidate's own doc comment already
// records as known and deliberate, not a new one minted here). The
// discrimination itself lives at this function's own call site
// (LifecycleCommand.Run, below); this function only renders the two
// strings, reused VERBATIM from decisionSupersedePreconditionViolation/
// decisionSupersedeUnresolvedViolation (internal/validate/lifecycle.go,
// off this phase's allowlist) rather than a second, independently phrased
// refusal for the same rule.
//
// successorResolved mirrors checkLifecycle's own `ev.SuccessorEnvelope ==
// nil` test (D9's own rule, types.go): false means the successor could
// not be resolved AT ALL (never a synthesized resolution), which pairs
// the LFC-006 UNMEASURED advisory alongside LFC-005 — never alone,
// exactly as decisionSupersedeUnresolvedViolation's own doc comment
// requires. true means the successor WAS resolved and the precondition
// still failed — LFC-005 alone.
//
// The message names WHAT WOULD MAKE THE SUPERSEDE LEGAL (spec 06's own
// discoverability instrument, epic AC-2): an approved successor, or one
// the acting system authored.
func decisionSupersedeRefusalMessage(id string, successorResolved bool) string {
	// The wording is validate's, not this file's: ADR-019's move-down applied
	// to the refusal TEXT, because the text is part of the rule (spec 06's
	// discoverability instrument — the message names what would make the
	// supersede legal). internal/mcp/eventdoc.go references the same two
	// constants, so the two surfaces cannot drift by editing one.
	message := fmt.Sprintf("%s: refused: %s (LFC-005)", id, validate.DecisionSupersedePreconditionMessage)
	if !successorResolved {
		message += "; " + validate.DecisionSupersedeUnresolvedMessage + " (LFC-006)"
	}
	return message
}

// --- shared verb plumbing (constructor DI) -------------------------------

// lifecycleDeps is the common constructor-injected dependency set every
// OP-211 verb command needs — factored out so each NewXCommand constructor
// stays a short, readable wrapper (rails DI, anti-pattern #10: every field
// here is required).
type lifecycleDeps struct {
	funnel       lifecycleFunnel
	mirrorDir    string
	spaceID      string
	ownSystem    string
	manifest     space.Manifest
	hostCfg      SubmitHostConfig
	resolveActor func(ActorFlags) (template.Actor, error)
	pending      PendingMarker

	now      func() time.Time
	entropy  io.Reader
	readFile func(path string) ([]byte, error)
}

func newLifecycleDeps(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) lifecycleDeps {
	return lifecycleDeps{
		funnel: funnel, mirrorDir: mirrorDir, spaceID: spaceID, ownSystem: ownSystem,
		manifest: manifest, hostCfg: hostCfg, resolveActor: resolveActor,
		pending: NewNoopPendingMarker(),
		now:     time.Now, entropy: rand.Reader, readFile: os.ReadFile,
	}
}

func (d *lifecycleDeps) setPendingMarker(pending PendingMarker) {
	if pending == nil {
		pending = NewNoopPendingMarker()
	}
	d.pending = pending
}

func (d lifecycleDeps) refusalMessage(id string, verdict fold.Verdict) string {
	message := verdictRefusalMessage(id, verdict)
	if verdict != fold.VerdictIllegalTransition {
		return message
	}
	reader, ok := d.pending.(PendingMarkerReader)
	if !ok {
		return message
	}
	pending, found, err := reader.Pending(d.spaceID, id)
	if err != nil || !found {
		return message
	}
	return fmt.Sprintf("%s; previous write is pending-merge (PR %s); run `a2a await %s` and retry", message, pending.PRURL, id)
}

// lifecycleActorFlags registers the §7.4 explicit-actor-override flags
// every verb accepts (same three flags NewCommand already registers) onto
// fs, returning pointers Run reads after fs.Parse.
func lifecycleActorFlags(fs *flag.FlagSet) (kind, name, model *string) {
	kind = fs.String("actor-kind", "", "explicit actor.kind override")
	name = fs.String("actor-name", "", "explicit actor.name override")
	model = fs.String("actor-model", "", "explicit actor.model override")
	return
}

// buildRequest no longer defaults an empty BaseBranch to "main"
// (no-silent-yes-2026-08 Group A) — it returns d.hostCfg.BaseBranch as-is,
// same as space/funnel.go's own resolvedBaseBranch. buildRequest is called
// from BOTH this file (5 verbs) and cmd_contract.go's own 4 contract
// verbs, so it cannot itself refuse without breaking every one of those
// callers' single-value assignment (ADR-001-adjacent: no signature change
// here can be scoped to only this file's own callers). The refusal lives
// one call later, in submit below — the ONE place every caller of
// buildRequest already funnels through before any git/network call runs.
func (d lifecycleDeps) buildRequest(ids []string, files []space.FileWrite, verb string, gated bool) space.SubmitRequest {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	commitMsg := fmt.Sprintf("a2a(%s): %s", verb, strings.Join(sorted, ", "))
	baseBranch := d.hostCfg.BaseBranch
	var prBody string
	if gated {
		prBody = fmt.Sprintf("ADVISORY GATE: %s requires an approving CODEOWNERS review before auto-merge (§3.7 G3).", verb)
	}
	return space.SubmitRequest{
		RepoDir: d.mirrorDir, System: d.ownSystem,
		Verb: verb, ArtifactID: strings.Join(sorted, "+"), ArtifactIDs: sorted, Files: files,
		CommitMessage: commitMsg, CommitAuthorName: d.hostCfg.CommitAuthorName, CommitAuthorEmail: d.hostCfg.CommitAuthorEmail,
		RemoteURL: d.hostCfg.RemoteURL, Repo: d.hostCfg.Repo, BaseBranch: baseBranch,
		PRTitle: commitMsg, PRBody: prBody, Credential: d.hostCfg.Credential,
		// The write floor (CC-085) belongs on EVERY write, not only on
		// `a2a submit`. Until 2026-07-26 this field was set by exactly three
		// call sites — submit (CLI and MCP) and `space update` — so the funnel's
		// guard, which fires only `if req.MinBinaryVersion != ""`, was inert for
		// all 15 lifecycle verbs and for `contract publish/deprecate/retire`.
		//
		// That is not hypothetical. The branch grammar BranchName renders — the
		// funnel's idempotency key — changed in 0.4.0, and the getvisa space
		// still carries two open PRs for ONE `contract publish` of one artifact,
		// on the two grammars, because a pre-0.4.0 binary was allowed to write.
		// Raising a space's floor is exactly how an operator stops that, and it
		// was doing nothing on the verb that caused it.
		//
		// Read from the parsed manifest rather than re-probing space.yaml the
		// way SubmitCommand.readMinBinaryVersion does: this path already
		// REQUIRES a parsed manifest (membership resolution reads it), so if we
		// got here the field is available and a second file read would only add
		// a way for the two to disagree.
		MinBinaryVersion: d.manifest.MinBinaryVersion,
	}
}

func (d lifecycleDeps) submit(ctx context.Context, req space.SubmitRequest, verb string, ids []string, stdio IO) int {
	// req.BaseBranch reaches here as buildRequest returned it — d.hostCfg's
	// own field, never guessed (no-silent-yes-2026-08 Group A; see
	// buildRequest's own doc comment for why the refusal lives here, one
	// call later, rather than in buildRequest itself). Every one of
	// cmd_lifecycle.go's own 5 verbs AND cmd_contract.go's 4 contract verbs
	// funnels through this one method before any git/network call runs.
	if req.BaseBranch == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", verb, errMissingHostBaseBranch)
		return 1
	}
	result, err := d.funnel.Submit(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", verb, err)
		return 1
	}
	effectiveIDs := ids
	if len(result.ArtifactIDs) > 0 {
		effectiveIDs = result.ArtifactIDs
	}
	for _, id := range effectiveIDs {
		switch result.State {
		case space.WriteStatePendingMerge, space.WriteStateAlreadyOpen:
			if err := d.pending.MarkPending(ctx, d.spaceID, id, result); err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "%s: pending-merge marker failed: %v\n", verb, err)
				return 1
			}
		default:
			if clearer, ok := d.pending.(PendingMarkerClearer); ok {
				if err := clearer.ClearPending(d.spaceID, id); err != nil {
					_, _ = fmt.Fprintf(stdio.Stderr, "%s: pending-merge marker cleanup failed: %v\n", verb, err)
					return 1
				}
			}
		}
	}
	switch result.State {
	case space.WriteStateAlreadyOpen, space.WriteStateAlreadyMerged:
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: already submitted for %s (PR %s, %s)\n",
			verb, strings.Join(effectiveIDs, ", "), result.PRURL, result.State)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: opened PR %s for %s (%s)\n", verb, result.PRURL, strings.Join(effectiveIDs, ", "), result.State)
		warnAutoMerge(stdio, verb, result.AutoMergeNote)
	}
	return 0
}

// --- generic OP-211 verbs (table-driven) ---------------------------------

// lifecycleVerbSpec is one OP-211 generic verb's own row (Future-proofing
// table, §9: a new §3.4 transition slots in as a new row, never a new
// branch). Every field mirrors spec 08 T1's flags column exactly.
type lifecycleVerbSpec struct {
	Verb              string
	Transition        string
	Synopsis          string
	RequireReason     bool
	RequireReasonCode bool
	RequireRefs       bool
	RequireFindings   bool
	GateMarker        bool // ALWAYS advisory-gated (approve/reject, G3)
	// SupportsVerdicts is B24's own per-row switch (docs/features/active/
	// agent-exchange-2026-08/epic-backlog.md): true on EXACTLY the `close`
	// row. LifecycleCommand.Run reads it to decide, ONCE per invocation
	// (never per-id inside the batch loop — "transition-scoped, not applied
	// to the loop", B24's own framing), whether to register `--verdict`,
	// floor-gate the authored schema to event/v2, and derive an
	// operation.Close key — the SAME machinery VerifyCommand.Run already
	// carries for its own `verify`/D-024-close pair, reached here through
	// the shared parser (lifecycleParseVerdicts) rather than a second one.
	// Every other row leaves this false, which is what keeps their own Run
	// path byte-identical to before this field existed: no flag registered,
	// eventSchema fixed at "event/v1", verdictsPtr always nil.
	SupportsVerdicts bool
}

var lifecycleVerbTable = []lifecycleVerbSpec{
	{Verb: "ack", Transition: fold.TAcknowledge, Synopsis: "acknowledge one or more artifacts"},
	{Verb: "accept", Transition: fold.TAccept, Synopsis: "accept one or more artifacts"},
	{Verb: "decline", Transition: fold.TDecline, Synopsis: "decline one or more artifacts", RequireReason: true, RequireReasonCode: true},
	{Verb: "start", Transition: fold.TStart, Synopsis: "start work on one or more artifacts"},
	{Verb: "block", Transition: fold.TBlock, Synopsis: "block one or more artifacts on a blocker", RequireRefs: true},
	{Verb: "unblock", Transition: fold.TUnblock, Synopsis: "unblock one or more artifacts (recovers pre-block state)"},
	{Verb: "cancel", Transition: fold.TCancel, Synopsis: "cancel one or more artifacts"},
	{Verb: "close", Transition: fold.TClose, Synopsis: "close one or more responded parents: close <parent-id...> [--verdict <index-or-criterion-id>:<met|unmet|not_warranted|not_exercised>:<cause_owner>]...", SupportsVerdicts: true},
	{Verb: "withdraw", Transition: fold.TWithdraw, Synopsis: "withdraw one or more requirements or proposed decisions"},
	{Verb: "supersede", Transition: fold.TSupersede, Synopsis: "supersede an artifact with its successor", RequireRefs: true},
	{Verb: "satisfy", Transition: fold.TSatisfy, Synopsis: "satisfy a requirement", RequireRefs: true},
	{Verb: "approve", Transition: fold.TApprove, Synopsis: "approve a decision (always G3-gated)", GateMarker: true},
	{Verb: "reject", Transition: fold.TReject, Synopsis: "reject a decision (always G3-gated)", RequireReason: true, GateMarker: true},
	{Verb: "verify-pass", Transition: fold.TVerifyPass, Synopsis: "record a passing handoff verification"},
	{Verb: "verify-fail", Transition: fold.TVerifyFail, Synopsis: "record a failing handoff verification", RequireFindings: true},
}

// LifecycleCommand implements every table-driven OP-211 generic verb: N
// ids batched into one commit/one PR, V2 legality refusal locally BEFORE
// the funnel (AC-302.1), the SAME uniform funnel call (no gate parameter —
// approve/reject add an advisory PR marker only, P8-3).
type LifecycleCommand struct {
	spec lifecycleVerbSpec
	deps lifecycleDeps
}

// SetPendingMarker wires the machine-local pending-write store.
func (c *LifecycleCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// newLifecycleCommand is every generic-verb NewXCommand constructor's
// shared body (table-driven, §9 Future-proofing — one place to extend
// when a new §3.4 transition needs an OP-211 verb).
func newLifecycleCommand(spec lifecycleVerbSpec, funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return &LifecycleCommand{spec: spec, deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// NewAckCommand constructs `a2a ack <id...>`. funnel, manifest and
// resolveActor must not be nil/zero-configured (rails anti-pattern #10).
func NewAckCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[0], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewAcceptCommand constructs `a2a accept <id...>`.
func NewAcceptCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[1], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewDeclineCommand constructs `a2a decline <id...> --reason <text> [--reason-code <enum>]`.
func NewDeclineCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[2], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewStartCommand constructs `a2a start <id...>`.
func NewStartCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[3], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewBlockCommand constructs `a2a block <id...> --refs <blocker-id>`.
func NewBlockCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[4], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewUnblockCommand constructs `a2a unblock <id...>`.
func NewUnblockCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[5], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewCancelCommand constructs `a2a cancel <id...>`.
func NewCancelCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[6], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewCloseCommand constructs `a2a close <parent-id...>`.
func NewCloseCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[7], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewWithdrawCommand constructs `a2a withdraw <requirement-id...>`.
func NewWithdrawCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[8], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewSupersedeCommand constructs `a2a supersede <id> --refs <successor-id>`.
func NewSupersedeCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[9], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewSatisfyCommand constructs `a2a satisfy <requirement-id> --refs <XC-id@version>,<XS-id>`.
func NewSatisfyCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[10], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewApproveCommand constructs `a2a approve <decision-id>` (ALWAYS G3-gated, P8-3).
func NewApproveCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[11], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewRejectCommand constructs `a2a reject <decision-id> --reason <text>` (ALWAYS G3-gated, P8-3).
func NewRejectCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[12], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewVerifyPassCommand constructs `a2a verify-pass <handoff-id>`.
func NewVerifyPassCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[13], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewVerifyFailCommand constructs `a2a verify-fail <handoff-id> --findings <text>`.
func NewVerifyFailCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[14], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// Name implements cli.Command.
func (c *LifecycleCommand) Name() string { return c.spec.Verb }

// Synopsis implements cli.Command.
func (c *LifecycleCommand) Synopsis() string { return c.spec.Synopsis }

// Run implements cli.Command. Exit codes: 2 = usage; 1 = local legality
// refusal or a funnel/IO error (all-or-nothing across the batch, OP-220
// pattern); 0 = success.
func (c *LifecycleCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet(c.spec.Verb, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	reason := fs.String("reason", "", "reason text")
	reasonCode := fs.String("reason-code", "", "machine-readable reason code")
	refs := fs.String("refs", "", "comma-separated refs (blocker/successor/contract+response ids)")
	findings := fs.String("findings", "", "verification findings text")
	// --verdict (B24): registered ONLY when c.spec.SupportsVerdicts (the
	// close row) — NOT unconditionally like reason/refs/findings above.
	// Registering it on every row would let the other 14 verbs accept a
	// flag they cannot legally act on; leaving it off their FlagSet means
	// Go's own flag.ContinueOnError refuses it as an undefined flag
	// (parseArgsAnyOrder below returns that error verbatim), which is what
	// "must not grow the flag" is actually testing
	// (TestOtherVerbRefusesVerdictFlagUnregistered).
	var verdictFlags newStringList
	if c.spec.SupportsVerdicts {
		fs.Var(&verdictFlags, "verdict", "<index-or-criterion-id>:verdict:cause_owner verdict entry — index is 0-based into the parent's acceptance_criteria[], or its declared criterion id (e.g. ac1) when that array carries ids (repeatable; verdict is met|unmet|not_warranted|not_exercised)")
	}
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	// Wave K fix (live run 6, "thirteen verbs refuse a flag written after
	// their positional argument"): parseArgsAnyOrder, not a bare
	// fs.Parse(args) — this is the most important of the thirteen, since
	// it is every N-id batch verb's own Run (ack/accept/decline/start/
	// block/unblock/cancel/close/withdraw/supersede/satisfy/approve/
	// reject/verify-pass/verify-fail all share this one method via the
	// table). See parseArgsAnyOrder's own doc comment (cli.go).
	ids, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(ids) == 0 {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s <id...>\n", c.spec.Verb)
		return 2
	}
	if c.spec.RequireReason && *reason == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s --reason <text> <id...>\n", c.spec.Verb)
		return 2
	}
	if c.spec.RequireRefs && *refs == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s --refs <ref,...> <id...>\n", c.spec.Verb)
		return 2
	}
	if c.spec.RequireFindings && *findings == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s --findings <text> <id...>\n", c.spec.Verb)
		return 2
	}

	// verdicts/eventSchema (B24): parsed and floor-gated with the SAME
	// lifecycleParseVerdicts/lifecycleEventSchema machinery
	// VerifyCommand.Run uses — one guarantee, one implementation, reached
	// here rather than re-derived. Every other row leaves c.spec.SupportsVerdicts
	// false, so verdicts stays nil, eventSchema stays fixed at "event/v1",
	// and this whole block is a no-op for them (the schema CHOICE is decided
	// ONCE here, transition-scoped by the row rather than per-id inside the
	// batch loop below, which just applies the one decided value uniformly
	// — the same shape VerifyCommand.Run already uses across its own batch).
	eventSchema := "event/v1"
	var verdicts []lifecycleVerdictEntry
	// verdictBindings (spec 04, P4): one entry per id, each resolved
	// against THAT id's own acceptance_criteria[] — close's own ids ARE
	// the parents directly (no response indirection). verdictBindings[0]
	// is verdicts above, the canonical set operation.Close's ONE key is
	// derived from (byte-identical, for exactly one id, to the pre-P4
	// "resolve against ids[0]" behaviour); every OTHER id's own close
	// event authors from its OWN binding (US-3) once
	// lifecycleVerdictsBindUniformly has confirmed every id resolves the
	// SAME tokens to the SAME referent (T1/§11 Amendments: refused by
	// name, naming the disagreeing id, otherwise — see that function's own
	// doc comment for why this is forced rather than a strictness choice).
	var verdictBindings []lifecycleVerdictParentBinding
	if c.spec.SupportsVerdicts {
		verdictTokens, verr := lifecycleParseVerdicts(verdictFlags)
		if verr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, verr)
			return 2
		}
		eventSchema = lifecycleEventSchema(c.deps.manifest.MinBinaryVersion)
		if len(verdictTokens) > 0 && eventSchema != "event/v2" {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"%s: --verdict requires this space's min_binary_version to be at or above %s (event/v2); this space's floor is %q\n",
				c.spec.Verb, contract.ContractPublicationFloor, c.deps.manifest.MinBinaryVersion)
			return 1
		}
		if len(verdictTokens) > 0 {
			for idx, id := range ids {
				_, probe, rerr := lifecycleLoadEnvelope(c.deps.mirrorDir, id)
				if rerr != nil {
					_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, rerr)
					return 1
				}
				resolved, rerr := lifecycleResolveVerdicts(verdictTokens, probe.AcceptanceCriteria)
				if rerr != nil {
					// idx==0's message is BYTE IDENTICAL to pre-P4 (this
					// file's own non-negotiable: the single-target path does
					// not move) — every OTHER id additionally names itself,
					// since lifecycleResolveVerdicts' own error text does not
					// (B31 refusal standard).
					if idx == 0 {
						_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, rerr)
					} else {
						_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: batch cannot bind uniformly: %v\n", c.spec.Verb, id, rerr)
					}
					return 2
				}
				verdictBindings = append(verdictBindings, lifecycleVerdictParentBinding{
					TargetID: id, ParentID: id, Criteria: probe.AcceptanceCriteria, Verdicts: resolved,
				})
			}
			if len(verdictBindings) > 1 {
				if uerr := lifecycleVerdictsBindUniformly("verdict", verdictBindings); uerr != nil {
					_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, uerr)
					return 2
				}
			}
			// T1: echoed once per id, PREFIXED with the id (AC1/AC2), and
			// only once every id is confirmed to bind uniformly — a batch
			// that is about to be refused prints no echo at all.
			for _, b := range verdictBindings {
				_, _ = fmt.Fprintf(stdio.Stdout, "%s:\n", b.TargetID)
				lifecycleEchoVerdicts(stdio.Stdout, b.Verdicts, b.Criteria)
			}
			verdicts = verdictBindings[0].Verdicts
		}
	}
	// verdictsPtr: nil (omitted) below the floor or on a row that does not
	// support verdicts at all; non-nil (present, even when empty) at/above
	// the floor on the close row — see VerifyCommand.Run's own identical
	// comment on why an EMPTY array, not an absent key, is what the
	// schema's conditional requires. This is the CANONICAL (id[0]) pointer;
	// the batch loop below overrides it per id when verdictBindings is
	// populated.
	var verdictsPtr *[]lifecycleVerdictEntry
	if eventSchema == "event/v2" {
		verdictsPtr = &verdicts
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	// operationKey (B24, mirroring VerifyCommand.Run's own identical
	// comment): derived only when verdicts actually carry content. Below
	// the floor, on a row that does not support verdicts, or above the
	// floor with no --verdict at all, this verb keeps its EXISTING dedup
	// mechanism (branchID falls back to the batch's own ArtifactID,
	// funnel.go) — unchanged for every caller that never uses this flag.
	// operation.Close, not operation.Verify: see operation.Close's own doc
	// comment for why a standalone close mints its own key domain rather
	// than reusing verify's.
	var operationKey string
	if len(verdicts) > 0 {
		opVerdicts := make([]operation.VerdictEntry, len(verdicts))
		for i, v := range verdicts {
			opVerdicts[i] = operation.VerdictEntry{Index: v.resolvedIndex, Verdict: v.Verdict, CauseOwner: v.CauseOwner}
		}
		operationKey = operation.Close(c.deps.ownSystem, actor.Kind, actor.Name, ids, opVerdicts)
	}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, err)
		return 1
	}

	var files []space.FileWrite
	parsedRefs := lifecycleRefsFromFlag(*refs)
	for idx, id := range ids {
		evaluation, env, successor, err := lifecycleEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, id, fold.Event{
			Transition: c.spec.Transition, Actor: actor,
		}, parsedRefs)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			message := c.deps.refusalMessage(id, evaluation.Verdict)
			// D-3 (wave 2c): the ONE local gate that resolves real
			// SuccessorFacts (lifecycleEvaluateCandidate, above) learns the
			// SAME decision-supersede discrimination checkLifecycle already
			// applies — see decisionSupersedeRefusalMessage's own doc
			// comment for the exact coarseness this reuses.
			if evaluation.Verdict == fold.VerdictUnauthorizedActor && c.spec.Transition == fold.TSupersede && env.Kind == fold.KindDecision {
				message = decisionSupersedeRefusalMessage(id, successor != nil)
			}
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s\n", c.spec.Verb, message)
			return 1
		}
		eventRefs := parsedRefs
		if c.spec.Transition == fold.TSatisfy {
			eventRefs, err = lifecycleValidateSatisfyRefs(c.deps.mirrorDir, c.deps.manifest, id, parsedRefs)
			if err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
				return 1
			}
		}

		_, probe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, id)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
			return 1
		}
		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: cannot mint event id: %v\n", c.spec.Verb, err)
			return 1
		}
		// idVerdictsPtr (US-3): THIS id's own resolved verdicts — identical
		// to verdictsPtr (verdictBindings[0]) for id == ids[0], and for
		// every batch that reached this point (uniformity already
		// confirmed above), so this changes no id's WRITTEN bytes versus
		// the canonical set — only which already-equal binding authors it.
		idVerdictsPtr := verdictsPtr
		if eventSchema == "event/v2" && len(verdictBindings) > 0 {
			idVerdicts := verdictBindings[idx].Verdicts
			idVerdictsPtr = &idVerdicts
		}
		ev := lifecycleEventDoc{
			Schema: eventSchema, Event: eventID.String(), Space: probe.Space,
			Subject: id, Transition: c.spec.Transition, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			// Verdicts: nil (omitted) on every row except close, and nil
			// below the floor even on close — see this Run's own comment
			// above verdictsPtr's assignment.
			Verdicts: idVerdictsPtr,
		}
		if *reason != "" {
			ev.Note = *reason
		}
		if *reasonCode != "" {
			ev.ReasonCode = *reasonCode
		}
		if len(eventRefs) > 0 {
			ev.Refs = eventRefs
		}
		if *findings != "" {
			ev.Note = *findings
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: cannot encode event for %s: %v\n", c.spec.Verb, id, merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
	}

	req := c.deps.buildRequest(ids, files, c.spec.Verb, c.spec.GateMarker)
	req.OperationKey = operationKey
	return c.deps.submit(ctx, req, c.spec.Verb, ids, stdio)
}

var _ Command = (*LifecycleCommand)(nil)

// lifecycleRespondSeed builds `respond`'s own canonical, content-derived
// seed (HIGH-1 fix-wave finding): a fixed-order join of parentID, result,
// every respFields k=v pair — SORTED by key, since respFields is a map and
// Go's own map iteration order is randomized per-process; skipping the
// sort would make the seed (and therefore responseID) nondeterministic in
// production while a fixed-entropy unit test still passed, the exact trap
// this fix targets — the body override, and the actor's kind/name/system.
// Deliberately EXCLUDES `now` (see this file's own respond Run comment)
// and any random id.
//
// refs is P6/P4's `--ref` flag (wave 23, agent-exchange-2026-08 spec 06
// §11's 2026-08-10 "AC9 wire decision"), included in GIVEN order — never
// sorted, unlike respFields above. `refs[]` is a SEQUENCE on the wire and
// its written order is part of the document's own bytes (respDoc["refs"]
// below preserves the same order), so two invocations that name the same
// refs in a DIFFERENT order write two different documents and must mint
// two different ids; sorting would silently collapse them onto one. An
// identical retry (same refs, same order) still reproduces the identical
// seed, which is the only determinism this function's callers actually
// need (a retry with identical inputs must reproduce the identical
// response id, so the funnel's dedup branch finds it).
// unmet/standing/blockedBy (defects-fix-2026-08 P2) join the seed for the
// same reason refs did: they are content, not derived defaults, and a retry
// differing ONLY in one of these three must NOT collide with a prior
// response that named none of them — the exact class of gap this file's own
// HIGH-1 fix-wave finding exists to close. Written AFTER refs so an empty
// unmet[]/absent standing/absent blockedBy (every caller before this phase,
// and every caller today that doesn't use the three new flags) writes
// exactly the bytes lifecycleRespondSeed always wrote — no existing
// responseID changes.
func lifecycleRespondSeed(parentID, result string, respFields map[string]string, bodyOverride []byte, actor fold.Actor, refs []string, unmet []lifecycleUnmetEntry, standing string, blockedBy *lifecycleBlockedByEntry, delivers []string) []byte {
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
	for _, ref := range refs {
		buf.WriteString("ref=" + ref + "\n")
	}
	// The ordinal branch writes the EXACT byte format every pre-P3 caller
	// already hashed ("unmet=<n>\n") — an existing --unmet <index> call
	// must keep minting the same responseID it always did. The criterion
	// branch ("unmet=<id>\n") is new content no legacy caller could ever
	// have produced, so it cannot collide with anything already committed.
	for _, u := range unmet {
		switch {
		case u.Criterion != "":
			buf.WriteString("unmet=" + u.Criterion + "\n")
		case u.Index != nil:
			buf.WriteString("unmet=" + strconv.Itoa(*u.Index) + "\n")
		}
	}
	if standing != "" {
		buf.WriteString("standing=" + standing + "\n")
	}
	if blockedBy != nil {
		buf.WriteString("blocked_by=" + blockedBy.ReasonCode + ":" + blockedBy.Owner + ":" + blockedBy.Needs + "\n")
	}
	// delivers joins the seed for the reason refs did — it is CONTENT, in
	// GIVEN order (delivers[] is a sequence on the wire, and respDoc
	// preserves the same order), so two responses naming different packages
	// must not mint one id. Written LAST and only when non-empty, so every
	// caller that names none writes exactly the bytes this function always
	// wrote and no already-computed responseID moves.
	//
	// This is the DOCUMENT's identity. The OPERATION KEY carries `delivers`
	// too, as of `dbdd7257` — it did not when this comment was first
	// written, and the paragraph that said so is replaced rather than
	// amended because a comment describing a tree that has moved is the
	// defect this epic is named after.
	//
	// The two are separate facts and both are needed. The document id is
	// content-derived and decides whether these are two artifacts; the
	// operation key decides whether they are two BRANCHES. With only the
	// first, two responses on one parent differing only in --delivers met
	// the funnel's already-open short-circuit and the second read as a
	// retry of the first. key.go's own comment had already argued the
	// principle: a key minted without a distinguishing fact is a COLLIDING
	// key, not a narrower one.
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

// --- respond (scaffolds + submits an XS) ---------------------------------

// RespondCommand implements `a2a respond <parent-id...>`: scaffolds a new
// XS response artifact per parent (draft->submit collapsed, D-026) AND
// authors the parent's own `respond` event (linking via refs[0], see
// lifecycleEventDoc's own doc comment) — batch = N parents, one PR.
type RespondCommand struct {
	deps lifecycleDeps
}

// SetPendingMarker replaces the command's pending-write recorder.
func (c *RespondCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewRespondCommand constructs the respond command.
func NewRespondCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *RespondCommand {
	return &RespondCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// SetClockForTest overrides this command's injected clock (test-only DI
// seam, rails anti-pattern #10: production always uses the constructor's
// own time.Now default). HIGH-1 fix-wave finding: proving responseID's
// determinism across two calls needs a FIXED, reproducible `now` — a real
// wall-clock read would make the assertion flaky near a UTC-date boundary
// (MintExchangeIDAt embeds today's UTC date).
func (c *RespondCommand) SetClockForTest(now func() time.Time) {
	c.deps.now = now
}

// Name implements cli.Command.
func (c *RespondCommand) Name() string { return "respond" }

// Synopsis implements cli.Command.
func (c *RespondCommand) Synopsis() string {
	// NOT extended with --delivers, deliberately: this one-line synopsis is
	// the generated catalog (`a2a __catalog` -> skill/a2ahub/reference/
	// commands.md, diffed by scripts/ci-skill-drift.sh), skill/** is off
	// this phase's allowlist, and the line is already a summary rather than
	// a flag list — defects-fix-2026-08 P2 added --unmet/--standing/
	// --blocked-by without touching it either. The flag is reachable from
	// `a2a respond -h` like its three siblings.
	return "respond to one or more parents: respond --result <answered|delivered|partial|cannot> [--ref <artifact-id>]... <parent-id...>"
}

// Run implements cli.Command.
func (c *RespondCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("respond", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	var fieldFlags newStringList
	fs.Var(&fieldFlags, "field", "k=v field override (repeatable)")
	// --ref (wave 23, agent-exchange-2026-08 spec 06 §11's 2026-08-10 "AC9
	// wire decision"): `refs` is the schema's own envelope-base field and it
	// was unreachable from `respond` entirely — `--field` goes through the
	// append pass, which writes ONE scalar node and is refused for an
	// array-typed key (fill-classes.yaml's envelope/v1/base/origin row has
	// recorded this exact limitation all along). A general flag closes the
	// real gap; the submit-time refusal that reads it (internal/space's
	// checkSubmitResponseDeliveryPossession) keys on WHAT a ref resolves to,
	// not on which flag wrote it, so this is not a single-purpose
	// `--fulfilled-by` flag. Follows `a2a attach`'s own precedent for a
	// value `--field` structurally cannot express.
	var refFlags newStringList
	fs.Var(&refFlags, "ref", "artifact id to record in refs[] (repeatable; e.g. the fulfilling handoff)")
	// --delivers (judge-the-thing-2026-08 P1, spec 01 §T2): the response
	// NAMES the data package it announces. A separate flag from --ref for
	// the reason the field is separate from refs[] — refs[] is a general,
	// optional wire that carries anything, so an agent that omits it sails
	// through the very check built to stop it (which is why the shipped
	// possession check could not fire on fb-20260808-d5740f). `delivers`
	// means exactly one thing, and internal/space refuses a submit naming a
	// package that has not landed on origin/main (REF-024).
	var deliversFlags newStringList
	fs.Var(&deliversFlags, "delivers", "data package id (DP-...) this response announces as delivered (repeatable)")
	bodyFile := fs.String("body-file", "", "path to a file whose contents replace the response body")
	result := fs.String("result", "", "answered|delivered|partial|cannot (required)")
	// --unmet/--standing/--blocked-by (defects-fix-2026-08 P2, spec 02 §T1):
	// envelope/v2/response's own P6 incompleteness fields. `--field` cannot
	// author any of them — `unmet` and `blocked_by` are array/object-typed
	// (writes ONE scalar node, refused for either), and even `standing`,
	// though scalar, gets its own named flag rather than `--field
	// standing=...` for the same reason `--verdict` (VerifyCommand, below)
	// is its own flag and not `--field verdicts=...`: an author names the
	// concept, not a JSON-shaped string. `--ref` is the shipped precedent
	// for exactly this class of gap.
	var unmetFlags newStringList
	fs.Var(&unmetFlags, "unmet", "0-based index into the parent's acceptance_criteria[] this response did NOT satisfy, or its declared criterion id (e.g. ac1) when that array carries ids (repeatable)")
	standing := fs.String("standing", "", "authoritative|provisional|advisory — whether these values are binding (absent = undeclared, NOT authoritative, per P-1)")
	blockedByFlag := fs.String("blocked-by", "", "<reason_code>:<owner>:<needs> — what would unblock the unmet criteria (reason_code: split-required|security-concern|out-of-scope|duplicate|other; needs: bytes|judgement|decision)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	parents, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(parents) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a respond --result <answered|delivered|partial|cannot> <parent-id...>")
		return 2
	}
	switch *result {
	case "answered", "delivered", "partial", "cannot":
	default:
		_, _ = fmt.Fprintln(stdio.Stderr, "respond: --result must be one of answered|delivered|partial|cannot")
		return 2
	}
	fields, ferr := newParseFields(fieldFlags)
	if ferr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", ferr)
		return 2
	}
	refs := []string(refFlags)
	for _, r := range refs {
		if strings.TrimSpace(r) == "" {
			_, _ = fmt.Fprintln(stdio.Stderr, "respond: --ref must not be empty")
			return 2
		}
	}
	// Emptiness is the ONLY thing checked here, deliberately: the id's
	// grammar and its landing are ONE question with ONE authority
	// (space.ResolveDataPackage, reached through the funnel), and a second
	// DP- parser in the CLI is precisely the duplicated "is this package
	// here" this epic is named after. A malformed id therefore refuses at
	// submit, naming REF-024, on both surfaces.
	delivers := []string(deliversFlags)
	for _, d := range delivers {
		if strings.TrimSpace(d) == "" {
			_, _ = fmt.Fprintln(stdio.Stderr, "respond: --delivers must not be empty")
			return 2
		}
	}
	unmetTokens, uerr := lifecycleParseUnmet(unmetFlags)
	if uerr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", uerr)
		return 2
	}
	// unmetBindings (spec 04, P4): resolved against EVERY parent's own
	// acceptance_criteria[], not just parents[0] — the pre-P4 "one set
	// applies to the whole batch" simplification, made explicit and now
	// actually CHECKED (T1/§11 Amendments: operation.Respond takes ONE
	// unmet slice for the whole batch, the same structural constraint
	// lifecycleUnmetsBindUniformly's own doc comment explains for
	// verdicts). unmetBindings[0].Unmet is the canonical set opUnmet/the
	// response seed below fall back to when a parent-specific binding
	// isn't in play — byte-identical, for exactly one parent, to the pre-P4
	// "resolve against parents[0]" behaviour.
	var unmet []lifecycleUnmetEntry
	var unmetBindings []lifecycleUnmetParentBinding
	if len(unmetTokens) > 0 {
		for idx, parentID := range parents {
			_, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
			if err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
				return 1
			}
			resolved, rerr := lifecycleResolveUnmet(unmetTokens, parentProbe.AcceptanceCriteria)
			if rerr != nil {
				// idx==0's message is BYTE IDENTICAL to pre-P4 — see the
				// verify/close sites' own identical comment.
				if idx == 0 {
					_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", rerr)
				} else {
					_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: batch cannot bind uniformly: %v\n", parentID, rerr)
				}
				return 2
			}
			unmetBindings = append(unmetBindings, lifecycleUnmetParentBinding{
				TargetID: parentID, ParentID: parentID, Criteria: parentProbe.AcceptanceCriteria, Unmet: resolved,
			})
		}
		if len(unmetBindings) > 1 {
			if uerr := lifecycleUnmetsBindUniformly(unmetBindings); uerr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", uerr)
				return 2
			}
		}
		unmet = unmetBindings[0].Unmet
	}
	if *standing != "" && !lifecycleStandingEnum[*standing] {
		_, _ = fmt.Fprintln(stdio.Stderr, "respond: --standing must be one of authoritative|provisional|advisory")
		return 2
	}
	var blockedBy *lifecycleBlockedByEntry
	if *blockedByFlag != "" {
		parsed, berr := lifecycleParseBlockedBy(*blockedByFlag)
		if berr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", berr)
			return 2
		}
		blockedBy = &parsed
	}
	var bodyOverride []byte
	if *bodyFile != "" {
		b, err := c.deps.readFile(*bodyFile)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot read --body-file: %v\n", err)
			return 1
		}
		bodyOverride = b
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}
	// refs are part of what this operation IS, so they go to operation.Respond
	// as their own argument. A retry with an IDENTICAL --ref set must land on
	// the SAME key (dedup), and two calls differing ONLY in --ref must NOT
	// collide — otherwise the second write reads as a repeat of the first and
	// its different refs[] is never committed. They must also not be smuggled
	// through `fields`: template.Render's applyFills walks that map and
	// refuses any key it cannot place (ErrUnappliableField).
	//
	// CLOSED by the lead in the SAME commit this gap was reported (see
	// internal/operation/key.go's own comment for why the signature WIDENED
	// instead of gaining a second entry point): unmet/standing/blocked_by
	// now feed the key. A retry differing only in one of them mints a
	// different operationKey, so a corrected response is no longer treated
	// as a repeat of the first at the funnel's idempotency layer.
	//
	// The comment is corrected rather than deleted: it read as a live gap
	// for one wave while the code three lines below already closed it, and
	// the epic's own auditor found the drift — CLI and MCP are independent
	// readers by ADR-001, and internal/mcp's sibling comment described the
	// shipped behaviour correctly the whole time.
	// opUnmet: internal/operation/key.go's RespondIncompleteness.Unmet still
	// reads a plain []int (that file is lead-reserved, off this phase's
	// allowlist — this file's own pre-P3 comment two lines below already
	// flags unmet/standing/blocked_by as NOT feeding operation.Respond's key
	// precisely). resolvedIndex is the one stable identity both wire forms
	// share, so it is what goes here — unchanged for every pre-P3 caller
	// (Index IS resolvedIndex for the ordinal form) and the least-wrong
	// value available for the new id form, given the constraint.
	opUnmet := make([]int, len(unmet))
	for i, u := range unmet {
		opUnmet[i] = u.resolvedIndex
	}
	respondFacts := operation.RespondIncompleteness{Unmet: opUnmet, Standing: *standing}
	if blockedBy != nil {
		respondFacts.BlockedByReason = blockedBy.ReasonCode
		respondFacts.BlockedByOwner = blockedBy.Owner
		respondFacts.BlockedByNeeds = blockedBy.Needs
	}
	operationKey := operation.Respond(
		c.deps.ownSystem, actor.Kind, actor.Name, parents, *result, fields, refs, bodyOverride, respondFacts, delivers,
	)

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	var ids []string
	for idx, parentID := range parents {
		parentEnv, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
			return 1
		}

		// parentUnmet (US-3): THIS parent's own resolved unmet set — the
		// judgement of the parent's OWN acceptance_criteria[], not a set
		// resolved against some other parent in the batch and reused
		// byte-for-byte. Identical to unmet (unmetBindings[0]) for
		// parentID == parents[0], and for every batch that reached this
		// point (uniformity already confirmed above), so no existing
		// caller's seed/wire content changes.
		parentUnmet := unmet
		if len(unmetBindings) > 0 {
			parentUnmet = unmetBindings[idx].Unmet
		}

		respFields := map[string]string{}
		for k, v := range fields {
			respFields[k] = v
		}
		respFields["parent"] = parentID
		respFields["result"] = *result
		if _, has := respFields["from"]; !has {
			respFields["from"] = c.deps.ownSystem
		}

		// HIGH-1 fix-wave finding: responseID's random suffix is derived
		// from the response's OWN content (lifecycleRespondSeed — parentID,
		// result, every respFields k=v pair SORTED by key, the body
		// override, and the actor), never c.deps.entropy directly — a retry
		// with IDENTICAL inputs reproduces the IDENTICAL responseID, landing
		// on the funnel's SAME deterministic branch (dedup,
		// space.WriteStateAlreadyOpen) instead of authoring a duplicate
		// response artifact + PR. Deliberately NOT keyed on parentID alone:
		// the verify/dispute design allows multiple distinct responses from
		// the same system on the same parent (TestVerifyMultiResponseDoes
		// NotAutoClose), so a parentID-only branch would silently collapse
		// two genuinely different responses onto one branch. NOTE:
		// MintExchangeIDAt still embeds today's UTC date from `now`; a retry
		// crossing midnight still mints a different id (spec 08 §11
		// amendment — accepted, out of scope here).
		seed := lifecycleRespondSeed(parentID, *result, respFields, bodyOverride, actor, refs, parentUnmet, *standing, blockedBy, delivers)
		responseID, err := artifact.MintExchangeIDAt("XS", c.deps.ownSystem, now, bytes.NewReader(seed))
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot mint response id: %v\n", err)
			return 1
		}
		evaluation, _, _, err := lifecycleEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, parentID, fold.Event{
			Transition: fold.TRespond, ResponseID: responseID, Actor: actor,
		}, nil)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s\n", c.deps.refusalMessage(parentID, evaluation.Verdict))
			return 1
		}

		// Wave K fix, `space`/`title` half (found alongside the pinned
		// `to` fix while checking this exact "drafts AND writes in one
		// call" shape, per that fix's own instruction to check other
		// verbs for the same class of gap): response.md's `space:
		// <space-id>` and `title: <human/agent-scannable title, ...>`
		// are SCALAR placeholders respFields never carried keys for, so
		// template.Render's own applyFills left both completely unfilled
		// — the committed response would carry those LITERAL strings
		// forever. internal/validate's V2 pass rejects neither (space is
		// a free-form string field; title has no placeholder-literal
		// check outside SubmitCommand.Run, cmd_submit.go, a path
		// `respond` never goes through — it calls c.deps.submit
		// directly). `contract deprecate` (cmd_contract.go) already
		// fixes the identical `space`/`title` gap on its own
		// announcement draft the same way.
		//
		// Added HERE, deliberately AFTER lifecycleRespondSeed/
		// MintExchangeIDAt above rather than alongside `parent`/`result`/
		// `from`: both are DERIVED defaults (parentProbe.Space; a
		// parentID-based title text), not part of what a retry's
		// dedup-by-content check (this file's own HIGH-1 fix, doc'd
		// below) needs to distinguish two responses — folding them into
		// the seed would also silently change every already-computed
		// responseID's own hash input, which is exactly the kind of
		// seed-shape change HIGH-1 warns never to make casually. It
		// happens to also keep this verb's content-derived id
		// numerically identical to internal/mcp's own (unfixed) respond
		// path, which mints from the SAME parent/result/from-only seed —
		// not the goal, but a useful confirmation neither seed shape
		// silently drifted.
		respFields["space"] = parentProbe.Space
		if _, has := respFields["title"]; !has {
			respFields["title"] = fmt.Sprintf("Response to %s", parentID)
		}

		// spec 46 §T1 R2/R4/R5: a derived artifact inherits its SOURCE's
		// thread — the response is derived from parentID, so it inherits
		// parentProbe.Thread, set here beside space/title (a derived
		// default, added AFTER lifecycleRespondSeed/MintExchangeIDAt above
		// for the exact reason space/title are: folding it into the seed
		// would silently change every already-computed responseID, per
		// this file's own comment above). An explicit --field thread=<id>
		// that DIFFERS from the parent's thread is refused (exit 2 — bad
		// caller input, same class as the --result usage check above)
		// naming both values — never a silent precedence, never a guess
		// (R4).
		// `parentProbe.Thread != ""` matters and is not defensive noise: without
		// it, a threadless parent plus an explicit `--field thread=X` takes THIS
		// branch and prints "conflicts with parent's thread " — an empty value in
		// a message whose whole job is naming both sides — instead of falling
		// through to the threadless refusal below, which names the real condition
		// and the real fix. MCP's twin already carried the guard; the two
		// surfaces disagreed on the message and the exit code for identical
		// input, which is the divergence class ADR-001's deliberate duplication
		// is supposed to be watched for rather than assumed away.
		if explicit, has := respFields["thread"]; has && explicit != "" && parentProbe.Thread != "" && explicit != parentProbe.Thread {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: --field thread=%s conflicts with parent's thread %s\n", parentID, explicit, parentProbe.Thread)
			return 2
		}
		// A parent with NO thread is refused, loudly and by name. Since
		// `a2a new` always mints (R1), the only artifacts in this state are
		// ones committed before P46 — and spec 46 carries no legacy path by
		// operator decision (greenfield: the spaces are reseeded). The two
		// alternatives are both worse and both were observed while this
		// wave was built: propagating the empty value writes YAML `null`
		// and the reply dies later with an opaque SCH-006 type error, while
		// leaving the field unset lets the canonical template's own
		// placeholder text survive into a committed document. Refusing here
		// names the actual condition and the actual fix.
		if parentProbe.Thread == "" {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"respond: %s: the parent carries no thread, so this reply has no conversation to join.\n"+
					"That artifact predates thread propagation; reseed the space (see `a2a space init`) or reply to an artifact drafted by this version.\n",
				parentID)
			return 1
		}
		respFields["thread"] = parentProbe.Thread

		// EnvelopeSchema: "envelope/v2" (defects-fix-2026-08 P2, LAST step of
		// this phase's own load-bearing fix order): a response is always
		// generationV2Unconditional (template.go's generationTable), and this
		// call never went through RenderNew/selectGeneration at all — it
		// renders `response` directly, so flipping the table alone would not
		// have moved this surface. Same override mechanism
		// internal/space/work_preparer.go:322 already uses for the identical
		// reason (an Input-level Render call outside the RenderNew path).
		// Ordered LAST, after schemas/templates/v2/response.md and the three
		// flags above: doing this FIRST would render `a2a respond --result
		// partial` with no way to satisfy the v2 conditional, which is
		// TestRespondResultPartialGenerationOrderingGuard's own point.
		draft, err := template.Render(template.Input{
			Type: "response", EnvelopeSchema: "envelope/v2", ID: responseID, Actor: resolved, Created: now,
			Fields: respFields, Body: bodyOverride,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: render failed for %s: %v\n", parentID, err)
			return 1
		}

		// Wave K fix (live run 6, "a2a respond writes the response
		// template's to: placeholder verbatim"): response.md's own `to:
		// [<requester-system>]` is SEQUENCE-valued, and internal/template's
		// applyFills/setScalar only ever rewrites a scalar node (P18's
		// deliberately-deferred "Fix C (--field lists)", off-limits this
		// wave) — so the rendered draft still carries the literal
		// placeholder text, and the funnel's own V2 pass refuses it
		// (REF-006/CC-008: "`to` includes an unknown system:
		// <requester-system>"). A response answers the system that asked
		// (§3.4.3: "to: EXACTLY one entry"), and that requester is already
		// in hand as parentEnv.From from this loop's own legality check
		// above — never re-derived. Fixed the SAME way runPublish
		// (cmd_contract.go) already sets `version`: decode the rendered
		// frontmatter into a map, assign the ONE real field, re-encode via
		// artifact.SerializeFrontmatter. Never ad-hoc text surgery on a
		// structured document (rails), and never reopening applyFills for
		// sequence values (that is P18's call, not this wave's).
		respFm, err := artifact.ParseFrontmatter(draft)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: parse rendered response for %s: %v\n", parentID, err)
			return 1
		}
		var respDoc map[string]any
		if err := yaml.Unmarshal(respFm.YAML, &respDoc); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: decode rendered response for %s: %v\n", parentID, err)
			return 1
		}
		respDoc["to"] = []string{parentEnv.From}
		if len(refs) > 0 {
			// Same array-typed-key shape as `to` above, and for the same
			// reason: `refs` (envelope base, §5.2.2) is a SEQUENCE the
			// append pass cannot write, so it is set here by decode/assign/
			// re-encode rather than by respFields/applyFills. Written in
			// GIVEN order (lifecycleRespondSeed's own doc comment explains
			// why order is not sorted). response.md's template carries `refs`
			// only as a commented-out placeholder, so this is always a fresh
			// key, never an overwrite of something the template rendered.
			refEntries := make([]map[string]string, 0, len(refs))
			for _, ref := range refs {
				refEntries = append(refEntries, map[string]string{"ref": ref})
			}
			respDoc["refs"] = refEntries
		}
		// delivers (judge-the-thing-2026-08 P1): the same array-typed-key
		// shape as `refs` above, written only when the caller named a
		// package — an unconditional `delivers: []` would declare an
		// announcement nobody made, and its ABSENCE is the ordinary answer
		// shape the schema's own description turns on.
		if len(delivers) > 0 {
			respDoc["delivers"] = append([]string(nil), delivers...)
		}
		// unmet/standing/blocked_by (defects-fix-2026-08 P2): same
		// decode/assign/re-encode idiom as `to`/`refs` above — `unmet` is a
		// SEQUENCE and `blocked_by` a MAPPING, neither writable by
		// applyFills' scalar-only append pass, and response.md's template
		// carries all three only as commented-out placeholders (P-1:
		// absence stays distinct from a declared value), so these are
		// always fresh keys, never an overwrite of something the template
		// rendered. Written only when the caller actually gave the flag —
		// an unconditional `unmet: []`/`standing: authoritative` would
		// declare something nobody asked for.
		// P3 widening: unmet[] items are HOMOGENEOUS (schemas/envelope/v2/
		// response.schema.json's own array-level anyOf) — either every entry
		// carries a bare integer or every entry carries {criterion: <id>},
		// never mixed. lifecycleResolveUnmet already guarantees this by
		// construction (every token resolved against the SAME parent's SAME
		// idsDeclared flag), so which branch fires below is decided once,
		// from the first entry, not per-entry.
		if len(parentUnmet) > 0 {
			if parentUnmet[0].Criterion != "" {
				unmetSeq := make([]map[string]string, len(parentUnmet))
				for i, u := range parentUnmet {
					unmetSeq[i] = map[string]string{"criterion": u.Criterion}
				}
				respDoc["unmet"] = unmetSeq
			} else {
				unmetSeq := make([]int, len(parentUnmet))
				for i, u := range parentUnmet {
					unmetSeq[i] = *u.Index
				}
				respDoc["unmet"] = unmetSeq
			}
		}
		if *standing != "" {
			respDoc["standing"] = *standing
		}
		if blockedBy != nil {
			respDoc["blocked_by"] = map[string]string{
				"reason_code": blockedBy.ReasonCode,
				"owner":       blockedBy.Owner,
				"needs":       blockedBy.Needs,
			}
		}
		respYAML, err := yaml.Marshal(respDoc)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: encode rendered response for %s: %v\n", parentID, err)
			return 1
		}
		draft = artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: respYAML, Body: respFm.Body})

		files = append(files, space.FileWrite{Path: layout.Exchange(responseID), Content: draft})

		// NOTE (deviation, see this phase's report): §3.4.6 prose describes
		// a response's own "draft -> submit -> submitted" mini-lifecycle,
		// but internal/fold's Apply (fold.go, off-limits this phase — only
		// legality.go was granted) has no dispatch case for a response-
		// SUBJECT event outside verify/dispute; a literal second `submit`
		// event on the response would fall through to applyPrimaryScoped
		// keyed on the PARENT's own kind/state and be flagged illegal
		// (spurious noise), while contributing nothing — Result.Responses
		// is seeded to `submitted` entirely by the PARENT's own `respond`
		// event below (applyPrimaryScoped's TRespond handling), independent
		// of any response-owned event. This phase does not author that
		// second event; a future fold amendment could add the dispatch
		// case if a literal audit-trail event is later required.

		// Parent's own `respond` event, linking the new response via refs[0].
		respondEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot mint event id: %v\n", err)
			return 1
		}
		respondEvent := lifecycleEventDoc{
			Schema: "event/v1", Event: respondEventID.String(), Space: parentProbe.Space,
			Subject: parentID, Transition: fold.TRespond, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			Refs:  []lifecycleRefEntry{{Ref: responseID}},
		}
		respondRaw, merr := yaml.Marshal(respondEvent)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot encode respond event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), respondEventID.String()), Content: respondRaw})

		ids = append(ids, parentID, responseID)
	}

	req := c.deps.buildRequest(ids, files, "respond", false)
	req.OperationKey = operationKey
	return c.deps.submit(ctx, req, "respond", ids, stdio)
}

var _ Command = (*RespondCommand)(nil)

// --- verify (response-scoped, D-024 single-response convenience close) --

// VerifyCommand implements `a2a verify <response-id|parent-id>...
// [--refs <response-id>]`: verifies one or more responses; on a
// single-response exchange it ALSO emits `close` on the parent in the
// same PR (D-024 convenience) — with multiple responses, `close` stays a
// separate, deliberate act.
type VerifyCommand struct {
	deps lifecycleDeps
}

// SetPendingMarker replaces the command's pending-write recorder.
func (c *VerifyCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewVerifyCommand constructs the verify command.
func NewVerifyCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *VerifyCommand {
	return &VerifyCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *VerifyCommand) Name() string { return "verify" }

// Synopsis implements cli.Command.
func (c *VerifyCommand) Synopsis() string {
	return "verify one or more responses: verify <response-id|parent-id...> [--refs <response-id>] [--verdict <index-or-criterion-id>:<met|unmet|not_warranted|not_exercised>:<cause_owner>]..."
}

// Run implements cli.Command.
func (c *VerifyCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	refs := fs.String("refs", "", "response id (disambiguates a multi-response parent)")
	// --verdict (P6 wave C, agent-exchange-2026-08 spec 06 §11's 2026-08-10
	// "wave C" amendment, threat-model.md T5): `verdicts[]` is an ARRAY the
	// generic `--field k=v` scalar-append pass structurally cannot express —
	// the same limitation `--ref` (this file's respond, above) and `a2a
	// attach` both already carry a dedicated flag for, rather than smuggling
	// it through `--field`. Repeatable (newStringList, cmd_new.go): one
	// judged criterion per occurrence.
	var verdictFlags newStringList
	fs.Var(&verdictFlags, "verdict", "<index-or-criterion-id>:verdict:cause_owner verdict entry — index is 0-based into the parent's acceptance_criteria[], or its declared criterion id (e.g. ac1) when that array carries ids (repeatable; verdict is met|unmet|not_warranted|not_exercised)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	targets, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a verify <response-id|parent-id...> [--refs <response-id>] [--verdict <index>:<verdict>:<cause_owner>]...")
		return 2
	}
	verdictTokens, verr := lifecycleParseVerdicts(verdictFlags)
	if verr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", verr)
		return 2
	}

	// eventSchema is floor-gated the SAME way internal/contract/
	// publication_plan.go gates envelope/v2 contract publication — see
	// lifecycleEventSchema's own doc comment. `verdicts[]` only exists on
	// event/v2, so a space below the floor cannot honour --verdict at all;
	// refusing here (rather than silently dropping the caller's judgements)
	// names the real condition, the same discipline this file's other
	// floor-gated refusal (publication_plan.go's own message) already
	// follows.
	eventSchema := lifecycleEventSchema(c.deps.manifest.MinBinaryVersion)
	if len(verdictTokens) > 0 && eventSchema != "event/v2" {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"verify: --verdict requires this space's min_binary_version to be at or above %s (event/v2); this space's floor is %q\n",
			contract.ContractPublicationFloor, c.deps.manifest.MinBinaryVersion)
		return 1
	}

	// verdictBindings (spec 04, P4): one entry per target, each resolved
	// against THAT target's own parent's acceptance_criteria[] — not just
	// targets[0]'s. verdictBindings[0] is verdicts below, the canonical set
	// operation.Verify's ONE key is derived from (byte-identical, for
	// exactly one target, to the pre-P4 "resolve against targets[0]"
	// behaviour); every OTHER target's own verify (and D-024 close) event
	// authors from its OWN binding (US-3) once
	// lifecycleVerdictsBindUniformly has confirmed every target resolves
	// the SAME tokens to the SAME referent (T1/§11 Amendments: refused by
	// name, naming the disagreeing target, otherwise). T1: printed BEFORE
	// anything is minted (AC1/AC2), for both parent shapes, once per
	// target — and only once every target is confirmed to bind uniformly;
	// a batch about to be refused prints no echo at all.
	var verdicts []lifecycleVerdictEntry
	var verdictBindings []lifecycleVerdictParentBinding
	if len(verdictTokens) > 0 {
		for idx, target := range targets {
			responseID, rerr := lifecycleResolveResponseID(c.deps.mirrorDir, c.deps.manifest, target, *refs)
			if rerr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", target, rerr)
				return 1
			}
			_, responseProbe, rerr := lifecycleLoadEnvelope(c.deps.mirrorDir, responseID)
			if rerr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", responseID, rerr)
				return 1
			}
			_, parentProbe, rerr := lifecycleLoadEnvelope(c.deps.mirrorDir, responseProbe.Parent)
			if rerr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", responseProbe.Parent, rerr)
				return 1
			}
			resolved, rerr := lifecycleResolveVerdicts(verdictTokens, parentProbe.AcceptanceCriteria)
			if rerr != nil {
				// idx==0's message is BYTE IDENTICAL to pre-P4 — see the
				// close site's own identical comment.
				if idx == 0 {
					_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", rerr)
				} else {
					_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: batch cannot bind uniformly: %v\n", target, rerr)
				}
				return 2
			}
			verdictBindings = append(verdictBindings, lifecycleVerdictParentBinding{
				TargetID: target, ParentID: responseProbe.Parent, Criteria: parentProbe.AcceptanceCriteria, Verdicts: resolved,
			})
		}
		if len(verdictBindings) > 1 {
			if uerr := lifecycleVerdictsBindUniformly("verdict", verdictBindings); uerr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", uerr)
				return 2
			}
		}
		for _, b := range verdictBindings {
			_, _ = fmt.Fprintf(stdio.Stdout, "%s:\n", b.TargetID)
			lifecycleEchoVerdicts(stdio.Stdout, b.Verdicts, b.Criteria)
		}
		verdicts = verdictBindings[0].Verdicts
	}
	// verdictsPtr is nil (omitted) below the floor and non-nil (present, even
	// when empty) at/above it — schemas/event/v2/event.schema.json's own
	// conditional REQUIRES the key on verify/close regardless of whether the
	// caller supplied any --verdict entries, and its description is explicit
	// that a parent with no acceptance_criteria[] at all "must stay
	// expressible with an empty array" rather than an absent key. This is the
	// CANONICAL (targets[0]) pointer; the batch loop below overrides it per
	// target when verdictBindings is populated.
	var verdictsPtr *[]lifecycleVerdictEntry
	if eventSchema == "event/v2" {
		verdictsPtr = &verdicts
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	// operationKey (P6 wave C determinism requirement): only derived when
	// --verdict actually carries content. Below the floor, or above it with
	// no --verdict at all, verify/close keep their EXISTING dedup mechanism
	// (branchID falls back to the batch's own ArtifactID, funnel.go) —
	// unchanged for every caller that does not use this new flag, so this
	// wave does not silently rename the branch every prior verify/close
	// invocation already relies on. Once verdicts are supplied, though, two
	// invocations naming the SAME targets with DIFFERENT judgements must NOT
	// collide onto that same content-independent branch (operation.Verify's
	// own doc comment).
	var operationKey string
	if len(verdicts) > 0 {
		opVerdicts := make([]operation.VerdictEntry, len(verdicts))
		for i, v := range verdicts {
			opVerdicts[i] = operation.VerdictEntry{Index: v.resolvedIndex, Verdict: v.Verdict, CauseOwner: v.CauseOwner}
		}
		operationKey = operation.Verify(c.deps.ownSystem, actor.Kind, actor.Name, targets, opVerdicts)
	}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	var ids []string
	for idx, target := range targets {
		responseID, err := lifecycleResolveResponseID(c.deps.mirrorDir, c.deps.manifest, target, *refs)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", target, err)
			return 1
		}

		evaluation, parentEnv, parentID, result, err := lifecycleEvaluateResponseCandidate(c.deps.mirrorDir, c.deps.manifest, responseID, fold.Event{
			Transition: fold.TVerify, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", responseID, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s\n", c.deps.refusalMessage(responseID, evaluation.Verdict))
			return 1
		}
		_, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", parentID, err)
			return 1
		}

		// targetVerdictsPtr (US-3): THIS target's own resolved verdicts —
		// identical to verdictsPtr (verdictBindings[0]) for target ==
		// targets[0], and for every batch that reached this point
		// (uniformity already confirmed above), so this changes no
		// target's WRITTEN bytes versus the canonical set — only which
		// already-equal binding authors it. Shared by the paired D-024
		// close event below (the SAME verification act).
		targetVerdictsPtr := verdictsPtr
		if eventSchema == "event/v2" && len(verdictBindings) > 0 {
			targetVerdicts := verdictBindings[idx].Verdicts
			targetVerdictsPtr = &targetVerdicts
		}

		verifyEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot mint event id: %v\n", err)
			return 1
		}
		verifyEvent := lifecycleEventDoc{
			Schema: eventSchema, Event: verifyEventID.String(), Space: parentProbe.Space,
			Subject: responseID, Transition: fold.TVerify, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			// Verdicts (P6 wave C, T5's discharge): present, even empty, on
			// EVERY event/v2 verify — schemas/event/v2/event.schema.json's
			// conditional requires the key regardless of whether this
			// invocation named any --verdict entries.
			Verdicts: targetVerdictsPtr,
		}
		verifyRaw, merr := yaml.Marshal(verifyEvent)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot encode event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), verifyEventID.String()), Content: verifyRaw})
		ids = append(ids, responseID)

		// D-024 convenience: single-response exchange also closes the
		// parent in the SAME PR. len(result.Responses) counts every
		// response tracked so far (this one included, already legal).
		if len(result.Responses) == 1 {
			closeEvaluation := fold.EvaluateCandidate(parentEnv.Kind, evaluation.Result, fold.Event{
				Subject: parentID, Transition: fold.TClose, Actor: actor,
			}, parentEnv, lifecycleMembership(c.deps.manifest))
			if closeEvaluation.Verdict != fold.VerdictLegal {
				// Not this phase's business to force a close that isn't
				// legal (e.g. an already-superseded parent) — verify still
				// stands on its own merit; only the convenience is skipped.
				continue
			}
			closeEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
			if err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot mint event id: %v\n", err)
				return 1
			}
			closeEvent := lifecycleEventDoc{
				Schema: eventSchema, Event: closeEventID.String(), Space: parentProbe.Space,
				Subject: parentID, Transition: fold.TClose, State: lifecycleReceiptState(closeEvaluation),
				Actor: eventActorFrom(resolved, actor.System),
				At:    now.UTC().Format(time.RFC3339),
				// Same verdicts as the paired verify above (this file's own
				// D-024 comment: it is the SAME verification act) — the
				// verifier's judgement of the parent's acceptance criteria
				// does not change because the convenience close rides in the
				// same PR.
				Verdicts: targetVerdictsPtr,
			}
			closeRaw, merr := yaml.Marshal(closeEvent)
			if merr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot encode close event: %v\n", merr)
				return 1
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), closeEventID.String()), Content: closeRaw})
			ids = append(ids, parentID)
		}
	}

	req := c.deps.buildRequest(ids, files, "verify", false)
	req.OperationKey = operationKey
	return c.deps.submit(ctx, req, "verify", ids, stdio)
}

var _ Command = (*VerifyCommand)(nil)

// lifecycleResolveResponseID resolves verify's own `<response-id|parent-
// id>` ambiguity (spec 08 T1): a bare XS- id is used directly; anything
// else is treated as a parent id, whose single open response is looked up
// (refsFlag disambiguates when the parent has more than one).
func lifecycleResolveResponseID(mirrorDir string, _ space.Manifest, target, refsFlag string) (string, error) {
	parsed, err := artifact.ParseID(target)
	if err == nil && parsed.Prefix == "XS" {
		return target, nil
	}
	if refsFlag != "" {
		return refsFlag, nil
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return "", err
	}
	var candidates []string
	seen := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == target && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			rid := ev.Refs[0].Ref
			if !seen[rid] {
				seen[rid] = true
				candidates = append(candidates, rid)
			}
		}
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("cli: %s has no attached response", target)
	case 1:
		return candidates[0], nil
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("cli: %s has multiple responses (%s) — disambiguate with --refs", target, strings.Join(candidates, ", "))
	}
}

// --- dispute (response-scoped) -------------------------------------------

// DisputeCommand implements `a2a dispute <response-id> --reason <text>
// [--reason-code <enum>]`: folds the response to `disputed`; the parent's
// responded->in_progress reopening is fold's OWN side effect
// (applyResponseScoped), never a second authored event.
type DisputeCommand struct {
	deps lifecycleDeps
}

// SetPendingMarker replaces the command's pending-write recorder.
func (c *DisputeCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewDisputeCommand constructs the dispute command.
func NewDisputeCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *DisputeCommand {
	return &DisputeCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *DisputeCommand) Name() string { return "dispute" }

// Synopsis implements cli.Command.
func (c *DisputeCommand) Synopsis() string {
	return "dispute a response: dispute --reason <text> [--reason-code <enum>] <response-id>"
}

// Run implements cli.Command.
func (c *DisputeCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("dispute", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	reason := fs.String("reason", "", "reason text (required)")
	reasonCode := fs.String("reason-code", "", "machine-readable reason code")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	ids, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(ids) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a dispute --reason <text> <response-id>")
		return 2
	}
	if *reason == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a dispute --reason <text> <response-id>")
		return 2
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	for _, responseID := range ids {
		evaluation, _, parentID, _, err := lifecycleEvaluateResponseCandidate(c.deps.mirrorDir, c.deps.manifest, responseID, fold.Event{
			Transition: fold.TDispute, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s: %v\n", responseID, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s\n", c.deps.refusalMessage(responseID, evaluation.Verdict))
			return 1
		}
		_, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s: %v\n", parentID, err)
			return 1
		}

		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: cannot mint event id: %v\n", err)
			return 1
		}
		ev := lifecycleEventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: parentProbe.Space,
			Subject: responseID, Transition: fold.TDispute, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			Note:  *reason, ReasonCode: *reasonCode,
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: cannot encode event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
	}

	req := c.deps.buildRequest(ids, files, "dispute", false)
	return c.deps.submit(ctx, req, "dispute", ids, stdio)
}

var _ Command = (*DisputeCommand)(nil)

// --- note (transition-free, D-025) ---------------------------------------

// NoteCommand implements `a2a note <id...> --note <text>`: a transition-
// free annotation (D-025), legal regardless of folded state. The shared
// fold predicate still checks authorization for either party before the
// funnel, matching the space's V3 required check.
type NoteCommand struct {
	deps lifecycleDeps
}

// SetPendingMarker replaces the command's pending-write recorder.
func (c *NoteCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewNoteCommand constructs the note command.
func NewNoteCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *NoteCommand {
	return &NoteCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *NoteCommand) Name() string { return "note" }

// Synopsis implements cli.Command.
func (c *NoteCommand) Synopsis() string {
	return "annotate one or more artifacts: note --note <text> <id...>"
}

// Run implements cli.Command.
func (c *NoteCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("note", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	noteText := fs.String("note", "", "annotation text (required)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	ids, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(ids) == 0 || *noteText == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a note --note <text> <id...>")
		return 2
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "note: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "note: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	for _, id := range ids {
		evaluation, _, _, err := lifecycleEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, id, fold.Event{
			Transition: fold.TNote, Actor: actor,
		}, nil)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: %s: %v\n", id, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", c.deps.refusalMessage(id, evaluation.Verdict))
			return 1
		}

		_, probe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, id)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: %s: %v\n", id, err)
			return 1
		}
		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: cannot mint event id: %v\n", err)
			return 1
		}
		ev := lifecycleEventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: id, Transition: fold.TNote, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
			Note:  *noteText,
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: cannot encode event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
	}

	req := c.deps.buildRequest(ids, files, "note", false)
	return c.deps.submit(ctx, req, "note", ids, stdio)
}

var _ Command = (*NoteCommand)(nil)
