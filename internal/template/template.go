package template

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

// Actor is this package's own minimal actor projection for filling a
// rendered draft's `actor:` block — deliberately not validate.Actor or
// fold.Actor (each layer owns its own minimal consumer-side view of the
// same domain concept; the established idiom throughout this repo, see
// e.g. internal/validate/seam.go's own Actor doc comment).
type Actor struct {
	Kind  string // "human" | "agent"
	Name  string
	Model string // optional; omitted from the rendered draft when empty
	// Session identifies the acting agent's own session, so several events
	// correlate back to one conversation.
	//
	// It is DETECTED (schemas/fill-classes.yaml), and until P3 it was
	// detected and then thrown away: agentid.Agent carried it, this struct
	// had no field for it, fillActor had no case for it, and every event
	// writer's actor struct lacked it too. The schemas allow it on every
	// envelope and event, and internal/validate's POL-016 bound-checks it —
	// against nothing, because no first-party writer could produce it.
	//
	// The one place a session id did get written is the v2 work-checkpoint
	// path, which mints its own rather than consulting detection. That is
	// the shape of the defect: the value existed, twice, and neither copy
	// was the detected one.
	Session string
	// KindClaimed says whether any SOURCE named the kind, as opposed to the
	// resolver falling back to "agent". The two are different facts and
	// collapsing them cost a real defect: fillActor overwrote
	// schemas/templates/v1/decision.md's own `actor: {kind: human, ...}` on
	// every render, contradicting the template's documented intent that a
	// decision typically carries a human actor because approving one is a
	// G3 gate.
	//
	// A flag rather than an empty Kind, because ResolveActor's "agent"
	// default is shipped, documented and test-pinned behaviour — the
	// missing information was never the value, it was whether anyone chose
	// it.
	KindClaimed bool
}

// Input carries every value Render needs that must come from the caller
// for testability: the minted ID, the resolved actor, and "now" are never
// computed inside this package (rails: "never time.Now() inside render").
type Input struct {
	// Type is one of the 8 §3.1 envelope types (schema.EnvelopeTypes()).
	Type string
	// EnvelopeSchema selects the canonical template generation. Empty keeps
	// the historical envelope/v1 default; envelope/v2 selects the concrete
	// v2 template for types that have shipped one.
	EnvelopeSchema string
	// ID is the already-minted artifact id, used verbatim — this package
	// never mints an ID itself (internal/artifact does, at the cmd_new.go
	// call site, per §3.3).
	ID string
	// Actor is the resolved actor (§7.4 order) to fill into the draft.
	Actor Actor
	// Created is the caller-resolved "now", rendered as RFC3339 UTC.
	Created time.Time
	// Fields carries --field k=v overrides, keyed by the top-level
	// frontmatter field name they replace. A present key always wins over
	// whatever placeholder/default the canonical template would otherwise
	// leave in place.
	Fields map[string]string
	// Body, when non-nil, replaces the canonical template's own
	// placeholder body verbatim (the `a2a new --body-file <path>` path,
	// wired at the cmd_new.go call site). A nil Body leaves the template's
	// own body section untouched.
	Body []byte
	// AcceptanceCriteria carries `a2a new --acceptance-criterion` values
	// (one per occurrence, author-typed text unchanged, in the order
	// given). A nil/empty slice means the flag was not given at all —
	// acceptance_criteria is left exactly as the template already has it
	// (its own placeholder, or absent entirely for a type whose template
	// carries no such key), never rewritten to an empty array.
	//
	// The SHAPE this renders is decided by EnvelopeSchema (already
	// resolved through selectGeneration by the time applyFills runs, on
	// both of RenderNew's passes): envelope/v2 mints the id-bearing
	// `{id, text}` form with ids `ac1..acN` in the order given; every
	// other generation (v1, or the sniff pass) renders bare strings and no
	// ids — envelope/v1's own published `items: {type: string}` shape,
	// immutable in the ratchet.
	//
	// This is a dedicated field rather than routed through Fields[
	// "acceptance_criteria"] deliberately: Fields is a map[string]string,
	// and the id-bearing form needs actual structure, not a hand-escaped
	// YAML string a caller would have to build correctly. It also survives
	// RenderNew's sniff-fallback path (`sniff.Fields = nil` on
	// ErrUnappliableField) untouched, since that only clears Fields, never
	// this field — the general mechanism's array/object append refusal
	// (schemaAllowsField) never applies here either, because this package
	// builds the exact typed node itself instead of asking a caller-typed
	// string to parse into one.
	//
	// Neither AUTHOR fill-class rule is bent by this: the criterion TEXT
	// is exactly what the author supplied, unchanged; the id is a
	// tool-minted HANDLE over that text, the same precedent `a2a attach`
	// sets for ref/digest over attached bytes.
	AcceptanceCriteria []string
}

// Types returns the 8 canonical envelope type names this package has an
// embedded template for, in schema.EnvelopeTypes()'s own stable order.
func Types() []string {
	return schema.EnvelopeTypes()
}

// Show returns typ's canonical embedded template's raw bytes, UNRENDERED —
// read-only inspection of the same template Render fills for the historical
// envelope/v1 generation.
//
// This is NOT what `a2a template show <type>` prints; ShowGeneration is. See
// that function's doc comment for why the distinction cost a merged, forever
// unpublishable contract.
func Show(typ string) ([]byte, error) {
	const op = "Show"
	raw, err := rawTemplate(typ, "")
	if err != nil {
		return nil, &Error{Op: op, Input: typ, Err: err}
	}
	return raw, nil
}

// AuthoringEnvelopeSchema reports the envelope generation FRESH authoring
// renders for typ — the same selection RenderNew makes (both call
// selectGeneration, the one seat below), resolved off the template's OWN
// default `schema_format` rather than a rendered draft's overrides, because
// a caller inspecting a template has no draft yet.
//
// The selection is table-driven (selectGeneration's generationTable), never
// derived from "does typ have a shipped templates/v2/<type>.md" — that
// table's own doc comment names the falsifying case (`announcement`).
// `contract` is the one type where the choice is a genuine sniff, because an
// openapi/proto3 contract has no publishable v2 shape at all;
// `work_request` selects envelope/v2 unconditionally, per
// specs/04-possession.md §11's 2026-08-10 amendment — its attachments[] and
// binding are both optional, so a v2 draft is a strict superset of v1 and
// there is no bifurcation to sniff. Before this wave, RenderNew and this
// function answered the same question differently (RenderNew branched only
// on `in.Type != "contract"`, this function additionally admitted
// `work_request` into the sniff branch); that disagreement was the
// 2026-08-06 incident recorded on ShowGeneration's own doc comment. A single
// selector removes the possibility of the two disagreeing again.
func AuthoringEnvelopeSchema(typ string, isJSONSchema func(string) bool) (string, error) {
	const op = "AuthoringEnvelopeSchema"
	raw, err := rawTemplate(typ, "")
	if err != nil {
		return "", &Error{Op: op, Input: typ, Err: err}
	}
	generation, gerr := selectGeneration(typ, func() (string, error) {
		return ContractDraftSchemaFormat(raw)
	}, isJSONSchema)
	if gerr != nil {
		return "", &Error{Op: op, Input: typ, Err: gerr}
	}
	return generation, nil
}

// generationSelection is selectGeneration's own vocabulary for how a type's
// fresh-authoring generation is decided — never a bool, because there are
// three real answers (always v1, always v2, or "ask the schema_format"), not
// two.
type generationSelection int

const (
	generationV1 generationSelection = iota
	generationV2Unconditional
	generationContractSniff
)

// generationTable is THE explicit, per-type answer to "what envelope
// generation does FRESH authoring render", decided in specs/04-possession.md
// §11 (2026-08-10 amendment, corrected the same day before the wave). It is
// a table, not a derivation, because the tempting derived rule — "select
// envelope/v2 whenever templates/v2/<type>.md exists" — is FALSIFIED by
// `announcement`: it HAS schemas/templates/v2/announcement.md, but that file
// is a work-checkpoint template (hardcoded `category: status`, a required
// `work:` block, its own internal/workcheckpoint validator and
// scripts/check_work_checkpoint_schema.sh), not a general v2 announcement —
// selecting v2 for it here would render a work checkpoint on every fresh
// `a2a new announcement`. `announcement` therefore carries an explicit
// generationV1 row below, with its reason, filed as epic-backlog B20, rather
// than being left to fall through a default and read as an oversight.
//
// `contract` is the sole generationContractSniff row: an openapi/proto3
// contract has no publishable v2 shape at all, so the choice is real there.
// Every other type — including `work_request`, which is
// generationV2Unconditional because attachments[] and binding are both
// optional and a v2 draft is a strict superset of v1 — is a fixed answer,
// not a sniff.
var generationTable = map[string]generationSelection{
	"contract":     generationContractSniff,
	"requirement":  generationV1,
	"question":     generationV1,
	"work_request": generationV2Unconditional,
	"decision":     generationV1,
	// response: generationV2Unconditional since defects-fix-2026-08 P2
	// (spec 02-a-response-can-say-what-it-failed.md). schemas/templates/
	// v2/response.md ships and `a2a respond`'s own authoring flags
	// (--unmet/--standing/--blocked-by) landed BEFORE this row moved — the
	// spec's own "fix order is load-bearing" section: envelope/v2/
	// response's `if result: partial|cannot` conditional requires either
	// (unmet AND blocked_by) or a non-authoritative standing, so flipping
	// this row before the authoring surface existed would have made
	// `a2a respond --result partial` render a draft that cannot validate.
	// TestRespondResultPartialGenerationOrderingGuard (internal/cli) is the
	// gate that proves the order, not merely asserts it.
	"response":     generationV2Unconditional,
	"handoff":      generationV1,
	"announcement": generationV1, // B20: templates/v2/announcement.md is a work checkpoint, not a general v2 template — see generationTable's own doc comment.
}

// selectGeneration is the ONE seat that answers "what envelope generation
// does fresh authoring render for typ" — RenderNew (the `a2a new` path) and
// AuthoringEnvelopeSchema (the `a2a template list`/`show` reporting path)
// both call it, so the two surfaces cannot answer the same question
// differently again. sniffFormat is called lazily and only for the
// generationContractSniff branch, so a type whose row does not need it never
// pays for the sniff.
func selectGeneration(typ string, sniffFormat func() (string, error), isJSONSchema func(string) bool) (string, error) {
	switch generationTable[typ] {
	case generationV2Unconditional:
		return "envelope/v2", nil
	case generationContractSniff:
		if isJSONSchema == nil {
			return "envelope/v1", nil
		}
		format, err := sniffFormat()
		if err != nil {
			return "", err
		}
		if !isJSONSchema(format) {
			return "envelope/v1", nil
		}
		return "envelope/v2", nil
	default:
		return "envelope/v1", nil
	}
}

// ShowGeneration returns typ's canonical embedded template for one explicit
// envelope generation, UNRENDERED. An empty generation means "whatever fresh
// authoring would render" (AuthoringEnvelopeSchema), which is what
// `a2a template show <type>` prints.
//
// Found on 2026-08-06 by an external report: `a2a template show contract`
// returned the envelope/v1 template unconditionally while `a2a new contract`
// rendered envelope/v2 for the very same default schema_format. An author who
// hand-wrote a contract from the SHOWN template got a descriptor with no
// top-level `artifacts:` inventory, which validated, submitted, passed the
// space's own CI and merged — and was then refused at
// `a2a contract preflight` with "authoring floor requires declared-v2
// candidate inventory", a shape no shipped surface offered. The inspection
// verb must show the generation the authoring verb writes, or it is not
// inspecting the same product.
func ShowGeneration(typ, generation string, isJSONSchema func(string) bool) ([]byte, error) {
	const op = "ShowGeneration"
	if generation == "" {
		resolved, err := AuthoringEnvelopeSchema(typ, isJSONSchema)
		if err != nil {
			return nil, err
		}
		generation = resolved
	}
	raw, err := rawTemplate(typ, generation)
	if err != nil {
		return nil, &Error{Op: op, Input: typ, Err: err}
	}
	return raw, nil
}

// Render fills typ's canonical embedded template with in's minted ID,
// resolved actor, and current date, and applies any --field overrides —
// then returns the complete draft bytes (frontmatter + body), otherwise
// byte-identical to the canonical template. Every other field the template
// already carries an already-schema-valid literal default for (priority,
// blocking, classification, ...) is left untouched, which is what makes
// AC-401.1 ("V1 pass on placeholder-only fills") hold without this package
// needing per-type domain knowledge — except the enum-alternatives field
// below, which deliberately gets no such default and is what a fresh draft
// of six of the eight types now requires an explicit choice for.
//
// An unreplaced enum-alternatives placeholder (e.g.
// "<clarification|defect|choice>") is deliberately NOT filled with its
// first alternative here (agent-exchange-2026-08 epic-backlog B3, spec
// 03-fill-classes.md §8 AC4): the agent chose nothing, and a rendered
// draft must not claim it did by picking on the author's behalf. The
// token survives to the rendered draft and is refused twice: at V1
// (bare `a2a validate`) by the ordinary JSON-Schema enum keyword,
// SCH-002 — the placeholder is not a member of the enum, so V1 is no
// longer silent on this field — and again at V2 (POL-010's
// `^<.*>$` placeholder check, at `a2a submit` / `a2a validate --ci`),
// which still has its own job: catching an unfilled PROSE placeholder
// (e.g. `expected_response.shape`) that no JSON-Schema enum, and so no
// V1 check, can ever see. Do not restore a default here "as a
// kindness"; that is precisely the fabrication this wave removed.
func Render(in Input) ([]byte, error) {
	const op = "Render"
	raw, err := rawTemplate(in.Type, in.EnvelopeSchema)
	if err != nil {
		return nil, &Error{Op: op, Input: in.Type, Err: err}
	}

	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, &Error{Op: op, Input: in.Type, Err: err}
	}

	var doc yaml.Node
	if uerr := yaml.Unmarshal(fm.YAML, &doc); uerr != nil {
		return nil, &Error{Op: op, Input: in.Type, Err: uerr}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, &Error{Op: op, Input: in.Type, Err: ErrMalformedTemplate}
	}

	if ferr := applyFills(doc.Content[0], in); ferr != nil {
		return nil, &Error{Op: op, Input: in.Type, Err: ferr}
	}
	if in.Type == "contract" && in.EnvelopeSchema == "envelope/v2" {
		parsed, parseErr := artifact.ParseID(in.ID)
		if parseErr != nil || parsed.Class != artifact.ClassStanding || parsed.Prefix != "XC" {
			return nil, &Error{Op: op, Input: in.Type, Err: ErrMalformedTemplate}
		}
		replaceScalarToken(doc.Content[0], "<slug>", parsed.Slug)
	}

	out, merr := yaml.Marshal(&doc)
	if merr != nil {
		return nil, &Error{Op: op, Input: in.Type, Err: merr}
	}

	body := fm.Body
	if in.Body != nil {
		body = in.Body
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: out, Body: body}), nil
}

// RenderNew centralizes fresh-authoring generation selection through
// selectGeneration — the one seat AuthoringEnvelopeSchema also answers from.
// A JSON-Schema contract is first rendered through the historical template
// so the actual schema_format override is known, then re-rendered as
// declared-v2 only if selectGeneration says so; every other type's answer is
// fixed and needs no re-render. The caller supplies the canonical dialect
// classifier, keeping template independent of validate while CLI and MCP
// still make one identical decision.
func RenderNew(in Input, isJSONSchema func(string) bool) ([]byte, error) {
	in.EnvelopeSchema = ""
	var unappliableAtV1 error
	draft, err := Render(in)
	if err != nil {
		// The first pass exists ONLY to read the draft's own schema_format so
		// selectGeneration can sniff it. A caller field that cannot be applied
		// at v1 must not fail the whole render here, because the generation
		// this is about to choose may be exactly the one that HAS the field —
		// and for `work_request` it always is.
		//
		// Found 2026-08-11 by driving the verb by hand, and it had made two
		// shipped claims false. `--field binding=none` on a work_request and
		// `--field x_binding=none` on a contract BOTH failed with "the
		// envelope/v1 schema for <type> has no <field> key", although both
		// fields exist on the v2 schema those types render. fill-classes.yaml
		// and specs/04 §11 each state that `--field <field>=none` "is a real,
		// valid authoring act" — measured through the reachability gate, which
		// calls Render with the generation ALREADY set and therefore never
		// walks this path. A gate proving something the product does not do is
		// the defect this epic exists to remove, and it was in the epic's own
		// evidence.
		//
		// Re-sniff from the template's own default instead. Overrides still
		// decide the generation whenever they CAN be applied at v1, which is
		// every pre-existing case; only the previously-fatal case changes.
		if !errors.Is(err, ErrUnappliableField) {
			return draft, err
		}
		// Remember the refusal. If the generation selected below turns out to
		// be v1 after all, the field was genuinely unappliable to what this
		// type authors and this error is the answer — returning the
		// fieldless sniff draft instead would DROP the override silently,
		// which is the defect testdata/t3/new_validate.txtar exists to
		// forbid: "a --field override the renderer cannot apply is refused
		// where it is GIVEN, never dropped." That test caught this exact
		// regression on the first attempt at this fix.
		unappliableAtV1 = err
		sniff := in
		sniff.Fields = nil
		if draft, err = Render(sniff); err != nil {
			return draft, err
		}
	}
	generation, gerr := selectGeneration(in.Type, func() (string, error) {
		return ContractDraftSchemaFormat(draft)
	}, isJSONSchema)
	if gerr != nil {
		return nil, gerr
	}
	if generation == "envelope/v1" {
		if unappliableAtV1 != nil {
			return nil, unappliableAtV1
		}
		return draft, nil
	}
	in.EnvelopeSchema = generation
	return Render(in)
}

func replaceScalarToken(node *yaml.Node, old, replacement string) {
	if node.Kind == yaml.ScalarNode {
		node.Value = strings.ReplaceAll(node.Value, old, replacement)
	}
	for _, child := range node.Content {
		replaceScalarToken(child, old, replacement)
	}
}

// applyFills walks mapping's top-level key/value pairs in place, filling
// id/created/actor from in and every other field from in.Fields (if
// present) or the enum-placeholder default rule (if the raw value matches
// enumPlaceholder). Any in.Fields key that names no key in mapping is an
// ERROR (ErrUnappliableField), never silence — see that sentinel's doc
// comment.
//
// Found on 2026-07-26 as the D0 root cause: --field/--thread overrides for a
// key the template does not carry were silently dropped (this loop only
// ever visits keys ALREADY IN the mapping, so an absent key was never even
// examined). `a2a new --thread <id>` parsed, set fields["thread"], and the
// value never reached the document — no template carried a `thread:` key at
// all. Fixing only `thread` would leave the mechanism itself lying about
// every other field; the fix is generic.
func applyFills(mapping *yaml.Node, in Input) error {
	present := make(map[string]bool, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		val := mapping.Content[i+1]
		present[key.Value] = true
		// acceptance_criteria is minted here, ahead of the switch below,
		// ONLY when the caller actually supplied --acceptance-criterion
		// values — an empty/nil in.AcceptanceCriteria falls straight
		// through to the switch's own default case, so a hand-authored
		// template placeholder or a plain --field acceptance_criteria=...
		// override behaves exactly as before this field existed.
		if key.Value == "acceptance_criteria" && len(in.AcceptanceCriteria) > 0 {
			setAcceptanceCriteria(val, envelopeGeneration(in.EnvelopeSchema), in.AcceptanceCriteria)
			continue
		}
		switch key.Value {
		case "id":
			setScalar(val, in.ID)
		case "created":
			setScalar(val, in.Created.UTC().Format(time.RFC3339))
		case "actor":
			fillActor(val, in.Actor)
		default:
			if override, ok := in.Fields[key.Value]; ok {
				if err := setField(val, key.Value, override); err != nil {
					return err
				}
				continue
			}
			// No default-fill for an unreplaced enum-alternatives
			// placeholder (agent-exchange-2026-08 B3, AC4): the tool
			// must not invent a choice the author never made. The
			// token is left exactly as the template wrote it and
			// survives to the rendered draft, where POL-010 refuses
			// it at V2. See Render's doc comment for the full story.
		}
	}

	// acceptance_criteria is APPENDED when --acceptance-criterion was given
	// and the template carries no such key at all (question, decision,
	// announcement — their templates never render it, unlike work_request/
	// requirement/handoff above, which hit the top-level case instead).
	// schemas/envelope/v{1,2}/base.schema.json declares acceptance_criteria
	// itself (AUTHOR, fill-classes.yaml), and every per-type schema $refs
	// base with unevaluatedProperties:false, so the field is legal on
	// EVERY type/generation this package renders — unlike the generic
	// --field append pass below, which refuses any array/object-typed key
	// outright (setField's append only ever writes one scalar node). This
	// bypasses that refusal deliberately: the node built here is a real,
	// correctly-typed sequence, not a caller-typed string being parsed.
	if len(in.AcceptanceCriteria) > 0 && !present["acceptance_criteria"] {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "acceptance_criteria"},
			renderAcceptanceCriteria(envelopeGeneration(in.EnvelopeSchema), in.AcceptanceCriteria))
		present["acceptance_criteria"] = true
	}

	// Second pass: any --field key not yet applied and not matched by the
	// top-level loop above is tried as a DOTTED path into a nested mapping
	// (e.g. `expected_response.shape`) — never a sequence index (`refs[0].
	// ref` stays unsupported; §A1's map-only grammar). An exact top-level
	// key always wins first (the loop above already ran), so this can
	// never change a non-dotted key's behaviour.
	//
	// Found on 2026-07-27 as the A1 half of the placeholder defect: a
	// nested field like `expected_response.shape` could not be filled by
	// `--field` at all, because applyFills only ever walked TOP-LEVEL
	// keys — a dotted key matched none of them and fell straight into the
	// trailing unappliable-field loop below with no chance to resolve.
	// Splitting the key, descending, and marking the resolved path present
	// land together in this one pass, so a dotted key can never silently
	// fall through the way the pre-fix top-level lookup did.
	//
	// One consequence is deliberate rather than incidental, so it is stated
	// here: because this pass runs AFTER the loop above, `--field
	// actor.name=X` now OVERRIDES the actor fillActor already wrote from
	// in.Actor. That is the ordering plan §7.4 mandates — "explicit flags >
	// A2A_ACTOR_* env vars > harness adapter defaults > config" — and a
	// dotted `--field` is an explicit flag, so it belongs at the top of that
	// order. Pinned by a test, because the alternative reading (the resolved
	// actor is authoritative and the flag is dropped) is exactly the silence
	// this whole mechanism exists to refuse.
	dotted := make([]string, 0, len(in.Fields))
	for field := range in.Fields {
		if present[field] || !strings.Contains(field, ".") {
			continue
		}
		dotted = append(dotted, field)
	}
	sort.Strings(dotted)
	for _, field := range dotted {
		node, rerr := resolveDottedPath(mapping, field)
		if rerr != nil {
			return rerr
		}
		if err := setField(node, field, in.Fields[field]); err != nil {
			return err
		}
		present[field] = true
	}

	// A --field key the template does not carry is APPENDED when the
	// envelope schema allows it, and refused only when it does not.
	//
	// Five fields — effort_estimate, supersedes, origin, migrated_from and
	// (on five of eight types) expected_response — are schema-legal on every
	// envelope and appear in no template, so they were unreachable from any
	// authoring surface: `--field` refused them for not being in the
	// template, and the template could not carry them without putting an
	// unfilled placeholder in every draft, which POL-010 then refuses at
	// submit. Neither end could move, and the field stayed unusable.
	//
	// Appending on demand breaks that: a draft carries the field only when
	// the author asked for it, and asks nothing of the drafts that do not.
	// The refusal stays for a key the schema genuinely does not have — a
	// typo must still be a typo.
	var unappliable []string
	for field := range in.Fields {
		if present[field] {
			continue
		}
		if !schemaAllowsField(in.Type, in.EnvelopeSchema, field) {
			unappliable = append(unappliable, field)
			continue
		}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: field},
			&yaml.Node{Kind: yaml.ScalarNode, Value: in.Fields[field]})
		present[field] = true
	}
	if len(unappliable) > 0 {
		sort.Strings(unappliable)
		field := unappliable[0]
		return fmt.Errorf("%w: --field %s=%q was given, but neither this template nor the %s schema for %q has a %q key",
			ErrUnappliableField, field, in.Fields[field], envelopeGeneration(in.EnvelopeSchema), in.Type, field)
	}
	return nil
}

// envelopeGeneration names the generation a draft is being rendered for,
// defaulting to the historical envelope/v1 exactly as rawTemplate does.
func envelopeGeneration(declared string) string {
	if declared == "" {
		return "envelope/v1"
	}
	return declared
}

// schemaAllowsField reports whether a CALLER may supply this field on an
// envelope of this type and generation. Two questions, both required:
//
//   - Does the schema have the field? The template is one authoring
//     convenience over the schema, and treating it as the definition of what
//     an artifact may contain is what made five schema-legal fields unusable.
//   - Is the field the caller's to state? schemas/fill-classes.yaml answers
//     that, and TOOL and DETECTED are refused.
//
// The second check was missing when append-on-demand first shipped, and a
// post-phase audit found the hole by typing `migrated_from` — a TOOL field —
// onto a v2 contract draft. Asking only the schema reopened, in the same
// phase, the exact rule the phase exists to enforce: a field the tool
// establishes must not be typeable, and "the schema has it" says nothing
// about who may fill it.
func schemaAllowsField(typ, generation, field string) bool {
	version := 1
	if generation == "envelope/v2" {
		version = 2
	}
	// Only a SCALAR field may be appended. The append writes one
	// yaml.ScalarNode, so naming an array (`origin`, `refs`) or an object
	// (`expected_response`) produces a draft the schema refuses — and
	// `a2a new` writes the file straight after rendering, with no validation
	// in between, so the author learns about it at submit or not at all.
	//
	// setField, which handles the list case, only ever runs for a key the
	// template already carries; there is no existing node to route through
	// here. Refusing is the honest answer until there is.
	if declared, err := schema.EnvelopeFieldType(version, typ, field); err == nil {
		switch declared {
		case "array", "object":
			return false
		}
	}
	if table, err := schema.LoadFillClasses(); err == nil {
		group := fmt.Sprintf("envelope/v%d/%s", version, typ)
		// Checked against BOTH the per-type group and the base, because a
		// shared field's row lives on the base and a caller naming it must
		// be refused by the base's classification.
		base := fmt.Sprintf("envelope/v%d/base", version)
		if !table.Typeable(group, field) || !table.Typeable(base, field) {
			return false
		}
	}
	// No error branch: "the corpus could not answer" and "the schema does
	// not allow it" are the same answer here — refuse. Distinguishing them
	// would only offer a way to accept a field nobody could confirm.
	paths, err := schema.EnvelopeFieldPaths(version, typ)
	if err != nil {
		return false
	}
	for _, p := range paths {
		if p == field {
			return true
		}
	}
	return false
}

// resolveDottedPath descends mapping through fullPath's dot-separated
// segments, returning the leaf node reached by the FINAL segment. Every
// intermediate segment must resolve to a MappingNode key that exists —
// anything else (an absent key, or a segment landing on a non-mapping
// node with more path remaining) is ErrUnappliableField naming fullPath
// and the segment where resolution stopped, never a panic and never
// silence: a dotted key that resolves to nothing is exactly the same
// class of bug this package's whole ErrUnappliableField mechanism exists
// to refuse instead of drop.
func resolveDottedPath(mapping *yaml.Node, fullPath string) (*yaml.Node, error) {
	segments := strings.Split(fullPath, ".")
	cur := mapping
	// walked is the path successfully descended through BEFORE the
	// current segment — i.e. the segment whose resolved node is `cur`.
	// Empty at i==0, since cur is still the template's own root mapping
	// there (always a MappingNode by construction — Render already
	// checked that before calling applyFills at all).
	walked := ""
	for i, seg := range segments {
		if cur.Kind != yaml.MappingNode {
			at := walked
			if at == "" {
				at = fullPath
			}
			return nil, fmt.Errorf("%w: --field %s=... cannot be applied — %q is a %s in the template, not a mapping, so it cannot be descended into",
				ErrUnappliableField, fullPath, at, nodeKindName(cur.Kind))
		}
		found := false
		var next *yaml.Node
		for j := 0; j+1 < len(cur.Content); j += 2 {
			if cur.Content[j].Value == seg {
				next = cur.Content[j+1]
				found = true
				break
			}
		}
		if walked == "" {
			walked = seg
		} else {
			walked += "." + seg
		}
		if !found {
			return nil, fmt.Errorf("%w: --field %s=... was given, but this template has no %q key",
				ErrUnappliableField, fullPath, walked)
		}
		if i == len(segments)-1 {
			return next, nil
		}
		cur = next
	}
	// Unreachable: fullPath always has >=1 segment (strings.Split never
	// returns an empty slice), so the loop above always returns.
	return nil, fmt.Errorf("%w: --field %s=... could not be resolved", ErrUnappliableField, fullPath)
}

// setField applies one --field override to whatever KIND of node the template
// has there.
//
// # Why this is not just setScalar
//
// setScalar writes node.Value, and for a SEQUENCE node the encoder emits its
// children and never looks at Value — so `--field to=alpha` was accepted and
// silently did nothing. The template's `<recipient-system>` placeholder survived,
// and the write was refused minutes later by `submit` with
// "REF-006 to: `to` includes an unknown system: <recipient-system>": a complaint
// about a placeholder the author believed they had replaced, with nothing
// anywhere saying the flag had been dropped.
//
// Found on 2026-07-26 by drafting an announcement on a real space and reading
// the refusal. Verified three ways first — `to=alpha`, `to=[alpha]` and
// `to=- alpha` all did nothing — because "my syntax was wrong" is the likelier
// explanation and had to be ruled out.
//
// Two readings are accepted for a sequence, both of which somebody will write:
// a YAML list (`[alpha]`, or `- alpha`), and a bare scalar (`alpha`), which is
// taken as the one-element list. Anything that cannot be applied is an ERROR
// naming the field and both kinds — never silence. That is the whole point: the
// old behaviour was not "unsupported", it was unsupported and quiet.
func setField(node *yaml.Node, field, override string) error {
	if node.Kind == yaml.ScalarNode {
		setScalar(node, override)
		return nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(override), &doc); err != nil || len(doc.Content) == 0 {
		return fmt.Errorf("%w: --field %s=%q is not valid YAML, and this field is a %s in the template",
			ErrUnappliableField, field, override, nodeKindName(node.Kind))
	}
	value := doc.Content[0]

	// A bare scalar for a sequence field is the one-element reading — what
	// `--field to=alpha` obviously means.
	if node.Kind == yaml.SequenceNode && value.Kind == yaml.ScalarNode {
		wrapped := *value
		value = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{&wrapped}}
	}
	if value.Kind != node.Kind {
		return fmt.Errorf("%w: --field %s=%q is a %s, but this field is a %s in the template",
			ErrUnappliableField, field, override, nodeKindName(value.Kind), nodeKindName(node.Kind))
	}

	// The template's trailing guidance comment ("# broadcast: array, or \"all\"")
	// is guidance for filling the field IN; once filled it is noise, and the
	// replacement node carries none. Head and foot comments are preserved,
	// because those document the field itself rather than how to complete it.
	head, foot := node.HeadComment, node.FootComment
	*node = *value
	node.HeadComment, node.FootComment = head, foot
	return nil
}

// nodeKindName renders a yaml.Kind for an error a human reads.
func nodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.ScalarNode:
		return "scalar"
	case yaml.SequenceNode:
		return "list"
	case yaml.MappingNode:
		return "mapping"
	default:
		return "value"
	}
}

// fillActor rewrites the actor mapping's kind/name/model entries from a,
// dropping the model pair entirely when a.Model is empty (an empty model
// value is not a meaningful fact to assert, and model is optional per the
// base envelope schema).
func fillActor(node *yaml.Node, a Actor) {
	if node.Kind != yaml.MappingNode {
		return
	}
	kept := make([]*yaml.Node, 0, len(node.Content))
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "kind":
			// An unset Kind means NO source claimed one — not "agent".
			// The template's own literal then stands, and the case that
			// proves why is schemas/templates/v1/decision.md's
			// `actor: {kind: human, ...}`: a decision typically carries a
			// human actor because approving one is a G3 gate, and this fill
			// used to overwrite that with `agent` on every render, silently
			// contradicting the template's own documented intent.
			//
			// The resolver no longer applies the "agent" default itself, so
			// the distinction survives all the way here. Event writers apply
			// it at their own boundary, where the field is required.
			if a.KindClaimed {
				setScalar(val, a.Kind)
			}
		case "name":
			setScalar(val, a.Name)
		case "model":
			if a.Model == "" {
				continue // omit, don't emit an empty model value
			}
			setScalar(val, a.Model)
		case "session":
			// Detection fills it when it fired; otherwise the template's own
			// key is LEFT ALONE, deliberately unlike `model` above.
			//
			// Dropping the key when the session is unknown looks symmetric
			// and is not: `actor.session` is required on a v2 work-checkpoint
			// announcement, and `a2a work start` supplies it through the
			// dotted-field pass — which refuses a key the template does not
			// carry. Removing it here turned that supported path into
			// "template has no actor.session key", caught by
			// TestWorkProductionWiringMutations.
			//
			// Before P3 there was no case here at all, so a detected session
			// was thrown away and an agent had to type by hand a value the
			// tool already knew.
			if a.Session != "" {
				setScalar(val, a.Session)
			}
		}
		kept = append(kept, key, val)
	}
	node.Content = kept
}

// setScalar overwrites node's value, clearing its style/tag so the
// encoder re-infers the correct emission form (plain vs quoted) for the
// new content rather than reusing whatever style the template's original
// placeholder text happened to have.
func setScalar(node *yaml.Node, value string) {
	node.Value = value
	node.Style = 0
	node.Tag = ""
}

// setAcceptanceCriteria replaces node's content in place with the sequence
// renderAcceptanceCriteria builds, preserving head/foot comments exactly as
// setField does — the trailing template guidance comment above the key is
// documentation for how to fill the field, and once filled it is noise
// (setField's own doc comment); the head/foot pairing survives here because
// they document the FIELD, not how to complete it.
func setAcceptanceCriteria(node *yaml.Node, generation string, criteria []string) {
	seq := renderAcceptanceCriteria(generation, criteria)
	head, foot := node.HeadComment, node.FootComment
	*node = *seq
	node.HeadComment, node.FootComment = head, foot
}

// renderAcceptanceCriteria builds the acceptance_criteria[] sequence node
// `a2a new --acceptance-criterion` mints, in the order the criteria were
// given: envelope/v2 gets the id-bearing `{id, text}` form, ids minted
// `ac1..acN`; every other generation (envelope/v1, or RenderNew's own v1
// sniff pass) gets bare strings and no ids — v1's published, immutable
// `items: {type: string}` shape (schemas/envelope/v1/base.schema.json).
//
// This is the ONE seat that mints a criterion id. The TEXT is exactly what
// the author typed, unchanged; the id is a tool-minted HANDLE over that
// text, in the order the author typed it — `a2a attach`'s own precedent for
// a tool-minted value (ref/digest) over author-supplied bytes. Neither
// AUTHOR fill-class field (the text) nor this minted handle collide with
// what the epic calls fabrication: nothing here invents what the author
// meant.
func renderAcceptanceCriteria(generation string, criteria []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i, text := range criteria {
		if generation != "envelope/v2" {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: text})
			continue
		}
		seq.Content = append(seq.Content, &yaml.Node{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "id"},
				{Kind: yaml.ScalarNode, Value: fmt.Sprintf("ac%d", i+1)},
				{Kind: yaml.ScalarNode, Value: "text"},
				{Kind: yaml.ScalarNode, Value: text},
			},
		})
	}
	return seq
}

func rawTemplate(typ, envelopeSchema string) ([]byte, error) {
	if !isKnownType(typ) {
		return nil, ErrUnknownType
	}
	version := "v1"
	switch envelopeSchema {
	case "", "envelope/v1":
	case "envelope/v2":
		version = "v2"
	default:
		return nil, ErrUnsupportedEnvelopeSchema
	}
	return schemas.FS.ReadFile("templates/" + version + "/" + typ + ".md")
}

func isKnownType(typ string) bool {
	for _, t := range schema.EnvelopeTypes() {
		if t == typ {
			return true
		}
	}
	return false
}
