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

// verbAgreementReplay is P10's OWN incident-replay registry
// (answers-that-hold-2026-08 spec 10, "two verbs decide one predicate and
// disagree"): the exact same shape incidentReplay above already proved out
// — corpus evidence, the fix commit and its own parent, a reproducible
// archaeology command, the real finding that command produced during THIS
// wave, an existing ProofRef, and an honest BinaryReplayStatus — but a
// SEPARATE type and a SEPARATE function from incidentReplay/incidentReplays.
//
// # Why a second type rather than adding rows to incidentReplays()
//
// incidents_test.go's own TestIncidentReplayCount pins that registry's
// length to EXACTLY 3 ("spec 08 §6's own count... a closed, spec-named
// set"), and incidents_test.go is OFF this wave's allowlist — only
// scenarios_incidents.go is granted. Appending an eighth-instance row to
// incidentReplays() would either red that pinned count (if incidents_test.go
// is left alone, which this wave must) or require editing a file this wave
// may not touch. A second, sibling type in the SAME file — which this
// wave's brief does grant — carries the identical discipline without
// touching either the existing type or the test that already judges it.
// Reported as a deviation rather than silently worked around.
//
// # Why documentary replay, not a live executable assertion
//
// Every instance below is SHIPPED. Calling today's binary-backed functions
// cannot reproduce the disagreement they name — that is what "shipped"
// means — so, exactly as scenarios_incidents.go's own doc comment argues
// for its three incidents, a live parameterised switch reproducing the old
// answer would be a SECOND IMPLEMENTATION of the bug, proving only that the
// switch agrees with itself. Each ArchaeologyCommand below was actually run
// against the real FixCommitParent during this wave (read-only `git show`,
// never a checkout — the same constraint scenarios_incidents.go's own doc
// comment states and the same reason: concurrent sibling waves share this
// checkout). Each entry's own disagreement is then proved by feeding the
// RECORDED pre-fix facts through the SAME assertDirectionalCell /
// assertReaderAgreement functions a live cell would use
// (scenarios_verb_agreement_test.go) — never by re-executing historical
// code.
type verbAgreementReplay struct {
	// ID is this registry's own stable identifier.
	ID string
	// Name is the disagreement's own short name.
	Name string
	// Class is one of the three pairClass constants (verbclasses.go).
	Class pairClass
	// Instances names the epic README's own fb-... id(s) this entry
	// discharges — one entry may cover more than one id when a single fix
	// commit closed a single root cause reported twice (d31acb/f9cfac).
	Instances    []string
	FixedByPhase string
	// CorpusEvidence is a real, on-disk feedback record this entry's own
	// gate proves exists.
	CorpusEvidence     string
	FixCommit          string
	FixCommitParent    string
	ArchaeologyCommand string
	ArchaeologyFinding string
	ProofRefs          []string
	BinaryReplayStatus string
	// PreFixLeftVerb/PreFixLeftAccepted/PreFixRightVerb/PreFixRightRefused
	// carry the pre-fix disagreement's own facts (established by
	// ArchaeologyCommand/ArchaeologyFinding above) in the SAME typed shape
	// assertDirectionalCell consumes, for Class in
	// {pairPreviewingActing, pairActingChecking}. Left/Right are empty and
	// PreFixReaderVerdicts is populated instead when Class ==
	// pairReaderReader.
	PreFixLeftVerb     string
	PreFixLeftAccepted bool
	PreFixRightVerb    string
	PreFixRightRefused bool
	// PreFixReaderVerdicts carries the pre-fix per-reader facts for
	// Class == pairReaderReader — reader name to its own verdict (true =
	// treats the input as legitimate; false = flags it as a problem).
	PreFixReaderVerdicts map[string]bool
}

// verbAgreementReplays returns this phase's own replay registry — one entry
// per fix commit, covering all EIGHT of the epic README's corrected C3
// instances (fb-20260801-457629, fb-20260806-3539ac, fb-20260806-c6ad38,
// fb-20260808-5c73a9, fb-20260812-d31acb, fb-20260812-f9cfac,
// fb-20260820-d1e370, fb-20260827-a84550). Every FixCommit/FixCommitParent
// pair and every ArchaeologyFinding below was verified against this
// repository's own history during this wave — `git show <sha>[:<path>]`,
// read-only, reproducible by anyone with this checkout.
func verbAgreementReplays() []verbAgreementReplay {
	return []verbAgreementReplay{
		{
			ID:              "advertiser-legality-note-submitted",
			Name:            "note mints an event the space validator rejects",
			Class:           pairPreviewingActing,
			Instances:       []string{"fb-20260801-457629"},
			FixedByPhase:    "fix(lifecycle): keep notes transition-free",
			CorpusEvidence:  "feedback/inbox/fb-20260801-457629.yaml",
			FixCommit:       "ccddb6c00d9bc987b5146bb0d8b83a7cbf04037c",
			FixCommitParent: "b8dee2748459e286ab45483f8858b8a9fc77548e",
			ArchaeologyCommand: "git show b8dee2748459e286ab45483f8858b8a9fc77548e:internal/fold/table.go | grep -n 'TNote'; " +
				"git show b8dee2748459e286ab45483f8858b8a9fc77548e:internal/cli/cmd_lifecycle.go | sed -n '/func (c \\*NoteCommand) Run/,/^}/p'",
			ArchaeologyFinding: "table.go already commented TNote as \"transition-free (D-025); never in this table\" but " +
				"fold.CheckCandidate (legality.go) had NO branch for it — so for kind=response, " +
				"currentState=submitted, transition=note, the generic transitionTable lookup found no row " +
				"and returned VerdictIllegalTransition (LFC-001), exactly the incident's own reproduction. " +
				"NoteCommand.Run (cmd_lifecycle.go) called lifecycleLoadEnvelope (existence probe only), " +
				"minted the event, and called submit unconditionally — no fold call at all, for any state — " +
				"so the ADVERTISER accepted (exit 0, \"note: opened PR ...\") for every state while the " +
				"space's own required check (running the identical CheckCandidate) rejected it. The fix " +
				"(diff of internal/fold/legality.go in ccddb6c0) adds `if transition == TNote { ... return " +
				"VerdictLegal }` ahead of the table lookup, making the actor's own local answer agree with " +
				"the CLI's.",
			ProofRefs:          []string{"internal/fold/legality_test.go:TestCheckLegalityNote"},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, per this file's own (a)-vs-(b) doc comment; a checkout of FixCommitParent is the same operation scenarios_incidents.go's own three entries forbid mid-wave.",
			PreFixLeftVerb:     "a2a note (advertiser)",
			PreFixLeftAccepted: true,
			PreFixRightVerb:    "fold.CheckLegality (space's required V2 lifecycle check)",
			PreFixRightRefused: true,
		},
		{
			ID:              "verify-export-descriptor-reserialization",
			Name:            "verify-export against a staged candidate can never match its published version",
			Class:           pairActingChecking,
			Instances:       []string{"fb-20260827-a84550"},
			FixedByPhase:    "P2 wave 1b (answers-that-hold-2026-08), 939c9c92",
			CorpusEvidence:  "feedback/inbox/fb-20260827-a84550.yaml",
			FixCommit:       "939c9c92388bd777a721503e57a34510f0be357a",
			FixCommitParent: "0ca94fc8dc8f5497ed367194828f985f94292c4a",
			ArchaeologyCommand: "git show 0ca94fc8dc8f5497ed367194828f985f94292c4a:cmd/a2a/contract_p6_wiring.go | " +
				"sed -n '/func (c \\*contractP6Core) verifyExport(/,/^}/p'",
			ArchaeologyFinding: "the versioned form of verifyExport called space.ReadContractCandidate directly on the " +
				"RAW staged tree (the author's own bytes: version: 0.0.0, 2-space indent) and digested that " +
				"against historical.PublishedDigest — the PUBLISH path, by contrast, re-serializes the " +
				"descriptor through finalizePublicationDescriptor (setYAMLScalar + yaml.Marshal), changing " +
				"the version field and the indentation. So the same candidate tree publish had just accepted " +
				"could never digest-match what verify-export computed over the raw candidate: the ACTOR " +
				"(publish) accepted, and the CHECKER (verify-export) refused the identical input. The fix " +
				"(939c9c92) routes verifyExport through the SAME c.freezePublicationCandidate helper publish " +
				"uses, so both now read identical, finalized descriptor bytes.",
			ProofRefs:          []string{"cmd/a2a/contract_p6_wiring_test.go:TestContractVerifyExportRoundTrip"},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, same constraint as this registry's other entries.",
			PreFixLeftVerb:     "a2a contract publish",
			PreFixLeftAccepted: true,
			PreFixRightVerb:    "a2a contract verify-export --local <staging> <id>@<version>",
			PreFixRightRefused: true,
		},
		{
			ID:              "data-package-readme-reader-disagreement",
			Name:            "inbox/outbox and doctor disagree with the validator over a packed data-package README",
			Class:           pairReaderReader,
			Instances:       []string{"fb-20260812-d31acb", "fb-20260812-f9cfac"},
			FixedByPhase:    "fix(agent-exchange): one data-package path grammar, for every reader",
			CorpusEvidence:  "feedback/inbox/fb-20260812-d31acb.yaml",
			FixCommit:       "c600b00ad4209d935849b58aeaef2e7edd02023c",
			FixCommitParent: "f5bb800c95a08ead04a436202e8a0fc481a9d4ae",
			ArchaeologyCommand: "git show f5bb800c95a08ead04a436202e8a0fc481a9d4ae:internal/cache/mirror.go | grep -c IsDataPackageReadmePath; " +
				"git show f5bb800c95a08ead04a436202e8a0fc481a9d4ae:internal/cli/visibility.go | grep -c DataPackageReadme",
			ArchaeologyFinding: "both greps returned 0 — at the fix's own parent commit, neither walkArtifacts " +
				"(internal/cache/mirror.go) nor the doctor visibility scan (internal/cli/visibility.go) " +
				"recognised a data package's own README.md path shape at all, while the validator already did " +
				"(P4 AC8, shipped earlier as fb-20260808-5c73a9's own fix): one reader (the validator, and the " +
				"space's own CI) treated <system>/data/<DP-id>/README.md as a legitimate, frontmatter-free " +
				"payload; two other readers of the identical file (the mirror decoder feeding `a2a " +
				"inbox`/`a2a outbox`, and doctor's repository-visibility scan) treated it as an artifact that " +
				"\"could not be decoded\" — one UNVERIFIED classification, one standing false-positive skip " +
				"note. The fix adds artifact.IsDataPackageReadmePath (internal/artifact/paths.go) and routes " +
				"BOTH remaining readers through it, so the same predicate everywhere replaces one reader " +
				"holding its own opinion.",
			ProofRefs: []string{
				"internal/cache/mirror_test.go:TestWalkArtifacts_DataPackageReadmeIsNotASkippedFile",
				"internal/cli/visibility_test.go:TestDoctorVisibilityDataPackageReadmeIsNotUNVERIFIED",
			},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, same constraint as this registry's other entries.",
			PreFixReaderVerdicts: map[string]bool{
				"validator (a2a validate --ci / merge gate)": true,
				"mirror decoder (a2a inbox / a2a outbox)":    false,
				"doctor repository-visibility scan":          false,
			},
		},
		{
			ID:                 "data-package-payload-pol-002",
			Name:               "a package the tool wrote fails the tool's own check",
			Class:              pairActingChecking,
			Instances:          []string{"fb-20260808-5c73a9"},
			FixedByPhase:       "fix(agent-exchange): a package the tool wrote passes the tool's own check",
			CorpusEvidence:     "feedback/inbox/fb-20260808-5c73a9.yaml",
			FixCommit:          "ddcad0165c09cdeddaefdf428f2b922350c7ff7b",
			FixCommitParent:    "614c922482a5d99935908142f53133904cd39b3f",
			ArchaeologyCommand: "git show 614c922482a5d99935908142f53133904cd39b3f:internal/cli/cmd_validate_ci.go | grep -c 'isDataPackagePayloadPath\\|DataPackageForPath'",
			ArchaeologyFinding: "0 — at the fix's own parent commit, artifact discovery in cmd_validate_ci.go had no " +
				"notion of a data package's own payload shape at all: `a2a data pack` (the ACTOR) wrote " +
				"README.md without frontmatter by construction (its bytes are sealed by the package's " +
				"manifest.json digest), and `a2a validate --ci` / POL-002 (the CHECKER) treated every `.md` " +
				"under a member section as a frontmatter-bearing artifact, refusing the tool's own output. " +
				"The fix adds isDataPackagePayloadPath (backed by space.DataPackageForPath) to both " +
				"artifact-discovery sites in that file.",
			ProofRefs:          []string{"internal/cli/cmd_validate_ci_test.go:TestValidateCI_PRDataPackagePayloadPassesByConstruction"},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, same constraint as this registry's other entries.",
			PreFixLeftVerb:     "a2a data pack",
			PreFixLeftAccepted: true,
			PreFixRightVerb:    "a2a validate --ci / the space's required check (POL-002)",
			PreFixRightRefused: true,
		},
		{
			ID:                 "contract-baseline-artifacts-arm-missing",
			Name:               "no contract declaring a companion artifact can be published",
			Class:              pairPreviewingActing,
			Instances:          []string{"fb-20260806-c6ad38"},
			FixedByPhase:       "fix(schemas): let a contract that declares a companion be published",
			CorpusEvidence:     "feedback/inbox/fb-20260806-c6ad38.yaml",
			FixCommit:          "f072bb1345189ffc28b9a1b8908a5d753245ee34",
			FixCommitParent:    "62fee5c035563da5be89867a49ef9b00aa3c6536",
			ArchaeologyCommand: "git show 62fee5c035563da5be89867a49ef9b00aa3c6536:internal/space/layout.go | grep -c artifacts",
			ArchaeologyFinding: "0 — at the fix's own parent commit, ContractForPath's switch admitted only the " +
				"descriptor, schema/** and fixtures/** shapes and returned false for everything else, so " +
				"IsContractBaselinePath was false for every artifacts/** path. `a2a contract preflight` (the " +
				"PREVIEW) planned the publication successfully, plan digest and all, while `a2a contract " +
				"publish` (the ACTOR) died in PrepareSubmission on \"missing or malformed frontmatter " +
				"delimiters\" for the exact same companion file — the preview passed where the actor refused. " +
				"The fix adds an `artifacts` arm to ContractForPath's switch (:143) and a matching " +
				"Layout.ProvidesArtifactsDir constructor.",
			ProofRefs:          []string{"internal/space/layout_test.go:TestContractForPathOwnsDescriptorAndBaselineShape"},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, same constraint as this registry's other entries.",
			PreFixLeftVerb:     "a2a contract preflight",
			PreFixLeftAccepted: true,
			PreFixRightVerb:    "a2a contract publish",
			PreFixRightRefused: true,
		},
		{
			ID:                 "submit-carries-unvalidated-companion",
			Name:               "submit carries a declared companion that validate --ci rejects, unseen locally",
			Class:              pairActingChecking,
			Instances:          []string{"fb-20260820-d1e370"},
			FixedByPhase:       "feat(cli): submit validates every file it carries, and --ci judges the same set (d321f47c)",
			CorpusEvidence:     "feedback/inbox/fb-20260820-d1e370.yaml",
			FixCommit:          "d321f47cc4d7486a645c9d010005b56a6368fae2",
			FixCommitParent:    "ccdd8be144bfbcfff6091bbf681d5d8b75d37d25",
			ArchaeologyCommand: "git show ccdd8be144bfbcfff6091bbf681d5d8b75d37d25:internal/cli/cmd_validate_ci.go | grep -c 'descriptors.classify'",
			ArchaeologyFinding: "0 — at the fix's own parent commit, discovery classified a companion purely by its " +
				"`.md` suffix, three lines below a space.ContractForPath call whose answer it discarded. " +
				"`a2a submit` (the ACTOR) validated only the descriptor and carried the declared companion " +
				"unvalidated; the space's own required `validate --ci` (the CHECKER) then rejected the SAME " +
				"companion with POL-002 after the PR was already open. The fix (d321f47c) routes discovery " +
				"through descriptors.classify(p) in both cmd_submit's own path and cmd_validate_ci.go, so " +
				"submit and --ci classify the identical set before either one runs.",
			ProofRefs:          []string{"internal/cli/cmd_validate_ci_test.go:TestValidateCI_DeclaredCompanionIsNotAnArtifact"},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, same constraint as this registry's other entries.",
			PreFixLeftVerb:     "a2a submit",
			PreFixLeftAccepted: true,
			PreFixRightVerb:    "a2a validate --ci / the space's required check (POL-002)",
			PreFixRightRefused: true,
		},
		{
			ID:                 "template-show-stale-generation",
			Name:               "contract publish planner refuses every contract the shipped template scaffolds",
			Class:              pairPreviewingActing,
			Instances:          []string{"fb-20260806-3539ac"},
			FixedByPhase:       "fix(cli): show the contract template publication actually accepts",
			CorpusEvidence:     "feedback/inbox/fb-20260806-3539ac.yaml",
			FixCommit:          "e01f8448e4b81f362582f9676186d03257993099",
			FixCommitParent:    "509976224b68faf6520096fa24c7e7b23d9ddb70",
			ArchaeologyCommand: "git show 509976224b68faf6520096fa24c7e7b23d9ddb70:internal/cli/cmd_new.go | grep -c ShowGeneration",
			ArchaeologyFinding: "0 — at the fix's own parent commit, `a2a template show contract` (the PREVIEW an " +
				"author follows) returned the historical envelope/v1 template unconditionally, while `a2a new " +
				"contract` already wrote envelope/v2 (carrying the top-level `artifacts:` inventory). An " +
				"author following the SHOWN template validated, submitted, passed the space's own CI and " +
				"auto-merged — every gate before the irreversible step accepted it — and only then was `a2a " +
				"contract preflight` (the ACTOR) permanently refused, naming a requirement no shown document " +
				"produced. The fix adds template.ShowGeneration so `show` prints the generation fresh " +
				"authoring actually renders.",
			ProofRefs:          []string{"internal/cli/cmd_new_test.go:TestTemplateShowPrintsTheAuthoringGeneration"},
			BinaryReplayStatus: "NOT RUN AS A BINARY — read-only git archaeology only, same constraint as this registry's other entries.",
			PreFixLeftVerb:     "a2a template show contract (preview of the accepted shape)",
			PreFixLeftAccepted: true,
			PreFixRightVerb:    "a2a contract preflight",
			PreFixRightRefused: true,
		},
	}
}
