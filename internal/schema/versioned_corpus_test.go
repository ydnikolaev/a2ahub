package schema

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionedCorpus_DistinctEnvelopeBaseObjects(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	v1, ok := c.schemaFor(corpusKey{family: familyEnvelope, version: 1, typ: typeBase})
	if !ok {
		t.Fatal("envelope/v1/base decoder is not registered")
	}
	v2, ok := c.schemaFor(corpusKey{family: familyEnvelope, version: 2, typ: typeBase})
	if !ok {
		t.Fatal("envelope/v2/base decoder is not registered")
	}
	if v1 == v2 {
		t.Fatal("envelope/v1/base and envelope/v2/base unexpectedly share one compiled schema object")
	}
}

func TestVersionedCorpus_EnvelopeV2BaseFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	v1, ok := c.schemaFor(corpusKey{family: familyEnvelope, version: 1, typ: typeBase})
	if !ok {
		t.Fatal("envelope/v1/base decoder is not registered")
	}
	v2, ok := c.schemaFor(corpusKey{family: familyEnvelope, version: 2, typ: typeBase})
	if !ok {
		t.Fatal("envelope/v2/base decoder is not registered")
	}

	valid := fixtureInstance(t, filepath.Join(corpusRoot, "envelope", "v2", "fixtures", "valid", "base-foundation.yaml"))
	if violations := extractFieldViolations(v2.Validate(valid), nil); len(violations) != 0 {
		t.Fatalf("v2 base rejected its valid foundation fixture: %+v", violations)
	}

	// The schema field is the v2 base's version discriminator. The same
	// document must not validate through the historical v1 object.
	violations := extractFieldViolations(v1.Validate(valid), nil)
	if !hasFieldViolation(violations, "schema", "const") {
		t.Fatalf("v1 base accepted the v2 schema field, violations: %+v", violations)
	}

	invalid := fixtureInstance(t, filepath.Join(corpusRoot, "envelope", "v2", "fixtures", "invalid", "base-foundation-wrong-schema.yaml"))
	violations = extractFieldViolations(v2.Validate(invalid), nil)
	if !hasFieldViolation(violations, "schema", "const") {
		t.Fatalf("v2 base accepted the invalid v1 schema field, violations: %+v", violations)
	}
}

// TestVersionedCorpus_UnregisteredEnvelopeV2TypeRefused pins the ABSENCE of
// an envelope/v2 decoder for the five types that genuinely have none —
// requirement, question, decision, handoff, response. This used to also
// cover work_request; P4 wave A registers (envelope, v2, work_request), so
// work_request now resolves and is proven separately below
// (TestVersionedCorpus_WorkRequestV2Resolves). The ErrUnknownType behaviour
// itself stays load-bearing for the five types that still lack a v2
// schema — it is the honest refusal an older binary gives a type it never
// had, per the plan's §Floor story.
func TestVersionedCorpus_UnregisteredEnvelopeV2TypeRefused(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	for _, typ := range []string{"requirement", "question", "decision", "handoff", "response"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			_, err := c.ValidateEnvelope(typ, "envelope/v2", toInstance(t, "schema: envelope/v2\ntype: "+typ+"\n"))
			if !errors.Is(err, ErrUnknownType) {
				t.Fatalf("ValidateEnvelope(%s, envelope/v2) error = %v, want ErrUnknownType", typ, err)
			}
		})
	}
}

// validWorkRequestV2 is a fully valid envelope/v2 work_request instance,
// carrying one attachment through the new attachments[] block (P4 wave A,
// spec 04-possession.md §7).
const validWorkRequestV2 = `
schema: envelope/v2
id: XW-axon-20260808-p9d3
type: work_request
title: A valid v2 work request
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260808-k3f9
actor: {kind: agent, name: codex}
created: "2026-08-08T08:40:00Z"
category: data
priority: p3
blocking: true
classification: internal
acceptance_criteria:
  - "Every code exists in the registry."
attachments:
  - ref: blob-1
    digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    verification: required
    retention: "168h"
    expires_at: "2026-08-15T10:00:00Z"
`

// TestVersionedCorpus_WorkRequestV2Resolves proves the mutation the brief
// names: remove the corpusDefinitions row for (envelope, v2, work_request)
// and this test must fail, naming work_request as the type that should
// resolve and does not.
// TestVersionedCorpus_RetentionAndExpiryAgree pins BOTH halves of the
// attachments[] conditional, because only one of them was reachable by
// accident.
//
// `retention` is a duration OR the literal "pinned", and `expires_at` is the
// resolved lapse date. The two are not independent: a duration is a RECIPE,
// and nothing on a committed artifact records when the bytes were attached —
// so without the date a reader has no anchor, and one reaching for `created`
// instead would compute a confidently wrong lapse. `pinned` means kept, so a
// lapse date on it is a contradiction rather than a redundancy.
//
// The schema states that as an if/then/else, and this test is what stops
// half of it from being decorative: the positive case above only exercises
// the `else` branch.
func TestVersionedCorpus_RetentionAndExpiryAgree(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	tests := []struct {
		name       string
		retention  string
		expiresAt  string
		wantRefuse bool
		why        string
	}{
		{"a duration carries its resolved date", "168h", "2026-08-15T10:00:00Z", false,
			"the ordinary case: kept for a week, and the artifact says when that ends"},
		{"a duration without the date is refused", "168h", "", true,
			"the reader is handed a recipe and no anchor — this is the shape that made AC5 unprovable"},
		{"pinned carries no date", "pinned", "", false,
			"pinned means kept; the absence of a lapse date IS the claim"},
		{"pinned with a date is refused", "pinned", "2026-08-15T10:00:00Z", true,
			"kept forever and expiring on a Saturday cannot both be true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := strings.Replace(validWorkRequestV2,
				"    retention: \"168h\"\n    expires_at: \"2026-08-15T10:00:00Z\"\n",
				"    retention: \""+tt.retention+"\"\n"+expiresLine(tt.expiresAt), 1)
			violations, err := c.ValidateEnvelope("work_request", "envelope/v2", toInstance(t, doc))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if tt.wantRefuse && len(violations) == 0 {
				t.Errorf("retention=%q expires_at=%q was accepted, want refused — %s", tt.retention, tt.expiresAt, tt.why)
			}
			if !tt.wantRefuse && len(violations) > 0 {
				t.Errorf("retention=%q expires_at=%q was refused (%v), want accepted — %s", tt.retention, tt.expiresAt, violations, tt.why)
			}
		})
	}
}

func expiresLine(v string) string {
	if v == "" {
		return ""
	}
	return "    expires_at: \"" + v + "\"\n"
}

func TestVersionedCorpus_WorkRequestV2Resolves(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	violations, err := c.ValidateEnvelope("work_request", "envelope/v2", toInstance(t, validWorkRequestV2))
	if errors.Is(err, ErrUnknownType) {
		t.Fatalf("ValidateEnvelope(work_request, envelope/v2) error = %v, want work_request to RESOLVE (not ErrUnknownType) now that (envelope, v2, work_request) is registered", err)
	}
	if err != nil {
		t.Fatalf("ValidateEnvelope(work_request, envelope/v2): %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("a fully valid envelope/v2 work_request instance (with attachments[]) produced violations: %+v", violations)
	}
}

func TestVersionedCorpus_HistoricalEnvelopeV1DecoderRetained(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	// Historical decoder retention is registry-based, not bounded by the
	// authoring window. When a later binary authors v3, v1 falls outside
	// N/N-1 but remains a registered replay identity.
	min, _ := acceptedWindow(3)
	if 1 >= min {
		t.Fatal("test precondition: v1 must be outside a simulated v3 authoring window")
	}
	if _, ok := c.schemaFor(corpusKey{family: familyEnvelope, version: 1, typ: "work_request"}); !ok {
		t.Fatal("historical envelope/v1/work_request decoder is not registered")
	}

	violations, err := c.ValidateEnvelope("work_request", "envelope/v1", toInstance(t, validWorkRequest))
	if err != nil {
		t.Fatalf("ValidateEnvelope historical v1: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("historical v1 document no longer validates through its original decoder: %+v", violations)
	}
}

func hasFieldViolation(violations []FieldViolation, path, keyword string) bool {
	for _, violation := range violations {
		if violation.Path == path && violation.Keyword == keyword {
			return true
		}
	}
	return false
}
