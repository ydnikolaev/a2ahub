package space

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

func TestResolveContractVersionUsesUniqueEstablishmentAndHistoricalBytes(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "old schema\n")
	establishment := commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))
	writeContractCandidateFile(t, repo, contractTestRoot+"/schema/order.schema.json", "current mirror bytes\n")
	writeContractCandidateFile(t, repo, contractTestRoot+"/contract.md", descriptor+"\nlater same-version edit\n")
	contractTestGit(t, repo, "add", "--", contractTestRoot)
	contractTestGit(t, repo, "commit", "-q", "-m", "later same-version edit")
	contractTestGit(t, repo, "push", "-q", "origin", "main")

	got, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("ResolveContractVersion: %v", err)
	}
	if got.CommitSHA != establishment {
		t.Fatalf("commit = %s, want establishment %s", got.CommitSHA, establishment)
	}
	if string(got.file("schema/order.schema.json").Raw) != "old schema\n" {
		t.Fatalf("historical schema = %q", got.file("schema/order.schema.json").Raw)
	}
	if got.DigestProfile != contract.ProfileContractSetV2 || got.AggregateVerification != ContractVerificationEventDigest || got.DescriptorVerification != ContractVerificationEventDigest {
		t.Fatalf("verification metadata = %+v", got)
	}
}

func TestVisitContractVersionsStreamsExactPackagesAndKeepsErrorsVersionScoped(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptorOne, filesOne, digestOne := contractV2Publication(t, "1.0.0", "VERSION_ONE_ONLY\n")
	commitContractPublication(t, repo, descriptorOne, filesOne, contractEventV2("1.0.0", digestOne))
	descriptorTwo, filesTwo, digestTwo := contractV2Publication(t, "2.0.0", "VERSION_TWO_ONLY\n")
	secondEvent := strings.Replace(contractEventV2("2.0.0", digestTwo), "01K1A2B3C4D5E6F7G8H9J0K1M7", "01K1A2B3C4D5E6F7G8H9J0K1M8", 1)
	commitContractPublicationAtPath(t, repo, "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml", descriptorTwo, filesTwo, secondEvent)

	type projectedResolution struct {
		version, snapshotVersion, raw string
		err                           error
	}
	results := make([]projectedResolution, 0, 3)
	err := VisitContractVersions(t.Context(), repo, contractTestID, []string{"1.0.0", "2.0.0", "3.0.0"}, contractHistoryValidator(t), func(result *HistoricalResolution) error {
		results = append(results, projectedResolution{
			version: result.Version, snapshotVersion: result.Snapshot.Version,
			raw: string(result.Snapshot.file("schema/order.schema.json").Raw), err: result.Err,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("VisitContractVersions: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d, want 3", len(results))
	}
	for index, token := range []string{"VERSION_ONE_ONLY", "VERSION_TWO_ONLY"} {
		result := results[index]
		if result.err != nil || result.snapshotVersion != result.version {
			t.Fatalf("result %d = %+v", index, result)
		}
		if !strings.Contains(result.raw, token) || strings.Contains(result.raw, []string{"VERSION_TWO_ONLY", "VERSION_ONE_ONLY"}[index]) {
			t.Errorf("%s mixed historical bytes: %q", result.version, result.raw)
		}
	}
	if results[2].version != "3.0.0" || !errors.Is(results[2].err, ErrContractNotPublished) || results[2].snapshotVersion != "" {
		t.Fatalf("missing version result = %+v", results[2])
	}
}

func TestVisitContractVersionsKeepsNonCanonicalVersionErrorScoped(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "CANONICAL_VERSION_ONLY\n")
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))

	type projectedResolution struct {
		version, snapshotVersion, raw string
		err                           error
	}
	results := make([]projectedResolution, 0, 2)
	err := VisitContractVersions(t.Context(), repo, contractTestID, []string{"1.0.0", "v1.0.0"}, contractHistoryValidator(t), func(result *HistoricalResolution) error {
		results = append(results, projectedResolution{
			version: result.Version, snapshotVersion: result.Snapshot.Version,
			raw: string(result.Snapshot.file("schema/order.schema.json").Raw), err: result.Err,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("VisitContractVersions returned caller-global error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if results[0].version != "1.0.0" || results[0].err != nil || results[0].snapshotVersion != "1.0.0" ||
		!strings.Contains(results[0].raw, "CANONICAL_VERSION_ONLY") {
		t.Fatalf("canonical result = %+v", results[0])
	}
	if results[1].version != "v1.0.0" || !errors.Is(results[1].err, ErrContractInvalidReference) || results[1].snapshotVersion != "" {
		t.Fatalf("non-canonical result = %+v", results[1])
	}
}

func TestVisitContractVersionsReleasesBorrowedRawBeforeNextVersion(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptorOne, filesOne, digestOne := contractV2Publication(t, "1.0.0", strings.Repeat("ONE", 4096))
	commitContractPublication(t, repo, descriptorOne, filesOne, contractEventV2("1.0.0", digestOne))
	descriptorTwo, filesTwo, digestTwo := contractV2Publication(t, "2.0.0", strings.Repeat("TWO", 4096))
	secondEvent := strings.Replace(contractEventV2("2.0.0", digestTwo), "01K1A2B3C4D5E6F7G8H9J0K1M7", "01K1A2B3C4D5E6F7G8H9J0K1M8", 1)
	commitContractPublicationAtPath(t, repo, "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml", descriptorTwo, filesTwo, secondEvent)

	retained := make([]*HistoricalResolution, 0, 3)
	order := make([]string, 0, 3)
	err := VisitContractVersions(t.Context(), repo, contractTestID, []string{"1.0.0", "v1.0.0", "2.0.0"}, contractHistoryValidator(t), func(result *HistoricalResolution) error {
		for _, prior := range retained {
			if raw := historicalResolutionRawBytes(prior); raw != 0 {
				t.Fatalf("prior borrowed result %s retains %d raw bytes at next callback", prior.Version, raw)
			}
		}
		if result.Err == nil && historicalResolutionRawBytes(result) == 0 {
			t.Fatalf("available result %s has no raw bytes during callback", result.Version)
		}
		order = append(order, result.Version)
		retained = append(retained, result)
		return nil
	})
	if err != nil {
		t.Fatalf("VisitContractVersions: %v", err)
	}
	if got, want := strings.Join(order, ","), "1.0.0,v1.0.0,2.0.0"; got != want {
		t.Fatalf("callback order = %q, want %q", got, want)
	}
	for _, result := range retained {
		if raw := historicalResolutionRawBytes(result); raw != 0 {
			t.Fatalf("borrowed result %s retains %d raw bytes after batch", result.Version, raw)
		}
	}

	stop := errors.New("stop visitor")
	var stopped *HistoricalResolution
	err = VisitContractVersions(t.Context(), repo, contractTestID, []string{"1.0.0", "2.0.0"}, contractHistoryValidator(t), func(result *HistoricalResolution) error {
		stopped = result
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("visitor error = %v, want %v", err, stop)
	}
	if stopped == nil || stopped.Version != "1.0.0" || historicalResolutionRawBytes(stopped) != 0 {
		t.Fatalf("visitor-error result was not released: %+v raw=%d", stopped, historicalResolutionRawBytes(stopped))
	}
}

func historicalResolutionRawBytes(result *HistoricalResolution) int {
	if result == nil {
		return 0
	}
	total := len(result.Snapshot.PublishEventRaw)
	for _, file := range result.Snapshot.Files {
		total += len(file.Raw)
	}
	for _, raw := range result.Snapshot.CarriedSet.Bytes {
		total += len(raw)
	}
	return total
}

func TestResolveContractVersionIgnoresLaterMalformedUnrelatedEvent(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "schema\n")
	establishment := commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))
	malformedPath := "seomatrix/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M9.yaml"
	writeContractCandidateFile(t, repo, malformedPath, "schema: event/v2\nsubject: [malformed\n")
	contractTestGit(t, repo, "add", "--", malformedPath)
	contractTestGit(t, repo, "commit", "-q", "-m", "retain malformed unrelated event")
	contractTestGit(t, repo, "push", "-q", "origin", "main")

	got, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("malformed unrelated event blocked exact history: %v", err)
	}
	if got.CommitSHA != establishment {
		t.Fatalf("establishment = %s, want %s", got.CommitSHA, establishment)
	}
}

func TestResolveContractVersionFindsEscapedSemanticSubject(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	files := map[string]string{"schema/main.schema.json": "schema\n"}
	digest := legacyContractDigest(t, files)
	event := strings.Replace(
		contractEventV1("1.0.0", digest),
		"subject: "+contractTestID,
		`subject: "XC-axon-\u0077idget"`,
		1,
	)
	establishment := commitContractPublication(t, repo, contractV1Descriptor("1.0.0"), files, event)

	got, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("escaped semantic subject was not resolved: %v", err)
	}
	if got.CommitSHA != establishment {
		t.Fatalf("establishment = %s, want %s", got.CommitSHA, establishment)
	}
}

func TestResolveContractVersionLegacyDescriptorHasDistinctVerification(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor := contractV1Descriptor("1.0.0")
	files := map[string]string{
		"schema/main.schema.json": "schema\n",
		"fixtures/valid/ok.json":  "{}\n",
	}
	digest := legacyContractDigest(t, files)
	commitContractPublication(t, repo, descriptor, files, contractEventV1("1.0.0", digest))

	got, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.DigestProfile != contract.ProfileContractTreeV1 || got.DescriptorVerification != ContractVerificationGitObjectAtPublishCommit {
		t.Fatalf("legacy verification metadata = profile %q descriptor %q", got.DigestProfile, got.DescriptorVerification)
	}
	if _, covered := got.CarriedSet.PerFileDigest[contract.DescriptorPath]; covered {
		t.Fatal("legacy event digest must not cover contract.md")
	}
}

func TestResolveContractVersionRejectsDuplicateEstablishment(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	commitLegacyContractVersion(t, repo, "1.0.0", "schema one")
	commitLegacyContractVersion(t, repo, "2.0.0", "schema two")
	commitLegacyContractVersion(t, repo, "1.0.0", "schema duplicate")

	_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrContractHistoryInvalid) {
		t.Fatalf("duplicate establishment error = %v", err)
	}
}

func TestResolveContractVersionRejectsLaterEventOnlyDuplicate(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	files := map[string]string{"schema/main.schema.json": "schema"}
	digest := legacyContractDigest(t, files)
	commitContractPublication(t, repo, contractV1Descriptor("1.0.0"), files, contractEventV1("1.0.0", digest))
	duplicateID := "01K1A2B3C4D5E6F7G8H9J0K1M8"
	duplicatePath := "axon/events/2026/" + duplicateID + ".yaml"
	writeContractCandidateFile(t, repo, duplicatePath, contractEventV1WithID(duplicateID, "1.0.0", digest))
	contractTestGit(t, repo, "add", "--", duplicatePath)
	contractTestGit(t, repo, "commit", "-q", "-m", "duplicate event only")
	contractTestGit(t, repo, "push", "-q", "origin", "main")

	_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrContractHistoryInvalid) {
		t.Fatalf("event-only duplicate error = %v", err)
	}
}

func TestResolveContractVersionRequiresCanonicalDocumentsAndExactIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		descriptor func(string) string
		event      func(string) string
		eventPath  string
	}{
		{
			name: "invalid descriptor",
			descriptor: func(version string) string {
				return strings.Replace(contractV1Descriptor(version), "title: Widget contract\n", "", 1)
			},
			event:     func(digest string) string { return contractEventV1("1.0.0", digest) },
			eventPath: contractTestEventPath,
		},
		{
			name:       "invalid event",
			descriptor: contractV1Descriptor,
			event: func(digest string) string {
				return strings.Replace(contractEventV1("1.0.0", digest), "at: 2026-08-03T12:00:00Z\n", "", 1)
			},
			eventPath: contractTestEventPath,
		},
		{
			name:       "foreign event owner",
			descriptor: contractV1Descriptor,
			event:      func(digest string) string { return contractEventV1("1.0.0", digest) },
			eventPath:  "seomatrix/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M7.yaml",
		},
		{
			name:       "actor owner mismatch",
			descriptor: contractV1Descriptor,
			event: func(digest string) string {
				return strings.Replace(contractEventV1("1.0.0", digest), "system: axon", "system: seomatrix", 1)
			},
			eventPath: contractTestEventPath,
		},
		{
			name:       "event filename mismatch",
			descriptor: contractV1Descriptor,
			event:      func(digest string) string { return contractEventV1("1.0.0", digest) },
			eventPath:  "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M8.yaml",
		},
		{
			name:       "v1 descriptor with v2 event",
			descriptor: contractV1Descriptor,
			event:      func(digest string) string { return contractEventV2("1.0.0", digest) },
			eventPath:  contractTestEventPath,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newContractHistoryRepo(t)
			files := map[string]string{"schema/main.schema.json": "schema"}
			digest := legacyContractDigest(t, files)
			commitContractPublicationAtPath(t, repo, tc.eventPath, tc.descriptor("1.0.0"), files, tc.event(digest))
			_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
			if !errors.Is(err, ErrContractHistoryInvalid) {
				t.Fatalf("invalid publication error = %v", err)
			}
		})
	}
}

func TestResolveContractVersionRejectsV2DescriptorWithV1Event(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "schema")
	commitContractPublication(t, repo, descriptor, files, contractEventV1("1.0.0", digest))
	_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrContractHistoryInvalid) {
		t.Fatalf("v2/v1 pairing error = %v", err)
	}
}

func TestResolveContractVersionIgnoresSecondParentOnlyPublication(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	writeContractCandidateFile(t, repo, "README.md", "base")
	contractTestGit(t, repo, "add", "--", "README.md")
	contractTestGit(t, repo, "commit", "-q", "-m", "base")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
	contractTestGit(t, repo, "checkout", "-q", "-b", "publish")
	files := map[string]string{"schema/main.schema.json": "schema"}
	writeContractPublication(t, repo, contractV1Descriptor("1.0.0"), files, contractEventV1("1.0.0", legacyContractDigest(t, files)))
	contractTestGit(t, repo, "add", "--", contractTestRoot, contractTestEventPath)
	contractTestGit(t, repo, "commit", "-q", "-m", "second parent publication")
	contractTestGit(t, repo, "checkout", "-q", "main")
	contractTestGit(t, repo, "merge", "-q", "--no-ff", "-s", "ours", "publish", "-m", "ignore topic tree")
	contractTestGit(t, repo, "push", "-q", "origin", "main")

	_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrContractNotPublished) {
		t.Fatalf("second-parent-only publication error = %v", err)
	}
}

func TestResolveContractVersionLegacyAggregateExcludesDescriptorAndArtifacts(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	files := make(map[string]string, 17)
	chunk := strings.Repeat("x", contract.MaxFileBytes)
	for i := 0; i < contract.MaxAggregateBytes/contract.MaxFileBytes; i++ {
		files[fmt.Sprintf("schema/%02d.bin", i)] = chunk
	}
	files["artifacts/ignored.txt"] = "not carried"
	digest := legacyContractDigest(t, files)
	commitContractPublication(t, repo, contractV1Descriptor("1.0.0"), files, contractEventV1("1.0.0", digest))

	got, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("legacy exact aggregate: %v", err)
	}
	aggregate := 0
	for _, raw := range got.CarriedSet.Bytes {
		aggregate += len(raw)
	}
	if aggregate != contract.MaxAggregateBytes {
		t.Fatalf("legacy aggregate = %d, want %d", aggregate, contract.MaxAggregateBytes)
	}
}

func TestResolveContractVersionRequiresValidator(t *testing.T) {
	t.Parallel()

	_, err := ResolveContractVersion(t.Context(), t.TempDir(), contractTestID, "1.0.0", nil)
	if !errors.Is(err, ErrContractHistoryInvalid) {
		t.Fatalf("nil validator error = %v", err)
	}
}

func TestResolveContractVersionReportsMissingHistoricalBlob(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	files := map[string]string{"schema/main.schema.json": "unique missing object bytes"}
	commitContractPublication(t, repo, contractV1Descriptor("1.0.0"), files, contractEventV1("1.0.0", legacyContractDigest(t, files)))
	oid := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD:"+contractTestRoot+"/schema/main.schema.json"))
	objectPath := filepath.Join(repo, ".git", "objects", oid[:2], oid[2:])
	if err := os.Remove(objectPath); err != nil {
		t.Fatalf("remove invocation-owned loose object: %v", err)
	}

	_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrContractObjectUnavailable) {
		t.Fatalf("missing historical object error = %v", err)
	}
}

func TestResolveContractVersionFirstParentPublicationShapes(t *testing.T) {
	t.Parallel()

	for _, shape := range []string{"merge", "squash", "rebase"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			repo := newContractHistoryRepo(t)
			writeContractCandidateFile(t, repo, "README.md", "base")
			contractTestGit(t, repo, "add", "--", "README.md")
			contractTestGit(t, repo, "commit", "-q", "-m", "base")
			contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
			contractTestGit(t, repo, "checkout", "-q", "-b", "publish")
			descriptor := contractV1Descriptor("1.0.0")
			files := map[string]string{"schema/main.schema.json": "schema"}
			digest := legacyContractDigest(t, files)
			writeContractPublication(t, repo, descriptor, files, contractEventV1("1.0.0", digest))
			contractTestGit(t, repo, "add", "--", contractTestRoot, contractTestEventPath)
			contractTestGit(t, repo, "commit", "-q", "-m", "publication topic")

			contractTestGit(t, repo, "checkout", "-q", "main")
			switch shape {
			case "merge":
				contractTestGit(t, repo, "merge", "-q", "--no-ff", "publish", "-m", "merge publication")
			case "squash":
				contractTestGit(t, repo, "merge", "-q", "--squash", "publish")
				contractTestGit(t, repo, "commit", "-q", "-m", "squash publication")
			case "rebase":
				writeContractCandidateFile(t, repo, "README.md", "main advanced")
				contractTestGit(t, repo, "add", "--", "README.md")
				contractTestGit(t, repo, "commit", "-q", "-m", "advance main")
				contractTestGit(t, repo, "checkout", "-q", "publish")
				contractTestGit(t, repo, "rebase", "main")
				contractTestGit(t, repo, "checkout", "-q", "main")
				contractTestGit(t, repo, "merge", "-q", "--ff-only", "publish")
			}
			establishment := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
			contractTestGit(t, repo, "push", "-q", "origin", "main")

			got, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
			if err != nil {
				t.Fatal(err)
			}
			if got.CommitSHA != establishment {
				t.Fatalf("establishment = %s, want authoritative %s commit %s", got.CommitSHA, shape, establishment)
			}
		})
	}
}

func TestResolveContractVersionRejectsHostileTreeModesPathsBoundsAndDigest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		make func(t *testing.T, repo string)
		want error
	}{
		{
			name: "whitespace-path",
			make: func(t *testing.T, repo string) {
				descriptor := contractV1Descriptor("1.0.0")
				files := map[string]string{"schema/name with space.json": "schema"}
				commitContractPublication(t, repo, descriptor, files, contractEventV1("1.0.0", artifact.Digest(nil)))
			},
			want: ErrContractHistoryInvalid,
		},
		{
			name: "symlink",
			make: func(t *testing.T, repo string) {
				descriptor := contractV1Descriptor("1.0.0")
				writeContractPublication(t, repo, descriptor, nil, contractEventV1("1.0.0", artifact.Digest(nil)))
				full := filepath.Join(repo, filepath.FromSlash(contractTestRoot+"/schema/link"))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("target", full); err != nil {
					t.Fatal(err)
				}
				contractTestGit(t, repo, "add", "--", contractTestRoot, contractTestEventPath)
				contractTestGit(t, repo, "commit", "-q", "-m", "symlink publication")
				contractTestGit(t, repo, "push", "-q", "origin", "main")
			},
			want: ErrContractUnsafeEntry,
		},
		{
			name: "submodule",
			make: func(t *testing.T, repo string) {
				writeContractCandidateFile(t, repo, "README.md", "base")
				contractTestGit(t, repo, "add", "--", "README.md")
				contractTestGit(t, repo, "commit", "-q", "-m", "base")
				base := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
				writeContractPublication(t, repo, contractV1Descriptor("1.0.0"), nil, contractEventV1("1.0.0", artifact.Digest(nil)))
				contractTestGit(t, repo, "add", "--", contractTestRoot+"/contract.md", contractTestEventPath)
				contractTestGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+base+","+contractTestRoot+"/schema/submodule")
				contractTestGit(t, repo, "commit", "-q", "-m", "submodule publication")
				contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
			},
			want: ErrContractUnsafeEntry,
		},
		{
			name: "oversized",
			make: func(t *testing.T, repo string) {
				descriptor := contractV1Descriptor("1.0.0")
				writeContractPublication(t, repo, descriptor, nil, contractEventV1("1.0.0", artifact.Digest(nil)))
				writeContractCandidateBytes(t, repo, contractTestRoot+"/schema/large", make([]byte, contract.MaxFileBytes+1))
				contractTestGit(t, repo, "add", "--", contractTestRoot, contractTestEventPath)
				contractTestGit(t, repo, "commit", "-q", "-m", "oversized publication")
				contractTestGit(t, repo, "push", "-q", "origin", "main")
			},
			want: ErrContractBoundedResult,
		},
		{
			name: "digest-mismatch",
			make: func(t *testing.T, repo string) {
				commitContractPublication(t, repo, contractV1Descriptor("1.0.0"), map[string]string{"schema/main": "schema"}, contractEventV1("1.0.0", artifact.Digest(nil)))
			},
			want: ErrContractDigestMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newContractHistoryRepo(t)
			tc.make(t, repo)
			_, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResolveContractVersion error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResolveContractVersionValidatesExactReferenceAndAuthority(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	for _, tc := range []struct{ id, version string }{
		{"XC-axon-widget", "latest"},
		{"XC-axon-widget", "01.0.0"},
		{"XR-axon-widget", "1.0.0"},
	} {
		if _, err := ResolveContractVersion(t.Context(), repo, tc.id, tc.version, contractHistoryValidator(t)); !errors.Is(err, ErrContractInvalidReference) {
			t.Fatalf("ResolveContractVersion(%q, %q) = %v", tc.id, tc.version, err)
		}
	}
	if _, err := ResolveContractVersion(t.Context(), repo, contractTestID, "1.0.0", contractHistoryValidator(t)); !errors.Is(err, ErrContractObjectUnavailable) {
		t.Fatalf("missing origin/main error = %v", err)
	}
}

func TestResolveContractVersionPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ResolveContractVersion(ctx, repo, contractTestID, "1.0.0", contractHistoryValidator(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveContractVersion cancelled error = %v", err)
	}
}

const (
	contractTestID        = "XC-axon-widget"
	contractTestRoot      = "axon/provides/widget"
	contractTestEventPath = "axon/events/2026/01K1A2B3C4D5E6F7G8H9J0K1M7.yaml"
)

func contractV1Descriptor(version string) string {
	return fmt.Sprintf(`---
schema: envelope/v1
id: %s
type: contract
title: Widget contract
space: test
from: axon
to: [seomatrix]
actor: {kind: agent, name: publisher}
created: 2026-08-03T09:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: %q
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:axon-20260803-c5h9
---
contract
`, contractTestID, version)
}

func contractV2Publication(t *testing.T, version, schemaRaw string) (string, map[string]string, string) {
	t.Helper()
	descriptor := fmt.Sprintf(`---
schema: envelope/v2
id: %s
type: contract
title: Widget contract
space: test
from: axon
to: [seomatrix]
actor: {kind: agent, name: publisher}
created: 2026-08-03T09:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: %q
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:axon-20260803-c5h9
artifacts:
  - path: schema/order.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/order.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: fixtures/invalid/order.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: artifacts/errors.yaml
    role: errors
    normative: true
    media_type: application/yaml
---
contract
`, contractTestID, version)
	files := map[string]string{
		"schema/order.schema.json":    schemaRaw,
		"fixtures/valid/order.json":   "{}\n",
		"fixtures/invalid/order.json": "null\n",
		"artifacts/errors.yaml":       "errors: []\n",
	}
	parsed, err := contract.ParseDescriptor([]byte(descriptor))
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]contract.CandidateFile, 0, len(files))
	for path, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte(descriptor), parsed, candidates)
	if len(issues) != 0 {
		t.Fatalf("test v2 carried set: %v", issues)
	}
	return descriptor, files, set.AggregateDigest
}

func legacyContractDigest(t *testing.T, files map[string]string) string {
	t.Helper()
	candidates := make([]contract.CandidateFile, 0, len(files))
	for path, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractTreeV1, nil, contract.Descriptor{}, candidates)
	if len(issues) != 0 {
		t.Fatalf("test legacy carried set: %v", issues)
	}
	return set.AggregateDigest
}

func contractEventV1(version, digest string) string {
	return contractEventV1WithID("01K1A2B3C4D5E6F7G8H9J0K1M7", version, digest)
}

func contractEventV2(version, digest string) string {
	return fmt.Sprintf("schema: event/v2\nevent: 01K1A2B3C4D5E6F7G8H9J0K1M7\nspace: test\nsubject: %s\ntransition: publish\nactor: {kind: agent, name: publisher, system: axon}\nat: 2026-08-03T12:00:00Z\nversion: %q\ndigest: %s\ndigest_profile: contract-set-v2\npublication:\n  intent_key: op-v1-%s\n  candidate_intent_digest: sha256:%s\n  version_selector: explicit:%s\n  operation_key: op-v1-%s\n", contractTestID, version, digest, strings.Repeat("a", 64), strings.Repeat("b", 64), version, strings.Repeat("c", 64))
}

func contractEventV1WithID(eventID, version, digest string) string {
	return fmt.Sprintf("schema: event/v1\nevent: %s\nspace: test\nsubject: %s\ntransition: publish\nactor: {kind: agent, name: publisher, system: axon}\nat: 2026-08-03T12:00:00Z\nversion: %q\ndigest: %s\n", eventID, contractTestID, version, digest)
}

func commitLegacyContractVersion(t *testing.T, repo, version, schema string) string {
	t.Helper()
	files := map[string]string{"schema/main.schema.json": schema}
	count := len(strings.Fields(contractTestGitOutput(t, repo, "rev-list", "--all")))
	eventID := fmt.Sprintf("01K1A2B3C4D5E6F7G8H9J0K1%c", '7'+rune(count))
	eventPath := fmt.Sprintf("axon/events/2026/%s.yaml", eventID)
	return commitContractPublicationAtPath(t, repo, eventPath, contractV1Descriptor(version), files, contractEventV1WithID(eventID, version, legacyContractDigest(t, files)))
}

func commitContractPublication(t *testing.T, repo, descriptor string, files map[string]string, event string) string {
	t.Helper()
	return commitContractPublicationAtPath(t, repo, contractTestEventPath, descriptor, files, event)
}

func commitContractPublicationAtPath(t *testing.T, repo, eventPath, descriptor string, files map[string]string, event string) string {
	t.Helper()
	writeContractPublicationAtPath(t, repo, eventPath, descriptor, files, event)
	contractTestGit(t, repo, "add", "--", contractTestRoot, eventPath)
	contractTestGit(t, repo, "commit", "-q", "-m", "publish contract")
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
	return strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
}

func writeContractPublication(t *testing.T, repo, descriptor string, files map[string]string, event string) {
	t.Helper()
	writeContractPublicationAtPath(t, repo, contractTestEventPath, descriptor, files, event)
}

func writeContractPublicationAtPath(t *testing.T, repo, eventPath, descriptor string, files map[string]string, event string) {
	t.Helper()
	writeContractCandidateFile(t, repo, contractTestRoot+"/contract.md", descriptor)
	for path, raw := range files {
		writeContractCandidateFile(t, repo, contractTestRoot+"/"+path, raw)
	}
	writeContractCandidateFile(t, repo, eventPath, event)
}

type canonicalContractHistoryValidator struct {
	engine *validate.Engine
}

func contractHistoryValidator(t *testing.T) canonicalContractHistoryValidator {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("load canonical schema corpus: %v", err)
	}
	return canonicalContractHistoryValidator{engine: validate.New(corpus)}
}

func (v canonicalContractHistoryValidator) ValidateHistoricalContractDocuments(ctx context.Context, documents ContractHistoryDocuments) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	descriptor, err := v.engine.ValidateDraft(validate.Draft{Path: documents.Descriptor.Path, Raw: documents.Descriptor.Raw})
	if err != nil {
		return err
	}
	if !descriptor.Valid {
		return fmt.Errorf("descriptor violations: %v", descriptor.Violations)
	}
	event, err := v.engine.ValidateEvent(documents.PublishEvent.Raw, "0.0.0")
	if err != nil {
		return err
	}
	if !event.Valid {
		return fmt.Errorf("event violations: %v", event.Violations)
	}
	return nil
}
