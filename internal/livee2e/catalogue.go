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
//
// NOT USED BY THE CATALOGUE YET. Spec §6 left "does MCP coverage reuse the CLI
// scenarios or need its own table" open and named the sequencing: decide after
// the first CLI matrix is green. It is not green yet, and nothing in the repo
// drives MCP over stdio — cmd/a2a/mcp_equivalence_test.go calls the registry's
// handlers in process, so a live MCP surface is a JSON-RPC client that does not
// exist. Declaring the nine MCP cells anyway would put `make live-e2e`
// permanently at exit 1, and a tier that is always red is a tier nobody reads.
// Kept here (and exercised by the tests) as the shape the follow-up restores.
func bothSurfaces() []Surface { return []Surface{SurfaceCLI, SurfaceMCP} }

// cliOnly is for scenarios about GitHub's own semantics (protection, check
// naming, workflow refs). Those are properties of the space, not of the entry
// point, so running them twice would double the Actions spend to re-observe
// the same fact — and, until the MCP driver exists, it is every scenario.
func cliOnly() []Surface { return []Surface{SurfaceCLI} }

// Catalogue is the declared live matrix (spec 36 §T3 + §8). Declaring it in
// untagged code means the intended coverage is visible, diffable and
// unit-testable BEFORE the live scenarios exist — and that a scenario dropped
// during implementation shows up as a not-run row rather than as silence.
//
// Each entry names the acceptance criterion it answers.
//
// Every entry is cliOnly() for now — see bothSurfaces() for why, and spec §10's
// 2026-07-24 amendment for the decision that made it so.
func Catalogue() []Scenario {
	return []Scenario{
		// AC-960.1 — the rig provisions and scaffolds its own space.
		{Name: "space-init", Systems: []string{SystemA}, Surfaces: cliOnly()},
		// AC-960.2 — the ordinary happy path, both systems.
		{Name: "submit-gate-merge", Systems: []string{SystemA, SystemB}, Surfaces: cliOnly()},
		{Name: "lifecycle-transitions", Systems: []string{SystemA, SystemB}, Surfaces: cliOnly()},
		{Name: "contract-publish-deprecate-retire", Systems: []string{SystemA, SystemB}, Surfaces: cliOnly()},
		{Name: "cross-system-visibility", Systems: []string{SystemA, SystemB}, Surfaces: cliOnly()},
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
		{Name: "out-of-section-write-refused", Systems: []string{SystemB}, Surfaces: cliOnly()},
		{Name: "stale-write-floor-refused", Systems: []string{SystemB}, Surfaces: cliOnly()},

		// AC-973.1 (spec 37) — the full producer<->consumer contract-
		// integrity story, both systems (A publishes/deprecates/retires, B
		// adopts/acks). Filed under SystemA: it is the one row this
		// scenario family produces, and A is the producer whose writes the
		// row's own narrative follows.
		{Name: "contract-integrity-registered-consumer", Systems: []string{SystemA}, Surfaces: cliOnly()},

		// AC-980.1 (spec 38 wave F) — Layer-1 rows for the five envelope
		// kinds nothing else in this matrix drives: the whole legal
		// lifecycle to a terminal state, against real GitHub, both
		// systems in play (A authors, B is the counterpart). Filed under
		// SystemA only, matching contract-integrity-registered-consumer's
		// own precedent above and this phase's own brief ("one row per
		// KIND, not per (kind x system)" — a second row in the other
		// direction would double the Actions spend to re-observe the
		// same fact).
		{Name: "question-lifecycle-to-closed", Systems: []string{SystemA}, Surfaces: cliOnly()},
		{Name: "work-request-lifecycle-to-closed", Systems: []string{SystemA}, Surfaces: cliOnly()},
		{Name: "handoff-lifecycle-to-accepted", Systems: []string{SystemA}, Surfaces: cliOnly()},
		{Name: "response-lifecycle-to-verified", Systems: []string{SystemA}, Surfaces: cliOnly()},
		{Name: "decision-lifecycle-to-approved", Systems: []string{SystemA}, Surfaces: cliOnly()},
	}
}
