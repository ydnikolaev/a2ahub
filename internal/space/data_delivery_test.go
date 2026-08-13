package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func newTestDataDeliveryRequest(fx *spacefixture.Fixture, system string) DataDeliveryRequest {
	return DataDeliveryRequest{
		System:    system,
		PackageID: "DP-" + system + "-20260804-ab12",
		// `transport_driver` is present because data-package/v1's schema
		// REQUIRES it and every real manifest carries it — this fixture
		// omitted it, and DeliverDataPackage now reads the field to look up
		// which driver moves the bytes, so the omission became visible.
		ManifestRaw:     []byte(`{"schema":"data-package/v1","id":"DP-` + system + `-20260804-ab12","transport_driver":"space-git"}`),
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

// TestDeliverDataPackageCommittedContentMatchesRequestBytes is the
// content-level half of "the committed file set is unchanged": it exercises
// a MULTI-entry payload (TestDeliverDataPackageSingleCommit's own fixture
// carries only one) so the refactor's actual risk surface — reassembling
// spaceGitDriver.Put's returned map[string][]byte back into FileWrite
// entries — is exercised on more than a single key, and asserts every
// committed blob's bytes are byte-identical to what the request carried,
// not merely that its path appeared.
func TestDeliverDataPackageCommittedContentMatchesRequestBytes(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")
	req.Payload = map[string][]byte{
		"orders.json":       []byte(`[{"id":1}]`),
		"customers.json":    []byte(`[{"id":2}]`),
		"nested/index.json": []byte(`{"count":2}`),
	}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	result, err := DeliverDataPackage(context.Background(), req, funnel)
	if err != nil {
		t.Fatalf("DeliverDataPackage: %v", err)
	}

	root := "axon/data/DP-axon-20260804-ab12/"
	wantBlobs := map[string][]byte{
		root + "manifest.json":                             req.ManifestRaw,
		root + "orders.json":                               req.Payload["orders.json"],
		root + "customers.json":                            req.Payload["customers.json"],
		root + "nested/index.json":                         req.Payload["nested/index.json"],
		"axon/exchanges/XH-axon-20260804-cd34.md":          req.HandoffRaw,
		"axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M9.yaml": req.EventRaw,
	}
	for blobPath, want := range wantBlobs {
		got, err := runGitOutput(context.Background(), req.SubmitTemplate.RepoDir, nil, "show", result.Branch+":"+blobPath)
		if err != nil {
			t.Fatalf("show %s: %v", blobPath, err)
		}
		// runGitOutput trims surrounding whitespace off git's own stdout, so
		// a single trailing newline in the committed blob (several of this
		// test's own fixture values end in one) is trimmed on both sides
		// rather than tripping this assertion on something runGitOutput
		// itself already does uniformly, not something DeliverDataPackage
		// changed.
		if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
			t.Fatalf("committed %s = %q, want %q", blobPath, got, string(want))
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

// TestDeliverDataPackageRefusesPayloadNamedManifest is the seeded-red
// receipt for the collision DeliverDataPackage's own doc comment names:
// merging payload entries and the manifest into ONE map, keyed by relative
// name, means a payload entry literally called "manifest.json" would
// otherwise silently overwrite (or be overwritten by) the real manifest
// instead of being caught the way two duplicate-path FileWrite entries used
// to be caught deep inside funnel.Submit's own normalizeMutations
// (ErrMutationDuplicatePath). Removing this function's own guard reproduces
// that silent loss: the test would then pass a corrupted delivery (either
// the manifest bytes or the payload entry's own bytes silently discarded)
// rather than failing loudly, so it is verified to fail with the guard
// removed before being trusted here — recorded in this file's own report,
// not left to be re-discovered.
func TestDeliverDataPackageRefusesPayloadNamedManifest(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")
	req.Payload = map[string][]byte{"manifest.json": []byte(`{"attacker":true}`)}

	fake := host.NewFakeHost()
	funnel := NewWriteFunnel(fake, nil, "0.1.0")

	if _, err := DeliverDataPackage(context.Background(), req, funnel); !errors.Is(err, ErrDataDeliveryInvalid) {
		t.Fatalf("error = %v, want ErrDataDeliveryInvalid", err)
	}
	if len(fake.Pushes) != 0 {
		t.Fatalf("a payload entry colliding with the manifest name reached the funnel: pushes=%d", len(fake.Pushes))
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

// TestDataPackageForPath is AC8's predicate-level proof: the path grammar
// DeliverDataPackage actually writes (dataPackageDir's own
// "<system>/data/<DP-id>/...") is recognised, and nothing that merely looks
// similar is — mirroring TestContractForPath's own shape for the sibling
// predicate this one is built to match (layout.go:117).
func TestDataPackageForPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		wantID       string
		wantManifest string
		wantOK       bool
	}{
		{
			name:         "the manifest itself",
			path:         "seomatrix/data/DP-seomatrix-20260808-7xaa/manifest.json",
			wantID:       "DP-seomatrix-20260808-7xaa",
			wantManifest: "seomatrix/data/DP-seomatrix-20260808-7xaa/manifest.json",
			wantOK:       true,
		},
		{
			name:         "the packed README — the exact incident shape",
			path:         "seomatrix/data/DP-seomatrix-20260808-7xaa/README.md",
			wantID:       "DP-seomatrix-20260808-7xaa",
			wantManifest: "seomatrix/data/DP-seomatrix-20260808-7xaa/manifest.json",
			wantOK:       true,
		},
		{
			name:         "a nested payload file",
			path:         "seomatrix/data/DP-seomatrix-20260808-7xaa/rows/orders.json",
			wantID:       "DP-seomatrix-20260808-7xaa",
			wantManifest: "seomatrix/data/DP-seomatrix-20260808-7xaa/manifest.json",
			wantOK:       true,
		},
		{
			name:   "a genuine artifact elsewhere under the same system — must NOT match",
			path:   "seomatrix/exchanges/XW-seomatrix-20260808-ab12.md",
			wantOK: false,
		},
		{
			name:   "a contract path — the sibling predicate's own territory",
			path:   "seomatrix/provides/widgets/schema/widget.json",
			wantOK: false,
		},
		{
			name:   "no data segment",
			path:   "seomatrix/README.md",
			wantOK: false,
		},
		{
			// A valid DP- id at the right DEPTH, but under a directory
			// literally named anything other than "data" — discriminates the
			// parts[1] == "data" guard from the len(parts) floor alone,
			// which a shorter "no data segment" case above cannot.
			name:   "a DP- id shaped path under a directory that is not literally \"data\"",
			path:   "seomatrix/notdata/DP-seomatrix-20260808-7xaa/README.md",
			wantOK: false,
		},
		{
			name:   "the package directory has no trailing file",
			path:   "seomatrix/data/DP-seomatrix-20260808-7xaa",
			wantOK: false,
		},
		{
			name:   "not a DP- id at all",
			path:   "seomatrix/data/XH-seomatrix-20260808-7xaa/README.md",
			wantOK: false,
		},
		{
			// dataPackageReportPath (this file) roots a verification report
			// under the VERIFYING system's own section while the packageID
			// still names the PRODUCER — TestRecordVerificationReportSingleCommit
			// commits exactly this shape ("peer/data/DP-axon-.../report.json").
			// A same-system check here would disagree with that real write.
			name:         "a DP- id whose OWN embedded system disagrees with the directory it sits under — the real verify-report shape",
			path:         "peer/data/DP-axon-20260804-ab12/report.json",
			wantID:       "DP-axon-20260804-ab12",
			wantManifest: "peer/data/DP-axon-20260804-ab12/manifest.json",
			wantOK:       true,
		},
		{
			name:   "path traversal",
			path:   "seomatrix/data/../DP-seomatrix-20260808-7xaa/README.md",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, manifest, ok := DataPackageForPath(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("DataPackageForPath(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if id != tt.wantID {
				t.Fatalf("DataPackageForPath(%q) id = %q, want %q", tt.path, id, tt.wantID)
			}
			if manifest != tt.wantManifest {
				t.Fatalf("DataPackageForPath(%q) manifest = %q, want %q", tt.path, manifest, tt.wantManifest)
			}
		})
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

// TestDeliverDataPackageRefusesAnUnregisteredTransport is the other half of
// the registry wiring: a manifest naming a driver nothing registered must be
// REFUSED, not silently delivered by whatever driver happens to be first.
//
// Without this, `Lookup` could be replaced by a hardcoded construction again
// and every existing test would still pass — which is exactly the state the
// phase audit found: a registry with no production caller, and an AC-8
// promise ("a second driver is a registry entry") that was not true as wired.
func TestDeliverDataPackageRefusesAnUnregisteredTransport(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	req := newTestDataDeliveryRequest(fx, "axon")
	req.ManifestRaw = []byte(`{"schema":"data-package/v1","id":"DP-axon-20260804-ab12","transport_driver":"no-such-driver"}`)

	funnel := NewWriteFunnel(host.NewFakeHost(), nil, "0.1.0")
	_, err := DeliverDataPackage(context.Background(), req, funnel)
	if err == nil {
		t.Fatal("DeliverDataPackage with an unregistered transport_driver = nil error, want a refusal")
	}
	if !errors.Is(err, ErrDataDeliveryInvalid) {
		t.Fatalf("error = %v, want it to wrap ErrDataDeliveryInvalid", err)
	}
	if !strings.Contains(err.Error(), "no-such-driver") {
		t.Fatalf("error = %v, want it to name the unregistered driver", err)
	}
}
