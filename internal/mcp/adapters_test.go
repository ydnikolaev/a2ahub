package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
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

func TestResolveActorFromRefusesAnonymousMCPWrites(t *testing.T) {
	t.Parallel()

	_, err := resolveActorFrom(ActorInput{}, ActorInput{}, "", noEnv)
	if !errors.Is(err, ErrNoActorName) {
		t.Fatalf("resolveActorFrom(empty) error = %v, want ErrNoActorName", err)
	}
	if !strings.Contains(err.Error(), "actor.name") || !strings.Contains(err.Error(), "A2A_ACTOR_NAME") {
		t.Fatalf("ErrNoActorName must name both MCP remedies, got: %v", err)
	}
}

func TestResolveActorFromUsesOSUserAsFinalFallback(t *testing.T) {
	t.Parallel()

	got, err := resolveActorFrom(ActorInput{}, ActorInput{}, "local-user", noEnv)
	if err != nil {
		t.Fatalf("resolveActorFrom(OS user): %v", err)
	}
	if got.Kind != "agent" || got.Name != "local-user" || got.Model != "" {
		t.Fatalf("resolved actor = %+v, want agent/local-user with no model", got)
	}
}

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

// TestAdaptersFileCarriesNoAcceptanceCriteriaDecode is P5's AC4 structural
// gate on this surface, mirroring internal/cli/adapters_test.go's own test
// of the same name: the parent-criteria resolution moved into
// internal/cache (ADR-004), so a future edit that re-added its own
// `m["acceptance_criteria"]` decode here would resurrect the sixth
// duplication instance this phase closed.
func TestAdaptersFileCarriesNoAcceptanceCriteriaDecode(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	if strings.Contains(string(raw), `["acceptance_criteria"]`) {
		t.Fatal("internal/mcp/adapters.go decodes acceptance_criteria directly — the resolution must live only in internal/cache (P5, ADR-004), never a second copy on a surface")
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

// TestSubmitValidatorAdapterRestrictedClassificationBilateralAccepted is
// this fix wave's own regression proof (no-silent-yes-2026-08/P3 stage 2 FIX
// B), mirroring internal/cli/adapters_test.go's own test of the same name.
// Before MirrorResolver implemented validate.ActiveParticipantLister on THIS
// surface, EVERY classification: restricted submission refused with
// POL-025/POL-026 (capability miss) regardless of whether the space was
// genuinely bilateral. A restricted artifact whose space's ACTIVE
// participants are exactly {from} ∪ to must be ACCEPTED once the capability
// is wired.
func TestSubmitValidatorAdapterRestrictedClassificationBilateralAccepted(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := testManifest() // {axon, beta}, both active
	legality := NewLegalityAdapter(t.TempDir(), "axon", manifest)
	resolver := NewMirrorResolver(t.TempDir(), manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k3f9\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + testFixtureThread + "\n" +
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
// is POL-024's own live proof on this surface, distinguishing a REAL
// bilateral violation from the capability-miss refusal the sibling test
// above closes: the same resolver now CAN enumerate active participants, and
// when the space's active membership genuinely exceeds {from} ∪ to, POL-024
// fires — not POL-025/POL-026 (the "cannot check" pair, which must NOT
// appear once the capability is wired).
func TestSubmitValidatorAdapterRestrictedClassificationExceedsBilateralRefused(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember}, {System: "third", Status: fold.MembershipMember},
	}}
	legality := NewLegalityAdapter(t.TempDir(), "axon", manifest)
	resolver := NewMirrorResolver(t.TempDir(), manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	artifactContent := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-k4g0\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + testFixtureThread + "\n" +
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
		t.Fatal("ValidateSubmit: expected POL-024 for a restricted artifact whose space's active participants ({axon, beta, third}) exceed {from} ∪ to ({axon, beta}), got nil")
	}
	var violationErr *ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *ViolationError, got %T: %v", err, err)
	}
	if !p1MCPHasViolationCode(violationErr.Violations, "POL-024") {
		t.Fatalf("ValidateSubmit refused, but not with POL-024: %+v", violationErr.Violations)
	}
	if p1MCPHasViolationCode(violationErr.Violations, "POL-025") || p1MCPHasViolationCode(violationErr.Violations, "POL-026") {
		t.Fatalf("ValidateSubmit fired POL-025/POL-026 (capability miss) even though MirrorResolver now implements ActiveParticipantLister: %+v", violationErr.Violations)
	}
}

// TestSubmitValidatorAdapterAcceptsContractBaselineFiles is the hermetic
// regression for the v0.16.0 full-live finding: a2a_submit correctly carried
// the scaffolded schema and fixtures, but this MCP adapter treated every
// non-event file as an envelope and fed the JSON schema to ParseFrontmatter.
// The CLI adapter already classified these files as contract baseline data.
func TestSubmitValidatorAdapterAcceptsContractBaselineFiles(t *testing.T) {
	t.Parallel()

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := testManifest()
	mirrorDir := t.TempDir()
	legality := NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	contract := []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XC-axon-widget\n" +
		"type: contract\n" +
		"title: Widget contract\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: 1.0.0\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"---\ncontract body\n")
	event := []byte("schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQRS\nspace: fixture-space\n" +
		"subject: XC-axon-widget\ntransition: publish\nactor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n")
	files := []space.FileWrite{
		{Path: "axon/provides/widget/contract.md", Content: contract},
		{Path: "axon/provides/widget/schema/widget.schema.json", Content: []byte(`{"type":"object"}`)},
		{Path: "axon/provides/widget/fixtures/valid/widget.json", Content: []byte(`{}`)},
		{Path: "axon/provides/widget/fixtures/invalid/widget.json", Content: []byte(`null`)},
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRS.yaml", Content: event},
	}

	if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
		t.Fatalf("ValidateSubmit with contract baseline: %v", err)
	}
}

// --- SubmitValidatorAdapter: events partition (P1/P5, REF-019/REF-023) ---
//
// MCP twin of internal/cli/adapters_test.go's own block of the same name —
// spec 01-the-write-gate-reaches-the-write.md §T1.
//
// # P5 closed the gap this block used to document
//
// Until rules-that-reach-2026-08 P5, internal/mcp's own MirrorResolver
// implemented validate.Resolver but NEITHER validate.ParentCriteriaCounter
// NOR validate.ParentCriteriaIDs NOR validate.ResponseParentResolver — not
// a duplicate of internal/cli's own capability, an ABSENCE of it. P1's own
// events-partition call to ValidateEventWithContext was correct and
// reached, but verdicts.go's checkVerdictIndexRange type-asserts the
// Resolver for those three capabilities before it can count a parent's
// declared criteria at all, so REF-019/REF-023 always degraded to "cannot
// check" and returned nil on this surface — an MCP-authored incomplete or
// out-of-range verdicts[] minted clean at submit
// (KI-02301-MCP-VERDICT-RESOLVER-GAP, now retired).
//
// P5 moved the capability into internal/cache (ADR-004) and gave this
// surface's own MirrorResolver thin delegations to it (adapters.go, gated
// at compile time by the var _ assertions immediately below ensureIndex),
// the same shape internal/cli's MirrorResolver already carried. The two
// tests below are the flipped proof: they used to assert the documented
// gap (a clean submit); they now assert the refusal, watched failing
// before this phase's fix and green after.

// p1MCPWriteParentWithTwoCriteria mirrors internal/cli's own
// p1WriteParentWithTwoCriteria.
func p1MCPWriteParentWithTwoCriteria(t *testing.T, mirrorDir, id string) {
	t.Helper()
	raw := "---\nschema: envelope/v1\nid: " + id + "\ntype: work_request\ntitle: t\nspace: fixture-space\n" +
		"from: axon\nto: [beta]\nthread: " + testFixtureThread + "\nactor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\npriority: p3\nblocking: true\nclassification: internal\n" +
		"category: feature\nproposed_change: x\nacceptance_criteria:\n  - \"first\"\n  - \"second\"\n" +
		"---\nbody\n"
	if err := os.WriteFile(filepath.Join(mirrorDir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// p1MCPCloseEvent mirrors internal/cli's own p1CloseEvent.
func p1MCPCloseEvent(eventID, parentID, tail string) []byte {
	return []byte("schema: event/v2\n" +
		"event: " + eventID + "\n" +
		"space: fixture-space\n" +
		"subject: " + parentID + "\n" +
		"transition: close\n" +
		"actor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n" +
		tail)
}

func p1MCPHasViolationCode(violations []validate.Violation, code string) bool {
	for _, v := range violations {
		if v.Code == code {
			return true
		}
	}
	return false
}

// TestSubmitValidatorAdapterEventsPartitionRefusesOutOfRangeVerdictIndex is
// P5's AC1 (renamed from
// ...CallsTheEntryPointButCannotResolveVerdictIndex, which used to assert
// the documented gap): with the capability now delegated to
// internal/cache, an out-of-range verdict index is refused with REF-019 on
// this surface too — mirrors internal/cli/adapters_test.go's own test of
// the same name.
func TestSubmitValidatorAdapterEventsPartitionRefusesOutOfRangeVerdictIndex(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p1a1"
	p1MCPWriteParentWithTwoCriteria(t, mirrorDir, parentID)
	manifest := testManifest()
	legality := NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	eventContent := p1MCPCloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQRT", parentID,
		"verdicts:\n  - index: 2\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRT.yaml", Content: eventContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected a refusal for an out-of-range verdict index, got nil — " +
			"an events-only submission must not write unjudged (P5: this surface now carries the same " +
			"capability internal/cli already did)")
	}
	var violationErr *ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *ViolationError, got %T: %v", err, err)
	}
	if !p1MCPHasViolationCode(violationErr.Violations, "REF-019") {
		t.Fatalf("ValidateSubmit refused, but not with REF-019: %+v", violationErr.Violations)
	}
}

// TestSubmitValidatorAdapterEventsPartitionRefusesIncompleteVerdicts is
// P5's AC2 (renamed from
// ...CallsTheEntryPointButCannotResolveVerdictCompleteness) — REF-023's
// own completeness half, riding the same call site as AC1.
func TestSubmitValidatorAdapterEventsPartitionRefusesIncompleteVerdicts(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p1a2"
	p1MCPWriteParentWithTwoCriteria(t, mirrorDir, parentID)
	manifest := testManifest()
	legality := NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	eventContent := p1MCPCloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQR9", parentID,
		"verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQR9.yaml", Content: eventContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected a refusal for an incomplete verdict set, got nil")
	}
	var violationErr *ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *ViolationError, got %T: %v", err, err)
	}
	if !p1MCPHasViolationCode(violationErr.Violations, "REF-023") {
		t.Fatalf("ValidateSubmit refused, but not with REF-023: %+v", violationErr.Violations)
	}
}

// p1MCPWriteResponseArtifact mirrors internal/cli/adapters_test.go's own
// writeResponseArtifact: a minimal response committed under
// mirrorDir/id.md carrying a `parent:` field — ParentOf's own read target.
func p1MCPWriteResponseArtifact(t *testing.T, mirrorDir, id, parentID string) {
	t.Helper()
	raw := "---\nid: " + id + "\ntype: response\nparent: " + parentID + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(mirrorDir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// p1MCPVerifyEvent mirrors p1MCPCloseEvent, transition verify — subject is
// the RESPONSE id, never the parent directly.
func p1MCPVerifyEvent(eventID, responseID, tail string) []byte {
	return []byte("schema: event/v2\n" +
		"event: " + eventID + "\n" +
		"space: fixture-space\n" +
		"subject: " + responseID + "\n" +
		"transition: verify\n" +
		"actor: {kind: agent, name: bot, system: axon}\n" +
		"at: 2026-07-21T10:00:00Z\n" +
		tail)
}

// TestSubmitValidatorAdapterEventsPartitionVerifyHopsToParentForVerdictCompleteness
// is P5's own regression for the ResponseParentResolver hop specifically.
// The two tests above use a `close` event, whose own subject already IS
// the parent — resolveOutOfRangeIndices resolves a count directly and
// ParentOf is never reached. A `verify` event's subject is the RESPONSE
// id, so checkVerdictIndexRange (verdicts.go) can only resolve a count by
// hopping through ResponseParentResolver.ParentOf first — the second of
// the three capabilities P5 moved, left unexercised by a close-event
// fixture alone.
func TestSubmitValidatorAdapterEventsPartitionVerifyHopsToParentForVerdictCompleteness(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	mirrorDir := t.TempDir()
	const parentID = "XW-axon-20260820-p5vh1"
	const responseID = "XS-beta-20260820-p5vh1"
	p1MCPWriteParentWithTwoCriteria(t, mirrorDir, parentID)
	p1MCPWriteResponseArtifact(t, mirrorDir, responseID, parentID)
	manifest := testManifest()
	legality := NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	eventContent := p1MCPVerifyEvent("01J8QYK2Z3ABCDEFGHJKMNPQRZ", responseID,
		"verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRZ.yaml", Content: eventContent},
	}

	err = adapter.ValidateSubmit(context.Background(), files)
	if err == nil {
		t.Fatal("ValidateSubmit: expected a refusal for an incomplete verdict set reached via the verify->parent hop, got nil")
	}
	var violationErr *ViolationError
	if !errors.As(err, &violationErr) {
		t.Fatalf("expected a *ViolationError, got %T: %v", err, err)
	}
	if !p1MCPHasViolationCode(violationErr.Violations, "REF-023") {
		t.Fatalf("ValidateSubmit refused, but not with REF-023: %+v", violationErr.Violations)
	}
}

// TestSubmitValidatorAdapterEventsPartitionUnresolvableParentDegradesSilently
// is P1's AC8 on this surface.
func TestSubmitValidatorAdapterEventsPartitionUnresolvableParentDegradesSilently(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	mirrorDir := t.TempDir() // empty: the parent this event names was never committed
	manifest := testManifest()
	legality := NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	adapter := NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	eventContent := p1MCPCloseEvent("01J8QYK2Z3ABCDEFGHJKMNPQRW", "XW-axon-20260820-doesnotexist",
		"verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n")
	files := []space.FileWrite{
		{Path: "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRW.yaml", Content: eventContent},
	}

	if err := adapter.ValidateSubmit(context.Background(), files); err != nil {
		t.Fatalf("ValidateSubmit: %v, want an unresolvable parent to degrade to \"cannot check\" (nil), never a synthesized violation", err)
	}
}

// noEnv is the empty environment the actor cases above state explicitly.
// resolveActorFrom now runs agent detection, so reading the process
// environment would make every one of them depend on whichever agent harness
// ran the suite — green on a bare shell, red inside Claude Code.
func noEnv(string) string { return "" }

// TestMCPDetectionOutranksTheNameATooLInputCarries is the MCP half of the
// inversion, and this surface is where it matters most: every write here is
// authored by a model filling in a structured tool input, so `actor.name` is a
// field the agent literally chooses.
//
// That is how `kind: agent, name: codex` reached the getvisa space on a
// publish codex did not perform. With a detectable agent present, the typed
// name must lose.
func TestMCPDetectionOutranksTheNameATooLInputCarries(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		if k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}

	got, err := resolveActorFrom(ActorInput{Name: "codex"}, ActorInput{Name: "codex"}, "yuranikolaev", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if got.Name != "claude-code" || got.Kind != "agent" {
		t.Fatalf("resolved actor = %+v, want the DETECTED agent — a tool input must not be able to "+
			"attribute the write to a vendor that did not perform it", got)
	}
}

// TestMCPExplicitHumanSuppressesDetection keeps the escape hatch reachable
// from this surface too: `kind: human` is a claim about a person, not about
// which binary is running.
func TestMCPExplicitHumanSuppressesDetection(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		if k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}

	got, err := resolveActorFrom(ActorInput{Kind: "human", Name: "ydnikolaev"}, ActorInput{}, "yuranikolaev", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if got.Kind != "human" || got.Name != "ydnikolaev" {
		t.Fatalf("resolved actor = %+v, want the declared human identity", got)
	}
}

// --- P9: the MCP surface reaches the identical rule (spec 09, epic AC5) ---
//
// fb-20260820-d1e370's defect had an exact twin here: this adapter's own
// contract arm branched on space.IsContractBaselinePath, so a declared
// companion carried by `a2a_submit` (internal/mcp/tools_submit.go carries
// sidecars through the SAME collector) was accepted here and refused by the
// space's `validate --ci` — and a companion whose media type is
// text/markdown could reach artifact.ParseFrontmatter's HARD error, which
// aborts the whole submit rather than reporting a violation.

// p9Companion builds the incident's three fixtures for this surface: the
// descriptor with its inventory, its sidecars, the lifecycle event, and
// whatever the caller adds or drops.
func p9CompanionFiles(slug string, extraEntries []string, extra map[string]string, drop ...string) []space.FileWrite {
	descriptor := "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-axon-" + slug + "\n" +
		"type: contract\n" +
		"title: Widget contract\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: 1.0.0\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"artifacts:\n" +
		"  - {path: schema/" + slug + ".schema.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/" + slug + ".json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/" + slug + ".schema.json}\n" +
		"  - {path: artifacts/CHANGELOG.md, role: changelog, normative: false, media_type: text/markdown}\n"
	for _, e := range extraEntries {
		descriptor += "  - " + e + "\n"
	}
	descriptor += "---\ncontract body\n"

	dir := "axon/provides/" + slug + "/"
	files := map[string]string{
		dir + "contract.md":                      descriptor,
		dir + "schema/" + slug + ".schema.json":  `{"type":"object"}`,
		dir + "fixtures/valid/" + slug + ".json": `{}`,
		// A changelog is markdown by nature and has no frontmatter. There
		// is no other shape to write one in.
		dir + "artifacts/CHANGELOG.md": "# Changelog\n\n## 1.0.0\n\n- first publication\n",
		"axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRS.yaml": "schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQRS\nspace: fixture-space\n" +
			"subject: XC-axon-" + slug + "\ntransition: publish\nactor: {kind: agent, name: bot, system: axon}\n" +
			"at: 2026-07-21T10:00:00Z\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for _, d := range drop {
		delete(files, d)
	}
	out := make([]space.FileWrite, 0, len(files))
	for p, c := range files {
		out = append(out, space.FileWrite{Path: p, Content: []byte(c)})
	}
	return out
}

func newP9Adapter(t *testing.T) *SubmitValidatorAdapter {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	manifest := testManifest()
	mirrorDir := t.TempDir()
	return NewSubmitValidatorAdapter(validate.New(corpus), "axon",
		NewMirrorResolver(mirrorDir, manifest), NewLegalityAdapter(mirrorDir, "axon", manifest))
}

// TestSubmitValidatorAdapterP9CompanionParity is criterion 10: the MCP
// surface reaches the IDENTICAL verdict on all three fixtures — pass,
// undeclared (POL-013), declared-but-absent (REF-014) — the same codes
// internal/cli's own adapter and `validate --ci` produce, because all three
// call one shared function rather than three similar ones.
//
// TEETH: reverting this adapter's contract arm to
// space.IsContractBaselinePath and dropping its
// space.ContractCarriedMembership call makes every want-a-code case pass
// silently.
func TestSubmitValidatorAdapterP9CompanionParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files []space.FileWrite
		want  string
	}{
		{
			name:  "a declared companion is carried and accepted",
			files: p9CompanionFiles("widget-p", nil, nil),
		},
		{
			name: "an undeclared carried file is refused",
			files: p9CompanionFiles("widget-q", nil, map[string]string{
				"axon/provides/widget-q/artifacts/NOTES.md": "scratch\n",
			}),
			want: "POL-013",
		},
		{
			name:  "a declared entry whose file is not carried is refused",
			files: p9CompanionFiles("widget-r", nil, nil, "axon/provides/widget-r/artifacts/CHANGELOG.md"),
			want:  "REF-014",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := newP9Adapter(t).ValidateSubmit(context.Background(), tc.files)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ValidateSubmit: %v — a DECLARED companion must submit on this surface too", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a %s refusal, got nil", tc.want)
			}
			var verr *ViolationError
			if !errors.As(err, &verr) {
				t.Fatalf("expected a ViolationError carrying %s, got %T: %v — a HARD error aborts the whole submit instead of reporting a violation", tc.want, err, err)
			}
			var found bool
			for _, v := range verr.Violations {
				if v.Code == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected %s among the violations, got %+v", tc.want, verr.Violations)
			}
		})
	}
}

// TestAdaptersFileBranchesOnTheClassifierNotThePathPredicate is criterion 3
// on this surface: after spec 09 there is exactly ONE function deciding what
// a carried path is, and this file must consult it rather than branch on
// space.IsContractBaselinePath directly. The predicate itself stays in
// internal/space (it is not deleted in this phase, §T1) — what is forbidden
// is a READER holding its own opinion, which is how the two halves of one
// binary came to disagree about one path.
func TestAdaptersFileBranchesOnTheClassifierNotThePathPredicate(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	code := mcpCodeLinesOnly(string(raw))
	if strings.Contains(code, "space.IsContractBaselinePath(") {
		t.Fatalf("internal/mcp/adapters.go still branches on space.IsContractBaselinePath — it must ask space.ClassifyCarried* instead (spec 09 criterion 3)")
	}
	if !strings.Contains(code, "space.ClassifyCarriedBatch(") {
		t.Fatalf("internal/mcp/adapters.go must classify its batch through space.ClassifyCarriedBatch (spec 09 criterion 3)")
	}
	if !strings.Contains(code, "space.ContractCarriedMembership(") {
		t.Fatalf("internal/mcp/adapters.go must reach the same membership rule the CLI does (epic AC5)")
	}
}

// mcpCodeLinesOnly is internal/cli/adapters_test.go's codeLinesOnly twin:
// the two surfaces may not import each other (ADR-001), so this three-line
// helper is duplicated on purpose rather than promoted.
func mcpCodeLinesOnly(src string) string {
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
// structural-gate shape and internal/cli/adapters_test.go's identical test
// of the same name: US-3 makes a forgotten envelope a compile-time-visible
// zero-valued struct field, not a runtime error from a separate
// registration method a caller could skip — a future edit re-adding that
// method (under any name containing this identifier) would resurrect the
// exact side-channel this phase removed.
func TestAdaptersFileCarriesNoRegisterEnvelopeMethod(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	const banned = "Register" + "Envelope" // split so this guard's own source never matches its own check
	if strings.Contains(string(raw), banned) {
		t.Fatalf("internal/mcp/adapters.go still carries a %s method/reference — the envelope side-channel must stay removed (US-3)", banned)
	}
}

// writeDecisionArtifactMCP seeds a committed `decision` under mirrorDir's
// space-level decisions/ directory (§4.2's multi-party PlacementSpaceLevel
// shape — no owning system section), authored by from, requiring approvals
// from every id in approvers.
func writeDecisionArtifactMCP(t *testing.T, mirrorDir, id, from string, approvers []string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: decision\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
		"to: [" + strings.Join(approvers, ", ") + "]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-08-27T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"required_approvers: [" + strings.Join(approvers, ", ") + "]\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "decisions/"+id+".md", content)
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
	writeDecisionArtifactMCP(t, mirrorDir, id, "axon", []string{"beta"})
	writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")

	r := NewMirrorResolver(mirrorDir, testManifest())
	author, state, ok := r.Successor(id)
	if !ok {
		t.Fatalf("Successor(%q): ok = false, want true", id)
	}
	if author != "axon" {
		t.Fatalf("author = %q, want axon", author)
	}
	if state != "proposed" {
		t.Fatalf("state = %q, want proposed (one committed propose event)", state)
	}
}

// TestMirrorResolverSuccessorUnknownIDDegrades pins the "cannot resolve"
// discipline every other optional-capability method in this file follows:
// never a synthesized author/state, always ok=false.
func TestMirrorResolverSuccessorUnknownIDDegrades(t *testing.T) {
	t.Parallel()
	r := NewMirrorResolver(t.TempDir(), testManifest())
	if _, _, ok := r.Successor("XD-axon-unknown"); ok {
		t.Fatal("Successor on an unindexed id: ok = true, want false")
	}
}

// TestMirrorResolverSuccessorResolvesApprovedAcrossSections is D-1+D-2's
// own proof on the MCP surface (this wave's report, "on BOTH surfaces" —
// the whole point of the wave): a successor decision carrying a REAL
// `required_approvers` list and a FULL quorum of `approve` events resolves
// as `approved` through MirrorResolver.Successor — even though every
// approve event is committed under the APPROVING participant's OWN
// section (beta's, gamma's), never the successor id's own home system's
// section (axon's). Mirrors internal/cli/adapters_test.go's own identical
// proof — see that test's own doc comment for why D-1 and D-2 each alone
// already block this exact, realistic scenario.
func TestMirrorResolverSuccessorResolvesApprovedAcrossSections(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const id = "XD-axon-20260827-q001"
	writeDecisionArtifactMCP(t, mirrorDir, id, "axon", []string{"beta", "gamma"})
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "propose", "axon")
	// Both approve events land under the APPROVING participant's OWN
	// section, never axon's (the successor id's own home system) — the
	// exact D-2 shape a single-section read cannot see.
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "approve", "beta")
	writeLifecycleEvent(t, mirrorDir, "gamma", 2, id, "approve", "gamma")

	r := NewMirrorResolver(mirrorDir, space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember}, {System: "gamma", Status: fold.MembershipMember},
	}})
	author, state, ok := r.Successor(id)
	if !ok {
		t.Fatalf("Successor(%q): ok = false, want true", id)
	}
	if author != "axon" {
		t.Fatalf("author = %q, want axon", author)
	}
	if state != "approved" {
		t.Fatalf("state = %q, want approved — D-1: RequiredApprovers must reach the folded envelope; "+
			"D-2: both approve events must resolve despite living under OTHER participants' own sections", state)
	}
}

// TestResolveSuccessorEnvelope is resolveSuccessorEnvelope's own direct
// unit coverage — this package's internal test (package mcp) reaches the
// unexported function directly, so every branch that leaves the result
// UNRESOLVED (nil) is pinned by name rather than only observed indirectly
// through a full ValidateSubmit call.
func TestResolveSuccessorEnvelope(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	const successorID = "XD-axon-20260827-t001"
	writeDecisionArtifactMCP(t, mirrorDir, successorID, "axon", []string{"beta"})
	resolver := NewMirrorResolver(mirrorDir, testManifest())

	cases := []struct {
		name       string
		resolver   validate.Resolver
		kind       string
		transition string
		refs       []string
		wantNil    bool
	}{
		{name: "non_supersede_transition_stays_nil", resolver: resolver, kind: "decision", transition: "reject", refs: []string{successorID}, wantNil: true},
		{name: "non_decision_kind_stays_nil", resolver: resolver, kind: "work_request", transition: "supersede", refs: []string{successorID}, wantNil: true},
		{name: "no_refs_stays_nil", resolver: resolver, kind: "decision", transition: "supersede", refs: nil, wantNil: true},
		{name: "resolver_lacks_capability_stays_nil", resolver: &fakeResolverNoSuccessor{}, kind: "decision", transition: "supersede", refs: []string{successorID}, wantNil: true},
		{name: "unresolvable_id_stays_nil", resolver: resolver, kind: "decision", transition: "supersede", refs: []string{"XD-axon-unknown"}, wantNil: true},
		{name: "resolvable_populates", resolver: resolver, kind: "decision", transition: "supersede", refs: []string{successorID}, wantNil: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveSuccessorEnvelope(tc.resolver, tc.kind, tc.transition, tc.refs)
			if tc.wantNil && got != nil {
				t.Fatalf("got %+v, want nil", got)
			}
			if !tc.wantNil {
				if got == nil {
					t.Fatal("got nil, want a populated *validate.SuccessorEnvelope")
				}
				if got.Author != "axon" {
					t.Fatalf("Author = %q, want axon", got.Author)
				}
			}
		})
	}
}

// fakeResolverNoSuccessor implements validate.Resolver only — never
// validate.SuccessorResolver — proving resolveSuccessorEnvelope degrades
// to nil rather than panicking on a type assertion when a caller's own
// Resolver lacks the capability.
type fakeResolverNoSuccessor struct{}

func (fakeResolverNoSuccessor) KnownArtifact(string) bool    { return false }
func (fakeResolverNoSuccessor) Digest(string) (string, bool) { return "", false }
func (fakeResolverNoSuccessor) System(string) (bool, bool)   { return false, false }

var _ validate.Resolver = fakeResolverNoSuccessor{}

// TestLegalityAdapterDecisionSupersedeSuccessorPrecondition is no-silent-
// yes-2026-08/P6's own consumer-side proof, driven directly against the
// REAL adapter: a rejected decision's supersede must be authored by the
// successor's own author; an approved decision's supersede must name an
// approved successor. Both rows: UNRESOLVED successor facts (nil
// SuccessorEnvelope) refuse, never a silent grant.
func TestLegalityAdapterDecisionSupersedeSuccessorPrecondition(t *testing.T) {
	t.Parallel()
	manifest := testManifest()

	t.Run("rejected_requires_successor_author", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		const id = "XD-axon-20260827-r001"
		writeLifecycleEvent(t, mirrorDir, "axon", 1, id, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "axon", 2, id, "reject", "beta")
		a := NewLegalityAdapter(mirrorDir, "axon", manifest)
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
		a := NewLegalityAdapter(mirrorDir, "axon", manifest)
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
