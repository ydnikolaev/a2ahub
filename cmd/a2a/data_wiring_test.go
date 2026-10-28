package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
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

// TestMCPDataAdapterPackAppliesDefaultExpiryWhenOmitted proves change #3
// (spec 05a's own delivery-loop wave): cli.DefaultDataPackExpiry's one-week
// floor lives in dataCore.pack — the ONE production core BOTH surfaces
// call — so an MCP `pack` that omits `expires` (mcp.DataPackRequest's own
// zero time.Duration, tools_data.go's dataDuration returning 0 for an empty
// string) mints a manifest whose expires_at is a week past its creation
// instant, not equal to it. Driven through mcpDataAdapter.Pack (not
// dataCore.pack directly) so the assertion covers the actual MCP-facing
// seam, not just the core it delegates to.
func TestMCPDataAdapterPackAppliesDefaultExpiryWhenOmitted(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
	core := &dataCore{
		ownSystem:  "axon",
		stagingDir: t.TempDir(),
		now:        func() time.Time { return now },
		entropy:    rand.Reader,
		resolveContractSchemas: func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, "XC-axon-widget@1.0.0#deadbeef", nil
		},
	}
	adapter := mcpDataAdapter{core: core}

	result, err := adapter.Pack(context.Background(), mcp.DataPackRequest{
		ContractRef: "XC-axon-widget@1.0.0", From: from,
		Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
		// Expires deliberately omitted (zero value) — the MCP surface's own
		// "the caller said nothing" shape.
	})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if result.Manifest == nil {
		t.Fatalf("Manifest = nil")
	}
	if result.Manifest.CreatedAt == result.Manifest.ExpiresAt {
		t.Fatalf("expires_at (%s) equals created_at (%s): the package would be expired at its own creation instant",
			result.Manifest.ExpiresAt, result.Manifest.CreatedAt)
	}
	wantExpiresAt := now.Add(cli.DefaultDataPackExpiry).UTC().Format(time.RFC3339)
	if result.Manifest.ExpiresAt != wantExpiresAt {
		t.Fatalf("expires_at = %q, want %q (created_at + DefaultDataPackExpiry)", result.Manifest.ExpiresAt, wantExpiresAt)
	}
}

// TestDataPackExpiryFloorRefusesNonPositive is dataPackExpiryFloor's own
// negative-duration half: an MCP caller CAN express a negative Go duration
// string (e.g. "-24h", unlike the CLI's flag.Duration usage layer, which
// refuses it before a request is ever built) — the floor is the only guard
// standing between that input and a manifest expired before it exists.
func TestDataPackExpiryFloorRefusesNonPositive(t *testing.T) {
	t.Parallel()
	if _, err := dataPackExpiryFloor(-1 * time.Hour); err == nil {
		t.Fatal("dataPackExpiryFloor(-1h): got nil error, want a refusal")
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

// TestMCPDataAdapterVerifyRecordDrivesVerifyFailOnAFailingReport is this
// wave's change #2 acceptance line, driven through the ACTUAL MCP-facing
// seam rather than dataCore.verify directly: with a FAILING report,
// mcpDataAdapter.Verify(ctx, mcp.DataVerifyRequest{Record: true, ...})
// results in a committed verify-fail transition. mcp.DataVerifyRequest has
// no Pass/verdict field at all — record is the only input that can drive a
// write, and its direction is derived from the report inside dataCore.verify
// (untouched by this translation layer) — so there is structurally no input
// through this adapter that can produce verify-pass on a failing report. The
// companion proof that the wire schema itself carries no such field is
// internal/mcp's own TestDataToolVerifyHasNoPassField (a raw "pass" key is a
// hard decode error, DisallowUnknownFields).
//
// This test's own distinct property — over dataCore.verify's existing
// TestDataCoreVerifyRecordDrivesVerifyFailOnAFailingReport receipt (removing
// datapackage.VerifyPass, which that test already covers) — is the
// TRANSLATION layer this file adds. Seeded-red receipt: in
// mcpDataAdapter.Verify, hardcode `Record: false` instead of forwarding
// `req.Record` — this test then fails with "record did not drive a write",
// proving the adapter (not just the core) actually threads the caller's
// record flag through.
func TestMCPDataAdapterVerifyRecordDrivesVerifyFailOnAFailingReport(t *testing.T) {
	t.Parallel()

	core, doc, fake := newRecordTestDataCore(t, []byte(`{"ok":false}`))
	adapter := mcpDataAdapter{core: core}

	result, err := adapter.Verify(context.Background(), mcp.DataVerifyRequest{PackageID: doc.ID, Record: true})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Report == nil || result.Report.Result() == datapackage.ResultPass {
		t.Fatalf("report = %+v, want a non-passing report", result.Report)
	}
	if result.Write == nil || len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("record did not drive a write for a failing report: Write=%+v pushes=%d opens=%d", result.Write, len(fake.Pushes), len(fake.Opens))
	}
	if got := dataWiringCommittedEventTransition(t, core.mirrorDir, result.Write.Branch); got != "verify-fail" {
		t.Fatalf("committed event transition = %q, want verify-fail (never verify-pass — the direction is derived from the report, not chosen)", got)
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

// --- attach / fetch-blob (P10 agent-exchange-2026-08 spec 10, wave B) -----

// blobPayloadDigestForTest reimplements — never calls — the exact two-
// branch algorithm internal/datapackage's own digestForPayload (attach.go)
// and internal/space's own recomputeAttachmentDigest (possession.go) both
// already implement independently (neither is exported nor importable
// across the ADR-001 boundary this test lives on the far side of). A THIRD
// independent copy, agreeing with both, is this wave's own cross-check that
// dataCore.deliverBlob's real, end-to-end write commits a payload whose
// declared digest is the value every OTHER layer of this system would also
// compute for the same bytes.
func blobPayloadDigestForTest(payload map[string][]byte) string {
	if len(payload) == 1 {
		for _, raw := range payload {
			return artifact.Digest(raw)
		}
	}
	perFile := make(map[string]string, len(payload))
	for path, raw := range payload {
		perFile[path] = artifact.Digest(raw)
	}
	return artifact.CombineDigestPairs(perFile)
}

// corruptBlobDigestSidecarForTest checks out main, mutates the committed
// blob.json sidecar's own digest field so it no longer matches the payload
// it declares, and commits the change LOCALLY (the caller pushes it) — the
// AC7 read-side fixture: "a corrupted blob on fetch" (spec 10 AC7).
func corruptBlobDigestSidecarForTest(t *testing.T, mirrorDir, system, blobID string) {
	t.Helper()
	runGitFixture(t, mirrorDir, "checkout", "-B", "main", "origin/main")
	relPath := filepath.Join(system, "blobs", blobID, "blob.json")
	raw, err := os.ReadFile(filepath.Join(mirrorDir, relPath))
	if err != nil {
		t.Fatalf("read digest sidecar: %v", err)
	}
	corrupted := strings.Replace(string(raw), `"digest":"sha256:`, `"digest":"sha256:deadbeef`, 1)
	if corrupted == string(raw) {
		t.Fatalf("sidecar did not contain a digest field to corrupt: %s", raw)
	}
	if err := os.WriteFile(filepath.Join(mirrorDir, relPath), []byte(corrupted), 0o644); err != nil {
		t.Fatalf("write corrupted sidecar: %v", err)
	}
	runGitFixture(t, mirrorDir, "add", filepath.ToSlash(relPath))
	runGitFixture(t, mirrorDir, "commit", "-m", "test: corrupt blob digest sidecar")
}

// TestDataCoreDeliverBlobAndFetchBlobEndToEnd is P10 (agent-exchange-2026-08)
// spec 10 wave B's own composed proof: dataCore.deliverBlob and
// dataCore.fetchBlob — the exact methods cliAttachAdapter/cliFetchAdapter
// (cmd_attach.go/cmd_fetch.go's own seams) and mcpDataAdapter/mcpDataRouter
// all delegate to — driven through a REAL git fixture and a fake (offline,
// in-memory) GitHub host, NEVER real GitHub (the PR "landing" below is
// simulated by pushing straight to the fixture's own bare origin, mirroring
// work_wiring_integration_test.go's own precedent for the identical need).
//
// It closes the one gap unit tests on either side of this seam cannot see:
// whether a payload's KEYS — not just its bytes — survive
// CLI-built-payload -> DeliverBlob's commit -> git -> ResolveBlob's read
// back. digestForPayload's multi-entry branch hashes (path, digest) PAIRS
// (blobPayloadDigestForTest above, reproducing it independently a third
// time), so a key silently reshaped anywhere in that round trip would
// produce a digest mismatch nothing else in this wave's test suite would
// catch. It also drives AC3 (fetch reproduces exact bytes, digest-verified,
// re-fetch is idempotent) and AC7's read-side half (a corrupted blob on
// fetch refuses) through the real wiring rather than only through
// internal/space's own direct-construction tests.
func TestDataCoreDeliverBlobAndFetchBlobEndToEnd(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	mirrorDir := fx.Clone("axon")
	fake := host.NewFakeHost()
	core := &dataCore{
		ownSystem: "axon", mirrorDir: mirrorDir,
		remoteURL: fx.RemoteURL(), repository: host.Repo{Owner: "acme", Name: "getvisa"},
		authorName: "a2a-axon", authorEmail: "a2a-axon@a2ahub.invalid",
		now: time.Now, entropy: rand.Reader,
		resolveCredential: func(context.Context) (host.Credential, error) { return host.Credential{}, nil },
		loadManifest:      func() (space.Manifest, error) { return space.Manifest{}, nil },
		funnel:            space.NewWriteFunnel(fake, dataNoopSubmitValidator{}, "0.1.0"),
	}

	// A nested, multi-file payload deliberately exercises the key-sensitive
	// multi-entry branch (CombineDigestPairs over (path, digest) pairs) —
	// the branch a single-file payload could never distinguish from a bug
	// that silently reshaped or dropped a key.
	payload := map[string][]byte{
		"sub/dir/a.txt": []byte("alpha bytes"),
		"b.txt":         []byte("bravo bytes"),
	}
	wantDigest := blobPayloadDigestForTest(payload)

	ctx := context.Background()
	blobID, write, err := core.deliverBlob(ctx, payload)
	if err != nil {
		t.Fatalf("deliverBlob: %v", err)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("deliverBlob did not push+open exactly once: pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}

	// Simulate the PR landing (never real GitHub): fast-forward the
	// fixture's own bare origin's main to the pushed branch.
	runGitFixture(t, mirrorDir, "push", "origin", write.Branch+":main")
	runGitFixture(t, mirrorDir, "fetch", "origin", "main")

	resolved, err := space.ResolveBlob(ctx, mirrorDir, blobID)
	if err != nil {
		t.Fatalf("ResolveBlob: %v", err)
	}
	if resolved.Digest != wantDigest {
		t.Fatalf("resolved digest = %q, want %q (the digest datapackage's own attach core would have declared for this payload)", resolved.Digest, wantDigest)
	}
	if len(resolved.Payload) != len(payload) {
		t.Fatalf("resolved payload has %d entries, want %d: %v", len(resolved.Payload), len(payload), resolved.Payload)
	}
	for relPath, want := range payload {
		got, ok := resolved.Payload[relPath]
		if !ok {
			t.Fatalf("resolved payload missing key %q (payload keys did not survive the round trip): %v", relPath, resolved.Payload)
		}
		if string(got) != string(want) {
			t.Fatalf("resolved payload[%q] = %q, want %q", relPath, got, want)
		}
	}

	// fetchBlob: bytes land byte-identical, "installed" the first time.
	dest := filepath.Join(t.TempDir(), "landed")
	outcome, err := core.fetchBlob(ctx, blobID, dest)
	if err != nil {
		t.Fatalf("fetchBlob: %v", err)
	}
	if outcome != string(datapackage.Installed) {
		t.Fatalf("fetchBlob outcome = %q, want %q", outcome, datapackage.Installed)
	}
	for relPath, want := range payload {
		got, rerr := os.ReadFile(filepath.Join(dest, filepath.FromSlash(relPath)))
		if rerr != nil {
			t.Fatalf("read fetched %s: %v", relPath, rerr)
		}
		if string(got) != string(want) {
			t.Fatalf("fetched %s = %q, want %q", relPath, got, want)
		}
	}

	// Re-fetch to the SAME destination: byte-identical content is accepted
	// as "already-present", never refused (InstallLocalFiles' own no-replace
	// semantics — the precedent cmd_fetch.go's own doc comment names rather
	// than inventing a second "--to must be empty" rule).
	outcome, err = core.fetchBlob(ctx, blobID, dest)
	if err != nil {
		t.Fatalf("re-fetchBlob: %v", err)
	}
	if outcome != string(datapackage.AlreadyPresent) {
		t.Fatalf("re-fetchBlob outcome = %q, want %q", outcome, datapackage.AlreadyPresent)
	}

	// Corrupt the blob's own committed digest sidecar and prove fetch
	// refuses rather than silently installing bytes that no longer match
	// what the space itself declared (AC7's read-side half).
	corruptBlobDigestSidecarForTest(t, mirrorDir, "axon", blobID)
	runGitFixture(t, mirrorDir, "push", "origin", "main:main")
	runGitFixture(t, mirrorDir, "fetch", "origin", "main")
	if _, err := core.fetchBlob(ctx, blobID, filepath.Join(t.TempDir(), "corrupt")); err == nil {
		t.Fatal("fetchBlob over a corrupted digest sidecar = nil error, want a digest-mismatch refusal")
	}
}

// --- express flow (spec 05 §8 rows 8-11 and 13) ------------------------------
//
// These drive the real sequencer over a real git mirror, with the one thing
// the fake host cannot do made scriptable: what each inner write returns
// (expressScriptedFunnel) and what waiting for a pending merge does. A write
// that "merges" is applied to the mirror's working tree, so the next step
// reads the state the previous one landed, exactly as the production refresh
// would show it.

// expressScriptedFunnel is the inner verbs' funnel: it records every request,
// answers with the state the test scripted for that call (merged unless said
// otherwise), and applies a merged write's files to the mirror.
type expressScriptedFunnel struct {
	// t fails the test when applying a merged write to the mirror fails, so a
	// scripted "merge" that did not land cannot read as a flow bug. Set by
	// installExpressSeams.
	t      testing.TB
	mu     sync.Mutex
	states []space.WriteState
	calls  []space.SubmitRequest
	queued map[string]space.SubmitRequest
}

func (f *expressScriptedFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := len(f.calls)
	f.calls = append(f.calls, req)
	state := space.WriteStateMerged
	if i < len(f.states) {
		state = f.states[i]
	}
	result := space.WriteResult{
		Branch: fmt.Sprintf("a2a/%s/%s-%d", req.System, req.ArtifactID, i), PRNumber: i + 1,
		PRURL: fmt.Sprintf("https://example.invalid/pr/%d", i+1), State: state, ArtifactIDs: req.ArtifactIDs,
		Note: "scripted",
	}
	switch state {
	case space.WriteStateMerged:
		result.Stage = space.WriteStageMerged
		expressApplyToMirror(f.t, req)
	case space.WriteStatePendingMerge:
		result.Stage = space.WriteStageAutoMergeArmed
		if f.queued == nil {
			f.queued = map[string]space.SubmitRequest{}
		}
		f.queued[result.Branch] = req
	default:
		result.Stage = space.WriteStagePRCreated
	}
	return result, nil
}

// land merges a queued pending write, as the awaited PR merging would, and
// reports whether the branch was one of this funnel's.
func (f *expressScriptedFunnel) land(branch string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	req, ok := f.queued[branch]
	if ok {
		expressApplyToMirror(f.t, req)
		delete(f.queued, branch)
	}
	return ok
}

func expressApplyToMirror(t testing.TB, req space.SubmitRequest) {
	t.Helper()
	write := func(path string, content []byte) {
		full := filepath.Join(req.RepoDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("apply a scripted merge: create %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatalf("apply a scripted merge: write %s: %v", full, err)
		}
	}
	for _, file := range req.Files {
		write(file.Path, file.Content)
	}
	for _, m := range req.Mutations {
		if m.Operation == space.MutationWrite {
			write(m.Path, m.Bytes)
		}
	}
}

// paths lists every path the recorded writes prepared.
func (f *expressScriptedFunnel) paths() []string {
	var out []string
	for _, req := range f.calls {
		for _, file := range req.Files {
			out = append(out, file.Path)
		}
		for _, m := range req.Mutations {
			out = append(out, m.Path)
		}
	}
	return out
}

// content returns the concatenated bytes of every file the call prepared.
func (f *expressScriptedFunnel) content(call int) string {
	var b strings.Builder
	for _, file := range f.calls[call].Files {
		b.Write(file.Content)
	}
	for _, m := range f.calls[call].Mutations {
		b.Write(m.Bytes)
	}
	return b.String()
}

type expressWaitLog struct {
	mu       sync.Mutex
	branches []string
}

// installExpressSeams points the core's express seams at the scripted funnel:
// the mirror refresh is a no-op (writes are applied on merge), and waiting
// for a pending merge lands the awaited branch, or fails with waitErr.
func installExpressSeams(t testing.TB, core *dataCore, funnel *expressScriptedFunnel, waitErr error) *expressWaitLog {
	t.Helper()
	funnel.t = t
	log := &expressWaitLog{}
	core.refreshMirror = func(context.Context) error { return nil }
	core.expressFunnel = func(space.Manifest) (expressSubmitter, error) { return funnel, nil }
	core.awaitMerge = func(_ context.Context, req space.AwaitRequest) (space.AwaitResult, error) {
		log.mu.Lock()
		log.branches = append(log.branches, req.Branch)
		log.mu.Unlock()
		if waitErr != nil {
			return space.AwaitResult{}, waitErr
		}
		funnel.land(req.Branch)
		return space.AwaitResult{PRURL: "merged"}, nil
	}
	return log
}

type expressSeedEvent struct {
	system, transition, subject string
	refs                        []string
}

// seedExpressEvents commits events into the mirror's working tree in order.
func seedExpressEvents(t *testing.T, mirrorDir string, events ...expressSeedEvent) {
	t.Helper()
	seedExpressEventsFrom(t, mirrorDir, 0, events...)
}

// seedExpressEventsFrom is seedExpressEvents with the ULID counter starting
// after offset, so a second seeding into the same mirror does not overwrite the
// first one's files. Every seeded ULID sorts before any ULID the code under test
// mints from the test clock, so seeded history always precedes minted history.
func seedExpressEventsFrom(t *testing.T, mirrorDir string, offset int, events ...expressSeedEvent) {
	t.Helper()
	for i, ev := range events {
		ulid := fmt.Sprintf("01K1A2B3C4D5E6F7G8H9J0%04d", offset+i+1)
		dir := filepath.Join(mirrorDir, ev.system, "events", "2026")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		doc := "schema: event/v1\nevent: " + ulid + "\nspace: test\nsubject: " + ev.subject + "\ntransition: " + ev.transition +
			"\nactor: {kind: agent, name: tester, system: " + ev.system + "}\nat: 2026-08-04T12:00:00Z\n"
		if len(ev.refs) > 0 {
			doc += "refs:\n"
			for _, ref := range ev.refs {
				doc += "  - ref: " + ref + "\n"
			}
		}
		if err := os.WriteFile(filepath.Join(dir, ulid+".yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// seedExpressWorkRequest writes the committed work_request from consumer to
// producer (envelope/v2, so its criteria carry ids), its response, and the
// submit/ack/accept/respond history that folds it to `responded`.
func seedExpressWorkRequest(t *testing.T, mirrorDir, consumer, producer, wrID, responseID, packageID string, criteria []string) {
	t.Helper()
	dir := filepath.Join(mirrorDir, consumer, "exchanges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var ac strings.Builder
	for i, text := range criteria {
		fmt.Fprintf(&ac, "  - id: ac%d\n    text: %q\n", i+1, text)
	}
	wr := "---\nschema: envelope/v2\nid: " + wrID + "\ntype: work_request\ntitle: t\nspace: test\nfrom: " + consumer +
		"\nto: [" + producer + "]\nactor: {kind: agent, name: tester}\ncreated: 2026-08-04T12:00:00Z\ncategory: data\npriority: p3\n" +
		"blocking: false\ninterim_behavior: x\nneeded_by: 2026-08-06\nthread: thread:" + consumer + "-20260804-ab12\n" +
		"acceptance_criteria:\n" + ac.String() + "refs:\n  - ref: XC-axon-widget@1.0.0\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, wrID+".md"), []byte(wr), 0o644); err != nil {
		t.Fatal(err)
	}
	pdir := filepath.Join(mirrorDir, producer, "exchanges")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	resp := "---\nschema: envelope/v1\nid: " + responseID + "\ntype: response\ntitle: r\nspace: test\nfrom: " + producer +
		"\nto: [" + consumer + "]\nactor: {kind: agent, name: tester}\ncreated: 2026-08-04T12:00:00Z\nparent: " + wrID +
		"\ndelivers: [" + packageID + "]\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(pdir, responseID+".md"), []byte(resp), 0o644); err != nil {
		t.Fatal(err)
	}
	seedExpressEvents(t, mirrorDir,
		expressSeedEvent{system: consumer, transition: "submit", subject: wrID},
		expressSeedEvent{system: producer, transition: "acknowledge", subject: wrID},
		expressSeedEvent{system: producer, transition: "accept", subject: wrID},
		expressSeedEvent{system: producer, transition: "respond", subject: wrID, refs: []string{responseID}},
	)
}

const (
	expressTestWR       = "XW-peer-20260804-ab12"
	expressTestResponse = "XS-axon-20260804-ef56"
)

// newExpressVerifyRig is the consumer's side of a finished delivery: the
// record-test core (own system "peer", the handoff's target), the work_request
// and its response seeded, and the express seams scripted.
func newExpressVerifyRig(t *testing.T, floor string, criteria []string) (*dataCore, *expressScriptedFunnel, *host.FakeHost, *expressWaitLog) {
	t.Helper()
	return newExpressVerifyRigWithPayload(t, floor, criteria, []byte(`{"ok":true}`))
}

// newExpressVerifyRigWithPayload is newExpressVerifyRig over a package whose one
// entry holds payload, so a rig can judge to fail as well as to pass.
func newExpressVerifyRigWithPayload(t *testing.T, floor string, criteria []string, payload []byte) (*dataCore, *expressScriptedFunnel, *host.FakeHost, *expressWaitLog) {
	t.Helper()
	core, doc, fake := newRecordTestDataCore(t, payload)
	core.spaceID = "test"
	core.stagingDir = t.TempDir()
	// The record write goes through the real funnel, whose own gate refuses a
	// binary older than the space's floor; this rig plays a current binary.
	core.funnel = space.NewWriteFunnel(fake, dataNoopSubmitValidator{}, "99.0.0")
	core.loadManifest = func() (space.Manifest, error) {
		return space.Manifest{MinBinaryVersion: floor, Participants: []space.Participant{{System: "axon"}, {System: "peer"}}}, nil
	}
	seedExpressWorkRequest(t, core.mirrorDir, "peer", "axon", expressTestWR, expressTestResponse, doc.ID, criteria)
	funnel := &expressScriptedFunnel{}
	log := installExpressSeams(t, core, funnel, nil)
	return core, funnel, fake, log
}

func expressStepSummary(steps []cli.DataStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Step + ":" + s.Outcome
	}
	return out
}

// TestExpressVerifyDerivesTheVerdictByFloor is spec 05 §8 row 10: at the
// event/v2 floor a passing record closes with the one verdict the
// verification proves, criterion ac1 met by the producer; below the floor the
// close carries no verdicts.
func TestExpressVerifyDerivesTheVerdictByFloor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		floor        string
		wantVerdicts bool
	}{
		{"at the floor", contract.ContractPublicationFloor, true},
		{"below the floor", "0.1.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			core, funnel, _, _ := newExpressVerifyRig(t, tc.floor, []string{expressVerifyCriterionText})
			result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true})
			if err != nil {
				t.Fatalf("verifyFor: %v (steps %v)", err, expressStepSummary(result.Steps))
			}
			if len(funnel.calls) != 1 || funnel.calls[0].Verb != "close" {
				t.Fatalf("inner writes = %v, want exactly one close", funnel.calls)
			}
			closeBody := funnel.content(0)
			hasVerdicts := strings.Contains(closeBody, "verdicts:")
			if hasVerdicts != tc.wantVerdicts {
				t.Fatalf("close event verdicts present = %v, want %v:\n%s", hasVerdicts, tc.wantVerdicts, closeBody)
			}
			if tc.wantVerdicts {
				for _, want := range []string{"criterion: ac1", "verdict: met", "cause_owner: axon"} {
					if !strings.Contains(closeBody, want) {
						t.Fatalf("close event lacks %q:\n%s", want, closeBody)
					}
				}
			}
		})
	}
}

// TestExpressVerifyExtraCriteriaNeedCallerVerdicts is spec 05 §8 row 11: at
// the floor a request carrying ac2 closes only with the caller's verdict for
// it. Without one the verb refuses BEFORE its first write, naming ac2; below
// the floor a caller verdict is refused too, because the event cannot carry it.
func TestExpressVerifyExtraCriteriaNeedCallerVerdicts(t *testing.T) {
	t.Parallel()
	criteria := []string{expressVerifyCriterionText, "rows reconcile with billing"}

	t.Run("refused before any write, naming ac2", func(t *testing.T) {
		t.Parallel()
		core, funnel, fake, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, criteria)
		result, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true})
		if err == nil || !strings.Contains(err.Error(), "ac2") {
			t.Fatalf("verifyFor err = %v, want a refusal naming ac2", err)
		}
		if len(funnel.calls) != 0 || len(fake.Pushes) != 0 || len(result.Steps) != 0 {
			t.Fatalf("a write happened before the refusal: inner=%d pushes=%d steps=%v", len(funnel.calls), len(fake.Pushes), result.Steps)
		}
	})

	t.Run("closes with the caller verdict for ac2", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, criteria)
		_, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{
			For: expressTestWR, Record: true, Verdicts: []string{"ac2:unmet:axon"},
		})
		if err != nil {
			t.Fatalf("verifyFor: %v", err)
		}
		closeBody := funnel.content(len(funnel.calls) - 1)
		for _, want := range []string{"criterion: ac1", "verdict: met", "criterion: ac2", "verdict: unmet"} {
			if !strings.Contains(closeBody, want) {
				t.Fatalf("close event lacks %q:\n%s", want, closeBody)
			}
		}
	})

	t.Run("caller verdict below the floor is refused before any write", func(t *testing.T) {
		t.Parallel()
		core, funnel, fake, _ := newExpressVerifyRig(t, "0.1.0", criteria)
		_, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{
			For: expressTestWR, Record: true, Verdicts: []string{"ac2:met:axon"},
		})
		if err == nil || !strings.Contains(err.Error(), "event/v2") {
			t.Fatalf("verifyFor err = %v, want a refusal naming the event/v2 floor", err)
		}
		if len(funnel.calls) != 0 || len(fake.Pushes) != 0 {
			t.Fatalf("a write happened before the refusal: inner=%d pushes=%d", len(funnel.calls), len(fake.Pushes))
		}
	})
}

// TestExpressSequencesByWriteState is spec 05 §8 row 9: a pending-merge inner
// write is waited on, on that write's own branch, before the next write; a
// timeout stops the run naming the last landed step with no further write; a
// needs-attention write stops it before the next one.
func TestExpressSequencesByWriteState(t *testing.T) {
	t.Parallel()
	// The seeded handoff of newRecordTestDataCore fulfills expressTestWR, so the
	// producer scenarios use a request nothing has been delivered against.
	const producerWR = "XW-peer-20260804-cd99"
	producerRig := func(t *testing.T, states []space.WriteState, waitErr error) (*dataCore, *expressScriptedFunnel, *expressWaitLog) {
		t.Helper()
		core, _, _ := newRecordTestDataCore(t, []byte(`{"ok":true}`))
		core.ownSystem = "axon" // the producer: the work_request is addressed to axon
		core.authorName, core.authorEmail = "a2a-axon", "a2a-axon@a2ahub.invalid"
		core.spaceID = "test"
		core.stagingDir = t.TempDir()
		core.loadManifest = func() (space.Manifest, error) {
			return space.Manifest{MinBinaryVersion: "0.1.0", Participants: []space.Participant{{System: "axon"}, {System: "peer"}}}, nil
		}
		// A request that was only submitted: ack and accept are both owed.
		dir := filepath.Join(core.mirrorDir, "peer", "exchanges")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		wr := "---\nschema: envelope/v1\nid: " + producerWR + "\ntype: work_request\ntitle: t\nspace: test\nfrom: peer\nto: [axon]\n" +
			"actor: {kind: agent, name: tester}\ncreated: 2026-08-04T12:00:00Z\ncategory: data\npriority: p3\nblocking: false\n" +
			"interim_behavior: x\nneeded_by: 2026-08-06\nthread: thread:peer-20260804-ab12\nacceptance_criteria: [\"c\"]\n" +
			"refs:\n  - ref: XC-axon-widget@1.0.0\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, producerWR+".md"), []byte(wr), 0o644); err != nil {
			t.Fatal(err)
		}
		seedExpressEvents(t, core.mirrorDir, expressSeedEvent{system: "peer", transition: "submit", subject: producerWR})
		funnel := &expressScriptedFunnel{states: states}
		return core, funnel, installExpressSeams(t, core, funnel, waitErr)
	}
	deliverReq := func(dir string) cli.DataDeliverForRequest {
		return cli.DataDeliverForRequest{For: producerWR, From: dir, Profile: "synthetic", Format: "json", Timeout: time.Minute}
	}

	t.Run("pending-merge is awaited on its own branch before the next write", func(t *testing.T) {
		t.Parallel()
		core, funnel, log := producerRig(t, []space.WriteState{space.WriteStatePendingMerge, space.WriteStateNeedsAttention}, nil)
		core.awaitMerge = func(ctx context.Context, req space.AwaitRequest) (space.AwaitResult, error) {
			if got := len(funnel.calls); got != 1 {
				t.Errorf("waited with %d inner write(s) made, want exactly the ack before the wait", got)
			}
			return func() (space.AwaitResult, error) {
				log.mu.Lock()
				log.branches = append(log.branches, req.Branch)
				log.mu.Unlock()
				funnel.land(req.Branch)
				return space.AwaitResult{PRURL: "merged"}, nil
			}()
		}
		result, err := core.deliverFor(context.Background(), deliverReq(t.TempDir()))
		if err == nil || !strings.Contains(err.Error(), "last landed step: ack "+producerWR) {
			t.Fatalf("deliverFor err = %v, want the needs-attention accept to stop the run naming the landed ack", err)
		}
		if got, want := expressStepSummary(result.Steps), []string{"ack:landed", "accept:stopped"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("steps = %v, want %v", got, want)
		}
		if len(funnel.calls) != 2 {
			t.Fatalf("inner writes = %d, want 2 (ack, then the accept that needs attention) and nothing after", len(funnel.calls))
		}
		wantBranch := fmt.Sprintf("a2a/axon/%s-0", funnel.calls[0].ArtifactID)
		if len(log.branches) != 1 || log.branches[0] != wantBranch {
			t.Fatalf("awaited branches = %v, want [%s]", log.branches, wantBranch)
		}
	})

	t.Run("a timed-out wait names the last landed step and writes nothing more", func(t *testing.T) {
		t.Parallel()
		core, funnel, _ := producerRig(t, []space.WriteState{space.WriteStatePendingMerge}, context.DeadlineExceeded)
		result, err := core.deliverFor(context.Background(), deliverReq(t.TempDir()))
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "not merged within") || !strings.Contains(err.Error(), "last landed step: none") {
			t.Fatalf("deliverFor err = %v, want the timeout naming the unmerged PR and that no step had landed", err)
		}
		if got, want := expressStepSummary(result.Steps), []string{"ack:stopped"}; strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("steps = %v, want %v", got, want)
		}
		if len(funnel.calls) != 1 {
			t.Fatalf("inner writes = %d, want 1: nothing may follow a timed-out wait", len(funnel.calls))
		}
	})
}

// TestExpressWritesOnlyUnderTheInvokingSystemsSection is spec 05 §8 row 13:
// every path the composite verbs prepare for a write lies under
// <invoking system>/. The shipped write funnel refuses a path outside the
// section on its own; this proves the sequencer never prepares one, over the
// request, the producer's ack and accept, and the consumer's close.
func TestExpressWritesOnlyUnderTheInvokingSystemsSection(t *testing.T) {
	t.Parallel()
	assertUnder := func(t *testing.T, system string, funnel *expressScriptedFunnel) {
		t.Helper()
		paths := funnel.paths()
		if len(paths) == 0 {
			t.Fatal("no write was prepared; the assertion would pass vacuously")
		}
		for _, path := range paths {
			if !strings.HasPrefix(path, system+"/") {
				t.Errorf("%s prepared %q, outside its own section %s/", system, path, system)
			}
		}
	}

	t.Run("consumer close", func(t *testing.T) {
		t.Parallel()
		core, funnel, _, _ := newExpressVerifyRig(t, contract.ContractPublicationFloor, []string{expressVerifyCriterionText})
		if _, err := core.verifyFor(context.Background(), cli.DataVerifyForRequest{For: expressTestWR, Record: true}); err != nil {
			t.Fatalf("verifyFor: %v", err)
		}
		assertUnder(t, "peer", funnel)
	})

	t.Run("consumer request", func(t *testing.T) {
		t.Parallel()
		core, _, _ := newRecordTestDataCore(t, []byte(`{"ok":true}`))
		core.spaceID = "test"
		core.stagingDir = t.TempDir()
		core.loadManifest = func() (space.Manifest, error) {
			return space.Manifest{MinBinaryVersion: "0.1.0", Participants: []space.Participant{{System: "axon"}, {System: "peer"}}}, nil
		}
		funnel := &expressScriptedFunnel{}
		installExpressSeams(t, core, funnel, nil)
		result, err := core.request(context.Background(), cli.DataRequestRequest{
			Producer: "axon", Contract: "XC-axon-widget@1.0.0", InterimBehavior: "keep working",
		})
		if err != nil {
			t.Fatalf("request: %v (steps %v)", err, expressStepSummary(result.Steps))
		}
		assertUnder(t, "peer", funnel)
	})

	t.Run("producer ack and accept", func(t *testing.T) {
		t.Parallel()
		core, _, _ := newRecordTestDataCore(t, []byte(`{"ok":true}`))
		core.ownSystem = "axon"
		core.spaceID = "test"
		core.stagingDir = t.TempDir()
		core.loadManifest = func() (space.Manifest, error) {
			return space.Manifest{MinBinaryVersion: "0.1.0", Participants: []space.Participant{{System: "axon"}, {System: "peer"}}}, nil
		}
		dir := filepath.Join(core.mirrorDir, "peer", "exchanges")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		const freshWR = "XW-peer-20260804-cd99"
		wr := "---\nschema: envelope/v1\nid: " + freshWR + "\ntype: work_request\ntitle: t\nspace: test\nfrom: peer\nto: [axon]\n" +
			"actor: {kind: agent, name: tester}\ncreated: 2026-08-04T12:00:00Z\ncategory: data\npriority: p3\nblocking: false\n" +
			"interim_behavior: x\nneeded_by: 2026-08-06\nthread: thread:peer-20260804-ab12\nacceptance_criteria: [\"c\"]\n" +
			"refs:\n  - ref: XC-axon-widget@1.0.0\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, freshWR+".md"), []byte(wr), 0o644); err != nil {
			t.Fatal(err)
		}
		seedExpressEvents(t, core.mirrorDir, expressSeedEvent{system: "peer", transition: "submit", subject: freshWR})
		funnel := &expressScriptedFunnel{states: []space.WriteState{space.WriteStateMerged, space.WriteStateMerged}}
		installExpressSeams(t, core, funnel, nil)
		// The pack step cannot run (--from does not exist), after ack and accept landed.
		_, err := core.deliverFor(context.Background(), cli.DataDeliverForRequest{
			For: freshWR, From: filepath.Join(t.TempDir(), "absent"), Profile: "synthetic", Format: "json",
		})
		if err == nil {
			t.Fatal("deliverFor = nil error, want the pack of a missing --from to fail")
		}
		if len(funnel.calls) != 2 {
			t.Fatalf("inner writes = %d, want ack and accept", len(funnel.calls))
		}
		assertUnder(t, "axon", funnel)
	})
}

// TestExpressDeliverResumesAfterPack is spec 05 §8 row 8: a run interrupted
// after pack and before deliver is re-run against the same --from, and the
// staged package is re-delivered (same DP id, its aggregate digest bound)
// instead of a second one being minted. A staged package whose bytes differ
// from --from is refused, naming its staging root, and so is an ambiguous
// pair of staged packages.
func TestExpressDeliverResumesAfterPack(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*dataCore, expressSnapshot, string) {
		t.Helper()
		from := t.TempDir()
		if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		core, doc, _ := newRecordTestDataCore(t, []byte(`{"ok":true}`))
		core.stagingDir = t.TempDir()
		core.spaceID = "test"
		core.ownSystem = "axon"
		schemas := map[string][]byte{dataWiringTestSchemaPath: []byte(dataWiringTestSchema)}
		core.resolveContractSchemas = func(context.Context, string) (map[string][]byte, string, error) {
			return schemas, doc.Contract, nil
		}
		seedExpressWorkRequest(t, core.mirrorDir, "peer", "axon", expressTestWR, expressTestResponse, "DP-axon-20260804-0000", []string{"c"})
		snap, err := core.expressSnapshot(expressTestWR)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		return core, snap, from
	}
	req := func(from string) cli.DataDeliverForRequest {
		return cli.DataDeliverForRequest{For: expressTestWR, From: from, Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON}
	}
	const contractRef = "XC-axon-widget@1.0.0"

	t.Run("same bytes re-deliver the staged package", func(t *testing.T) {
		t.Parallel()
		core, snap, from := setup(t)
		first := core.newExpressRun(cli.ActorFlags{}, 0)
		doc1, root1, err := first.packOrResume(context.Background(), req(from), snap, contractRef, "")
		if err != nil {
			t.Fatalf("first packOrResume: %v", err)
		}
		// The run died here: pack landed on disk, deliver never ran.
		second := core.newExpressRun(cli.ActorFlags{}, 0)
		doc2, root2, err := second.packOrResume(context.Background(), req(from), snap, contractRef, "")
		if err != nil {
			t.Fatalf("second packOrResume: %v", err)
		}
		if doc2.ID != doc1.ID || root2 != root1 || doc2.AggregateDigest != doc1.AggregateDigest {
			t.Fatalf("re-run minted %s at %s (digest %s), want the staged %s at %s (digest %s)", doc2.ID, root2, doc2.AggregateDigest, doc1.ID, root1, doc1.AggregateDigest)
		}
		matches, _ := filepath.Glob(filepath.Join(core.stagingDir, "data", "*"))
		if len(matches) != 1 {
			t.Fatalf("staging holds %d packages after the re-run, want 1: %v", len(matches), matches)
		}
		if got := expressStepSummary(second.result.Steps); len(got) != 1 || got[0] != "pack:already-done" {
			t.Fatalf("re-run steps = %v, want [pack:already-done]", got)
		}
	})

	t.Run("different bytes are refused naming the staging root", func(t *testing.T) {
		t.Parallel()
		core, snap, from := setup(t)
		first := core.newExpressRun(cli.ActorFlags{}, 0)
		_, root1, err := first.packOrResume(context.Background(), req(from), snap, contractRef, "")
		if err != nil {
			t.Fatalf("first packOrResume: %v", err)
		}
		if err := os.WriteFile(filepath.Join(from, "orders.json"), []byte(`{"ok":true,"changed":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		second := core.newExpressRun(cli.ActorFlags{}, 0)
		_, _, err = second.packOrResume(context.Background(), req(from), snap, contractRef, "")
		if err == nil || !strings.Contains(err.Error(), root1) {
			t.Fatalf("packOrResume err = %v, want a refusal naming the staging root %s", err, root1)
		}
		matches, _ := filepath.Glob(filepath.Join(core.stagingDir, "data", "*"))
		if len(matches) != 1 {
			t.Fatalf("a second package was minted despite the refusal: %v", matches)
		}
	})

	t.Run("two staged candidates are refused naming both", func(t *testing.T) {
		t.Parallel()
		core, snap, from := setup(t)
		// Two independent packs of the same request, as two crashed runs leave.
		var roots []string
		for i := 0; i < 2; i++ {
			core.now = func() time.Time { return time.Date(2026, 8, 4, 12, i, 0, 0, time.UTC) }
			core.entropy = rand.Reader
			packed, err := core.pack(context.Background(), cli.DataPackRequest{
				ContractRef: contractRef, From: from, Profile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON, Fulfills: expressTestWR,
			})
			if err != nil {
				t.Fatalf("pack %d: %v", i, err)
			}
			roots = append(roots, packed.StagingRoot)
		}
		run := core.newExpressRun(cli.ActorFlags{}, 0)
		_, _, err := run.packOrResume(context.Background(), req(from), snap, contractRef, "")
		if err == nil {
			t.Fatal("packOrResume = nil error, want an ambiguity refusal")
		}
		for _, root := range roots {
			if !strings.Contains(err.Error(), root) {
				t.Fatalf("err = %v, want it to name %s", err, root)
			}
		}
	})
}
