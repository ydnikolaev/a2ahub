package schema

import (
	"errors"
	"path/filepath"
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

func TestVersionedCorpus_UnregisteredEnvelopeV2TypeRefused(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	_, err := c.ValidateEnvelope("requirement", "envelope/v2", toInstance(t, `
schema: envelope/v2
type: requirement
`))
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("ValidateEnvelope(requirement, envelope/v2) error = %v, want ErrUnknownType", err)
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
