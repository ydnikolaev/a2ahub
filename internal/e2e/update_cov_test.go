// update_cov_test.go — P26 wave 14c, `a2a update` (OP-217, spec 19)
// coverage against a LOCAL fake release-asset fixture: resolve (GitHubSource
// against an httptest server, never the real GitHub API) -> verify (checksum
// always-first) -> refuse-on-bad-checksum. The live self-swap path stays
// P19's own unit/manual territory (spec 26 §1 skip-set justification).
//
// DEVIATION (reported in this wave's structured return, not silently): this
// exercises internal/release's real Latest/Resolve/Apply pipeline directly,
// NOT cli.UpdateCommand.Run. cli.UpdateCommand's DI seams (source,
// httpClient, verifier, runner, ...) are all unexported fields with no env
// override for the API base URL (cmd/a2a/wire.go's NewUpdateCommand always
// resolves the real "https://api.github.com"), so the CLI verb itself is
// unreachable from this package without editing internal/cli (off-limits,
// not in this brief's allowlist). internal/release is the exact primitive
// set cmd_update.go orchestrates (its own doc comment: "this file
// ORCHESTRATES the shipped internal/release primitives ... release.Apply is
// the package's only safe entry point") — Latest/Resolve/Apply are real,
// unstubbed production code, so this proves the update PIPELINE the verb
// wires together, at the boundary the verb itself cannot be exec'd or
// direct-constructed past. This also adds internal/release to internal/e2e's
// import surface (not previously imported here) — within ADR-001's
// core-package import ceiling, but outside this package's own doc-comment
// list and spec 26's "may import: nothing new" line; the lead should treat
// this test as evidence for the update pipeline, not literal verb-level
// coverage, when deciding whether to flip the coverage.go skip row.
package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/release"
)

// fakeReleaseServer serves a single-release GitHub releases-list response
// (v0.3.0, matching the v*.*.* grammar, not draft/pre-release) plus the
// platform asset + SHA256SUMS the Download step fetches — the URLs are
// self-referential (the handler closes over srv, assigned after
// httptest.NewServer returns). corruptSums flips the published checksum so
// the refuse-on-bad-checksum path is real (a tampered/mismatched asset),
// never simulated by skipping verification.
func fakeReleaseServer(t *testing.T, assetBytes []byte, corruptSums bool) *httptest.Server {
	t.Helper()
	platform := fmt.Sprintf("a2a-%s-%s", runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(assetBytes)
	hexsum := hex.EncodeToString(sum[:])
	if corruptSums {
		hexsum = strings.Repeat("0", 64)
	}
	sums := fmt.Sprintf("%s  %s\n", hexsum, platform)

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `[{
			"tag_name": "v0.3.0",
			"target_commitish": "deadbeef",
			"draft": false,
			"prerelease": false,
			"assets": [
				{"name": %q, "browser_download_url": %q},
				{"name": "SHA256SUMS", "browser_download_url": %q}
			]
		}]`, platform, srv.URL+"/asset", srv.URL+"/sums")
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(assetBytes) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sums)) })

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// matchingUpdateRunner fakes the post-download self-check exec (SelfCheckVersion)
// reporting the target version, so Apply's happy path reaches Swap without
// spawning a real subprocess.
func matchingUpdateRunner(version string) release.Runner {
	return func(_ context.Context, _ string, _ ...string) (string, error) {
		return fmt.Sprintf("a2a %s (deadbeef)\n", version), nil
	}
}

// TestT3UpdateResolveVerifyRefuseChecksum drives the real
// GitHubSource.Latest -> release.Resolve -> release.Apply pipeline
// (cmd_update.go's own orchestration) against a LOCAL fake release-asset
// fixture: a happy resolve+verify+swap onto a throwaway exec path, and a
// refusal when the published SHA256SUMS does not match the asset —
// ErrChecksumMismatch, never gateable by --allow-unsigned (T2/D-013), so the
// running "binary" (a throwaway temp file, never the real test binary) is
// provably untouched.
func TestT3UpdateResolveVerifyRefuseChecksum(t *testing.T) {
	t.Parallel()
	assetBytes := []byte("FAKE-RELEASE-ASSET-v0.3.0")

	t.Run("happy path: resolve+verify+swap", func(t *testing.T) {
		t.Parallel()
		srv := fakeReleaseServer(t, assetBytes, false)
		src := &release.GitHubSource{Client: http.DefaultClient, BaseURL: srv.URL, Repo: "ydnikolaev/a2ahub"}

		rel, err := src.Latest(context.Background())
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if rel.Version != "0.3.0" {
			t.Fatalf("Latest.Version = %q, want 0.3.0", rel.Version)
		}

		dec, err := release.Resolve("0.1.0", rel.Version, rel, "", "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if dec.UpToDate {
			t.Fatalf("Resolve: UpToDate = true, want an available update (0.1.0 -> 0.3.0)")
		}

		dir := t.TempDir()
		execPath := filepath.Join(dir, "a2a")
		if err := os.WriteFile(execPath, []byte("OLD-BINARY"), 0o755); err != nil {
			t.Fatalf("seed exec: %v", err)
		}

		res, err := release.Apply(context.Background(), dec.Current, release.ApplyOptions{
			Target:        rel,
			ExecPath:      execPath,
			AllowUnsigned: true, // no cosign bundle in this fixture — signature is UNVERIFIED, gated ONLY by this flag
			Run:           matchingUpdateRunner(rel.Version),
		})
		if err != nil {
			t.Fatalf("Apply: unexpected error: %v", err)
		}
		if res.FromVersion != "0.1.0" || res.ToVersion != "0.3.0" {
			t.Fatalf("Apply result = %+v, want 0.1.0 -> 0.3.0", res)
		}
		got, err := os.ReadFile(execPath)
		if err != nil {
			t.Fatalf("read swapped exec: %v", err)
		}
		if string(got) != string(assetBytes) {
			t.Fatalf("exec was not swapped to the fetched asset: got %q", got)
		}
	})

	t.Run("refuse on bad checksum: never swaps, even with --allow-unsigned", func(t *testing.T) {
		t.Parallel()
		srv := fakeReleaseServer(t, assetBytes, true) // corrupted SHA256SUMS
		src := &release.GitHubSource{Client: http.DefaultClient, BaseURL: srv.URL, Repo: "ydnikolaev/a2ahub"}

		rel, err := src.Latest(context.Background())
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}

		dir := t.TempDir()
		execPath := filepath.Join(dir, "a2a")
		orig := []byte("OLD-BINARY")
		if err := os.WriteFile(execPath, orig, 0o755); err != nil {
			t.Fatalf("seed exec: %v", err)
		}

		_, err = release.Apply(context.Background(), "0.1.0", release.ApplyOptions{
			Target:        rel,
			ExecPath:      execPath,
			AllowUnsigned: true, // must NOT rescue a checksum mismatch
			Run:           matchingUpdateRunner(rel.Version),
		})
		if err == nil {
			t.Fatal("Apply: want a checksum-mismatch error, got nil (refusal did not fire)")
		}
		if !errors.Is(err, release.ErrChecksumMismatch) {
			t.Fatalf("Apply err = %v, want release.ErrChecksumMismatch", err)
		}
		got, rErr := os.ReadFile(execPath)
		if rErr != nil {
			t.Fatalf("read exec: %v", rErr)
		}
		if string(got) != string(orig) {
			t.Fatalf("running exec was modified on a refused (bad-checksum) update: got %q", got)
		}
	})
}
