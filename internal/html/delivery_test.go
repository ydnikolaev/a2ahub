package html

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// loadDeliveryFixture decodes testdata/delivery_fixture.json — the exact
// snake_case wire shape internal/cache.Delivery marshals to, so this test
// exercises the boundary a later wave will actually cross (cache.Delivery
// in, html.Delivery out) rather than a shape invented for this file alone.
func loadDeliveryFixture(t *testing.T) []cache.Delivery {
	t.Helper()
	raw, err := os.ReadFile("testdata/delivery_fixture.json")
	if err != nil {
		t.Fatalf("read testdata/delivery_fixture.json: %v", err)
	}
	var deliveries []cache.Delivery
	if err := json.Unmarshal(raw, &deliveries); err != nil {
		t.Fatalf("decode testdata/delivery_fixture.json into []cache.Delivery: %v", err)
	}
	return deliveries
}

// TestProjectDeliveries_FixtureRendersAC7 is spec 05a AC-7's own render
// test: "render the dashboard from a fixture space with one failed and one
// accepted delivery." The fixture carries two attempts of the SAME
// logical delivery (attempt 1 failed, attempt 2 — which supersedes it —
// passed), plus an unverified delivery and an unresolved one, so every
// state AC-7 and the top-level brief name is asserted from one render.
func TestProjectDeliveries_FixtureRendersAC7(t *testing.T) {
	t.Parallel()
	got := ProjectDeliveries(loadDeliveryFixture(t))

	if len(got) != 4 {
		t.Fatalf("want all 4 deliveries visible under their handoffs, got %d: %+v", len(got), got)
	}

	failed, passed, unverified, unresolved := got[0], got[1], got[2], got[3]

	// --- the failed delivery names its entry and rule ---
	if failed.Resolution != DeliveryResolved {
		t.Fatalf("failed delivery: want resolution=resolved, got %q", failed.Resolution)
	}
	if failed.Verdict != DeliveryVerdictFailed {
		t.Fatalf("failed delivery: want verdict=failed, got %q", failed.Verdict)
	}
	if len(failed.Failures) != 1 {
		t.Fatalf("failed delivery: want exactly 1 named failure, got %+v", failed.Failures)
	}
	f := failed.Failures[0]
	if f.EntryPath != "dataset/records.ndjson" || f.Rule != "chk-schema" {
		t.Fatalf("failed delivery: want the failing entry+rule named, got %+v", f)
	}
	if f.Record == nil || *f.Record != 4108 {
		t.Fatalf("failed delivery: want the ndjson record number named, got %v", f.Record)
	}
	if failed.PackageID != "DP-beta-20260101-aaaa" || failed.Attempt != 1 {
		t.Fatalf("failed delivery: want package id + attempt 1, got %+v", failed)
	}

	// --- the accepted delivery carries the supersede chain back to attempt 1 ---
	if passed.Verdict != DeliveryVerdictPassed {
		t.Fatalf("accepted delivery: want verdict=passed, got %q", passed.Verdict)
	}
	if passed.PackageID != "DP-beta-20260102-bbbb" || passed.Attempt != 2 {
		t.Fatalf("accepted delivery: want package id + attempt 2, got %+v", passed)
	}
	if passed.Supersedes != "DP-beta-20260101-aaaa" {
		t.Fatalf("accepted delivery: want Supersedes naming attempt 1, got %q", passed.Supersedes)
	}
	if len(passed.Chain) != 2 {
		t.Fatalf("accepted delivery: want a 2-entry supersede chain back to the first attempt, got %+v", passed.Chain)
	}
	if passed.Chain[0].PackageID != failed.PackageID || passed.Chain[0].Verdict != DeliveryVerdictFailed {
		t.Fatalf("accepted delivery: want chain[0] to be the first (failed) attempt, got %+v", passed.Chain[0])
	}
	if passed.Chain[1].PackageID != passed.PackageID || passed.Chain[1].Verdict != DeliveryVerdictPassed {
		t.Fatalf("accepted delivery: want chain[1] to be this (passed) attempt, got %+v", passed.Chain[1])
	}

	// --- "not yet verified" must be visibly distinct from "passed" ---
	if unverified.Verdict != DeliveryVerdictUnverified {
		t.Fatalf("unverified delivery: want verdict=unverified, got %q", unverified.Verdict)
	}
	if unverified.Verdict == DeliveryVerdictPassed {
		t.Fatal("unverified delivery must never equal DeliveryVerdictPassed")
	}
	if unverified.Verdict == "" {
		t.Fatal("unverified delivery must not render as the empty string — absent is unknown, never good")
	}

	// --- a delivery whose manifest cannot be resolved renders as unresolved, never vanishes ---
	if unresolved.Resolution != DeliveryUnresolved {
		t.Fatalf("unresolved delivery: want resolution=unresolved, got %q", unresolved.Resolution)
	}
	if unresolved.Unavailable == "" {
		t.Fatal("unresolved delivery must carry a non-empty reason")
	}
	if unresolved.HandoffID == "" || unresolved.Ref == "" {
		t.Fatalf("unresolved delivery must still carry its own identity, got %+v", unresolved)
	}
	if unresolved.Chain == nil {
		t.Fatal("unresolved delivery's Chain must be a non-nil empty slice, never omitted")
	}
	if unresolved.Verdict != "" {
		t.Fatalf("unresolved delivery has no verdict to report, got %q", unresolved.Verdict)
	}
}

// TestProjectVerdict_EveryCacheStateHasAnExplicitBranch guards
// projectVerdict directly: every cache.DeliveryVerdictStatus this project
// defines must map to its OWN non-empty, non-"passed" spelling — the
// single boundary that would let "not yet verified" round down to
// "passed" if it silently fell through to a shared default.
func TestProjectVerdict_EveryCacheStateHasAnExplicitBranch(t *testing.T) {
	t.Parallel()
	cases := map[cache.DeliveryVerdictStatus]DeliveryVerdict{
		cache.DeliveryVerdictPass:       DeliveryVerdictPassed,
		cache.DeliveryVerdictFail:       DeliveryVerdictFailed,
		cache.DeliveryVerdictError:      DeliveryVerdictErrored,
		cache.DeliveryVerdictUnverified: DeliveryVerdictUnverified,
	}
	for in, want := range cases {
		if got := projectVerdict(in); got != want {
			t.Errorf("projectVerdict(%q) = %q, want %q", in, got, want)
		}
	}
	if projectVerdict(cache.DeliveryVerdictUnverified) == DeliveryVerdictPassed {
		t.Fatal("unverified must never project to passed")
	}
}

// TestProjectDeliveries_RenderedIntoPage closes this wave's own gap: nothing
// wired ProjectDeliveries' output into the page before it (internal/html has
// no deliveries field, template.html never mentions one), so the dashboard
// showed nothing about a delivery even though the projection was fully
// tested in isolation. This test asserts on the ACTUAL SELF-CONTAINED HTML
// PAGE Render() emits — the wire format the shipped dashboard writes to
// disk, not the ThreadView/Delivery Go structs — carries every AC-7 fact:
// the package id (both attempts), the failing entry path + rule, and the
// supersede chain back to the first attempt.
func TestProjectDeliveries_RenderedIntoPage(t *testing.T) {
	t.Parallel()
	deliveries := ProjectDeliveries(loadDeliveryFixture(t))

	data := Data{
		GeneratedAt: time.Now(),
		Self:        "beta",
		Spaces:      []SpaceHealth{},
		Flags:       []Flag{},
		ThreadViews: []ThreadView{
			{
				Thread:     "thread:XH-beta-20260101-aaaa",
				Space:      "s-demo",
				Deliveries: deliveries,
			},
		},
	}

	out, err := Render(DefaultTemplate(), data, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	page := string(out)

	// The failed attempt's package id, its failing entry path and rule, and
	// the ndjson record number the top-level brief names explicitly.
	for _, want := range []string{
		`"packageId":"DP-beta-20260101-aaaa"`,
		`"entryPath":"dataset/records.ndjson"`,
		`"rule":"chk-schema"`,
		`"record":4108`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %s (the failed attempt's own facts)", want)
		}
	}

	// The accepted (superseding) attempt's own package id, plus its
	// supersede chain naming BOTH the failed first attempt and itself, so a
	// reader sees what was tried, not only the outcome.
	for _, want := range []string{
		`"packageId":"DP-beta-20260102-bbbb"`,
		`"supersedes":"DP-beta-20260101-aaaa"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %s (the accepted attempt's own facts)", want)
		}
	}
	// The chain array itself: both package ids must appear, in the same
	// rendered page, as chain entries (not merely as the top-level
	// packageId/supersedes fields already checked above).
	if n := strings.Count(page, "DP-beta-20260101-aaaa"); n < 2 {
		t.Errorf("rendered page names the first attempt's package id %d times, want at least 2 (its own delivery + the chain entry under the accepted attempt)", n)
	}

	// Every verdict state renders under its OWN name, and "unverified" is
	// never mistakable for "passed" — the top-level brief's own guard
	// against the defect this project has already shipped once.
	for _, verdict := range []string{`"verdict":"failed"`, `"verdict":"passed"`, `"verdict":"unverified"`} {
		if !strings.Contains(page, verdict) {
			t.Errorf("rendered page missing %s", verdict)
		}
	}
	if strings.Contains(page, `"verdict":"unverified: passed"`) {
		t.Fatal("unverified must never render spelled as passed")
	}

	// The unresolved delivery still appears — never vanished — and carries
	// its own reason.
	if !strings.Contains(page, `"resolution":"unresolved"`) {
		t.Error("rendered page missing the unresolved delivery's resolution")
	}
	if !strings.Contains(page, "could not be resolved") {
		t.Error("rendered page missing the unresolved delivery's own reason")
	}
}

// TestDefaultTemplate_ConsumesThreadViewDeliveries guards the OTHER half of
// this wave's gap: TestProjectDeliveries_RenderedIntoPage above proves
// Render() emits ThreadView.Deliveries into the page's embedded DATA JSON,
// but the shipped template.html also has to actually CONSUME that field —
// join it against its handoff by HandoffID, and give every verdict its own
// rendering branch — or a reader opening the real dashboard still sees
// nothing. This asserts against the committed, generated template.html
// itself (DefaultTemplate(), the exact bytes `a2a html` ships), so a rebuild
// that silently drops the delivery markup fails here, not only in the
// dashboard-template-drift gate (which only proves template.html matches
// its own design source, not that either one renders a delivery at all).
func TestDefaultTemplate_ConsumesThreadViewDeliveries(t *testing.T) {
	t.Parallel()
	tmpl := dashboardDesignSource(t, "ThreadsView.dc.html")
	shipped := dashboardTemplateCorpus(t)

	// handoffId is Delivery's own join key (spec 05a AC-7: rendered "under
	// the handoff that carries it") — its presence in the shipped bundle is
	// the one fact that proves the template reads per-delivery data at all,
	// survives minification (it is read via dot/bracket property access,
	// which a minifier must not rename).
	if !strings.Contains(tmpl, "handoffId") {
		t.Error("template.html never reads Delivery.HandoffID — deliveries cannot be joined under their handoff")
	}
	if !strings.Contains(tmpl, "hasDeliveries") {
		t.Error("template.html never gates on hasDeliveries — a thread with no deliveries would render an empty section instead of none at all")
	}
	// Verdict is carried source data. The browser may present it, but must not
	// recreate a local verdict-label/status table. The remaining literals and
	// property reads distinguish the delivery renderer from unrelated protocol
	// history: its heading, join, failure facts, attempt chain and unresolved
	// branch must all remain reachable in the shipped bundle.
	for _, want := range []string{
		"Data deliveries", "Every attempt",
		"Package not found in the local mirror", "recordLabel",
		"del.verdict", "del.failures", "del.chain",
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template.html missing %q — the delivery markup is not in the shipped bundle", want)
		}
		if !strings.Contains(shipped, want) {
			t.Errorf("shipped dashboard missing delivery contract %q", want)
		}
	}
	for _, forbidden := range []string{"deliveryVerdictText", `const DELIVERY_VERDICTS`, `const DELIVERY_STATUS`} {
		if strings.Contains(tmpl, forbidden) {
			t.Errorf("template.html still owns stale delivery verdict vocabulary %q", forbidden)
		}
	}
}

// deliveryFixtureManifestPath/deliveryFixtureReportPath reproduce the EXACT
// space-relative path shape internal/space/data_delivery.go's own unexported
// dataPackageDir/dataPackageManifestPath/dataPackageReportPath writes to
// (<system>/data/<packageID>/manifest.json|report.json — verified by
// reading that file, not guessed) and internal/cache/packageresolver.go's
// own duplicate of that same shape reads from. Writing anywhere else here
// would prove nothing about the real wiring this test exists to exercise.
func deliveryFixtureManifestPath(system, packageID string) string {
	return filepath.Join(system, "data", packageID, "manifest.json")
}

func deliveryFixtureReportPath(system, packageID string) string {
	return filepath.Join(system, "data", packageID, "report.json")
}

func writeDeliveryFixtureManifest(t *testing.T, dir, system, packageID string, extra map[string]any) {
	t.Helper()
	fields := map[string]any{"schema": "data-package/v1", "id": packageID, "attempt": 1}
	for k, v := range extra {
		fields[k] = v
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("writeDeliveryFixtureManifest: marshal: %v", err)
	}
	full := filepath.Join(dir, deliveryFixtureManifestPath(system, packageID))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeDeliveryFixtureManifest: mkdir: %v", err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatalf("writeDeliveryFixtureManifest: write: %v", err)
	}
}

func writeDeliveryFixtureReport(t *testing.T, dir, verifyingSystem, packageID string, extra map[string]any) {
	t.Helper()
	fields := map[string]any{
		"schema": "verification-report/v1", "id": "VR-" + verifyingSystem + "-20260101-aaaa",
		"package": map[string]any{"id": packageID, "aggregate_digest": "sha256:deadbeef"},
		"result":  "fail",
		"checks": []map[string]any{
			{"id": "chk-schema", "path": "dataset/records.ndjson", "status": "fail", "violations": []map[string]any{
				{"instance_pointer": "/field", "schema_pointer": "/properties/field", "message": "wrong type", "record": 4108},
			}},
		},
	}
	for k, v := range extra {
		fields[k] = v
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("writeDeliveryFixtureReport: marshal: %v", err)
	}
	full := filepath.Join(dir, deliveryFixtureReportPath(verifyingSystem, packageID))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeDeliveryFixtureReport: mkdir: %v", err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatalf("writeDeliveryFixtureReport: write: %v", err)
	}
}

// TestAssemble_DeliveriesReachThePageFromAFixtureSpace is the test this
// wave's brief demands and names explicitly: "a test that starts from a
// fixture space on disk containing a thread with a handoff carrying a data
// deliverable, a package manifest and a verification report, runs the real
// assembly path end to end, and asserts the rendered output carries the
// package, its attempt, its verdict and the failing entry." Every prior
// test in this file (TestProjectDeliveries_RenderedIntoPage included) hand-
// populates ThreadView.Deliveries via loadDeliveryFixture — this one does
// not touch that field, or any cache.Delivery/html.Delivery value, at all.
// It writes only the same three things a real space ever carries: an
// artifact *.md file (writeArtifact, this package's own existing helper,
// assemble_integration_test.go), a manifest.json and a report.json, at the
// exact paths the real writer (internal/space/data_delivery.go) and the
// real reader (internal/cache/packageresolver.go) already agree on. If the
// resolver wiring (cache.Store.ThreadView -> internal/cache's
// buildDeliveries -> internal/html's toThreadView -> ProjectDeliveries) is
// broken anywhere along that path, this test — not a hand-populated one —
// is what catches it.
//
// It also covers the brief's own "unverified" requirement: a second
// deliverable on the SAME handoff whose package resolves but whose
// report.json was never written, asserted distinguishable in the rendered
// bytes from the first (failed) deliverable's own verdict.
func TestAssemble_DeliveriesReachThePageFromAFixtureSpace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const threadID = "thread:axon-20260101-deliver1"
	const handoffID = "XH-axon-20260101-aaaa"
	const failingPackageID = "DP-axon-20260101-aaaa"
	const unverifiedPackageID = "DP-axon-20260101-bbbb"

	writeArtifact(t, dir, "axon/exchanges/"+handoffID+".md", map[string]any{
		"schema": "envelope/v1", "id": handoffID, "type": "handoff", "title": "data delivery",
		"space": "getvisa", "from": "axon", "to": []string{"seomatrix"}, "thread": threadID,
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": "2026-01-01T10:00:00Z", "priority": "p2", "blocking": false, "classification": "internal",
		"deliverables": []map[string]any{
			{"name": "failing dataset", "ref": failingPackageID, "kind": "data"},
			{"name": "unverified dataset", "ref": unverifiedPackageID, "kind": "data"},
		},
		"verification": "manual", "acceptance_criteria": []string{"done"}, "limitations": []string{},
	}, "handoff body")

	// The failing delivery: manifest under its own producer (axon, per its
	// own DP- id), report under the VERIFYING system (seomatrix) — the
	// asymmetry packageresolver.go's own doc comment records at length.
	writeDeliveryFixtureManifest(t, dir, "axon", failingPackageID, nil)
	writeDeliveryFixtureReport(t, dir, "seomatrix", failingPackageID, nil)

	// The unverified delivery: manifest present, no report.json anywhere.
	writeDeliveryFixtureManifest(t, dir, "axon", unverifiedPackageID, map[string]any{"attempt": 2})

	manifest := space.Manifest{Space: "getvisa", Participants: []space.Participant{
		{System: "axon", Section: "axon/", Org: "r22d222", Status: fold.MembershipMember, Owners: []string{"ydnikolaev"}},
		{System: "seomatrix", Section: "seomatrix/", Org: "r22d222", Status: fold.MembershipMember, Owners: []string{"xpressmike"}},
	}}
	store := cache.NewStore("axon", t.TempDir(),
		[]cache.SpaceMirror{{SpaceID: "getvisa", Dir: dir, RepoURL: "https://github.com/r22d222/a2a", Manifest: manifest}},
		func() time.Time { return now }, 0)

	data, err := Assemble(context.Background(), store, "", now)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	var thread *ThreadView
	for i := range data.ThreadViews {
		if data.ThreadViews[i].Thread == threadID {
			thread = &data.ThreadViews[i]
		}
	}
	if thread == nil {
		t.Fatalf("thread %s not assembled at all: %+v", threadID, data.ThreadViews)
	}
	if len(thread.Deliveries) != 2 {
		t.Fatalf("ThreadView.Deliveries = %+v, want exactly 2 (resolved by the real Store.ThreadView path, not hand-populated)", thread.Deliveries)
	}

	out, err := Render(DefaultTemplate(), data, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	page := string(out)

	for _, want := range []string{
		`"packageId":"` + failingPackageID + `"`,
		`"attempt":1`,
		`"verdict":"failed"`,
		`"entryPath":"dataset/records.ndjson"`,
		`"rule":"chk-schema"`,
		`"record":4108`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %s (the failing delivery's own resolved facts, via the real assembly path)", want)
		}
	}

	for _, want := range []string{
		`"packageId":"` + unverifiedPackageID + `"`,
		`"attempt":2`,
		`"verdict":"unverified"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %s (the unverified delivery's own resolved facts)", want)
		}
	}
	// "unverified" must be a byte-distinct value from "passed" in the
	// rendered output — the exact defect this project has already shipped
	// once (a missing report rounded down to a settled state).
	if strings.Contains(page, `"verdict":"passed"`) {
		t.Fatal("a delivery with no report.json rendered as passed — absent must never round down to good")
	}
}
