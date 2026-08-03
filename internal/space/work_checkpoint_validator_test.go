package space

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

const workValidatorArtifact = `---
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

type recordingWorkCheckpointValidator struct {
	calls  int
	phases []bool
}

func (v *recordingWorkCheckpointValidator) ValidateWorkCheckpoint(_ context.Context, candidate WorkCheckpointValidation) error {
	v.calls++
	v.phases = append(v.phases, candidate.ProducerStampPending)
	return nil
}

func TestWorkCheckpointRepositoryFactsUseFirstParentOrder(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	dir := fx.Clone("axon")
	writeCommittedWorkCheckpoint(t, dir, "XA-axon-20260803-c3d4", 99, "older")
	writeCommittedWorkCheckpoint(t, dir, "XA-axon-20260803-d4e5", 2, "newer")
	contractTestGit(t, dir, "checkout", "-q", "-b", "local-only")
	manifestPath := filepath.Join(dir, "space.yaml")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw = append(manifestRaw, []byte("\n  - system: local-only\n    org: fixture\n    section: local-only\n    owners: [local]\n    status: active\n    joined: 2026-08-03\n")...)
	if err := os.WriteFile(manifestPath, manifestRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	contractTestGit(t, dir, "add", "--", "space.yaml")
	contractTestGit(t, dir, "commit", "-q", "-m", "local-only manifest")
	facts, err := NewWorkCheckpointRepositoryFacts(dir, "axon")
	if err != nil {
		t.Fatal(err)
	}
	got, err := facts.ResolveWorkCheckpointFacts(t.Context(), WorkCheckpointFactQuery{WorkID: "work:01K20ABCDEFHJKMNPQRSTVWXYZ", SemanticSequence: 3, SubjectRef: "thread:axon-20260803-a2b3"})
	if err != nil || got.Previous == nil || got.Previous.SemanticSequence != 2 {
		t.Fatalf("facts=%+v err=%v", got, err)
	}
	if fmt.Sprint(got.Previous.Recipients) != "[beta]" || got.Previous.Classification != "internal" {
		t.Fatalf("previous audience facts=%v/%q", got.Previous.Recipients, got.Previous.Classification)
	}
	for _, system := range got.HistoricalSystems {
		if system == "local-only" {
			t.Fatal("historical manifest facts followed local HEAD instead of origin/main")
		}
	}
}

func writeCommittedWorkCheckpoint(t *testing.T, dir, id string, sequence uint64, message string) {
	t.Helper()
	raw := []byte(strings.Replace(workValidatorArtifact, "XA-axon-20260803-b3c4", id, 1))
	raw = []byte(strings.Replace(string(raw), "semantic_sequence: 1", fmt.Sprintf("semantic_sequence: %d", sequence), 1))
	path := filepath.Join(dir, "axon", "exchanges", id+".md")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "--", filepath.ToSlash(filepath.Join("axon", "exchanges", id+".md"))}, {"commit", "-m", message}, {"push", "origin", "HEAD:main"}} {
		cmd := exec.Command("git", gitfixture.Args(args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a2a", "GIT_AUTHOR_EMAIL=a@invalid", "GIT_COMMITTER_NAME=a2a", "GIT_COMMITTER_EMAIL=a@invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestWorkSubmissionValidatorMountsBothSeams(t *testing.T) {
	t.Parallel()
	recorder := &recordingWorkCheckpointValidator{}
	validator, err := NewWorkSubmissionValidator(recorder, "project", "space")
	if err != nil {
		t.Fatal(err)
	}
	event := []byte("event: 01J5A1B2C3D4E5F6G7H8J9K0M1\nspace: fixture-space\nsubject: XA-axon-20260803-b3c4\n")
	files := []FileWrite{{Path: "x.md", Content: []byte(workValidatorArtifact)}, {Path: "x.yaml", Content: event}}
	if err := validator.ValidateSubmitCandidate(t.Context(), files); err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateSubmit(t.Context(), files); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 2 {
		t.Fatalf("calls=%d", recorder.calls)
	}
	if got := fmt.Sprint(recorder.phases); got != "[true false]" {
		t.Fatalf("producer-stamp phases = %s, want candidate pending then final", got)
	}
}
