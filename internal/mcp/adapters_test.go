package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// This assertion pins the ThreadResolver capability at COMPILE time on this
// surface. It is what would have caught the gap the CLI side closed one wave
// earlier: the two surfaces carry independent MirrorResolver types (ADR-001 —
// internal/mcp may never import internal/cli), so a capability added to one is
// invisible to the other, and REF-009/REF-010 then fail OPEN on whichever one
// was missed.
//
// It has to be a compile-time assertion rather than a behavioural test,
// because a fail-open guard returns no violation and so does a clean
// document — at runtime the two are indistinguishable, which is precisely what
// makes this class of gap survive a green test suite.
var _ validate.ThreadResolver = (*MirrorResolver)(nil)

// TestMirrorResolverThreadOfAndThreadExists covers the pair behaviourally: the
// index carries the thread, an unknown id is not found, and an empty thread is
// never reported as "carried" (which would otherwise make every threadless
// artifact answer true).
func TestMirrorResolverThreadOfAndThreadExists(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const id = "XQ-axon-20260721-t001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")

	r := NewMirrorResolver(mirrorDir, testManifest())

	got, found := r.ThreadOf(id)
	if !found {
		t.Fatalf("ThreadOf(%q): found = false, want true", id)
	}
	if got != testFixtureThread {
		t.Fatalf("ThreadOf(%q) = %q, want %q", id, got, testFixtureThread)
	}

	if _, found := r.ThreadOf("XQ-axon-20260721-zzzz"); found {
		t.Fatal("ThreadOf on an unknown id: found = true, want false")
	}

	if !r.ThreadExists(testFixtureThread) {
		t.Fatalf("ThreadExists(%q) = false, want true", testFixtureThread)
	}
	if r.ThreadExists("thread:axon-20260721-nope") {
		t.Fatal("ThreadExists on an unseen thread = true, want false")
	}
	if r.ThreadExists("") {
		t.Fatal(`ThreadExists("") = true — an empty thread is carried by nothing`)
	}
}

// TestMirrorResolverAdapterCarriesNoWalk is AC-1.3's structural gate for
// this surface: this file must never regain its own filepath.WalkDir — the
// resolver's index build lives in internal/cache.BuildArtifactIndex now
// (spec 01-resolver-one-home.md), and this package still never imports
// internal/cli, so re-adding a walk here would resurrect the third,
// worse, unreported copy this phase deleted rather than sharing
// internal/cli's.
func TestMirrorResolverAdapterCarriesNoWalk(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	if strings.Contains(string(raw), "filepath.WalkDir") {
		t.Fatal("internal/mcp/adapters.go calls filepath.WalkDir directly — the resolver's own artifact walk must live in internal/cache only (AC-1.3)")
	}
}

// TestMirrorResolverSkippedNamesTheBadFileAndGoodRefStillResolves mirrors
// internal/cli's own test of the same name (adapters_test.go) — AC-1.1/
// AC-1.2's core proof at the resolver layer, independently on this
// surface since internal/mcp carries its own MirrorResolver.
func TestMirrorResolverSkippedNamesTheBadFileAndGoodRefStillResolves(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeQuestionArtifact(t, mirrorDir, "XQ-axon-20260721-good1", "beta")

	badRelPath := "beta/exchanges/XW-beta-20260721-bad.md"
	writeMirrorFile(t, mirrorDir, badRelPath,
		"---\nid: XW-beta-20260721-bad\nthread: thread:beta:one\nthread: thread:beta:two\n---\nbad body\n")

	r := NewMirrorResolver(mirrorDir, testManifest())

	if !r.KnownArtifact("XQ-axon-20260721-good1") {
		t.Fatal("KnownArtifact(good) = false, want true — one bad file elsewhere must never blind the resolver to a real artifact")
	}

	skipped := r.Skipped()
	if len(skipped) != 1 || skipped[0].Path != badRelPath || skipped[0].Reason != "undecodable-yaml" {
		t.Fatalf("Skipped() = %+v, want exactly one entry naming %q/undecodable-yaml", skipped, badRelPath)
	}
}

// TestSubmitValidatorAdapterViolationNamesSkippedFile is AC-1.2's proof at
// the ValidateSubmit layer on this surface: mirrors internal/cli's own
// test of the same name. Skipped rides ViolationError.Error()'s
// plain-text message here rather than a structured payload — see
// ViolationError's own doc comment (adapters.go) for why: a HandlerFunc's
// error branch (registry.go) has no structured-data channel.
func TestSubmitValidatorAdapterViolationNamesSkippedFile(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := testManifest()
	mirrorDir := t.TempDir()

	badRelPath := "beta/exchanges/XW-beta-20260721-bad.md"
	writeMirrorFile(t, mirrorDir, badRelPath,
		"---\nid: XW-beta-20260721-bad\nthread: thread:beta:one\nthread: thread:beta:two\n---\nbad body\n")

	legality := NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	// Missing required `category` field -> a schema-class violation. The
	// violation's own cause is irrelevant here; what matters is that
	// Skipped/Error surface the UNRELATED bad file regardless of which
	// violation fired.
	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/XQ-axon-20260721-k3f9.md", Content: artifactContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	var violationErr *ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *ViolationError, got %T: %v", err, err)
	}
	if len(violationErr.Skipped) != 1 || violationErr.Skipped[0].Path != badRelPath || violationErr.Skipped[0].Reason != "undecodable-yaml" {
		t.Fatalf("violationErr.Skipped = %+v, want exactly one entry naming %q/undecodable-yaml", violationErr.Skipped, badRelPath)
	}
	msg := violationErr.Error()
	if !strings.Contains(msg, badRelPath) {
		t.Fatalf("Error() = %q, want it to NAME the skipped path %q", msg, badRelPath)
	}
	if !strings.Contains(msg, "undecodable-yaml") {
		t.Fatalf("Error() = %q, want the skip reason named", msg)
	}
}
