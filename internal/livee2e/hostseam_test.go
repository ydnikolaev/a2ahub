package livee2e

import (
	"errors"
	"strings"
	"testing"
)

// TestNewLiveHostSeamPinsTodaysThreeStrings pins the exact strings a live
// run has always produced, verbatim, so a future edit that "tidies" the
// construction shows up as a diff in THIS test, not as a silently different
// remote a live run discovers the hard way.
func TestNewLiveHostSeamPinsTodaysThreeStrings(t *testing.T) {
	t.Parallel()

	seam := NewLiveHostSeam("acme-org", "live-e2e-space")

	if seam.APIRoot != "https://api.github.com" {
		t.Fatalf("APIRoot = %q, want %q", seam.APIRoot, "https://api.github.com")
	}
	if seam.RemoteURL != "https://github.com/acme-org/live-e2e-space.git" {
		t.Fatalf("RemoteURL = %q, want %q", seam.RemoteURL, "https://github.com/acme-org/live-e2e-space.git")
	}
	if seam.WebRoot != "https://github.com/acme-org/live-e2e-space" {
		t.Fatalf("WebRoot = %q, want %q", seam.WebRoot, "https://github.com/acme-org/live-e2e-space")
	}
}

func TestHostSeamCloneURLCarriesDotGitAndWebRootDoesNot(t *testing.T) {
	t.Parallel()

	seam := NewLiveHostSeam("acme-org", "live-e2e-space")

	if !strings.HasSuffix(seam.CloneURL(), ".git") {
		t.Fatalf("CloneURL() = %q, want a .git suffix", seam.CloneURL())
	}
	if strings.HasSuffix(seam.WebRoot, ".git") {
		t.Fatalf("WebRoot = %q, must not carry a .git suffix (a2a init --space takes the plain root)", seam.WebRoot)
	}
	if seam.CloneURL() != seam.WebRoot+".git" {
		t.Fatalf("CloneURL() = %q, want WebRoot+.git = %q", seam.CloneURL(), seam.WebRoot+".git")
	}
}

func TestHostSeamPullURLMatchesTheProductFormat(t *testing.T) {
	t.Parallel()

	seam := NewLiveHostSeam("acme-org", "live-e2e-space")

	got := seam.PullURL(1549)
	want := "https://github.com/acme-org/live-e2e-space/pull/1549"
	if got != want {
		t.Fatalf("PullURL(1549) = %q, want %q", got, want)
	}
}

func TestHostSeamIsRealGitHub(t *testing.T) {
	t.Parallel()

	live := NewLiveHostSeam("acme-org", "live-e2e-space")
	if !live.IsRealGitHub() {
		t.Fatalf("NewLiveHostSeam(...).IsRealGitHub() = false, want true")
	}

	local := HostSeam{
		APIRoot:   "http://127.0.0.1:8080",
		RemoteURL: "/tmp/livee2e-local/space.git",
		WebRoot:   "/tmp/livee2e-local/space",
	}
	if local.IsRealGitHub() {
		t.Fatalf("a local seam's IsRealGitHub() = true, want false")
	}
}

func TestHostSeamValidateAcceptsTheLiveSeam(t *testing.T) {
	t.Parallel()

	seam := NewLiveHostSeam("acme-org", "live-e2e-space")
	if err := seam.Validate(); err != nil {
		t.Fatalf("Validate() on the live seam = %v, want nil", err)
	}
}

func TestHostSeamValidateNamesEachEmptyField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		seam  HostSeam
		field string
	}{
		{"APIRoot empty", HostSeam{RemoteURL: "https://github.com/o/r.git", WebRoot: "https://github.com/o/r"}, "APIRoot"},
		{"RemoteURL empty", HostSeam{APIRoot: "https://api.github.com", WebRoot: "https://github.com/o/r"}, "RemoteURL"},
		{"WebRoot empty", HostSeam{APIRoot: "https://api.github.com", RemoteURL: "https://github.com/o/r.git"}, "WebRoot"},
		{"all empty", HostSeam{}, "APIRoot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.seam.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error naming %s", tc.field)
			}
			if !errors.Is(err, ErrHostSeamIncomplete) {
				t.Fatalf("Validate() = %v, want errors.Is ErrHostSeamIncomplete", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %q, want it to name the empty field %q", err.Error(), tc.field)
			}
		})
	}
}

func TestHostSeamValidateRefusesMixedRealGitHubAndLocalValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		seam HostSeam
	}{
		{
			name: "real GitHub API root, local remote and web root",
			seam: HostSeam{
				APIRoot:   "https://api.github.com",
				RemoteURL: "/tmp/livee2e-local/space.git",
				WebRoot:   "/tmp/livee2e-local/space",
			},
		},
		{
			name: "local API root, real GitHub remote and web root",
			seam: HostSeam{
				APIRoot:   "http://127.0.0.1:8080",
				RemoteURL: "https://github.com/acme-org/live-e2e-space.git",
				WebRoot:   "https://github.com/acme-org/live-e2e-space",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.seam.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error for a mixed real-GitHub/local seam")
			}
			if !errors.Is(err, ErrHostSeamMixedHost) {
				t.Fatalf("Validate() = %v, want errors.Is ErrHostSeamMixedHost", err)
			}
		})
	}
}
