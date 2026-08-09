package validate

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// TestPOL010ReachesEveryPlaceholderInTheShippedTemplates is POL-010's
// coverage gate, and it exists because a hand-listed sample of placeholders
// already failed once.
//
// The first version of placeholderPattern was `^<[^<>]*>$`, chosen with four
// hand-picked cases. It looked right and its own tests passed — and it
// silently missed `title: <human/agent-scannable title, <=120 chars>`, which
// EVERY one of the eight canonical templates carries, because the inner `<`
// of "<=120" defeated the no-nesting clause. A refusal that misses the most
// visible field of every artifact type is not a refusal.
//
// So this test does not sample. It renders each of the eight types the way
// `a2a new` does, runs the real check, and pins the EXACT set of paths it
// reports. That makes both directions loud:
//
//   - narrow the pattern (or drop a walk branch) and paths disappear -> red,
//     naming the ones that went missing;
//   - add or reword a template placeholder and a path appears -> red, naming
//     it, so the corpus and the refusal can never drift apart in silence.
//
// The expected sets ARE the interesting product fact in their own right:
// they are exactly what a freshly drafted artifact of each type still has to
// fill before it may enter the shared record.
func TestPOL010ReachesEveryPlaceholderInTheShippedTemplates(t *testing.T) {
	t.Parallel()

	// What `a2a new` itself always supplies: id, created and actor come
	// from Input's own fields; `from` is the caller's own system; `space`
	// is defaulted when exactly one space is connected; `thread` is minted
	// (spec 46 §T1). Everything NOT in this list is what the author still
	// owes, and is what the expectations below enumerate.
	fields := map[string]string{
		"space":  "getvisa",
		"from":   "axon",
		"thread": "thread:axon-20260727-h6ps",
	}

	// `to.0` appears for every type: no template ships a recipient, and
	// `a2a new` does not default one. That is the placeholder whose silent
	// survival produced the "REF-006 to: `to` includes an unknown system:
	// <recipient-system>" refusal this repo already documents — now named
	// at submit, by path, instead of arriving as a complaint about a
	// system nobody typed.
	// `category` (announcement, contract, question, requirement,
	// work_request) and `result` (response) join this table for
	// agent-exchange-2026-08 B3 (spec 03-fill-classes.md §8 AC4):
	// applyFills no longer fills an unreplaced enum-alternatives placeholder
	// with its first alternative, so a freshly drafted artifact of these six
	// types now owes its enum field too, exactly like every other
	// author-owed placeholder in this table. `decision` and `handoff` carry
	// no top-level enum placeholder and are unaffected. This entry MOVES
	// WITH THE PRODUCT: it is what makes the row above a pin rather than a
	// decoration — it reds the moment the fill is restored.
	want := map[string][]string{
		"announcement": {"category", "title", "to.0"},
		"contract":     {"category", "title", "to.0"},
		"decision": {
			"context", "options_considered.0", "options_considered.1",
			"required_approvers.0", "required_approvers.1", "title", "to.0",
		},
		"question": {"category", "expected_response.shape", "title", "to.0"},
		"response": {"parent", "result", "title", "to.0"},
		"requirement": {
			"acceptance_criteria.0", "category", "expected_response.shape", "interim_behavior",
			"needed_by", "title", "to.0",
		},
		"work_request": {
			"acceptance_criteria.0", "category", "expected_response.shape", "interim_behavior",
			"needed_by", "title", "to.0",
		},
		"handoff": {
			"acceptance_criteria.0", "deliverables.0.name", "deliverables.0.ref",
			"fulfills.0", "refs.0.note", "refs.0.ref", "title", "to.0",
			"verification",
		},
	}

	types := template.Types()
	if len(types) != len(want) {
		t.Fatalf("template.Types() has %d entries but this test enumerates %d — a new envelope type must state which placeholders a fresh draft of it still owes", len(types), len(want))
	}

	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			expected, ok := want[typ]
			if !ok {
				t.Fatalf("no expectation for envelope type %q", typ)
			}

			raw, err := template.Render(template.Input{
				Type:    typ,
				ID:      "XQ-axon-20260727-k3f9",
				Actor:   template.Actor{Kind: "agent", Name: "opus"},
				Created: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
				Fields:  fields,
			})
			if err != nil {
				t.Fatalf("template.Render(%s): %v", typ, err)
			}

			fm, ferr := artifact.ParseFrontmatter(raw)
			if ferr != nil {
				t.Fatalf("parse rendered %s: %v", typ, ferr)
			}
			instance, ierr := schema.DecodeYAMLInstance(fm.YAML)
			if ierr != nil {
				t.Fatalf("decode rendered %s: %v", typ, ierr)
			}

			got := make([]string, 0, len(expected))
			for _, v := range checkUnfilledPlaceholders(instance) {
				if v.Code != "POL-010" || v.Class != ClassPolicy {
					t.Errorf("violation %+v: want code POL-010 and ClassPolicy", v)
				}
				got = append(got, v.Path)
			}
			sort.Strings(got)

			if strings.Join(got, ",") != strings.Join(expected, ",") {
				t.Errorf("a freshly drafted %s reports POL-010 for\n  got  %v\n  want %v\n"+
					"If a path went MISSING the refusal no longer reaches a real placeholder — that is the "+
					"defect this test exists for, not a stale expectation. If a path is NEW, the template "+
					"corpus changed: confirm the new placeholder is genuinely author-owed, then update the "+
					"expectation here.", typ, got, expected)
			}
		})
	}
}

// TestPOL010DoesNotTripTheSanitizationFixture pins the case that motivated
// the original (too narrow) pattern, so widening it cannot quietly
// reintroduce a false positive on real angle-bracket content. The value is
// the literal `title:` from internal/e2e/testdata/sanitization/script-tag.md
// — a real committed fixture, not an invented string.
func TestPOL010DoesNotTripTheSanitizationFixture(t *testing.T) {
	t.Parallel()

	notPlaceholders := []string{
		`<script>alert(1)</script> raw <b>HTML</b> && "quotes"`,
		"<nil> panic in the parser",
		"comparing <a> and <b> tags in the feed",
		"a normal title",
		"",
	}
	for _, v := range notPlaceholders {
		if got := checkUnfilledPlaceholders(map[string]any{"title": v}); len(got) != 0 {
			t.Errorf("title=%q tripped POL-010 (%+v); it is real content, not a placeholder", v, got)
		}
	}
}

// TestPOL010RemedyNamesTheReachableAction guards the half of the message that
// is load-bearing: a caller who follows the sentence must actually be able to
// clear the violation. Every case here was WRONG in the first draft and was
// found by reading the real emitted messages, not by reasoning — which is why
// each one is pinned rather than trusted.
func TestPOL010RemedyNamesTheReachableAction(t *testing.T) {
	t.Parallel()

	// A mapping path: `--field` reaches it directly, and removal is offered
	// because an optional field is cleared by deleting the line rather than by
	// inventing a value (needed_by, valid_until, env_requirements).
	mapPath := placeholderRemedy("expected_response.shape")
	if !strings.Contains(mapPath, "--field expected_response.shape=") {
		t.Errorf("remedy for a mapping path = %q, want it to name the --field that reaches it", mapPath)
	}
	if !strings.Contains(mapPath, "delete the line") {
		t.Errorf("remedy for a mapping path = %q, want it to also offer deletion for an optional field", mapPath)
	}

	// A list whose placeholder IS the whole list. `--field to=seomatrix`
	// WORKS — setField reads a bare scalar for a sequence field as the
	// one-element list — so sending the caller to a text editor here would be
	// false, and it was: this is the single most common refusal across all
	// eight types (`to.0`), plus acceptance_criteria.0 and fulfills.0.
	for _, path := range []string{"to.0", "acceptance_criteria.0", "fulfills.0"} {
		got := placeholderRemedy(path)
		field := strings.TrimSuffix(path, ".0")
		if !strings.Contains(got, "--field "+field+"=") {
			t.Errorf("remedy for %q = %q, want it to name `--field %s=<value>` — that flag reaches a whole-list placeholder and telling the caller to hand-edit instead is simply untrue", path, got, field)
		}
		if !strings.Contains(got, "single entry") {
			t.Errorf("remedy for %q = %q, want it to say the flag REPLACES the list with that one entry — a caller with a longer list must not learn that the hard way", path, got)
		}
	}

	// An index MID-path: the dotted grammar descends mappings and stops at the
	// list, so no flag reaches this one. Here the file edit is the truth.
	for _, path := range []string{"refs.0.ref", "deliverables.0.name"} {
		got := placeholderRemedy(path)
		// Naming the flag to EXPLAIN that it cannot reach here is fine and is
		// what the message does; what must not appear is an actionable
		// `--field <something>=` the caller would copy and watch fail.
		if strings.Contains(got, "=<value>") {
			t.Errorf("remedy for %q = %q, but --field cannot reach INTO a list element — handing the caller a flag to copy sends them into a refusal loop", path, got)
		}
		if !strings.Contains(got, "edit "+path) {
			t.Errorf("remedy for %q = %q, want it to name the file edit instead", path, got)
		}
	}
}
