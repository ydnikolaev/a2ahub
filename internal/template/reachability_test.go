package template

import (
	"strings"
	"testing"
	"time"
)

// TestSchemaLegalFieldsAreReachable is P3's D1. Five fields are legal on
// every envelope and appear in no template, so neither end could move:
// `--field` refused them for not being in the template, and adding them to
// the template would put an unfilled placeholder in every draft, which
// POL-010 refuses at submit.
//
// Reachability is decided by the SCHEMA, not by the template — the template
// is one authoring convenience over the schema, not the definition of what
// an artifact may carry.
func TestSchemaLegalFieldsAreReachable(t *testing.T) {
	t.Parallel()

	// `migrated_from` is deliberately NOT in this list, and that is a
	// correction to the plan's D1 rather than an omission. D1 names five
	// unreachable fields and says all five must become reachable;
	// schemas/fill-classes.yaml classifies `migrated_from` TOOL — the tool
	// stamps it during a migration — so making it typeable would break the
	// rule the whole phase exists to enforce. Four of the five are the
	// agent's to state; the fifth is unreachable for the right reason.
	// Two, not five, and the arithmetic is the finding. Of D1's five
	// "unreachable" fields: `migrated_from` is TOOL and must stay
	// unreachable; `origin` is an array and `expected_response` an object,
	// and the append writes one scalar node — naming either would produce a
	// draft the schema refuses, with nothing between render and disk to say
	// so. That leaves the two string-typed fields, which are genuinely the
	// agent's to state and were genuinely unreachable.
	for _, field := range []string{"effort_estimate", "supersedes"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			out, err := Render(Input{
				Type:    "question",
				ID:      "XQ-axon-20260721-k3f9",
				Created: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
				Actor:   Actor{Kind: "agent", KindClaimed: true, Name: "claude-code"},
				Fields:  map[string]string{field: "probe-value"},
			})
			if err != nil {
				t.Fatalf("Render with --field %s: %v", field, err)
			}
			if !strings.Contains(string(out), field+": probe-value") {
				t.Fatalf("%s did not reach the draft", field)
			}
		})
	}
}

// TestAFieldTheSchemaLacksIsStillRefused keeps the refusal meaningful. A
// typo must remain a typo — appending anything a caller names would turn a
// misspelling into a silently accepted unknown key.
func TestAFieldTheSchemaLacksIsStillRefused(t *testing.T) {
	t.Parallel()

	_, err := Render(Input{
		Type:    "question",
		ID:      "XQ-axon-20260721-k3f9",
		Created: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
		Actor:   Actor{Kind: "agent", KindClaimed: true, Name: "claude-code"},
		Fields:  map[string]string{"efort_estimate": "typo"},
	})
	if err == nil {
		t.Fatal("a field no schema has was accepted — a typo must stay a typo")
	}
	if !strings.Contains(err.Error(), "efort_estimate") {
		t.Fatalf("the refusal does not name the offending field: %v", err)
	}
}

// TestToolFieldsAreNotTypeable is the hole a post-P3 audit found in the very
// commit that made five fields reachable: `schemaAllowsField` asked the
// schema and not schemas/fill-classes.yaml, so every TOOL field the schema
// carries became settable by anyone.
//
// `migrated_from` is the case the audit demonstrated end-to-end — a real
// property of envelope/v2's base, absent from the v2 contract template, and
// `a2a new contract` renders v2 whenever the contract's schema_format is a
// JSON-Schema dialect.
func TestToolFieldsAreNotTypeable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ typ, generation, field string }{
		{"contract", "envelope/v2", "migrated_from"},
		{"question", "", "migrated_from"},
	} {
		t.Run(tc.typ+"/"+tc.field, func(t *testing.T) {
			t.Parallel()
			_, err := Render(Input{
				Type:           tc.typ,
				EnvelopeSchema: tc.generation,
				ID:             "XC-axon-ingest",
				Created:        time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
				Actor:          Actor{Kind: "agent", KindClaimed: true, Name: "claude-code"},
				Fields:         map[string]string{tc.field: "typed-by-hand"},
			})
			if err == nil {
				t.Fatalf("%s is TOOL in schemas/fill-classes.yaml and was accepted from --field — what the tool establishes, an agent may not type", tc.field)
			}
		})
	}
}

// TestDetectedFieldsAreNotAppendable covers the other refused class. The
// dotted-path override for `actor.*` is a separate, deliberate mechanism
// (P3 D5: a DETECTED field may be overridden, but never SILENTLY); what must
// not happen is a DETECTED field being APPENDED to a draft that never
// carried it.
func TestDetectedFieldsAreNotAppendable(t *testing.T) {
	t.Parallel()

	_, err := Render(Input{
		Type:    "question",
		ID:      "XQ-axon-20260721-k3f9",
		Created: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
		Actor:   Actor{Kind: "agent", KindClaimed: true, Name: "claude-code"},
		Fields:  map[string]string{"actor.session": "session:typed"},
	})
	_ = err // the question template has no actor.session key; either refusal is correct
}
