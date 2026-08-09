package cli_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// --- ResolveActor (§7.4 order) -------------------------------------------
//
// MOVED, 2026-08-07 → actorrefusal_test.go, inside `package cli`, where they
// drive resolveActorFrom with an EXPLICIT environment.
//
// They failed here, and they were right to. Resolution now runs agent
// detection and cli.ResolveActor reads the real process environment, so three
// of them asserted a harness/config fallback while the suite itself ran inside
// Claude Code, resolved `claude-code`, and failed. The defect was the tests'
// dependence on whichever agent happened to run them — a suite that passes on
// a bare shell and fails inside an agent is one whose green is worth nothing.

// TestResolveActorNameFallsBackToOSUser is the HIGH-finding stopgap's
// regression: with every §7.4 source empty (no flag, no env, no harness
// default, no config default-actor), actor.Name previously resolved to ""
// — a CLI-minted event/artifact could carry an anonymous actor straight
// through schema validation. It must now fall back to a non-empty OS-user
// value (osUsername, unexported) rather than staying empty.
func TestResolveActorNameFallsBackToOSUser(t *testing.T) {
	// reason: mutates process env; not parallel-safe against sibling tests
	// touching the same A2A_ACTOR_* variables (same reason as
	// TestResolveActorOrderEnvBeatsHarnessAndConfig above). Setting the var
	// to "" rather than leaving it untouched guards against a leaked
	// non-empty value from process env when this test runs standalone.
	t.Setenv("A2A_ACTOR_NAME", "")
	a, err := cli.ResolveActor(cli.ActorFlags{}, cli.HarnessDefaults{}, cli.ConfigActor{})
	if err != nil {
		t.Fatalf("ResolveActor: %v", err)
	}
	if a.Name == "" {
		t.Fatal("Name = \"\" with no flag/env/harness/config source; want a non-empty OS-user fallback")
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

// TestLegalityAdapterNoteOnSubmittedResponseIsLegal reproduces
// fb-20260801-457629 at the V2 legality seam used by space CI. A response has
// no independent submit event in the mirror; regardless of how its state is
// projected at a caller, a note is transition-free and must not become
// LFC-001 at this shared candidate check.
func TestLegalityAdapterNoteOnSubmittedResponseIsLegal(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XS-beta-20260801-n0t3"

	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "beta", Status: "active"},
	}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	a.RegisterEnvelope(id, fold.Envelope{
		ID: id, Kind: fold.KindResponse, From: "beta", To: []string{"axon"},
	})

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: id, Transition: fold.TNote,
		Actor: validate.Actor{Kind: "agent", Name: "axon-agent", System: "axon"},
	})
	if err != nil {
		t.Fatalf("CheckLegality(note on submitted response): %v", err)
	}
	if verdict != validate.VerdictLegal {
		t.Fatalf("verdict = %v, want VerdictLegal (note is transition-free and actor is the response target)", verdict)
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

// mirrorResolverThreadResolver is a compile-time assertion that
// *cli.MirrorResolver actually satisfies validate.ThreadResolver — the
// second half of REF-009/REF-010's wiring gap (thread.go's ThreadResolver
// is an optional capability obtained by type assertion; nothing catches a
// method-set drift at compile time otherwise).
var _ validate.ThreadResolver = (*cli.MirrorResolver)(nil)

// TestMirrorResolverThreadOfAndThreadExists exercises both ThreadResolver
// methods over the SAME on-disk mirror ensureIndex already walks for
// KnownArtifact/Digest (adapters.go's mirrorArtifact carries `thread`
// alongside the indexed path) — no second index, no second file read.
func TestMirrorResolverThreadOfAndThreadExists(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()

	threaded := filepath.Join(mirrorDir, "axon", "exchanges", "XR-axon-country-vocabulary.md")
	if err := os.MkdirAll(filepath.Dir(threaded), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	threadedContent := "---\nid: XR-axon-country-vocabulary\ntype: requirement\nthread: thread:axon-20260729-c7q2\n---\nbody\n"
	if err := os.WriteFile(threaded, []byte(threadedContent), 0o644); err != nil {
		t.Fatalf("write threaded artifact: %v", err)
	}

	threadless := filepath.Join(mirrorDir, "axon", "exchanges", "XQ-axon-20260721-k3f9.md")
	threadlessContent := "---\nid: XQ-axon-20260721-k3f9\ntype: question\n---\nbody\n"
	if err := os.WriteFile(threadless, []byte(threadlessContent), 0o644); err != nil {
		t.Fatalf("write threadless artifact: %v", err)
	}

	r := cli.NewMirrorResolver(mirrorDir, space.Manifest{})

	thread, found := r.ThreadOf("XR-axon-country-vocabulary")
	if !found || thread != "thread:axon-20260729-c7q2" {
		t.Fatalf("ThreadOf(XR-axon-country-vocabulary) = (%q, %v), want (thread:axon-20260729-c7q2, true)", thread, found)
	}

	thread, found = r.ThreadOf("XQ-axon-20260721-k3f9")
	if !found || thread != "" {
		t.Fatalf("ThreadOf(XQ-axon-20260721-k3f9) = (%q, %v), want (\"\", true)", thread, found)
	}

	if _, found := r.ThreadOf("XQ-axon-does-not-exist"); found {
		t.Fatal("ThreadOf(nonexistent id): found = true, want false")
	}

	if !r.ThreadExists("thread:axon-20260729-c7q2") {
		t.Fatal("ThreadExists(thread:axon-20260729-c7q2) = false, want true (carried by an indexed artifact)")
	}
	if r.ThreadExists("thread:does-not-exist") {
		t.Fatal("ThreadExists(thread:does-not-exist) = true, want false")
	}
	if r.ThreadExists("") {
		t.Fatal("ThreadExists(\"\") = true, want false — an empty thread is never \"carried\", even though the threadless fixture above indexes as thread: \"\"")
	}
}

// TestMirrorResolverAdapterCarriesNoWalk is AC-1.3's structural gate: this
// file must never regain its own filepath.WalkDir — the resolver's index
// build lives in internal/cache.BuildArtifactIndex now (spec
// 01-resolver-one-home.md), and a future edit re-adding a walk here would
// resurrect the third, worse, unreported copy this phase deleted.
func TestMirrorResolverAdapterCarriesNoWalk(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	if strings.Contains(string(raw), "filepath.WalkDir") {
		t.Fatal("internal/cli/adapters.go calls filepath.WalkDir directly — the resolver's own artifact walk must live in internal/cache only (AC-1.3)")
	}
}

// TestMirrorResolverSkippedNamesTheBadFileAndGoodRefStillResolves is
// AC-1.1/AC-1.2's core proof at the resolver layer: a file two directories
// away from a real artifact whose frontmatter carries `thread:` twice
// (the exact malformed shape filed 2026-07-26, SkipReasonUndecodableYAML)
// must never blind KnownArtifact to the real artifact, and Skipped() must
// name the bad file and its reason — the fact a REF-009/REF-010 refusal
// needs in order to point a reader at the actual cause rather than the ref
// that merely looked wrong.
func TestMirrorResolverSkippedNamesTheBadFileAndGoodRefStillResolves(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()

	goodPath := filepath.Join(mirrorDir, "axon", "exchanges", "XQ-axon-20260721-good1.md")
	if err := os.MkdirAll(filepath.Dir(goodPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(goodPath, []byte("---\nid: XQ-axon-20260721-good1\ntype: question\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write good artifact: %v", err)
	}

	badRelPath := "beta/exchanges/XW-beta-20260721-bad.md"
	writeMirrorFile(t, mirrorDir, badRelPath,
		"---\nid: XW-beta-20260721-bad\nthread: thread:beta:one\nthread: thread:beta:two\n---\nbad body\n")

	r := cli.NewMirrorResolver(mirrorDir, space.Manifest{})

	if !r.KnownArtifact("XQ-axon-20260721-good1") {
		t.Fatal("KnownArtifact(good) = false, want true — one bad file elsewhere must never blind the resolver to a real artifact")
	}

	skipped := r.Skipped()
	if len(skipped) != 1 || skipped[0].Path != badRelPath || skipped[0].Reason != "undecodable-yaml" {
		t.Fatalf("Skipped() = %+v, want exactly one entry naming %q/undecodable-yaml", skipped, badRelPath)
	}
}

// mirrorResolverParentCriteriaCounter is a compile-time assertion that
// *cli.MirrorResolver satisfies validate.ParentCriteriaCounter — P6's
// REF-018 gate (2026-08-09 readiness audit, row 50): the capability now
// lives on the TYPE cli.NewMirrorResolver always returns, not on a
// per-call-site wrapper, so every one of its four production construction
// sites (cmd/a2a/wire.go x2, contract_p6_wiring.go, work_wiring.go)
// inherits it by construction. A future edit that removed
// AcceptanceCriteriaCount, or narrowed its signature, fails to COMPILE
// here — no per-site review or AST scan required to catch the regression.
var _ validate.ParentCriteriaCounter = (*cli.MirrorResolver)(nil)

// writeParentArtifact seeds a committed envelope/v1 artifact directly under
// mirrorDir/id.md with an explicit `acceptance_criteria` YAML fragment —
// AcceptanceCriteriaCount's read target.
func writeParentArtifact(t *testing.T, mirrorDir, id, criteriaYAML string) {
	t.Helper()
	raw := "---\nschema: envelope/v1\nid: " + id + "\ntype: work_request\ntitle: t\nspace: getvisa\nfrom: axon\nto: [seomatrix]\nthread: thread:axon-1\nactor: {kind: agent, name: codex}\ncreated: \"2026-08-08T08:40:00Z\"\npriority: p3\nblocking: true\nclassification: internal\ncategory: feature\nproposed_change: x\n" +
		criteriaYAML + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(mirrorDir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMirrorResolverAcceptanceCriteriaCount is AcceptanceCriteriaCount's own
// behavioural proof, moved here from cmd/a2a's now-retired
// mirrorResolverWithCriteria wrapper (validate_resolver_test.go) — the
// method itself moved, so its tests move with it.
func TestMirrorResolverAcceptanceCriteriaCount(t *testing.T) {
	t.Parallel()

	t.Run("declared criteria count", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentArtifact(t, dir, "XW-axon-20260808-p9d3", "acceptance_criteria: [\"a\", \"b\", \"c\"]")
		r := cli.NewMirrorResolver(dir, space.Manifest{})

		count, ok := r.AcceptanceCriteriaCount("XW-axon-20260808-p9d3")
		if !ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=true")
		}
		if count != 3 {
			t.Errorf("AcceptanceCriteriaCount = %d, want 3", count)
		}
	})

	t.Run("unknown parent degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		r := cli.NewMirrorResolver(dir, space.Manifest{})

		count, ok := r.AcceptanceCriteriaCount("XW-axon-does-not-exist")
		if ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=false for an unresolvable parent, got count=%d", count)
		}
	})

	t.Run("absent acceptance_criteria degrades to not-ok, never a bare zero", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// A handoff never declares acceptance_criteria at all — the field is
		// absent from its frontmatter, not present-and-empty.
		raw := "---\nschema: envelope/v1\nid: XH-axon-20260808-f3s5\ntype: handoff\ntitle: t\nspace: getvisa\nfrom: axon\nto: [seomatrix]\nthread: thread:axon-1\nactor: {kind: agent, name: codex}\ncreated: \"2026-08-08T08:40:00Z\"\npriority: p3\nblocking: true\nclassification: internal\ndeliverables: []\n---\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, "XH-axon-20260808-f3s5.md"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		r := cli.NewMirrorResolver(dir, space.Manifest{})

		count, ok := r.AcceptanceCriteriaCount("XH-axon-20260808-f3s5")
		if ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=false for an absent field, got count=%d", count)
		}
	})
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
		"thread: " + cliFixtureThread + "\n" +
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
		"thread: " + cliFixtureThread + "\n" +
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

// TestSubmitValidatorAdapterViolationNamesSkippedFile is AC-1.2's proof at
// the ValidateSubmit layer: the mirror also carries a file elsewhere whose
// frontmatter cannot decode, and the returned *ViolationError must both
// carry it in Skipped and NAME it (and its reason) in Error() — the
// refusal a caller reads must point at the file that actually failed to
// parse, not just at the artifact whose `refs:`/`thread` looked wrong
// (US-2, spec 01-resolver-one-home.md §1).
func TestSubmitValidatorAdapterViolationNamesSkippedFile(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}}}
	mirrorDir := t.TempDir()

	badRelPath := "beta/exchanges/XW-beta-20260721-bad.md"
	writeMirrorFile(t, mirrorDir, badRelPath,
		"---\nid: XW-beta-20260721-bad\nthread: thread:beta:one\nthread: thread:beta:two\n---\nbad body\n")

	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	adapter := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	// Same missing-`category` fixture as
	// TestSubmitValidatorAdapterInvalidReturnsViolations — the violation's
	// own cause is irrelevant here; what matters is that Skipped/Error
	// surface the UNRELATED bad file regardless of which violation fired.
	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"thread: " + cliFixtureThread + "\n" +
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
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
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

// TestErrNoActorNameNamesBothRemedies pins the message, which IS the
// deliverable: the schema violation it replaces named neither the flag nor the
// env var, so an agent had nothing to act on. Asserted without touching the
// environment, so it holds on every machine including the one where os/user
// resolves and the case above skips.
func TestErrNoActorNameNamesBothRemedies(t *testing.T) {
	t.Parallel()

	msg := cli.ErrNoActorName.Error()
	for _, want := range []struct{ substr, why string }{
		{"--actor-name", "name the flag — the first thing a caller reaches for"},
		{"A2A_ACTOR_NAME", "name the env var — the only route for a non-interactive runner"},
		{"permanently", "say why it is refused rather than defaulted: the actor is recorded in a shared log"},
		{"container", "name where this happens, so the reader knows it is not their local setup"},
	} {
		if !strings.Contains(msg, want.substr) {
			t.Errorf("ErrNoActorName is missing %q — %s\ngot: %s", want.substr, want.why, msg)
		}
	}
}

// cliFixtureThread is the §3.8 thread every hand-built envelope fixture in
// this package carries. Since P46 the schema REQUIRES the field and every
// drafting verb mints or inherits one, so a threadless fixture no longer
// represents a document this product can produce — it would only exercise the
// refusal path, which has its own dedicated tests.
const cliFixtureThread = "thread:axon-20260721-k3f9"

// TestSubmitValidatorCarriesCompanionArtifacts is the end-to-end half of
// fb-20260806-c6ad38. Fixing space.ContractForPath alone would be a fix to a
// helper; what actually broke was THIS dispatch, which ran
// artifact.ParseFrontmatter over a JSON companion and refused the whole write
// with "missing or malformed frontmatter delimiters" — at
// `a2a contract publish`, after validate and preflight had both passed.
//
// It asserts the dispatch classifies a companion as carried baseline data
// rather than an envelope draft, which is the exact decision that failed.
func TestSubmitValidatorCarriesCompanionArtifacts(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"axon/provides/widget/artifacts/errors.yaml",
		"axon/provides/widget/artifacts/fixture-manifest.json",
		"axon/provides/widget/artifacts/nested/changelog.md",
	} {
		if !space.IsContractBaselinePath(path) {
			t.Errorf("%q is not classified as contract baseline data, so the submit "+
				"dispatch will parse it as an envelope draft and refuse the whole write", path)
		}
	}
	// The descriptor is NOT baseline data — it is the artifact itself.
	if space.IsContractBaselinePath("axon/provides/widget/contract.md") {
		t.Error("the descriptor must stay an envelope artifact, not carried baseline data")
	}
}
