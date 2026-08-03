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
	// Branch distinguishes separately adjudicated evidence branches of one
	// named scenario. Legacy matrix rows leave it empty; the P7 operational-
	// confidence catalogue uses the exact baseline/provider branch tokens
	// frozen by the evidence contract.
	Branch string
	// Optional permits an honest unverified result. It is deliberately a
	// declaration property rather than a verdict-side convention: only the
	// provider-setting branch of LE-OC-04 and unsupported-provider branch of
	// LE-OC-08 may set it.
	Optional bool
	// Assertions is the complete assertion set this scenario must prove. P7
	// runtime evidence is accepted only when each named assertion has an
	// explicit outcome; old matrix rows leave it nil.
	Assertions []string
	// Systems the scenario is meaningful for. Boundary scenarios list only
	// SystemB: an assertion authored by an admin proves nothing (§T5).
	Systems []string
	// Surfaces the scenario is driven through.
	Surfaces []Surface
	// Kinds names the internal/fold.Kind string value(s) (e.g. "contract",
	// "question") this row actually drives an envelope of. This is
	// AC-983.1's own association mechanism (spec 38 §T3): completeness.go's
	// gate needs SOME way to map a catalogue row to the kind(s) it covers,
	// and an explicit field here is preferred over parsing row NAMES for
	// the reason spec 38's own brief states — a name-parsing rule silently
	// stops working the first time someone renames a row, while a renamed
	// row that forgets to carry its Kinds along fails LOUDLY (the kind
	// simply drops out of coverage and the gate reds naming it).
	//
	// Left empty (nil) for a row that drives no envelope kind at all —
	// space-init, direct-push-refused, space-update(-scope-preflight):
	// these assert something about the SPACE or about protection, not
	// about a specific kind's lifecycle, and forcing a Kinds entry on them
	// would be noise the gate would have to specifically ignore.
	Kinds []string
	// Family names the run*Scenarios function (runner_live_test.go,
	// scenarios_*_live.go) that actually returns a Result for this row —
	// e.g. "happy" for runHappyScenarios, "boundary" for
	// runBoundaryScenarios. This is a SECOND explicit association mechanism,
	// alongside Kinds and for the identical reason (spec 38 §T3's own
	// argument, restated here because it applies even more directly): the
	// defect this field exists to catch (spec 46 postmortem, filed
	// 2026-07-27) is a row whose family was never added to driveFamilies'
	// hardcoded dispatch table, so it stayed not-run for a full 59-minute
	// live run before anyone noticed. An explicit field lets an UNTAGGED
	// test (familyset_test.go) catch that in milliseconds, before any
	// GitHub call — but only because the family is a field a reader can
	// read, not a fact parsing the row NAME would have to reconstruct. And
	// unlike Kinds, the row name genuinely cannot be parsed into it even in
	// principle: "space-init" is produced by the "happy" family,
	// "space-update" by "space", "decision-lifecycle-to-approved" by
	// "draft-family" — the family is a fact about the IMPLEMENTATION's own
	// file layout, not the scenario's own name, so a name-parsing rule here
	// would not just rot on a rename (Kinds' failure mode) — it would never
	// have worked in the first place.
	//
	// Must name a value from CanonicalFamilies() (familyset.go), with the
	// one documented exception FamilySpaceCIHealth carries (a row TestLiveMatrix
	// dispatches directly, never through driveFamilies' own per-family loop).
	Family string
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
// Critical write parity is declared on both surfaces; GitHub-administration
// and other host-only properties remain CLI-only.
func Catalogue() []Scenario {
	return append([]Scenario{
		// AC-960.1 — the rig provisions and scaffolds its own space. No
		// envelope kind: this row asserts the SPACE exists, not any
		// artifact's lifecycle.
		{Name: "space-init", Systems: []string{SystemA}, Surfaces: cliOnly(), Family: "happy"},
		// AC-960.2 — the ordinary happy path, both systems.
		{Name: "submit-gate-merge", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces(), Kinds: []string{"announcement"}, Family: "happy"},
		{Name: "lifecycle-transitions", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces(), Kinds: []string{"requirement"}, Family: "happy"},
		{Name: "contract-publish-deprecate-retire", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces(), Kinds: []string{"contract"}, Family: "happy"},
		{Name: "cross-system-visibility", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces(), Kinds: []string{"announcement"}, Family: "happy"},
		{Name: "validate-ci-both-modes", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "happy"},

		// AC-961.1 — host.CheckStatus resolves the REAL compound context
		// `a2a-validate / validate` (the P34 defect class).
		{Name: "checkstatus-compound-context", Systems: []string{SystemB}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "happy"},

		// AC-961.2 — protection blocks until green; a direct push is rejected.
		// SystemB only: the provisioner would sail through both.
		{Name: "protection-blocks-until-green", Systems: []string{SystemB}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "boundary"},
		// direct-push-refused writes a raw marker file straight to main,
		// never an envelope artifact — no Kinds entry.
		{Name: "direct-push-refused", Systems: []string{SystemB}, Surfaces: cliOnly(), Family: "refusal"},

		// AC-961.3 — THE row: a cross-section PR from B stays RED after the
		// provisioner re-triggers the run (the v0.6.4 authorization bypass).
		{Name: "cross-section-retrigger-stays-red", Systems: []string{SystemB}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "boundary"},

		// AC-961.4 — protection's reach, asserted in both directions.
		{Name: "protection-binds-participant", Systems: []string{SystemB}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "boundary"},
		{Name: "protection-skips-provisioner", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "boundary"},

		// AC-961.5 — a scopeless credential is refused at pre-flight, not by
		// a push rejection (the v0.6.3 class). Both rows are about the
		// SPACE's own config, not an envelope kind.
		{Name: "space-update-scope-preflight", Systems: []string{SystemB}, Surfaces: cliOnly(), Family: "space"},
		{Name: "space-update", Systems: []string{SystemA}, Surfaces: cliOnly(), Family: "space"},

		// The tier's own blind spot, made a row. Every other cell asserts a
		// check this matrix deliberately caused; NOTHING looked at the rest
		// of the space's CI. Twice on 2026-07-26 a real failure there — a
		// post-merge audit red on main, and a space left pinned to a tag that
		// did not exist — reached the operator by email while this matrix
		// reported 38/38 green. A tier that cannot see a repository it just
		// spent an hour writing to is reporting on less than it claims.
		//
		// Family: FamilySpaceCIHealth, not a CanonicalFamilies() member — see
		// that constant's own doc (familyset.go) for why: this is the one
		// cell TestLiveMatrix (runner_live_test.go) records directly,
		// deliberately AFTER driveFamilies returns, rather than through
		// driveFamilies' own per-family dispatch loop.
		{Name: "space-ci-has-no-unexplained-failures", Systems: []string{SystemA}, Surfaces: cliOnly(), Family: FamilySpaceCIHealth},

		// AC-960.5 — a re-trigger after main moved must assert WHICH ref ran,
		// because GitHub can serve a stale merge commit (§T6-b).
		{Name: "executed-ref-not-stale", Systems: []string{SystemB}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "boundary"},

		// Refusal paths that are cheap to keep honest live.
		{Name: "out-of-section-write-refused", Systems: []string{SystemB}, Surfaces: bothSurfaces(), Kinds: []string{"announcement"}, Family: "refusal"},
		{Name: "stale-write-floor-refused", Systems: []string{SystemB}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "refusal"},

		// AC-973.1 (spec 37) — the full producer<->consumer contract-
		// integrity story, both systems (A publishes/deprecates/retires, B
		// adopts/acks). Filed under SystemA: it is the one row this
		// scenario family produces, and A is the producer whose writes the
		// row's own narrative follows.
		{Name: "contract-integrity-registered-consumer", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"contract"}, Family: "contract-integrity"},

		// P4 (agent-ops-2026-07, spec 04) — the ROLLING WINDOW: several
		// version lines of one contract live at once, a consumer who
		// arrives mid-sunset, and a retirement that used to be
		// impossible. Same family as the row above because it is the same
		// producer<->consumer story one release on; filed under SystemA
		// for the same reason, A being the producer the narrative follows.
		//
		// This row exists for the three things no hermetic test can see:
		// the write funnel's own deduplication of a deprecation
		// announcement (Edge 3 exists BECAUSE that dedup drops a late
		// adopter), CI on a maintenance-line publish whose baseline is not
		// the globally-highest version, and a retire succeeding against
		// real branch protection while other versions are published.
		{Name: "contract-rolling-window", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"contract"}, Family: "contract-integrity"},

		// AC-980.1 (spec 38 wave F) — Layer-1 rows for the five envelope
		// kinds nothing else in this matrix drives: the whole legal
		// lifecycle to a terminal state, against real GitHub, both
		// systems in play (A authors, B is the counterpart). Filed under
		// SystemA only, matching contract-integrity-registered-consumer's
		// own precedent above and this phase's own brief ("one row per
		// KIND, not per (kind x system)" — a second row in the other
		// direction would double the Actions spend to re-observe the
		// same fact).
		{Name: "question-lifecycle-to-closed", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"question"}, Family: "submitted-family"},
		{Name: "work-request-lifecycle-to-closed", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"work_request"}, Family: "submitted-family"},
		{Name: "handoff-lifecycle-to-accepted", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"handoff"}, Family: "submitted-family"},
		{Name: "response-lifecycle-to-verified", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"response"}, Family: "submitted-family"},
		{Name: "decision-lifecycle-to-approved", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"decision"}, Family: "draft-family"},

		// AC-981.1 (spec 38 wave G, §T2 Layer 2) — one illegal transition
		// (or, for response, the unauthorized-actor case the brief itself
		// names as an equally valid Layer-2 shape) per envelope kind,
		// refused through the REAL path: `a2a <verb>` -> V2's local
		// fold.CheckLegality check -> refusal BEFORE the write funnel, so
		// no PR is ever opened. Every row asserts both halves: the CLI
		// cites the expected LFC- registry code, and the transition's own
		// deterministic branch (plus the run-wide PR count) did not grow.
		// Filed under SystemA, matching every Layer-1 row's own convention
		// above (scenarios_illegal_live.go).
		{Name: "contract-retire-without-deprecation-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"contract"}, Family: "illegal-transitions"},
		{Name: "requirement-satisfy-before-ack-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"requirement"}, Family: "illegal-transitions"},
		{Name: "question-accept-before-ack-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"question"}, Family: "illegal-transitions"},
		{Name: "work-request-accept-before-ack-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"work_request"}, Family: "illegal-transitions"},
		{Name: "decision-close-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"decision"}, Family: "illegal-transitions"},
		{Name: "handoff-verify-pass-before-ack-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"handoff"}, Family: "illegal-transitions"},
		{Name: "announcement-accept-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "illegal-transitions"},
		{Name: "response-dispute-by-non-owner-refused", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"response"}, Family: "illegal-transitions"},

		// AC-982.1/982.2/982.3 (spec 38 wave H, §T2 Layer 3) — failure and
		// recovery: the layer an automated fleet actually lives in. Filed
		// under SystemA, matching every Layer-1/Layer-2 row's own
		// convention above (scenarios_failure_recovery_live.go).
		//
		// five-xx-mid-write-injected-unknown-then-recovered's own Result
		// carries EvidenceClassInjectedFault (report.go) — the ONE row in
		// this whole matrix driven through a proxy in front of the real
		// GitHub API (spec 38 §6-Q1, plan D-G) rather than observing an
		// unscheduled failure directly. Declared here exactly like every
		// other row: the DISTINCT-evidence-class labelling lives in the
		// rendered report, not in a second catalogue shape.
		{Name: "five-xx-mid-write-injected-unknown-then-recovered", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "failure-recovery"},
		{Name: "interrupted-submit-retried-one-pr", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "failure-recovery"},
		{Name: "concurrent-writes-no-lost-write", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"announcement"}, Family: "failure-recovery"},

		// P46 (spec 46 §T3/§T4, OP-210) — `a2a thread` read back from BOTH
		// real participants after a full question/response chain. The
		// mechanism (transcript ordering, open-items resolution, your-move
		// computation) is covered hermetically already; the ONE thing only
		// two real GitHub identities can prove is that A and B, each
		// reading through their own local mirror and their own `--system`
		// identity, see the SAME conversation while correctly DISAGREEING
		// about whose move it is. Filed under SystemA only — the row
		// inherently drives BOTH systems (A authors and reads, B
		// acks/responds and reads), matching subfamResultFromErr's own
		// documented convention (scenarios_submitted_live.go) for a row
		// like this: a second declaration under SystemB would double the
		// Actions spend to re-observe the exact same conversation from the
		// other end, which is the coverage this row's own name already
		// claims.
		{Name: "thread-chain-reads-identically-from-both-sides", Systems: []string{SystemA}, Surfaces: cliOnly(), Kinds: []string{"question", "response"}, Family: "thread-chain"},
	}, OperationalConfidenceCatalogue()...)
}
