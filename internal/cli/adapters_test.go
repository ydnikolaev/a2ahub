package cli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// --- ResolveActor (§7.4 order) -------------------------------------------

func TestResolveActorOrderExplicitFlagWins(t *testing.T) {
	t.Parallel()
	a := cli.ResolveActor(
		cli.ActorFlags{Kind: "human", Name: "flag-name"},
		cli.HarnessDefaults{Kind: "agent", Name: "harness-name"},
		cli.ConfigActor{Kind: "agent", Name: "config-name"},
	)
	if a.Kind != "human" || a.Name != "flag-name" {
		t.Fatalf("got %+v, want explicit flag values to win", a)
	}
}

func TestResolveActorOrderEnvBeatsHarnessAndConfig(t *testing.T) {
	// reason: mutates process env; not parallel-safe against sibling tests
	// touching the same A2A_ACTOR_* variables.
	t.Setenv("A2A_ACTOR_NAME", "env-name")
	a := cli.ResolveActor(
		cli.ActorFlags{},
		cli.HarnessDefaults{Name: "harness-name"},
		cli.ConfigActor{Name: "config-name"},
	)
	if a.Name != "env-name" {
		t.Fatalf("Name = %q, want env-name", a.Name)
	}
}

func TestResolveActorOrderHarnessBeatsConfig(t *testing.T) {
	t.Parallel()
	a := cli.ResolveActor(cli.ActorFlags{}, cli.HarnessDefaults{Name: "harness-name"}, cli.ConfigActor{Name: "config-name"})
	if a.Name != "harness-name" {
		t.Fatalf("Name = %q, want harness-name", a.Name)
	}
}

func TestResolveActorOrderConfigFallback(t *testing.T) {
	t.Parallel()
	a := cli.ResolveActor(cli.ActorFlags{}, cli.HarnessDefaults{}, cli.ConfigActor{Name: "config-name"})
	if a.Name != "config-name" {
		t.Fatalf("Name = %q, want config-name", a.Name)
	}
}

func TestResolveActorDefaultsKindToAgent(t *testing.T) {
	t.Parallel()
	a := cli.ResolveActor(cli.ActorFlags{}, cli.HarnessDefaults{}, cli.ConfigActor{})
	if a.Kind != "agent" {
		t.Fatalf("Kind = %q, want agent (default when no source names one)", a.Kind)
	}
}

// --- LegalityAdapter -----------------------------------------------------

func TestLegalityAdapterFreshSubmitIsLegal(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir() // empty mirror: no committed history for anything

	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	a.RegisterEnvelope("XQ-axon-20260721-k3f9", fold.Envelope{
		ID: "XQ-axon-20260721-k3f9", Kind: fold.KindQuestion, From: "axon", To: []string{"other"},
	})

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: "XQ-axon-20260721-k3f9", Transition: fold.TSubmit,
		Actor: validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
	})
	if err != nil {
		t.Fatalf("CheckLegality: %v", err)
	}
	if verdict != validate.VerdictLegal {
		t.Fatalf("verdict = %v, want VerdictLegal (fresh subject, no committed history, entry transition from draft)", verdict)
	}
}

func TestLegalityAdapterAlreadySubmittedIsIllegal(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeCommittedEvent(t, mirrorDir, "axon", "2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS", "XQ-axon-20260721-k3f9", fold.TSubmit, "axon")

	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	a.RegisterEnvelope("XQ-axon-20260721-k3f9", fold.Envelope{
		ID: "XQ-axon-20260721-k3f9", Kind: fold.KindQuestion, From: "axon", To: []string{"other"},
	})

	has, err := a.HasCommittedHistory("XQ-axon-20260721-k3f9")
	if err != nil {
		t.Fatalf("HasCommittedHistory: %v", err)
	}
	if !has {
		t.Fatal("HasCommittedHistory = false, want true (one committed event written)")
	}

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: "XQ-axon-20260721-k3f9", Transition: fold.TSubmit,
		Actor: validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
	})
	if err != nil {
		t.Fatalf("CheckLegality: %v", err)
	}
	if verdict != validate.VerdictIllegalTransition {
		t.Fatalf("verdict = %v, want VerdictIllegalTransition (re-submitting an already-submitted subject)", verdict)
	}
}

func TestLegalityAdapterUnauthorizedActor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	// No participant entry for "intruder": membership fails closed.
	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	a.RegisterEnvelope("XQ-axon-20260721-k3f9", fold.Envelope{
		ID: "XQ-axon-20260721-k3f9", Kind: fold.KindQuestion, From: "axon", To: []string{"other"},
	})

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: "XQ-axon-20260721-k3f9", Transition: fold.TSubmit,
		Actor: validate.Actor{Kind: "agent", Name: "bot", System: "intruder"},
	})
	if err != nil {
		t.Fatalf("CheckLegality: %v", err)
	}
	if verdict != validate.VerdictUnauthorizedActor {
		t.Fatalf("verdict = %v, want VerdictUnauthorizedActor", verdict)
	}
}

func TestLegalityAdapterVerifyDisputeUnsupported(t *testing.T) {
	t.Parallel()
	a := cli.NewLegalityAdapter(t.TempDir(), "axon", space.Manifest{})
	for _, transition := range []string{fold.TVerify, fold.TDispute} {
		if _, err := a.CheckLegality(validate.CandidateEvent{Subject: "XS-axon-20260721-k3f9", Transition: transition}); err == nil {
			t.Fatalf("CheckLegality(%q): expected an 'unsupported in P6' error, got nil", transition)
		}
	}
}

func TestLegalityAdapterNoRegisteredEnvelope(t *testing.T) {
	t.Parallel()
	a := cli.NewLegalityAdapter(t.TempDir(), "axon", space.Manifest{})
	if _, err := a.CheckLegality(validate.CandidateEvent{Subject: "unknown-id", Transition: fold.TSubmit}); err == nil {
		t.Fatal("expected an error when no envelope was registered for the subject")
	}
}

// writeCommittedEvent writes a minimal event/v1 YAML file under
// mirrorDir/system/events/year/ulid.yaml, for adapter tests that need
// pre-existing committed history.
func writeCommittedEvent(t *testing.T, mirrorDir, system, year, ulid, subject, transition, actorSystem string) {
	t.Helper()
	dir := filepath.Join(mirrorDir, system, "events", year)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "schema: event/v1\nevent: " + ulid + "\nspace: fixture-space\nsubject: " + subject +
		"\ntransition: " + transition + "\nactor: {kind: agent, name: bot, system: " + actorSystem + "}\nat: " + time.Now().UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(filepath.Join(dir, ulid+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write event: %v", err)
	}
}

// --- MirrorResolver --------------------------------------------------------

func TestMirrorResolverKnownArtifactAndDigest(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	artifactPath := filepath.Join(mirrorDir, "axon", "exchanges", "XQ-axon-20260721-k3f9.md")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nid: XQ-axon-20260721-k3f9\ntype: question\n---\nbody\n"
	if err := os.WriteFile(artifactPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	r := cli.NewMirrorResolver(mirrorDir, space.Manifest{})
	if !r.KnownArtifact("XQ-axon-20260721-k3f9") {
		t.Fatal("KnownArtifact = false, want true")
	}
	if r.KnownArtifact("XQ-axon-does-not-exist") {
		t.Fatal("KnownArtifact = true for a nonexistent id, want false")
	}

	digest, found := r.Digest("XQ-axon-20260721-k3f9@1.0.0")
	if !found {
		t.Fatal("Digest: found = false, want true")
	}
	if digest == "" {
		t.Fatal("Digest: got empty digest")
	}
}

func TestMirrorResolverSystem(t *testing.T) {
	t.Parallel()
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "retired-sys", Status: "left"},
	}}
	r := cli.NewMirrorResolver(t.TempDir(), manifest)

	if member, left := r.System("axon"); !member || left {
		t.Fatalf("System(axon) = (%v, %v), want (true, false)", member, left)
	}
	if member, left := r.System("retired-sys"); !member || !left {
		t.Fatalf("System(retired-sys) = (%v, %v), want (true, true)", member, left)
	}
	if member, _ := r.System("unknown-sys"); member {
		t.Fatal("System(unknown-sys) = true, want false")
	}
}

// --- SubmitValidatorAdapter ------------------------------------------------

func TestSubmitValidatorAdapterValid(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"}, {System: "other", Status: "active"},
	}}
	legality := cli.NewLegalityAdapter(t.TempDir(), "axon", manifest)
	resolver := cli.NewMirrorResolver(t.TempDir(), manifest)
	adapter := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n")
	eventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQRS\nspace: fixture-space\n" +
		"subject: XQ-axon-20260721-k3f9\ntransition: submit\nactor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/XQ-axon-20260721-k3f9.md", Content: artifactContent},
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRS.yaml", Content: eventContent},
	}

	if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
		t.Fatalf("ValidateSubmit: %v", err)
	}
}

func TestSubmitValidatorAdapterInvalidReturnsViolations(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}
	legality := cli.NewLegalityAdapter(t.TempDir(), "axon", manifest)
	resolver := cli.NewMirrorResolver(t.TempDir(), manifest)
	adapter := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	// Missing required `category` field -> a schema-class violation.
	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
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
	if err == nil {
		t.Fatal("expected a validation error for a missing required field")
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
	}
	if len(violationErr.Violations) == 0 {
		t.Fatal("expected at least one violation")
	}
}

// --- ManifestValidatorAdapter ----------------------------------------------

func TestManifestValidatorAdapterValid(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	adapter := cli.NewManifestValidatorAdapter(corpus)

	raw := []byte("schema: space/v1\n" +
		"space: fixture-space\n" +
		"min_binary_version: \"0.1.0\"\n" +
		"participants:\n" +
		"  - system: axon\n" +
		"    org: acme\n" +
		"    section: axon\n" +
		"    owners: [alice]\n" +
		"    status: active\n" +
		"    joined: \"2026-01-01\"\n")

	if err := adapter.ValidateManifest(context.Background(), raw); err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
}

func TestManifestValidatorAdapterInvalid(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	adapter := cli.NewManifestValidatorAdapter(corpus)

	// Missing required top-level fields entirely.
	raw := []byte("schema: manifest/v1\n")

	if err := adapter.ValidateManifest(context.Background(), raw); err == nil {
		t.Fatal("expected an error for an incomplete manifest")
	}
}

// --- PendingMarker / CacheRemover no-ops ------------------------------------

func TestNoopPendingMarker(t *testing.T) {
	t.Parallel()
	m := cli.NewNoopPendingMarker()
	if err := m.MarkPending(context.Background(), "space-1", "XQ-axon-20260721-k3f9", space.WriteResult{}); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
}

func TestNoopCacheRemover(t *testing.T) {
	t.Parallel()
	m := cli.NewNoopCacheRemover()
	if err := m.RemoveSpace(context.Background(), "space-1"); err != nil {
		t.Fatalf("RemoveSpace: %v", err)
	}
}
