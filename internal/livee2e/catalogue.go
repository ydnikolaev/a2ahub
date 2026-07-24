package livee2e

// The two participating systems (spec 36 §T1: two independent local
// checkouts, the axon/seomatrix shape). SystemA is backed by the
// provisioner identity, SystemB by the non-admin machine account — which is
// why every boundary scenario below is declared for SystemB only (§T5).
const (
	SystemA = "A"
	SystemB = "B"
)

// DefaultRepo is the test space's repository name inside the dedicated org.
// The harness creates and resets it, so it is a constant rather than an
// operator input: one less variable that can be pointed somewhere real.
const DefaultRepo = "live-e2e-space"

// Scenario declares one family of matrix rows: what is being tested, and the
// systems and surfaces it applies to.
type Scenario struct {
	Name string
	// Systems the scenario is meaningful for. Boundary scenarios list only
	// SystemB: an assertion authored by an admin proves nothing (§T5).
	Systems []string
	// Surfaces the scenario is driven through.
	Surfaces []Surface
}

// bothSurfaces is the CLI/MCP pair — P15's parity claim, exercised rather
// than asserted.
func bothSurfaces() []Surface { return []Surface{SurfaceCLI, SurfaceMCP} }

// cliOnly is for scenarios about GitHub's own semantics (protection, check
// naming, workflow refs). Those are properties of the space, not of the entry
// point, so running them twice would double the Actions spend to re-observe
// the same fact.
func cliOnly() []Surface { return []Surface{SurfaceCLI} }

// Catalogue is the declared live matrix (spec 36 §T3 + §8). Declaring it in
// untagged code means the intended coverage is visible, diffable and
// unit-testable BEFORE the live scenarios exist — and that a scenario dropped
// during implementation shows up as a not-run row rather than as silence.
//
// Each entry names the acceptance criterion it answers.
func Catalogue() []Scenario {
	return []Scenario{
		// AC-960.1 — the rig provisions and scaffolds its own space.
		{Name: "space-init", Systems: []string{SystemA}, Surfaces: cliOnly()},
		// AC-960.2 — the ordinary happy path, both systems, both surfaces.
		{Name: "submit-gate-merge", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces()},
		{Name: "lifecycle-transitions", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces()},
		{Name: "contract-publish-deprecate-retire", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces()},
		{Name: "cross-system-visibility", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces()},
		{Name: "validate-ci-both-modes", Systems: []string{SystemA}, Surfaces: cliOnly()},

		// AC-961.1 — host.CheckStatus resolves the REAL compound context
		// `a2a-validate / validate` (the P34 defect class).
		{Name: "checkstatus-compound-context", Systems: []string{SystemB}, Surfaces: cliOnly()},

		// AC-961.2 — protection blocks until green; a direct push is rejected.
		// SystemB only: the provisioner would sail through both.
		{Name: "protection-blocks-until-green", Systems: []string{SystemB}, Surfaces: cliOnly()},
		{Name: "direct-push-refused", Systems: []string{SystemB}, Surfaces: cliOnly()},

		// AC-961.3 — THE row: a cross-section PR from B stays RED after the
		// provisioner re-triggers the run (the v0.6.4 authorization bypass).
		{Name: "cross-section-retrigger-stays-red", Systems: []string{SystemB}, Surfaces: cliOnly()},

		// AC-961.4 — protection's reach, asserted in both directions.
		{Name: "protection-binds-participant", Systems: []string{SystemB}, Surfaces: cliOnly()},
		{Name: "protection-skips-provisioner", Systems: []string{SystemA}, Surfaces: cliOnly()},

		// AC-961.5 — a scopeless credential is refused at pre-flight, not by
		// a push rejection (the v0.6.3 class).
		{Name: "space-update-scope-preflight", Systems: []string{SystemB}, Surfaces: cliOnly()},
		{Name: "space-update", Systems: []string{SystemA}, Surfaces: cliOnly()},

		// AC-960.5 — a re-trigger after main moved must assert WHICH ref ran,
		// because GitHub can serve a stale merge commit (§T6-b).
		{Name: "executed-ref-not-stale", Systems: []string{SystemB}, Surfaces: cliOnly()},

		// Refusal paths that are cheap to keep honest live.
		{Name: "out-of-section-write-refused", Systems: []string{SystemB}, Surfaces: bothSurfaces()},
		{Name: "stale-write-floor-refused", Systems: []string{SystemB}, Surfaces: cliOnly()},
	}
}
