package cache

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// writeSuccessorDecisionArtifact seeds a committed `decision` under
// decisions/<id>.md, authored by axon, requiring approvals from every id
// in approvers — the SAME shape internal/cli's own (now-retired
// method-level) writeDecisionArtifact test helper used, moved here since
// the logic under test (SuccessorFacts) moved here.
func writeSuccessorDecisionArtifact(t *testing.T, dir, id string, approvers []string) {
	t.Helper()
	quoted := make([]string, len(approvers))
	copy(quoted, approvers)
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: decision\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + strings.Join(quoted, ", ") + "]\n" +
		"thread: XT-axon-20260827-thread\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-08-27T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"required_approvers: [" + strings.Join(quoted, ", ") + "]\n" +
		"---\nbody\n"
	writeSuccessorFile(t, dir, id+".md", content)
}

// writeSuccessorEvent seeds a pre-existing committed event under
// actingSystem's own section, at a caller-supplied sequence number — seq
// is minted as a REAL ULID at a fixed 2020 baseline (seq seconds apart),
// the SAME shape internal/cli's own writeLifecycleEvent test helper uses,
// so multiple events sort correctly relative to each other purely by
// ULID (this package's CommittedEventsAllSections never sets CommitSeq;
// fold.Fold's own sort falls all the way through to the ULID tiebreak).
func writeSuccessorEvent(t *testing.T, dir, actingSystem string, seq int, subject, transition, actorSystem string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("writeSuccessorEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:00:00Z\n",
		id.String(), subject, transition, actorSystem,
	)
	writeSuccessorFile(t, dir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
}

func writeSuccessorFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeSuccessorFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writeSuccessorFile: write %s: %v", full, err)
	}
}

func successorManifest(systems ...string) space.Manifest {
	m := space.Manifest{}
	for _, s := range systems {
		m.Participants = append(m.Participants, space.Participant{System: s, Status: fold.MembershipMember})
	}
	return m
}

// TestSuccessorFacts_ResolvesAuthorAndFoldedState is D7/D9's SOURCE half,
// driven directly against the moved function: given a committed decision
// artifact plus its own committed `propose` event, SuccessorFacts resolves
// its envelope `from` (author) and its current folded lifecycle state —
// the two facts internal/fold's own declared decision-supersede row
// preconditions check. Mirrors internal/cli's own
// TestMirrorResolverSuccessorResolvesAuthorAndFoldedState and
// internal/mcp's own identically-shaped test, both of which now exercise
// this same function through their own MirrorResolver.Successor
// delegation.
func TestSuccessorFacts_ResolvesAuthorAndFoldedState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const id = "XD-axon-20260827-s001"
	writeSuccessorDecisionArtifact(t, dir, id, []string{"beta"})
	writeSuccessorEvent(t, dir, "axon", 1, id, "propose", "axon")

	idx, _, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	manifest := successorManifest("axon", "beta")

	author, state, ok := SuccessorFacts(dir, idx, manifest, id)
	if !ok {
		t.Fatalf("SuccessorFacts(%q): ok = false, want true", id)
	}
	if author != "axon" {
		t.Fatalf("author = %q, want axon (writeSuccessorDecisionArtifact's own `from`)", author)
	}
	if state != "proposed" {
		t.Fatalf("state = %q, want proposed (one committed propose event)", state)
	}
}

// TestSuccessorFacts_UnknownIDDegrades pins the "cannot resolve"
// discipline AcceptanceCriteriaCount's own doc comment establishes for
// this package: never a synthesized author/state, always ok=false.
func TestSuccessorFacts_UnknownIDDegrades(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	idx, _, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}

	if _, _, ok := SuccessorFacts(dir, idx, space.Manifest{}, "XD-axon-unknown"); ok {
		t.Fatal("SuccessorFacts on an unindexed id: ok = true, want false")
	}
}

// TestSuccessorFacts_UnparseableIDDegrades covers the id-grammar guard
// (internal/artifact.ParseID) — an index entry whose key does not
// round-trip the §3.3 id grammar must still degrade to ok=false, never a
// synthesized author/state built from an id this resolver could not
// validate.
func TestSuccessorFacts_UnparseableIDDegrades(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const badID = "not-a-real-id"
	writeSuccessorFile(t, dir, "decisions/bad.md",
		"---\nschema: envelope/v1\nid: "+badID+"\ntype: decision\nfrom: axon\nrequired_approvers: [beta]\n---\nbody\n")

	idx, _, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	if _, ok := idx[badID]; !ok {
		t.Fatalf("test setup: %q not present in index (BuildArtifactIndex may have skipped it)", badID)
	}

	if _, _, ok := SuccessorFacts(dir, idx, space.Manifest{}, badID); ok {
		t.Fatalf("SuccessorFacts(%q): ok = true, want false (id fails artifact.ParseID)", badID)
	}
}

// TestSuccessorFacts_ResolvesApprovedAcrossSections is D-1+D-2's own
// proof: a successor decision carrying a REAL `required_approvers` list
// and a FULL quorum of `approve` events resolves as `approved` through
// SuccessorFacts — even though every approve event is committed under the
// APPROVING participant's OWN section (beta's, gamma's), never the
// successor id's own home system's section (axon's). Mirrors
// internal/cli's own
// TestMirrorResolverSuccessorResolvesApprovedAcrossSections.
func TestSuccessorFacts_ResolvesApprovedAcrossSections(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const id = "XD-axon-20260827-q001"
	writeSuccessorDecisionArtifact(t, dir, id, []string{"beta", "gamma"})
	writeSuccessorEvent(t, dir, "axon", 0, id, "propose", "axon")
	// Both approve events land under the APPROVING participant's OWN
	// section, never axon's (the successor id's own home system) — the
	// exact D-2 shape a single-section read cannot see.
	writeSuccessorEvent(t, dir, "beta", 1, id, "approve", "beta")
	writeSuccessorEvent(t, dir, "gamma", 2, id, "approve", "gamma")

	idx, _, err := BuildArtifactIndex(dir)
	if err != nil {
		t.Fatalf("BuildArtifactIndex: %v", err)
	}
	manifest := successorManifest("axon", "beta", "gamma")

	author, state, ok := SuccessorFacts(dir, idx, manifest, id)
	if !ok {
		t.Fatalf("SuccessorFacts(%q): ok = false, want true", id)
	}
	if author != "axon" {
		t.Fatalf("author = %q, want axon (writeSuccessorDecisionArtifact's own `from`)", author)
	}
	if state != "approved" {
		t.Fatalf("state = %q, want approved — D-1: RequiredApprovers must reach the folded envelope; "+
			"D-2: both approve events must resolve despite living under OTHER participants' own sections", state)
	}
}
