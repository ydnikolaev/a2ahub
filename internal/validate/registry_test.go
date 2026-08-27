package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// fakeResolver is a hand-written mock for the Resolver seam (rails: no
// codegen, 1-3 method interfaces). It never mutates after construction in
// these tests, but is a pointer type per the "mock with value receiver"
// anti-pattern guard (go-conventions.md anti-pattern #15).
type fakeResolver struct {
	known   map[string]bool
	digests map[string]string
	member  map[string]bool
	left    map[string]bool
}

func (f *fakeResolver) KnownArtifact(id string) bool { return f.known[id] }
func (f *fakeResolver) Digest(ref string) (string, bool) {
	d, ok := f.digests[ref]
	return d, ok
}
func (f *fakeResolver) System(system string) (member, left bool) {
	return f.member[system], f.left[system]
}

// registryClosureParticipantResolver is this file's own minimal Resolver
// that ALSO implements ActiveParticipantLister (classification.go) —
// exercising POL-024's own audience-exceeds branch needs a resolver that
// CAN answer the question; fakeResolver above deliberately cannot, which
// is what proves POL-025/POL-026's capability-miss branch instead.
type registryClosureParticipantResolver struct {
	active []string
}

func (registryClosureParticipantResolver) KnownArtifact(string) bool         { return true }
func (registryClosureParticipantResolver) Digest(string) (string, bool)      { return "", false }
func (registryClosureParticipantResolver) System(string) (member, left bool) { return true, false }
func (r *registryClosureParticipantResolver) ActiveParticipants() ([]string, bool) {
	return r.active, true
}

var _ Resolver = (*registryClosureParticipantResolver)(nil)
var _ ActiveParticipantLister = (*registryClosureParticipantResolver)(nil)

// fakeLegality is a hand-written mock for LegalityChecker: it always
// returns the configured verdict, regardless of the candidate.
type fakeLegality struct {
	verdict Verdict
	err     error
}

func (f *fakeLegality) CheckLegality(CandidateEvent) (Verdict, error) {
	return f.verdict, f.err
}

// TestRegistryClosure is AC row 8: every violation this package can emit
// carries a non-empty registry code, and every referential/lifecycle/
// policy registry row is actually emitted by some exercised path — no
// orphans in either direction. (Schema-class codes are covered by
// TestGoldenFixtures_Envelope / TestGoldenFixtures_EventManifestConsumes
// against the P2 fixture corpus; this test covers the REF-/LFC-/POL- rows
// this phase itself adds.)
func TestRegistryClosure(t *testing.T) {
	t.Parallel()

	registryRaw, err := os.ReadFile(filepath.Join(corpusRoot, "errors/v1/registry.yaml"))
	if err != nil {
		t.Fatalf("read registry.yaml: %v", err)
	}
	registry, err := schema.LoadRegistry(registryRaw)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}

	produced := map[string]bool{}
	record := func(vs []Violation) {
		for _, v := range vs {
			if v.Code == "" {
				t.Errorf("violation %+v has an empty code", v)
				continue
			}
			if !registry.Has(v.Code) {
				t.Errorf("violation carries unknown registry code %q: %+v", v.Code, v)
				continue
			}
			produced[v.Code] = true
		}
	}

	resolver := &fakeResolver{
		known: map[string]bool{
			"XC-axon-ingest": true,
		},
		digests: map[string]string{
			"XC-axon-ingest#deadbeef": "cafebabe", // deliberately mismatched
		},
		member: map[string]bool{
			"seomatrix": true,
		},
		left: map[string]bool{
			"seomatrix": true,
		},
	}

	// REF-001: id parses, but the filename doesn't match it.
	record(checkIDForm(envelope{ID: "XW-axon-20260731-p9d3"}, "axon/exchanges/WRONG-STEM.md"))
	// REF-002: id parses, filename matches, but section doesn't.
	record(checkIDForm(envelope{ID: "XW-axon-20260731-p9d3"}, "seomatrix/exchanges/XW-axon-20260731-p9d3.md"))
	// REF-001 (malformed grammar branch): id doesn't even parse.
	record(checkIDForm(envelope{ID: "XR-axon"}, "axon/exchanges/XR-axon.md"))

	// REF-003: ref doesn't resolve.
	record(checkRefs(envelope{Refs: []refEntry{{Ref: "XC-axon-unknown"}}}, resolver))
	// REF-004: ref resolves, pinned digest mismatches.
	record(checkRefs(envelope{Refs: []refEntry{{Ref: "XC-axon-ingest#deadbeef"}}}, resolver))
	// REF-008: id is known but the digest-pinned target can't be resolved
	// to verify (Digest returns found=false for this exact pinned ref).
	record(checkRefs(envelope{Refs: []refEntry{{Ref: "XC-axon-ingest#cafebabe"}}}, resolver))
	// REF-007: ref resolves, but is entirely unpinned.
	record(checkRefs(envelope{Refs: []refEntry{{Ref: "XC-axon-ingest"}}}, resolver))

	// REF-005: from != own system (non-decision type).
	record(checkAuthz(envelope{Type: "work_request", From: "seomatrix"}, "axon"))
	// REF-006: to includes an unknown system.
	record(checkAddressees(envelope{To: []any{"unknown-system"}}, resolver))
	// REF-006 (left branch): to includes a system marked left.
	record(checkAddressees(envelope{To: []any{"seomatrix"}}, resolver))

	// POL-024 (no-silent-yes-2026-08/P3 stage 2, spec 03 §8 AC 4/5): a
	// restricted artifact whose space's ACTIVE participants exceed
	// {from} ∪ to.
	record(checkClassificationBilateral(
		envelope{Classification: "restricted", From: "axon", To: []any{"seomatrix"}},
		&registryClosureParticipantResolver{active: []string{"axon", "seomatrix", "getvisa"}},
	))
	// POL-025 + POL-026 (D9's first consumer, spec 03 §8 AC 13): the
	// participant list cannot be resolved at all — `resolver` above
	// (fakeResolver) does not implement ActiveParticipantLister, exactly
	// the capability-miss shape no production Resolver closes yet.
	record(checkClassificationBilateral(
		envelope{Classification: "restricted", From: "axon", To: []any{"seomatrix"}},
		resolver,
	))

	// REF-009: resolvable parent and child disagree on thread.
	record(checkFork(envelope{Parent: "XC-axon-ingest", Thread: "thread:axon-20260731-bbbb"}, &fakeThreadResolver{
		known:   map[string]bool{"XC-axon-ingest": true},
		threads: map[string]string{"XC-axon-ingest": "thread:axon-20260731-aaaa"},
	}))
	// REF-010: a new thread is minted under another system's name.
	record(checkForeignMint(envelope{From: "axon", Thread: "thread:seomatrix-20260731-abcd"}, &fakeThreadResolver{
		exists: map[string]bool{},
	}))
	// REF-012: supersession crosses threads (warning).
	record(checkSupersedeThreadContinuity("thread:axon-20260731-aaaa", "thread:axon-20260731-bbbb"))
	// REF-020/REF-021 (CC-024/CC-025): the supersession graph check, exercised
	// directly against its own pure input rather than through a caller — see
	// CheckSupersessionGraph's doc comment for why this is a V3-only,
	// full-repo-collected check with no per-artifact call site in this
	// package. One fork and one cycle input, each producing its own code.
	record(CheckSupersessionGraph([]SupersedeLink{
		{Successor: "XW-axon-20260801-bbbb", Predecessor: "XW-axon-20260731-aaaa"},
		{Successor: "XW-axon-20260801-zzzz", Predecessor: "XW-axon-20260731-aaaa"},
	}))
	record(CheckSupersessionGraph([]SupersedeLink{
		{Successor: "XW-axon-20260801-cccc", Predecessor: "XW-axon-20260731-dddd"},
		{Successor: "XW-axon-20260731-dddd", Predecessor: "XW-axon-20260801-cccc"},
	}))
	// REF-011 is emitted by the V3 base-to-head adapter in internal/cli. The
	// repository-wide error-history gate verifies that literal emitter against
	// this registry; mark the cross-package owner here without importing cli.
	if registry.Has("REF-011") {
		produced["REF-011"] = true
	}
	// REF-013: manifest authority map assigns one active login twice.
	// REF-022: notification route names a participant absent from
	// participants[] — carried on the SAME probe (rather than a second
	// call) so the closure gate actually exercises checkManifestPolicy's
	// checkNotificationRoutes branch, which every prior probe left empty
	// (space-notify-2026-08 propagation-probe gap 3). The route itself is
	// otherwise shape-valid (channel/chat/events all present and
	// well-formed) so this call proves REF-022 alone, not POL-021 too.
	record(checkManifestPolicy(manifestProbe{
		Participants: []manifestParticipantProbe{
			{System: "axon", Section: "axon/", Owners: []string{"alice"}, Status: "active"},
			{System: "matrix", Section: "matrix/", Owners: []string{"alice"}, Status: "active"},
		},
		NotificationRoutes: []any{
			map[string]any{"channel": "telegram", "chat": "-1002034567890", "for": "ghost", "events": []any{"blocking"}},
		},
	}))
	// POL-021: a notification route is not well-formed against its own
	// declared shape (space-notify-2026-08 P1 §7, now Go policy because
	// space.schema.json is byte-frozen at row 16 of
	// schemas/published-v1.sha256) — an unknown key, exercised on its own
	// probe so this call proves POL-021 alone, not REF-022 too.
	record(checkManifestPolicy(manifestProbe{
		Participants: []manifestParticipantProbe{
			{System: "axon", Section: "axon/", Owners: []string{"alice"}, Status: "active"},
		},
		NotificationRoutes: []any{
			map[string]any{"channel": "telegram", "chat": "-1002034567890", "events": []any{"blocking"}, "frequency": "hourly"},
		},
	}))
	// REF-014: one declared carried file resolves as a symlink rather than
	// the exact regular bytes promised by the descriptor.
	ref014Input := validContractInput(V2)
	ref014Input.Snapshot.Files[0].Kind = "symlink"
	_, ref014, err := ValidateContractCarriedSet(ref014Input)
	if err != nil {
		t.Fatalf("ValidateContractCarriedSet REF-014 probe: %v", err)
	}
	record(ref014.Violations)
	// REF-016: a work subject must parse through the canonical thread/artifact
	// grammar before any repository resolution can accept it.
	ref016Input := validWorkCheckpointInput()
	ref016Input.Work.SubjectRef = "../../not-a-subject"
	record(ValidateWorkCheckpoint(ref016Input).Violations)

	// LFC-001 / LFC-002.
	lfc1, err := checkLifecycle([]CandidateEvent{{Subject: "XW-axon-20260731-p9d3", Transition: "respond"}}, &fakeLegality{verdict: VerdictIllegalTransition})
	if err != nil {
		t.Fatalf("checkLifecycle: %v", err)
	}
	record(lfc1)
	lfc2, err := checkLifecycle([]CandidateEvent{{Subject: "XW-axon-20260731-p9d3", Transition: "approve"}}, &fakeLegality{verdict: VerdictUnauthorizedActor})
	if err != nil {
		t.Fatalf("checkLifecycle: %v", err)
	}
	record(lfc2)
	// LFC-003: once the lead activates the reserved live-registry row, keep it
	// tied to the real contextual receipt emitter rather than marking it by
	// declaration. Before that atomic registry+fixture integration, the row is
	// intentionally absent and this conditional does nothing.
	if registry.Has("LFC-003") {
		lfc3, _ := checkEventReceipt(
			eventProducerProbe{},
			true,
			questionEvaluation(fold.StateNone, fold.TAcknowledge),
		)
		record(lfc3)
	}

	// POL-001: secret pattern in raw content.
	record(scanForSecrets([]byte("AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")))
	// POL-002: malformed frontmatter.
	record([]Violation{malformedFrontmatterViolation()})
	// POL-003: oversized artifact.
	record(checkAdmission(make([]byte, DefaultMaxArtifactBytes+1)))
	// POL-004: non-UTF-8.
	record(checkAdmission([]byte{0xff, 0xfe, 0x00}))
	// POL-005: unsupported schema version, exercised through the full
	// engine (it's raised inside runCommonEnvelope, not a standalone
	// helper).
	engine := mustEngine(t)
	result, err := engine.ValidateDraft(Draft{
		Path: "axon/exchanges/XW-axon-20260731-p9d3.md",
		Raw:  []byte("---\nschema: envelope/v3\nid: XW-axon-20260731-p9d3\n---\nbody\n"),
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	record(result.Violations)

	// POL-012/POL-016 are P0-reserved but land in the live registry only with
	// their lead-owned fixtures. Once present, exercise each through its real
	// emitter so registry closure cannot be satisfied by a hand-marked code.
	if registry.Has("POL-012") {
		probe := eventProducerProbe{}
		probe.ProducedBy.Tool = "a2a"
		probe.ProducedBy.Version = "not-semver"
		_, violation := firstPartyReceiptStatus(probe)
		if violation == nil {
			t.Fatal("firstPartyReceiptStatus did not emit reserved POL-012")
		}
		record([]Violation{*violation})
	}
	if registry.Has("POL-016") {
		emptyModel := ""
		probe := eventProducerProbe{}
		probe.Actor.Model = &emptyModel
		record(checkFirstPartyActor(probe))
	}
	// POL-013: after the authoritative rollout floor, a proposed contract
	// cannot select the legacy descriptor/event/digest profile.
	_, pol013, err := ValidateContractCarriedSet(legacyContractInput(V2, ContractCandidateProposed, "0.19.0"))
	if err != nil {
		t.Fatalf("ValidateContractCarriedSet POL-013 probe: %v", err)
	}
	record(pol013.Violations)
	// POL-014: a declared invalid fixture that validates contradicts the
	// contract's own executable conformance suite.
	record(ValidateContractConformance(contract.ConformanceResult{
		Mode: contract.ConformanceModeSuite, Outcome: contract.ConformanceSuiteInconsistent,
		Results: []contract.ConformanceCaseResult{{
			Path:     "fixtures/invalid/unexpectedly-valid.json",
			Expected: contract.ConformanceExpectedNonconformant, Actual: contract.ConformanceActualConformant,
		}},
	}))
	// POL-015: work is isolated to status announcements; the same contextual
	// validator is used at V2 and V3 (its dedicated test pins parity).
	pol015Input := validWorkCheckpointInput()
	pol015Input.Category = "notice"
	record(ValidateWorkCheckpoint(pol015Input).Violations)

	// POL-006: retire refused because a registered consumer hasn't acked.
	if v, _ := CheckRetirePrecondition(RetirePrecondition{
		Consumers: []RegisteredConsumer{{System: "seomatrix", Acked: false}},
	}); v != nil {
		record([]Violation{*v})
	}

	// POL-007: declared minor bump contradicts computed compatibility —
	// the mislabeled-minor compat fixture (§5.4b, CC-080; see compat_test.go
	// for the corpus's own dedicated coverage).
	mislabeledNewSchema, err := os.ReadFile(filepath.Join(corpusRoot, "fixtures/compat/mislabeled-minor/new.schema.json"))
	if err != nil {
		t.Fatalf("read mislabeled-minor/new.schema.json: %v", err)
	}
	mislabeledFixture, err := os.ReadFile(filepath.Join(corpusRoot, "fixtures/compat/mislabeled-minor/fixtures/valid/widget-1.json"))
	if err != nil {
		t.Fatalf("read mislabeled-minor/fixtures/valid/widget-1.json: %v", err)
	}
	pol007 := CheckComputedCompatibility(CompatInput{
		DeclaredBump:  "minor",
		PriorVersion:  "1.0.0",
		NewVersion:    "1.1.0",
		NewSchemas:    map[string][]byte{"schema/widget.schema.json": mislabeledNewSchema},
		PriorFixtures: map[string][]byte{"fixtures/valid/widget-1.json": mislabeledFixture},
	})
	if pol007.Violation != nil {
		record([]Violation{*pol007.Violation})
	}

	// POL-008: computed compatibility could not be evaluated — an
	// ambiguous fixture->schema mapping (D-E's fail-closed branch).
	pol008 := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas: map[string][]byte{
			"schema/a.schema.json": []byte(`{"type":"object"}`),
			"schema/b.schema.json": []byte(`{"type":"object"}`),
		},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`),
		},
	})
	if pol008.Violation != nil {
		record([]Violation{*pol008.Violation})
	}

	// POL-009: a JSON-Schema contract published with no baseline to
	// compute against (D-D).
	if v := CheckContractPublishable(PublishableInput{
		SchemaFormat: "json-schema-2020-12",
		ContractID:   "XC-axon-ingest",
	}); v != nil {
		record([]Violation{*v})
	}

	// POL-010: a frontmatter scalar still carries the template's own
	// unfilled placeholder token.
	record(checkUnfilledPlaceholders(map[string]any{
		"expected_response": map[string]any{"shape": "<what a good answer looks like>"},
	}))

	// REF-017 and POL-017: P4's possession rule. One body produces both —
	// a digest named in prose that no attachment carries (the refusal) and
	// a file tree enumerated with nothing declared (the warning) — which is
	// the real incident's own shape, not two contrived fixtures.
	record(checkPossession([]byte(
		"The bundle is sha256:"+strings.Repeat("a", 64)+" and contains:\n"+
			"- one.csv\n- two.csv\n- three.csv\n- four.csv\n"),
		map[string]any{"type": "work_request"}))

	// REF-018 and LFC-004: P6's incompleteness rules. This comment used to
	// say both were DORMANT under every shipped Resolver, and that is no
	// longer true — kept as history because the STUBS' reason for existing
	// did not change with the wiring. REF-018 and LFC-004 have had
	// production call sites since 2026-08-09: cli.MirrorResolver satisfies
	// ParentCriteriaCounter by a compile-time assertion, which closed four
	// construction sites at once (see incompleteness.go's own doc comment).
	//
	// The stubs stay because this gate asks a different question from
	// "is it wired": whether the code can be PRODUCED at all, from inputs
	// this package controls. A gate that depended on a caller wiring
	// something would go quiet the day that caller changed, which is the
	// "gate watching nothing" this corpus keeps calling worse than a red.
	record(checkUnmetIndexRange(
		envelope{Type: "response", Parent: "XW-axon-20260808-clos"},
		map[string]any{"unmet": []any{int64(9)}},
		&criteriaResolver{criteria: map[string]int{"XW-axon-20260808-clos": 1}},
	))
	// REF-019: P6 wave C's verdicts[] index-range mirror. This comment used
	// to give three reasons it was dormant — no Resolver on ValidateEvent,
	// and verify/close authored as event/v1 so nothing could carry the field.
	// BOTH were closed on 2026-08-10 (wave 25): `ValidateEventWithContext`
	// carries a Resolver and cmd_validate_ci.go offers one, and
	// VerifyCommand.Run authors event/v2 above the space floor.
	//
	// What remains true, and is narrower than "dormant": the standalone
	// `a2a close <parent-id>` verb still authors event/v1 (epic-backlog B24),
	// and internal/mcp's verify/close hardcode event/v1 unconditionally
	// (B22), so REF-019 fires on the CLI verify path and not on those two.
	// The stub's job is unchanged either way — it proves the code is
	// producible from this package's own inputs, independent of who calls.
	record(checkVerdictIndexRange(
		"XW-axon-20260808-clos",
		map[string]any{"verdicts": []any{map[string]any{
			"index": int64(9), "verdict": "met", "cause_owner": "axon",
		}}},
		&criteriaResolver{criteria: map[string]int{"XW-axon-20260808-clos": 1}},
	))
	// POL-018 (P5 AC3): the hand-rolled declaration block. Unlike the three
	// above this one is NOT dormant — engine.go's runCommonEnvelope reaches
	// it on both ValidateDraft and ValidateForSubmit, so both surfaces get
	// it through the one Engine. The stub is here for the same reason as
	// its neighbours: this gate proves the code is PRODUCIBLE from inputs
	// this package controls, so it cannot go quiet on the day a caller
	// changes. The forbidden key is derived, never listed — `category` is a
	// real field on the type being checked.
	if violations, err := checkDeclarationBlock(
		[]byte("```\ncategory = data\n```\n"), 2, "work_request",
	); err == nil {
		record(violations)
	}
	// POL-019 and POL-020 (P5 AC5): the two halves of the capability check,
	// and they are two codes rather than one with two severities precisely
	// because their consequences differ. A DECLARED set that excludes the
	// required mode is a checkable fact and refuses; an ABSENT set is
	// silence, which warns — P-1's rule that undeclared reads as neither
	// yes nor no, and POL-017's that only checkable facts refuse.
	record(CheckCapabilityMismatch("http_push", []string{"file"}))
	record(CheckCapabilityMismatch("http_push", nil))
	record(checkResidue(
		envelope{Type: "response", Parent: "XW-axon-20260808-clos"},
		map[string]any{"unmet": []any{int64(0)}},
		[]CandidateEvent{{Subject: "XW-axon-20260808-clos", Transition: "close"}},
	))

	// POL-022 (defects-fix-2026-08 P9): a handoff whose §16.2-required body
	// sections carry no content. The FIRST positive body check in this
	// package — possession and the declaration block both refuse a specific
	// wrong thing; this one requires a right thing to be present.
	// POL-023 (defects-fix-2026-08 P8): the runtime-pin-against-absent-
	// endpoint conjunction. A WARNING — either declaration alone is silent,
	// which is why the pair is the fact and neither half is.
	record(checkOperationalClaim(map[string]any{
		"x_binding":     map[string]any{"runtime_pinnable": true},
		"x_operational": []any{map[string]any{"name": "endpoint", "state": "absent"}},
	}))

	if violations, err := checkHandoffSections(
		[]byte("## Context\n\n## What was built\n\n## How to verify\n\n## How to operate\n\n## Limitations & next steps\n"),
		"handoff",
	); err == nil {
		record(violations)
	}

	// This loop's premise is that every referential/lifecycle/policy code is a
	// validate.Violation this package raises. That was true by accident until
	// 2026-08-21 — there was one emitter, so nobody had to say so — and
	// REF-024 ended it: a write-funnel refusal in internal/space, placed there
	// so BOTH write surfaces inherit it through funnel.Submit rather than each
	// mapping it separately (ADR-004; the epic's AC5). No path this test
	// exercises can produce it, and no path in this package ever will.
	//
	// So the loop asks the registry who raises a code instead of assuming.
	// A foreign-emitter code is SKIPPED AND NAMED, never silently dropped:
	// this test's whole job is to refuse a code nothing produces, and a
	// skip nobody can see would be the same hole one level up. Its own
	// evidence lives with its emitter — REF-024's is a declared conformance
	// path plus internal/space's funnel-seat tests.
	var foreign []string
	for _, code := range append(append(registry.CodesInClass("referential"), registry.CodesInClass("lifecycle")...), registry.CodesInClass("policy")...) {
		if emitter := registry.EmitterFor(code); emitter != "internal/validate" {
			foreign = append(foreign, code+" ("+emitter+")")
			continue
		}
		if !produced[code] {
			t.Errorf("registry code %q is never produced by any exercised path in this test", code)
		}
	}
	if len(foreign) > 0 {
		t.Logf("not covered here, raised outside internal/validate and proven by their own emitter: %s",
			strings.Join(foreign, ", "))
	}
}
