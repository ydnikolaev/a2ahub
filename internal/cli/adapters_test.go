package cli_test

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/version"
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

	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: fold.MembershipMember}}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: "XQ-axon-20260721-k3f9", Transition: fold.TSubmit,
		Actor:    validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
		Envelope: validate.Envelope{ID: "XQ-axon-20260721-k3f9", Kind: "question", From: "axon", To: []string{"other"}},
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

	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: fold.MembershipMember}}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)

	has, err := a.HasCommittedHistory("XQ-axon-20260721-k3f9")
	if err != nil {
		t.Fatalf("HasCommittedHistory: %v", err)
	}
	if !has {
		t.Fatal("HasCommittedHistory = false, want true (one committed event written)")
	}

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: "XQ-axon-20260721-k3f9", Transition: fold.TSubmit,
		Actor:    validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
		Envelope: validate.Envelope{ID: "XQ-axon-20260721-k3f9", Kind: "question", From: "axon", To: []string{"other"}},
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
		{System: "axon", Status: fold.MembershipMember},
		{System: "beta", Status: fold.MembershipMember},
	}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: id, Transition: fold.TNote,
		Actor:    validate.Actor{Kind: "agent", Name: "axon-agent", System: "axon"},
		Envelope: validate.Envelope{ID: id, Kind: "response", From: "beta", To: []string{"axon"}},
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
	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: fold.MembershipMember}}}
	a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)

	verdict, err := a.CheckLegality(validate.CandidateEvent{
		Subject: "XQ-axon-20260721-k3f9", Transition: fold.TSubmit,
		Actor:    validate.Actor{Kind: "agent", Name: "bot", System: "intruder"},
		Envelope: validate.Envelope{ID: "XQ-axon-20260721-k3f9", Kind: "question", From: "axon", To: []string{"other"}},
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

// TestLegalityAdapterZeroValueEnvelopeNoLongerErrors is no-silent-yes-2026-08/
// P6's own regression pin for US-3: a forgotten envelope used to be a
// RUNTIME ERROR ("no envelope registered for subject") raised by a
// separate registration method a caller could skip entirely.
// validate.CandidateEvent.Envelope (seam.go) makes that structural rather
// than a call a caller can forget — a candidate that still never sets it
// carries a ZERO-VALUE Envelope, which resolves to no known (kind, state,
// transition) table row and refuses as an ordinary ILLEGAL TRANSITION,
// never a hard error.
func TestLegalityAdapterZeroValueEnvelopeNoLongerErrors(t *testing.T) {
	t.Parallel()
	a := cli.NewLegalityAdapter(t.TempDir(), "axon", space.Manifest{})
	verdict, err := a.CheckLegality(validate.CandidateEvent{Subject: "unknown-id", Transition: fold.TSubmit})
	if err != nil {
		t.Fatalf("CheckLegality with a zero-value Envelope returned an error: %v — want a verdict, never a runtime error (US-3)", err)
	}
	if verdict != validate.VerdictIllegalTransition {
		t.Fatalf("verdict = %v, want VerdictIllegalTransition (an empty Kind matches no table row)", verdict)
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
		{System: "axon", Status: fold.MembershipMember},
		{System: "retired-sys", Status: fold.MembershipLeft},
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

// mirrorResolverResponseParentResolver is ParentCriteriaCounter's own
// compile-time-gate pattern above, applied to the second optional upgrade
// P6 wave C's REF-019 gap-#2 fix adds (internal/validate/verdicts.go's
// ResponseParentResolver): every cli.NewMirrorResolver construction site
// gets ParentOf by construction, and a future edit that removed it or
// narrowed its signature fails to COMPILE here.
var _ validate.ResponseParentResolver = (*cli.MirrorResolver)(nil)

// writeResponseArtifact seeds a committed artifact directly under
// mirrorDir/id.md with a `parent:` field — ParentOf's own read target. Same
// minimal-frontmatter shape TestMirrorResolverSkippedNamesTheBadFileAndGoodRefStillResolves
// already uses above (id + type is enough for the index to carry it; ParentOf
// only re-reads `parent`, never validates against the full response schema).
func writeResponseArtifact(t *testing.T, mirrorDir, id, parentID string) {
	t.Helper()
	raw := "---\nid: " + id + "\ntype: response\nparent: " + parentID + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(mirrorDir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMirrorResolverParentOf is ParentOf's own behavioural proof — the
// verify-side hop internal/validate/verdicts.go's checkVerdictIndexRange
// takes when a response's own parent is not directly criteria-bearing.
func TestMirrorResolverParentOf(t *testing.T) {
	t.Parallel()

	t.Run("resolves the response's own parent", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeResponseArtifact(t, dir, "XS-seomatrix-20260808-r1", "XW-axon-20260808-p9d3")
		r := cli.NewMirrorResolver(dir, space.Manifest{})

		parentID, ok := r.ParentOf("XS-seomatrix-20260808-r1")
		if !ok {
			t.Fatalf("ParentOf: expected ok=true")
		}
		if parentID != "XW-axon-20260808-p9d3" {
			t.Errorf("ParentOf = %q, want XW-axon-20260808-p9d3", parentID)
		}
	})

	t.Run("unknown response degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		r := cli.NewMirrorResolver(dir, space.Manifest{})

		parentID, ok := r.ParentOf("XS-does-not-exist")
		if ok {
			t.Fatalf("ParentOf: expected ok=false for an unresolvable response, got parent=%q", parentID)
		}
	})

	t.Run("artifact with no parent degrades to not-ok, never an empty string treated as found", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentArtifact(t, dir, "XW-axon-20260808-p9d3", "acceptance_criteria: [\"a\"]") // a work_request carries no `parent`
		r := cli.NewMirrorResolver(dir, space.Manifest{})

		parentID, ok := r.ParentOf("XW-axon-20260808-p9d3")
		if ok {
			t.Fatalf("ParentOf: expected ok=false for an artifact with no parent, got parent=%q", parentID)
		}
	})
}

// TestAdaptersFileCarriesNoAcceptanceCriteriaDecode is P5's AC4 structural
// gate on this surface, the same "read the file, grep the literal" shape
// TestMirrorResolverAdapterCarriesNoWalk already uses above: the
// parent-criteria resolution moved into internal/cache (ADR-004), so a
// future edit that re-added its own `m["acceptance_criteria"]` decode here
// would resurrect the sixth duplication instance this phase closed. The
// bracket-literal target (rather than a bare substring match) survives
// doc-comment prose that still legitimately mentions the field name.
func TestAdaptersFileCarriesNoAcceptanceCriteriaDecode(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	if strings.Contains(string(raw), `["acceptance_criteria"]`) {
		t.Fatal("internal/cli/adapters.go decodes acceptance_criteria directly — the resolution must live only in internal/cache (P5, ADR-004), never a second copy on a surface")
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
		{System: "axon", Status: fold.MembershipMember}, {System: "other", Status: fold.MembershipMember},
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

// TestSubmitValidatorAdapterRestrictedClassificationBilateralAccepted is
// this fix wave's own regression proof (no-silent-yes-2026-08/P3 stage 2
// FIX B). Before MirrorResolver implemented validate.ActiveParticipantLister,
// EVERY classification: restricted submission refused on the capability miss
// regardless of whether the space was genuinely bilateral —
// D9's fail-safe philosophy working as designed, but a live regression for a
// space that legitimately uses restricted today. A restricted artifact whose
// space's ACTIVE participants are exactly {from} ∪ to must be ACCEPTED once
// the capability is wired, not refused for a capability the resolver now has.
func TestSubmitValidatorAdapterRestrictedClassificationBilateralAccepted(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "other", Status: fold.MembershipMember},
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
		"classification: restricted\n" +
		"---\nbody\n")
	eventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQRZ\nspace: fixture-space\n" +
		"subject: XQ-axon-20260721-k3f9\ntransition: submit\nactor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/XQ-axon-20260721-k3f9.md", Content: artifactContent},
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRZ.yaml", Content: eventContent},
	}

	if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
		t.Fatalf("ValidateSubmit: %v — a restricted artifact whose space's ACTIVE participants are exactly {from} ∪ to must be accepted", err)
	}
}

// TestSubmitValidatorAdapterRestrictedClassificationExceedsBilateralRefused
// is POL-024's own live proof, distinguishing a REAL bilateral violation from
// the capability-miss refusal the sibling test above closes: the same
// resolver now CAN enumerate active participants, and when the space's
// active membership genuinely exceeds {from} ∪ to, POL-024 fires ALONE —
// without POL-026, the "could not check" explanation, which must NOT appear
// once the capability is wired.
//
// The capability miss now emits POL-024 + POL-026 rather than a code of its
// own: the wave-1 POL-025 was a SECOND reject code for one rule, which D9
// never asked for ("printed as an explanation alongside an ORDINARY reject").
// It was deleted, not renamed — see internal/validate/classification.go.
func TestSubmitValidatorAdapterRestrictedClassificationExceedsBilateralRefused(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "other", Status: fold.MembershipMember}, {System: "third", Status: fold.MembershipMember},
	}}
	legality := cli.NewLegalityAdapter(t.TempDir(), "axon", manifest)
	resolver := cli.NewMirrorResolver(t.TempDir(), manifest)
	adapter := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k4g0\n" +
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
		"classification: restricted\n" +
		"---\nbody\n")
	eventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQS0\nspace: fixture-space\n" +
		"subject: XQ-axon-20260721-k4g0\ntransition: submit\nactor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/XQ-axon-20260721-k4g0.md", Content: artifactContent},
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQS0.yaml", Content: eventContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected POL-024 for a restricted artifact whose space's active participants ({axon, other, third}) exceed {from} ∪ to ({axon, other}), got nil")
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
	}
	if !p1HasViolationCode(violationErr.Violations, "POL-024") {
		t.Fatalf("ValidateSubmit refused, but not with POL-024: %+v", violationErr.Violations)
	}
	if p1HasViolationCode(violationErr.Violations, "POL-025") || p1HasViolationCode(violationErr.Violations, "POL-026") {
		t.Fatalf("ValidateSubmit fired POL-025/POL-026 (capability miss) even though MirrorResolver now implements ActiveParticipantLister: %+v", violationErr.Violations)
	}
}

func TestSubmitValidatorAdapterInvalidReturnsViolations(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: fold.MembershipMember}}}
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
	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: fold.MembershipMember}}}
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

// --- SubmitValidatorAdapter: events partition (P1, REF-019/REF-023) ------
//
// spec 01-the-write-gate-reaches-the-write.md §T1: before this phase the
// events partition of ValidateSubmit decoded each event into a lookup map
// used only by the drafts loop and validated nothing, so a `verify`/`close`
// submission — which carries event files exclusively — was written
// unjudged. These are P1's AC1/AC2/AC3/AC7/AC8, driven directly at the
// adapter layer (the T3 row lives in internal/e2e).

// p1WriteParentWithTwoCriteria seeds a committed work_request directly
// under mirrorDir/<id>.md declaring exactly two acceptance_criteria —
// REF-019's own bounds and REF-023's own completeness target. Same shape as
// this file's own writeParentArtifact, restated under the fixture-space id
// every other SubmitValidatorAdapter test here uses, so one manifest serves
// both the events and drafts partitions of a single ValidateSubmit call.
func p1WriteParentWithTwoCriteria(t *testing.T, mirrorDir, id string) {
	t.Helper()
	raw := "---\nschema: envelope/v1\nid: " + id + "\ntype: work_request\ntitle: t\nspace: fixture-space\n" +
		"from: axon\nto: [beta]\nthread: " + cliFixtureThread + "\nactor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\npriority: p3\nblocking: true\nclassification: internal\n" +
		"category: feature\nproposed_change: x\nacceptance_criteria:\n  - \"first\"\n  - \"second\"\n" +
		"---\nbody\n"
	if err := os.WriteFile(filepath.Join(mirrorDir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// p1CloseEvent builds a close event/v2 document over parentID —
// cmd_validate_ci_test.go's own closeEvent fixture
// (TestValidateCI_REF019FiresOnAnOutOfRangeVerdictIndex), restated here
// because that helper is unexported in a different test binary.
func p1CloseEvent(eventID, parentID, tail string) []byte {
	return []byte("schema: event/v2\n" +
		"event: " + eventID + "\n" +
		"space: fixture-space\n" +
		"subject: " + parentID + "\n" +
		"transition: close\n" +
		"actor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n" +
		tail)
}

// p1HasViolationCode reports whether violations carries code.
func p1HasViolationCode(violations []validate.Violation, code string) bool {
	for _, v := range violations {
		if v.Code == code {
			return true
		}
	}
	return false
}

// p1NewCLIAdapter builds a SubmitValidatorAdapter over a fresh engine and
// mirrorDir, with axon/beta as the manifest's only participants — the
// shared arrange step every test below reuses.
func p1NewCLIAdapter(t *testing.T, mirrorDir string, manifest space.Manifest) *cli.SubmitValidatorAdapter {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	return cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)
}

var p1TwoSystemManifest = space.Manifest{Participants: []space.Participant{
	{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember},
}}

// TestSubmitValidatorAdapterEventsPartitionRefusesOutOfRangeVerdictIndex is
// P1's AC1: a submission carrying only event files — exactly what
// `a2a verify`/`a2a close` produce — must be refused when a `verdicts[]`
// entry names an index outside the parent's own acceptance_criteria[].
// WATCHED FAILING before the fix: pre-fix, ValidateSubmit's events branch
// only decodes into a lookup map and returns nil for this exact submission.
func TestSubmitValidatorAdapterEventsPartitionRefusesOutOfRangeVerdictIndex(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p1a1"
	p1WriteParentWithTwoCriteria(t, mirrorDir, parentID)
	adapter := p1NewCLIAdapter(t, mirrorDir, p1TwoSystemManifest)

	// The parent declares exactly two criteria (indices 0, 1) — 2 is out of range.
	eventContent := p1CloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQRT", parentID,
		"verdicts:\n  - index: 2\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRT.yaml", Content: eventContent},
	}

	err := adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected a refusal for an out-of-range verdict index, got nil — " +
			"an events-only submission must not write unjudged")
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
	}
	if !p1HasViolationCode(violationErr.Violations, "REF-019") {
		t.Fatalf("ValidateSubmit refused, but not with REF-019: %+v", violationErr.Violations)
	}
}

// TestSubmitValidatorAdapterEventsPartitionRefusesIncompleteVerdicts is P1's
// AC2 — REF-023's own completeness half, riding the same call site as AC1
// (verdicts.go's checkVerdictIndexRange calls checkVerdictCompleteness
// internally).
func TestSubmitValidatorAdapterEventsPartitionRefusesIncompleteVerdicts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p1a2"
	p1WriteParentWithTwoCriteria(t, mirrorDir, parentID)
	adapter := p1NewCLIAdapter(t, mirrorDir, p1TwoSystemManifest)

	// Only criterion 0 of the parent's two declared criteria is named.
	eventContent := p1CloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQR9", parentID,
		"verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQR9.yaml", Content: eventContent},
	}

	err := adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected a refusal for an incomplete verdict set, got nil")
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
	}
	if !p1HasViolationCode(violationErr.Violations, "REF-023") {
		t.Fatalf("ValidateSubmit refused, but not with REF-023: %+v", violationErr.Violations)
	}
}

// TestSubmitValidatorAdapterEventsPartitionAcceptsCompleteVerdicts is P1's
// AC3 control case: a complete, in-range verdict set still exits 0 (the
// event mints). Run at the manifest's min_binary_version PINNED to
// version.ProducerStampFloor, with the event ALREADY carrying produced_by —
// exactly what space.WriteFunnel.PrepareSubmission hands ValidateSubmit,
// since StampProducer runs before the final ValidateSubmit call
// (prepared.go: stamp at line ~229, validate at ~236). Proves the new call
// site does not turn a correctly-stamped write into a false POL-012
// refusal at a space that has raised its floor.
func TestSubmitValidatorAdapterEventsPartitionAcceptsCompleteVerdicts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p1a3"
	p1WriteParentWithTwoCriteria(t, mirrorDir, parentID)
	manifest := space.Manifest{
		MinBinaryVersion: version.ProducerStampFloor,
		Participants:     p1TwoSystemManifest.Participants,
	}
	adapter := p1NewCLIAdapter(t, mirrorDir, manifest)

	eventContent := p1CloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQRV", parentID,
		"produced_by:\n  tool: a2a\n  version: \""+version.ProducerStampFloor+"\"\n"+
			"verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n"+
			"  - index: 1\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRV.yaml", Content: eventContent},
	}

	if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
		t.Fatalf("ValidateSubmit: %v, want a complete in-range verdict set to mint clean at a raised floor", err)
	}
}

// TestSubmitValidatorAdapterEventsPartitionUnresolvableParentDegradesSilently
// is P1's AC8: a resolver that cannot resolve the event's own subject at
// all (an empty mirror — the parent was never committed) degrades to
// "cannot check", never a synthesized violation.
func TestSubmitValidatorAdapterEventsPartitionUnresolvableParentDegradesSilently(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir() // empty: the parent this event names was never committed
	adapter := p1NewCLIAdapter(t, mirrorDir, p1TwoSystemManifest)

	eventContent := p1CloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQRW", "XW-axon-20260820-doesnotexist",
		"verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRW.yaml", Content: eventContent},
	}

	if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
		t.Fatalf("ValidateSubmit: %v, want an unresolvable parent to degrade to \"cannot check\" (nil), never a synthesized violation", err)
	}
}

// TestSubmitValidatorAdapterEventsAndDraftsYieldOneVerdictNoDuplicate is
// P1's AC7: a submission carrying BOTH a draft (with its own paired submit
// event) AND an unrelated close event over a different, out-of-range
// verdict set must judge both partitions into ONE result, with the
// out-of-range violation reported exactly once — not duplicated by any
// interaction between the drafts loop's own candidate widening and the
// events partition's new call.
func TestSubmitValidatorAdapterEventsAndDraftsYieldOneVerdictNoDuplicate(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p1a7"
	p1WriteParentWithTwoCriteria(t, mirrorDir, parentID)
	adapter := p1NewCLIAdapter(t, mirrorDir, p1TwoSystemManifest)

	// A VALID draft + its own submit event (same shape as
	// TestSubmitValidatorAdapterValid), contributing zero violations.
	draftContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n")
	draftEventContent := []byte("schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQRX\nspace: fixture-space\n" +
		"subject: XQ-axon-20260721-k3f9\ntransition: submit\nactor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n")

	// The unrelated out-of-range close, same fixture as AC1's own test.
	closeEventContent := p1CloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQRY", parentID,
		"verdicts:\n  - index: 2\n    verdict: met\n    cause_owner: axon\n")

	files := []space.FileWrite{
		{Path: "axon/exchanges/XQ-axon-20260721-k3f9.md", Content: draftContent},
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRX.yaml", Content: draftEventContent},
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRY.yaml", Content: closeEventContent},
	}

	err := adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected a refusal from the mixed batch's out-of-range close, got nil")
	}
	var violationErr *cli.ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
	}
	count := 0
	for _, v := range violationErr.Violations {
		if v.Code == "REF-019" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("REF-019 violation count = %d, want exactly 1 (one verdict set, one out-of-range index, no duplicate reporting): %+v",
			count, violationErr.Violations)
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

// --- P9 criterion 3: ONE function decides the class (spec 09) ----------

// TestSubmitValidatorBranchesOnTheClassifierNotThePathPredicate is spec 09
// criterion 3 on the CLI surface. Four path-family predicates —
// IsDataPackageReadmePath, isDataPackagePayloadPath, isBlobPayloadPath and
// IsContractBaselinePath — already answered one question between them, and
// every reader answered it AGAIN in its own idiom: this dispatch by "is
// this a contract baseline path", `validate --ci` by "does it end in .md".
// Those two answers disagreed about
// `<system>/provides/<slug>/artifacts/CHANGELOG.md`, which is
// fb-20260820-d1e370 in one sentence.
//
// The predicate itself is NOT deleted in this phase (§T1 says so, and §9
// records collapsing the family as named debt). What is forbidden is a
// READER holding its own opinion, so this asserts the shape of the file
// rather than a behaviour a passing test could reproduce by accident.
func TestSubmitValidatorBranchesOnTheClassifierNotThePathPredicate(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	src := string(raw)
	if strings.Contains(codeLinesOnly(src), "space.IsContractBaselinePath(") {
		t.Fatalf("internal/cli/adapters.go still branches on space.IsContractBaselinePath — it must ask space.ClassifyCarried* instead (spec 09 criterion 3)")
	}
	if !strings.Contains(src, "space.ClassifyCarriedBatch(") {
		t.Fatalf("internal/cli/adapters.go must classify its batch through space.ClassifyCarriedBatch (spec 09 criterion 3)")
	}
	if !strings.Contains(src, "space.ContractCarriedMembership(") {
		t.Fatalf("internal/cli/adapters.go must reach the shared membership rule (spec 09 S-3/S-4)")
	}
}

// TestValidateCIBranchesOnTheClassifierNotTheSuffix is the same criterion at
// `validate --ci`'s discovery, the OTHER half of the disagreement: the loop
// that classified by `.md` suffix three lines below a space.ContractForPath
// call whose answer it discarded.
func TestValidateCIBranchesOnTheClassifierNotTheSuffix(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("cmd_validate_ci.go")
	if err != nil {
		t.Fatalf("read cmd_validate_ci.go: %v", err)
	}
	if !strings.Contains(string(raw), "descriptors.classify(p)") {
		t.Fatalf("`validate --ci`'s discovery must consult the classifier before its .md/consumes/event switch (spec 09 criterion 3)")
	}
	if !strings.Contains(string(raw), "space.UndeclaredCompanionFinding(") {
		t.Fatalf("`validate --ci` must raise S-3's refusal through the SHARED finding, so submit and the merge gate name the same fix in the same words")
	}
}

// TestNoFifthPathFamilyPredicate is criterion 3's second clause: no new
// path-family predicate is added anywhere. The named cost of getting this
// wrong a third time is a fifth member of a family that already has four,
// so the guard counts the family rather than trusting a review.
func TestNoFifthPathFamilyPredicate(t *testing.T) {
	t.Parallel()
	family := []string{
		"IsDataPackageReadmePath",
		"isDataPackagePayloadPath",
		"isBlobPayloadPath",
		"IsContractBaselinePath",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var declared []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "func ") {
				continue
			}
			name, _, ok := strings.Cut(strings.TrimPrefix(line, "func "), "(")
			if !ok || !strings.HasSuffix(line, "bool {") {
				continue
			}
			lower := strings.ToLower(name)
			if strings.Contains(lower, "payloadpath") || strings.Contains(lower, "baselinepath") || strings.Contains(lower, "readmepath") {
				declared = append(declared, e.Name()+":"+name)
			}
		}
	}
	for _, d := range declared {
		_, name, _ := strings.Cut(d, ":")
		var known bool
		for _, f := range family {
			if name == f {
				known = true
			}
		}
		if !known {
			t.Fatalf("%s is a FIFTH path-family predicate for a question space.ClassifyCarried already answers — make it a case of the classifier instead (spec 09, \"The named cost of getting this wrong a third time\")", d)
		}
	}
}

// codeLinesOnly strips whole-line comments so a shape assertion judges what
// the file DOES, not what it says about what it used to do — the comments
// above these call sites deliberately quote the predicate they replaced.
func codeLinesOnly(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// --- no-silent-yes-2026-08/P6: successor precondition -------------------

// TestAdaptersFileCarriesNoRegisterEnvelopeMethod is AC 5's own regression
// guard, mirroring TestAdaptersFileCarriesNoAcceptanceCriteriaDecode's
// (internal/mcp/adapters_test.go) structural-gate shape: US-3 makes a
// forgotten envelope a compile-time-visible zero-valued struct field, not a
// runtime error from a separate registration method a caller could skip —
// a future edit re-adding that method (under any name containing this
// identifier) would resurrect the exact side-channel this phase removed.
func TestAdaptersFileCarriesNoRegisterEnvelopeMethod(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	const banned = "Register" + "Envelope" // split so this guard's own source never matches its own check
	if strings.Contains(string(raw), banned) {
		t.Fatalf("internal/cli/adapters.go still carries a %s method/reference — the envelope side-channel must stay removed (US-3)", banned)
	}
}

// TestMirrorResolverSuccessorResolvesAuthorAndFoldedState is D7/D9's SOURCE
// half, driven directly against the concrete Resolver: given a committed
// decision artifact plus its own committed `propose` event, Successor
// resolves its envelope `from` (author) and its current folded lifecycle
// state — the two facts internal/fold's own declared decision-supersede
// row preconditions check.
func TestMirrorResolverSuccessorResolvesAuthorAndFoldedState(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const id = "XD-axon-20260827-s001"
	writeDecisionArtifact(t, mirrorDir, id, []string{"beta"})
	writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")

	r := cli.NewMirrorResolver(mirrorDir, space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember},
	}})
	author, state, ok := r.Successor(id)
	if !ok {
		t.Fatalf("Successor(%q): ok = false, want true", id)
	}
	if author != "axon" {
		t.Fatalf("author = %q, want axon (writeDecisionArtifact's own `from`)", author)
	}
	if state != "proposed" {
		t.Fatalf("state = %q, want proposed (one committed propose event)", state)
	}
}

// TestMirrorResolverSuccessorUnknownIDDegrades pins the "cannot resolve"
// discipline every other optional-capability method in this file follows
// (AcceptanceCriteriaCount's own doc comment): never a synthesized
// author/state, always ok=false.
func TestMirrorResolverSuccessorUnknownIDDegrades(t *testing.T) {
	t.Parallel()
	r := cli.NewMirrorResolver(t.TempDir(), space.Manifest{})
	if _, _, ok := r.Successor("XD-axon-unknown"); ok {
		t.Fatal("Successor on an unindexed id: ok = true, want false")
	}
}

// TestMirrorResolverSuccessorResolvesApprovedAcrossSections is D-1+D-2's
// own proof (this wave's report, the whole point of the wave): a successor
// decision carrying a REAL `required_approvers` list and a FULL quorum of
// `approve` events resolves as `approved` through MirrorResolver.Successor
// — even though every approve event is committed under the APPROVING
// participant's OWN section (beta's, gamma's), never the successor id's
// own home system's section (axon's). Before this wave, D-1 alone
// (RequiredApprovers never carried into the folded envelope) made
// StateApproved unreachable through this resolver regardless of D-2; D-2
// alone (single-section committed-history read) would have hidden both
// approve events even with D-1 fixed. Either bug alone already blocks
// this exact, realistic scenario.
func TestMirrorResolverSuccessorResolvesApprovedAcrossSections(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const id = "XD-axon-20260827-q001"
	writeDecisionArtifact(t, mirrorDir, id, []string{"beta", "gamma"})
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "propose", "axon")
	// Both approve events land under the APPROVING participant's OWN
	// section, never axon's (the successor id's own home system) — the
	// exact D-2 shape a single-section read cannot see.
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "approve", "beta")
	writeLifecycleEvent(t, mirrorDir, "gamma", 2, id, "approve", "gamma")

	r := cli.NewMirrorResolver(mirrorDir, space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember}, {System: "gamma", Status: fold.MembershipMember},
	}})
	author, state, ok := r.Successor(id)
	if !ok {
		t.Fatalf("Successor(%q): ok = false, want true", id)
	}
	if author != "axon" {
		t.Fatalf("author = %q, want axon (writeDecisionArtifact's own `from`)", author)
	}
	if state != "approved" {
		t.Fatalf("state = %q, want approved — D-1: RequiredApprovers must reach the folded envelope; "+
			"D-2: both approve events must resolve despite living under OTHER participants' own sections", state)
	}
}

// TestLegalityAdapterDecisionSupersedeSuccessorPrecondition is no-silent-
// yes-2026-08/P6's own consumer-side proof, driven directly against the
// REAL adapter: a rejected decision's supersede must be authored by the
// successor's own author; an approved decision's supersede must name an
// approved successor. Both rows: UNRESOLVED successor facts (nil
// SuccessorEnvelope) refuse, never a silent grant.
func TestLegalityAdapterDecisionSupersedeSuccessorPrecondition(t *testing.T) {
	t.Parallel()
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember},
	}}

	t.Run("rejected_requires_successor_author", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		const id = "XD-axon-20260827-r001"
		writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, id, "reject", "beta")
		a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
		env := validate.Envelope{ID: id, Kind: "decision", From: "axon", RequiredApprovers: []string{"beta"}}

		t.Run("unresolved_successor_refuses", func(t *testing.T) {
			t.Parallel()
			verdict, err := a.CheckLegality(validate.CandidateEvent{
				Subject: id, Transition: "supersede", Envelope: env,
				Actor: validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if verdict != validate.VerdictUnauthorizedActor {
				t.Fatalf("verdict = %v, want VerdictUnauthorizedActor", verdict)
			}
		})
		t.Run("resolved_matching_author_grants", func(t *testing.T) {
			t.Parallel()
			verdict, err := a.CheckLegality(validate.CandidateEvent{
				Subject: id, Transition: "supersede", Envelope: env,
				Actor:             validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
				SuccessorEnvelope: &validate.SuccessorEnvelope{Author: "axon", State: "draft"},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if verdict != validate.VerdictLegal {
				t.Fatalf("verdict = %v, want VerdictLegal", verdict)
			}
		})
		t.Run("resolved_nonmatching_author_refuses", func(t *testing.T) {
			t.Parallel()
			verdict, err := a.CheckLegality(validate.CandidateEvent{
				Subject: id, Transition: "supersede", Envelope: env,
				Actor:             validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
				SuccessorEnvelope: &validate.SuccessorEnvelope{Author: "beta", State: "draft"},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if verdict != validate.VerdictUnauthorizedActor {
				t.Fatalf("verdict = %v, want VerdictUnauthorizedActor", verdict)
			}
		})
	})

	t.Run("approved_requires_successor_approved", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		const id = "XD-axon-20260827-a001"
		writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, id, "approve", "beta")
		a := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
		env := validate.Envelope{ID: id, Kind: "decision", From: "axon", RequiredApprovers: []string{"beta"}}

		t.Run("unresolved_successor_refuses", func(t *testing.T) {
			t.Parallel()
			verdict, err := a.CheckLegality(validate.CandidateEvent{
				Subject: id, Transition: "supersede", Envelope: env,
				Actor: validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if verdict != validate.VerdictUnauthorizedActor {
				t.Fatalf("verdict = %v, want VerdictUnauthorizedActor", verdict)
			}
		})
		t.Run("resolved_approved_successor_grants", func(t *testing.T) {
			t.Parallel()
			verdict, err := a.CheckLegality(validate.CandidateEvent{
				Subject: id, Transition: "supersede", Envelope: env,
				Actor:             validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
				SuccessorEnvelope: &validate.SuccessorEnvelope{Author: "irrelevant", State: "approved"},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if verdict != validate.VerdictLegal {
				t.Fatalf("verdict = %v, want VerdictLegal", verdict)
			}
		})
		t.Run("resolved_unapproved_successor_refuses", func(t *testing.T) {
			t.Parallel()
			verdict, err := a.CheckLegality(validate.CandidateEvent{
				Subject: id, Transition: "supersede", Envelope: env,
				Actor:             validate.Actor{Kind: "agent", Name: "bot", System: "axon"},
				SuccessorEnvelope: &validate.SuccessorEnvelope{Author: "irrelevant", State: "proposed"},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if verdict != validate.VerdictUnauthorizedActor {
				t.Fatalf("verdict = %v, want VerdictUnauthorizedActor", verdict)
			}
		})
	})
}

// TestSubmitValidatorAdapterDecisionSupersedeSourcesSuccessorFacts is the
// FULL chain, through the real SubmitValidatorAdapter: a decision-supersede
// candidate's SuccessorEnvelope is sourced from ctx.Resolver's own optional
// SuccessorResolver capability (resolveSuccessorEnvelope, adapters.go) —
// keyed off the supersede event's own `refs[].ref` — and consumed by
// LegalityAdapter.CheckLegality, surfacing as LFC-005 (author mismatch) or
// LFC-005+LFC-006 (successor unresolvable) in the engine's own Result.
func TestSubmitValidatorAdapterDecisionSupersedeSourcesSuccessorFacts(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember},
	}}

	newAdapter := func(mirrorDir string) *cli.SubmitValidatorAdapter {
		legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
		resolver := cli.NewMirrorResolver(mirrorDir, manifest)
		return cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)
	}

	// decisionDraft reuses writeDecisionArtifact's own schema-valid content
	// (already exercised by this package's other tests) rather than
	// hand-rolling a second, possibly-drifting shape: it writes the SAME
	// artifact this test's own committed history already names, then reads
	// those exact bytes back for the batch's own "resubmission" draft.
	decisionDraft := func(t *testing.T, mirrorDir, id string, approvers []string) []byte {
		t.Helper()
		writeDecisionArtifact(t, mirrorDir, id, approvers)
		raw, err := os.ReadFile(filepath.Join(mirrorDir, "decisions", id+".md"))
		if err != nil {
			t.Fatalf("decisionDraft: read back %s: %v", id, err)
		}
		return raw
	}
	supersedeEvent := func(t *testing.T, id, successorID, actorSystem string) []byte {
		t.Helper()
		eventID, err := artifact.MintULIDAt(time.Date(2026, 8, 27, 10, 1, 0, 0, time.UTC), rand.Reader)
		if err != nil {
			t.Fatalf("supersedeEvent: mint ulid: %v", err)
		}
		return []byte("schema: event/v1\nevent: " + eventID.String() + "\nspace: fixture-space\n" +
			"subject: " + id + "\ntransition: supersede\nrefs: [{ref: " + successorID + "}]\n" +
			"actor: {kind: agent, name: bot, system: " + actorSystem + "}\nat: 2026-08-27T10:01:00Z\n")
	}

	t.Run("resolved_matching_author_no_LFC005", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		const id = "XD-axon-20260827-a1b1"
		const successorID = "XD-axon-20260827-s1c1"
		writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, id, "reject", "beta")
		// writeDecisionArtifact hardcodes `from: axon` — the acting actor
		// below is axon too, so the successor's own author matches.
		writeDecisionArtifact(t, mirrorDir, successorID, []string{"beta"})

		adapter := newAdapter(mirrorDir)
		files := []space.FileWrite{
			{Path: "decisions/" + id + ".md", Content: decisionDraft(t, mirrorDir, id, []string{"beta"})},
			{Path: "axon/events/2026/01J8QYK2Z3SUPRSDE0000001.yaml", Content: supersedeEvent(t, id, successorID, "axon")},
		}
		if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
			var violationErr *cli.ViolationError
			if errors.As(err, &violationErr) && residueHasCode(violationErr.Violations, "LFC-005") {
				t.Fatalf("expected no LFC-005 (successor's own author matches the acting actor), got %+v", violationErr.Violations)
			}
		}
	})

	t.Run("resolved_nonmatching_author_is_LFC005_alone", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		const id = "XD-axon-20260827-a1b2"
		const successorID = "XD-axon-20260827-s2c2"
		writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, id, "reject", "beta")
		// writeDecisionArtifact hardcodes `from: axon`; the acting actor
		// below is beta instead — a genuine author mismatch, resolved
		// successor, so exactly LFC-005 (never LFC-006, which is only for
		// the UNRESOLVED branch).
		writeDecisionArtifact(t, mirrorDir, successorID, []string{"beta"})

		adapter := newAdapter(mirrorDir)
		files := []space.FileWrite{
			{Path: "decisions/" + id + ".md", Content: decisionDraft(t, mirrorDir, id, []string{"beta"})},
			{Path: "axon/events/2026/01J8QYK2Z3SUPRSDE0000001.yaml", Content: supersedeEvent(t, id, successorID, "beta")},
		}
		err := adapter.ValidateSubmit(context.Background(), files)
		var violationErr *cli.ViolationError
		if !errors.As(err, &violationErr) {
			t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
		}
		if !residueHasCode(violationErr.Violations, "LFC-005") {
			t.Fatalf("expected LFC-005 among violations, got %+v", violationErr.Violations)
		}
		if residueHasCode(violationErr.Violations, "LFC-006") {
			t.Fatalf("expected NO LFC-006 (the successor WAS resolved, just failed the precondition), got %+v", violationErr.Violations)
		}
	})

	t.Run("unresolvable_successor_is_LFC005_plus_LFC006", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		const id = "XD-axon-20260827-a1b3"
		writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, id, "reject", "beta")
		// No successor artifact written at all: refs names an id this
		// resolver's own index can never contain.

		adapter := newAdapter(mirrorDir)
		files := []space.FileWrite{
			{Path: "decisions/" + id + ".md", Content: decisionDraft(t, mirrorDir, id, []string{"beta"})},
			{Path: "axon/events/2026/01J8QYK2Z3SUPRSDE0000001.yaml", Content: supersedeEvent(t, id, "XD-axon-20260827-gh57", "axon")},
		}
		err := adapter.ValidateSubmit(context.Background(), files)
		var violationErr *cli.ViolationError
		if !errors.As(err, &violationErr) {
			t.Fatalf("expected a *cli.ViolationError, got %T: %v", err, err)
		}
		if !residueHasCode(violationErr.Violations, "LFC-005") {
			t.Fatalf("expected LFC-005 among violations, got %+v", violationErr.Violations)
		}
		if !residueHasCode(violationErr.Violations, "LFC-006") {
			t.Fatalf("expected LFC-006 among violations (unresolvable successor), got %+v", violationErr.Violations)
		}
	})
}
