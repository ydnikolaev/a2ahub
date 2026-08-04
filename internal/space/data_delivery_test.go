package space

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func newTestDataDeliveryRequest(fx *spacefixture.Fixture, system string) DataDeliveryRequest {
	return DataDeliveryRequest{
		System:          system,
		PackageID:       "DP-" + system + "-20260804-ab12",
		ManifestRaw:     []byte(`{"schema":"data-package/v1","id":"DP-` + system + `-20260804-ab12"}`),
		AggregateDigest: "sha256:" + "aa11" + "0000000000000000000000000000000000000000000000000000000000",
		Payload:         map[string][]byte{"orders.json": []byte(`[{"id":1}]`)},
		HandoffID:       "XH-" + system + "-20260804-cd34",
		HandoffRaw:      []byte("---\nid: XH-" + system + "-20260804-cd34\n---\nbody\n"),
		EventID:         "01K1A2B3C4D5E6F7G8H9J0K1M9",
		EventYear:       "2026",
		EventRaw:        []byte("event: submit\n"),
		SubmitTemplate: SubmitRequest{
			RepoDir:           fx.Clone(system),
			CommitMessage:     "a2a(data-deliver): DP-" + system + "-20260804-ab12",
			CommitAuthorName:  "a2a-" + system,
			CommitAuthorEmail: "a2a-" + system + "@a2ahub.invalid",
			RemoteURL:         fx.RemoteURL(),
			Repo:              host.Repo{Owner: "acme", Name: "getvisa"},
			BaseBranch:        "main",
			PRTitle:           "Deliver DP-" + system + "-20260804-ab12",
			MinBinaryVersion:  "0.1.0",
		},
	}
}

// TestDeliverDataPackageSingleCommit is AC-3's first half: payload, manifest
// and handoff (plus its own first lifecycle event) land in exactly ONE
// commit, on the branch OperationKey derives.
func TestDeliverDataPackageSingleCommit(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := DeliverDataPackage(context.Background(), req, funnel)
	if err != nil {
		t.Fatalf("DeliverDataPackage: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	wantBranch := BranchName("axon", "data-deliver", dataDeliverOperationKey(req.PackageID))
	if result.Branch != wantBranch {
		t.Fatalf("Branch = %q, want %q", result.Branch, wantBranch)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}

	count, err := runGitOutput(context.Background(), req.SubmitTemplate.RepoDir, nil, "rev-list", "--count", "main.."+result.Branch)
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if count != "1" {
		t.Fatalf("commits ahead of main = %s, want 1", count)
	}
	changed, err := runGitOutput(context.Background(), req.SubmitTemplate.RepoDir, nil, "diff", "--name-only", "main", result.Branch)
	if err != nil {
		t.Fatalf("diff --name-only: %v", err)
	}
	want := map[string]bool{
		"axon/data/DP-axon-20260804-ab12/manifest.json":    true,
		"axon/data/DP-axon-20260804-ab12/orders.json":      true,
		"axon/exchanges/XH-axon-20260804-cd34.md":          true,
		"axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M9.yaml": true,
	}
	got := map[string]bool{}
	for _, line := range splitLinesForTest(changed) {
		got[line] = true
	}
	if len(got) != len(want) {
		t.Fatalf("changed files = %v, want exactly %v", got, want)
	}
	for path := range want {
		if !got[path] {
			t.Fatalf("changed files %v missing %q", got, path)
		}
	}
}

// TestDeliverDataPackageIdempotentRerunRepairsSamePR is AC-3's second half:
// a re-run of deliver against the SAME package repairs its own pull request
// rather than opening a second one — even though the retry mints a fresh
// HandoffID/EventID (crypto/rand.Reader-shaped entropy is never
// deterministic across invocations in production), because OperationKey is
// a pure function of PackageID alone (plan D-6).
func TestDeliverDataPackageIdempotentRerunRepairsSamePR(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := DeliverDataPackage(context.Background(), req, funnel)
	if err != nil {
		t.Fatalf("first DeliverDataPackage: %v", err)
	}
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "queued", HeadSHA: "checked-head"}, nil
	}

	// The retry: same PackageID/manifest/payload (the packed staging root
	// is immutable), but a freshly-minted, DIFFERENT handoff/event pair —
	// exactly what re-running `a2a data deliver` against the same staging
	// root does in production.
	retry := req
	retry.HandoffID = "XH-axon-20260804-zz99"
	retry.HandoffRaw = []byte("---\nid: XH-axon-20260804-zz99\n---\nbody\n")
	retry.EventID = "01K1A2B3C4D5E6F7G8H9J0K1MA"

	second, err := DeliverDataPackage(context.Background(), retry, funnel)
	if err != nil {
		t.Fatalf("second (re-run) DeliverDataPackage: %v", err)
	}
	if second.State != WriteStateAlreadyOpen {
		t.Fatalf("second State = %v, want %v", second.State, WriteStateAlreadyOpen)
	}
	if second.PRNumber != first.PRNumber || second.Branch != first.Branch {
		t.Fatalf("second = %+v, want same branch/PR as first %+v", second, first)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected NO second push/open cycle (no second PR), got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}

	// The FIRST call's handoff — never the retry's freshly-minted, never-
	// committed one — is what actually landed.
	changed, err := runGitOutput(context.Background(), req.SubmitTemplate.RepoDir, nil, "diff", "--name-only", "main", first.Branch)
	if err != nil {
		t.Fatalf("diff --name-only: %v", err)
	}
	if !containsLineForTest(changed, "axon/exchanges/XH-axon-20260804-cd34.md") {
		t.Fatalf("committed tree = %q, want the FIRST call's handoff, not the retry's", changed)
	}
	if containsLineForTest(changed, "axon/exchanges/XH-axon-20260804-zz99.md") {
		t.Fatalf("committed tree = %q, must NOT contain the retry's discarded handoff", changed)
	}
}

// TestDeliverDataPackageRefusesExpectPackMismatchBeforeAnyWrite is AC-3's
// --expect-pack binding: a caller-supplied digest that disagrees with the
// packed manifest's own AggregateDigest is refused before the funnel is
// ever touched — no push, no PR, no commit.
func TestDeliverDataPackageRefusesExpectPackMismatchBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")
	req.ExpectPack = "sha256:" + "ffff" + "0000000000000000000000000000000000000000000000000000000000"

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := DeliverDataPackage(context.Background(), req, funnel); !errors.Is(err, ErrDataDeliveryExpectPackChanged) {
		t.Fatalf("error = %v, want ErrDataDeliveryExpectPackChanged", err)
	}
	if len(fake.Pushes) != 0 || len(fake.Opens) != 0 {
		t.Fatalf("expect-pack mismatch touched the funnel: pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

func TestDeliverDataPackageAcceptsMatchingExpectPack(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")
	req.ExpectPack = req.AggregateDigest

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := DeliverDataPackage(context.Background(), req, funnel); err != nil {
		t.Fatalf("DeliverDataPackage: %v", err)
	}
}

func TestDeliverDataPackageRefusesIncompleteRequest(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	base := newTestDataDeliveryRequest(fx, "axon")
	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	for name, mutate := range map[string]func(*DataDeliveryRequest){
		"system":     func(r *DataDeliveryRequest) { r.System = "" },
		"packageID":  func(r *DataDeliveryRequest) { r.PackageID = "" },
		"manifest":   func(r *DataDeliveryRequest) { r.ManifestRaw = nil },
		"digest":     func(r *DataDeliveryRequest) { r.AggregateDigest = "" },
		"handoffID":  func(r *DataDeliveryRequest) { r.HandoffID = "" },
		"handoffRaw": func(r *DataDeliveryRequest) { r.HandoffRaw = nil },
		"eventID":    func(r *DataDeliveryRequest) { r.EventID = "" },
		"eventYear":  func(r *DataDeliveryRequest) { r.EventYear = "" },
		"eventRaw":   func(r *DataDeliveryRequest) { r.EventRaw = nil },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			if _, err := DeliverDataPackage(context.Background(), req, funnel); !errors.Is(err, ErrDataDeliveryInvalid) {
				t.Fatalf("%s: error = %v, want ErrDataDeliveryInvalid", name, err)
			}
		})
	}
	if len(fake.Pushes) != 0 {
		t.Fatalf("an invalid request reached the funnel: pushes=%d", len(fake.Pushes))
	}
}

func TestDeliverDataPackageRefusesUnsafePayloadPath(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")
	req.Payload = map[string][]byte{"../escape.json": []byte("{}")}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := DeliverDataPackage(context.Background(), req, funnel); !errors.Is(err, ErrDataDeliveryInvalid) {
		t.Fatalf("error = %v, want ErrDataDeliveryInvalid", err)
	}
	if len(fake.Pushes) != 0 {
		t.Fatalf("unsafe payload path reached the funnel: pushes=%d", len(fake.Pushes))
	}
}

func TestDataDeliverOperationKeyIsPackageIDOnly(t *testing.T) {
	t.Parallel()

	if !operationKeyValid(dataDeliverOperationKey("DP-axon-20260804-ab12")) {
		t.Fatalf("dataDeliverOperationKey does not produce a valid op-v1 key")
	}
	firstKey := dataDeliverOperationKey("DP-axon-20260804-ab12")
	secondKey := dataDeliverOperationKey("DP-axon-20260804-ab12")
	if firstKey != secondKey {
		t.Fatalf("dataDeliverOperationKey is not deterministic")
	}
	if dataDeliverOperationKey("DP-axon-20260804-ab12") == dataDeliverOperationKey("DP-axon-20260804-zz99") {
		t.Fatalf("dataDeliverOperationKey collided across two distinct package ids")
	}
}

func newTestDataVerificationRecordRequest(fx *spacefixture.Fixture, system string) DataVerificationRecordRequest {
	return DataVerificationRecordRequest{
		System:     system,
		PackageID:  "DP-axon-20260804-ab12",
		ReportID:   "VR-" + system + "-20260804-ef56",
		ReportRaw:  []byte(`{"schema":"verification-report/v1","id":"VR-` + system + `-20260804-ef56"}`),
		HandoffID:  "XH-axon-20260804-cd34",
		Transition: fold.TVerifyPass,
		EventID:    "01K1A2B3C4D5E6F7G8H9J0K1MZ",
		EventYear:  "2026",
		EventRaw:   []byte("event: verify-pass\n"),
		SubmitTemplate: SubmitRequest{
			RepoDir:           fx.Clone(system),
			CommitMessage:     "a2a(verify-pass): XH-axon-20260804-cd34",
			CommitAuthorName:  "a2a-" + system,
			CommitAuthorEmail: "a2a-" + system + "@a2ahub.invalid",
			RemoteURL:         fx.RemoteURL(),
			Repo:              host.Repo{Owner: "acme", Name: "getvisa"},
			BaseBranch:        "main",
			PRTitle:           "Verify-pass XH-axon-20260804-cd34",
			MinBinaryVersion:  "0.1.0",
		},
	}
}

// TestRecordVerificationReportSingleCommit is AC-5's write-half: the report
// and its one lifecycle event land in exactly ONE commit, stored beside a
// copy of the package's own "data/<id>/" shape rooted at the VERIFYING
// system's own section (dataPackageReportPath's own doc comment).
func TestRecordVerificationReportSingleCommit(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "peer")
	req := newTestDataVerificationRecordRequest(fx, "peer")

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := RecordVerificationReport(context.Background(), req, funnel)
	if err != nil {
		t.Fatalf("RecordVerificationReport: %v", err)
	}
	if result.State != WriteStatePendingMerge {
		t.Fatalf("State = %v, want %v", result.State, WriteStatePendingMerge)
	}
	wantBranch := BranchName("peer", "data-verify", dataVerifyOperationKey(req.ReportID))
	if result.Branch != wantBranch {
		t.Fatalf("Branch = %q, want %q", result.Branch, wantBranch)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected exactly 1 push + 1 open, got %d/%d", len(fake.Pushes), len(fake.Opens))
	}

	count, err := runGitOutput(context.Background(), req.SubmitTemplate.RepoDir, nil, "rev-list", "--count", "main.."+result.Branch)
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if count != "1" {
		t.Fatalf("commits ahead of main = %s, want 1", count)
	}
	changed, err := runGitOutput(context.Background(), req.SubmitTemplate.RepoDir, nil, "diff", "--name-only", "main", result.Branch)
	if err != nil {
		t.Fatalf("diff --name-only: %v", err)
	}
	want := map[string]bool{
		"peer/data/DP-axon-20260804-ab12/report.json":      true,
		"peer/events/2026/01K1A2B3C4D5E6F7G8H9J0K1MZ.yaml": true,
	}
	got := map[string]bool{}
	for _, line := range splitLinesForTest(changed) {
		got[line] = true
	}
	if len(got) != len(want) {
		t.Fatalf("changed files = %v, want exactly %v", got, want)
	}
	for path := range want {
		if !got[path] {
			t.Fatalf("changed files %v missing %q", got, path)
		}
	}
}

// TestRecordVerificationReportIdempotentRerunRepairsSamePR is the write-half
// of "re-running --record repairs its own pull request rather than opening a
// second one" — proven here at the space layer exactly as
// TestDeliverDataPackageIdempotentRerunRepairsSamePR proves it for deliver: a
// retry with a freshly-minted, DIFFERENT EventID (never deterministic across
// invocations in production) still collides, because OperationKey is a pure
// function of ReportID alone.
func TestRecordVerificationReportIdempotentRerunRepairsSamePR(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "peer")
	req := newTestDataVerificationRecordRequest(fx, "peer")

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	first, err := RecordVerificationReport(context.Background(), req, funnel)
	if err != nil {
		t.Fatalf("first RecordVerificationReport: %v", err)
	}
	fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "queued", HeadSHA: "checked-head"}, nil
	}

	retry := req
	retry.EventID = "01K1A2B3C4D5E6F7G8H9J0K1MY"
	retry.EventRaw = []byte("event: verify-pass (retry)\n")

	second, err := RecordVerificationReport(context.Background(), retry, funnel)
	if err != nil {
		t.Fatalf("second (re-run) RecordVerificationReport: %v", err)
	}
	if second.State != WriteStateAlreadyOpen {
		t.Fatalf("second State = %v, want %v", second.State, WriteStateAlreadyOpen)
	}
	if second.PRNumber != first.PRNumber || second.Branch != first.Branch {
		t.Fatalf("second = %+v, want same branch/PR as first %+v", second, first)
	}
	if len(fake.Pushes) != 1 || len(fake.Opens) != 1 {
		t.Fatalf("expected NO second push/open cycle (no second PR), got pushes=%d opens=%d", len(fake.Pushes), len(fake.Opens))
	}
}

// TestRecordVerificationReportRefusesUnknownTransition is the seeded-red
// receipt for D-12's own closed set at THIS layer: a Transition outside
// {verify-pass, verify-fail} is refused before the funnel is ever touched.
func TestRecordVerificationReportRefusesUnknownTransition(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "peer")
	req := newTestDataVerificationRecordRequest(fx, "peer")
	req.Transition = "verify-forced-pass"

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := RecordVerificationReport(context.Background(), req, funnel); !errors.Is(err, ErrDataVerificationRecordInvalid) {
		t.Fatalf("error = %v, want ErrDataVerificationRecordInvalid", err)
	}
	if len(fake.Pushes) != 0 {
		t.Fatalf("an invalid transition reached the funnel: pushes=%d", len(fake.Pushes))
	}
}

// TestRecordVerificationReportAcceptsVerifyFail proves the write side never
// refuses the FAIL direction — spec 05a's own gap this wave closes: W3
// shipped no verify-fail path at all.
func TestRecordVerificationReportAcceptsVerifyFail(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "peer")
	req := newTestDataVerificationRecordRequest(fx, "peer")
	req.Transition = fold.TVerifyFail
	req.EventRaw = []byte("event: verify-fail\n")

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := RecordVerificationReport(context.Background(), req, funnel); err != nil {
		t.Fatalf("RecordVerificationReport(verify-fail): %v", err)
	}
}

func TestRecordVerificationReportRefusesIncompleteRequest(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "peer")
	base := newTestDataVerificationRecordRequest(fx, "peer")
	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	for name, mutate := range map[string]func(*DataVerificationRecordRequest){
		"system":    func(r *DataVerificationRecordRequest) { r.System = "" },
		"packageID": func(r *DataVerificationRecordRequest) { r.PackageID = "" },
		"reportID":  func(r *DataVerificationRecordRequest) { r.ReportID = "" },
		"reportRaw": func(r *DataVerificationRecordRequest) { r.ReportRaw = nil },
		"handoffID": func(r *DataVerificationRecordRequest) { r.HandoffID = "" },
		"eventID":   func(r *DataVerificationRecordRequest) { r.EventID = "" },
		"eventYear": func(r *DataVerificationRecordRequest) { r.EventYear = "" },
		"eventRaw":  func(r *DataVerificationRecordRequest) { r.EventRaw = nil },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			if _, err := RecordVerificationReport(context.Background(), req, funnel); !errors.Is(err, ErrDataVerificationRecordInvalid) {
				t.Fatalf("%s: error = %v, want ErrDataVerificationRecordInvalid", name, err)
			}
		})
	}
	if len(fake.Pushes) != 0 {
		t.Fatalf("an invalid request reached the funnel: pushes=%d", len(fake.Pushes))
	}
}

func TestDataVerifyOperationKeyIsReportIDOnly(t *testing.T) {
	t.Parallel()

	if !operationKeyValid(dataVerifyOperationKey("VR-axon-20260804-ab12")) {
		t.Fatalf("dataVerifyOperationKey does not produce a valid op-v1 key")
	}
	firstVerifyKey := dataVerifyOperationKey("VR-axon-20260804-ab12")
	secondVerifyKey := dataVerifyOperationKey("VR-axon-20260804-ab12")
	if firstVerifyKey != secondVerifyKey {
		t.Fatalf("dataVerifyOperationKey is not deterministic")
	}
	if dataVerifyOperationKey("VR-axon-20260804-ab12") == dataVerifyOperationKey("VR-axon-20260804-zz99") {
		t.Fatalf("dataVerifyOperationKey collided across two distinct report ids")
	}
	if dataVerifyOperationKey("same-id") == dataDeliverOperationKey("same-id") {
		t.Fatalf("dataVerifyOperationKey collided with dataDeliverOperationKey on the same input (domain separation broken)")
	}
}

func splitLinesForTest(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func containsLineForTest(s, want string) bool {
	for _, line := range splitLinesForTest(s) {
		if line == want {
			return true
		}
	}
	return false
}

func operationKeyValid(key string) bool {
	const prefix = "op-v1-"
	if len(key) != len(prefix)+64 || key[:len(prefix)] != prefix {
		return false
	}
	for _, c := range key[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
