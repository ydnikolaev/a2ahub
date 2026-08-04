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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// coverageEntry is one manifest/skip-set row. Exactly one of Txtar, GoTest,
// Skip must be non-empty — see (coverageEntry).evidenceKind.
type coverageEntry struct {
	Verb   string
	Action string // optional sub-action when the binary catalog exposes only the family
	Txtar  string // testdata/t3/<file>.txtar
	GoTest string // "internal/e2e.TestXxx"
	Skip   string // one-line justification
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

// coverageManifest is the P26 SSOT (spec 26 §1/§2/§11): every catalog CLI
// verb this wave (14a) resolved, mapped-or-skipped. Later waves (14c) flip
// skip rows to real evidence rows as families land — this wave's own
// mandate is GREEN via a fully justified skip-set (plan 26 wave 14a).
var coverageManifest = []coverageEntry{
	// --- Setup & health ------------------------------------------------
	{Verb: "connect", Txtar: "connect.txtar"},
	{Verb: "disconnect", Txtar: "setup_disconnect.txtar"},
	{Verb: "doctor", Txtar: "doctor.txtar"},
	{Verb: "init", Txtar: "init.txtar"},

	// --- Authoring -------------------------------------------------------
	{Verb: "new", Txtar: "new_validate.txtar"},
	{Verb: "space", Txtar: "space_init.txtar"},
	// `space update` needs a host (it opens a PR through the write funnel),
	// so it cannot be a txtar against the local fixture — it is covered at
	// the host-rig tier instead, the same way `submit` is (spec 35 AC-950.5).
	{Verb: "space", GoTest: "internal/e2e.TestHostLoopSpaceUpdate"},
	{Verb: "template", Txtar: "template_list.txtar"},
	{Verb: "validate", Txtar: "new_validate.txtar"},

	// --- Write funnel ------------------------------------------------
	// submit: direct-construction write-path Go test (spec 26 §11 amendment
	// — exec-unreachable), PLUS the exec-txtar covering the CC-002 local
	// refusal path (which runs before any network call, so it IS exec'able).
	{Verb: "submit", GoTest: "internal/e2e.TestT3Submit"},
	{Verb: "submit", Txtar: "submit_refusal.txtar"},
	{Verb: "sync", Txtar: "sync.txtar"},
	{Verb: "await", GoTest: "internal/e2e.TestT3Await"},

	// --- Read surface --------------------------------------------------
	{Verb: "contracts", Txtar: "search_contracts.txtar"},
	{Verb: "inbox", Txtar: "inbox_outbox.txtar"},
	{Verb: "outbox", Txtar: "inbox_outbox.txtar"},
	{Verb: "search", Txtar: "search_contracts.txtar"},
	{Verb: "show", Txtar: "show_thread.txtar"},
	{Verb: "statusline", Txtar: "statusline.txtar"},
	{Verb: "thread", Txtar: "show_thread.txtar"},

	// --- Lifecycle (all 19 OP-211 verbs — direct-construction, exec-
	// unreachable per spec 26 §11 amendment) ---------------------------
	{Verb: "accept", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "ack", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "approve", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "block", GoTest: "internal/e2e.TestT3BlockUnblock"},
	{Verb: "cancel", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "close", GoTest: "internal/e2e.TestT3CloseFromResponded"},
	{Verb: "decline", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "dispute", GoTest: "internal/e2e.TestT3RespondVerifyDispute"},
	{Verb: "note", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "reject", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "respond", GoTest: "internal/e2e.TestT3RespondVerifyDispute"},
	{Verb: "satisfy", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "start", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "supersede", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "unblock", GoTest: "internal/e2e.TestT3BlockUnblock"},
	{Verb: "verify", GoTest: "internal/e2e.TestT3RespondVerifyDispute"},
	{Verb: "verify-fail", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "verify-pass", GoTest: "internal/e2e.TestT3LifecycleVerbs"},
	{Verb: "withdraw", GoTest: "internal/e2e.TestT3LifecycleVerbs"},

	// --- Contract ops (direct-construction, exec-unreachable) -----------
	{Verb: "contract adopt", GoTest: "internal/e2e.TestT3ContractAdopt"},
	{Verb: "contract check", GoTest: "internal/e2e.TestHostLoopContractFamily"},
	{Verb: "contract deprecate", GoTest: "internal/e2e.TestT3ContractNewPublishDeprecate"},
	{Verb: "contract diff", GoTest: "internal/e2e.TestT3ContractDiff"},
	{Verb: "contract materialize", GoTest: "internal/e2e.TestHostLoopContractFamily"},
	{Verb: "contract new", GoTest: "internal/e2e.TestT3ContractNewPublishDeprecate"},
	{Verb: "contract preflight", GoTest: "internal/e2e.TestHostLoopContractFamily"},
	{Verb: "contract publish", GoTest: "internal/e2e.TestT3ContractNewPublishDeprecate"},
	{Verb: "contract retire", GoTest: "internal/e2e.TestT3ContractRetireCleanUngated"},
	{Verb: "contract verify-export", GoTest: "internal/e2e.TestT3ContractVerifyExportLocal"},

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
	// `verify --pass` can each drive a write and so are exec-unreachable for
	// the same reason every contract sub-verb is (txtar_test.go's own doc
	// comment: no github.com-shaped remote in this fixture) — covered
	// instead by cmd/a2a's own direct-construction dataCore tests
	// (data_wiring_test.go).
	{Verb: "data", Txtar: "data_pack_refusal.txtar"},
	{Verb: "data", GoTest: "cmd/a2a.TestDataCorePackEndToEnd"},
	// The two-party host-rig loop (spec 05a §6.3): pack, deliver (one
	// commit, idempotent re-run), fetch (clean + divergent destination),
	// verify (judge-only and --record, both directions), and the
	// fail->supersede->pass->close loop against a real fake-host merge —
	// exercise no earlier row here reaches, since data_wiring_test.go's
	// own rows are direct-construction, never through the funnel.
	{Verb: "data", GoTest: "internal/e2e.TestDataLoopFailSupersedePassCloseInboxNeverAccumulates"},

	// --- Ops & delivery --------------------------------------------------
	{Verb: "completion", Txtar: "ops_completion.txtar"},
	{Verb: "dashboard", Txtar: "ops_html.txtar"},
	{Verb: "html", Txtar: "ops_html.txtar"},
	{Verb: "notifications", GoTest: "internal/e2e.TestT3Notifications"},
	{Verb: "serve", Txtar: "operational_commands.txtar"},
	{Verb: "skill", Txtar: "ops_skill.txtar"},
	{Verb: "version", Txtar: "ops_version.txtar"},
	{Verb: "whatsnew", Txtar: "whatsnew.txtar"},

	// --- Operational work family (OP-224) ------------------------------
	// The binary catalog exposes `work` as one family. Action-qualified rows
	// make every closed subcommand carry behavior-level evidence. The six
	// mutations resolve to the exact production-wiring host integration test;
	// a direct fake backend or missing-argument usage line is deliberately not
	// executable evidence for buildCommands/buildWorkCommand.
	// Status remains a real successful built-binary offline path.
	{Verb: "work", Action: "start", GoTest: "cmd/a2a.TestWorkProductionWiringMutations"},
	{Verb: "work", Action: "heartbeat", GoTest: "cmd/a2a.TestWorkProductionWiringMutations"},
	{Verb: "work", Action: "resume", GoTest: "cmd/a2a.TestWorkProductionWiringMutations"},
	{Verb: "work", Action: "checkpoint", GoTest: "cmd/a2a.TestWorkProductionWiringMutations"},
	{Verb: "work", Action: "wait", GoTest: "cmd/a2a.TestWorkProductionWiringMutations"},
	{Verb: "work", Action: "stop", GoTest: "cmd/a2a.TestWorkProductionWiringMutations"},
	{Verb: "work", Action: "status", Txtar: "operational_commands.txtar"},

	// --- Feedback (P25) — spec 26 §2 Feedback row: this phase only
	// registers P25's own evidence, 4 exec-txtar + 1 Go-test ref. --------
	{Verb: "feedback", Txtar: "feedback_new_validate.txtar"},
	{Verb: "feedback", Txtar: "feedback_intake_guards.txtar"},
	{Verb: "feedback", Txtar: "feedback_status.txtar"},
	{Verb: "feedback", Txtar: "feedback_triage.txtar"},
	{Verb: "feedback", GoTest: "internal/e2e.TestFeedbackSubmitWrite"},
	{Verb: "feedback", GoTest: "internal/e2e.TestFeedbackSubmitFromANonCollaborator"},

	// --- Permanent skip-set (spec 26 §1, exhaustive) --------------------
	{Verb: "mcp", Skip: "serve-loop; CLI/MCP behavior equivalence is owned by the P14/P15 parity suite"},
	{Verb: "update", Skip: "network self-swap — the exec'd `a2a update` verb replaces the running binary, so the VERB can't run headlessly (permanent skip, spec §1/§11). The resolve→verify→refuse-on-bad-checksum MECHANISM is exercised by internal/e2e.TestT3UpdateResolveVerifyRefuseChecksum (against a local fake release fixture), and the verb's own dispatch by internal/cli/cmd_update_test.go."},
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
