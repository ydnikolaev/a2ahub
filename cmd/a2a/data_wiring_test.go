package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
	"gopkg.in/yaml.v3"
)

// dataWiringTestSchema requires {"ok": true} — the smallest schema that can
// distinguish a passing entry from a failing one for these tests.
const dataWiringTestSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["ok"],"properties":{"ok":{"const":true}}}`

const dataWiringTestSchemaPath = "schema/order.schema.json"

func dataWiringTestDocument(t *testing.T, payload []byte) (datapackage.Document, map[string][]byte) {
	t.Helper()
	const entryPath = "orders.json"
	set, err := datapackage.BuildEntrySet([]datapackage.LocalFile{{RelPath: entryPath, Bytes: payload}})
	if err != nil {
		t.Fatalf("BuildEntrySet: %v", err)
	}
	count := int64(1)
	entry := set.Entries[0]
	entry.Role = datapackage.RoleDataset
	entry.MediaType = "application/json"
	entry.ConformsTo = dataWiringTestSchemaPath
	entry.RecordCount = &count

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	doc := datapackage.Document{
		Schema: "data-package/v1", ID: "DP-axon-20260804-ab12", Thread: "thread:axon-20260804-ab12",
		Contract: "XC-axon-widget@1.0.0#deadbeef", Mode: datapackage.ModeSnapshot,
		Classification: "internal", DataProfile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
		TransportDriver: datapackage.TransportDriverSpaceGit, Locator: "axon/data/DP-axon-20260804-ab12",
		Entries: []datapackage.Entry{entry}, AggregateDigest: set.AggregateDigest, SizeBytes: entry.SizeBytes,
		RecordCount: 1, CreatedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Attempt: 1, Provenance: datapackage.Provenance{OriginSystem: "axon", ExtractedAt: now.Format(time.RFC3339)},
	}
	return doc, map[string][]byte{entryPath: payload}
}

// newStubDataCore builds a dataCore whose only real dependencies are the two
// injectable resolution seams (this file's own load-bearing property under
// test) and a real, empty mirror directory (so loadEnvelope/readAllEvents/
// findHandoffForPackage — reached only on the verify-pass path — see a real,
// deterministic, empty tree rather than an arbitrary cwd).
func newStubDataCore(t *testing.T, doc datapackage.Document, payload map[string][]byte) (core *dataCore, resolveCalls *int) {
	t.Helper()
	calls := 0
	schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal test document: %v", err)
	}
	mirrorDir := t.TempDir()
	core = &dataCore{
		ownSystem: "axon",
		mirrorDir: mirrorDir,
		now:       func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) },
		entropy:   rand.Reader,
		resolveActor: func(kind, name, model string) (template.Actor, error) {
			return template.Actor{Kind: "agent", Name: "tester"}, nil
		},
		resolveCredential: func(context.Context) (host.Credential, error) { return host.Credential{}, nil },
		loadManifest:      func() (space.Manifest, error) { return space.Manifest{}, nil },
		resolveContractSchemas: func(context.Context, string) (map[string][]byte, string, error) {
			calls++
			return schemas, doc.Contract, nil
		},
		resolveDataPackage: func(context.Context, string) (space.DataPackageBytes, error) {
			return space.DataPackageBytes{System: "axon", Root: "axon/data/" + doc.ID, ManifestRaw: raw, Payload: payload}, nil
		},
	}
	return core, &calls
}

// TestDataCorePackEndToEnd exercises `dataCore.pack` through its real
// datapackage.Pack call — proving Locator's pre-mint placeholder actually
// satisfies datapackage.Pack's own step-2 refusal
// (!artifact.CleanRelativePath(req.Locator), which is false for "" — the
// bug this test exists to catch: a run against the original "" placeholder
// refuses on every single call to `a2a data pack`, and no earlier test in
// this file ever executed the pack path to notice) and that the staged
// manifest+payload land under this file's own writeStagedPackage
// convention, ready for `a2a data deliver <that path>`.
func TestDataCorePackEndToEnd(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stagingDir := t.TempDir()
	schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
	core := &dataCore{
		ownSystem:  "axon",
		stagingDir: stagingDir,
		now:        func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) },
		entropy:    rand.Reader,
		resolveContractSchemas: func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, "XC-axon-widget@1.0.0#deadbeef", nil
		},
	}

	result, err := core.pack(context.Background(), cli.DataPackRequest{
		ContractRef: "XC-axon-widget@1.0.0", From: from, Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if result.Manifest == nil {
		t.Fatalf("Manifest = nil")
	}
	wantLocator := "axon/data/" + result.Manifest.ID
	if result.Manifest.Locator != wantLocator {
		t.Fatalf("Locator = %q, want %q (the pre-mint placeholder %q must never leak into the returned manifest)", result.Manifest.Locator, wantLocator, "axon")
	}

	stagingRoot := filepath.Join(stagingDir, "data", result.Manifest.ID)
	manifestRaw, err := os.ReadFile(filepath.Join(stagingRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("staged manifest: %v", err)
	}
	var staged datapackage.Document
	if err := json.Unmarshal(manifestRaw, &staged); err != nil {
		t.Fatalf("decode staged manifest: %v", err)
	}
	if staged.ID != result.Manifest.ID || staged.Locator != wantLocator {
		t.Fatalf("staged manifest = %+v, want it to match the returned one", staged)
	}
	payloadRaw, err := os.ReadFile(filepath.Join(stagingRoot, "orders.json"))
	if err != nil {
		t.Fatalf("staged payload: %v", err)
	}
	if string(payloadRaw) != `{"ok":true}` {
		t.Fatalf("staged payload = %q", payloadRaw)
	}
}

// dataWiringTestSchemaPathTwo is a second contract schema entry, distinct
// from dataWiringTestSchemaPath, whose own stem ("customer") is a distinct
// directory name from the first's ("order") — spec 05a §T2.3.5 / plan D-13.
const dataWiringTestSchemaPathTwo = "schema/customer.schema.json"

func dataWiringTwoSchemaCore(t *testing.T, from string) *dataCore {
	t.Helper()
	schemas := map[string][]byte{
		dataWiringTestSchemaPath:    []byte(dataWiringTestSchema),
		dataWiringTestSchemaPathTwo: []byte(dataWiringTestSchema),
	}
	return &dataCore{
		ownSystem:  "axon",
		stagingDir: t.TempDir(),
		now:        func() time.Time { return time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC) },
		entropy:    rand.Reader,
		resolveContractSchemas: func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, "XC-axon-widget@1.0.0#deadbeef", nil
		},
	}
}

// TestDataCorePackDirectoryPerSchemaSourceResolvesEachEntry is D-13's own
// acceptance line: "a two-schema contract packs correctly from a
// directory-per-schema source" — the case W3's unconditional
// schemaCount != 1 refusal made impossible for EVERY multi-schema contract.
func TestDataCorePackDirectoryPerSchemaSourceResolvesEachEntry(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	if err := os.MkdirAll(filepath.Join(from, "order"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(from, "customer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "order", "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "customer", "customers.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	core := dataWiringTwoSchemaCore(t, from)
	result, err := core.pack(context.Background(), cli.DataPackRequest{
		ContractRef: "XC-axon-widget@1.0.0", From: from, Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	got := map[string]string{}
	for _, e := range result.Manifest.Entries {
		got[e.Path] = e.ConformsTo
	}
	if got["order/orders.json"] != dataWiringTestSchemaPath {
		t.Fatalf(`entries["order/orders.json"].ConformsTo = %q, want %q`, got["order/orders.json"], dataWiringTestSchemaPath)
	}
	if got["customer/customers.json"] != dataWiringTestSchemaPathTwo {
		t.Fatalf(`entries["customer/customers.json"].ConformsTo = %q, want %q`, got["customer/customers.json"], dataWiringTestSchemaPathTwo)
	}
}

// TestDataCorePackFlatSourceAgainstTwoSchemaContractRefusesNamingBoth is
// D-13's own acceptance line: "a flat source against that same contract is
// refused with both schema names in the message".
func TestDataCorePackFlatSourceAgainstTwoSchemaContractRefusesNamingBoth(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	core := dataWiringTwoSchemaCore(t, from)
	_, err := core.pack(context.Background(), cli.DataPackRequest{
		ContractRef: "XC-axon-widget@1.0.0", From: from, Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
	})
	if err == nil {
		t.Fatalf("pack: got nil error, want a refusal naming both schemas")
	}
	if !strings.Contains(err.Error(), dataWiringTestSchemaPath) || !strings.Contains(err.Error(), dataWiringTestSchemaPathTwo) {
		t.Fatalf("pack refusal = %q, want it to name both %q and %q", err.Error(), dataWiringTestSchemaPath, dataWiringTestSchemaPathTwo)
	}
}

// TestDataCoreVerifyResolvesContractSchemasExactlyOnce is the wave's own
// required receipt: "the contract carried set is resolved once per run —
// assert the call count, do not assert it in prose" (spec 05a plan D-2).
func TestDataCoreVerifyResolvesContractSchemasExactlyOnce(t *testing.T) {
	t.Parallel()

	doc, payload := dataWiringTestDocument(t, []byte(`{"ok":true}`))
	core, calls := newStubDataCore(t, doc, payload)

	result, err := core.verify(context.Background(), cli.DataVerifyRequest{PackageID: doc.ID})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("resolveContractSchemas called %d times, want exactly 1", *calls)
	}
	if result.Report == nil || result.Report.Result() != datapackage.ResultPass {
		t.Fatalf("report = %+v, want a passing report", result.Report)
	}
}

// newRecordTestDataCore builds a dataCore ready to exercise
// `verify(..., Record: true)`: a real, git-backed peer mirror (the consumer
// that runs verify is the handoff's TARGET, fold/table.go: TVerifyPass and
// TVerifyFail are both Role: RoleTarget from StateAcknowledged — a
// DIFFERENT dataCore instance than the producer that ran `a2a data
// deliver`, own system "peer" here), with the handoff already submitted (by
// axon) and acknowledged (by peer) — the one prior state both rows require.
func newRecordTestDataCore(t *testing.T, payload []byte) (core *dataCore, doc datapackage.Document, fake *host.FakeHost) {
	t.Helper()
	var payloadFiles map[string][]byte
	doc, payloadFiles = dataWiringTestDocument(t, payload)
	core, _ = newStubDataCore(t, doc, payloadFiles)

	fx := spacefixture.New(t, "axon", "peer")
	core.mirrorDir = fx.Clone("peer")
	core.remoteURL = fx.RemoteURL()
	core.repository = host.Repo{Owner: "acme", Name: "getvisa"}
	core.authorName, core.authorEmail = "a2a-peer", "a2a-peer@a2ahub.invalid"
	core.ownSystem = "peer"
	core.loadManifest = func() (space.Manifest, error) {
		return space.Manifest{Participants: []space.Participant{{System: "axon"}, {System: "peer"}}}, nil
	}
	seedDataWiringHandoff(t, core.mirrorDir, "axon", "peer", doc.ID)

	fake = host.NewFakeHost()
	core.funnel = space.NewWriteFunnel(fake, dataNoopSubmitValidator{}, "0.1.0")
	return core, doc, fake
}

// dataWiringCommittedEventTransition finds the ONE events/*.yaml file the
// record write committed (there is exactly one besides report.json) and
// decodes its own "transition" field — the ground truth for AC-5/D-12's
// "the direction is derived, not chosen": a test that only checked err==nil
// would pass whether the code drove verify-pass or verify-fail, which is
// exactly the property this file's own seeded-red receipt (removing the
// datapackage.VerifyPass(report) call and hardcoding fold.TVerifyPass)
// needs to be caught by.
func dataWiringCommittedEventTransition(t *testing.T, mirrorDir, branch string) string {
	t.Helper()
	changed := gitFixtureOutput(t, mirrorDir, "diff", "--name-only", "main", branch)
	var eventPath string
	for _, line := range strings.Split(strings.TrimSpace(changed), "\n") {
		if strings.Contains(line, "/events/") {
			eventPath = line
			break
		}
	}
	if eventPath == "" {
		t.Fatalf("committed tree %q carries no events/*.yaml file", changed)
	}
	raw := gitFixtureOutput(t, mirrorDir, "show", branch+":"+eventPath)
	var ev struct {
		Transition string `yaml:"transition"`
	}
	if err := yaml.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("decode committed event %s: %v", eventPath, err)
	}
	return ev.Transition
}

// TestDataCoreVerifyRecordDrivesVerifyPassOnAPassingReport is AC-5/D-12's
// pass-direction half: a passing report drives verify-pass, never a flag.
func TestDataCoreVerifyRecordDrivesVerifyPassOnAPassingReport(t *testing.T) {
	t.Parallel()

	core, doc, fake := newRecordTestDataCore(t, []byte(`{"ok":true}`))

	result, err := core.verify(context.Background(), cli.DataVerifyRequest{PackageID: doc.ID, Record: true})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Report == nil || result.Report.Result() != datapackage.ResultPass {
		t.Fatalf("report = %+v, want a passing report", result.Report)
	}
	if result.HandoffID != "XH-axon-20260804-cd34" {
		t.Fatalf("HandoffID = %q", result.HandoffID)
	}
	if result.Write == nil || len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("--record did not drive a write: Write=%+v pushes=%d opens=%d", result.Write, len(fake.Pushes), len(fake.Opens))
	}
	if got := dataWiringCommittedEventTransition(t, core.mirrorDir, result.Write.Branch); got != "verify-pass" {
		t.Fatalf("committed event transition = %q, want verify-pass", got)
	}
	reportRaw := gitFixtureOutput(t, core.mirrorDir, "show", result.Write.Branch+":peer/data/"+doc.ID+"/report.json")
	var committed datapackage.Report
	if err := json.Unmarshal([]byte(reportRaw), &committed); err != nil {
		t.Fatalf("decode committed report: %v", err)
	}
	if committed.Result() != datapackage.ResultPass {
		t.Fatalf("committed report result = %q, want pass", committed.Result())
	}
}

// TestDataCoreVerifyRecordDrivesVerifyFailOnAFailingReport is AC-5/D-12's
// fail-direction half — the gap this wave closes: W3 shipped no verify-fail
// path at all, so a consumer's rejection never reached the producer.
func TestDataCoreVerifyRecordDrivesVerifyFailOnAFailingReport(t *testing.T) {
	t.Parallel()

	core, doc, fake := newRecordTestDataCore(t, []byte(`{"ok":false}`))

	result, err := core.verify(context.Background(), cli.DataVerifyRequest{PackageID: doc.ID, Record: true})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Report == nil || result.Report.Result() == datapackage.ResultPass {
		t.Fatalf("report = %+v, want a non-passing report", result.Report)
	}
	if result.HandoffID != "XH-axon-20260804-cd34" {
		t.Fatalf("HandoffID = %q", result.HandoffID)
	}
	if result.Write == nil || len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("--record did not drive a write for a failing report: Write=%+v pushes=%d opens=%d", result.Write, len(fake.Pushes), len(fake.Opens))
	}
	if got := dataWiringCommittedEventTransition(t, core.mirrorDir, result.Write.Branch); got != "verify-fail" {
		t.Fatalf("committed event transition = %q, want verify-fail", got)
	}
	reportRaw := gitFixtureOutput(t, core.mirrorDir, "show", result.Write.Branch+":peer/data/"+doc.ID+"/report.json")
	var committed datapackage.Report
	if err := json.Unmarshal([]byte(reportRaw), &committed); err != nil {
		t.Fatalf("decode committed report: %v", err)
	}
	if committed.Result() == datapackage.ResultPass {
		t.Fatalf("committed report result = %q, want a non-passing result", committed.Result())
	}
}

// TestDataCoreVerifyRecordRepairsItsOwnPullRequestOnRerun is AC-5/D-12's
// idempotency half at the dataCore layer, mirroring
// TestRecordVerificationReportIdempotentRerunRepairsSamePR
// (internal/space/data_delivery_test.go): a re-run of `verify(...,
// Record: true)` against the SAME package and contract mints the SAME
// (deterministic) report id — dataReportEntropy's own load-bearing property
// — and therefore repairs the same pull request rather than opening a
// second one, even though the funnel would otherwise treat two separate
// process invocations as unrelated writes.
func TestDataCoreVerifyRecordRepairsItsOwnPullRequestOnRerun(t *testing.T) {
	t.Parallel()

	core, doc, fake := newRecordTestDataCore(t, []byte(`{"ok":true}`))

	first, err := core.verify(context.Background(), cli.DataVerifyRequest{PackageID: doc.ID, Record: true})
	if err != nil {
		t.Fatalf("first verify: %v", err)
	}
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "queued", HeadSHA: "checked-head"}, nil
	}

	second, err := core.verify(context.Background(), cli.DataVerifyRequest{PackageID: doc.ID, Record: true})
	if err != nil {
		t.Fatalf("second (re-run) verify: %v", err)
	}
	if second.Write == nil || first.Write == nil || second.Write.Branch != first.Write.Branch {
		t.Fatalf("second.Write = %+v, want the SAME branch as first %+v", second.Write, first.Write)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected NO second push/open cycle (no second PR), got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// seedDataWiringHandoff commits a handoff (from producer, to target) that
// carries packageID, already submitted (by producer) and acknowledged (by
// target) — the exact prior state fold/table.go's TVerifyPass row requires
// (From: StateAcknowledged) before this file's own verify() can drive it.
func seedDataWiringHandoff(t *testing.T, mirrorDir, producer, target, packageID string) {
	t.Helper()
	handoffID := "XH-" + producer + "-20260804-cd34"
	dir := filepath.Join(mirrorDir, producer, "exchanges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + handoffID + "\n" +
		"type: handoff\n" +
		"title: test handoff\n" +
		"space: test\n" +
		"from: " + producer + "\n" +
		"to: [" + target + "]\n" +
		"actor: {kind: agent, name: tester}\n" +
		"created: 2026-08-04T12:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"thread: thread:" + producer + "-20260804-cd34\n" +
		"fulfills: [XW-" + target + "-20260804-ab12]\n" +
		"deliverables:\n" +
		"  - {name: \"" + packageID + "\", ref: \"" + packageID + "\", kind: data}\n" +
		"verification: \"run a2a data verify\"\n" +
		"acceptance_criteria: [\"delivered\"]\n" +
		"limitations: []\n" +
		"---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, handoffID+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	eventsDir := filepath.Join(mirrorDir, producer, "events", "2026")
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	submitEvent := "schema: event/v1\n" +
		"event: 01K1A2B3C4D5E6F7G8H9J0K1M1\n" +
		"space: test\n" +
		"subject: " + handoffID + "\n" +
		"transition: submit\n" +
		"actor: {kind: agent, name: tester, system: " + producer + "}\n" +
		"at: 2026-08-04T12:00:00Z\n"
	if err := os.WriteFile(filepath.Join(eventsDir, "01K1A2B3C4D5E6F7G8H9J0K1M1.yaml"), []byte(submitEvent), 0o644); err != nil {
		t.Fatal(err)
	}
	ackEvent := "schema: event/v1\n" +
		"event: 01K1A2B3C4D5E6F7G8H9J0K1M2\n" +
		"space: test\n" +
		"subject: " + handoffID + "\n" +
		"transition: acknowledge\n" +
		"actor: {kind: agent, name: tester, system: " + target + "}\n" +
		"at: 2026-08-04T12:05:00Z\n"
	if err := os.WriteFile(filepath.Join(eventsDir, "01K1A2B3C4D5E6F7G8H9J0K1M2.yaml"), []byte(ackEvent), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadStagedPackageRefusesAManifestNamingAPathOutsideTheRoot is spec 05a
// §6.4's first adversarial case, at the seam where it can actually happen.
//
// `deliver` is a separate invocation from `pack`, so nothing proves the
// manifest sitting in a staging root is the one pack wrote. An earlier
// revision read each entry with os.ReadFile(filepath.Join(root, entry.Path))
// on the reasoning that pack had already proven containment — provenance, not
// a check. A manifest naming "../secret.txt" would have been read and
// committed into the exchange space.
func TestReadStagedPackageRefusesAManifestNamingAPathOutsideTheRoot(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "staging")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "schema": "data-package/v1",
	  "id": "DP-axon-20260804-k3f9",
	  "thread": "thread:axon-20260721-k3f9",
	  "contract": "XC-axon-widget@1.0.0#sha256:` + strings.Repeat("a", 64) + `",
	  "mode": "snapshot",
	  "classification": "internal",
	  "data_profile": "synthetic",
	  "format": "json",
	  "transport_driver": "space-git",
	  "locator": "axon/data/DP-axon-20260804-k3f9",
	  "entries": [{
	    "path": "../secret.txt",
	    "role": "dataset",
	    "media_type": "application/json",
	    "conforms_to": "schema/widget.schema.json",
	    "size_bytes": 7,
	    "digest": "sha256:` + strings.Repeat("b", 64) + `",
	    "record_count": 1
	  }],
	  "aggregate_digest": "sha256:` + strings.Repeat("c", 64) + `",
	  "size_bytes": 7,
	  "record_count": 1,
	  "attempt": 1,
	  "created_at": "2026-08-04T12:00:00Z",
	  "expires_at": "2026-09-04T12:00:00Z",
	  "provenance": {"origin_system": "axon", "extracted_at": "2026-08-04T12:00:00Z"}
	}`
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := readStagedPackage(context.Background(), root)
	if err == nil {
		t.Fatal("readStagedPackage(manifest naming ../secret.txt) = nil error, want a refusal")
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatalf("the refusal leaked the file's contents: %v", err)
	}
	// The schema's own path pattern is what refuses it, which is the right
	// layer: a traversal is unrepresentable in a valid manifest rather than
	// merely caught later. The walker's containment and VerifyEntrySet's
	// digest proof sit behind it as the second and third guards.
	if !strings.Contains(err.Error(), "entries.0.path") {
		t.Fatalf("refusal did not name the offending entry path: %v", err)
	}
}
