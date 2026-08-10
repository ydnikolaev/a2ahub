package html

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestLimitContractVersionDetailsBoundsWorstCaseDeterministically(t *testing.T) {
	t.Parallel()
	if preview := contractVersionPreview([]byte("valid-prefix\xf0\x9f")); preview != "valid-prefix" || !utf8.ValidString(preview) {
		t.Fatalf("invalid UTF-8 preview = %q", preview)
	}

	const maximumHistoricalVersions = 256
	hostile := strings.Repeat("<\x00😀", 700)
	build := func(reverse bool) []Contract {
		versions := make([]ContractVersion, 0, maximumHistoricalVersions)
		for versionIndex := 0; versionIndex < maximumHistoricalVersions; versionIndex++ {
			documents := make([]ContractVersionDocument, 0, contract.MaxExplicitFiles+1)
			documents = append(documents, ContractVersionDocument{
				Path: contract.DescriptorPath, Role: "descriptor", Normative: true,
				MediaType: "text/markdown", Digest: "sha256:" + strings.Repeat("d", 64),
				SizeBytes: int64(len(hostile)), Preview: hostile, PreviewLanguage: "markdown",
			})
			for fileIndex := 0; fileIndex < contract.MaxExplicitFiles; fileIndex++ {
				documents = append(documents, ContractVersionDocument{
					Path: fmt.Sprintf("files/%03d.json", fileIndex), Role: "schema", Normative: true,
					MediaType: "application/json", Digest: fmt.Sprintf("sha256:%064d", fileIndex),
					SizeBytes: int64(len(hostile)), Preview: hostile, PreviewLanguage: "json",
				})
			}
			if reverse {
				slices.Reverse(documents)
			}
			versions = append(versions, ContractVersion{
				Version: fmt.Sprintf("1.%d.0", versionIndex),
				Detail:  &ContractVersionDetail{Status: ContractVersionDetailAvailable, Description: hostile, Documents: documents},
			})
		}
		if reverse {
			slices.Reverse(versions)
		}
		return []Contract{{Space: "space-a", ID: "XC-atlas-orders", Versions: versions}}
	}

	forward := build(false)
	reversed := build(true)
	limitContractVersionDetails(forward)
	limitContractVersionDetails(reversed)

	projectionMap := func(contracts []Contract) (map[string]ContractVersionDocument, map[string]string, int, int, int) {
		documents := make(map[string]ContractVersionDocument, maximumEmbeddedContractDocuments)
		previews := make(map[string]string, maximumEmbeddedContractDocuments+maximumHistoricalVersions)
		encodedBytes := 0
		totalDocuments := 0
		omittedDocuments := 0
		for _, version := range contracts[0].Versions {
			detail := version.Detail
			if detail.TotalDocumentCount != contract.MaxExplicitFiles+1 || detail.OmittedDocumentCount != detail.TotalDocumentCount-len(detail.Documents) {
				t.Fatalf("%s counts = total %d omitted %d shown %d", version.Version, detail.TotalDocumentCount, detail.OmittedDocumentCount, len(detail.Documents))
			}
			totalDocuments += detail.TotalDocumentCount
			omittedDocuments += detail.OmittedDocumentCount
			values := []struct{ key, preview string }{{version.Version + ":description", detail.Description}}
			var descriptorShown bool
			for _, document := range detail.Documents {
				if document.Digest == "" || document.SizeBytes != int64(len(hostile)) || document.Path == "" {
					t.Fatalf("metadata changed while limiting %s@%s: %+v", contracts[0].ID, version.Version, document)
				}
				key := version.Version + ":" + document.Path
				documents[key] = document
				values = append(values, struct{ key, preview string }{version.Version + ":" + document.Path, document.Preview})
				descriptorShown = descriptorShown || document.Path == contract.DescriptorPath
			}
			if !descriptorShown {
				t.Fatalf("%s omitted descriptor metadata before lower-priority carried files", version.Version)
			}
			for _, value := range values {
				if !utf8.ValidString(value.preview) {
					t.Fatalf("%s preview is not valid UTF-8", value.key)
				}
				previews[value.key] = value.preview
				encoded, err := json.Marshal(value.preview)
				if err != nil {
					t.Fatalf("marshal %s preview: %v", value.key, err)
				}
				encodedBytes += len(encoded) - 2
			}
		}
		return documents, previews, encodedBytes, totalDocuments, omittedDocuments
	}

	forwardDocuments, forwardPreviews, encodedBytes, totalDocuments, omittedDocuments := projectionMap(forward)
	reversedDocuments, reversedPreviews, reversedEncodedBytes, reversedTotalDocuments, reversedOmittedDocuments := projectionMap(reversed)
	wantTotalDocuments := maximumHistoricalVersions * (contract.MaxExplicitFiles + 1)
	if totalDocuments != wantTotalDocuments || reversedTotalDocuments != wantTotalDocuments {
		t.Fatalf("total document counts = %d/%d, want %d", totalDocuments, reversedTotalDocuments, wantTotalDocuments)
	}
	if len(forwardDocuments) > maximumEmbeddedContractDocuments || len(reversedDocuments) > maximumEmbeddedContractDocuments ||
		omittedDocuments != totalDocuments-len(forwardDocuments) || reversedOmittedDocuments != reversedTotalDocuments-len(reversedDocuments) {
		t.Fatalf("bounded projection shown=%d/%d omitted=%d/%d", len(forwardDocuments), len(reversedDocuments), omittedDocuments, reversedOmittedDocuments)
	}
	if encodedBytes > maximumEmbeddedContractPreviewJSONBytes || reversedEncodedBytes > maximumEmbeddedContractPreviewJSONBytes {
		t.Fatalf("encoded preview bytes = %d/%d, maximum %d", encodedBytes, reversedEncodedBytes, maximumEmbeddedContractPreviewJSONBytes)
	}
	docs, err := Docs()
	if err != nil {
		t.Fatalf("load embedded docs: %v", err)
	}
	shell, err := Render(DefaultTemplate(), Data{Contracts: forward}, docs)
	if err != nil {
		t.Fatalf("render 256 x 257 historical package shell: %v", err)
	}
	if len(shell) > 4<<20 {
		t.Fatalf("256 x 257 historical package shell = %d bytes, exceeds a2a serve 4 MiB ceiling", len(shell))
	}
	if !reflect.DeepEqual(forwardDocuments, reversedDocuments) || !reflect.DeepEqual(forwardPreviews, reversedPreviews) {
		t.Fatal("document or preview projection changed when version and file input order changed")
	}
	limitContractVersionDetails(forward)
	repeatedDocuments, repeatedPreviews, repeatedEncodedBytes, repeatedTotalDocuments, repeatedOmittedDocuments := projectionMap(forward)
	if !reflect.DeepEqual(forwardDocuments, repeatedDocuments) || !reflect.DeepEqual(forwardPreviews, repeatedPreviews) ||
		repeatedEncodedBytes != encodedBytes || repeatedTotalDocuments != totalDocuments || repeatedOmittedDocuments != omittedDocuments {
		t.Fatal("bounded projection changed when applied a second time")
	}
	var nonEmpty, truncated int
	for _, preview := range forwardPreviews {
		if preview != "" {
			nonEmpty++
		}
		if preview != "" && len(preview) < len(hostile) {
			truncated++
		}
	}
	if nonEmpty == 0 || truncated == 0 || nonEmpty == len(forwardPreviews) {
		t.Fatalf("worst-case allocation non-empty=%d truncated=%d total=%d, want deterministic exhaustion with a partial UTF-8-safe preview", nonEmpty, truncated, len(forwardPreviews))
	}
}

func TestAssembleContractVersionDetailsResolveExactHistoricalPackages(t *testing.T) {
	t.Parallel()

	fixture := spacefixture.New(t, "atlas", "checkout")
	mirror := fixture.Clone("atlas")
	writeHTMLContractFile(t, mirror, "checkout/consumes.yaml", "schema: consumes/v1\nsystem: checkout\ndependencies:\n  - contract: XC-atlas-orders\n    major: 1\n    since: 2026-05-01\n")
	first := publishHTMLLegacyContractVersion(t, mirror, "1.0.0", "01K1A2B3C4D5E6F7G8H9J0K1M7", "2026-06-01T10:00:00Z", "VERSION_ONE_ONLY")
	second := publishHTMLContractVersion(t, mirror, "1.1.0", "01K1A2B3C4D5E6F7G8H9J0K1M8", "2026-07-01T11:00:00Z", "VERSION_TWO_ONLY", true)
	// The working cache can be ahead of its synchronized origin/main. It may
	// list this fold-visible version, but its historical package is not yet an
	// authoritative package and must not borrow bytes from either pushed line.
	publishHTMLContractVersion(t, mirror, "2.0.0", "01K1A2B3C4D5E6F7G8H9J0K1M9", "2026-08-01T12:00:00Z", "LOCAL_ONLY", false)

	manifestRaw, err := os.ReadFile(filepath.Join(mirror, "space.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := space.ParseManifest(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	store := cache.NewStore("atlas", t.TempDir(), []cache.SpaceMirror{{
		SpaceID: manifest.Space, Dir: mirror, RepoURL: fixture.RemoteURL(), Manifest: manifest,
	}}, func() time.Time { return now }, 0)

	data, err := AssembleWithContractHistory(t.Context(), store, "", now, htmlContractHistoryValidator(t))
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	var assembled *Contract
	for index := range data.Contracts {
		if data.Contracts[index].Space == manifest.Space && data.Contracts[index].ID == "XC-atlas-orders" {
			assembled = &data.Contracts[index]
			break
		}
	}
	if assembled == nil {
		t.Fatalf("assembled contracts omit XC-atlas-orders: %+v", data.Contracts)
	}

	versions := make(map[string]ContractVersion, len(assembled.Versions))
	for _, version := range assembled.Versions {
		versions[version.Version] = version
	}
	for _, expectation := range []struct {
		version, ownToken, otherToken, commit, publishedAt, profile, descriptorVerification string
	}{
		{version: "1.0.0", ownToken: "VERSION_ONE_ONLY", otherToken: "VERSION_TWO_ONLY", commit: first, publishedAt: "2026-06-01T10:00:00Z", profile: "contract-tree-v1", descriptorVerification: space.ContractVerificationGitObjectAtPublishCommit},
		{version: "1.1.0", ownToken: "VERSION_TWO_ONLY", otherToken: "VERSION_ONE_ONLY", commit: second, publishedAt: "2026-07-01T11:00:00Z", profile: "contract-set-v2", descriptorVerification: space.ContractVerificationEventDigest},
	} {
		version, ok := versions[expectation.version]
		if !ok || version.Detail == nil || version.Detail.Status != ContractVersionDetailAvailable {
			t.Fatalf("%s detail = %+v, want exact available package", expectation.version, version.Detail)
		}
		detail := version.Detail
		if detail.TotalDocumentCount != len(detail.Documents) || detail.OmittedDocumentCount != 0 {
			t.Errorf("%s document projection counts = total %d shown %d omitted %d", expectation.version, detail.TotalDocumentCount, len(detail.Documents), detail.OmittedDocumentCount)
		}
		if detail.PublishedAt != expectation.publishedAt || detail.PublishedBy != "atlas" ||
			detail.SchemaFormat != "json-schema-2020-12" || detail.CompatPolicy != "backward" ||
			detail.Provenance == nil || detail.Provenance.CommitSHA != expectation.commit ||
			detail.Provenance.Profile != expectation.profile || detail.Provenance.DigestProfile != expectation.profile ||
			detail.Provenance.DescriptorVerification != expectation.descriptorVerification || detail.Provenance.PublishedDigest == "" ||
			!strings.Contains(detail.Description, expectation.ownToken) || strings.Contains(detail.Description, expectation.otherToken) ||
			strings.Contains(detail.Description, "LOCAL_ONLY") {
			t.Fatalf("%s exact publication facts = %+v", expectation.version, detail)
		}
		if expectation.version == "1.1.0" {
			if len(detail.ConsumerPins) != 1 || detail.ConsumerPins[0].System != "checkout" ||
				detail.ConsumerPins[0].PinnedVersion != "1.1.0" || detail.ConsumerPins[0].Drift != "behind" {
				t.Errorf("1.1.0 exact current pin = %+v", detail.ConsumerPins)
			}
		} else if len(detail.ConsumerPins) != 0 {
			t.Errorf("%s borrowed a consumer pin: %+v", expectation.version, detail.ConsumerPins)
		}
		joined := ""
		var descriptorDocuments int
		for _, document := range detail.Documents {
			joined += document.Path + "\n" + document.Preview + "\n"
			if document.Digest == "" || document.SizeBytes == 0 {
				t.Errorf("%s document lacks verified metadata: %+v", expectation.version, document)
			}
			if document.Path == contract.DescriptorPath {
				descriptorDocuments++
				if document.Role != "descriptor" || !document.Normative || document.MediaType != "text/markdown" ||
					document.PreviewLanguage != "markdown" || !strings.Contains(document.Preview, expectation.ownToken) {
					t.Errorf("%s descriptor projection = %+v", expectation.version, document)
				}
				if expectation.profile == string(contract.ProfileContractSetV2) && document.Digest != artifact.Digest([]byte(document.Preview)) {
					t.Errorf("%s descriptor digest = %q, want digest of exact descriptor bytes", expectation.version, document.Digest)
				}
			}
		}
		if descriptorDocuments != 1 {
			t.Errorf("%s descriptor document count = %d, want 1", expectation.version, descriptorDocuments)
		}
		if !strings.Contains(joined, expectation.ownToken) || strings.Contains(joined, expectation.otherToken) || strings.Contains(joined, "LOCAL_ONLY") {
			t.Errorf("%s package mixed version bytes:\n%s", expectation.version, joined)
		}
		if len(detail.History) != 1 || detail.History[0].EventID == "" || detail.History[0].Transition != "publish" ||
			detail.History[0].State != "published" || detail.History[0].At != expectation.publishedAt {
			t.Errorf("%s exact history = %+v", expectation.version, detail.History)
		}
	}

	local := versions["2.0.0"]
	if local.Detail == nil || local.Detail.Status != ContractVersionDetailUnavailable ||
		strings.TrimSpace(local.Detail.UnavailableReason) == "" {
		t.Fatalf("local-only detail = %+v, want honest unavailable reason", local.Detail)
	}
	if len(local.Detail.Documents) != 0 || len(local.Detail.History) != 0 || local.Detail.Provenance != nil ||
		local.Detail.Description != "" || local.Detail.PublishedAt != "" {
		t.Fatalf("unavailable version borrowed package facts: %+v", local.Detail)
	}
}

func TestContractVersionHistoryProjectsOnlyProvenLifecycleStates(t *testing.T) {
	t.Parallel()

	publishedOne := contractVersionPublishProjection{Event: "01K1A2B3C4D5E6F7G8H9J0K1M7", At: "2026-06-01T10:00:00Z"}
	publishedOne.Actor.System = "atlas-one"
	publishedTwo := contractVersionPublishProjection{Event: "01K1A2B3C4D5E6F7G8H9J0K1M8", At: "2026-07-01T11:00:00Z"}
	publishedTwo.Actor.System = "atlas-two"
	deprecated := contractVersionHistory(publishedOne, cache.ContractVersion{Version: "1.0.0", State: "deprecated"})
	retired := contractVersionHistory(publishedTwo, cache.ContractVersion{Version: "2.0.0", State: "retired"})

	if len(deprecated) != 2 || deprecated[0].Transition != "publish" || deprecated[1].Transition != "deprecate" || deprecated[1].State != "deprecated" {
		t.Fatalf("deprecated history = %+v", deprecated)
	}
	if len(retired) != 3 || retired[0].Transition != "publish" || retired[1].Transition != "deprecate" || retired[2].Transition != "retire" || retired[2].State != "retired" {
		t.Fatalf("retired history = %+v", retired)
	}
	for version, tc := range map[string]struct {
		history   []ContractVersionHistory
		published contractVersionPublishProjection
	}{"1.0.0": {deprecated, publishedOne}, "2.0.0": {retired, publishedTwo}} {
		history := tc.history
		for _, lifecycle := range history[1:] {
			if lifecycle.EventID != "" || lifecycle.Actor != "" || lifecycle.At != "" {
				t.Errorf("%s fabricated unavailable lifecycle evidence: %+v", version, lifecycle)
			}
		}
		if history[0].EventID != tc.published.Event || history[0].Actor != tc.published.Actor.System || history[0].At != tc.published.At {
			t.Errorf("%s mixed publish evidence: %+v", version, history[0])
		}
	}
}

type testContractHistoryValidator struct{ engine *validate.Engine }

func htmlContractHistoryValidator(t *testing.T) testContractHistoryValidator {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return testContractHistoryValidator{engine: validate.New(corpus)}
}

func (v testContractHistoryValidator) ValidateHistoricalContractDocuments(ctx context.Context, documents space.ContractHistoryDocuments) error {
	descriptor, err := v.engine.ValidateDraft(validate.Draft{Path: documents.Descriptor.Path, Raw: documents.Descriptor.Raw})
	if err != nil {
		return err
	}
	if err := testHistoricalValidationError("descriptor", descriptor); err != nil {
		return err
	}
	event, err := v.engine.ValidateEvent(documents.PublishEvent.Raw, "0.0.0")
	if err != nil {
		return err
	}
	return testHistoricalValidationError("publish event", event)
}

func testHistoricalValidationError(label string, result validate.Result) error {
	if result.Valid {
		return nil
	}
	return fmt.Errorf("%s validation rejected: %+v", label, result.Violations)
}

func publishHTMLLegacyContractVersion(t *testing.T, repo, version, eventID, publishedAt, token string) string {
	t.Helper()

	const root = "atlas/provides/orders"
	descriptor := fmt.Sprintf(`---
schema: envelope/v1
id: XC-atlas-orders
type: contract
title: Orders contract
space: fixture-space
from: atlas
to: [checkout]
actor: {kind: agent, name: atlas-publisher}
created: 2026-05-01T09:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: %q
schema_format: json-schema-2020-12
compat_policy: backward
thread: thread:atlas-20260501-a1b2
---
Package description for %s.
`, version, token)
	files := map[string]string{
		"schema/orders.schema.json":    fmt.Sprintf("{\"title\":%q,\"type\":\"object\"}\n", token),
		"fixtures/valid/orders.json":   fmt.Sprintf("{\"marker\":%q}\n", token),
		"fixtures/invalid/orders.json": "null\n",
	}
	candidates := make([]contract.CandidateFile, 0, len(files))
	for name, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: name, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractTreeV1, nil, contract.Descriptor{}, candidates)
	if len(issues) != 0 {
		t.Fatalf("BuildCarriedSet legacy: %+v", issues)
	}
	event := fmt.Sprintf(`schema: event/v1
event: %s
space: fixture-space
subject: XC-atlas-orders
transition: publish
actor: {kind: agent, name: atlas-publisher, system: atlas}
at: %s
version: %q
digest: %s
`, eventID, publishedAt, version, set.AggregateDigest)

	writeHTMLContractFile(t, repo, root+"/contract.md", descriptor)
	for name, raw := range files {
		writeHTMLContractFile(t, repo, root+"/"+name, raw)
	}
	eventPath := "atlas/events/2026/" + eventID + ".yaml"
	writeHTMLContractFile(t, repo, eventPath, event)
	runHTMLContractGit(t, repo, nil, "add", "--", root, eventPath, "checkout/consumes.yaml")
	runHTMLContractGit(t, repo, []string{
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	}, "commit", "-q", "-m", "publish "+version)
	runHTMLContractGit(t, repo, nil, "push", "-q", "origin", "main")
	return strings.TrimSpace(runHTMLContractGit(t, repo, nil, "rev-parse", "HEAD"))
}

func publishHTMLContractVersion(t *testing.T, repo, version, eventID, publishedAt, token string, push bool) string {
	t.Helper()

	const root = "atlas/provides/orders"
	descriptor := fmt.Sprintf(`---
schema: envelope/v2
id: XC-atlas-orders
type: contract
title: Orders contract
space: fixture-space
from: atlas
to: [checkout]
actor: {kind: agent, name: atlas-publisher}
created: 2026-05-01T09:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: %q
schema_format: json-schema-2020-12
compat_policy: backward
thread: thread:atlas-20260501-a1b2
generated_from:
  tool: fixture-exporter
  source_digest: sha256:%s
artifacts:
  - path: schema/orders.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/orders.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/orders.schema.json
  - path: fixtures/invalid/orders.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/orders.schema.json
  - path: artifacts/changelog.md
    role: changelog
    normative: false
    media_type: text/markdown
---
Package description for %s.
`, version, strings.Repeat("d", 64), token)
	files := map[string]string{
		"schema/orders.schema.json":    fmt.Sprintf("{\"title\":%q,\"type\":\"object\"}\n", token),
		"fixtures/valid/orders.json":   fmt.Sprintf("{\"marker\":%q}\n", token),
		"fixtures/invalid/orders.json": "null\n",
		"artifacts/changelog.md":       "# " + token + "\n",
	}
	parsed, err := contract.ParseDescriptor([]byte(descriptor))
	if err != nil {
		t.Fatalf("ParseDescriptor: %v", err)
	}
	candidates := make([]contract.CandidateFile, 0, len(files))
	for name, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: name, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte(descriptor), parsed, candidates)
	if len(issues) != 0 {
		t.Fatalf("BuildCarriedSet: %+v", issues)
	}
	event := fmt.Sprintf(`schema: event/v2
event: %s
space: fixture-space
subject: XC-atlas-orders
transition: publish
actor: {kind: agent, name: atlas-publisher, system: atlas}
at: %s
version: %q
digest: %s
digest_profile: contract-set-v2
publication:
  intent_key: op-v1-%s
  candidate_intent_digest: sha256:%s
  version_selector: explicit:%s
  operation_key: op-v1-%s
`, eventID, publishedAt, version, set.AggregateDigest,
		strings.Repeat("a", 64), strings.Repeat("b", 64), version, strings.Repeat("c", 64))

	writeHTMLContractFile(t, repo, root+"/contract.md", descriptor)
	for name, raw := range files {
		writeHTMLContractFile(t, repo, root+"/"+name, raw)
	}
	eventPath := "atlas/events/2026/" + eventID + ".yaml"
	writeHTMLContractFile(t, repo, eventPath, event)
	runHTMLContractGit(t, repo, nil, "add", "--", root, eventPath, "checkout/consumes.yaml")
	runHTMLContractGit(t, repo, []string{
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	}, "commit", "-q", "-m", "publish "+version)
	if push {
		runHTMLContractGit(t, repo, nil, "push", "-q", "origin", "main")
	}
	return strings.TrimSpace(runHTMLContractGit(t, repo, nil, "rev-parse", "HEAD"))
}

func writeHTMLContractFile(t *testing.T, repo, name, content string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runHTMLContractGit(t *testing.T, repo string, extraEnv []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = repo
	if len(extraEnv) != 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output.String())
	}
	return output.String()
}

// TestToArtifactDetail_AttachmentsPassThroughWithNoSecondDecode is spec 04's
// (agent-exchange-2026-08) §11 amendment: ArtifactDetail.Attachments must
// carry cache.ShowResult.Attachments straight through — the SAME
// datapackage.AttachmentClaim values, already fully derived by
// internal/cache/store.go, not re-decoded or re-derived here. A defensive
// copy is still taken (matching Flags/To's own convention), so the two
// slices are equal by value but distinct in memory.
func TestToArtifactDetail_AttachmentsPassThroughWithNoSecondDecode(t *testing.T) {
	t.Parallel()
	claims := []datapackage.AttachmentClaim{
		{
			AttachmentManifestEntry: datapackage.AttachmentManifestEntry{
				Attachment: datapackage.Attachment{
					Ref: "sha256:aaaa", Digest: "sha256:aaaa",
					Verification: datapackage.VerificationRequired, Retention: "1h",
				},
				ExpiresAt: "2026-08-01T00:00:00Z",
			},
			VerificationClaim: "a verdict is required for these bytes",
			Lapsed:            true,
			LapsedOn:          "2026-08-01",
			LapseClaim:        "references bytes whose retention lapsed on 2026-08-01",
		},
	}

	detail, err := toArtifactDetail(cache.ShowResult{
		ID: "XW-test", Type: "work_request", Title: "carries bytes", Body: "body",
		Attachments: claims,
	})
	if err != nil {
		t.Fatalf("toArtifactDetail: %v", err)
	}
	if !reflect.DeepEqual(detail.Attachments, claims) {
		t.Fatalf("detail.Attachments = %+v, want %+v (byte-identical passthrough)", detail.Attachments, claims)
	}
	if len(detail.Attachments) > 0 && &detail.Attachments[0] == &claims[0] {
		t.Fatalf("detail.Attachments shares backing memory with the ShowResult's own slice, want a defensive copy")
	}
}

// TestToArtifactDetail_NoAttachmentsStaysEmpty guards the D4-style
// invariant at this layer: an artifact carrying none produces an empty
// (never nil-vs-non-nil-surprise) Attachments slice.
func TestToArtifactDetail_NoAttachmentsStaysEmpty(t *testing.T) {
	t.Parallel()
	detail, err := toArtifactDetail(cache.ShowResult{ID: "XR-test", Type: "requirement", Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("toArtifactDetail: %v", err)
	}
	if len(detail.Attachments) != 0 {
		t.Fatalf("detail.Attachments = %+v, want empty", detail.Attachments)
	}
}
