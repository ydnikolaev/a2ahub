package release

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// NewCohortDetailChecker extends the version-only background check with a
// fail-closed, separately signed future What’s New cache. Failure to obtain or
// verify detail never suppresses the latest-version fact and never exposes
// unverified prose.
func NewCohortDetailChecker(
	source Source,
	client *http.Client,
	token string,
	checkPath string,
	detailPath string,
	protocol int,
	verifier Verifier,
	decoder DetailDecoder,
	now func() time.Time,
) func(context.Context) {
	if client == nil {
		client = http.DefaultClient
	}
	return func(ctx context.Context) {
		rel, err := source.Latest(ctx)
		if err != nil {
			return
		}
		checkedAt := now().UTC()
		if err := WriteCheck(checkPath, CheckState{
			CheckedAt: checkedAt, Latest: rel.Version, Source: source.Name(),
		}); err != nil {
			return
		}

		writeFallback := func(status DetailStatus) {
			_ = WriteDetailCache(detailPath, detailFallback(status, rel.Version, "", "", ""))
		}
		if verifier == nil || decoder == nil {
			writeFallback(DetailUnverified)
			return
		}

		parent := filepath.Dir(detailPath)
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return
		}
		stage, err := os.MkdirTemp(parent, ".detail-stage-*")
		if err != nil {
			return
		}
		defer func() { _ = os.RemoveAll(stage) }()

		cohortPath, ok := fetchDetailAsset(ctx, client, token, rel, CohortAssetName, stage)
		if !ok {
			writeFallback(DetailMissing)
			return
		}
		if _, ok := fetchDetailAsset(ctx, client, token, rel, "SHA256SUMS", stage); !ok {
			writeFallback(DetailUnverified)
			return
		}
		// KeylessVerifier expects the detached bundle beside the subject.
		if _, ok := fetchDetailAsset(ctx, client, token, rel, CohortAssetName+".cosign.bundle", stage); !ok {
			writeFallback(DetailUnverified)
			return
		}
		cohort, err := VerifyCohort(ctx, cohortPath, rel, verifier)
		if err != nil || cohort.manifest.CheckCompatibility(protocol) != nil {
			writeFallback(DetailUnverified)
			return
		}
		component, ok := releaseNotesComponent(cohort)
		if !ok {
			writeFallback(DetailMissing)
			return
		}
		notesPath, ok := fetchDetailAsset(ctx, client, token, rel, component.Name, stage)
		if !ok {
			_ = WriteDetailCache(detailPath, detailFallback(
				DetailMissing, rel.Version, component.Name, component.Schema, component.SHA256,
			))
			return
		}
		_, _ = fetchDetailAsset(ctx, client, token, rel, component.Name+".cosign.bundle", stage)
		cache := ResolveDetail(ctx, DetailOptions{
			Cohort: cohort, AssetPath: notesPath, Release: rel,
			Verifier: verifier, Decoder: decoder, Now: checkedAt,
		})
		_ = WriteDetailCache(detailPath, cache)
	}
}

func fetchDetailAsset(
	ctx context.Context,
	client *http.Client,
	token string,
	rel Release,
	name string,
	dir string,
) (string, bool) {
	asset, ok := findAsset(rel, name)
	if !ok {
		return "", false
	}
	path := filepath.Join(dir, name)
	if err := fetchAsset(ctx, client, token, asset, path); err != nil {
		_ = os.Remove(path)
		return "", false
	}
	return path, true
}
