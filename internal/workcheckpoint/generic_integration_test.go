package workcheckpoint_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/workcheckpoint"
)

func TestGenericSubmitMountUsesCanonicalV2PolicyForWorkCandidates(t *testing.T) {
	t.Parallel()
	artifact, err := os.ReadFile("../../schemas/envelope/v2/fixtures/valid/XA-axon-work-planning.md")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := os.ReadFile("../../schemas/fixtures/secret-corpus/positive/github-pat.md")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatal(err)
	}
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "seomatrix", Status: "active"},
	}}
	mirror := t.TempDir()
	adapter := cli.NewSubmitValidatorAdapter(
		validate.New(corpus), "axon", cli.NewMirrorResolver(mirror, manifest),
		cli.NewLegalityAdapter(mirror, "axon", manifest),
	)
	generic, err := workcheckpoint.NewGenericSubmit(func(context.Context) (space.SubmitValidator, error) {
		return adapter, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	event := []byte("schema: event/v1\nevent: 01J5A1B2C3D4E5F6G7H8J9K0M1\nspace: checkout-core\nsubject: XA-axon-20260803-a2b3\ntransition: publish\nstate: published\nactor: {kind: agent, name: codex, system: axon}\nat: 2026-08-03T10:00:00Z\n")
	base := space.WorkCheckpointValidation{
		ArtifactPath: "axon/exchanges/XA-axon-20260803-a2b3.md",
		EventPath:    "axon/events/2026/01J5A1B2C3D4E5F6G7H8J9K0M1.yaml",
		Event:        event,
	}

	for _, test := range []struct {
		name string
		raw  []byte
		code string
	}{
		{name: "secret", raw: append(append([]byte(nil), artifact...), secret...), code: "POL-001"},
		{name: "unknown recipient", raw: []byte(strings.Replace(string(artifact), "to: [seomatrix]", "to: [intruder]", 1)), code: "REF-006"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := base
			candidate.Artifact = test.raw
			err := generic.ValidateGenericWorkCheckpoint(t.Context(), candidate)
			var violations *cli.ViolationError
			if !errors.As(err, &violations) || !hasViolationCode(violations.Violations, test.code) {
				t.Fatalf("canonical V2 error = %T %v; want %s", err, err, test.code)
			}
		})
	}
}

func hasViolationCode(violations []validate.Violation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}
