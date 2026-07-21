package validate

import (
	"os"
	"path/filepath"
	"testing"

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
	// REF-007: ref resolves, but is entirely unpinned.
	record(checkRefs(envelope{Refs: []refEntry{{Ref: "XC-axon-ingest"}}}, resolver))

	// REF-005: from != own system (non-decision type).
	record(checkAuthz(envelope{Type: "work_request", From: "seomatrix"}, "axon"))
	// REF-006: to includes an unknown system.
	record(checkAddressees(envelope{To: []any{"unknown-system"}}, resolver))
	// REF-006 (left branch): to includes a system marked left.
	record(checkAddressees(envelope{To: []any{"seomatrix"}}, resolver))

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
		Raw:  []byte("---\nschema: envelope/v2\nid: XW-axon-20260731-p9d3\n---\nbody\n"),
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	record(result.Violations)

	for _, code := range append(append(registry.CodesInClass("referential"), registry.CodesInClass("lifecycle")...), registry.CodesInClass("policy")...) {
		if !produced[code] {
			t.Errorf("registry code %q is never produced by any exercised path in this test", code)
		}
	}
}
