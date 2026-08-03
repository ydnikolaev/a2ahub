package workcheckpoint

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

type fixedFacts struct{ value space.WorkCheckpointFacts }

func (f fixedFacts) ResolveWorkCheckpointFacts(context.Context, space.WorkCheckpointFactQuery) (space.WorkCheckpointFacts, error) {
	return f.value, nil
}

type acceptingGeneric struct{}

func (acceptingGeneric) ValidateGenericWorkCheckpoint(context.Context, space.WorkCheckpointValidation) error {
	return nil
}

type recordingGeneric struct{ calls int }

func (g *recordingGeneric) ValidateGenericWorkCheckpoint(context.Context, space.WorkCheckpointValidation) error {
	g.calls++
	return nil
}

const validArtifact = `---
schema: envelope/v2
id: XA-axon-20260803-b3c4
type: announcement
title: Work checkpoint
space: fixture-space
from: axon
to: [beta]
actor: {kind: agent, name: codex, session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ"}
created: 2026-08-03T10:15:00Z
category: status
priority: p2
blocking: false
ack_requested: false
classification: internal
thread: thread:axon-20260803-a2b3
work: {id: "work:01K20ABCDEFHJKMNPQRSTVWXYZ", semantic_sequence: 1, mode: implementing, subject_ref: "thread:axon-20260803-a2b3", summary: Work, reported_at: "2026-08-03T10:15:00Z", valid_until: "2026-08-04T10:15:00Z"}
---
Work.
`
const validEvent = "schema: event/v1\nevent: 01J5A1B2C3D4E5F6G7H8J9K0M1\nspace: fixture-space\nsubject: XA-axon-20260803-b3c4\ntransition: publish\nstate: published\nactor: {kind: agent, name: codex, system: axon, session: \"session:01K20ABCDEFHJKMNPQRSTVWXYZ\"}\nat: 2026-08-03T10:15:00Z\nproduced_by:\n  tool: a2a\n  version: \"0.19.0\"\n"
const manifestRaw = "schema: space/v1\nspace: fixture-space\nmin_binary_version: 0.19.0\nparticipants:\n  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n  - system: beta\n    org: fixture\n    section: beta\n    owners: [beta-bot]\n    status: active\n    joined: \"2026-01-01\"\n"

func TestValidatorUsesCanonicalEngineAndRetainsCodes(t *testing.T) {
	t.Parallel()
	manifest, err := space.ParseManifest([]byte(manifestRaw))
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	validator, err := New(validate.New(corpus), fixedFacts{space.WorkCheckpointFacts{ManifestRaw: []byte(manifestRaw), Manifest: manifest, HistoricalSystems: []string{"axon", "beta"}}}, acceptingGeneric{}, "axon")
	if err != nil {
		t.Fatal(err)
	}
	candidate := space.WorkCheckpointValidation{Space: "fixture-space", ArtifactID: "XA-axon-20260803-b3c4", EventID: "01J5A1B2C3D4E5F6G7H8J9K0M1", ArtifactPath: "axon/exchanges/XA-axon-20260803-b3c4.md", EventPath: "axon/events/2026/01J5A1B2C3D4E5F6G7H8J9K0M1.yaml", Artifact: []byte(validArtifact), Event: []byte(validEvent)}
	if err := validator.ValidateWorkCheckpoint(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Artifact = []byte(strings.Replace(validArtifact, "semantic_sequence: 1", "semantic_sequence: 2", 1))
	err = validator.ValidateWorkCheckpoint(t.Context(), candidate)
	var violations *ViolationError
	if !errors.As(err, &violations) || len(violations.Violations) == 0 || violations.Violations[0].Code != "POL-015" {
		t.Fatalf("error=%v", err)
	}
}

func TestValidatorProducerStampPendingExemptsOnlyAbsentStamp(t *testing.T) {
	t.Parallel()
	manifest, err := space.ParseManifest([]byte(manifestRaw))
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	generic := &recordingGeneric{}
	validator, err := New(validate.New(corpus), fixedFacts{space.WorkCheckpointFacts{
		ManifestRaw: []byte(manifestRaw), Manifest: manifest, HistoricalSystems: []string{"axon", "beta"},
	}}, generic, "axon")
	if err != nil {
		t.Fatal(err)
	}
	withoutStamp := strings.Replace(validEvent, "produced_by:\n  tool: a2a\n  version: \"0.19.0\"\n", "", 1)
	candidate := space.WorkCheckpointValidation{
		ProducerStampPending: true, Space: "fixture-space",
		ArtifactID: "XA-axon-20260803-b3c4", EventID: "01J5A1B2C3D4E5F6G7H8J9K0M1",
		ArtifactPath: "axon/exchanges/XA-axon-20260803-b3c4.md", EventPath: "axon/events/2026/01J5A1B2C3D4E5F6G7H8J9K0M1.yaml",
		Artifact: []byte(validArtifact), Event: []byte(withoutStamp),
	}
	if err := validator.ValidateWorkCheckpoint(t.Context(), candidate); err != nil {
		t.Fatalf("pre-stamp candidate rejected: %v", err)
	}
	if generic.calls != 0 {
		t.Fatalf("pre-stamp candidate crossed generic final validation %d times", generic.calls)
	}

	final := candidate
	final.ProducerStampPending = false
	assertWorkViolationCode(t, validator.ValidateWorkCheckpoint(t.Context(), final), "POL-012")
	if generic.calls != 1 {
		t.Fatalf("final missing-stamp candidate generic calls = %d, want 1", generic.calls)
	}
	v3 := candidate
	v3.InvocationPoint = string(validate.V3)
	assertWorkViolationCode(t, validator.ValidateWorkCheckpoint(t.Context(), v3), "POL-012")

	malformed := candidate
	malformed.Event = []byte(strings.Replace(validEvent, "  version: \"0.19.0\"\n", "", 1))
	assertWorkViolationCode(t, validator.ValidateWorkCheckpoint(t.Context(), malformed), "POL-012")
	if generic.calls != 2 {
		t.Fatalf("malformed present stamp generic calls = %d, want 2", generic.calls)
	}

	invalid := candidate
	invalid.Event = []byte(strings.Replace(withoutStamp, "transition: publish", "transition: impossible", 1))
	if err := validator.ValidateWorkCheckpoint(t.Context(), invalid); err == nil {
		t.Fatal("pre-stamp candidate suppressed a non-producer violation")
	}
}

func assertWorkViolationCode(t *testing.T, err error, code string) {
	t.Helper()
	var violations *ViolationError
	if !errors.As(err, &violations) {
		t.Fatalf("error = %v, want coded work violation", err)
	}
	for _, violation := range violations.Violations {
		if violation.Code == code {
			return
		}
	}
	t.Fatalf("violations = %+v, want %s", violations.Violations, code)
}

func TestValidatorRejectsV2V3ContinuationAudienceOrClassificationChange(t *testing.T) {
	t.Parallel()
	manifest, err := space.ParseManifest([]byte(manifestRaw))
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	facts := fixedFacts{space.WorkCheckpointFacts{
		ManifestRaw: []byte(manifestRaw), Manifest: manifest, HistoricalSystems: []string{"axon", "beta"},
		Previous: &space.WorkCheckpointPreviousFact{
			WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYZ", SemanticSequence: 1,
			Space: "fixture-space", Thread: "thread:axon-20260803-a2b3", System: "axon", Name: "codex",
			Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ", Mode: "planning",
			Recipients: []string{"beta"}, Classification: "internal",
		},
	}}
	validator, err := New(validate.New(corpus), facts, acceptingGeneric{}, "axon")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.Replace(validArtifact, "semantic_sequence: 1", "semantic_sequence: 2", 1)
	for _, test := range []struct {
		name, from, to, path string
	}{
		{name: "audience", from: "to: [beta]", to: "to: all", path: "to"},
		{name: "classification", from: "classification: internal", to: "classification: restricted", path: "classification"},
	} {
		for _, point := range []validate.InvocationPoint{validate.V2, validate.V3} {
			t.Run(test.name+"/"+string(point), func(t *testing.T) {
				t.Parallel()
				candidate := space.WorkCheckpointValidation{
					InvocationPoint: string(point), Space: "fixture-space", ArtifactID: "XA-axon-20260803-b3c4", EventID: "01J5A1B2C3D4E5F6G7H8J9K0M1",
					ArtifactPath: "axon/exchanges/XA-axon-20260803-b3c4.md", EventPath: "axon/events/2026/01J5A1B2C3D4E5F6G7H8J9K0M1.yaml",
					Artifact: []byte(strings.Replace(base, test.from, test.to, 1)), Event: []byte(validEvent),
				}
				err := validator.ValidateWorkCheckpoint(t.Context(), candidate)
				var violations *ViolationError
				if !errors.As(err, &violations) || len(violations.Violations) != 1 || violations.Violations[0].Code != "POL-015" || violations.Violations[0].Path != test.path {
					t.Fatalf("error=%v violations=%+v", err, violations)
				}
			})
		}
	}
}
