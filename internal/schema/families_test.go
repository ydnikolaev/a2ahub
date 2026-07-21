package schema

import "testing"

func TestValidateEvent(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	valid := toInstance(t, `
schema: event/v1
event: 01J40A7M9P1S3V5W7Y9A1C3E5G
space: getvisa
subject: XW-axon-20260731-p9d3
transition: submit
actor: {kind: agent, name: codex, system: axon}
at: "2026-07-31T08:40:00Z"
`)
	violations, err := c.ValidateEvent("v1", valid)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected valid event, got %+v", violations)
	}

	invalid := toInstance(t, `
schema: event/v1
event: 01J40A7M9P1S3V5W7Y9A1C3E5G
space: getvisa
subject: XW-axon-20260731-p9d3
transition: teleport
actor: {kind: agent, name: codex, system: axon}
at: "2026-07-31T08:40:00Z"
`)
	violations, err = c.ValidateEvent("v1", invalid)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != "enum" {
		t.Fatalf("expected exactly one enum violation, got %+v", violations)
	}
}

func TestValidateManifest(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	valid := toInstance(t, `
schema: space/v1
space: getvisa
min_binary_version: 1.0.0
participants: []
`)
	violations, err := c.ValidateManifest("v1", valid)
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected valid manifest, got %+v", violations)
	}

	invalid := toInstance(t, `
schema: space/v1
space: getvisa
participants: []
`)
	violations, err = c.ValidateManifest("v1", invalid)
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != "required" {
		t.Fatalf("expected exactly one required violation, got %+v", violations)
	}
}

func TestValidateConsumes(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	valid := toInstance(t, `
schema: consumes/v1
system: axon
dependencies: []
`)
	violations, err := c.ValidateConsumes("v1", valid)
	if err != nil {
		t.Fatalf("ValidateConsumes: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected valid consumes, got %+v", violations)
	}

	invalid := toInstance(t, `
schema: consumes/v1
system: axon
dependencies:
  - contract: XC-axon-ingest
    major: "2"
    since: 2026-01-01
`)
	violations, err = c.ValidateConsumes("v1", invalid)
	if err != nil {
		t.Fatalf("ValidateConsumes: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != "type" {
		t.Fatalf("expected exactly one type violation, got %+v", violations)
	}
}

func TestBaseEnvelopeFields(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	fields := c.BaseEnvelopeFields()
	for _, want := range []string{"schema", "id", "type", "title", "space", "from", "to", "actor", "created", "priority", "blocking", "classification"} {
		if !fields[want] {
			t.Errorf("expected BaseEnvelopeFields to include %q", want)
		}
	}
	if fields["not_a_real_field"] {
		t.Error("expected BaseEnvelopeFields to NOT include a made-up field")
	}
}

func TestRegistryAccessors(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	reg := c.Registry()

	if !reg.Has("SCH-001") {
		t.Fatal("expected registry to have SCH-001")
	}
	if reg.Has("SCH-999") {
		t.Fatal("expected registry to NOT have SCH-999")
	}

	entry, ok := reg.Entry("SCH-001")
	if !ok || entry.Class != "schema" {
		t.Fatalf("Entry(SCH-001) = %+v, %v", entry, ok)
	}
	if _, ok := reg.Entry("NOPE-000"); ok {
		t.Fatal("expected Entry(NOPE-000) to report not found")
	}

	codes := reg.Codes()
	if len(codes) == 0 {
		t.Fatal("expected a non-empty code list")
	}
}

func TestVersionSeam_OtherFamilies(t *testing.T) {
	t.Parallel()
	if !AcceptsEventVersion(1) || AcceptsEventVersion(2) {
		t.Errorf("AcceptsEventVersion window is wrong")
	}
	if !AcceptsManifestVersion(1) || AcceptsManifestVersion(2) {
		t.Errorf("AcceptsManifestVersion window is wrong")
	}
	if !AcceptsConsumesVersion(1) || AcceptsConsumesVersion(2) {
		t.Errorf("AcceptsConsumesVersion window is wrong")
	}
}

func TestParseVersion_Invalid(t *testing.T) {
	t.Parallel()
	if _, ok := ParseVersion("not-a-version"); ok {
		t.Error("expected ParseVersion to reject a string with no v<N> suffix")
	}
	if _, ok := ParseVersion("envelope/v0"); ok {
		t.Error("expected ParseVersion to reject v0 (versions are 1-indexed; parsed < 1 is invalid)")
	}
}
