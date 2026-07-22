package release

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
)

// applyFixture wires an httptest release + a fake current binary on disk so
// Apply's full Download→Verify→SelfCheck→Swap pipeline runs end to end without
// touching the network or a real subprocess.
type applyFixture struct {
	target     Release
	execPath   string
	assetBytes []byte
	origBytes  []byte
}

func newApplyFixture(t *testing.T, corruptSums bool) applyFixture {
	t.Helper()
	platform := fmt.Sprintf("a2a-%s-%s", runtime.GOOS, runtime.GOARCH)
	assetBytes := []byte("NEW-BINARY-CONTENTS-v0.3.0")

	sum := sha256.Sum256(assetBytes)
	hexsum := hex.EncodeToString(sum[:])
	if corruptSums {
		hexsum = strings.Repeat("0", 64)
	}
	sums := fmt.Sprintf("%s  %s\n", hexsum, platform)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(assetBytes)
		case "/sums":
			_, _ = w.Write([]byte(sums))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	execPath := filepath.Join(dir, "a2a")
	origBytes := []byte("OLD-BINARY-CONTENTS-v0.1.0")
	if err := os.WriteFile(execPath, origBytes, 0o755); err != nil {
		t.Fatalf("seed exec: %v", err)
	}

	return applyFixture{
		target: Release{
			Tag:     "v0.3.0",
			Version: "0.3.0",
			Commit:  "abc1234",
			Assets: []Asset{
				{Name: platform, BrowserDownloadURL: srv.URL + "/asset"},
				{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/sums"},
			},
		},
		execPath:   execPath,
		assetBytes: assetBytes,
		origBytes:  origBytes,
	}
}

func matchingRunner(version string) Runner {
	return func(_ context.Context, _ string, _ ...string) (string, error) {
		return fmt.Sprintf("a2a %s (abc1234)\n", version), nil
	}
}

func assertUnswapped(t *testing.T, f applyFixture) {
	t.Helper()
	got, err := os.ReadFile(f.execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != string(f.origBytes) {
		t.Fatalf("running binary was modified on a failed update: got %q", got)
	}
	// The downloaded temp asset must be gone (T1 step 3: "delete temp").
	tmp := filepath.Join(filepath.Dir(f.execPath), fmt.Sprintf("a2a-%s-%s", runtime.GOOS, runtime.GOARCH))
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temp asset not cleaned up: stat err = %v", err)
	}
}

func TestApply_HappyPath_AllowUnsigned(t *testing.T) {
	f := newApplyFixture(t, false)
	res, err := Apply(context.Background(), "0.1.0", ApplyOptions{
		Target:        f.target,
		ExecPath:      f.execPath,
		AllowUnsigned: true, // interim: signature is UNVERIFIED, so this is required
		Run:           matchingRunner("0.3.0"),
	})
	if err != nil {
		t.Fatalf("Apply: unexpected error %v", err)
	}
	if res.FromVersion != "0.1.0" || res.ToVersion != "0.3.0" || res.Commit != "abc1234" {
		t.Fatalf("delta = %+v, want 0.1.0->0.3.0 (abc1234)", res)
	}
	got, err := os.ReadFile(f.execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != string(f.assetBytes) {
		t.Fatalf("binary not swapped: got %q want %q", got, f.assetBytes)
	}
	// Sums temp cleaned after a successful swap.
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.execPath), "SHA256SUMS")); !os.IsNotExist(err) {
		t.Fatalf("SHA256SUMS temp not cleaned: %v", err)
	}
}

func TestApply_ChecksumMismatch_NeverSwaps_EvenAllowUnsigned(t *testing.T) {
	f := newApplyFixture(t, true) // corrupt sums
	_, err := Apply(context.Background(), "0.1.0", ApplyOptions{
		Target:        f.target,
		ExecPath:      f.execPath,
		AllowUnsigned: true, // must NOT rescue a checksum mismatch
		Run:           matchingRunner("0.3.0"),
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v, want ErrChecksumMismatch", err)
	}
	assertUnswapped(t, f)
}

func TestApply_UnverifiedSignature_RefusedWithoutAllowUnsigned(t *testing.T) {
	f := newApplyFixture(t, false)
	_, err := Apply(context.Background(), "0.1.0", ApplyOptions{
		Target:        f.target,
		ExecPath:      f.execPath,
		AllowUnsigned: false, // checksum passes, signature UNVERIFIED => refuse
		Run:           matchingRunner("0.3.0"),
	})
	if !errors.Is(err, ErrSignatureUnverified) {
		t.Fatalf("err = %v, want ErrSignatureUnverified", err)
	}
	assertUnswapped(t, f)
}

func TestApply_SelfCheckMismatch_NoSwap(t *testing.T) {
	f := newApplyFixture(t, false)
	_, err := Apply(context.Background(), "0.1.0", ApplyOptions{
		Target:        f.target,
		ExecPath:      f.execPath,
		AllowUnsigned: true,
		Run:           matchingRunner("9.9.9"), // stamped version != target
	})
	if !errors.Is(err, ErrSelfCheckFailed) {
		t.Fatalf("err = %v, want ErrSelfCheckFailed", err)
	}
	assertUnswapped(t, f)
}
