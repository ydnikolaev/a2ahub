package validate

import (
	"os"
	"path/filepath"
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
	// REF-011 is emitted by the V3 base-to-head adapter in internal/cli. The
	// repository-wide error-history gate verifies that literal emitter against
	// this registry; mark the cross-package owner here without importing cli.
	if registry.Has("REF-011") {
		produced["REF-011"] = true
	}
	// REF-013: manifest authority map assigns one active login twice.
	record(checkManifestPolicy(manifestProbe{Participants: []manifestParticipantProbe{
		{System: "axon", Section: "axon/", Owners: []string{"alice"}, Status: "active"},
		{System: "matrix", Section: "matrix/", Owners: []string{"alice"}, Status: "active"},
	}}))
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

	for _, code := range append(append(registry.CodesInClass("referential"), registry.CodesInClass("lifecycle")...), registry.CodesInClass("policy")...) {
		if !produced[code] {
			t.Errorf("registry code %q is never produced by any exercised path in this test", code)
		}
	}
}
