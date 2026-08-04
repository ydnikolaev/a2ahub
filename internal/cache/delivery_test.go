package cache

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// fakeResolver is a hand-rolled PackageResolver test double — no
// filesystem, no space mirror, per this file's own doc comment on why
// PackageResolver is an injected seam rather than a directory walk.
type fakeResolver struct {
	docs    map[string]datapackage.Document
	reports map[string]datapackage.Report
}

func (r fakeResolver) ResolvePackage(ref string) (datapackage.Document, bool) {
	doc, ok := r.docs[ref]
	return doc, ok
}

func (r fakeResolver) ResolveReport(packageID string) (datapackage.Report, bool) {
	rep, ok := r.reports[packageID]
	return rep, ok
}

const handoffRaw = `---
schema: envelope/v1
id: XH-alpha-20260101-aaaa
type: handoff
thread: thread:t1
from: alpha
to: [beta]
title: Delivery
created: 2026-01-01T00:00:00Z
deliverables:
  - name: sample dataset
    ref: DP-alpha-20260101-aaaa
    kind: data
  - name: code artifact
    ref: some/code/path
    kind: code
verification: manual
acceptance_criteria: ["done"]
limitations: []
fulfills: ["XW-alpha-20251231-zzzz"]
---
Body.
`

func mustCheck(id, path string, status datapackage.CheckStatus, violations []datapackage.InstanceViolation) datapackage.Check {
	return datapackage.NewCheck(id, path, status, violations)
}

func TestDecodeDeliverables_FiltersByFrontmatter(t *testing.T) {
	t.Parallel()
	got, err := DecodeDeliverables([]byte(handoffRaw))
	if err != nil {
		t.Fatalf("DecodeDeliverables: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 deliverables, got %d: %+v", len(got), got)
	}
	if got[0].Ref != "DP-alpha-20260101-aaaa" || got[0].Kind != "data" {
		t.Fatalf("unexpected first deliverable: %+v", got[0])
	}
	if got[1].Kind != "code" {
		t.Fatalf("unexpected second deliverable: %+v", got[1])
	}
}

func TestDecodeDeliverables_NotFrontmatterShaped(t *testing.T) {
	t.Parallel()
	if _, err := DecodeDeliverables([]byte("no frontmatter here")); err == nil {
		t.Fatal("want an error for non-frontmatter-shaped input")
	}
}

func TestResolveDeliveries_OnlyDataKind(t *testing.T) {
	t.Parallel()
	resolver := fakeResolver{
		docs: map[string]datapackage.Document{
			"DP-alpha-20260101-aaaa": {ID: "DP-alpha-20260101-aaaa", Attempt: 1},
		},
		reports: map[string]datapackage.Report{},
	}
	deliveries, err := ResolveDeliveries("XH-alpha-20260101-aaaa", []byte(handoffRaw), resolver)
	if err != nil {
		t.Fatalf("ResolveDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("want exactly 1 data-kind delivery (the code deliverable must be excluded), got %d", len(deliveries))
	}
	if deliveries[0].Ref != "DP-alpha-20260101-aaaa" {
		t.Fatalf("unexpected delivery: %+v", deliveries[0])
	}
}

func TestResolveDelivery_Unavailable_NeverVanishes(t *testing.T) {
	t.Parallel()
	resolver := fakeResolver{docs: map[string]datapackage.Document{}, reports: map[string]datapackage.Report{}}
	d := Deliverable{Name: "missing", Ref: "DP-ghost-20260101-zzzz", Kind: DeliverableKindData}

	got := ResolveDelivery("XH-1", d, resolver)

	if got.Status != DeliveryUnavailable {
		t.Fatalf("want Status=unavailable, got %q", got.Status)
	}
	if got.UnavailableReason == "" {
		t.Fatal("want a non-empty UnavailableReason naming the ref that could not be resolved")
	}
	if got.HandoffID != "XH-1" || got.Name != "missing" || got.Ref != "DP-ghost-20260101-zzzz" {
		t.Fatalf("unavailable delivery must still carry its own identity, got %+v", got)
	}
	if got.Chain == nil {
		t.Fatal("Chain must be a non-nil empty slice, never omitted, even when unavailable")
	}
}

func TestResolveDelivery_NoReport_IsUnverified_NotPass(t *testing.T) {
	t.Parallel()
	doc := datapackage.Document{ID: "DP-a-20260101-aaaa", Attempt: 1}
	resolver := fakeResolver{
		docs:    map[string]datapackage.Document{"DP-a-20260101-aaaa": doc},
		reports: map[string]datapackage.Report{},
	}
	d := Deliverable{Name: "x", Ref: "DP-a-20260101-aaaa", Kind: DeliverableKindData}

	got := ResolveDelivery("XH-1", d, resolver)

	if got.Status != DeliveryAvailable {
		t.Fatalf("want Status=available (manifest resolved), got %q", got.Status)
	}
	if got.Verdict != DeliveryVerdictUnverified {
		t.Fatalf("want Verdict=unverified when no report exists, got %q", got.Verdict)
	}
	if got.Verdict == DeliveryVerdictPass {
		t.Fatal("unverified must never equal pass")
	}
	if len(got.Failures) != 0 {
		t.Fatalf("an unverified delivery names no failures, got %+v", got.Failures)
	}
}

func TestResolveDelivery_Pass(t *testing.T) {
	t.Parallel()
	doc := datapackage.Document{ID: "DP-a-20260101-aaaa", Attempt: 1}
	report, err := datapackage.NewReport(
		"VR-a-20260101-aaaa", "thread:t1",
		datapackage.PackageRef{ID: doc.ID, AggregateDigest: "sha256:deadbeef"},
		"XC-a-1@1.0.0#sha256:beef", "beta",
		[]datapackage.Check{mustCheck("chk-schema", "dataset/records.json", datapackage.CheckPass, nil)},
		datapackage.Observed{RecordCount: 10, SizeBytes: 100, DurationMS: 5},
		"2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z",
	)
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	resolver := fakeResolver{
		docs:    map[string]datapackage.Document{doc.ID: doc},
		reports: map[string]datapackage.Report{doc.ID: report},
	}
	got := ResolveDelivery("XH-1", Deliverable{Name: "x", Ref: doc.ID, Kind: DeliverableKindData}, resolver)

	if got.Verdict != DeliveryVerdictPass {
		t.Fatalf("want Verdict=pass, got %q", got.Verdict)
	}
	if len(got.Failures) != 0 {
		t.Fatalf("a passing delivery names no failures, got %+v", got.Failures)
	}
	if got.ReportID != report.ID {
		t.Fatalf("want ReportID=%q, got %q", report.ID, got.ReportID)
	}
}

func TestResolveDelivery_Fail_NamesEntryRuleAndRecord(t *testing.T) {
	t.Parallel()
	doc := datapackage.Document{ID: "DP-a-20260101-aaaa", Attempt: 1}
	record := int64(42)
	report, err := datapackage.NewReport(
		"VR-a-20260101-aaaa", "thread:t1",
		datapackage.PackageRef{ID: doc.ID, AggregateDigest: "sha256:deadbeef"},
		"XC-a-1@1.0.0#sha256:beef", "beta",
		[]datapackage.Check{
			mustCheck("chk-schema", "dataset/records.ndjson", datapackage.CheckFail, []datapackage.InstanceViolation{
				{InstancePointer: "/field", SchemaPointer: "/properties/field", Message: "wrong type", Record: &record},
			}),
			mustCheck("chk-index", "index.json", datapackage.CheckPass, nil),
		},
		datapackage.Observed{RecordCount: 10, SizeBytes: 100, DurationMS: 5},
		"2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z",
	)
	if err != nil {
		t.Fatalf("NewReport: %v", err)
	}
	resolver := fakeResolver{
		docs:    map[string]datapackage.Document{doc.ID: doc},
		reports: map[string]datapackage.Report{doc.ID: report},
	}
	got := ResolveDelivery("XH-1", Deliverable{Name: "x", Ref: doc.ID, Kind: DeliverableKindData}, resolver)

	if got.Verdict != DeliveryVerdictFail {
		t.Fatalf("want Verdict=fail, got %q", got.Verdict)
	}
	if len(got.Failures) != 1 {
		t.Fatalf("want exactly 1 failure, got %+v", got.Failures)
	}
	f := got.Failures[0]
	if f.EntryPath != "dataset/records.ndjson" || f.Rule != "chk-schema" {
		t.Fatalf("unexpected failure identity: %+v", f)
	}
	if f.Record == nil || *f.Record != 42 {
		t.Fatalf("want Record=42 for the ndjson violation, got %v", f.Record)
	}
}

func TestResolveDelivery_SupersedeChain_OldestFirst(t *testing.T) {
	t.Parallel()
	attempt1 := datapackage.Document{ID: "DP-a-20260101-aaaa", Attempt: 1}
	attempt2 := datapackage.Document{ID: "DP-a-20260102-bbbb", Attempt: 2, Supersedes: attempt1.ID}

	failReport, err := datapackage.NewReport(
		"VR-a-20260101-aaaa", "thread:t1",
		datapackage.PackageRef{ID: attempt1.ID, AggregateDigest: "sha256:one"},
		"XC-a-1@1.0.0#sha256:beef", "beta",
		[]datapackage.Check{mustCheck("chk-schema", "dataset/records.json", datapackage.CheckFail, nil)},
		datapackage.Observed{}, "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z",
	)
	if err != nil {
		t.Fatalf("NewReport(attempt1): %v", err)
	}
	passReport, err := datapackage.NewReport(
		"VR-a-20260102-bbbb", "thread:t1",
		datapackage.PackageRef{ID: attempt2.ID, AggregateDigest: "sha256:two"},
		"XC-a-1@1.0.0#sha256:beef", "beta",
		[]datapackage.Check{mustCheck("chk-schema", "dataset/records.json", datapackage.CheckPass, nil)},
		datapackage.Observed{}, "2026-01-02T00:00:00Z", "2026-01-02T00:00:01Z",
	)
	if err != nil {
		t.Fatalf("NewReport(attempt2): %v", err)
	}

	resolver := fakeResolver{
		docs: map[string]datapackage.Document{attempt1.ID: attempt1, attempt2.ID: attempt2},
		reports: map[string]datapackage.Report{
			attempt1.ID: failReport,
			attempt2.ID: passReport,
		},
	}

	got := ResolveDelivery("XH-2", Deliverable{Name: "x", Ref: attempt2.ID, Kind: DeliverableKindData}, resolver)

	if got.Verdict != DeliveryVerdictPass {
		t.Fatalf("want the current attempt's verdict=pass, got %q", got.Verdict)
	}
	if len(got.Chain) != 2 {
		t.Fatalf("want a 2-entry chain (attempt1, attempt2), got %+v", got.Chain)
	}
	if got.Chain[0].PackageID != attempt1.ID || got.Chain[0].Attempt != 1 || got.Chain[0].Verdict != DeliveryVerdictFail {
		t.Fatalf("want chain[0]=attempt1/fail (oldest first), got %+v", got.Chain[0])
	}
	if got.Chain[1].PackageID != attempt2.ID || got.Chain[1].Attempt != 2 || got.Chain[1].Verdict != DeliveryVerdictPass {
		t.Fatalf("want chain[1]=attempt2/pass, got %+v", got.Chain[1])
	}
}

func TestResolveDelivery_ChainDanglingSupersedesIsUnavailable(t *testing.T) {
	t.Parallel()
	doc := datapackage.Document{ID: "DP-a-20260102-bbbb", Attempt: 2, Supersedes: "DP-a-20260101-aaaa"}
	resolver := fakeResolver{
		docs:    map[string]datapackage.Document{doc.ID: doc}, // the superseded attempt is NOT present
		reports: map[string]datapackage.Report{},
	}

	got := ResolveDelivery("XH-2", Deliverable{Name: "x", Ref: doc.ID, Kind: DeliverableKindData}, resolver)

	if len(got.Chain) != 2 {
		t.Fatalf("want a 2-entry chain (the current attempt plus the dangling link), got %+v", got.Chain)
	}
	// oldest-first after the reverse: the dangling supersedes target sorts first.
	if got.Chain[0].PackageID != "DP-a-20260101-aaaa" || got.Chain[0].Status != DeliveryUnavailable {
		t.Fatalf("want chain[0] to name the unresolved prior attempt, got %+v", got.Chain[0])
	}
	if got.Chain[1].PackageID != doc.ID || got.Chain[1].Status != DeliveryAvailable {
		t.Fatalf("want chain[1] to be the resolved current attempt, got %+v", got.Chain[1])
	}
}

func TestResolveDelivery_ChainCycleDoesNotHang(t *testing.T) {
	t.Parallel()
	a := datapackage.Document{ID: "DP-a-20260101-aaaa", Attempt: 1, Supersedes: "DP-a-20260102-bbbb"}
	b := datapackage.Document{ID: "DP-a-20260102-bbbb", Attempt: 2, Supersedes: "DP-a-20260101-aaaa"} // points back at a
	resolver := fakeResolver{
		docs:    map[string]datapackage.Document{a.ID: a, b.ID: b},
		reports: map[string]datapackage.Report{},
	}

	got := ResolveDelivery("XH-2", Deliverable{Name: "x", Ref: b.ID, Kind: DeliverableKindData}, resolver)

	if len(got.Chain) != 2 {
		t.Fatalf("want the cycle to stop after visiting each package once, got %d entries: %+v", len(got.Chain), got.Chain)
	}
}
