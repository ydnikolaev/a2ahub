// Package e2e — crosssurface_test.go is P2's AC1/AC2 proof: ONE artifact per
// fold state, FOUR shipped read verbs (`inbox`, `outbox`, `thread`, `html`),
// driven through the BUILT `a2a` binary (never in-process), asserting every
// surface reports the IDENTICAL pendency answer — owed-move set
// (waiting_on), owed transition (expected_transition) and justification
// (why) — for the same artifact. Nothing in this package compared two
// surfaces' JSON before this file (see coverage.go's own package doc:
// "TestE2ECoverageParity ... never a third" evidence kind — this is neither
// a Txtar nor a verb-coverage row; it is a cross-surface CONSISTENCY proof,
// which is why it is not added to coverageManifest — see this file's own
// note at the bottom).
//
// Fixture shape: ONE mirror directory (no git plumbing needed — read verbs
// are pure filesystem reads; internal/cache.Store.SpaceSyncFacts degrades a
// missing .git to "not synced" rather than erroring, and cmd_show_test.go's
// own direct-construction tests already prove a bare t.TempDir() mirror is
// legal), one hand-authored space.yaml naming SIX participants (axon the
// sender, plus five addressees split three-active/three-left), and four
// artifacts — one per fold state this phase requires (spec P2 AC2):
//
//   - settled:                XC-axon-settled     (contract, published)
//   - departed counterparty:  XR-axon-departed     (requirement, published,
//     to: [delta, epsilon, zeta], all three LEFT — CC-062 full transfer)
//   - human-gated:             XD-axon-gated        (decision, proposed,
//     required_approvers: [beta, gamma], neither approved — G3)
//   - blocked:                 XQ-axon-20260721-b1kn (question, blocked)
//
// The departed-counterparty fixture is MULTI-TARGET (to: three systems),
// deliberately: a single departed target cannot distinguish "the only
// target left, so of course the obligation transfers" from the real CC-062
// rule internal/pendency.Resolve implements — "the obligation transfers to
// the sender ONLY when EVERY addressed target has left; a target list with
// some left and some still active must narrow, not transfer". A
// single-target fixture is degenerate for that rule (the two are the same
// fixture), and the phase plan names exactly this as a live defect class:
// a surface that special-cased "one recipient" quietly generalized wrong to
// "any recipient left means transfer". See
// TestCrossSurface/fixture_is_multi_target below, which reds if this
// fixture regresses to a single target.
package e2e

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
)

// --- fixture-local envelope/manifest writers -------------------------------
//
// helpers_test.go is off limits to this wave (brief constraint). Every
// existing writeXArtifact helper there is reused as-is where its fixed shape
// already fits (writeContractDescriptorFor, writeRequirementArtifact,
// writeQuestionArtifact, writeLifecycleEvent[Version] all thread everything
// onto e2eFixtureThread, so a single `a2a thread` call sees all four
// artifacts). The one gap: writeDecisionArtifact in helpers_test.go omits
// `thread:` entirely — a decision predating base.schema.json's P46 "thread
// is REQUIRED on every artifact" rule — so it never groups into any
// ThreadView and the html surface could never carry its pendency verdict at
// all. A real decision this product produces always has one (`a2a new`
// mints it), so this is a local, schema-faithful variant, not a workaround.

// crossSurfaceParticipant is one space.yaml participant row, with a
// caller-chosen status (properManifestYAML in helpers_test.go hardcodes
// every participant "active" — this fixture needs some "left").
type crossSurfaceParticipant struct {
	system string
	status string // "active" | "left"
}

// crossSurfaceManifestYAML renders a schema-valid (manifest/v1/space.schema.json)
// space.yaml body for participants, each carrying its own status — the one
// axis properManifestYAML (helpers_test.go) cannot express.
func crossSurfaceManifestYAML(spaceID string, participants []crossSurfaceParticipant) string {
	var b strings.Builder
	b.WriteString("schema: space/v1\n")
	b.WriteString("space: " + spaceID + "\n")
	b.WriteString("min_binary_version: \"0.0.0\"\n")
	b.WriteString("participants:\n")
	for _, p := range participants {
		b.WriteString("  - system: " + p.system + "\n")
		b.WriteString("    org: fixture\n")
		b.WriteString("    section: " + p.system + "\n")
		b.WriteString("    owners: [" + p.system + "-bot]\n")
		b.WriteString("    status: " + p.status + "\n")
		b.WriteString("    joined: \"2026-01-01\"\n")
	}
	return b.String()
}

// writeDecisionArtifactWithThread is writeDecisionArtifact's (helpers_test.go)
// schema-faithful twin: identical shape, plus the `thread:` field P46 made
// required, so this artifact groups into a ThreadView like every other
// fixture artifact this file writes.
func writeDecisionArtifactWithThread(t *testing.T, mirrorDir, id, thread string, approvers []string) {
	t.Helper()
	quoted := strings.Join(approvers, ", ")
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: decision\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + quoted + "]\n" +
		"thread: " + thread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"required_approvers: [" + quoted + "]\n" +
		"context: does it matter\n" +
		"options_considered: [\"yes\", \"no\"]\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "decisions/"+id+".md", content)
}

// --- JSON probe shapes -----------------------------------------------------
//
// Each surface owns its own minimal decode (the repo's own established idiom
// — see internal/cache/committed_events.go's mirrorCommittedEvent doc
// comment: "every layer in this repo owns its own minimal decode of the
// same underlying document ... rather than sharing one").

// csItem narrows `a2a inbox`/`a2a outbox --json`'s guaranteed cache.Item
// shape to the pendency verdict fields this test compares. cache.Item
// carries NO HumanGate field at all (internal/cache/types.go's own Item
// struct — only cache.OpenItem and html.ThreadOpenItem do); that is this
// file's first named exclusion, asserted nowhere against inbox/outbox
// because there is nothing there to assert.
type csItem struct {
	ID                 string   `json:"id"`
	State              string   `json:"state"`
	WaitingOn          []string `json:"waiting_on"`
	ExpectedTransition string   `json:"expected_transition"`
	Why                string   `json:"why"`
	// OperationalItems narrows cache.Item.OperationalItems (spec 05 AC4,
	// epic-backlog B25 — `inbox`/`outbox` are the two surfaces that could
	// not carry this projection until now) — see
	// TestCrossSurfaceOperationalItems below, the one place this file reads
	// it.
	OperationalItems []csOperationalItem `json:"operational_items"`
}

// csThreadResult narrows cache.ThreadResult to its OpenItems.
type csThreadResult struct {
	OpenItems []csOpenItem `json:"open_items"`
}

// csOpenItem narrows cache.OpenItem (the `a2a thread --json` shape) to the
// pendency verdict fields, INCLUDING HumanGate — thread carries it.
type csOpenItem struct {
	ID                 string   `json:"id"`
	WaitingOn          []string `json:"waiting_on"`
	ExpectedTransition string   `json:"expected_transition"`
	Why                string   `json:"why"`
	HumanGate          string   `json:"human_gate"`
	// OperationalItems narrows cache.OpenItem.OperationalItems (spec 05
	// AC4) — see TestCrossSurfaceOperationalItems below, the one place
	// this file reads it.
	OperationalItems []csOperationalItem `json:"operational_items"`
}

// csHTMLData narrows html.Data to ThreadViews. html.Item (the `a2a html
// --json` Inbox/Outbox row shape) carries NO WaitingOn/ExpectedTransition/
// Why/HumanGate at all (internal/html/model.go's own Item struct, verified
// against html.ThreadOpenItem's doc comment: "This projection used to stop
// at WaitingOn/YourMove" — that history belongs to ThreadOpenItem, never
// Item) — this file's second named exclusion: html's pendency verdict is
// asked ONLY via ThreadViews[].OpenItems, never via Data.Inbox/Data.Outbox,
// because the latter structurally cannot answer.
type csHTMLData struct {
	ThreadViews []csHTMLThreadView `json:"threadViews"`
}

type csHTMLThreadView struct {
	OpenItems []csHTMLOpenItem `json:"open_items"`
}

// csHTMLOpenItem mirrors html.ThreadOpenItem — the dashboard's own carry of
// internal/pendency's verdict, including HumanGate.
type csHTMLOpenItem struct {
	ID                 string   `json:"id"`
	WaitingOn          []string `json:"waiting_on"`
	ExpectedTransition string   `json:"expected_transition"`
	Why                string   `json:"why"`
	HumanGate          string   `json:"human_gate"`
	// OperationalItems narrows html.ThreadOpenItem.OperationalItems (spec
	// 05 AC4) — see TestCrossSurfaceOperationalItems below.
	OperationalItems []csOperationalItem `json:"operational_items"`
}

// csOperationalItem narrows cache.OperationalItem/html.ThreadOpenItem's
// `operational_items[]` entry (spec 05-declared-nature.md AC4,
// agent-exchange-2026-08 P5) to the two fields every surface carries.
type csOperationalItem struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

func csFindOperationalItem(items []csOperationalItem, name string) (csOperationalItem, bool) {
	for _, it := range items {
		if it.Name == name {
			return it, true
		}
	}
	return csOperationalItem{}, false
}

// csShowResult narrows internal/cli's showOutput (`a2a show --json`) to
// the one field TestCrossSurfaceOperationalItems reads.
type csShowResult struct {
	ID               string              `json:"id"`
	OperationalItems []csOperationalItem `json:"operational_items"`
}

func csRunShow(t *testing.T, mirrorDir, id string) csShowResult {
	t.Helper()
	stdout, stderr, code := runReadVerbAs(t, mirrorDir, crossSurfaceSpaceID, "axon", "show", id, "--json")
	if code != 0 {
		t.Fatalf("show %s: code = %d, want 0; stdout=%s stderr=%s", id, code, stdout, stderr)
	}
	var result csShowResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("show %s: decode: %v\nstdout=%s", id, err, stdout)
	}
	return result
}

// --- verdict comparison -----------------------------------------------------

// csVerdict is the pendency answer this test expects, independent of which
// surface renders it.
type csVerdict struct {
	Owners    []string
	Expected  string
	Why       string
	HumanGate string // "" when the surface carries no HumanGate field, or none applies
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// assertOwedMove compares (waitingOn, expected, why) — the three fields
// EVERY surface in this file carries — against want. surface/caseName
// identify the failing assertion; HumanGate is compared by the caller
// separately where the surface carries it, never here (this function must
// stay usable for csItem, which cannot).
func assertOwedMove(t *testing.T, surface, caseName string, gotWaitingOn []string, gotExpected, gotWhy string, want csVerdict) {
	t.Helper()
	if !slices.Equal(sortedStrings(gotWaitingOn), sortedStrings(want.Owners)) {
		t.Errorf("%s: case %q waiting_on = %v, want %v", surface, caseName, gotWaitingOn, want.Owners)
	}
	if gotExpected != want.Expected {
		t.Errorf("%s: case %q expected_transition = %q, want %q", surface, caseName, gotExpected, want.Expected)
	}
	if gotWhy != want.Why {
		t.Errorf("%s: case %q why = %q, want %q", surface, caseName, gotWhy, want.Why)
	}
}

// --- fixture cases -----------------------------------------------------------

type crossSurfaceCase struct {
	name string
	id   string
	// viewSystem is the addressed system `inbox` is read AS — the surface
	// that must show this item is addressed to somebody OTHER than axon
	// (axon's own view is exercised via `outbox`, always).
	viewSystem string
	want       csVerdict
}

// departedCounterpartyTargets is the departed-counterparty fixture's own
// `to:` set — three systems, every one of them left. See this file's
// package doc comment ("MULTI-TARGET, deliberately") for why length must
// exceed 1; TestCrossSurface's own fixture_is_multi_target subtest reds if
// this shrinks to one target.
var departedCounterpartyTargets = []string{"delta", "epsilon", "zeta"}

// crossSurfaceCaseTable is P2 AC2's four required fold states, one artifact
// each: settled, departed counterparty (CC-062), human-gated (G3), blocked.
// Deleting a row reds TestCrossSurface's own case_table_covers_all_four_states
// subtest below (the first required meta-assertion).
var crossSurfaceCaseTable = []crossSurfaceCase{
	{
		name:       "settled",
		id:         "XC-axon-settled",
		viewSystem: "beta",
		want: csVerdict{
			Owners: nil, Expected: "",
			Why: "alive and settled: the owner MAY publish a successor or deprecate, but neither is a move anyone waits for",
		},
	},
	{
		name:       "departed-counterparty",
		id:         "XR-axon-departed",
		viewSystem: "epsilon", // one of the departed targets — its OWN inbox must still say axon owes it, not epsilon
		want: csVerdict{
			Owners: []string{"axon"}, Expected: "",
			Why: "orphaned counterparty (CC-062): delta, epsilon, zeta left the space and can no longer write, " +
				"so the acknowledge this row names can never land; the sender owes a cancel or re-route decision instead",
		},
	},
	{
		name:       "human-gated",
		id:         "XD-axon-gated",
		viewSystem: "beta",
		want: csVerdict{
			Owners: []string{"beta", "gamma"}, Expected: "approve",
			Why:       "domain 3.4.4's quorum gate; reject is the same owed turn",
			HumanGate: "G3",
		},
	},
	{
		name:       "blocked",
		id:         "XQ-axon-20260721-b1kn",
		viewSystem: "beta",
		want: csVerdict{
			Owners: []string{"beta"}, Expected: "unblock",
			Why: "domain 3.4.3 makes unblock the target's own event; the referenced blocker is a separate artifact carrying its own pendency",
		},
	},
}

const crossSurfaceSpaceID = "fixture-space"

// buildCrossSurfaceFixture writes the manifest and all four fixture
// artifacts into one throwaway mirror directory (no git required — see
// package doc comment), returning that directory for every read below.
func buildCrossSurfaceFixture(t *testing.T) string {
	t.Helper()
	mirrorDir := t.TempDir()

	participants := []crossSurfaceParticipant{
		{"axon", "active"},
		{"beta", "active"},
		{"gamma", "active"},
		{"delta", "left"},
		{"epsilon", "left"},
		{"zeta", "left"},
	}
	writeMirrorFile(t, mirrorDir, "space.yaml", crossSurfaceManifestYAML(crossSurfaceSpaceID, participants))

	seq := 0
	nextSeq := func() int { seq++; return seq }

	// settled: contract, draft -> published (nobody owed).
	writeContractDescriptorFor(t, mirrorDir, "axon", "settled", "1.0.0")
	writeLifecycleEventVersion(t, mirrorDir, "axon", nextSeq(), "XC-axon-settled", "publish", "axon", "1.0.0")

	// departed-counterparty: requirement, draft -> published, addressed to
	// three systems that have ALL left — CC-062 full transfer to axon.
	writeRequirementArtifact(t, mirrorDir, "XR-axon-departed", "axon", strings.Join(departedCounterpartyTargets, ", "))
	writeLifecycleEvent(t, mirrorDir, "axon", nextSeq(), "XR-axon-departed", "publish", "axon")

	// human-gated: decision, draft -> proposed, two required approvers,
	// neither has approved — owed transition sits behind G3.
	writeDecisionArtifactWithThread(t, mirrorDir, "XD-axon-gated", e2eFixtureThread, []string{"beta", "gamma"})
	writeLifecycleEvent(t, mirrorDir, "axon", nextSeq(), "XD-axon-gated", "propose", "axon")

	// blocked: question, draft -> submitted -> acknowledged -> blocked.
	writeQuestionArtifact(t, mirrorDir, "XQ-axon-20260721-b1kn", "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", nextSeq(), "XQ-axon-20260721-b1kn", "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", nextSeq(), "XQ-axon-20260721-b1kn", "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", nextSeq(), "XQ-axon-20260721-b1kn", "block", "beta")

	return mirrorDir
}

// --- surface readers ---------------------------------------------------------

func csRunInbox(t *testing.T, mirrorDir, viewSystem string) []csItem {
	t.Helper()
	stdout, stderr, code := runReadVerbAs(t, mirrorDir, crossSurfaceSpaceID, viewSystem, "inbox", "--json")
	if code != 0 {
		t.Fatalf("inbox (as %s): code = %d, want 0; stdout=%s stderr=%s", viewSystem, code, stdout, stderr)
	}
	var items []csItem
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("inbox (as %s): decode: %v\nstdout=%s", viewSystem, err, stdout)
	}
	return items
}

func csRunOutbox(t *testing.T, mirrorDir string) []csItem {
	t.Helper()
	stdout, stderr, code := runReadVerbAs(t, mirrorDir, crossSurfaceSpaceID, "axon", "outbox", "--json")
	if code != 0 {
		t.Fatalf("outbox: code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
	}
	var items []csItem
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("outbox: decode: %v\nstdout=%s", err, stdout)
	}
	return items
}

func csRunThread(t *testing.T, mirrorDir string) csThreadResult {
	t.Helper()
	stdout, stderr, code := runReadVerbAs(t, mirrorDir, crossSurfaceSpaceID, "axon", "thread", e2eFixtureThread, "--space", crossSurfaceSpaceID, "--json")
	if code != 0 {
		t.Fatalf("thread: code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
	}
	var result csThreadResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("thread: decode: %v\nstdout=%s", err, stdout)
	}
	return result
}

func csRunHTML(t *testing.T, mirrorDir string) csHTMLData {
	t.Helper()
	stdout, stderr, code := runReadVerbAs(t, mirrorDir, crossSurfaceSpaceID, "axon", "html", "--json")
	if code != 0 {
		t.Fatalf("html: code = %d, want 0; stdout=%s stderr=%s", code, stdout, stderr)
	}
	var data csHTMLData
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		t.Fatalf("html: decode: %v\nstdout=%s", err, stdout)
	}
	return data
}

func csFindItem(items []csItem, id string) (csItem, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return csItem{}, false
}

func csFindOpenItem(items []csOpenItem, id string) (csOpenItem, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return csOpenItem{}, false
}

func csFindHTMLOpenItem(data csHTMLData, id string) (csHTMLOpenItem, bool) {
	for _, tv := range data.ThreadViews {
		for _, it := range tv.OpenItems {
			if it.ID == id {
				return it, true
			}
		}
	}
	return csHTMLOpenItem{}, false
}

// --- the test ----------------------------------------------------------------

// TestCrossSurface is P2's AC1/AC2 proof. See this file's package doc
// comment for the fixture shape and the two named field exclusions
// (cache.Item carries no HumanGate; html.Item carries no pendency fields at
// all).
// crossSurfaceReaders enumerates the surfaces this epic must reconcile, so
// the set is a value the test can assert over rather than five call sites a
// reader has to count by eye.
//
// The value is a marker, not the reader itself: the five readers have
// different return types (\[\]csItem, csThreadResult, csHTMLData,
// csShowResult), and forcing them behind one signature would cost more than
// it buys. What has to be enumerable is WHICH surfaces are in scope — that
// is the claim P2's future-proofing row makes.
//
// `show` joined this map in spec 05-declared-nature.md's AC4 wave (P5,
// agent-exchange-2026-08): this file's own doc comment above named it as
// "the fifth surface" a later phase would have to add, and it was absent
// until AC4 needed it — see TestCrossSurfaceOperationalItems below for the
// undeclared-vs-absent proof that reader exists to carry.
var crossSurfaceReaders = map[string]func(){
	"inbox":  func() {},
	"outbox": func() {},
	"thread": func() {},
	"html":   func() {},
	"show":   func() {},
}

func TestCrossSurface(t *testing.T) {
	mirrorDir := buildCrossSurfaceFixture(t)

	// Meta-assertion 1 (required): the case table names all four AC2 fold
	// states. Deleting a row below changes this set and reds here, naming
	// both what the table actually covers and what it must.
	t.Run("case_table_covers_all_four_states", func(t *testing.T) {
		want := []string{"blocked", "departed-counterparty", "human-gated", "settled"}
		got := make([]string, 0, len(crossSurfaceCaseTable))
		for _, c := range crossSurfaceCaseTable {
			got = append(got, c.name)
		}
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Fatalf("crossSurfaceCaseTable names = %v, want exactly %v — "+
				"AC2 requires one real fold state per row: settled (nobody owes), "+
				"departed counterparty (CC-062 transfer), human-gated (G3 quorum), blocked",
				got, want)
		}
	})

	// Meta-assertion 2 (required): the departed-counterparty fixture names
	// more than one target. See package doc comment and
	// departedCounterpartyTargets' own doc comment for why a single-target
	// fixture cannot exercise CC-062's actual rule (transfer requires EVERY
	// addressed target to have left, not just one).
	t.Run("fixture_is_multi_target", func(t *testing.T) {
		if len(departedCounterpartyTargets) <= 1 {
			t.Fatalf("departedCounterpartyTargets = %v (len %d), want len > 1: "+
				"CC-062's rule is 'transfer ONLY when every addressed target has left' — "+
				"a single-target fixture cannot distinguish that from 'the one target left, "+
				"so naturally everything transferred', which is exactly the defect class "+
				"a multi-target fixture (some left, or here all three) is required to catch",
				departedCounterpartyTargets, len(departedCounterpartyTargets))
		}
	})

	// Meta-assertion 3 (required): the SURFACE set is enumerated, not merely
	// called.
	//
	// P2's spec puts this in its own future-proofing row — "a fifth surface
	// must join the test to ship" — and four hardcoded reader calls below do
	// not make that true. They make it true only for as long as somebody
	// remembers, which is the same thing this epic keeps finding is not a
	// mechanism.
	//
	// So the surfaces are named here, and a reader that exists without being
	// named reds. Adding `a2a statusline --json` or a future MCP read tool to
	// this file without adding it to `crossSurfaceReaders` is refused with the
	// name of the one that was left out.
	t.Run("every_surface_is_enumerated", func(t *testing.T) {
		want := []string{"html", "inbox", "outbox", "show", "thread"}
		got := make([]string, 0, len(crossSurfaceReaders))
		for name := range crossSurfaceReaders {
			got = append(got, name)
		}
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Fatalf("crossSurfaceReaders = %v, want exactly %v — every surface this "+
				"epic reconciles must be in the map AND asserted per case; a surface "+
				"that is read but not enumerated can diverge with nothing to notice",
				got, want)
		}
		// And the enumeration must not be decorative: each name has to
		// correspond to a reader this file actually invokes.
		for _, name := range want {
			if crossSurfaceReaders[name] == nil {
				t.Errorf("crossSurfaceReaders[%q] is nil — the name is declared and nothing reads it", name)
			}
		}
	})

	outboxItems := csRunOutbox(t, mirrorDir)
	threadResult := csRunThread(t, mirrorDir)
	htmlData := csRunHTML(t, mirrorDir)

	for _, c := range crossSurfaceCaseTable {
		t.Run(c.name, func(t *testing.T) {
			// outbox (as axon, the sender of every fixture artifact here).
			outItem, ok := csFindItem(outboxItems, c.id)
			if !ok {
				t.Fatalf("outbox: no item %q (isOpen for this fold state must be true for the fixture to be meaningful)", c.id)
			}
			assertOwedMove(t, "outbox", c.name, outItem.WaitingOn, outItem.ExpectedTransition, outItem.Why, c.want)

			// inbox (as the addressed system named by the case).
			inItems := csRunInbox(t, mirrorDir, c.viewSystem)
			inItem, ok := csFindItem(inItems, c.id)
			if !ok {
				t.Fatalf("inbox (as %s): no item %q", c.viewSystem, c.id)
			}
			assertOwedMove(t, "inbox", c.name, inItem.WaitingOn, inItem.ExpectedTransition, inItem.Why, c.want)

			// thread — carries HumanGate too.
			threadItem, ok := csFindOpenItem(threadResult.OpenItems, c.id)
			if !ok {
				t.Fatalf("thread: no open_items entry %q (open_items = %+v)", c.id, threadResult.OpenItems)
			}
			assertOwedMove(t, "thread", c.name, threadItem.WaitingOn, threadItem.ExpectedTransition, threadItem.Why, c.want)
			if threadItem.HumanGate != c.want.HumanGate {
				t.Errorf("thread: case %q human_gate = %q, want %q", c.name, threadItem.HumanGate, c.want.HumanGate)
			}

			// html — carries HumanGate too, via ThreadViews[].OpenItems
			// only (see this file's package doc comment: html.Item, the
			// Inbox/Outbox row shape, carries no pendency fields at all).
			htmlItem, ok := csFindHTMLOpenItem(htmlData, c.id)
			if !ok {
				t.Fatalf("html: no threadViews[].open_items entry %q", c.id)
			}
			assertOwedMove(t, "html", c.name, htmlItem.WaitingOn, htmlItem.ExpectedTransition, htmlItem.Why, c.want)
			if htmlItem.HumanGate != c.want.HumanGate {
				t.Errorf("html: case %q human_gate = %q, want %q", c.name, htmlItem.HumanGate, c.want.HumanGate)
			}
		})
	}
}

// --- AC4: undeclared reads distinctly from absent --------------------------
//
// Spec 05-declared-nature.md AC4 (agent-exchange-2026-08 P5): an
// x_operational[] item's declared `state: absent` must read DIFFERENTLY
// from a name that was never declared at all — checked here on all five
// surfaces crossSurfaceReaders (above) enumerates: inbox, outbox, thread,
// html, show. TestCrossSurfaceOperationalItems is the one subtest that
// actually walks the operational-item projection; every OTHER TestCrossSurface
// subtest exercises the pendency verdict, a separate relation.
//
// inbox/outbox could not carry this projection until epic-backlog B25 closed:
// cache.Item (the wire shape) and toItem (its own populating function) live
// in internal/cache/types.go and store.go — the wave that shipped `thread`/
// `html`/`show` was not granted either file. Both now carry
// `operational_items` (cache.Item.OperationalItems), populated by toItem from
// the SAME foldedArtifact.OperationalItems (mirror.go's DeriveOperationalItems
// output) the other three surfaces already share — one rule, one
// implementation, five readers.

// writeCrossSurfaceOperationalContract is a schema-faithful, envelope/v2
// contract writer local to this file — helpers_test.go's own
// writeContractDescriptorFor writes envelope/v1, which carries no
// x_operational field at all (v2-only,
// schemas/envelope/v2/contract.schema.json), and that file is off this
// wave's allowlist. xOperationalRaw is inserted as-is (mirroring
// internal/cli/cmd_contract_test.go's own
// writeContractDescriptorWithXOperational), so a caller may declare any
// items, or none — the same "schema-faithful local variant" precedent
// writeDecisionArtifactWithThread (above) already sets for this file. No
// consumes.yaml is ever written for either fixture this test builds:
// OperationalDebtOwed (a wholly separate P5 derivation, never conditioned
// on x_operational — mirror.go's own doc comment) must stay out of this
// proof entirely.
func writeCrossSurfaceOperationalContract(t *testing.T, mirrorDir, slug, xOperationalRaw string) string {
	t.Helper()
	id := "XC-axon-" + slug
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: " + crossSurfaceSpaceID + "\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"1.0.0\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		xOperationalRaw +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/"+slug+"/contract.md", content)
	return id
}

func TestCrossSurfaceOperationalItems(t *testing.T) {
	mirrorDir := t.TempDir()
	writeMirrorFile(t, mirrorDir, "space.yaml", crossSurfaceManifestYAML(crossSurfaceSpaceID, []crossSurfaceParticipant{
		{"axon", "active"},
		{"beta", "active"},
	}))

	// declared: endpoint is explicitly `state: absent` — a producer who
	// SAID "not yet".
	declaredID := writeCrossSurfaceOperationalContract(t, mirrorDir, "cs-declared",
		"x_operational:\n  - name: endpoint\n    state: absent\n")
	// undeclared: no x_operational field at all — a producer who said
	// nothing. Both must be readable, and they must not read the same.
	undeclaredID := writeCrossSurfaceOperationalContract(t, mirrorDir, "cs-undeclared", "")
	// nonContract: a decision, addressed axon -> beta, same fixture. Proves
	// mirror.go's KindContract gate (buildIndex, "computing operational rows
	// for a question or a decision is noise, not fidelity") reaches inbox/
	// outbox too, not only thread/html/show — see
	// non_contract_carries_no_operational_rows below.
	nonContractID := "XD-axon-cs-nc"
	writeDecisionArtifactWithThread(t, mirrorDir, nonContractID, e2eFixtureThread, []string{"beta"})

	threadResult := csRunThread(t, mirrorDir)
	htmlData := csRunHTML(t, mirrorDir)
	inboxItems := csRunInbox(t, mirrorDir, "beta")
	outboxItems := csRunOutbox(t, mirrorDir)

	cases := []struct {
		name string
		id   string
		want string // endpoint's own state on this fixture
	}{
		{"declared-absent", declaredID, "absent"},
		{"never-declared", undeclaredID, "undeclared"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			threadItem, ok := csFindOpenItem(threadResult.OpenItems, c.id)
			if !ok {
				t.Fatalf("thread: no open_items entry %q", c.id)
			}
			endpoint, ok := csFindOperationalItem(threadItem.OperationalItems, "endpoint")
			if !ok {
				t.Fatalf("thread: %q carries no operational_items entry named endpoint (items = %+v)", c.id, threadItem.OperationalItems)
			}
			if endpoint.State != c.want {
				t.Errorf("thread: %q endpoint state = %q, want %q", c.id, endpoint.State, c.want)
			}
			// A well-known name NEITHER fixture ever mentions must read
			// undeclared on both — the union rule's other half: silence on
			// a name is undeclared regardless of what a DIFFERENT name on
			// the same contract declared.
			for _, other := range []string{"credential-channel", "registration"} {
				item, ok := csFindOperationalItem(threadItem.OperationalItems, other)
				if !ok {
					t.Fatalf("thread: %q carries no operational_items entry named %s (items = %+v)", c.id, other, threadItem.OperationalItems)
				}
				if item.State != "undeclared" {
					t.Errorf("thread: %q %s state = %q, want undeclared (neither fixture ever declares it)", c.id, other, item.State)
				}
			}

			htmlItem, ok := csFindHTMLOpenItem(htmlData, c.id)
			if !ok {
				t.Fatalf("html: no threadViews[].open_items entry %q", c.id)
			}
			htmlEndpoint, ok := csFindOperationalItem(htmlItem.OperationalItems, "endpoint")
			if !ok {
				t.Fatalf("html: %q carries no operational_items entry named endpoint", c.id)
			}
			if htmlEndpoint.State != c.want {
				t.Errorf("html: %q endpoint state = %q, want %q", c.id, htmlEndpoint.State, c.want)
			}

			show := csRunShow(t, mirrorDir, c.id)
			showEndpoint, ok := csFindOperationalItem(show.OperationalItems, "endpoint")
			if !ok {
				t.Fatalf("show: %q carries no operational_items entry named endpoint", c.id)
			}
			if showEndpoint.State != c.want {
				t.Errorf("show: %q endpoint state = %q, want %q", c.id, showEndpoint.State, c.want)
			}

			// inbox (as beta, the addressee of both fixture contracts) —
			// epic-backlog B25's own gap: cache.Item carried no field for
			// this projection until now.
			inItem, ok := csFindItem(inboxItems, c.id)
			if !ok {
				t.Fatalf("inbox (as beta): no item %q", c.id)
			}
			inEndpoint, ok := csFindOperationalItem(inItem.OperationalItems, "endpoint")
			if !ok {
				t.Fatalf("inbox: %q carries no operational_items entry named endpoint (items = %+v)", c.id, inItem.OperationalItems)
			}
			if inEndpoint.State != c.want {
				t.Errorf("inbox: %q endpoint state = %q, want %q", c.id, inEndpoint.State, c.want)
			}

			// outbox (as axon, the sender of both fixture contracts).
			outItem, ok := csFindItem(outboxItems, c.id)
			if !ok {
				t.Fatalf("outbox: no item %q", c.id)
			}
			outEndpoint, ok := csFindOperationalItem(outItem.OperationalItems, "endpoint")
			if !ok {
				t.Fatalf("outbox: %q carries no operational_items entry named endpoint (items = %+v)", c.id, outItem.OperationalItems)
			}
			if outEndpoint.State != c.want {
				t.Errorf("outbox: %q endpoint state = %q, want %q", c.id, outEndpoint.State, c.want)
			}
		})
	}

	// Cross-fixture proof, stated directly rather than only implied by the
	// two cases above: the SAME well-known name reads two DIFFERENT values
	// depending on what its own contract actually declared — AC4's literal
	// claim ("reads distinctly"), not two independently-true facts that
	// merely happen not to collide. This is the assertion the plan's own
	// "make the derivation unconditional" mutation is aimed at: collapsing
	// declared/undeclared to one rendering makes this red and nothing else
	// in this file catches it (the per-case checks above would both still
	// pass against a single WRONG constant value).
	t.Run("same_name_reads_distinctly_across_fixtures", func(t *testing.T) {
		declaredItem, ok := csFindOpenItem(threadResult.OpenItems, declaredID)
		if !ok {
			t.Fatalf("thread: no open_items entry %q", declaredID)
		}
		undeclaredItem, ok := csFindOpenItem(threadResult.OpenItems, undeclaredID)
		if !ok {
			t.Fatalf("thread: no open_items entry %q", undeclaredID)
		}
		declaredEndpoint, ok := csFindOperationalItem(declaredItem.OperationalItems, "endpoint")
		if !ok {
			t.Fatalf("thread: %q carries no operational_items entry named endpoint", declaredID)
		}
		undeclaredEndpoint, ok := csFindOperationalItem(undeclaredItem.OperationalItems, "endpoint")
		if !ok {
			t.Fatalf("thread: %q carries no operational_items entry named endpoint", undeclaredID)
		}
		if declaredEndpoint.State == undeclaredEndpoint.State {
			t.Fatalf("endpoint state collapsed to one rendering: declared=%q undeclared=%q, want them distinct",
				declaredEndpoint.State, undeclaredEndpoint.State)
		}

		// The same proof, on the two surfaces this wave (epic-backlog B25)
		// closes: a reader that only ever saw ONE of the two contracts could
		// not tell whether it had built a real projection or a hardcoded
		// constant. inbox/outbox must not collapse the two either.
		declaredInItem, ok := csFindItem(inboxItems, declaredID)
		if !ok {
			t.Fatalf("inbox (as beta): no item %q", declaredID)
		}
		undeclaredInItem, ok := csFindItem(inboxItems, undeclaredID)
		if !ok {
			t.Fatalf("inbox (as beta): no item %q", undeclaredID)
		}
		declaredInEndpoint, ok := csFindOperationalItem(declaredInItem.OperationalItems, "endpoint")
		if !ok {
			t.Fatalf("inbox: %q carries no operational_items entry named endpoint", declaredID)
		}
		undeclaredInEndpoint, ok := csFindOperationalItem(undeclaredInItem.OperationalItems, "endpoint")
		if !ok {
			t.Fatalf("inbox: %q carries no operational_items entry named endpoint", undeclaredID)
		}
		if declaredInEndpoint.State == undeclaredInEndpoint.State {
			t.Fatalf("inbox: endpoint state collapsed to one rendering: declared=%q undeclared=%q, want them distinct",
				declaredInEndpoint.State, undeclaredInEndpoint.State)
		}

		declaredOutItem, ok := csFindItem(outboxItems, declaredID)
		if !ok {
			t.Fatalf("outbox: no item %q", declaredID)
		}
		undeclaredOutItem, ok := csFindItem(outboxItems, undeclaredID)
		if !ok {
			t.Fatalf("outbox: no item %q", undeclaredID)
		}
		declaredOutEndpoint, ok := csFindOperationalItem(declaredOutItem.OperationalItems, "endpoint")
		if !ok {
			t.Fatalf("outbox: %q carries no operational_items entry named endpoint", declaredID)
		}
		undeclaredOutEndpoint, ok := csFindOperationalItem(undeclaredOutItem.OperationalItems, "endpoint")
		if !ok {
			t.Fatalf("outbox: %q carries no operational_items entry named endpoint", undeclaredID)
		}
		if declaredOutEndpoint.State == undeclaredOutEndpoint.State {
			t.Fatalf("outbox: endpoint state collapsed to one rendering: declared=%q undeclared=%q, want them distinct",
				declaredOutEndpoint.State, undeclaredOutEndpoint.State)
		}
	})

	// mirror.go gates the whole projection on fold.KindContract ("computing
	// operational rows for a question or a decision is noise, not
	// fidelity") — proven on thread/html/show already; this closes the same
	// proof on inbox/outbox, the two surfaces this wave adds.
	t.Run("non_contract_carries_no_operational_rows", func(t *testing.T) {
		threadItem, ok := csFindOpenItem(threadResult.OpenItems, nonContractID)
		if !ok {
			t.Fatalf("thread: no open_items entry %q", nonContractID)
		}
		if len(threadItem.OperationalItems) != 0 {
			t.Errorf("thread: non-contract %q operational_items = %+v, want none", nonContractID, threadItem.OperationalItems)
		}

		htmlItem, ok := csFindHTMLOpenItem(htmlData, nonContractID)
		if !ok {
			t.Fatalf("html: no threadViews[].open_items entry %q", nonContractID)
		}
		if len(htmlItem.OperationalItems) != 0 {
			t.Errorf("html: non-contract %q operational_items = %+v, want none", nonContractID, htmlItem.OperationalItems)
		}

		show := csRunShow(t, mirrorDir, nonContractID)
		if len(show.OperationalItems) != 0 {
			t.Errorf("show: non-contract %q operational_items = %+v, want none", nonContractID, show.OperationalItems)
		}

		inItem, ok := csFindItem(inboxItems, nonContractID)
		if !ok {
			t.Fatalf("inbox (as beta): no item %q", nonContractID)
		}
		if len(inItem.OperationalItems) != 0 {
			t.Errorf("inbox: non-contract %q operational_items = %+v, want none", nonContractID, inItem.OperationalItems)
		}

		outItem, ok := csFindItem(outboxItems, nonContractID)
		if !ok {
			t.Fatalf("outbox: no item %q", nonContractID)
		}
		if len(outItem.OperationalItems) != 0 {
			t.Errorf("outbox: non-contract %q operational_items = %+v, want none", nonContractID, outItem.OperationalItems)
		}
	})
}
