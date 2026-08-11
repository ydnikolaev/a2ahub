// scenarios_incidents.go declares P8's AC4 registry (spec 08-paths.md §8:
// "the three live incidents replay offline and fail against the binary
// that shipped before their phase" — how to verify: "run at the parent
// commit").
//
// # Why this is a REGISTRY, not a declared Path row
//
// Spec 08's own "The rule" section states the two mechanisms as DIFFERENT
// on purpose: "Every scenario in P0-P7 lands as a declared path row, and
// the three live incidents land as fixture replays" — a path row for one
// universe, a fixture replay for the other. This file follows that split
// rather than blurring it, and the blur was checked and rejected before
// writing a single Path value:
//
//   - Adding a new id to ConformancePaths() (pathcatalogue_paths.go) would
//     be caught by pathdrivability_test.go's TestPathDrivabilityCoversEveryPath
//     — "every ConformancePaths() id is either in drivenPathIDs() or
//     undrivablePaths()" — and BOTH of those live in pathdrivability.go,
//     which is OFF this wave's allowlist. A new Path row with no
//     classification reds that gate; this wave cannot add the
//     classification to fix it. Confirmed by reading the test, not
//     inferred.
//   - Driving any of the three through the real/logic binary would need a
//     new leg in pathdriver_live.go (a driver function, exactly the shape
//     runPathContractBaseline/driveSimpleVerb/... already take) — also OFF
//     this wave's allowlist.
//
// So this wave's own mechanism is the one it can actually reach:
// a declared REGISTRY (same shape as undrivenScenarios(),
// scenariocoverage_test.go — a typed list a parser/gate reads, never a
// human-summarized total) naming, per incident: the corpus evidence, the
// commit that fixed it, the commit immediately before it (read-only git —
// `git show <sha>^:<path>`, never `git checkout`; this package's own
// harness discipline never mutates THIS repository's working tree), the
// archaeological finding that command produced, and where TODAY's binary's
// correct behaviour is already proven (an existing, named test in the
// package the fix actually shipped in — internal/validate, internal/cache,
// internal/pendency; none of those packages are on this wave's own
// allowlist to ADD tests to, so this registry POINTS AT what already
// proves the fix rather than re-proving it a second time in a package that
// cannot own the fix).
//
// # (a) vs (b), and why (a)
//
// The brief poses two honest shapes for "fails against the pre-phase
// binary": (a) assert today's behaviour, record the pre-phase failure
// ONCE as real evidence; (b) a behaviour switch parameterised to simulate
// the old answer. (b) is rejected: a switch that reproduces the bug is a
// SECOND IMPLEMENTATION of the bug, and it would pass whether or not the
// real pre-phase binary ever behaved that way — it proves the switch
// agrees with itself, nothing about the product's history. This file is
// (a): every ArchaeologyFinding below was produced by an actual command
// run during this wave (recorded in each entry, reproducible by anyone
// with this repository's history) against the ACTUAL commit that shipped
// before the fix — never invented, never "what we'd expect it to say".
//
// # The half this wave could NOT run, named rather than faked
//
// "Run at the parent commit" most naturally reads as: build the `a2a`
// binary at FixCommitParent, run it against the incident's own fixture,
// observe it fail. That specific act needs `git checkout` (or an
// equivalent detached build) of an old commit in THIS working tree, which
// the hard invariants this wave operates under forbid unconditionally —
// concurrent sibling waves share this checkout, and a detached build
// mid-wave would corrupt every one of them. Checked for an escape hatch
// before accepting the gap: `which -a a2a` found ONE installed binary
// (`/usr/local/bin/a2a`, reporting `0.19.9
// (1d039a522830c163a2a16c619ef683586bcf637a)`) — `git merge-base
// --is-ancestor 1d039a522830c163a2a16c619ef683586bcf637a HEAD` exits 1: that
// commit is not reachable from this checkout's HEAD at all (it is some
// other build, not this repository's history), so it cannot stand in for
// any of the three FixCommitParent values below. BinaryReplayStatus on
// every entry says so, by name, rather than silently reporting a status
// that implies the binary-level run happened.
package livee2e

// incidentReplay is one of the epic's three founding incidents (spec 08
// §6/§8 AC4), the phase that fixed it, and the evidence this wave could
// assemble without a git-mutating operation.
type incidentReplay struct {
	// ID is this registry's own stable identifier — lowercase, hyphenated,
	// matching pathcatalogue_paths.go's own Path.ID convention (never a
	// ConformancePaths() id; this registry is a different universe, see
	// this file's own doc comment).
	ID string
	// Name is the incident's own short name, as spec 08 §6 names it.
	Name string
	// FixedByPhase names the phase (and its own AC) whose landing closed
	// the incident.
	FixedByPhase string
	// CorpusEvidence is where the real incident is recorded in the review
	// corpus — a doc path this entry's own gate proves exists on disk.
	CorpusEvidence string
	// FixCommit is the full commit sha that shipped the fix, in THIS
	// repository's history.
	FixCommit string
	// FixCommitParent is FixCommit's own first parent — "the binary that
	// shipped before their phase" (spec 08 §6). Read-only git only
	// (`git show <FixCommitParent>[:<path>]`), never a checkout.
	FixCommitParent string
	// ArchaeologyCommand is the exact command this wave ran against
	// FixCommitParent to establish ArchaeologyFinding — copy-pasteable,
	// reproducible by anyone with this repository's history.
	ArchaeologyCommand string
	// ArchaeologyFinding is the REAL output that command produced during
	// this wave (or a faithful, literal description of it for a command
	// whose output is itself a shell "file not found" — never a predicted
	// or expected output; see this file's own (a)-vs-(b) doc comment).
	ArchaeologyFinding string
	// ProofRefs names, per "<path>:<TestFuncName>", the test(s) ALREADY
	// shipped in the package that owns the fix, proving today's binary
	// behaves correctly. This registry's own gate (incidents_test.go)
	// verifies each file exists and still defines that func name — the
	// rot check a stale citation would otherwise defeat silently.
	ProofRefs []string
	// BinaryReplayStatus states, in one sentence, whether this wave
	// executed the pre-phase BINARY itself (never partially true — either
	// a real binary at FixCommitParent ran and its output is recorded
	// here as ArchaeologyFinding, or it did not and this field says so by
	// name).
	BinaryReplayStatus string
}

// incidentReplays returns the three declared incidents, in the order spec
// 08 §6 lists them.
func incidentReplays() []incidentReplay {
	return []incidentReplay{
		{
			ID:             "operational-deadlock-named-owner",
			Name:           "the operational deadlock",
			FixedByPhase:   "P5 AC1 (specs/05-declared-nature.md)",
			CorpusEvidence: "docs/inbox/agent-exchange-review/01-mandate.md",
			// No single committed artifact id: the corpus records this
			// incident as a NARRATIVE (the axon/seomatrix contract-and-
			// consumer story), not one artifact — unlike incidents 2/3
			// below, which each have one.
			FixCommit:       "151ca155c630655bfb80c45fbb86272aba4202ac",
			FixCommitParent: "525a1b8c2016ecb7e1b2c3422a274396979ec031",
			ArchaeologyCommand: "git show 525a1b8c2016ecb7e1b2c3422a274396979ec031:internal/pendency/pendency.go | grep -ci operational; " +
				"git show 525a1b8c2016ecb7e1b2c3422a274396979ec031:internal/cache/mirror.go | grep -ci operational",
			ArchaeologyFinding: "both greps returned 0 — at the fix's own parent commit, neither " +
				"internal/pendency/pendency.go nor internal/cache/mirror.go contained the word " +
				"\"operational\" in any form. OperationalDebtOwed (the P-1 derivation: published " +
				"version + a registered consumer + a floor gate, with NO voluntary field required) " +
				"did not exist at all — a published contract with a registered, un-endpointed " +
				"consumer had no mechanism to name an owner, exactly the deadlock the corpus " +
				"records.",
			ProofRefs: []string{
				"internal/cache/operational_debt_test.go:TestBuildIndex_OperationalDebtOwed_P1CoreAssertion",
				"internal/cache/operational_debt_test.go:TestBuildIndex_OperationalDebtOwed_GatedOnFloor",
				"internal/pendency/pendency_test.go:TestBlockedNamesTheOwnerInsteadOfTheTarget",
			},
			BinaryReplayStatus: "VERIFIED 2026-08-11 BY RUNNING IT. The pre-phase binary was " +
				"built from FixCommitParent in a detached git WORKTREE — which touches no shared " +
				"working tree, and is the escape the implementing wave could not take because its " +
				"own invariants forbid checkout/stash/reset. `a2a contract activate --help` at that " +
				"commit answers `contract: unknown subcommand \"activate\"`, listing the eight " +
				"sub-verbs that did exist. The discharge for this incident was not merely unwired: " +
				"it was unspeakable. Today's binary accepts the verb and fails later on a missing " +
				"project config, which is a different error and the expected one outside a space. " +
				"The old note is kept below for the escape it correctly ruled out. " +
				"One already-installed `a2a` was found on this machine " +
				"(/usr/local/bin/a2a, 0.19.9, commit 1d039a522830c163a2a16c619ef683586bcf637a) but " +
				"`git merge-base --is-ancestor 1d039a522830c163a2a16c619ef683586bcf637a HEAD` exits " +
				"1 — that commit is not reachable from this checkout's own history at all, so it " +
				"cannot stand in for FixCommitParent. This is a lead-side act.",
		},
		{
			ID:                 "named-but-unsent-bytes-refused",
			Name:               "the named-but-unsent bytes",
			FixedByPhase:       "P4 AC1 (specs/04-possession.md)",
			CorpusEvidence:     "docs/inbox/agent-exchange-review/03-bytes-note.md",
			FixCommit:          "19f66c576733f856ff81bfed6f1ed15e34d9dadf",
			FixCommitParent:    "209f7952254becd60138d5daa04cc996727bf428",
			ArchaeologyCommand: "git show 209f7952254becd60138d5daa04cc996727bf428:internal/validate/possession.go",
			ArchaeologyFinding: "the command reported \"fatal: path 'internal/validate/possession.go' " +
				"exists on disk, but not in '209f7952254becd60138d5daa04cc996727bf428'\" — REF-017 " +
				"(the body-content scan that refuses a bare `sha256:` token no declared attachment " +
				"carries) did not exist as a file, let alone a check, at the fix's own parent commit. " +
				"A body naming bytes by digest in prose — the real `XW-axon-20260801-vet6` shape " +
				"(the artifact itself lives in a private space this checkout does not have cloned; " +
				"internal/validate/possession_test.go's own doc comment records the same constraint " +
				"and the same RECONSTRUCTION discipline this entry's ProofRefs rely on) — validated " +
				"clean at that commit.",
			ProofRefs: []string{
				"internal/validate/possession_test.go:TestPossession_AC1_IncidentReconstructionRefusedAtValidate",
			},
			BinaryReplayStatus: "VERIFIED 2026-08-11 BY RUNNING IT, in a detached worktree at " +
				"FixCommitParent. TWO observations, and the pair is the point: `a2a attach` answers " +
				"`unknown command \"attach\"` — there was no instrument to carry bytes with a " +
				"request at all — and `internal/validate/possession.go` does not exist at that " +
				"commit, with REF-017 appearing nowhere under internal/validate. So the incident " +
				"had neither a way to send the bytes nor a refusal for naming what could not be " +
				"resolved. An agent describing them in prose was not being careless; it was using " +
				"the only surface there was.",
		},
		{
			ID:             "silent-partial-names-blocker",
			Name:           "the silent partial",
			FixedByPhase:   "P6 AC1/AC2 (specs/06-incompleteness.md)",
			CorpusEvidence: "docs/features/active/agent-exchange-2026-08/specs/06-incompleteness.md",
			// The real committed artifact — XS-seomatrix-20260801-6vr2 —
			// lives in the same private space as incident 2's vet6 id;
			// this checkout does not have it cloned, the identical
			// constraint incident 2 above already names.
			FixCommit:       "aa4ed8ee87236a8b5fda3524219f42fcce00c71e",
			FixCommitParent: "3903f5e645fe46e94184c4dc10c7aefee7729d92",
			ArchaeologyCommand: "git show 3903f5e645fe46e94184c4dc10c7aefee7729d92:schemas/envelope/v2/response.schema.json; " +
				"git show d1016c5b3650b8efc800fddc1e7a85f6f28a1869:internal/pendency/pendency.go | grep -ci blockedbyowner",
			ArchaeologyFinding: "the first command reported the file did not exist at that commit " +
				"(envelope/v2/response had not shipped at all, so `unmet[]`/`blocked_by`/`standing` " +
				"had no schema to carry them and a bare `result: partial` with neither was legal). " +
				"The second command (run against the pendency fix's own, slightly earlier, parent " +
				"commit d1016c5b3650b8efc800fddc1e7a85f6f28a1869) returned 0 — BlockedByOwner did " +
				"not exist, so a blocked exchange's next-move derivation could only ever name the " +
				"TARGET of the block, never the actual blocker, which is the second half of this " +
				"same incident (a response reporting completion while nothing named who actually " +
				"owed the next move).",
			ProofRefs: []string{
				"internal/validate/incompleteness_test.go:TestD1_PartialWithoutStandingIsRefused",
				"internal/pendency/pendency_test.go:TestBlockedNamesTheOwnerInsteadOfTheTarget",
			},
			BinaryReplayStatus: "VERIFIED 2026-08-11, and this one needed no binary because the " +
				"absence is structural: `git show <FixCommitParent>:schemas/envelope/v2/response.schema.json` " +
				"reports the file does not exist at that commit. A response could not carry " +
				"`unmet[]` or `blocked_by` because the generation that declares them was not in " +
				"the corpus the binary embeds — schemas/embed.go compiles the corpus IN, so a " +
				"schema absent from the tree is absent from every binary built at it. Building one " +
				"to watch it fail would observe a fact the embed already settles. Two commits are " +
				"named because the schema half and the pendency half shipped separately, and both " +
				"parents predate their own fix.",
		},
	}
}
