package livee2e

import (
	"errors"
	"testing"
)

func TestNewLocalHostSeamPinsAllThreeStrings(t *testing.T) {
	t.Parallel()

	seam := NewLocalHostSeam("http://127.0.0.1:54321", "/tmp/livee2e-logic/origin.git")

	if seam.APIRoot != "http://127.0.0.1:54321" {
		t.Fatalf("APIRoot = %q, want %q", seam.APIRoot, "http://127.0.0.1:54321")
	}
	if seam.RemoteURL != "/tmp/livee2e-logic/origin.git" {
		t.Fatalf("RemoteURL = %q, want %q", seam.RemoteURL, "/tmp/livee2e-logic/origin.git")
	}
	if seam.WebRoot != "/tmp/livee2e-logic/origin.git" {
		t.Fatalf("WebRoot = %q, want %q", seam.WebRoot, "/tmp/livee2e-logic/origin.git")
	}
}

// TestNewLocalHostSeamIsNotRealGitHub proves a seam built by NewLocalHostSeam
// validates cleanly and reports IsRealGitHub() == false — the property the
// logic tier's every guard rests on.
func TestNewLocalHostSeamIsNotRealGitHub(t *testing.T) {
	t.Parallel()

	seam := NewLocalHostSeam("http://127.0.0.1:54321", "/tmp/livee2e-logic/origin.git")

	if err := seam.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if seam.IsRealGitHub() {
		t.Fatalf("IsRealGitHub() = true for a local seam: %+v", seam)
	}
}

// TestNewLocalHostSeamMixedWithARealGitHubOriginIsRefused pins D-5's
// structural guard: NewLocalHostSeam does not — cannot — stop a caller from
// passing a real github.com URL as the "local" origin (it has no way to
// know), so the refusal has to come from Validate, not from the
// constructor. A seam whose APIRoot names a fake host but whose RemoteURL/
// WebRoot name real GitHub must be refused, never silently accepted as
// "local enough" — this is the property TestLogicMatrix's own
// non-real-GitHub assertion, and the whole "cannot reach a real API root"
// claim (plan D-5), is built on.
func TestNewLocalHostSeamMixedWithARealGitHubOriginIsRefused(t *testing.T) {
	t.Parallel()

	seam := NewLocalHostSeam("http://127.0.0.1:54321", "https://github.com/acme-org/live-e2e-space.git")

	err := seam.Validate()
	if err == nil {
		t.Fatalf("Validate() = nil for a mixed fake-API/real-GitHub-remote seam, want ErrHostSeamMixedHost")
	}
	if !errors.Is(err, ErrHostSeamMixedHost) {
		t.Fatalf("Validate() = %v, want errors.Is ErrHostSeamMixedHost", err)
	}
}
