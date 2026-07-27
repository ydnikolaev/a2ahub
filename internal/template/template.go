package template

import (
	"fmt"
	"regexp"
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
}

// Input carries every value Render needs that must come from the caller
// for testability: the minted ID, the resolved actor, and "now" are never
// computed inside this package (rails: "never time.Now() inside render").
type Input struct {
	// Type is one of the 8 §3.1 envelope types (schema.EnvelopeTypes()).
	Type string
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
}

// enumPlaceholder matches the pipe-alternatives placeholder token every
// canonical template uses for its one enum-constrained field (e.g.
// "<clarification|defect|choice>", "<answered|delivered|partial|cannot>").
// Render's default-fill rule for these is entirely data-driven off this
// shape — no per-type switch statement (Future-proofing table, §9): absent
// an explicit Fields override, the FIRST alternative (the template
// author's own ordering, chosen to avoid triggering any conditionally-
// required field a later alternative implies) is filled in.
var enumPlaceholder = regexp.MustCompile(`^<([^<>|]+(?:\|[^<>|]+)+)>$`)

// Types returns the 8 canonical envelope type names this package has an
// embedded template for, in schema.EnvelopeTypes()'s own stable order.
func Types() []string {
	return schema.EnvelopeTypes()
}

// Show returns typ's canonical embedded template's raw bytes, UNRENDERED —
// what `a2a template show <type>` prints: read-only inspection of the same
// template Render fills.
func Show(typ string) ([]byte, error) {
	const op = "Show"
	raw, err := rawTemplate(typ)
	if err != nil {
		return nil, &Error{Op: op, Input: typ, Err: err}
	}
	return raw, nil
}

// Render fills typ's canonical embedded template with in's minted ID,
// resolved actor, and current date, applies any --field overrides, and
// fills every enum-constrained placeholder field with its first valid
// alternative absent an override — then returns the complete draft bytes
// (frontmatter + body), otherwise byte-identical to the canonical
// template. Every other field the template already carries an
// already-schema-valid literal default for (priority, blocking,
// classification, ...) is left untouched, which is what makes AC-401.1
// ("V1 pass on placeholder-only fills") hold without this package needing
// per-type domain knowledge beyond the enum-placeholder convention.
func Render(in Input) ([]byte, error) {
	const op = "Render"
	raw, err := rawTemplate(in.Type)
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
			if val.Kind == yaml.ScalarNode {
				if m := enumPlaceholder.FindStringSubmatch(val.Value); m != nil {
					alts := strings.Split(m[1], "|")
					setScalar(val, strings.TrimSpace(alts[0]))
				}
			}
		}
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

	for field := range in.Fields {
		if !present[field] {
			return fmt.Errorf("%w: --field %s=%q was given, but this template has no %q key",
				ErrUnappliableField, field, in.Fields[field], field)
		}
	}
	return nil
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
			setScalar(val, orDefault(a.Kind, "agent"))
		case "name":
			setScalar(val, a.Name)
		case "model":
			if a.Model == "" {
				continue // omit, don't emit an empty model value
			}
			setScalar(val, a.Model)
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func rawTemplate(typ string) ([]byte, error) {
	if !isKnownType(typ) {
		return nil, ErrUnknownType
	}
	return schemas.FS.ReadFile("templates/v1/" + typ + ".md")
}

func isKnownType(typ string) bool {
	for _, t := range schema.EnvelopeTypes() {
		if t == typ {
			return true
		}
	}
	return false
}
