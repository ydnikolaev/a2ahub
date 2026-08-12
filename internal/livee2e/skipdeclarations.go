package livee2e

// skipdeclarations.go is P8's AC5 registry (spec 08-paths.md §8 AC5, US-3):
// "every assertion that needs a live provider is declared as such, with the
// reason... zero silent skips." Same shape as this tier's other reasoned
// exemptions — undrivablePaths() (pathdrivability.go), undrivenScenarios()
// (scenariocoverage_test.go) and incidentReplays() (scenarios_incidents.go):
// a gap the tier cannot close is DECLARED, with a reason, never left silent.
//
// **Scope, decided and justified.** The universe this registry answers for
// is every `t.Skip`/`t.Skipf`/`t.SkipNow` call in this package's `*_test.go`
// files — the Go-language boundary for "this package's test surface" (only
// files ending `_test.go` are compiled as tests at all; a plain `.go` file
// that happens to take a `*testing.T` parameter, like pathdriver_live.go's
// helpers, is driven BY a test but is not itself one, and — checked, not
// assumed — carries no Skip call today). Not the whole repo: a skip in an
// unrelated package is not this phase's claim. Not one file: the tier is
// flat (`internal/livee2e` has no subpackages) and every `_test.go` file in
// it — tagged `livee2e` or not — is equally in scope, because
// skipdeclarations_test.go's scanner reads Go syntax, not build constraints,
// and a skip hidden behind a tag nobody runs by hand is exactly the silent
// one this gate exists to catch.
//
// **This file is deliberately UNTAGGED**, same reasoning pathdrivability.go's
// own doc comment gives for itself: it must run on the plain
// `go test ./internal/livee2e/...` suite `make check` already executes on
// every commit, never only under a 20-minute `-tags=livee2e` run nobody
// triggers by hand.
//
// This file carries only the registry — pure data, no scanning. The static
// scanner that finds every actual Skip-family call lives in
// skipdeclarations_test.go, the same split scenariocoverage_test.go draws
// between its own pure parser (parseScenarioIDs) and pathdrivability.go's
// registry-only shape: keeping go/parser out of the shipped package.

// skipDeclaration pairs one Skip-family call site with the reason it exists.
// Keyed on (File, Func, Occurrence) rather than (File, Func) alone: a second
// Skip call added later inside an already-declared function must produce a
// NEW, undeclared key rather than silently matching the first declaration's
// reason — the exact silent-skip shape a coarser key would let through.
// Occurrence is 1-based, assigned in source (line) order within Func by the
// scanner in skipdeclarations_test.go.
type skipDeclaration struct {
	File       string
	Func       string
	Occurrence int
	Reason     string
}

// skipDeclarations is the reasoned registry: every Skip-family call this
// package's `_test.go` files actually contain, with the reason it exists.
// TestSkipDeclarationsCoverEverySkip (skipdeclarations_test.go) is the union
// gate — an undeclared site or a stale declaration reds it.
func skipDeclarations() []skipDeclaration {
	return []skipDeclaration{
		{
			File:       "protection_test.go",
			Func:       "TestChecklistDoesNotClaimAdminsAreBoundWhileTheyAreNot",
			Occurrence: 1,
			Reason: "NOT a live-provider skip — classified honestly rather than folded into " +
				"US-3's framing by default. This test asserts a POSITIVE claim (the checklist " +
				"states GitHub's own \"Bypassed rule violations\" wording) that is only true, " +
				"and only worth demanding, when ProtectionBody()[\"enforce_admins\"] is false — " +
				"that is the exact configuration under which an admin's direct push actually " +
				"succeeds (the 2026-07-26 incident this test's own doc comment records). When " +
				"enforce_admins reads true, admins genuinely ARE bound, the checklist is free to " +
				"say so, and there is nothing false left for this test to catch — the branch it " +
				"is testing FOR does not exist in that configuration. Skipping states that fact " +
				"rather than asserting a requirement that would fail the moment the space sensibly " +
				"hardens admin enforcement, which would make the gate red for the RIGHT config. " +
				"Declared here, keyed to its exact call site, so a future flip of enforce_admins " +
				"is never a silent hole: TestSkipDeclarationsCoverEverySkip pins that this exact " +
				"skip, and no other, is the one this reason covers.",
		},
		{
			File:       "privatecorpus_test.go",
			Func:       "skipWithoutPrivateCorpus",
			Occurrence: 1,
			Reason: "NOT a live-provider skip either, and it fires in exactly one kind of tree: " +
				"the FILTERED release candidate. Five tests in this package hold the shipped " +
				"conformance catalogue against the corpus that declares it — spec files under " +
				"docs/features/**, incident evidence under docs/inbox/** — and `docs/` is " +
				"STRIPPED from the public projection. A tree that carries the catalogue but " +
				"not the corpus cannot have drifted from that corpus, so the honest verdict " +
				"there is \"not judged here\", the same answer the Makefile gives for five " +
				"presence-gated private gates. In the PRIVATE tree every path is present and " +
				"every one of those tests runs, which `make check` re-proves on each run. " +
				"Added 2026-08-12 after reconstructing the candidate and discovering the six " +
				"reads would fail its own `make check` in the Go tier — introduced 2026-08-09 " +
				"to 08-11, with no candidate cut in between to execute them. The helper stats " +
				"the exact path its caller is about to read and skips ONLY on absence; every " +
				"other read error stays fatal.",
		},
	}
}
