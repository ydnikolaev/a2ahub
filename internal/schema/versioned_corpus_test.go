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
// an envelope/v2 decoder for the four types that genuinely have none —
// requirement, question, decision, handoff. This used to also cover
// work_request and response; P4 wave A registers (envelope, v2,
// work_request) and P6 wave A registers (envelope, v2, response), so both
// now resolve and are proven separately below
// (TestVersionedCorpus_WorkRequestV2Resolves,
// TestVersionedCorpus_ResponseV2Resolves). The ErrUnknownType behaviour
// itself stays load-bearing for the four types that still lack a v2
// schema — it is the honest refusal an older binary gives a type it never
// had, per the plan's §Floor story.
func TestVersionedCorpus_UnregisteredEnvelopeV2TypeRefused(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	for _, typ := range []string{"requirement", "question", "decision", "handoff"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			_, err := c.ValidateEnvelope(typ, "envelope/v2", toInstance(t, "schema: envelope/v2\ntype: "+typ+"\n"))
			if !errors.Is(err, ErrUnknownType) {
				t.Fatalf("ValidateEnvelope(%s, envelope/v2) error = %v, want ErrUnknownType", typ, err)
			}
		})
	}
}

// defaultBindingBlock is the object-form `binding` validWorkRequestV2 carries
// by default: a declared claim, adoptable and pinnable, with a real
// (non-`none`) compatibility_status. TestVersionedCorpus_BindingAgrees swaps
// this exact substring to exercise every cell of P5 D1/Q2's shape — the bare
// `none` sentinel, the long form's four required leaves, and the
// compatibility_status:none -> unadoptable/unpinnable conditional threat-model
// T2 requires (the long form must not disagree with the sentinel).
const defaultBindingBlock = "binding:\n" +
	"  artifact_class: definition\n" +
	"  compatibility_status: \"backward-compatible\"\n" +
	"  adoptable: true\n" +
	"  runtime_pinnable: true\n"

// validWorkRequestV2 is a fully valid envelope/v2 work_request instance,
// carrying one attachment through the new attachments[] block (P4 wave A,
// spec 04-possession.md §7) and a declared `binding` (P5 D1/Q2).
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
` + defaultBindingBlock + `acceptance_criteria:
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

// TestVersionedCorpus_BindingUndeclaredIsLegal pins P-1's discipline for
// `binding` itself: absence is a live state ("undeclared"), never an error.
// The field must stay optional — making it required would reproduce the
// opt-in failure the spec forbids in the other direction, forcing a
// declaration nobody may have grounds to make yet.
func TestVersionedCorpus_BindingUndeclaredIsLegal(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	doc := strings.Replace(validWorkRequestV2, defaultBindingBlock, "", 1)
	violations, err := c.ValidateEnvelope("work_request", "envelope/v2", toInstance(t, doc))
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("a work_request carrying no `binding` at all (undeclared, P-1) was refused: %+v", violations)
	}
}

// TestVersionedCorpus_BindingAgrees pins every cell of P5 D1/Q2's `binding`
// shape: the bare `none` sentinel, the long form's four required leaves, and
// — per the plan's 2026-08-09 re-anchor's lesson about pinning every cell of
// a conditional, not only the reachable one — both directions of the
// compatibility_status:none -> unadoptable/unpinnable rule threat-model T2
// requires so the long form cannot disagree with the sentinel.
func TestVersionedCorpus_BindingAgrees(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	tests := []struct {
		name       string
		block      string
		wantRefuse bool
		why        string
	}{
		{
			"the bare none sentinel",
			"binding: \"none\"\n",
			false,
			"US-1: a producer declaring itself non-binding — the XW-axon-20260801-vet6 shape",
		},
		{
			"none compatibility with both flags false agrees",
			"binding:\n  artifact_class: review\n  compatibility_status: \"none\"\n  adoptable: false\n  runtime_pinnable: false\n",
			false,
			"the long form's none case must match the sentinel: unadoptable, unpinnable",
		},
		{
			"none compatibility with adoptable true disagrees",
			"binding:\n  artifact_class: review\n  compatibility_status: \"none\"\n  adoptable: true\n  runtime_pinnable: false\n",
			true,
			"T2: compatibility_status:none must be as unadoptable as the bare sentinel — this is the escape hatch T2 names",
		},
		{
			"none compatibility with runtime_pinnable true disagrees",
			"binding:\n  artifact_class: review\n  compatibility_status: \"none\"\n  adoptable: false\n  runtime_pinnable: true\n",
			true,
			"same asymmetry, the other flag",
		},
		{
			"a real compatibility claim with both flags true agrees",
			defaultBindingBlock,
			false,
			"the conditional only fires on compatibility_status:none; a real claim leaves both flags free",
		},
		{
			"a real compatibility claim with both flags false also agrees",
			"binding:\n  artifact_class: definition\n  compatibility_status: \"backward-compatible\"\n  adoptable: false\n  runtime_pinnable: false\n",
			false,
			"an artifact may decline adoption for its own reasons while still making a real compatibility claim — the conditional is one-directional",
		},
		{
			"missing a required leaf is refused",
			"binding:\n  compatibility_status: \"backward-compatible\"\n  adoptable: true\n  runtime_pinnable: true\n",
			true,
			"the long form requires all four leaves; artifact_class is missing here",
		},
		{
			"an unknown key is refused",
			"binding:\n  artifact_class: definition\n  compatibility_status: \"backward-compatible\"\n  adoptable: true\n  runtime_pinnable: true\n  extra: nope\n",
			true,
			"additionalProperties: false closes the long form",
		},
		{
			"a string other than none matches neither shape",
			"binding: \"sometimes\"\n",
			true,
			"the sentinel is exactly \"none\"; anything else must take the long form",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := strings.Replace(validWorkRequestV2, defaultBindingBlock, tt.block, 1)
			violations, err := c.ValidateEnvelope("work_request", "envelope/v2", toInstance(t, doc))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if tt.wantRefuse && len(violations) == 0 {
				t.Errorf("%s: accepted, want refused — %s", tt.name, tt.why)
			}
			if !tt.wantRefuse && len(violations) > 0 {
				t.Errorf("%s: refused (%+v), want accepted — %s", tt.name, violations, tt.why)
			}
		})
	}
}

// responseV2Header is the base frontmatter every envelope/v2 response case
// below shares: everything the base schema requires plus `parent`, which is
// response's own required field alongside `result`. Each test appends
// `result:` plus whichever of P6's incompleteness fields the case needs.
const responseV2Header = `
schema: envelope/v2
id: XS-axon-20260808-p9d3
type: response
title: A valid v2 response
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260808-k3f9
actor: {kind: agent, name: codex}
created: "2026-08-08T08:40:00Z"
priority: p3
blocking: true
classification: internal
parent: XW-axon-20260808-p9d3
`

// validResponseV2 exercises every P6 field at once — the response half of
// the live XS-seomatrix-20260801-6vr2 case, reconstructed per the plan's
// 2026-08-08 amendment (the original bytes are not in this repo), plus
// `residue` and `standing` so a single fixture proves the whole shape
// resolves and validates with zero violations.
const validResponseV2 = responseV2Header + `result: partial
unmet: [0]
blocked_by:
  reason_code: out-of-scope
  owner: seomatrix
  needs: bytes
attempted: "Replayed 0 of 1 fixtures; reported, not silent."
standing: authoritative
residue:
  - criterion_index: 0
    carried_to: XS-axon-20260809-q7d2
`

// TestVersionedCorpus_ResponseV2Resolves proves the mutation the brief
// names: remove the corpusDefinitions row for (envelope, v2, response) and
// this test must fail, naming response as the type that should resolve and
// does not.
func TestVersionedCorpus_ResponseV2Resolves(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	violations, err := c.ValidateEnvelope("response", "envelope/v2", toInstance(t, validResponseV2))
	if errors.Is(err, ErrUnknownType) {
		t.Fatalf("ValidateEnvelope(response, envelope/v2) error = %v, want response to RESOLVE (not ErrUnknownType) now that (envelope, v2, response) is registered", err)
	}
	if err != nil {
		t.Fatalf("ValidateEnvelope(response, envelope/v2): %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("a fully valid envelope/v2 response instance (unmet+blocked_by+attempted+standing+residue) produced violations: %+v", violations)
	}
}

// TestVersionedCorpus_ResponseV2IncompletenessRefusal pins D1's third
// clause — the refusal rule for `result: partial`/`cannot` — with ALL its
// cells, not only the one the live incident happened to exercise (the P4
// if/then lesson the brief calls out by name).
func TestVersionedCorpus_ResponseV2IncompletenessRefusal(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	tests := []struct {
		name       string
		body       string
		wantRefuse bool
		why        string
	}{
		{
			name:       "partial with none of unmet/blocked_by/standing is refused",
			body:       "result: partial\n",
			wantRefuse: true,
			why:        "the shape that existed before D1 — a partial that says nothing machine-readable",
		},
		{
			name: "partial naming unmet and blocked_by is accepted",
			body: "result: partial\n" +
				"unmet: [0]\n" +
				"blocked_by:\n  reason_code: out-of-scope\n  owner: seomatrix\n  needs: bytes\n",
			wantRefuse: false,
			why:        "AC1/US-1: the 6vr2 replay — what is unmet and what would unblock it",
		},
		{
			name:       "partial with only standing=authoritative is refused",
			body:       "result: partial\nstanding: authoritative\n",
			wantRefuse: true,
			why:        "authoritative is the default meaning — typing it supplies no caveat and must not satisfy the refusal it exists to close",
		},
		{
			name:       "partial with standing=provisional and empty unmet is accepted",
			body:       "result: partial\nstanding: provisional\nunmet: []\n",
			wantRefuse: false,
			why:        "AC6/D1: the p1pf replay — every criterion answered, the shortfall is standing, not completeness",
		},
		{
			name:       "cannot with none of unmet/blocked_by/standing is refused",
			body:       "result: cannot\n",
			wantRefuse: true,
			why:        "the if's enum names both partial and cannot — cannot must be refused too, not just accepted by construction of the condition",
		},
		{
			name:       "cannot with standing=advisory is accepted",
			body:       "result: cannot\nstanding: advisory\n",
			wantRefuse: false,
			why:        "the cannot branch of the same rule must also have a reachable, accepted cell — not just a reachable refusal",
		},
		{
			name:       "answered carries none of the fields and is accepted",
			body:       "result: answered\n",
			wantRefuse: false,
			why:        "the if's condition is false for answered/delivered — the then-branch must not apply at all",
		},
		{
			name:       "an unmet entry restated as prose instead of an index is refused",
			body:       "result: partial\nunmet: [\"criterion 1 is not warranted\"]\nblocked_by:\n  reason_code: out-of-scope\n  owner: seomatrix\n  needs: bytes\n",
			wantRefuse: true,
			why:        "AC4: unmet is identified by INDEX into the parent's acceptance_criteria, never by restating the criterion in free text — drift must be impossible by construction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEnvelope("response", "envelope/v2", toInstance(t, responseV2Header+tt.body))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if tt.wantRefuse && len(violations) == 0 {
				t.Errorf("%s: accepted, want refused — %s", tt.name, tt.why)
			}
			if !tt.wantRefuse && len(violations) > 0 {
				t.Errorf("%s: refused (%+v), want accepted — %s", tt.name, violations, tt.why)
			}
		})
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
