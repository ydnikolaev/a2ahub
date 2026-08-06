package lane

// Corpus returns every phase that MUST carry a lane-inputs declaration:
// the Makefile's REPO_GATES list (after D-2's presence-gate filter) union
// scripts/verify.sh's top-level run_phase call sites (deduped by name).
//
// It does not include a Go package's derived "go-test-scoped:./pkg/..."
// phases — those are exempt from corpus membership by construction
// (D-2's exemption): a package directory that exists is always a valid
// phase to declare, so it never needs to appear in a fixed list.
func Corpus(root string) ([]Phase, error) {
	makePhases, _, err := corpusFromMakefile(root)
	if err != nil {
		return nil, err
	}
	verifyPhases, _, err := corpusFromVerify(root)
	if err != nil {
		return nil, err
	}
	phases := make([]Phase, 0, len(makePhases)+len(verifyPhases))
	phases = append(phases, makePhases...)
	phases = append(phases, verifyPhases...)
	return phases, nil
}
