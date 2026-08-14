package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/html"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
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

// TestCatalogVocabularyIsTheDomainsOwn proves that the composite document has
// exactly the four established fold-owned fields plus the three P2 families,
// and that every value comes directly from its owning domain enumerator. It
// deliberately inspects raw keys before decoding individual fields: decoding
// the extended document into fold.Vocabulary would silently ignore the new
// families and let an absent family pass.
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

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	gotKeys := make([]string, 0, len(raw))
	for key := range raw {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := []string{
		"consistency_severity",
		"dependency_drift",
		"freshness",
		"gate",
		"human_gates",
		"live_transport",
		"operational_state",
		"outcomes",
		"reason",
		"rule_identity",
		"source_freshness",
		"states",
		"transitions",
		"work_mode",
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Errorf("vocabulary keys = %v, want exactly %v", gotKeys, wantKeys)
	}
	assertCatalogKeyOrder(t, stdout.String(), []string{
		"outcomes",
		"states",
		"transitions",
		"human_gates",
		"reason",
		"rule_identity",
		"gate",
		"freshness",
		"source_freshness",
		"work_mode",
		"dependency_drift",
		"consistency_severity",
		"operational_state",
		"live_transport",
	})

	base := fold.BuildVocabulary()
	if got := decodeCatalogField[[]string](t, raw, "outcomes"); !reflect.DeepEqual(got, base.Outcomes) {
		t.Errorf("outcomes = %v, want fold-owned %v", got, base.Outcomes)
	}
	if got := decodeCatalogField[map[string][]string](t, raw, "states"); !reflect.DeepEqual(got, base.States) {
		t.Errorf("states = %v, want fold-owned %v", got, base.States)
	}
	if got := decodeCatalogField[[]string](t, raw, "transitions"); !reflect.DeepEqual(got, base.Transitions) {
		t.Errorf("transitions = %v, want fold-owned %v", got, base.Transitions)
	}
	if got := decodeCatalogField[map[string]string](t, raw, "human_gates"); !reflect.DeepEqual(got, base.HumanGates) {
		t.Errorf("human_gates = %v, want fold-owned %v", got, base.HumanGates)
	}
	if got, want := decodeCatalogField[[]cache.ReasonCode](t, raw, "reason"), cache.ReasonCodes(); !reflect.DeepEqual(got, want) {
		t.Errorf("reason = %v, want cache-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]pendency.RuleIdentity](t, raw, "rule_identity"), pendency.RuleIdentities(); !reflect.DeepEqual(got, want) {
		t.Errorf("rule_identity = %v, want pendency-owned %v", got, want)
	}
	wantGates := make([]string, 0, len(base.HumanGates))
	seenGates := map[string]bool{}
	for _, gate := range base.HumanGates {
		if gate != "" && !seenGates[gate] {
			seenGates[gate] = true
			wantGates = append(wantGates, gate)
		}
	}
	sort.Strings(wantGates)
	if got := decodeCatalogField[[]string](t, raw, "gate"); !reflect.DeepEqual(got, wantGates) {
		t.Errorf("gate = %v, want derived unique non-empty values %v", got, wantGates)
	}
	if got, want := decodeCatalogField[[]operational.Freshness](t, raw, "freshness"), operational.FreshnessValues(); !reflect.DeepEqual(got, want) {
		t.Errorf("freshness = %v, want operational-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]operational.SourceFreshness](t, raw, "source_freshness"), operational.SourceFreshnessValues(); !reflect.DeepEqual(got, want) {
		t.Errorf("source_freshness = %v, want operational-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]workreport.Mode](t, raw, "work_mode"), workreport.Modes(); !reflect.DeepEqual(got, want) {
		t.Errorf("work_mode = %v, want workreport-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]string](t, raw, "dependency_drift"), cache.DependencyDrifts(); !reflect.DeepEqual(got, want) {
		t.Errorf("dependency_drift = %v, want cache-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]operational.ConsistencySeverity](t, raw, "consistency_severity"), operational.ConsistencySeverities(); !reflect.DeepEqual(got, want) {
		t.Errorf("consistency_severity = %v, want operational-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]string](t, raw, "operational_state"), cache.OperationalStates(); !reflect.DeepEqual(got, want) {
		t.Errorf("operational_state = %v, want cache-owned %v", got, want)
	}
	if got, want := decodeCatalogField[[]string](t, raw, "live_transport"), html.LiveTransportStates(); !reflect.DeepEqual(got, want) {
		t.Errorf("live_transport = %v, want html-owned %v", got, want)
	}
}

// TestCatalogVocabularyCoversDashboardVocabulary compares the actual binary
// document with the payload dictionary. It starts from raw JSON so an additive
// family or a changed field shape cannot disappear during a narrower decode.
func TestCatalogVocabularyCoversDashboardVocabulary(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := runCatalog([]string{"--vocabulary", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d (stderr: %q), want 0", code, stderr.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	catalogue := normalizedDashboardCatalogue(t, raw)
	if len(catalogue) != len(html.VocabularyFamilies()) {
		t.Errorf("normalized catalogue has %d families, want %d", len(catalogue), len(html.VocabularyFamilies()))
	}
	actual := make(map[catalogVocabularyPair]bool)
	for _, family := range html.VocabularyFamilies() {
		values, ok := catalogue[family]
		if !ok || len(values) == 0 {
			t.Errorf("catalogue family %q is empty", family)
			continue
		}
		for _, value := range values {
			pair := catalogVocabularyPair{family: family, value: value}
			if value == "" {
				t.Errorf("catalogue contains empty value in family %q", family)
				continue
			}
			if actual[pair] {
				t.Errorf("catalogue duplicates %s/%s", family, value)
			}
			actual[pair] = true
		}
	}

	entries := make(map[catalogVocabularyPair]bool)
	for _, entry := range html.DashboardVocabulary().Entries {
		pair := catalogVocabularyPair{family: entry.Family, value: entry.Value}
		if entry.Family == "" || entry.Value == "" {
			t.Errorf("dashboard vocabulary contains fallback-like row family=%q value=%q", entry.Family, entry.Value)
			continue
		}
		if entries[pair] {
			t.Errorf("dashboard vocabulary duplicates %s/%s", pair.family, pair.value)
		}
		entries[pair] = true
	}
	for pair := range actual {
		if !entries[pair] {
			t.Errorf("dashboard vocabulary omits catalogue pair %s/%s", pair.family, pair.value)
		}
	}
	for pair := range entries {
		if !actual[pair] {
			t.Errorf("dashboard vocabulary has non-catalogue pair %s/%s", pair.family, pair.value)
		}
	}
}

type catalogVocabularyPair struct {
	family html.VocabularyFamily
	value  string
}

func normalizedDashboardCatalogue(t *testing.T, raw map[string]json.RawMessage) map[html.VocabularyFamily][]string {
	t.Helper()
	baseStates := decodeCatalogField[map[string][]string](t, raw, "states")
	humanGates := decodeCatalogField[map[string]string](t, raw, "human_gates")
	return map[html.VocabularyFamily][]string{
		html.VocabularyFamilyFreshness:           stringsFromCatalog(decodeCatalogField[[]operational.Freshness](t, raw, "freshness")),
		html.VocabularyFamilySourceFreshness:     stringsFromCatalog(decodeCatalogField[[]operational.SourceFreshness](t, raw, "source_freshness")),
		html.VocabularyFamilyOutcome:             decodeCatalogField[[]string](t, raw, "outcomes"),
		html.VocabularyFamilyLifecycleState:      unionCatalogSlices(baseStates),
		html.VocabularyFamilyReason:              stringsFromCatalog(decodeCatalogField[[]cache.ReasonCode](t, raw, "reason")),
		html.VocabularyFamilyGate:                unionCatalogValues(humanGates),
		html.VocabularyFamilyWorkMode:            stringsFromCatalog(decodeCatalogField[[]workreport.Mode](t, raw, "work_mode")),
		html.VocabularyFamilyDependencyDrift:     decodeCatalogField[[]string](t, raw, "dependency_drift"),
		html.VocabularyFamilyConsistencySeverity: stringsFromCatalog(decodeCatalogField[[]operational.ConsistencySeverity](t, raw, "consistency_severity")),
		html.VocabularyFamilyOperationalState:    decodeCatalogField[[]string](t, raw, "operational_state"),
		html.VocabularyFamilyLiveTransport:       decodeCatalogField[[]string](t, raw, "live_transport"),
	}
}

func stringsFromCatalog[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func unionCatalogSlices(values map[string][]string) []string {
	seen := map[string]bool{}
	for _, group := range values {
		for _, value := range group {
			if value != "" {
				seen[value] = true
			}
		}
	}
	return sortedCatalogSet(seen)
}

func unionCatalogValues(values map[string]string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	return sortedCatalogSet(seen)
}

func sortedCatalogSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func assertCatalogKeyOrder(t *testing.T, document string, keys []string) {
	t.Helper()
	previous := -1
	for _, key := range keys {
		position := strings.Index(document, `"`+key+`"`)
		if position < 0 {
			t.Errorf("catalogue output omits key %q", key)
			continue
		}
		if position <= previous {
			t.Errorf("catalogue key %q appears out of contract order", key)
		}
		previous = position
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

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	states := decodeCatalogField[map[string][]string](t, raw, "states")
	outcomes := decodeCatalogField[[]string](t, raw, "outcomes")
	transitions := decodeCatalogField[[]string](t, raw, "transitions")

	for _, krs := range fold.RestingStates() {
		found := false
		for _, s := range states[string(krs.Kind)] {
			if s == string(krs.State) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("vocabulary omits (%s, %s), a pair RestingStates() yields — a gate reading this would not police it", krs.Kind, krs.State)
		}
	}
	if len(outcomes) == 0 || len(transitions) == 0 {
		t.Fatal("vocabulary has no outcomes or no transitions — a gate deriving from it would forbid nothing and pass silently")
	}
}

func decodeCatalogField[T any](t *testing.T, raw map[string]json.RawMessage, key string) T {
	t.Helper()
	var value T
	field, ok := raw[key]
	if !ok {
		t.Fatalf("vocabulary omits %q", key)
	}
	if err := json.Unmarshal(field, &value); err != nil {
		t.Fatalf("vocabulary field %q is invalid: %v", key, err)
	}
	return value
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
