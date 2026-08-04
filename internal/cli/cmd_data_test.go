package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// fakeDataOperations is the cmd_contract_p6_test.go fake idiom applied to
// the one unified DataOperations seam: every call is recorded and answers
// with whatever the test staged.
type fakeDataOperations struct {
	packReq    cli.DataPackRequest
	deliverReq cli.DataDeliverRequest
	fetchReq   cli.DataFetchRequest
	verifyReq  cli.DataVerifyRequest

	result DataResultStub
	err    error
}

// DataResultStub avoids importing the same type twice under two names —
// it IS cli.DataResult, aliased only so this file's fixtures read fluently.
type DataResultStub = cli.DataResult

func (f *fakeDataOperations) Pack(_ context.Context, req cli.DataPackRequest) (cli.DataResult, error) {
	f.packReq = req
	return f.result, f.err
}

func (f *fakeDataOperations) Deliver(_ context.Context, req cli.DataDeliverRequest) (cli.DataResult, error) {
	f.deliverReq = req
	return f.result, f.err
}

func (f *fakeDataOperations) Fetch(_ context.Context, req cli.DataFetchRequest) (cli.DataResult, error) {
	f.fetchReq = req
	return f.result, f.err
}

func (f *fakeDataOperations) Verify(_ context.Context, req cli.DataVerifyRequest) (cli.DataResult, error) {
	f.verifyReq = req
	return f.result, f.err
}

var _ cli.DataOperations = (*fakeDataOperations)(nil)

func runDataCommand(t *testing.T, cmd *cli.DataCommand, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmd.Run(t.Context(), args, cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

func sampleManifest() *datapackage.Document {
	records := int64(42)
	return &datapackage.Document{
		Schema: "data-package/v1", ID: "DP-axon-20260804-ab12", Thread: "thread-1",
		Contract: "XC-axon-orders@1.0.0#sha256:contractdigest", Mode: datapackage.ModeSnapshot,
		Classification: "internal", DataProfile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
		TransportDriver: datapackage.TransportDriverSpaceGit, Locator: "deliveries/DP-axon-20260804-ab12",
		Entries: []datapackage.Entry{
			{Path: "orders.json", Role: datapackage.RoleDataset, MediaType: "application/json", ConformsTo: "schema/orders.json", SizeBytes: 1024, Digest: "sha256:entrydigest", RecordCount: &records},
		},
		AggregateDigest: "sha256:aggregatedigest", SizeBytes: 1024, RecordCount: 42,
		CreatedAt: "2026-08-04T00:00:00Z", ExpiresAt: "2026-08-11T00:00:00Z", Attempt: 1,
		Provenance: datapackage.Provenance{OriginSystem: "axon", ExtractedAt: "2026-08-04T00:00:00Z"},
	}
}

func TestDataPackTextAndJSON(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{result: cli.DataResult{Manifest: sampleManifest()}}
	cmd := cli.NewDataCommand(fake)

	code, output, stderr := runDataCommand(t, cmd,
		"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders",
		"--profile", "synthetic", "--format", "json", "--expires", "168h")
	if code != 0 || stderr != "" {
		t.Fatalf("pack: code=%d out=%q err=%q", code, output, stderr)
	}
	if fake.packReq.ContractRef != "XC-axon-orders@1.0.0" || fake.packReq.From != "staging/orders" ||
		fake.packReq.Profile != "synthetic" || fake.packReq.Format != "json" {
		t.Fatalf("pack request not forwarded thin: %+v", fake.packReq)
	}
	if !strings.Contains(output, "orders.json") || !strings.Contains(output, "digest=sha256:entrydigest") || !strings.Contains(output, "records=42") {
		t.Fatalf("pack text output missing entry preview: %q", output)
	}
	if !strings.Contains(output, "packed DP-axon-20260804-ab12") {
		t.Fatalf("pack text output missing summary line: %q", output)
	}

	code, output, stderr = runDataCommand(t, cmd,
		"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders",
		"--profile", "synthetic", "--format", "json", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("pack --json: code=%d out=%q err=%q", code, output, stderr)
	}
	var got cli.DataResult
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.Manifest == nil || got.Manifest.ID != "DP-axon-20260804-ab12" {
		t.Fatalf("pack JSON mismatch: err=%v out=%q", err, output)
	}
}

func TestDataPackUsageErrors(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{result: cli.DataResult{Manifest: sampleManifest()}}
	cmd := cli.NewDataCommand(fake)
	for _, args := range [][]string{
		{"pack"},
		{"pack", "--contract", "XC-axon-orders@1.0.0"},
		{"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders", "--profile", "synthetic"},
	} {
		code, _, _ := runDataCommand(t, cmd, args...)
		if code != 2 {
			t.Fatalf("args %v: got exit %d, want usage exit 2", args, code)
		}
	}
}

// TestDataPackProfileEnumIsTheCoresJob proves --profile/--format are
// forwarded verbatim rather than validated in this thin frontend (ADR-001):
// "production" is syntactically a fine string, and ErrProductionProfile
// (spec 05a §T2.4, AC-2) is the CORE's own named refusal, reached only if
// the CLI does not intercept it with a generic usage error first.
func TestDataPackProfileEnumIsTheCoresJob(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{err: fmt.Errorf("datapackage: Pack: %w", datapackage.ErrProductionProfile)}
	cmd := cli.NewDataCommand(fake)
	code, _, stderr := runDataCommand(t, cmd,
		"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders",
		"--profile", "production", "--format", "json")
	if code != 1 || fake.packReq.Profile != "production" || !strings.Contains(stderr, "data_profile production is refused") {
		t.Fatalf("code=%d req=%+v stderr=%q", code, fake.packReq, stderr)
	}
}

// TestDataPackRefusalNamesValueAndPathInBothModes is this wave's own
// acceptance line: a refusal must render usefully in BOTH modes, naming the
// offending value/path and the record number when an ndjson record is at
// fault. internal/datapackage's own error wrapping already composes that
// text (pack.go's describeFailingCheck); this proves the CLI carries it
// through unmodified in text (stderr) AND --json (stdout envelope).
func TestDataPackRefusalNamesValueAndPathInBothModes(t *testing.T) {
	t.Parallel()
	record := int64(4108)
	refusal := fmt.Errorf("datapackage: Pack: %w: entry %q (check %q, status fail) record %d: value is not an integer",
		datapackage.ErrEntryNonConforming, "orders.ndjson", "schema-check", record)
	fake := &fakeDataOperations{err: refusal}
	cmd := cli.NewDataCommand(fake)

	code, _, stderr := runDataCommand(t, cmd,
		"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders",
		"--profile", "synthetic", "--format", "ndjson")
	if code != 1 || !strings.Contains(stderr, "orders.ndjson") || !strings.Contains(stderr, "record 4108") {
		t.Fatalf("pack text refusal missing value/path/record: code=%d stderr=%q", code, stderr)
	}

	code, output, stderr := runDataCommand(t, cmd,
		"pack", "--contract", "XC-axon-orders@1.0.0", "--from", "staging/orders",
		"--profile", "synthetic", "--format", "ndjson", "--json")
	if code != 1 {
		t.Fatalf("pack --json refusal: got exit %d", code)
	}
	if !strings.Contains(stderr, "orders.ndjson") || !strings.Contains(stderr, "record 4108") {
		t.Fatalf("pack --json refusal must still name value/path/record on stderr: %q", stderr)
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("pack --json refusal did not emit a JSON envelope on stdout: err=%v out=%q", err, output)
	}
	if !strings.Contains(envelope.Error, "orders.ndjson") || !strings.Contains(envelope.Error, "record 4108") {
		t.Fatalf("pack --json refusal envelope missing value/path/record: %q", envelope.Error)
	}
}

func TestDataDeliverIsThin(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{result: cli.DataResult{
		Manifest:  sampleManifest(),
		HandoffID: "XH-axon-20260804-cd34",
		Write:     &space.WriteResult{Branch: "a2a/data-deliver", PRURL: "https://github.com/axon/space/pull/9", State: space.WriteStatePendingMerge},
	}}
	cmd := cli.NewDataCommand(fake)

	code, output, stderr := runDataCommand(t, cmd,
		"deliver", "staging/packed-attempt-1", "--fulfills", "XW-axon-20260804-ef56")
	if code != 0 || stderr != "" {
		t.Fatalf("deliver: code=%d out=%q err=%q", code, output, stderr)
	}
	if fake.deliverReq.StagingRoot != "staging/packed-attempt-1" || fake.deliverReq.Fulfills != "XW-axon-20260804-ef56" {
		t.Fatalf("deliver request not forwarded thin: %+v", fake.deliverReq)
	}
	if !strings.Contains(output, "delivered DP-axon-20260804-ab12 handoff XH-axon-20260804-cd34") {
		t.Fatalf("deliver text missing package/handoff line: %q", output)
	}
	if !strings.Contains(output, "https://github.com/axon/space/pull/9") {
		t.Fatalf("deliver text missing PR line: %q", output)
	}

	code, _, stderr = runDataCommand(t, cmd, "deliver", "staging/packed-attempt-1")
	if code != 2 {
		t.Fatalf("deliver without --fulfills: got exit %d, want usage exit 2, stderr=%q", code, stderr)
	}
}

func TestDataFetchUsageAndOutcome(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{result: cli.DataResult{Manifest: sampleManifest(), Outcome: "installed"}}
	cmd := cli.NewDataCommand(fake)

	code, _, stderr := runDataCommand(t, cmd, "fetch", "not-a-package-id", "--to", "vendor/orders")
	if code != 2 {
		t.Fatalf("fetch with a non DP- ref: got exit %d, want usage exit 2, stderr=%q", code, stderr)
	}

	code, output, stderr := runDataCommand(t, cmd, "fetch", "DP-axon-20260804-ab12", "--to", "vendor/orders", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("fetch: code=%d out=%q err=%q", code, output, stderr)
	}
	if fake.fetchReq.PackageID != "DP-axon-20260804-ab12" || fake.fetchReq.Destination != "vendor/orders" {
		t.Fatalf("fetch request not forwarded thin: %+v", fake.fetchReq)
	}
	if !strings.Contains(output, `"outcome":"installed"`) {
		t.Fatalf("fetch JSON missing outcome: %q", output)
	}
}

// TestDataFetchRefusalNamesDigestMismatch proves AC-4's own message
// requirement survives the CLI layer unmodified.
func TestDataFetchRefusalNamesDigestMismatch(t *testing.T) {
	t.Parallel()
	refusal := fmt.Errorf("datapackage: Fetch: %w: manifest aggregate_digest sha256:aaa, verified sha256:bbb", datapackage.ErrDigestMismatch)
	fake := &fakeDataOperations{err: refusal}
	cmd := cli.NewDataCommand(fake)

	code, _, stderr := runDataCommand(t, cmd, "fetch", "DP-axon-20260804-ab12", "--to", "vendor/orders")
	if code != 1 || !strings.Contains(stderr, "sha256:aaa") || !strings.Contains(stderr, "sha256:bbb") {
		t.Fatalf("fetch refusal missing digests: code=%d stderr=%q", code, stderr)
	}
}

func sampleFailingReport() *datapackage.Report {
	record := int64(4108)
	report, err := datapackage.NewReport(
		"VR-axon-20260804-gh78", "thread-1",
		datapackage.PackageRef{ID: "DP-axon-20260804-ab12", AggregateDigest: "sha256:aggregatedigest"},
		"XC-axon-orders@1.0.0#sha256:contractdigest", "beta",
		[]datapackage.Check{datapackage.NewCheck("schema-check", "orders.ndjson", datapackage.CheckFail, []datapackage.InstanceViolation{
			{InstancePointer: "/amount", SchemaPointer: "/properties/amount/type", Message: "value is not an integer", Record: &record},
		})},
		datapackage.Observed{RecordCount: 41, SizeBytes: 1024, DurationMS: 12},
		"2026-08-04T01:00:00Z", "2026-08-04T01:00:01Z",
	)
	if err != nil {
		panic(err)
	}
	return &report
}

func TestDataVerifyRendersVerdictAndExitsNonZeroOnFail(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{result: cli.DataResult{Report: sampleFailingReport()}}
	cmd := cli.NewDataCommand(fake)

	code, output, stderr := runDataCommand(t, cmd, "verify", "DP-axon-20260804-ab12")
	if code != 1 || stderr != "" {
		t.Fatalf("verify: code=%d out=%q err=%q", code, output, stderr)
	}
	if !strings.Contains(output, "orders.ndjson") || !strings.Contains(output, "record 4108") {
		t.Fatalf("verify text missing failing entry/record: %q", output)
	}
	if fake.verifyReq.PackageID != "DP-axon-20260804-ab12" || fake.verifyReq.Record {
		t.Fatalf("verify request not forwarded thin: %+v", fake.verifyReq)
	}

	code, output, _ = runDataCommand(t, cmd, "verify", "DP-axon-20260804-ab12", "--record", "--json")
	if code != 1 {
		t.Fatalf("verify --json fail: got exit %d", code)
	}
	if !strings.Contains(output, "record") || !strings.Contains(output, "4108") {
		t.Fatalf("verify --json missing record number: %q", output)
	}
	if !fake.verifyReq.Record {
		t.Fatalf("verify --record was not forwarded")
	}
}

// TestDataVerifyHasNoPassFlag is D-12's own seam-level receipt: --pass no
// longer exists as a way to select the transition direction (spec 05a plan
// D-12: "There is no --pass, because there is nothing for a caller to
// choose") — a caller that still types it gets a usage error, not a
// silently ignored flag.
func TestDataVerifyHasNoPassFlag(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{result: cli.DataResult{Report: sampleFailingReport()}}
	cmd := cli.NewDataCommand(fake)

	code, _, stderr := runDataCommand(t, cmd, "verify", "DP-axon-20260804-ab12", "--pass")
	if code != 2 {
		t.Fatalf("verify --pass: got exit %d, want usage exit 2 (the flag must not exist), stderr=%q", code, stderr)
	}
}

// TestDataVerifyRecordRendersTheWrite proves --record's own write (branch/
// PR) reaches the operator in text mode, the same shape `data deliver`
// already renders.
func TestDataVerifyRecordRendersTheWrite(t *testing.T) {
	t.Parallel()
	report := sampleFailingReport()
	fake := &fakeDataOperations{result: cli.DataResult{
		Report:    report,
		HandoffID: "XH-axon-20260804-cd34",
		Write:     &space.WriteResult{Branch: "a2a/peer/data-verify/op-v1-abc", PRURL: "https://github.com/axon/space/pull/11", State: space.WriteStatePendingMerge},
	}}
	cmd := cli.NewDataCommand(fake)

	code, output, stderr := runDataCommand(t, cmd, "verify", "DP-axon-20260804-ab12", "--record")
	if code != 1 || stderr != "" {
		t.Fatalf("verify --record: code=%d out=%q err=%q", code, output, stderr)
	}
	if !fake.verifyReq.Record {
		t.Fatalf("verify --record was not forwarded: %+v", fake.verifyReq)
	}
	if !strings.Contains(output, "https://github.com/axon/space/pull/11") {
		t.Fatalf("verify --record text missing the write's PR line: %q", output)
	}
}

func TestDataServiceUnavailableForEverySubverb(t *testing.T) {
	t.Parallel()
	cmd := cli.NewDataCommand(nil)
	for _, args := range [][]string{
		{"pack", "--contract", "XC-a@1.0.0", "--from", "d", "--profile", "synthetic", "--format", "json"},
		{"deliver", "staging", "--fulfills", "XW-a-20260804-aaaa"},
		{"fetch", "DP-a-20260804-aaaa", "--to", "d"},
		{"verify", "DP-a-20260804-aaaa"},
	} {
		code, _, stderr := runDataCommand(t, cmd, args...)
		if code != 1 || !strings.Contains(stderr, "service is not configured") {
			t.Fatalf("args %v: got code=%d stderr=%q, want a 'service is not configured' refusal", args, code, stderr)
		}
	}
}

func TestDataHelpListsEverySubcommand(t *testing.T) {
	t.Parallel()
	cmd := cli.NewDataCommand(&fakeDataOperations{})
	code, output, stderr := runDataCommand(t, cmd, "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("data --help: code=%d out=%q err=%q", code, output, stderr)
	}
	for _, sub := range cli.DataSubcommands() {
		if !strings.Contains(output, sub.Name) {
			t.Fatalf("data --help missing sub-verb %q: %q", sub.Name, output)
		}
	}
}

func TestDataUnknownSubcommand(t *testing.T) {
	t.Parallel()
	cmd := cli.NewDataCommand(&fakeDataOperations{})
	code, _, stderr := runDataCommand(t, cmd, "teleport")
	if code != 2 || !strings.Contains(stderr, `unknown subcommand "teleport"`) {
		t.Fatalf("got code=%d stderr=%q", code, stderr)
	}
}

// TestDataSubcommandsMatchRunSwitch is the SSOT/switch consistency
// receipt this wave's brief requires: every name DataSubcommands() lists
// must be a real case in DataCommand.Run's own bare switch. A bare
// invocation of a real sub-verb always hits a usage error (exit 2, no
// positionals/flags satisfied) — never "unknown subcommand". Proven to
// fail without its guard: temporarily removing a `case` from the switch
// (or adding a name here with none there) turns exactly this test red —
// see this wave's own report for the seeded-red receipt.
func TestDataSubcommandsMatchRunSwitch(t *testing.T) {
	t.Parallel()
	for _, sub := range cli.DataSubcommands() {
		t.Run(sub.Name, func(t *testing.T) {
			t.Parallel()
			cmd := cli.NewDataCommand(&fakeDataOperations{})
			code, _, stderr := runDataCommand(t, cmd, sub.Name)
			if code != 2 {
				t.Fatalf("data %s: got exit %d, want usage exit 2 (bare invocation)", sub.Name, code)
			}
			if strings.Contains(stderr, "unknown subcommand") {
				t.Fatalf("data %s: DataSubcommands() names a sub-verb the Run switch does not dispatch: %s", sub.Name, stderr)
			}
		})
	}
}

// TestDataDeliverRefusesSupersedesRatherThanIgnoringIt pins the correction of
// a flag that used to be accepted and silently do nothing.
//
// Supersession is baked into the manifest at pack time; deliver only ships
// what pack produced, so it cannot honour --supersedes. Ignoring it was the
// worst available option: an agent that set it there — the intuitive place —
// would believe the chain was recorded, and would find out one feedback cycle
// later that attempt 2 claims to be attempt 1.
func TestDataDeliverRefusesSupersedesRatherThanIgnoringIt(t *testing.T) {
	t.Parallel()
	fake := &fakeDataOperations{}
	cmd := cli.NewDataCommand(fake)

	code, _, stderr := runDataCommand(t, cmd,
		"deliver", "staging/packed-attempt-2", "--fulfills", "XW-axon-20260804-ef56",
		"--supersedes", "DP-axon-20260803-zz99")
	if code == 0 {
		t.Fatalf("deliver --supersedes = exit 0, want a refusal")
	}
	if !strings.Contains(stderr, "set at pack time") {
		t.Fatalf("refusal does not say where supersession belongs: %q", stderr)
	}
	if fake.deliverReq.StagingRoot != "" {
		t.Fatalf("deliver reached the core despite refusing: %+v", fake.deliverReq)
	}
}
