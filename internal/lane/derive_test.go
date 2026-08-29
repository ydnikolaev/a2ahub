package lane

import "testing"

func TestDeriveUnclaimedPathRefuses(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	sel, refusals := Derive(decls, []string{"skill/a2ahub/loops.md"}, nil)
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if len(sel.Phases) != 0 {
		t.Fatalf("expected no phases selected, got %+v", sel.Phases)
	}
}

func TestDeriveUnionOfMultipleGates(t *testing.T) {
	decls := []Declaration{
		{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}},
		{Phase: "vet", Kind: KindScoped, Inputs: []string{"**/*.go"}},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	backing := map[string]PhaseBacking{
		"go-test": {BackedPatterns: map[string]bool{"**/*.go": true}},
	}
	sel, refusals := Derive(decls, []string{"internal/lane/derive.go"}, backing)
	if len(refusals) != 0 {
		t.Fatalf("unexpected refusals: %+v", refusals)
	}
	names := map[string]bool{}
	for _, p := range sel.Phases {
		names[p.Declaration.Phase] = true
	}
	if !names["go-test"] || !names["vet"] {
		t.Fatalf("expected the diff to select BOTH go-test and vet (union), got %v", names)
	}
	if names["readme-lint"] {
		t.Fatalf("readme-lint's input did not intersect the diff, should not be selected")
	}
}

func TestDeriveAlwaysAlwaysSelected(t *testing.T) {
	decls := []Declaration{
		{Phase: "classify-guard", Kind: KindAlways, Reason: "reads the whole tracked set"},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	backing := map[string]PhaseBacking{
		"readme-lint": {BackedPatterns: map[string]bool{"README.md": true}},
	}
	sel, refusals := Derive(decls, []string{"README.md"}, backing)
	if len(refusals) != 0 {
		t.Fatalf("unexpected refusals: %+v", refusals)
	}
	names := map[string]bool{}
	for _, p := range sel.Phases {
		names[p.Declaration.Phase] = true
	}
	if !names["classify-guard"] {
		t.Fatalf("KindAlways must always run, got %v", names)
	}
}

func TestDeriveNeverIsNeverSelected(t *testing.T) {
	decls := []Declaration{
		{Phase: "live-e2e", Kind: KindNever, Reason: "needs two credentials, network and a real GitHub space"},
	}
	sel, refusals := Derive(decls, []string{"schemas/envelope/v2/contract.schema.json"}, nil)
	if len(refusals) != 1 {
		// The path is genuinely unclaimed since KindNever has no Inputs.
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	for _, p := range sel.Phases {
		if p.Declaration.Phase == "live-e2e" {
			t.Fatalf("live-e2e (KindNever) must never be auto-selected")
		}
	}
}

func TestDeriveMatchedPathsRecorded(t *testing.T) {
	decls := []Declaration{{Phase: "go-test-scoped:./internal/notes/...", Kind: KindScoped, Inputs: []string{"releasenotes/**"}}}
	backing := map[string]PhaseBacking{
		"go-test-scoped:./internal/notes/...": {BackedPatterns: map[string]bool{"releasenotes/**": true}},
	}
	sel, refusals := Derive(decls, []string{"releasenotes/v0.7.0.yaml", "README.md"}, backing)
	if len(refusals) != 1 {
		t.Fatalf("README.md should be unclaimed, got %d refusals: %+v", len(refusals), refusals)
	}
	if len(sel.Phases) != 1 {
		t.Fatalf("got %d phases, want 1: %+v", len(sel.Phases), sel.Phases)
	}
	got := sel.Phases[0].Matched
	if len(got) != 1 || got[0].Path != "releasenotes/v0.7.0.yaml" || got[0].Pattern != "releasenotes/**" {
		t.Fatalf("Matched = %+v", got)
	}
}

// A lane-claims path must satisfy Derive's claim test WITHOUT the claiming
// gate being selected by it — the gate is KindAlways and is in the lane
// regardless. Discriminating: an ALWAYS gate with NO Claims must still refuse
// the same path, so the test fails if alwaysClaimBacked is deleted OR if it
// is widened to "any ALWAYS declaration claims everything".
func TestDeriveAlwaysClaimsSatisfyTheClaimWithoutSelecting(t *testing.T) {
	withClaims := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "judges commit subjects", Claims: []string{"docs/status.md"}},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	backing := map[string]PhaseBacking{
		"epic-drift": {BackedPatterns: map[string]bool{"docs/status.md": true}},
	}
	sel, refusals := Derive(withClaims, []string{"docs/status.md"}, backing)
	if len(refusals) != 0 {
		t.Fatalf("a lane-claims path must not be refused, got: %+v", refusals)
	}
	if len(sel.Phases) != 1 || sel.Phases[0].Declaration.Phase != "epic-drift" {
		t.Fatalf("expected only the ALWAYS gate selected, got %+v", sel.Phases)
	}
	if len(sel.Phases[0].Matched) != 0 {
		t.Fatalf("a claim must not register as a MATCH — that would read as though the path selected the gate: %+v", sel.Phases[0].Matched)
	}

	noClaims := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "judges commit subjects"},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	_, refusals = Derive(noClaims, []string{"docs/status.md"}, backing)
	if len(refusals) != 1 {
		t.Fatalf("an ALWAYS gate with no Claims must still leave the path unclaimed, got %d refusals: %+v", len(refusals), refusals)
	}
}

// TestDeriveSelectsAnUnbackedGateButStillRefuses pins the reconcile.go:87
// fence this phase must not cross: an over-declared, unbacked glob still
// SELECTS its gate (a diff touching it must still run the gate), but the
// path is not counted as JUDGED, so the refusal still fires. Selection and
// coverage are two different questions and this is the test that keeps them
// that way.
func TestDeriveSelectsAnUnbackedGateButStillRefuses(t *testing.T) {
	decls := []Declaration{
		{Phase: "release-notes-freshness", Kind: KindScoped, Inputs: []string{"internal/**"}},
	}
	sel, refusals := Derive(decls, []string{"internal/release/trusted_root.json"}, nil)
	if len(sel.Phases) != 1 || sel.Phases[0].Declaration.Phase != "release-notes-freshness" {
		t.Fatalf("an unbacked glob must still SELECT its gate, got %+v", sel.Phases)
	}
	if len(refusals) != 1 {
		t.Fatalf("an unbacked claim must still refuse (not silently judged), got %d: %+v", len(refusals), refusals)
	}
	if !containsAll(refusals[0].String(), `phase "release-notes-freshness"`, "internal/release/trusted_root.json") {
		t.Errorf("refusal does not name the phase and the path: %+v", refusals[0])
	}
}

// TestDeriveAndCoverageAgreeOnClaimedVerdict is the fact #5 regression test:
// the SAME (decls, backing, path) must produce the SAME claimed verdict from
// both entry points, for an unbacked claim and for a backed one — or
// `--derive` and `--verify` would contradict each other with no
// operator-visible resolution, which is exactly what derive.go's own
// comments warn against twice.
func TestDeriveAndCoverageAgreeOnClaimedVerdict(t *testing.T) {
	decls := []Declaration{
		{Phase: "release-notes-freshness", Kind: KindScoped, Inputs: []string{"internal/**"}},
	}
	path := "internal/release/trusted_root.json"

	// Unbacked: both must refuse.
	_, deriveRefusals := Derive(decls, []string{path}, nil)
	coverageRefusals := Coverage(decls, []string{path}, nil, nil)
	if len(deriveRefusals) != 1 || len(coverageRefusals) != 1 {
		t.Fatalf("unbacked claim must refuse in BOTH Derive (%d) and Coverage (%d)", len(deriveRefusals), len(coverageRefusals))
	}

	// Backed: both must accept.
	backing := map[string]PhaseBacking{"release-notes-freshness": {BackedPatterns: map[string]bool{"internal/**": true}}}
	_, deriveRefusals = Derive(decls, []string{path}, backing)
	coverageRefusals = Coverage(decls, []string{path}, nil, backing)
	if len(deriveRefusals) != 0 || len(coverageRefusals) != 0 {
		t.Fatalf("a backed claim must be accepted by BOTH Derive (%d refusals) and Coverage (%d refusals)", len(deriveRefusals), len(coverageRefusals))
	}
}
