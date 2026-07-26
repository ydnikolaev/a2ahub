package mcp

import (
	"testing"

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
