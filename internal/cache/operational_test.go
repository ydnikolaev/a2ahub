package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

func TestDecodeOperationalCheckpointReadsOnlyV2StatusWork(t *testing.T) {
	t.Parallel()
	raw := []byte(`---
schema: envelope/v2
id: XA-atlas-20260803-abcd
type: announcement
title: Testing ingest
space: checkout-core
from: atlas
to: [checkout]
actor: {kind: agent, name: codex, model: gpt-5, session: session:01K20ABCDEFHJKMNPQRSTVWXYZ}
created: 2026-08-03T10:30:00Z
category: status
priority: p2
blocking: false
ack_requested: false
classification: internal
thread: thread:atlas-20260803-abcd
refs: []
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 3
  mode: waiting
  subject_ref: XW-atlas-20260728-c3d4
  summary: Waiting for checkout fixtures
  reported_at: 2026-08-03T10:30:00Z
  valid_until: 2026-08-04T10:30:00Z
  waiting_on:
    - {kind: system, id: checkout, summary: Revised fixtures}
---

Waiting for checkout fixtures.
`)
	member := foldedArtifact{
		SpaceID: "checkout-core", Raw: raw, Seq: 6, OrderKnown: true,
		Env: envelopeProbe{Thread: "thread:atlas-20260803-abcd"},
	}

	checkpoint, ok, err := decodeOperationalCheckpoint(member)
	if err != nil || !ok {
		t.Fatal("valid work checkpoint was not decoded")
	}
	if checkpoint.WorkID != "work:01K20ABCDEFHJKMNPQRSTVWXYZ" || checkpoint.CommitSequence != 7 ||
		checkpoint.Actor.System != "atlas" || checkpoint.Actor.Session == "" ||
		checkpoint.Mode != workreport.ModeWaiting || len(checkpoint.WaitingOn) != 1 {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}

	invalid := []byte(strings.Replace(string(raw), "semantic_sequence: 3", "semantic_sequence: 0", 1))
	member.Raw = invalid
	if _, ok, err := decodeOperationalCheckpoint(member); !ok || !errors.Is(err, errInvalidOperationalCheckpoint) {
		t.Fatalf("invalid checkpoint = ok %v, err %v; want classified invalid checkpoint", ok, err)
	}
	member.Raw = raw
	member.Seq = 0
	member.OrderKnown = false
	if _, ok, err := decodeOperationalCheckpoint(member); !ok || !errors.Is(err, errInvalidOperationalCheckpoint) {
		t.Fatalf("unknown commit order = ok %v, err %v; want explicit degradation", ok, err)
	}
}

func TestInvalidRecognizedCheckpointRemainsOutsideProtocolTruth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	const checkpointID = "XA-atlas-20260803-invalid"
	member := foldedArtifact{
		Env: envelopeProbe{ID: checkpointID, Thread: "thread:atlas-20260803-abcd"},
		Raw: []byte(`---
schema: envelope/v2
id: XA-atlas-20260803-invalid
type: announcement
space: checkout-core
from: atlas
thread: thread:atlas-20260803-abcd
category: status
actor: {kind: agent, name: codex, session: session:01K20ABCDEFHJKMNPQRSTVWXYZ}
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 0
  mode: testing
  subject_ref: XW-atlas-20260728-c3d4
  summary: invalid sequence
  reported_at: 2026-08-03T10:30:00Z
  valid_until: 2026-08-03T10:45:00Z
  waiting_on: []
---
`),
		Seq: 2, OrderKnown: true,
	}
	checkpoint, recognized, err := classifyOperationalCheckpoint(member)
	if !recognized || !errors.Is(err, errInvalidOperationalCheckpoint) || checkpoint.ArtifactID != checkpointID {
		t.Fatalf("classification = %#v, recognized=%v, err=%v", checkpoint, recognized, err)
	}
	view := ThreadResult{
		Space: "checkout-core", Thread: member.Env.Thread, Opener: ThreadOpener{Title: "Process"},
		OpenItems: []OpenItem{
			{ID: "XW-atlas", WaitingOn: []string{"atlas"}, YourMove: true},
			{ID: checkpointID, WaitingOn: []string{"checkout"}, Blocking: true, YourMove: true},
		},
		Transcript: []TranscriptEntry{
			{Kind: "event", At: now.Add(-time.Minute), Event: &TranscriptEvent{Subject: "XW-atlas", Transition: "accept", Actor: TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ"}}},
			{Kind: "event", At: now, Event: &TranscriptEvent{Subject: checkpointID, Transition: "publish", Actor: TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ"}}},
		},
	}
	thread := operationalThreadFromView(view, map[string]struct{}{checkpoint.ArtifactID: {}})
	if thread.OpenCount != 1 || len(thread.WaitingOn) != 1 || thread.WaitingOn[0] != "atlas" || thread.LatestMilestone == nil || thread.LatestMilestone.Subject != "XW-atlas" {
		t.Fatalf("degraded checkpoint polluted protocol/milestone: %#v", thread)
	}
}

func TestOperationalEvidenceNormalizesFirstGitCommitCheckpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fxRunGit(t, dir, "init", "-b", "main")
	raw := `---
schema: envelope/v2
id: XA-atlas-20260803-first
type: announcement
title: First committed checkpoint
space: checkout-core
from: atlas
to: [checkout]
actor: {kind: agent, name: codex, model: gpt-5, session: session:01K20ABCDEFHJKMNPQRSTVWXYZ}
created: 2026-08-03T10:30:00Z
category: status
priority: p2
blocking: false
ack_requested: false
classification: internal
thread: thread:atlas-20260803-first
refs: []
work:
  id: work:01K20ABCDEFHJKMNPQRSTVWXYZ
  semantic_sequence: 1
  mode: implementing
  subject_ref: XW-atlas-20260728-c3d4
  summary: Implementing ingest
  reported_at: 2026-08-03T10:30:00Z
  valid_until: 2026-08-04T10:30:00Z
  waiting_on: []
---

Implementing ingest.
`
	path := filepath.Join(dir, "atlas", "exchanges", "XA-atlas-20260803-first.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	fxCommit(t, dir, "fixture: first checkpoint")
	store := NewStore("atlas", t.TempDir(), []SpaceMirror{{
		SpaceID: "checkout-core", Dir: dir,
		Manifest: space.Manifest{Space: "checkout-core", Participants: []space.Participant{
			{System: "atlas", Section: "atlas/", Status: "active"},
			{System: "checkout", Section: "checkout/", Status: "active"},
		}},
	}}, time.Now, 0)

	evidence, err := store.OperationalEvidence(context.Background())
	if err != nil {
		t.Fatalf("OperationalEvidence() error = %v", err)
	}
	if len(evidence.CommittedWork) != 1 || evidence.CommittedWork[0].CommitSequence != 1 {
		t.Fatalf("first Git commit checkpoint = %#v; want normalized sequence 1", evidence.CommittedWork)
	}
}

func TestOperationalThreadFromViewKeepsProtocolSeparateFromMilestone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	view := ThreadResult{
		Space: "checkout-core", Thread: "thread:checkout-20260803-abcd",
		Opener:       ThreadOpener{Title: "Prevent duplicate capture"},
		Participants: []string{"atlas", "checkout"},
		OpenItems: []OpenItem{
			{ID: "XW-checkout", Blocking: true, WaitingOn: []string{"atlas"}, YourMove: true},
			{ID: "XQ-checkout", WaitingOn: []string{"checkout"}},
		},
		Transcript: []TranscriptEntry{
			{Kind: "event", At: now.Add(-time.Minute), Event: &TranscriptEvent{
				ULID: "01K20ABCDEFHJKMNPQRSTVWXYZ", Subject: "XW-checkout", Transition: "claim",
				Actor: TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas"},
			}},
			{Kind: "event", At: now, Event: &TranscriptEvent{
				ULID: "01K20ABCDEFHJKMNPQRSTVWXYA", Subject: "XW-checkout", Transition: "respond",
				Actor: TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ"},
			}},
		},
	}

	thread := operationalThreadFromView(view, nil)
	if thread.Settled || thread.OpenCount != 2 || !thread.YourMove {
		t.Fatalf("protocol = %#v", thread)
	}
	if len(thread.WaitingOn) != 2 || len(thread.BlockingBy) != 1 || thread.BlockingBy[0] != "atlas" {
		t.Fatalf("waiting/blocking = %#v / %#v", thread.WaitingOn, thread.BlockingBy)
	}
	if thread.LatestMilestone == nil || thread.LatestMilestone.Transition != "respond" {
		t.Fatalf("latest complete milestone = %#v", thread.LatestMilestone)
	}
}

func TestOperationalThreadFromViewDoesNotInventEscapeHatchObligation(t *testing.T) {
	t.Parallel()
	view := ThreadResult{
		Space: "getvisa", Thread: "thread:axon-20260728-163w",
		Opener:       ThreadOpener{Title: "Welcome, Seomatrix!"},
		Participants: []string{"axon", "seomatrix"},
		OpenItems: []OpenItem{{
			ID: "XA-axon-20260728-343t", Type: "announcement", State: "published",
			NextActions: []NextAction{{Transition: "supersede", By: []string{"axon"}}},
			WaitingOn: []string{}, YourMove: false,
		}},
	}

	thread := operationalThreadFromView(view, nil)
	if !thread.Settled || thread.OpenCount != 0 || len(thread.WaitingOn) != 0 || thread.YourMove || len(thread.BlockingBy) != 0 {
		t.Fatalf("escape-hatch-only member invented a protocol obligation: %#v", thread)
	}
}

func TestOperationalThreadFromViewSkipsStatusCheckpointPublishMilestone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	const checkpointID = "XA-atlas-20260803-status"
	view := ThreadResult{
		Space: "checkout-core", Thread: "thread:checkout-20260803-abcd",
		Opener: ThreadOpener{Title: "Prevent duplicate capture"},
		OpenItems: []OpenItem{
			{ID: "XW-checkout", Blocking: true, WaitingOn: []string{"atlas"}, YourMove: true},
			{ID: checkpointID, Blocking: true, WaitingOn: []string{"checkout"}, YourMove: true},
		},
		Transcript: []TranscriptEntry{
			{Kind: "event", At: now.Add(-time.Minute), Event: &TranscriptEvent{
				ULID: "01K20ABCDEFHJKMNPQRSTVWXYZ", Subject: "XW-checkout", Transition: "accept",
				Actor: TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ"},
			}},
			{Kind: "event", At: now, Event: &TranscriptEvent{
				ULID: "01K20ABCDEFHJKMNPQRSTVWXYA", Subject: checkpointID, Transition: "publish",
				Actor: TranscriptEventActor{Kind: "agent", Name: "codex", System: "atlas", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYZ"},
			}},
		},
	}

	thread := operationalThreadFromView(view, map[string]struct{}{checkpointID: {}})
	if thread.LatestMilestone == nil || thread.LatestMilestone.Transition != "accept" || thread.LatestMilestone.Subject != "XW-checkout" {
		t.Fatalf("latest meaningful milestone = %#v", thread.LatestMilestone)
	}
	if thread.OpenCount != 1 || thread.Settled || len(thread.WaitingOn) != 1 || thread.WaitingOn[0] != "atlas" || len(thread.BlockingBy) != 1 || thread.BlockingBy[0] != "atlas" {
		t.Fatalf("status checkpoint polluted protocol truth: %#v", thread)
	}
}
