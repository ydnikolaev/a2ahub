package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// catalog_test.go is the P13 catalog seam's own guard (spec 13 §8 AC #3 +
// §11 wave-7 amendment): a name-parity guard (catalog CLI section ⇆
// buildCommands() keys, two-level, mirroring mcp_parity_test.go's
// designatedCLIVerbs() shape), a determinism check, and an MCP-section
// parity check against the SAME emptyRegistry() this file's sibling
// mcp_parity_test.go already defines (package main, one definition).

// expectedCatalogCommandNames independently recomputes the "## Commands"
// section's expected verb-name set from buildCommands() +
// cli.ContractSubcommands() — the SAME expansion rule catalog.go's
// catalogCommandRows() applies, written a second time here so a drift in
// either place (a verb added to dispatch but not handled in catalog.go, or
// vice versa) is caught independently rather than the test trivially
// agreeing with the implementation it is meant to guard.
func expectedCatalogCommandNames() []string {
	var out []string
	for name := range buildCommands() {
		if name == "contract" {
			for _, sub := range cli.ContractSubcommands() {
				out = append(out, "contract "+sub.Name)
			}
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TestCatalogCommandNameParity is the missing/orphan-verb guard: a verb
// added to buildCommands() but not handled in catalog.go's
// catalogCommandRows()/catalogCLICommand (missing catalogHandTypedSynopsis
// entry AND no cli.Command constructor case) makes catalogCommandRows()
// panic, failing this test; a name set drift between the two independent
// expansions also fails it directly.
func TestCatalogCommandNameParity(t *testing.T) {
	t.Parallel()
	want := expectedCatalogCommandNames()

	rows := catalogCommandRows()
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("catalog CLI-section verb-name set mismatch:\nwant: %v\ngot:  %v", want, got)
	}
}

// TestCatalogCommandRowsHaveSynopsis proves every row's Synopsis is
// non-empty — a construction that silently yields a blank Synopsis() would
// still pass the name-parity check above but produce a useless catalog
// row.
func TestCatalogCommandRowsHaveSynopsis(t *testing.T) {
	t.Parallel()
	for _, r := range catalogCommandRows() {
		if r.Synopsis == "" {
			t.Errorf("catalog row %q has an empty synopsis", r.Name)
		}
	}
}

// TestCatalogMCPSectionParity is the MCP-section parity check: the
// catalog's "## MCP tools" name set equals emptyRegistry().ToolNames() —
// the SAME registry construction mcp_parity_test.go's own bijection tests
// already use (package-shared emptyRegistry(), defined once).
func TestCatalogMCPSectionParity(t *testing.T) {
	t.Parallel()
	want := emptyRegistry().ToolNames() // already sorted

	rows := catalogMCPRows()
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("catalog MCP-section name set mismatch:\nwant: %v\ngot:  %v", want, got)
	}
}

// TestCatalogDeterministic is spec 13 §8 AC #3: calling the catalog
// renderer twice yields byte-identical output (no timestamp, no map-
// iteration-order leak, no absolute path, no version/sha).
func TestCatalogDeterministic(t *testing.T) {
	t.Parallel()
	a := renderCatalog()
	b := renderCatalog()
	if a != b {
		t.Fatalf("renderCatalog() is not deterministic across two calls")
	}
}

// TestRunCatalogExitCodeAndOutput proves the dispatched verb itself (not
// just the internal renderer) writes exactly renderCatalog()'s output to
// stdout, nothing to stderr, and exits 0.
func TestRunCatalogExitCodeAndOutput(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runCatalog(nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runCatalog exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runCatalog wrote to stderr: %q", stderr.String())
	}
	if stdout.String() != renderCatalog() {
		t.Fatalf("runCatalog stdout does not match renderCatalog() output")
	}
}

// TestCatalogRegisteredInDispatch proves `__catalog` is a real
// buildCommands() key (wire.go registration), not just a standalone
// function — dispatch never sees it unless registered.
func TestCatalogRegisteredInDispatch(t *testing.T) {
	t.Parallel()
	if _, ok := buildCommands()["__catalog"]; !ok {
		t.Fatal("expected \"__catalog\" to be registered in buildCommands()")
	}
}

// TestEveryCatalogNameIsDispatchable is the guard the catalog was missing,
// and it exists because the one above did not catch a real, shipped defect.
//
// TestCatalogCommandNameParity independently recomputes the expansion rule
// and compares the two name sets — which proves the two derivations agree,
// and proves nothing about whether the names they agree on can be typed.
// They could not: the expansion joined `contract` to its sub-verb with a
// HYPHEN, so `commands.md` — the machine-consumed catalog that ships into
// every consumer repo via `a2a skill install` — advertised seven commands
// (`contract-publish`, `contract-adopt`, `contract-diff`, `contract-new`,
// `contract-deprecate`, `contract-retire`, `contract-verify-export`) that
// the dispatcher answers with `unknown command`. `skill/a2ahub/loops.md`
// had copied one of them into prose. Two independent derivations of one
// wrong rule agree perfectly.
//
// `skill-drift` (.github/workflows/ci.yml) could not see it either: it
// regenerates commands.md from this same code and byte-diffs, so it proves
// the committed copy is CURRENT, never that what it advertises is REAL.
//
// This checks the property that actually matters: every catalog row names a
// verb the dispatcher recognises. A two-token name is legal only when its
// FIRST token is a dispatch key (the rest are that verb's arguments), which
// is precisely the distinction the hyphen erased.
//
// TEETH: change catalog.go's expansion separator back to "-" and this reds,
// naming every undispatchable row.
func TestEveryCatalogNameIsDispatchable(t *testing.T) {
	t.Parallel()

	dispatch := buildCommands()
	var broken []string
	for _, r := range catalogCommandRows() {
		verb, _, _ := strings.Cut(r.Name, " ")
		if _, ok := dispatch[verb]; !ok {
			broken = append(broken, r.Name)
		}
	}
	if len(broken) > 0 {
		sort.Strings(broken)
		t.Fatalf("these catalog rows name commands the dispatcher does not recognise:\n  %s\n\n"+
			"commands.md is read by agents as the list of things they may invoke. A row whose first "+
			"token is not a buildCommands() key is a command that answers `unknown command`. "+
			"A sub-verb is an ARGUMENT of its parent verb, so it is joined with a space, never a hyphen.",
			strings.Join(broken, "\n  "))
	}
}

// TestCatalogVocabularyIsTheDomainsOwn proves the flag emits what fold
// derives, not a projection this file re-states. The whole point of the
// mode is that a gate can stop carrying its own copy of the vocabulary;
// a CLI that filtered or reordered on the way out would reintroduce the
// second source it exists to remove.
func TestCatalogVocabularyIsTheDomainsOwn(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := runCatalog([]string{"--vocabulary", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d (stderr: %q), want 0", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("wrote to stderr: %q", stderr.String())
	}

	var got fold.Vocabulary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !reflect.DeepEqual(got, fold.BuildVocabulary()) {
		t.Fatalf("emitted vocabulary differs from fold.BuildVocabulary()\ngot:  %+v\nwant: %+v", got, fold.BuildVocabulary())
	}
}

// TestCatalogVocabularyCoversEveryRestingPair is the assertion a gate
// depends on and cannot make for itself: the vocabulary it derives its
// forbidden list from must actually contain every state the domain can
// produce. A vocabulary missing a state polices nothing about it, and the
// gate would be green while a component spelled it by hand.
func TestCatalogVocabularyCoversEveryRestingPair(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := runCatalog([]string{"--vocabulary", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d (stderr: %q)", code, stderr.String())
	}

	var v fold.Vocabulary
	if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	for _, krs := range fold.RestingStates() {
		found := false
		for _, s := range v.States[string(krs.Kind)] {
			if s == string(krs.State) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("vocabulary omits (%s, %s), a pair RestingStates() yields — a gate reading this would not police it", krs.Kind, krs.State)
		}
	}
	if len(v.Outcomes) == 0 || len(v.Transitions) == 0 {
		t.Fatal("vocabulary has no outcomes or no transitions — a gate deriving from it would forbid nothing and pass silently")
	}
}

// TestCatalogFlagRefusals pins the two refusals as refusals. Defaulting
// either one would be worse than failing: --vocabulary alone rendering
// markdown invites a gate to parse prose, and --json alone silently
// changing the command catalog's format would break the committed
// skill/a2ahub/reference/commands.md projection.
func TestCatalogFlagRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"vocabulary_without_json", []string{"--vocabulary"}},
		{"json_without_vocabulary", []string{"--json"}},
		{"unknown_flag", []string{"--nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := runCatalog(tc.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2 (a usage refusal)", code)
			}
			if stderr.Len() == 0 {
				t.Fatal("refused with no explanation on stderr")
			}
			if stdout.Len() != 0 {
				t.Fatalf("a refusal still wrote to stdout: %q", stdout.String())
			}
		})
	}
}

// TestBareCatalogUnaffectedByTheNewFlags is the regression guard for the
// committed projection: adding a mode must not move the default output,
// which skill/a2ahub/reference/commands.md reproduces byte for byte.
func TestBareCatalogUnaffectedByTheNewFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := runCatalog([]string{}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.String() != renderCatalog() {
		t.Fatal("an empty argument slice no longer produces the markdown catalog")
	}
}
