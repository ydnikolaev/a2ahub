// Package e2e — coverage.go implements the P26 coverage manifest (spec 26
// §1/§11): a table mapping every catalog CLI verb to real evidence — a
// verb-invoking txtar scenario OR a direct-construction Go test — or an
// explicit, justified skip. TestE2ECoverageParity (coverage_test.go) is the
// self-enforcing gate this manifest feeds; this file holds ONLY the data
// table and the pure (non-testing) resolution helpers so both the parity
// gate and its own teeth tests can drive them against real or synthetic
// fixtures alike.
//
// Two evidence kinds (spec 26 §11 amendment), never a third: `Txtar`
// resolves by reading testdata/t3/<file>.txtar and confirming the script
// actually invokes `a2a <verb>` (grepped); `GoTest` resolves via `go test
// -list ^TestXxx$ ./<owning-package>` (cccoverage_test.go's resolveTestRef,
// reused — never re-implemented here). `Skip` carries a one-line
// justification for a verb not yet covered (pending a later P26 wave) or
// permanently out of scope (mcp, update — spec 26 §1).
//
// A verb may have MORE THAN ONE manifest row (e.g. `feedback`'s 4 exec-txtar
// + 1 Go-test ref all join the manifest per spec 26 §11/§2's Feedback row;
// `submit`'s write-path Go test AND its CC-002-refusal txtar both resolve) —
// the parity gate only requires each row that EXISTS to resolve, and that
// every catalog verb have AT LEAST ONE row (evidence or skip).
package e2e

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tier vocabulary (spec 11 §AC9, the plan's own T1-T6 tiers): a coverage
// row's evidence either execs the BUILT `a2a` binary (tierT3) or drives its
// command directly, in-process (tierInProcess). tierInProcess is a real,
// explicit value — not the empty string — precisely so an evidence row that
// forgot to declare its tier (Tier == "") is distinguishable from one that
// correctly declared it does NOT reach the binary. No third tier token: a
// row either reaches T3 or it plainly says it doesn't.
const (
	tierT3        = "T3"
	tierInProcess = "in-process"
)

// coverageEntry is one manifest/skip-set row. Exactly one of Txtar, GoTest,
// Skip must be non-empty — see (coverageEntry).evidenceKind. An evidence row
// (Txtar or GoTest) must also declare Tier explicitly — see
// (coverageEntry).declaredTier.
type coverageEntry struct {
	Verb   string
	Action string // optional sub-action when the binary catalog exposes only the family
	Txtar  string // testdata/t3/<file>.txtar
	GoTest string // "internal/e2e.TestXxx"
	Skip   string // one-line justification
	Tier   string // tierT3 or tierInProcess; empty ONLY on a Skip row
}

// target is the exact command spelling the evidence must exercise. Most
// catalog rows are already complete (for example "contract preflight"). Work
// is intentionally catalogued as one top-level family, so its closed action
// set uses Action to prevent a generic `a2a work --help` row from standing in
// for seven independent transport paths.
func (e coverageEntry) target() string {
	return strings.TrimSpace(e.Verb + " " + e.Action)
}

// evidenceKind reports which of Txtar/GoTest/Skip is set, erroring if it is
// not EXACTLY one (an all-empty row is exactly what teeth check (iv) — an
// empty skip justification — must catch; a row with more than one field set
// is just as malformed and caught the same way).
func (e coverageEntry) evidenceKind() (string, error) {
	kinds := map[string]string{}
	if e.Txtar != "" {
		kinds["txtar"] = e.Txtar
	}
	if e.GoTest != "" {
		kinds["goTest"] = e.GoTest
	}
	if e.Skip != "" {
		kinds["skip"] = e.Skip
	}
	if len(kinds) != 1 {
		return "", fmt.Errorf("coverage entry for verb %q must have exactly one of Txtar/GoTest/Skip set (a skip needs a non-empty justification), got %d set", e.Verb, len(kinds))
	}
	for k := range kinds {
		return k, nil
	}
	panic("unreachable")
}

// declaredTier validates e's Tier claim for the already-resolved evidence
// kind: a "skip" row carries no evidence, so it must NOT declare a tier
// (Tier == ""); a "txtar" row must declare exactly tierT3 — a txtar scenario
// always execs the built binary via TestT3Scripts's testscript.Run, so
// tierT3 is the only truthful value and anything else (including "") is
// rejected; a "goTest" row must declare exactly one of tierT3/tierInProcess
// — an empty Tier here is the undeclared-tier bug this check exists to
// catch, so it is rejected the same as any other unrecognized value, never
// silently defaulted to either.
func (e coverageEntry) declaredTier(kind string) error {
	switch kind {
	case "skip":
		if e.Tier != "" {
			return fmt.Errorf("skip row for %q must not declare a Tier (no evidence to tier), got %q", e.Verb, e.Tier)
		}
		return nil
	case "txtar":
		if e.Tier != tierT3 {
			return fmt.Errorf("txtar row for %q must declare Tier: %q (a txtar scenario always execs the built binary by construction), got %q", e.target(), tierT3, e.Tier)
		}
		return nil
	case "goTest":
		if e.Tier != tierT3 && e.Tier != tierInProcess {
			return fmt.Errorf("goTest row for %q has no recognized Tier declaration (want %q or %q), got %q", e.target(), tierT3, tierInProcess, e.Tier)
		}
		return nil
	}
	panic("unreachable")
}

// coverageManifest is the P26 SSOT (spec 26 §1/§2/§11): every catalog CLI
// verb this wave (14a) resolved, mapped-or-skipped. Later waves (14c) flip
// skip rows to real evidence rows as families land — this wave's own
// mandate is GREEN via a fully justified skip-set (plan 26 wave 14a).
var coverageManifest = []coverageEntry{
	// --- Setup & health ------------------------------------------------
	{Verb: "connect", Txtar: "connect.txtar", Tier: tierT3},
	{Verb: "disconnect", Txtar: "setup_disconnect.txtar", Tier: tierT3},
	{Verb: "doctor", Txtar: "doctor.txtar", Tier: tierT3},
	{Verb: "init", Txtar: "init.txtar", Tier: tierT3},

	// --- Authoring -------------------------------------------------------
	{Verb: "new", Txtar: "new_validate.txtar", Tier: tierT3},
	{Verb: "space", Txtar: "space_init.txtar", Tier: tierT3},
	// `space update` needs a host (it opens a PR through the write funnel),
	// so it cannot be a txtar against the local fixture — it is covered at
	// the host-rig tier instead, the same way `submit` is (spec 35 AC-950.5).
	{Verb: "attach", GoTest: "internal/e2e.TestAttachVerbReachesTheBuiltBinary", Tier: tierT3},
	// `fetch` (P10, agent-exchange-2026-08 spec 10 §3 seam 6): a designated
	// top-level verb resolving ANY blob-shaped attachment, not a `data`
	// sub-action — carried no manifest row at all until this wave (spec 10
	// AC6's own instruction: "if the bijection gate does NOT demand it
	// automatically, that silence is itself a finding to file" —
	// TestE2ECoverageParity was observed red for exactly this reason before
	// this row was added).
	{Verb: "fetch", GoTest: "internal/e2e.TestFetchRoundTripsUnpinnedAttachment", Tier: tierT3},
	{Verb: "space", GoTest: "internal/e2e.TestHostLoopSpaceUpdate", Tier: tierT3},
	{Verb: "template", Txtar: "template_list.txtar", Tier: tierT3},
	{Verb: "validate", Txtar: "new_validate.txtar", Tier: tierT3},

	// --- Write funnel ------------------------------------------------
	// submit: direct-construction write-path Go test (spec 26 §11 amendment
	// — exec-unreachable), PLUS the exec-txtar covering the CC-002 local
	// refusal path (which runs before any network call, so it IS exec'able).
	{Verb: "submit", GoTest: "internal/e2e.TestSubmitDirectConstruction", Tier: tierInProcess},
	{Verb: "submit", Txtar: "submit_refusal.txtar", Tier: tierT3},
	{Verb: "sync", Txtar: "sync.txtar", Tier: tierT3},
	{Verb: "await", GoTest: "internal/e2e.TestAwaitThroughStubbedAwaitTarget", Tier: tierInProcess},

	// --- Read surface --------------------------------------------------
	{Verb: "contracts", Txtar: "search_contracts.txtar", Tier: tierT3},
	{Verb: "inbox", Txtar: "inbox_outbox.txtar", Tier: tierT3},
	{Verb: "outbox", Txtar: "inbox_outbox.txtar", Tier: tierT3},
	{Verb: "search", Txtar: "search_contracts.txtar", Tier: tierT3},
	{Verb: "show", Txtar: "show_thread.txtar", Tier: tierT3},
	{Verb: "statusline", Txtar: "statusline.txtar", Tier: tierT3},
	{Verb: "thread", Txtar: "show_thread.txtar", Tier: tierT3},

	// --- Lifecycle (all 19 OP-211 verbs — direct-construction, exec-
	// unreachable per spec 26 §11 amendment) ---------------------------
	{Verb: "accept", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	// answers-that-hold-2026-08 P1 repair: TestThreadJSONMarksTransitionFreeOnlyOnTransitionFreeEvents
	// (thread_transitionfree_test.go) drives `ack`/`accept`/`note` directly
	// through newHostRig — the REAL built binary, no host.FakeHost anywhere
	// in the file — already; the manifest simply never named it.
	{Verb: "accept", GoTest: "internal/e2e.TestThreadJSONMarksTransitionFreeOnlyOnTransitionFreeEvents", Tier: tierT3},
	{Verb: "ack", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "ack", GoTest: "internal/e2e.TestThreadJSONMarksTransitionFreeOnlyOnTransitionFreeEvents", Tier: tierT3},
	{Verb: "approve", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "block", GoTest: "internal/e2e.TestBlockUnblockDirectConstruction", Tier: tierInProcess},
	{Verb: "cancel", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "close", GoTest: "internal/e2e.TestCloseFromRespondedDirectConstruction", Tier: tierInProcess},
	// answers-that-hold-2026-08 P1 repair: TestT3CloseAndVerifyVerdictThroughRealBinary
	// (event_v2_binary_test.go) drives `close --verdict` through newHostRig —
	// the real built binary — already; the manifest simply never named it.
	{Verb: "close", GoTest: "internal/e2e.TestT3CloseAndVerifyVerdictThroughRealBinary", Tier: tierT3},
	{Verb: "decline", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "dispute", GoTest: "internal/e2e.TestRespondVerifyDisputeDirectConstruction", Tier: tierInProcess},
	{Verb: "note", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "note", GoTest: "internal/e2e.TestThreadJSONMarksTransitionFreeOnlyOnTransitionFreeEvents", Tier: tierT3},
	{Verb: "reject", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "respond", GoTest: "internal/e2e.TestRespondVerifyDisputeDirectConstruction", Tier: tierInProcess},
	// answers-that-hold-2026-08 P1 repair: TestP1AC5REF023RefusesAnIncompleteVerifyOnBothSurfaces
	// (ac5_ref023_test.go) drives `respond`/`verify` through newHostRig — the
	// real built binary — already; the manifest simply never named it.
	{Verb: "respond", GoTest: "internal/e2e.TestP1AC5REF023RefusesAnIncompleteVerifyOnBothSurfaces", Tier: tierT3},
	{Verb: "satisfy", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "start", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "supersede", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	// answers-that-hold-2026-08 P1 repair: TestLifecycleVerbsThroughWriteFunnel
	// constructs host.NewFakeHost in place of the production Host (a
	// stub-backed row under this phase's new obligation), and it was
	// `supersede`'s only row. blocked_by_owner.txtar already drives `a2a
	// supersede` through the REAL built binary and asserts its LFC-005/
	// LFC-006 refusal (`! exec`) — real-backed evidence this manifest simply
	// never named. It proves a REFUSAL, never a success path (this phase's
	// own residual-hole note, spec 01 §9): TestT3Scripts's Setup pins
	// A2A_GITHUB_API at a 403-only denial stub, so no write verb can succeed
	// through this harness at all (blocked_by_owner.txtar's own trailing
	// comment).
	{Verb: "supersede", Txtar: "blocked_by_owner.txtar", Tier: tierT3},
	{Verb: "unblock", GoTest: "internal/e2e.TestBlockUnblockDirectConstruction", Tier: tierInProcess},
	{Verb: "verify", GoTest: "internal/e2e.TestRespondVerifyDisputeDirectConstruction", Tier: tierInProcess},
	{Verb: "verify", GoTest: "internal/e2e.TestP1AC5REF023RefusesAnIncompleteVerifyOnBothSurfaces", Tier: tierT3},
	{Verb: "verify-fail", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "verify-pass", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},
	{Verb: "withdraw", GoTest: "internal/e2e.TestLifecycleVerbsThroughWriteFunnel", Tier: tierInProcess},

	// --- Contract ops ----------------------------------------------------
	// Two tiers, declared row by row rather than assumed by the section.
	// This header used to read "direct-construction, exec-unreachable", and
	// P11 wave C measured that as false: `check`, `materialize`, `preflight`,
	// `publish` and `retire` are all driven through the BUILT binary by
	// TestHostLoopContractFamily, and `activate`/`adopt`/`deprecate` gained
	// binary-tier REFUSALS in the same wave (spec 11 AC4). What remains
	// in-process is in-process because the evidence chosen for that row is,
	// not because the verb cannot be reached.
	//
	// P5 AC1's discharge. `contract activate`'s in-process evidence is in
	// internal/cli rather than here because the verb's whole subject is a set
	// of REFUSALS resolved from a descriptor and the space floor — a txtar
	// scenario would prove the happy path and none of the four rules that
	// make it correct.
	{Verb: "contract activate", GoTest: "internal/cli.TestContractActivate", Tier: tierInProcess},
	{Verb: "contract activate", GoTest: "internal/e2e.TestContractBinaryRefusalActivateBeforePublish", Tier: tierT3},
	{Verb: "contract adopt", GoTest: "internal/e2e.TestContractAdoptDirectConstruction", Tier: tierInProcess},
	{Verb: "contract adopt", GoTest: "internal/e2e.TestContractBinaryRefusalAdoptNonAdoptable", Tier: tierT3},
	{Verb: "contract check", GoTest: "internal/e2e.TestHostLoopContractFamily", Tier: tierT3},
	{Verb: "contract deprecate", GoTest: "internal/e2e.TestContractNewPublishDeprecateDirectConstruction", Tier: tierInProcess},
	{Verb: "contract deprecate", GoTest: "internal/e2e.TestContractBinaryRefusalDeprecateUnpublishedVersion", Tier: tierT3},
	{Verb: "contract diff", GoTest: "internal/e2e.TestContractDiffDirectConstruction", Tier: tierInProcess},
	// verify-published aggregates across EVERY connected space, so its
	// evidence is in-process: a two-space fixture is a construction the
	// shared t3 binary's single-space harness cannot stand up. The
	// multi-space criterion (spec 07 AC-7) is exactly what the second row
	// drives, through the command's own constructor.
	{Verb: "contract verify-published", GoTest: "internal/cli.TestContractVerifyPublishedAggregatesRowsAndResolvesVersion", Tier: tierInProcess},
	{Verb: "contract verify-published", GoTest: "internal/cli.TestContractVerifyPublishedTwoConnectedSpacesChecksBoth", Tier: tierInProcess},
	{Verb: "contract materialize", GoTest: "internal/e2e.TestHostLoopContractFamily", Tier: tierT3},
	{Verb: "contract new", GoTest: "internal/e2e.TestContractNewPublishDeprecateDirectConstruction", Tier: tierInProcess},
	{Verb: "contract preflight", GoTest: "internal/e2e.TestHostLoopContractFamily", Tier: tierT3},
	{Verb: "contract publish", GoTest: "internal/e2e.TestContractNewPublishDeprecateDirectConstruction", Tier: tierInProcess},
	{Verb: "contract publish", GoTest: "internal/e2e.TestHostLoopContractFamily", Tier: tierT3},
	{Verb: "contract retire", GoTest: "internal/e2e.TestContractRetireCleanUngatedDirectConstruction", Tier: tierInProcess},
	// The binary-tier REFUSAL for `retire` (a registered consumer that has
	// not acked) is step 6 of the same scenario. Wave A's refusal audit
	// missed it because the test's name carries no tier token at all — the
	// mirror image of the tier-lying names AC9 removes.
	{Verb: "contract retire", GoTest: "internal/e2e.TestHostLoopContractFamily", Tier: tierT3},
	{Verb: "contract verify-export", GoTest: "internal/e2e.TestContractVerifyExportLocalDirectConstruction", Tier: tierInProcess},

	// --- Data (spec 05a T1): pack|deliver|fetch|verify fold into ONE
	// catalog verb, "data" — unlike `contract`, DataCommand's sub-verbs are
	// NOT expanded into "data <sub>" catalog rows (catalog.go's own
	// catalogCLICommand mirrors `work`'s single-row precedent here, not
	// `contract`'s per-sub expansion), so cli.DataSubcommands() names are
	// not this manifest's verb keys. `pack` never syncs the mirror or
	// resolves a credential (cmd/a2a/wire.go's resolveDataDeps: it is
	// write-free by design, its own synopsis says so), so a pinned
	// <XC-id>@<version> this space never published refuses LOCALLY
	// (internal/space.ErrContractNotPublished), exec-able against the local
	// fixture before any GitHub host is ever reached. `deliver` and
	// `verify --pass` can each drive a write and so are unreachable from
	// TestT3Scripts's own fixture specifically (its Setup points
	// A2A_GITHUB_API at a 403-only denial stub, not a working fake host —
	// see txtar_test.go's own doc comment, corrected this wave, for the
	// general write-path picture) — covered instead by cmd/a2a's own
	// direct-construction dataCore tests (data_wiring_test.go).
	{Verb: "data", Txtar: "data_pack_refusal.txtar", Tier: tierT3},
	{Verb: "data", GoTest: "cmd/a2a.TestDataCorePackEndToEnd", Tier: tierInProcess},
	// The two-party host-rig loop (spec 05a §6.3): pack, deliver (one
	// commit, idempotent re-run), fetch (clean + divergent destination),
	// verify (judge-only and --record, both directions), and the
	// fail->supersede->pass->close loop against a real fake-host merge —
	// exercise no earlier row here reaches, since data_wiring_test.go's
	// own rows are direct-construction, never through the funnel. Its own
	// body never spells the exec seam directly — it drives every step
	// through setupDataLoop's *hostRig, whose newHostRig call lives in this
	// SAME file (data_loop_test.go), which is exactly the one-hop case
	// resolveGoTestExecSeam's same-file helper resolution exists for.
	{Verb: "data", GoTest: "internal/e2e.TestDataLoopFailSupersedePassCloseInboxNeverAccumulates", Tier: tierT3},

	// --- Ops & delivery --------------------------------------------------
	{Verb: "completion", Txtar: "ops_completion.txtar", Tier: tierT3},
	{Verb: "dashboard", Txtar: "ops_html.txtar", Tier: tierT3},
	{Verb: "html", Txtar: "ops_html.txtar", Tier: tierT3},
	{Verb: "notifications", GoTest: "internal/e2e.TestNotificationsStatusThroughStubbedBackend", Tier: tierInProcess},
	// `notify` is the SPACE plane, not the local one beside it: it reads a
	// space-repo checkout with no `.a2a/` at all and prints the messages a
	// push would produce. The evidence is a direct-construction test rather
	// than a txtar scenario because the T3 harness gives a connected PROJECT
	// as cwd, and this verb's whole premise is running inside the space
	// checkout itself, where no cache exists to connect to.
	{Verb: "notify", GoTest: "internal/cli.TestNotifyRender_NoCache", Tier: tierInProcess},
	{Verb: "serve", Txtar: "operational_commands.txtar", Tier: tierT3},
	{Verb: "skill", Txtar: "ops_skill.txtar", Tier: tierT3},
	{Verb: "version", Txtar: "ops_version.txtar", Tier: tierT3},
	{Verb: "adapt", Txtar: "adapt.txtar", Tier: tierT3},
	{Verb: "docs", Txtar: "docs.txtar", Tier: tierT3},
	{Verb: "whatsnew", Txtar: "whatsnew.txtar", Tier: tierT3},

	// --- Operational work family (OP-224) ------------------------------
	// The binary catalog exposes `work` as one family. Action-qualified rows
	// make every closed subcommand carry behavior-level evidence. The six
	// mutations resolve to the exact production-wiring host integration test;
	// a direct fake backend or missing-argument usage line is deliberately not
	// executable evidence for buildCommands/buildWorkCommand. That evidence
	// calls run() in-process (never exec.Command), so it declares
	// tierInProcess despite driving a real fake host + real git space.
	// Status remains a real successful built-binary offline path.
	{Verb: "work", Action: "start", GoTest: "cmd/a2a.TestWorkProductionWiringMutations", Tier: tierInProcess},
	{Verb: "work", Action: "heartbeat", GoTest: "cmd/a2a.TestWorkProductionWiringMutations", Tier: tierInProcess},
	{Verb: "work", Action: "resume", GoTest: "cmd/a2a.TestWorkProductionWiringMutations", Tier: tierInProcess},
	{Verb: "work", Action: "checkpoint", GoTest: "cmd/a2a.TestWorkProductionWiringMutations", Tier: tierInProcess},
	{Verb: "work", Action: "wait", GoTest: "cmd/a2a.TestWorkProductionWiringMutations", Tier: tierInProcess},
	{Verb: "work", Action: "stop", GoTest: "cmd/a2a.TestWorkProductionWiringMutations", Tier: tierInProcess},
	{Verb: "work", Action: "status", Txtar: "operational_commands.txtar", Tier: tierT3},

	// --- Feedback (P25) — spec 26 §2 Feedback row: this phase only
	// registers P25's own evidence, 4 exec-txtar + 1 Go-test ref. --------
	{Verb: "feedback", Txtar: "feedback_new_validate.txtar", Tier: tierT3},
	{Verb: "feedback", Txtar: "feedback_intake_guards.txtar", Tier: tierT3},
	{Verb: "feedback", Txtar: "feedback_status.txtar", Tier: tierT3},
	{Verb: "feedback", Txtar: "feedback_triage.txtar", Tier: tierT3},
	{Verb: "feedback", GoTest: "internal/e2e.TestFeedbackSubmitWrite", Tier: tierInProcess},
	{Verb: "feedback", GoTest: "internal/e2e.TestFeedbackSubmitFromANonCollaborator", Tier: tierInProcess},

	// --- Permanent skip-set (spec 26 §1, exhaustive) --------------------
	{Verb: "mcp", Skip: "serve-loop; CLI/MCP behavior equivalence is owned by the P14/P15 parity suite"},
	{Verb: "update", Skip: "network self-swap — the exec'd `a2a update` verb replaces the running binary, so the VERB can't run headlessly (permanent skip, spec §1/§11). The resolve→verify→refuse-on-bad-checksum MECHANISM is exercised by internal/e2e.TestUpdateResolveVerifyRefuseChecksumThroughReleasePipeline (against a local fake release fixture), and the verb's own dispatch by internal/cli/cmd_update_test.go."},
}

// manifestVerbSet returns the set of every Verb named by ANY row of
// entries (evidence or skip) — check (a)'s "in the manifest or skip-set".
func manifestVerbSet(entries []coverageEntry) map[string]bool {
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[e.Verb] = true
	}
	return set
}

// missingVerbs returns every catalogVerbs entry absent from entries'
// combined verb set — a catalog verb neither mapped nor skipped.
func missingVerbs(catalogVerbs []string, entries []coverageEntry) []string {
	known := manifestVerbSet(entries)
	var missing []string
	for _, v := range catalogVerbs {
		if !known[v] {
			missing = append(missing, v)
		}
	}
	sort.Strings(missing)
	return missing
}

// staleEntries returns every entries row whose Verb is NOT a real,
// currently-shipped catalog verb — the reverse-direction check (a manifest/
// skip row surviving a verb rename or removal).
func staleEntries(catalogVerbs []string, entries []coverageEntry) []coverageEntry {
	valid := make(map[string]bool, len(catalogVerbs))
	for _, v := range catalogVerbs {
		valid[v] = true
	}
	var stale []coverageEntry
	for _, e := range entries {
		if !valid[e.Verb] {
			stale = append(stale, e)
		}
	}
	return stale
}

// runCatalogBinary execs `<binPath> __catalog` and returns its stdout+
// stderr combined (the hidden verb never writes to stderr on success, so a
// non-empty combined-output-on-error case still surfaces something useful).
func runCatalogBinary(binPath string) (string, error) {
	cmd := exec.Command(binPath, "__catalog")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("exec %s __catalog: %w: %s", binPath, err, out)
	}
	return string(out), nil
}

// parseCatalogVerbs parses `a2a __catalog`'s stdout markdown (catalog.go's
// renderCatalog shape: "## Commands\n\n- `name` — synopsis\n...") and
// returns every "## Commands" section verb name, sorted, EXCLUDING the
// hidden `__catalog` row itself and any `a2a_*` MCP-tool row (defense in
// depth — those live under the separate "## MCP tools" heading and this
// function stops consuming rows once that heading is reached, but a rename
// of either section's contents should not silently smuggle a wrong name
// into the CLI verb set).
func parseCatalogVerbs(catalogOutput string) []string {
	var verbs []string
	inCommands := false
	for _, line := range strings.Split(catalogOutput, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inCommands = trimmed == "## Commands"
			continue
		}
		if !inCommands || !strings.HasPrefix(trimmed, "- `") {
			continue
		}
		rest := trimmed[len("- `"):]
		end := strings.Index(rest, "`")
		if end < 0 {
			continue
		}
		name := rest[:end]
		if name == "__catalog" || strings.HasPrefix(name, "a2a_") {
			continue
		}
		verbs = append(verbs, name)
	}
	sort.Strings(verbs)
	return verbs
}

// verbInvocationPattern builds the regexp source matching a real `a2a
// <verb>` invocation for verb.
//
// A catalog verb may be MULTI-TOKEN: `contract` is a single dispatch key
// (cmd/a2a/wire.go registers ONE "contract" buildCommands() entry) whose
// sub-verb is its first ARGUMENT, so `a2a contract publish` is two words.
// Each token is matched with `\s+` between, which is also what lets a txtar
// script wrap or align an invocation without going invisible to this check.
//
// This used to special-case a `contract-<sub>` prefix, because catalog.go
// stitched those row names with a HYPHEN while this function's own comment
// recorded that the invoked form was really two words. That gap was not
// harmless: commands.md ships into consumer repos as the list of things an
// agent may invoke, and it advertised seven commands the dispatcher answers
// with `unknown command`. catalog.go now emits the invocable form, so the
// special case is gone and the generic split IS the rule.
func verbInvocationPattern(verb string) string {
	parts := strings.Fields(verb)
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return `\ba2a\s+` + strings.Join(parts, `\s+`) + `\b`
}

// txtarInvokesVerb reports whether scriptContent's txtar source actually
// invokes `a2a <verb>` (§1.3(b)'s grep check).
func txtarInvokesVerb(scriptContent, verb string) bool {
	return regexp.MustCompile(verbInvocationPattern(verb)).MatchString(scriptContent)
}

// resolveTxtarEntry resolves a Txtar evidence row: t3Dir/file must exist and
// its content must invoke `a2a <verb>` per txtarInvokesVerb.
func resolveTxtarEntry(t3Dir, file, verb string) error {
	path := filepath.Join(t3Dir, file)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("txtar %q: %w", file, err)
	}
	if !txtarInvokesVerb(string(raw), verb) {
		return fmt.Errorf("txtar %q does not appear to invoke `a2a %s` (no `a2a %s` line found)", file, verb, verb)
	}
	return nil
}

// execSeamIdents are the identifiers that mark a Go test as reaching the
// BUILT `a2a` binary rather than driving its command directly, in-process:
// newHostRig (host_loop_test.go's hostRig, execs via os/exec against a fake
// host) and binDir (the built-binary directory main_test.go's TestMain
// populates, used directly by e2e1_test.go's runReadVerbAs and
// coverage_test.go's own runCatalogBinary). execSeamPackage is the third
// seam, matched as a selector (testscript.Run, testscript.Params, ...) —
// AC9's gate reuses this exact list from the manual measurement that
// produced this wave's rename set; adding a new exec seam here is a normal
// commit, silently losing one from this list is not.
var execSeamIdents = map[string]bool{
	"newHostRig": true,
	"binDir":     true,
}

const execSeamPackage = "testscript"

// bodyReachesExecSeam reports whether body's AST directly references one of
// execSeamIdents or a execSeamPackage-qualified selector. AST identifier
// matching (never a raw substring/grep over the source text) is deliberate:
// it cannot be fooled by a doc comment or a string literal that merely
// MENTIONS "binDir", because comments and literals are not
// *ast.Ident/*ast.SelectorExpr nodes.
func bodyReachesExecSeam(body *ast.BlockStmt) bool {
	return bodyReachesSeam(body, func(n ast.Node) bool {
		switch expr := n.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := expr.X.(*ast.Ident); ok && pkg.Name == execSeamPackage {
				return true
			}
		case *ast.Ident:
			if execSeamIdents[expr.Name] {
				return true
			}
		}
		return false
	})
}

// stubSeamPackage/stubSeamIdents are execSeamPackage/execSeamIdents' sibling
// for the SECOND question this file's AST walk answers (answers-that-hold-
// 2026-08 P1): not "did this reach the built binary" but "did this
// construct a test double in place of the production component under
// test". Measured at HEAD: internal/host/fake.go's FakeHost/NewFakeHost is
// the one hand-written component double in the tree today (fake.go:13's own
// doc comment: "a hand-written, in-memory Host test double"). Matched as a
// stubSeamPackage-QUALIFIED selector only — deliberately narrower than
// execSeamPackage's "any selector on this package name" shape — because
// internal/host ALSO ships the real production Host (github.go's
// GitHubHost/NewGitHubHost): a bare "any host.-selector" match would
// misclassify every genuine production host reference as a stub. Adding a
// second hand-written test double is a normal commit: extend
// stubSeamIdents, never add a second parser.
const stubSeamPackage = "host"

var stubSeamIdents = map[string]bool{
	"NewFakeHost": true,
	"FakeHost":    true,
}

// bodyReachesStubSeam reports whether body's AST directly references a
// stubSeamPackage-qualified selector named in stubSeamIdents (e.g.
// `host.NewFakeHost(...)`, `host.FakeHost{...}`, `*host.FakeHost`) — AST
// selector matching only, for the same reason bodyReachesExecSeam is AST-
// only: a doc comment or string literal that merely mentions "FakeHost" is
// not an *ast.SelectorExpr node.
func bodyReachesStubSeam(body *ast.BlockStmt) bool {
	return bodyReachesSeam(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == stubSeamPackage && stubSeamIdents[sel.Sel.Name]
	})
}

// bodyReachesSeam is the shared AST scan both bodyReachesExecSeam and
// bodyReachesStubSeam are built from: it walks body once and reports
// whether any node satisfies matches, stopping at the first hit.
func bodyReachesSeam(body *ast.BlockStmt, matches func(ast.Node) bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if matches(n) {
			found = true
		}
		return true
	})
	return found
}

// funcReachesExecSeam reports whether funcName (a top-level, non-method
// func declared in file) reaches an exec seam: directly in its own body, or
// through a helper function it calls that is declared IN THE SAME FILE —
// followed transitively, with cycle protection. located is false when
// funcName has no such declaration in file at all (the "cannot be located"
// case AC9 requires to FAIL, never silently pass).
//
// The same-file bound is deliberate, not an accident of convenience: a
// package-wide, cross-file helper like e2e1_test.go's assertShow execs `a2a
// show` purely to verify a DIRECT-CONSTRUCTION write's folded state — it is
// verification scaffolding, not the verb under test. Following calls across
// files would credit every lifecycle test that merely calls assertShow with
// reaching the binary tier, which is exactly the false claim this rename
// wave exists to correct (lifecycle_cov_test.go's own
// TestLifecycleTransitionCoverageDirectConstruction is the concrete case
// that decided this). A same-file helper, by contrast, is this test's own
// driving machinery (data_loop_test.go's setupDataLoop calling newHostRig in
// the SAME file for
// TestDataLoopFailSupersedePassCloseInboxNeverAccumulates is the concrete
// case that requires the one-hop extension at all — that test's own body
// never spells "newHostRig", only its same-file setup helper does).
//
// This bound is FILE-PLACEMENT-SENSITIVE, and that cuts both ways
// differently: moving setupDataLoop out of data_loop_test.go into a shared
// helpers file would silently RED a currently-true tierT3 row (loud,
// self-correcting — someone has to explain the new failure). Moving
// assertShow INTO a same-file position with a caller that declares
// tierT3 would silently let a false claim PASS (quiet — nothing here
// would catch it). The second failure mode is the one to watch for in
// review, not the first.
func funcReachesExecSeam(file *ast.File, funcName string) (found, located bool) {
	return funcReachesSeam(file, funcName, bodyReachesExecSeam)
}

// funcReachesStubSeam is funcReachesExecSeam's sibling for the stub/real
// question (answers-that-hold-2026-08 P1): the SAME same-file, one-hop
// bound, asked of bodyReachesStubSeam instead of bodyReachesExecSeam. The
// bound's reasoning — direct doctest above — applies unchanged: a stub
// constructed by a same-file setup helper the named test calls IS this
// test's own driving machinery; a stub constructed by an unrelated
// top-level function the named test never calls is not credited to it.
func funcReachesStubSeam(file *ast.File, funcName string) (found, located bool) {
	return funcReachesSeam(file, funcName, bodyReachesStubSeam)
}

// funcReachesSeam is the shared one-hop AST walk funcReachesExecSeam and
// funcReachesStubSeam are both built from, parameterized only by which
// seam a function body must reach (reaches). See funcReachesExecSeam's
// historical doc comment (preserved on the exec-seam call site) for why the
// bound is same-file rather than package-wide — that reasoning is identical
// for both questions this function now answers.
func funcReachesSeam(file *ast.File, funcName string, reaches func(*ast.BlockStmt) bool) (found, located bool) {
	decls := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			decls[fd.Name.Name] = fd
		}
	}
	target, ok := decls[funcName]
	if !ok || target.Body == nil {
		return false, false
	}

	visited := map[string]bool{}
	var walk func(fd *ast.FuncDecl) bool
	walk = func(fd *ast.FuncDecl) bool {
		if fd.Body == nil || visited[fd.Name.Name] {
			return false
		}
		visited[fd.Name.Name] = true
		if reaches(fd.Body) {
			return true
		}
		var callees []string
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok {
					callees = append(callees, id.Name)
				}
			}
			return true
		})
		for _, name := range callees {
			if callee, ok := decls[name]; ok && walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(target), true
}

// resolveGoTestExecSeam is the goTest half of AC9's per-row tier gate: a row
// declaring Tier: tierT3 must name a function whose body — in
// internal/e2e/*_test.go, directly or through a same-file helper
// (funcReachesExecSeam) — actually reaches execSeamIdents/execSeamPackage.
//
// Only "internal/e2e.TestXxx" refs are searched here, by design: this
// repo's binary tier means "execs the a2a binary via os/exec", and every
// current cross-package GoTest row (cmd/a2a.TestWorkProductionWiringMutations
// calling run() in-process, cmd/a2a.TestDataCorePackEndToEnd calling
// dataCore.pack directly, internal/cli.TestContractActivate against a fake
// funnel) is already a same-process Go call, never exec.Command — so a
// cross-package ref can never truthfully declare tierT3, and this function
// fails it exactly the way a genuinely-unlocatable function fails: absence
// of evidence is not evidence.
func resolveGoTestExecSeam(root, goTestRef string) error {
	const pkgPrefix = "internal/e2e."
	if !strings.HasPrefix(goTestRef, pkgPrefix) {
		return fmt.Errorf("GoTest ref %q declares Tier: %q but is not an internal/e2e test — its body cannot be searched for an exec seam here", goTestRef, tierT3)
	}
	funcName := strings.TrimPrefix(goTestRef, pkgPrefix)
	dir := filepath.Join(root, "internal", "e2e")
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return fmt.Errorf("glob %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	for _, m := range matches {
		f, err := parser.ParseFile(fset, m, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", m, err)
		}
		found, located := funcReachesExecSeam(f, funcName)
		if !located {
			continue
		}
		if !found {
			return fmt.Errorf("GoTest ref %q declares Tier: %q but %s's body (in %s) never reaches an exec seam (newHostRig/binDir/testscript.), directly or through a same-file helper", goTestRef, tierT3, funcName, filepath.Base(m))
		}
		return nil
	}
	return fmt.Errorf("GoTest ref %q declares Tier: %q but no function named %s was found anywhere in internal/e2e/*_test.go — absence of evidence is not evidence", goTestRef, tierT3, funcName)
}

// resolveGoTestStubSeam is answers-that-hold-2026-08 P1's second question,
// asked the same way resolveGoTestExecSeam asks its own: does goTestRef's
// named function — directly, or through a same-file helper
// (funcReachesStubSeam) — construct the internal/host test double in place
// of the production Host?
//
// Unlike resolveGoTestExecSeam, this is NOT bound to internal/e2e: the
// exec-seam question is bound there because only that package's tests can
// truthfully claim "reaches the built binary", but the stub/real question
// is answerable wherever a GoTest row actually points, and today's manifest
// names cmd/a2a and internal/cli tests too. goTestRef's own package-path
// prefix (everything before the final ".") already IS the on-disk
// directory for every current ref — internal/e2e, cmd/a2a, internal/cli —
// so this is a filepath.Join, never a lookup table.
func resolveGoTestStubSeam(root, goTestRef string) (stubBacked bool, err error) {
	i := strings.LastIndex(goTestRef, ".")
	if i < 0 {
		return false, fmt.Errorf("GoTest ref %q not in \"pkg/path.TestName\" shape", goTestRef)
	}
	pkgPath, funcName := goTestRef[:i], goTestRef[i+1:]
	dir := filepath.Join(root, filepath.FromSlash(pkgPath))
	matches, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return false, fmt.Errorf("glob %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	for _, m := range matches {
		f, err := parser.ParseFile(fset, m, nil, 0)
		if err != nil {
			return false, fmt.Errorf("parse %s: %w", m, err)
		}
		found, located := funcReachesStubSeam(f, funcName)
		if !located {
			continue
		}
		return found, nil
	}
	return false, fmt.Errorf("GoTest ref %q: no function named %s was found anywhere in %s/*_test.go — absence of evidence is not evidence", goTestRef, funcName, pkgPath)
}

// stubBackedExemptions is the "declared:" idiom this repo already uses for
// exactly this shape of exception (scripts/lib/lane-ungated.txt,
// schemas/prose-coverage.yaml's declared: map, both read by their own
// gates through an is_deferral_reason-style refusal): a catalog verb whose
// ONLY resolved coverage evidence is stub-backed normally fails the
// obligation below. An entry here excuses exactly one verb, and only with
// a reason isDeferralReason does not recognize as a deferral — a reason
// naming a future phase/wave, or reading as "not yet"/"TODO"/"pending"/
// etc., is REFUSED rather than accepted (spec P1 AC-7), because the
// obligation exists precisely to stop that class of "covered later"
// promise from standing in for evidence today.
//
// Empty at HEAD, and deliberately so: `contract verify-export` is a stub-
// only verb this phase's first run named, but every reason for that gap is
// itself deferral-shaped (the real production repository-lookup/tree-
// reconstruction test does not exist yet, anywhere in the tree) — so an
// exemption here would be self-refuting under this obligation's own rule.
// It is reported instead, in the phase's own findings, and left failing.
var stubBackedExemptions = map[string]string{}

// deferralReasonPattern is isDeferralReason's vocabulary, the same
// substance as scripts/check-prose-coverage.sh's is_deferral_reason applied
// here in Go rather than shell: deferral words (todo, fixme, "not [yet]
// documented", later, soon, pending, backlog, deferred, planned, coming,
// "will be"), plus a word-bounded phase/wave/P-id shape matching this
// repo's own epic-wave vocabulary (P13, H3, "wave-7").
var deferralReasonPattern = regexp.MustCompile(`(?i)\btodo\b|\bfixme\b|not (yet )?documented|\blater\b|\bsoon\b|\bpending\b|\bbacklog\b|\bdeferred\b|\bplanned\b|\bcoming\b|\bwill be\b|\bphase\s+[0-9]+\b|\bwave[- ][0-9a-zA-Z]+\b|\bp[0-9]+\b|\bh[0-9]+\b`)

// isDeferralReason reports whether reason READS AS A DEFERRAL rather than a
// structural fact about the code today — see deferralReasonPattern.
func isDeferralReason(reason string) bool {
	return deferralReasonPattern.MatchString(reason)
}

// stubOnlyVerbs is spec P1 AC-1/AC-2/AC-7's whole verdict, as a pure
// function over its inputs so TestE2ECoverageParity's obligation and its
// own teeth test (a synthetic fixture, never the real manifest) can both
// drive it. verbHasEvidence/verbHasRealEvidence are precomputed by the
// caller, which already resolved every row's classification against the
// filesystem (with caching) — this function's own job is only the set
// arithmetic plus the exemption's deferral check.
//
// named lists every verb that fails the obligation (AC-2: NAMED, never
// merely counted) — a verb with at least one real-backed row, or a validly
// (non-deferral) exempted verb, never appears. problems lists a separate
// diagnostic for every REFUSED exemption (a deferral-shaped reason): a
// refused exemption does not excuse the verb, so it is also present in
// named.
// stubOnlyBudget is the RATCHET, and it is deliberately not an exemption.
//
// The obligation's first run named 22 catalog verbs whose only coverage
// evidence replaces the production Host with a test double; wiring four
// pieces of already-existing, previously-unregistered real evidence closed
// seven of them. The remaining 15 have NO real-tier evidence anywhere in
// the tree — confirmed per-verb by uncapped search, not inferred.
//
// AN EXEMPTION WOULD HAVE BEEN DISHONEST AND THE GATE SAYS SO ITSELF.
// stubBackedExemptions excuses a verb only for a STRUCTURAL reason, and
// isDeferralReason REFUSES a reason that reads as "not yet". Every reason
// available for these 15 is exactly that: the test does not exist. Writing
// it as an exemption would launder a deferral through the one mechanism
// built to refuse deferrals.
//
// So the debt is carried as a BUDGET instead — the shape this repository
// already uses for a debt with a size (scripts/lib/lane-declarations'
// count, P4's per-file refusal budget): a named roster that MAY NOT GROW
// and whose entries are removed by ordinary commits. The obligation reds in
// BOTH directions, which is what makes it shrink rather than merely stall:
// a verb that appears and is not here is NEW debt; a verb here that no
// longer appears is a STALE row the budget must drop.
//
// It does not weaken AC-3. The stub/real classification is still DERIVED by
// AST from the test source; nothing here declares a row's kind. This roster
// only records which derived failures were already known on 2026-08-28.
//
// The two shapes behind the 15, so a reader knows what closing them costs:
//   - 12 OP-211 lifecycle verbs whose only rows resolve to direct-
//     construction tests against host.NewFakeHost. `newHostRig` already
//     shows the real-tier pattern for exactly these; nobody has extended it.
//   - 3 contract P6 sub-verbs. `contract verify-export` is the one spec P1
//     AC-5 names: its only test injects a fake P6 inspection service AND a
//     FakeHost, and its own comment defers to a "P6 space integration suite"
//     that does not exist anywhere in the tree. It was green the entire time
//     it proved nothing about production repository lookup.
var stubOnlyBudget = map[string]bool{
	"approve": true, "block": true, "cancel": true, "contract diff": true,
	"contract new": true, "contract verify-export": true, "decline": true,
	"dispute": true, "reject": true, "satisfy": true, "start": true,
	"unblock": true, "verify-fail": true, "verify-pass": true, "withdraw": true,
}

// stubOnlyBudgetDrift compares the obligation's derived verdict against the
// budget above and returns the two ways the ratchet is violated: newDebt is
// a stub-only verb nobody recorded, and staleBudget is a recorded verb that
// has since gained real evidence and must be dropped from the roster.
func stubOnlyBudgetDrift(named []string, budget map[string]bool) (newDebt, staleBudget []string) {
	got := map[string]bool{}
	for _, v := range named {
		got[v] = true
		if !budget[v] {
			newDebt = append(newDebt, v)
		}
	}
	for v := range budget {
		if !got[v] {
			staleBudget = append(staleBudget, v)
		}
	}
	sort.Strings(newDebt)
	sort.Strings(staleBudget)
	return newDebt, staleBudget
}

func stubOnlyVerbs(verbHasEvidence, verbHasRealEvidence map[string]bool, exemptions map[string]string) (named, problems []string) {
	for verb := range verbHasEvidence {
		if verbHasRealEvidence[verb] {
			continue
		}
		if reason, exempt := exemptions[verb]; exempt {
			if !isDeferralReason(reason) {
				continue // validly excused: a structural reason, not a deferral
			}
			problems = append(problems, fmt.Sprintf("verb %q: stub-backed exemption reason %q reads as a deferral, not a structural fact about the code today — refused", verb, reason))
		}
		named = append(named, verb)
	}
	sort.Strings(named)
	sort.Strings(problems)
	return named, problems
}
