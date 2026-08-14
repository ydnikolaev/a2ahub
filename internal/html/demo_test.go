package html

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	contractcore "github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

// TestDemoFixtureParses verifies the design demo fixture
// (testdata/demo.json) unmarshals exactly into the Data model — a
// field-name typo in the fixture (or a drifted model) fails via
// DisallowUnknownFields — and that it exercises every drift value, every
// artifact type across the complete demo, and the hostile-title corner case the dashboard must
// render safely.
func TestDemoFixtureParses(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/demo.json")
	if err != nil {
		t.Fatalf("read testdata/demo.json: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var data Data
	if err := dec.Decode(&data); err != nil {
		t.Fatalf("decode testdata/demo.json into Data: %v", err)
	}

	if len(data.Nodes) < 5 {
		t.Errorf("len(Nodes) = %d, want >= 5", len(data.Nodes))
	}
	if data.Meta.Schema != "a2a-design-demo/v4" || !data.Meta.Synthetic {
		t.Fatalf("demo metadata = %+v, want admitted synthetic v4 contract", data.Meta)
	}
	if data.Operational.SchemaVersion != 1 || len(data.Operational.Timeline) == 0 {
		t.Fatalf("raw demo fixture lacks its generated operational projection: %+v", data.Operational)
	}
	wantCounts := map[string][2]int{
		"spaces": {len(data.Spaces), 3}, "nodes": {len(data.Nodes), 7},
		"contracts": {len(data.Contracts), 8}, "contractEdges": {len(data.ContractEdges), 10},
		"exchangeEdges": {len(data.ExchangeEdges), 12}, "inbox": {len(data.Inbox), 10},
		"outbox": {len(data.Outbox), 7}, "threadViews": {len(data.ThreadViews), 5}, "workReports": {len(data.WorkReports), 8},
		"artifactDetails": {len(data.ArtifactDetails), 10}, "unavailable": {len(data.Unavailable), 4},
	}

	durableByArtifact := make(map[string]WorkReport, len(data.WorkReports))
	for _, report := range data.WorkReports {
		if report.Space == "" || report.Thread == "" || report.ArtifactID == "" || report.SubjectRef == "" || report.ReportedAt == "" {
			t.Errorf("demo work report lacks durable identity: %+v", report)
		}
		durableByArtifact[report.ArtifactID] = report
	}
	for _, row := range data.Operational.Timeline {
		for _, work := range row.Work {
			if work.CommittedCheckpoint == nil {
				continue
			}
			report, ok := durableByArtifact[work.CommittedCheckpoint.ArtifactID]
			if !ok {
				t.Errorf("committed checkpoint %s has no durable demo history row", work.CommittedCheckpoint.ArtifactID)
				continue
			}
			if report.ReportedAt != work.CommittedCheckpoint.ReportedAt.UTC().Format(time.RFC3339Nano) {
				t.Errorf("durable report %s time = %s, checkpoint = %s; local observation may have leaked into history", report.ArtifactID, report.ReportedAt, work.CommittedCheckpoint.ReportedAt.UTC().Format(time.RFC3339Nano))
			}
		}
	}
	for name, gotWant := range wantCounts {
		if gotWant[0] != gotWant[1] {
			t.Errorf("demo %s count = %d, want %d", name, gotWant[0], gotWant[1])
		}
	}

	wantDrifts := map[string]bool{
		"current": false, "behind": false, "deprecated": false,
		"retired": false, "missing": false, "dangling": false,
	}
	for _, e := range data.ContractEdges {
		if _, ok := wantDrifts[e.Drift]; ok {
			wantDrifts[e.Drift] = true
		}
	}
	for drift, seen := range wantDrifts {
		if !seen {
			t.Errorf("no ContractEdge with drift %q in demo.json", drift)
		}
	}

	wantTypes := map[string]bool{
		"question": false, "work_request": false, "contract": false,
		"requirement": false, "decision": false, "handoff": false,
		"response": false, "announcement": false,
	}
	for _, it := range append(append([]Item{}, data.Inbox...), data.Outbox...) {
		if _, ok := wantTypes[it.Type]; ok {
			wantTypes[it.Type] = true
		}
	}
	for _, detail := range data.ArtifactDetails {
		if _, ok := wantTypes[detail.Type]; ok {
			wantTypes[detail.Type] = true
		}
	}
	for typ, seen := range wantTypes {
		if !seen {
			t.Errorf("no demo record with type %q in demo.json", typ)
		}
	}

	found := false
	for _, it := range append(append([]Item{}, data.Inbox...), data.Outbox...) {
		if strings.Contains(it.Title, `</script><img`) && strings.Contains(it.Title, "onerror=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no demo item carries the hostile script/image-handler title verbatim")
	}

	for _, flag := range data.Flags {
		if strings.HasPrefix(flag.Code, "V") {
			t.Errorf("demo flag %q looks like an invented validation code; demo may only carry mounted fold/cache-index facts", flag.Code)
		}
		if flag.Source != "fold" && flag.Source != "cache-index" {
			t.Errorf("demo flag %q has unsupported source %q", flag.Code, flag.Source)
		}
	}

	matchingReceipt, mismatchReceipt, attributed := false, false, false
	for _, view := range data.ThreadViews {
		for _, row := range view.Transcript {
			if row.Event == nil {
				continue
			}
			if row.Event.ProducedBy.Tool != "" && row.Event.ProducedBy.Version != "" {
				attributed = true
			}
			if row.Event.ClaimedState != "" && row.Event.Consistency == nil {
				matchingReceipt = true
			}
			if row.Event.Consistency != nil && row.Event.Consistency.Kind == "state-claim-mismatch" &&
				row.Event.Consistency.Actual != "" && row.Event.Consistency.Claimed != "" {
				mismatchReceipt = true
			}
		}
	}
	if !matchingReceipt || !mismatchReceipt || !attributed {
		t.Errorf("receipt demo coverage: matching=%v mismatch=%v attributed=%v", matchingReceipt, mismatchReceipt, attributed)
	}
}

// updateDemoCopy rewrites the published demo projection instead of asserting
// it, the same `-update` idiom internal/livee2e/report_test.go uses for its
// golden reports.
var updateDemoCopy = flag.Bool("update-demo", false, "rewrite internal/html/demo-data.json from the derived demo model")

// TestDemoPublishedCopyMatchesTheDerivedModel guards internal/html/demo-data.json,
// which is the payload the SITE serves: web/scripts/generate-content.mjs reads
// it, adds the three newest releases, and writes web/public/demo-data.json,
// which the dashboard page fetches when it has no embedded snapshot.
//
// It used to be a byte copy of testdata/demo.json, and that made it the one
// place the two dashboard surfaces could disagree. `a2a html --demo` renders
// DemoData() — the fixture with deriveDemoOwnership, deriveDemoRowFacts and
// deriveDemoDerivedState applied — while the site rendered the raw fixture,
// which authors none of those. The local dashboard therefore knew a `closed`
// question was terminal and the public one did not, from the same fixture, in
// the release that added the field.
//
// So the published copy is now the DERIVED model, and the site consumes the
// domain's answer without re-deriving it in JavaScript — which SKILL.md's
// first rule forbids and which is how the browser got `retired` wrong before
// 0.19.10. ReleaseNotes are stripped: DemoData() attaches the whole embedded
// corpus (~380 KB of it), both consumers attach their own, and shipping a
// third copy to the site would be dead weight on every page load.
//
// Regenerate with: go test ./internal/html/ -run PublishedCopy -update-demo
func TestDemoPublishedCopyMatchesTheDerivedModel(t *testing.T) {
	want, err := publishedDemoCopy()
	if err != nil {
		t.Fatalf("build published demo copy: %v", err)
	}
	if *updateDemoCopy {
		if err := os.WriteFile("demo-data.json", want, 0o644); err != nil {
			t.Fatalf("write published demo copy: %v", err)
		}
		t.Log("rewrote internal/html/demo-data.json")
		return
	}
	published, err := os.ReadFile("demo-data.json")
	if err != nil {
		t.Fatalf("read published demo copy: %v", err)
	}
	if !bytes.Equal(want, published) {
		t.Fatalf("internal/html/demo-data.json drifted from the derived demo model (%d bytes published, %d derived).\n"+
			"This file is what the SITE serves — regenerate it:\n"+
			"  go test ./internal/html/ -run PublishedCopy -update-demo", len(published), len(want))
	}
}

// publishedDemoCopy is the derived demo model as the site consumes it: every
// field DemoData() derives, minus the embedded release-note corpus that both
// consumers replace with their own selection.
func publishedDemoCopy() ([]byte, error) {
	d, err := DemoData()
	if err != nil {
		return nil, err
	}
	d.ReleaseNotes = nil
	b, err := MarshalData(d)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func TestDemoOperationalSnapshotUsesSharedProjection(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	if d.Operational.SchemaVersion != 1 || d.Operational.Revision == "" || len(d.Operational.Timeline) == 0 {
		t.Fatalf("operational snapshot is not a canonical shared projection: %#v", d.Operational)
	}
	foundConcurrentWork := false
	wantModes := map[string]bool{
		"planning": false, "implementing": false, "testing": false, "reviewing": false,
		"waiting": false, "paused": false, "finished": false,
	}
	wantFreshness := map[string]bool{
		"local-current": false, "committed-current": false, "stale": false,
		"finished": false, "pending-recovery": false, "unknown": false,
	}
	foundExplainedWait := false
	for _, row := range d.Operational.Timeline {
		if row.Space == "" || row.Thread == "" || row.Title == "" {
			t.Fatalf("operational row lacks qualified process identity: %+v", row)
		}
		if len(row.Work) > 1 {
			foundConcurrentWork = true
		}
		for _, work := range row.Work {
			wantModes[string(work.Mode)] = true
			wantFreshness[string(work.Freshness)] = true
			for _, wait := range work.WaitingOn {
				if wait.ID != "" && wait.Kind != "" && wait.Summary != "" {
					foundExplainedWait = true
				}
			}
		}
	}
	if !foundConcurrentWork {
		t.Fatal("demo snapshot does not exercise simultaneous work on one process")
	}
	for mode, seen := range wantModes {
		if !seen {
			t.Errorf("demo operational snapshot does not exercise work mode %q", mode)
		}
	}
	for freshness, seen := range wantFreshness {
		if !seen {
			t.Errorf("demo operational snapshot does not exercise work freshness %q", freshness)
		}
	}
	if !foundExplainedWait {
		t.Error("demo work wait has no typed, human-readable reason")
	}
}

func TestDemoCommittedWorkEvidenceComesFromWorkReports(t *testing.T) {
	t.Parallel()

	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	evidence, err := demoCommittedWork(d.WorkReports)
	if err != nil {
		t.Fatalf("demoCommittedWork: %v", err)
	}
	if len(evidence) != len(d.WorkReports) {
		t.Fatalf("committed work evidence count = %d, want %d", len(evidence), len(d.WorkReports))
	}

	type qualifiedArtifact struct{ space, artifactID string }
	projected := make(map[qualifiedArtifact]struct {
		space, thread string
		work          operational.Work
	})
	for _, row := range d.Operational.Timeline {
		for _, work := range row.Work {
			if work.CommittedCheckpoint != nil {
				projected[qualifiedArtifact{row.Space, work.CommittedCheckpoint.ArtifactID}] = struct {
					space, thread string
					work          operational.Work
				}{space: row.Space, thread: row.Thread, work: work}
			}
		}
	}

	for index, report := range d.WorkReports {
		gotEvidence := evidence[index]
		wantActor := workreport.Actor{Kind: report.Actor.Kind, Name: report.Actor.Name, System: report.Actor.System, Model: report.Actor.Model, Session: report.Actor.Session}
		wantWaiting := make([]workreport.WaitingOn, len(report.WaitingOn))
		for i, waiting := range report.WaitingOn {
			wantWaiting[i] = workreport.WaitingOn{Kind: workreport.WaitKind(waiting.Kind), ID: waiting.ID, Summary: waiting.Summary}
		}
		wantProjectedWaiting := append([]workreport.WaitingOn{}, wantWaiting...)
		sort.Slice(wantProjectedWaiting, func(i, j int) bool {
			if wantProjectedWaiting[i].Kind != wantProjectedWaiting[j].Kind {
				return wantProjectedWaiting[i].Kind < wantProjectedWaiting[j].Kind
			}
			if wantProjectedWaiting[i].ID != wantProjectedWaiting[j].ID {
				return wantProjectedWaiting[i].ID < wantProjectedWaiting[j].ID
			}
			return wantProjectedWaiting[i].Summary < wantProjectedWaiting[j].Summary
		})
		wantReportedAt, parseErr := time.Parse(time.RFC3339Nano, report.ReportedAt)
		if parseErr != nil {
			t.Fatalf("report %s reported_at %q: %v", report.ArtifactID, report.ReportedAt, parseErr)
		}
		var wantValidUntil time.Time
		if report.ValidUntil != "" {
			wantValidUntil, parseErr = time.Parse(time.RFC3339Nano, report.ValidUntil)
			if parseErr != nil {
				t.Fatalf("report %s valid_until %q: %v", report.ArtifactID, report.ValidUntil, parseErr)
			}
		}

		if gotEvidence.Space != report.Space || gotEvidence.Thread != report.Thread ||
			gotEvidence.WorkID != report.WorkID || gotEvidence.SubjectRef != report.SubjectRef ||
			gotEvidence.Mode != workreport.Mode(report.Mode) || gotEvidence.Summary != report.Summary ||
			gotEvidence.ArtifactID != report.ArtifactID || gotEvidence.CommitSequence != report.CommitSequence ||
			!gotEvidence.ReportedAt.Equal(wantReportedAt) || !gotEvidence.ValidUntil.Equal(wantValidUntil) ||
			gotEvidence.Actor != wantActor || !reflect.DeepEqual(gotEvidence.WaitingOn, wantWaiting) {
			t.Errorf("report %s was not converted to complete committed evidence: got %+v", report.ArtifactID, gotEvidence)
		}

		got, ok := projected[qualifiedArtifact{report.Space, report.ArtifactID}]
		if !ok {
			t.Errorf("report %s has no operational checkpoint", report.ArtifactID)
			continue
		}
		if got.space != report.Space || got.thread != report.Thread ||
			got.work.CommittedCheckpoint.ArtifactID != report.ArtifactID ||
			!got.work.CommittedCheckpoint.ReportedAt.Equal(wantReportedAt) ||
			!equalTimePointer(got.work.CommittedCheckpoint.ValidUntil, wantValidUntil) {
			t.Errorf("report %s has an incomplete operational checkpoint: %+v", report.ArtifactID, got.work)
		}
		if got.work.Source == "local-lease" {
			continue // The current local lease deliberately supersedes this committed checkpoint.
		}
		if got.work.Source != "committed-checkpoint" {
			t.Errorf("report %s projection source = %q, want committed checkpoint or superseding local lease", report.ArtifactID, got.work.Source)
			continue
		}
		if got.work.WorkID != report.WorkID ||
			got.work.SubjectRef != report.SubjectRef || got.work.Mode != workreport.Mode(report.Mode) ||
			got.work.Summary != report.Summary || got.work.Actor.Kind != report.Actor.Kind ||
			got.work.Actor.Name != report.Actor.Name || got.work.Actor.System != report.Actor.System ||
			got.work.Actor.Model != report.Actor.Model || got.work.Actor.Session != report.Actor.Session ||
			!reflect.DeepEqual(got.work.WaitingOn, wantProjectedWaiting) {
			t.Errorf("report %s was not projected with complete committed-work semantics: %+v", report.ArtifactID, got.work)
		}
	}
}

func equalTimePointer(got *time.Time, want time.Time) bool {
	return (got == nil && want.IsZero()) || (got != nil && got.Equal(want))
}

func TestDemoIsTwoFullSpacesPlusOneHonestUnavailableSpace(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	readable, limited := map[string]bool{}, map[string]bool{}
	for _, space := range d.Spaces {
		if space.Readable {
			readable[space.ID] = true
		} else {
			limited[space.ID] = true
		}
	}
	if len(readable) != 2 || !readable["checkout-core"] || !readable["customer-ops"] {
		t.Fatalf("readable demo spaces = %v, want checkout-core and customer-ops", readable)
	}
	if len(limited) != 1 || !limited["archive-migration"] {
		t.Fatalf("limited demo spaces = %v, want archive-migration", limited)
	}

	rowsBySpace := map[string]int{}
	for _, row := range d.Operational.Timeline {
		rowsBySpace[row.Space]++
	}
	for space := range readable {
		if rowsBySpace[space] == 0 {
			t.Errorf("readable space %q has no operational row", space)
		}
	}
	if rowsBySpace["archive-migration"] != 0 {
		t.Errorf("limited space has %d fabricated operational rows", rowsBySpace["archive-migration"])
	}

	limitedSourceUnavailable, limitedReason := false, false
	for _, source := range d.Operational.Sources {
		if source.Kind == "space" && source.Space == "archive-migration" && source.Freshness == "unavailable" {
			limitedSourceUnavailable = true
		}
	}
	for _, unavailable := range d.Operational.Unavailable {
		if unavailable.Space == "archive-migration" {
			limitedReason = true
		}
	}
	if !limitedSourceUnavailable || !limitedReason {
		t.Fatalf("limited space explanation: source unavailable=%v reason=%v", limitedSourceUnavailable, limitedReason)
	}
}

func TestDemoOperationalEvidenceResolvesWithinItsSpace(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	type qualified struct{ space, id string }
	views := map[qualified]bool{}
	subjects := map[qualified]bool{}
	for _, view := range d.ThreadViews {
		views[qualified{view.Space, view.Thread}] = true
		for _, artifact := range view.Artifacts {
			subjects[qualified{view.Space, artifact.ID}] = true
		}
	}
	for _, detail := range d.ArtifactDetails {
		subjects[qualified{detail.Space, detail.ID}] = true
	}
	for _, contract := range d.Contracts {
		subjects[qualified{contract.Space, contract.ID}] = true
	}

	for _, row := range d.Operational.Timeline {
		if !views[qualified{row.Space, row.Thread}] {
			t.Errorf("phantom operational thread %s/%s", row.Space, row.Thread)
		}
		for _, work := range row.Work {
			id := strings.SplitN(work.SubjectRef, "@", 2)[0]
			if !subjects[qualified{row.Space, id}] {
				t.Errorf("work %s subject %q does not resolve in %s", work.WorkID, work.SubjectRef, row.Space)
			}
		}
	}
}

func TestDemoDataCarriesEmbeddedReleaseNotes(t *testing.T) {
	t.Parallel()
	data, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	if len(data.ReleaseNotes) == 0 {
		t.Fatal("a2a html --demo must demonstrate the embedded release-note surface")
	}
	latest := data.ReleaseNotes[len(data.ReleaseNotes)-1]
	foundCurrentIssue := false
	for _, change := range latest.Changes {
		if change.ID == "KI-MACOS-ADHOC-SIGNING" {
			foundCurrentIssue = true
		}
	}
	if !foundCurrentIssue {
		t.Fatalf("latest HTML release note omits the standing known issue: %+v", latest)
	}
}

// TestDemoCarriesARollingWindow pins that the demo fixture actually
// DEMONSTRATES the per-version contract lifecycle (P4), which is the whole
// reason `--demo` exists: it is what a designer, a screenshot, and a new
// reader see, and a demo that shows only the pre-P4 shape teaches the wrong
// model to everyone who looks at it before they look at the code.
//
// Three shapes, deliberately, because each renders differently:
//   - a live window (several versions, at least one deprecated) — the steady
//     state the whole phase exists to make expressible;
//   - a window that contains a retired line beside a live successor;
//   - a fully retired legacy contract. The dense v4 fixture intentionally
//     models every contract with a version window; version-less compatibility
//     remains a unit concern rather than taking a design-demo slot.
func TestDemoCarriesARollingWindow(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	var live, retiredLine, fullyRetired int
	for _, c := range d.Contracts {
		if len(c.Versions) < 2 {
			if len(c.Versions) == 1 && c.Versions[0].State == "retired" && c.State == "retired" {
				fullyRetired++
			}
			continue
		}
		published, deprecated, retired := 0, 0, 0
		for _, v := range c.Versions {
			switch v.State {
			case "published":
				published++
			case "deprecated":
				deprecated++
			case "retired":
				retired++
			}
		}
		if published > 0 && deprecated > 0 {
			live++
		}
		if published > 0 && retired > 0 {
			retiredLine++
		}
	}

	if live == 0 {
		t.Error("no demo contract shows a live rolling window (>=1 published AND >=1 deprecated version) — " +
			"the demo teaches the pre-P4 model of one version per contract")
	}
	if retiredLine == 0 {
		t.Error("no demo contract shows a retired line beside a live successor")
	}
	if fullyRetired == 0 {
		t.Error("no demo contract shows a fully retired legacy window")
	}

	// The window is rendered in TWO places, and the first revision shipped
	// only one of them: under "You provide". The demo's own `self` provided
	// nothing, so that block never rendered at all and the feature was
	// invisible in the only page anyone looks at before reading code. Both
	// sides are pinned here because the consumer's side is the one that
	// matters more — during a sunset the question is not "what exists" but
	// "what is happening to the line I am pinned to".
	var selfProvidesAWindow, selfConsumesAWindow bool
	byID := map[string][]ContractVersion{}
	for _, c := range d.Contracts {
		byID[c.ID] = c.Versions
		if c.Provider == d.Self && len(c.Versions) >= 2 {
			selfProvidesAWindow = true
		}
	}
	for _, e := range d.ContractEdges {
		if e.From == d.Self && len(byID[e.Contract]) >= 2 {
			selfConsumesAWindow = true
		}
	}
	if !selfProvidesAWindow {
		t.Errorf("no contract PROVIDED by the demo's own system (%q) has a version window — the "+
			"\"You provide\" block is where the producer's own window renders, and it does not render at all "+
			"when self provides nothing", d.Self)
	}
	if !selfConsumesAWindow {
		t.Errorf("no contract CONSUMED by the demo's own system (%q) has a version window — that is the "+
			"dependency row, and it is where a consumer reads which of the provider's lines exist beside "+
			"their own pinned one", d.Self)
	}
}

func TestContractVersionDetailStrictDecode(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"version":"9.9.9","state":"published","detail":{"status":"available","invented":true}}`)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var version ContractVersion
	if err := dec.Decode(&version); err == nil || !strings.Contains(err.Error(), "invented") {
		t.Fatalf("unknown nested detail field decoded without a useful error: %v", err)
	}
}

func TestDemoContractVersionDetailsAreVersionScoped(t *testing.T) {
	t.Parallel()

	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	var atlas *Contract
	unavailable := 0
	documentDigests := map[string]string{}
	historyEvents := map[string]string{}
	provenanceSentinels := map[string]string{}
	previewLanguages := map[string]bool{}

	for ci := range d.Contracts {
		contract := &d.Contracts[ci]
		if contract.ID == "XC-atlas-order-envelope" {
			atlas = contract
		}
		for _, version := range contract.Versions {
			detail := version.Detail
			if detail == nil {
				continue
			}
			owner := contract.ID + "@" + version.Version
			switch detail.Status {
			case ContractVersionDetailUnavailable:
				unavailable++
				if strings.TrimSpace(detail.UnavailableReason) == "" {
					t.Errorf("%s unavailable detail has no reason", owner)
				}
			case ContractVersionDetailAvailable:
				if detail.UnavailableReason != "" {
					t.Errorf("%s available detail carries unavailableReason %q", owner, detail.UnavailableReason)
				}
				if detail.Description == "" || detail.SchemaFormat == "" || detail.CompatPolicy == "" ||
					detail.PublishedAt == "" || detail.PublishedBy == "" || detail.ChangeSummary == "" {
					t.Errorf("%s available detail lacks human or publication context: %+v", owner, detail)
				}
				if _, parseErr := time.Parse(time.RFC3339, detail.PublishedAt); parseErr != nil {
					t.Errorf("%s publishedAt = %q: %v", owner, detail.PublishedAt, parseErr)
				}
				if len(detail.Documents) < 2 {
					t.Errorf("%s has %d documents, want a package with normative and supporting evidence", owner, len(detail.Documents))
				}
				normative, supporting := false, false
				for _, document := range detail.Documents {
					if document.Path == "" || document.Role == "" || document.Digest == "" || document.SizeBytes <= 0 {
						t.Errorf("%s has incomplete document metadata: %+v", owner, document)
					}
					if document.Normative {
						normative = true
					} else {
						supporting = true
					}
					if document.Preview == "" || len(document.Preview) > 2048 {
						t.Errorf("%s document %q preview length = %d, want bounded non-empty safe text", owner, document.Path, len(document.Preview))
					}
					previewLanguages[document.PreviewLanguage] = true
					if previous, exists := documentDigests[document.Digest]; exists {
						t.Errorf("document digest %q reused across %s and %s", document.Digest, previous, owner)
					}
					documentDigests[document.Digest] = owner
				}
				if !normative || !supporting {
					t.Errorf("%s package categories: normative=%v supporting=%v", owner, normative, supporting)
				}
				if len(detail.History) == 0 {
					t.Errorf("%s has no version lifecycle history", owner)
				}
				for _, event := range detail.History {
					if event.EventID == "" || event.Transition == "" || event.State == "" || event.Actor == "" {
						t.Errorf("%s has incomplete lifecycle event: %+v", owner, event)
					}
					if previous, exists := historyEvents[event.EventID]; exists {
						t.Errorf("history event %q reused across %s and %s", event.EventID, previous, owner)
					}
					historyEvents[event.EventID] = owner
				}
				if detail.Provenance == nil || detail.Provenance.Profile == "" || detail.Provenance.CommitSHA == "" ||
					detail.Provenance.TreeObjectID == "" || detail.Provenance.PublishedDigest == "" {
					t.Errorf("%s has incomplete immutable provenance: %+v", owner, detail.Provenance)
					break
				}
				profile := contractcore.DigestProfile(detail.Provenance.Profile)
				if profile != contractcore.ProfileContractTreeV1 && profile != contractcore.ProfileContractSetV2 {
					t.Errorf("%s uses non-canonical contract profile %q", owner, detail.Provenance.Profile)
				}
				if detail.Provenance.DigestProfile != detail.Provenance.Profile {
					t.Errorf("%s profile %q and digestProfile %q diverge", owner, detail.Provenance.Profile, detail.Provenance.DigestProfile)
				}
				for label, sentinel := range map[string]string{
					"commitSHA":       detail.Provenance.CommitSHA,
					"treeObjectID":    detail.Provenance.TreeObjectID,
					"publishedDigest": detail.Provenance.PublishedDigest,
				} {
					key := label + ":" + sentinel
					if previous, exists := provenanceSentinels[key]; exists {
						t.Errorf("provenance %s %q reused across %s and %s", label, sentinel, previous, owner)
					}
					provenanceSentinels[key] = owner
				}
			default:
				t.Errorf("%s has unknown detail status %q", owner, detail.Status)
			}
		}
	}

	if unavailable == 0 {
		t.Fatal("demo has no honest unavailable version detail with a reason")
	}
	if atlas == nil {
		t.Fatal("demo lacks XC-atlas-order-envelope")
	}
	wantVersions := map[string]bool{"1.5.0": false, "2.0.0": false, "2.1.0": false, "2.2.0": false}
	packages := map[string]string{}
	currentPin, retiredPin, legacyProfile := false, false, false
	for _, version := range atlas.Versions {
		if _, ok := wantVersions[version.Version]; !ok {
			continue
		}
		wantVersions[version.Version] = true
		if version.Detail == nil || version.Detail.Status != ContractVersionDetailAvailable {
			t.Fatalf("XC-atlas-order-envelope@%s has no available detail", version.Version)
		}
		paths := make([]string, 0, len(version.Detail.Documents))
		for _, document := range version.Detail.Documents {
			paths = append(paths, document.Path)
		}
		packages[version.Version] = strings.Join(paths, "|")
		for _, pin := range version.Detail.ConsumerPins {
			currentPin = currentPin || (version.Version == "2.2.0" && pin.System == "checkout" && pin.Drift == "current")
			retiredPin = retiredPin || (version.Version == "1.5.0" && pin.System == "fulfillment" && pin.Drift == "retired")
		}
		legacyProfile = legacyProfile || (version.Version == "1.5.0" &&
			version.Detail.Provenance.Profile == "contract-tree-v1" &&
			version.Detail.Provenance.DigestProfile == "contract-tree-v1")
	}
	for version, seen := range wantVersions {
		if !seen {
			t.Errorf("XC-atlas-order-envelope lacks required version %s", version)
		}
	}
	if packages["1.5.0"] == packages["2.1.0"] || packages["2.1.0"] == packages["2.2.0"] || packages["1.5.0"] == packages["2.2.0"] {
		t.Errorf("materially distinct version packages collapsed: %+v", packages)
	}
	if !currentPin || !retiredPin || !legacyProfile {
		t.Errorf("version-specific evidence: currentPin=%v retiredPin=%v legacyProfile=%v", currentPin, retiredPin, legacyProfile)
	}
	for _, language := range []string{"json", "yaml", "markdown"} {
		if !previewLanguages[language] {
			t.Errorf("demo contract packages do not exercise %s previews", language)
		}
	}
}

// TestDemoOwnershipIsDerivedNotAuthored guards the rule that put a whose-move
// panel over an empty list: the fixture states WaitingOn, and Pending /
// WaitingOthers are computed from it. Decoding a fixture that predates a
// derived field yields its zero value, which reads as "nothing is pending" —
// silently, and only on the demo surface.
func TestDemoOwnershipIsDerivedNotAuthored(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	pending := 0
	waitingByThread := map[string][]string{}
	for _, tv := range d.ThreadViews {
		for _, oi := range tv.OpenItems {
			if oi.Pending != (len(oi.WaitingOn) > 0) {
				t.Fatalf("%s: Pending=%v with WaitingOn=%v — Pending must be derived from WaitingOn", oi.ID, oi.Pending, oi.WaitingOn)
			}
			if !oi.Pending {
				continue
			}
			pending++
			waitingByThread[tv.Thread] = append(waitingByThread[tv.Thread], oi.WaitingOn...)
		}
	}
	if pending == 0 {
		t.Fatal("no open item in the demo fixture is pending; the whose-move panel would render empty on every thread")
	}

	for _, th := range d.Threads {
		for i, who := range th.WaitingOthers {
			if who == d.Self {
				t.Fatalf("%s: WaitingOthers contains self %q — YourMove already carries that", th.ID, d.Self)
			}
			if i > 0 && th.WaitingOthers[i-1] >= who {
				t.Fatalf("%s: WaitingOthers is not sorted and deduped: %v", th.ID, th.WaitingOthers)
			}
			if !containsString(waitingByThread[th.ID], who) {
				t.Fatalf("%s: WaitingOthers names %q, which owes no pending move on that thread", th.ID, who)
			}
		}
		if th.Settled && len(th.WaitingOthers) > 0 {
			t.Fatalf("%s: settled thread still names %v as owing a move", th.ID, th.WaitingOthers)
		}
	}
}

// TestDemoRowFactsAreDerivedNotAuthored is the same guard one field further on.
// The exchange list sorts on CreatedAt and the prompt button appears on
// YourMove; both decode to their zero value from a fixture that predates them,
// and a zeroed sort key looks like an order rather than like a bug.
func TestDemoRowFactsAreDerivedNotAuthored(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	open := map[string]ThreadOpenItem{}
	for _, tv := range d.ThreadViews {
		for _, oi := range tv.OpenItems {
			open[tv.Space+"/"+oi.ID] = oi
		}
	}

	rows, dated, ours := 0, 0, 0
	for _, list := range [][]Item{d.Inbox, d.Outbox, d.Archive} {
		for _, it := range list {
			rows++
			if it.Age == "" {
				if it.CreatedAt != "" {
					t.Errorf("%s: no age but CreatedAt=%q", it.ID, it.CreatedAt)
				}
				continue
			}
			if it.CreatedAt == "" {
				t.Errorf("%s: age %q with no CreatedAt — the exchange list would sort it at random", it.ID, it.Age)
				continue
			}
			created, parseErr := time.Parse(time.RFC3339, it.CreatedAt)
			if parseErr != nil {
				t.Errorf("%s: CreatedAt %q does not parse: %v", it.ID, it.CreatedAt, parseErr)
				continue
			}
			if got := humanizeAge(d.GeneratedAt, created); got != it.Age {
				t.Errorf("%s: CreatedAt reformats to %q, but the row is labelled %q", it.ID, got, it.Age)
			}
			dated++

			oi, hasOpen := open[it.Space+"/"+it.ID]
			if want := hasOpen && containsString(oi.WaitingOn, d.Self); it.YourMove != want {
				t.Errorf("%s: YourMove=%v, but the folded item waits on %v", it.ID, it.YourMove, oi.WaitingOn)
			}
			if it.YourMove {
				ours++
				if it.Prompt == nil {
					t.Errorf("%s: the move is ours and the button would have nothing to copy", it.ID)
					continue
				}
				if len(it.Prompt.Moves) == 0 || it.Prompt.Loop == "" {
					t.Errorf("%s: prompt facts are incomplete: %+v", it.ID, *it.Prompt)
				}
				for _, ask := range it.Prompt.AskFirst {
					if !containsString(it.Prompt.Moves, ask) {
						t.Errorf("%s: askFirst %q is not one of the offered moves %v", it.ID, ask, it.Prompt.Moves)
					}
				}
			}
		}
	}
	if dated != rows {
		t.Fatalf("only %d of %d exchange rows carry a sort key", dated, rows)
	}
	if ours == 0 {
		t.Fatal("no exchange row in the demo is ours to move on; the prompt button would never appear")
	}
}

// TestDemoDerivedStateIsDerivedNotAuthored is the same guard as the two above,
// one release further on, and it is the one that caught a WRONG claim rather
// than a missing one.
//
// 0.19.10 put Outcome, Terminal and the three State* fields on every read
// surface. Terminal carries no `omitempty`, so a fixture that predates it
// serialises `terminal:false` on all 31 carriers — including a `closed`
// question and a `cancelled` work_request, both terminal by the domain's own
// answer. `a2a html --demo` and the site's dashboard-example page both render
// this fixture, so the release whose headline is "the domain says what a state
// means, and every surface reads the same answer" shipped a showcase saying
// the opposite.
//
// Two halves, asserted separately because they fail differently: the fixture
// must AUTHOR none of the five, and the derivation must agree with fold for
// every carrier.
func TestDemoDerivedStateIsDerivedNotAuthored(t *testing.T) {
	t.Parallel()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(demoJSON, &raw); err != nil {
		t.Fatalf("demo fixture: %v", err)
	}
	for _, authored := range []string{`"outcome"`, `"terminal"`, `"stateSince"`, `"stateBy"`, `"stateEvent"`} {
		if bytes.Contains(demoJSON, []byte(authored)) {
			t.Errorf("testdata/demo.json authors %s — these five are derived by deriveDemoDerivedState "+
				"from (type, state) and the fixture's own events. An authored copy is a second answer "+
				"that can disagree with the domain, which is exactly the defect 0.19.10 removed.", authored)
		}
	}

	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	carriers, terminal, outcomes := 0, 0, map[fold.Outcome]int{}
	check := func(what, typ, state string, gotOutcome fold.Outcome, gotTerminal bool) {
		carriers++
		wantOutcome := fold.OutcomeOf(fold.Kind(typ), fold.State(state))
		wantTerminal := fold.Terminal(fold.Kind(typ), fold.State(state))
		if gotOutcome != wantOutcome {
			t.Errorf("%s: %s/%s outcome=%q, domain says %q", what, typ, state, gotOutcome, wantOutcome)
		}
		if gotTerminal != wantTerminal {
			t.Errorf("%s: %s/%s terminal=%v, domain says %v", what, typ, state, gotTerminal, wantTerminal)
		}
		if gotTerminal {
			terminal++
		}
		outcomes[gotOutcome]++
	}
	for _, list := range [][]Item{d.Inbox, d.Outbox, d.Archive} {
		for _, it := range list {
			check("exchange row "+it.ID, it.Type, it.State, it.Outcome, it.Terminal)
		}
	}
	for _, tv := range d.ThreadViews {
		for _, oi := range tv.OpenItems {
			check("open item "+oi.ID, oi.Type, oi.State, oi.Outcome, oi.Terminal)
		}
	}
	for _, ad := range d.ArtifactDetails {
		check("artifact detail "+ad.ID, ad.Type, ad.State, ad.Outcome, ad.Terminal)
	}

	if carriers == 0 {
		t.Fatal("no carrier of the five fields in the demo — this test would pass vacuously")
	}
	// The fixture's job is to render every branch the dashboard has. A demo
	// where nothing is terminal, or where every artifact has the same outcome,
	// leaves the component's own `outcome === "settled"` / `"refused"`
	// branches unexercised while still looking rich on screen.
	if terminal == 0 {
		t.Errorf("%d carriers and not one is terminal — the dashboard's terminal branch renders nowhere in the demo", carriers)
	}
	if len(outcomes) < 2 {
		t.Errorf("every carrier has the same outcome (%v) — the demo cannot show what the field is FOR", outcomes)
	}
}

// TestDemoStateOriginsAgreeAcrossBothEventSources holds the assumption
// demoStateOrigins is built on: the fixture keeps events in two places — a
// thread's transcript rows and an artifact detail's own list — and an artifact
// present in both must be described identically by both. Without this the
// index would silently prefer whichever source sorted last, and the demo would
// name an event that contradicts the panel next to it.
func TestDemoStateOriginsAgreeAcrossBothEventSources(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	type seen struct{ at, by, claimed string }
	fromTranscript := map[string]seen{}
	for _, tv := range d.ThreadViews {
		for _, row := range tv.Transcript {
			if row.Kind != "event" || row.Event == nil {
				continue
			}
			fromTranscript[tv.Space+"/"+row.Event.ULID] = seen{
				at: row.At, by: demoActorSystem(row.Event.Actor), claimed: row.Event.ClaimedState,
			}
		}
	}
	overlap := 0
	for _, ad := range d.ArtifactDetails {
		for _, e := range ad.Events {
			other, ok := fromTranscript[ad.Space+"/"+e.ULID]
			if !ok {
				continue
			}
			overlap++
			if other.at != e.At || other.by != e.ActorSystem || other.claimed != e.ClaimedState {
				t.Errorf("event %s is described twice and differently: transcript %+v, detail {at:%s by:%s claimed:%s}",
					e.ULID, other, e.At, e.ActorSystem, e.ClaimedState)
			}
		}
	}
	if overlap == 0 {
		t.Log("no event appears in both sources; this test is currently vacuous but guards the merge as the fixture grows")
	}
}

// TestDemoStateOriginNamesAnEventThatProducedThatState checks the derivation
// against the fixture rather than against itself: whatever event
// demoStateOrigins credits with a row's current state must be an event the
// fixture actually states, and must claim exactly that state. A row whose
// origin claims a DIFFERENT state means the fixture disagrees with itself, and
// the dashboard would print a confident "since / by" under the wrong state.
func TestDemoStateOriginNamesAnEventThatProducedThatState(t *testing.T) {
	t.Parallel()
	d, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}

	claimed := map[string]string{} // space/ULID -> claimed_state
	for _, tv := range d.ThreadViews {
		for _, row := range tv.Transcript {
			if row.Kind == "event" && row.Event != nil {
				claimed[tv.Space+"/"+row.Event.ULID] = row.Event.ClaimedState
			}
		}
	}
	for _, ad := range d.ArtifactDetails {
		for _, e := range ad.Events {
			claimed[ad.Space+"/"+e.ULID] = e.ClaimedState
		}
	}

	withOrigin := 0
	for _, list := range [][]Item{d.Inbox, d.Outbox, d.Archive} {
		for _, it := range list {
			if it.StateEvent == "" {
				// Honest absence: the fixture states no state-moving event for
				// this artifact, the same answer internal/cache/mirror.go gives
				// for an artifact with no events. StateSince and StateBy must be
				// absent with it, never one without the others.
				if !it.StateSince.IsZero() || it.StateBy != "" {
					t.Errorf("%s: no StateEvent but StateSince=%v StateBy=%q — a partial origin is not a state the product can produce",
						it.ID, it.StateSince, it.StateBy)
				}
				continue
			}
			withOrigin++
			got, ok := claimed[it.Space+"/"+it.StateEvent]
			if !ok {
				t.Errorf("%s: StateEvent %q names an event the fixture does not state", it.ID, it.StateEvent)
				continue
			}
			if got != it.State {
				t.Errorf("%s is %q but its StateEvent %q claims %q", it.ID, it.State, it.StateEvent, got)
			}
			if it.StateBy == "" {
				t.Errorf("%s: StateEvent %q names no system — StateBy is the participant that moved it", it.ID, it.StateEvent)
			}
		}
	}
	if withOrigin == 0 {
		t.Fatal("no exchange row in the demo carries a state origin — the since/by panel renders empty everywhere")
	}
}
