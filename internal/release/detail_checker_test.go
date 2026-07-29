package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type checkerSource struct {
	release Release
}

func (s checkerSource) Latest(context.Context) (Release, error) { return s.release, nil }
func (checkerSource) Name() string                              { return "test" }

type checkerVerifier struct{}

func (checkerVerifier) Verify(context.Context, string, Release) error { return nil }

type checkerTransport map[string][]byte

func (t checkerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	name := req.URL.Path[1:]
	raw, ok := t[name]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(string(raw))),
		Header:     make(http.Header),
	}, nil
}

func TestCohortDetailCheckerWritesOnlyVerifiedProjection(t *testing.T) {
	t.Parallel()

	notesRaw := []byte("trusted release notes")
	notesDigest := testDigest(notesRaw)
	manifest := CohortManifest{
		Schema: CohortSchemaV1, ProductVersion: "1.2.3",
		Protocol: ProtocolRange{Min: 1, Max: 1},
		Components: []CohortComponent{
			{Kind: ComponentCLI, Name: "a2a-darwin-arm64", SHA256: strings.Repeat("1", 64), ProductVersion: "1.2.3", ProtocolVersion: 1},
			{Kind: ComponentMacOS, Name: "a2a-notifier.zip", SHA256: strings.Repeat("2", 64), ProductVersion: "1.2.3", ProtocolVersion: 1, BundleID: MacOSBundleID, TeamID: "TEAM123"},
			{Kind: ComponentVSCode, Name: "a2ahub-notifications.vsix", SHA256: strings.Repeat("3", 64), ProductVersion: "1.2.3", ProtocolVersion: 1, Publisher: "a2ahub", ExtensionID: "a2ahub.a2ahub-notifications"},
			{Kind: ComponentReleaseNotes, Name: "release-notes-1.2.3.yaml", SHA256: notesDigest, ProductVersion: "1.2.3", ProtocolVersion: 1, Schema: ReleaseNotesSchemaV1},
		},
	}
	cohortRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal cohort: %v", err)
	}
	sums := []byte(testDigest(cohortRaw) + "  " + CohortAssetName + "\n" +
		notesDigest + "  release-notes-1.2.3.yaml\n")
	assets := checkerTransport{
		CohortAssetName: cohortRaw, "SHA256SUMS": sums,
		CohortAssetName + ".cosign.bundle":       []byte("bundle"),
		"release-notes-1.2.3.yaml":               notesRaw,
		"release-notes-1.2.3.yaml.cosign.bundle": []byte("bundle"),
	}
	releaseAssets := make([]Asset, 0, len(assets))
	for name := range assets {
		releaseAssets = append(releaseAssets, Asset{Name: name, BrowserDownloadURL: "https://example/" + name})
	}
	rel := Release{Tag: "v1.2.3", Version: "1.2.3", Assets: releaseAssets}
	root := t.TempDir()
	checkPath, detailPath := root+"/update-check.json", root+"/release-detail.json"
	checker := NewCohortDetailChecker(
		checkerSource{release: rel}, &http.Client{Transport: assets}, "",
		checkPath, detailPath, 1, checkerVerifier{},
		detailDecoderFunc(func([]byte) (Detail, error) {
			return validDetail(), nil
		}),
		func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) },
	)
	checker(context.Background())

	if latest, _ := ReadLatest(checkPath, time.Date(2026, 7, 29, 12, 1, 0, 0, time.UTC), time.Hour); latest != "1.2.3" {
		t.Fatalf("latest = %q", latest)
	}
	detail, ok := ReadDetailCache(detailPath, "1.2.3")
	if !ok || detail.Status != DetailVerified || detail.Detail == nil {
		t.Fatalf("detail = %+v, ok=%v", detail, ok)
	}
}

func testDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
