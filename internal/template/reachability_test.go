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

	for _, field := range []string{"effort_estimate", "supersedes", "origin", "migrated_from"} {
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
