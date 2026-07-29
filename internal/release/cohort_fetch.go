package release

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// FetchVerifiedComponent obtains one companion from the same signed release
// cohort as rel. Nothing is published into destDir until the manifest,
// compatibility window, component checksum, and component signature pass.
func FetchVerifiedComponent(
	ctx context.Context,
	client *http.Client,
	token string,
	rel Release,
	kind ComponentKind,
	protocol int,
	destDir string,
	verifier Verifier,
) (CohortComponent, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if verifier == nil {
		return CohortComponent{}, "", fmt.Errorf("%w: verifier is required", ErrCohortVerification)
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return CohortComponent{}, "", err
	}
	stage, err := os.MkdirTemp(destDir, ".cohort-fetch-*")
	if err != nil {
		return CohortComponent{}, "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	cohortPath, ok := fetchDetailAsset(ctx, client, token, rel, CohortAssetName, stage)
	if !ok {
		return CohortComponent{}, "", fmt.Errorf("%w: %s", ErrCohortAssetInvalid, CohortAssetName)
	}
	sumsPath, ok := fetchDetailAsset(ctx, client, token, rel, "SHA256SUMS", stage)
	if !ok {
		return CohortComponent{}, "", fmt.Errorf("%w: SHA256SUMS", ErrCohortAssetInvalid)
	}
	cohortBundle, ok := fetchDetailAsset(ctx, client, token, rel, CohortAssetName+".cosign.bundle", stage)
	if !ok {
		return CohortComponent{}, "", fmt.Errorf("%w: cohort signature", ErrCohortVerification)
	}
	verified, err := VerifyCohort(ctx, cohortPath, rel, verifier)
	if err != nil {
		return CohortComponent{}, "", err
	}
	if err := verified.manifest.CheckCompatibility(protocol); err != nil {
		return CohortComponent{}, "", err
	}
	var component CohortComponent
	for _, candidate := range verified.manifest.Components {
		if candidate.Kind == kind {
			if component.Name != "" {
				return CohortComponent{}, "", fmt.Errorf("%w: multiple %s components", ErrCohortInvalid, kind)
			}
			component = candidate
		}
	}
	if component.Name == "" {
		return CohortComponent{}, "", fmt.Errorf("%w: no %s component", ErrCohortInvalid, kind)
	}
	assetPath, ok := fetchDetailAsset(ctx, client, token, rel, component.Name, stage)
	if !ok {
		return CohortComponent{}, "", fmt.Errorf("%w: %s", ErrCohortAssetInvalid, component.Name)
	}
	bundlePath, ok := fetchDetailAsset(ctx, client, token, rel, component.Name+".cosign.bundle", stage)
	if !ok {
		return CohortComponent{}, "", fmt.Errorf("%w: %s signature", ErrCohortAssetInvalid, component.Name)
	}
	if err := verifier.Verify(ctx, assetPath, rel); err != nil {
		return CohortComponent{}, "", fmt.Errorf("%w: %w", ErrCohortAssetInvalid, err)
	}
	got, err := sha256File(assetPath)
	if err != nil || got != component.SHA256 {
		return CohortComponent{}, "", fmt.Errorf("%w: %s digest", ErrCohortAssetInvalid, component.Name)
	}

	for _, path := range []string{cohortPath, cohortBundle, sumsPath, assetPath, bundlePath} {
		if err := publishCohortFile(path, filepath.Join(destDir, filepath.Base(path))); err != nil {
			return CohortComponent{}, "", err
		}
	}
	return component, filepath.Join(destDir, component.Name), nil
}

func publishCohortFile(source, target string) error {
	if err := os.Chmod(source, 0o600); err != nil {
		return err
	}
	_ = os.Remove(target)
	return os.Rename(source, target)
}
