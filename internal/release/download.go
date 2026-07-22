package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

// maxAssetBytes caps a single downloaded asset (the platform binary,
// SHA256SUMS, or a .cosign.bundle). 512 MiB is far above any real a2a asset
// while keeping the streamed disk write bounded (rails anti-pattern #13).
const maxAssetBytes = 512 << 20

// DownloadResult carries the destDir paths Download wrote. BundlePath is ""
// when the release carries no .cosign.bundle for the platform asset (P16
// signs best-effort; T2's interim signature slot does not consume the
// bundle yet, but Download still fetches it when present so a later
// sigstore-go Verifier has it on disk).
type DownloadResult struct {
	AssetPath  string
	SumsPath   string
	BundlePath string
}

// Download fetches the platform's a2a-<runtime.GOOS>-<runtime.GOARCH>
// asset, SHA256SUMS, and (if the release carries one) that asset's
// .cosign.bundle into destDir (T1 step 2). destDir is caller-chosen: the
// verb passes the running binary's own directory so the later Swap rename
// stays on the same filesystem.
//
// token selects the fetch path per asset (T3/T1): non-empty => the private
// release-asset API URL with Accept: application/octet-stream + Bearer;
// empty => the public, tokenless BrowserDownloadURL. A 404 for the
// platform asset (or a missing SHA256SUMS entry) is a clear error; any
// partial file this call created is cleaned up before returning.
func Download(ctx context.Context, client *http.Client, token string, rel Release, destDir string) (DownloadResult, error) {
	const op = "Download"
	if client == nil {
		client = http.DefaultClient
	}

	platformName := fmt.Sprintf("a2a-%s-%s", runtime.GOOS, runtime.GOARCH)
	asset, ok := findAsset(rel, platformName)
	if !ok {
		return DownloadResult{}, &Error{Op: op, Input: platformName, Err: ErrAssetNotFound}
	}
	assetPath := filepath.Join(destDir, asset.Name)
	if err := fetchAsset(ctx, client, token, asset, assetPath); err != nil {
		cleanupPaths(assetPath)
		return DownloadResult{}, err
	}
	// The asset is the release BINARY — it must carry the execute bit before
	// Apply's post-download `version` self-check execs it (os.Create leaves it
	// 0644, so without this the self-check EACCES-fails on every real run and
	// no swap can ever complete). Swap re-chmods too, but the self-check runs
	// first — this is the one that makes the pipeline work in production.
	if err := os.Chmod(assetPath, 0o755); err != nil { //nolint:gosec // reason: this is the downloaded a2a BINARY — it must be executable (0600 would make the self-check exec EACCES-fail), not a secret
		cleanupPaths(assetPath)
		return DownloadResult{}, &Error{Op: op, Input: assetPath, Err: fmt.Errorf("%w: chmod: %w", ErrDownloadFailed, err)}
	}

	sumsAsset, ok := findAsset(rel, "SHA256SUMS")
	if !ok {
		cleanupPaths(assetPath)
		return DownloadResult{}, &Error{Op: op, Input: "SHA256SUMS", Err: ErrAssetNotFound}
	}
	sumsPath := filepath.Join(destDir, sumsAsset.Name)
	if err := fetchAsset(ctx, client, token, sumsAsset, sumsPath); err != nil {
		cleanupPaths(assetPath, sumsPath)
		return DownloadResult{}, err
	}

	var bundlePath string
	bundleName := asset.Name + ".cosign.bundle"
	if bundleAsset, ok := findAsset(rel, bundleName); ok {
		bundlePath = filepath.Join(destDir, bundleAsset.Name)
		if err := fetchAsset(ctx, client, token, bundleAsset, bundlePath); err != nil {
			cleanupPaths(assetPath, sumsPath, bundlePath)
			return DownloadResult{}, err
		}
	}

	return DownloadResult{AssetPath: assetPath, SumsPath: sumsPath, BundlePath: bundlePath}, nil
}

// fetchAsset downloads one asset to destPath via the token-selected path
// (private release-asset API vs public browser_download_url).
func fetchAsset(ctx context.Context, client *http.Client, token string, asset Asset, destPath string) error {
	const op = "Download"

	url := asset.BrowserDownloadURL
	if token != "" {
		url = asset.URL
	}
	if url == "" {
		return &Error{Op: op, Input: asset.Name, Err: ErrAssetNotFound}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &Error{Op: op, Input: asset.Name, Err: fmt.Errorf("%w: build request: %w", ErrDownloadFailed, err)}
	}
	if token != "" {
		httpReq.Header.Set("Accept", "application/octet-stream")
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return &Error{Op: op, Input: asset.Name, Err: fmt.Errorf("%w: %w", ErrDownloadFailed, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return &Error{Op: op, Input: asset.Name, Err: ErrAssetNotFound}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Op: op, Input: asset.Name, Err: fmt.Errorf("%w: status %d", ErrDownloadFailed, resp.StatusCode)}
	}

	out, err := os.Create(destPath)
	if err != nil {
		return &Error{Op: op, Input: destPath, Err: fmt.Errorf("%w: %w", ErrDownloadFailed, err)}
	}
	defer func() { _ = out.Close() }()

	// Bounded read (rails: every external input is size-capped, matching
	// source.go's io.LimitReader on the releases-list JSON). A truncation at
	// the cap makes the downstream checksum verify fail closed — a compromised
	// host can never stream unbounded bytes into the binary's own directory.
	if _, err := io.Copy(out, io.LimitReader(resp.Body, maxAssetBytes)); err != nil {
		return &Error{Op: op, Input: asset.Name, Err: fmt.Errorf("%w: %w", ErrDownloadFailed, err)}
	}
	return nil
}

// cleanupPaths removes every non-empty path, best-effort (a cleanup failure
// is not itself actionable — the caller already has a primary error to
// report).
func cleanupPaths(paths ...string) {
	for _, p := range paths {
		if p != "" {
			_ = os.Remove(p)
		}
	}
}
