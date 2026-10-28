package livee2e

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
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

// TestHostSeamValidateAcceptsABareRepoSeamWithNoAPIRoot is spec 07 §8 row 6's
// first half: a bare repository on this machine's disk has no API, and a seam
// of that kind must validate with the API root empty, where every GitHub-shaped
// seam refuses exactly that.
func TestHostSeamValidateAcceptsABareRepoSeamWithNoAPIRoot(t *testing.T) {
	t.Parallel()

	seam := newBareRepoHostSeam("/tmp/livee2e-local/space.git")
	if seam.APIRoot != "" {
		t.Fatalf("a bare-repo seam carries APIRoot %q, want none", seam.APIRoot)
	}
	if err := seam.Validate(); err != nil {
		t.Fatalf("Validate() on a bare-repo seam with no API root = %v, want nil", err)
	}
	if seam.IsRealGitHub() {
		t.Fatalf("a bare-repo seam's IsRealGitHub() = true, want false")
	}
	if got := seam.CloneURL(); got != "/tmp/livee2e-local/space.git" {
		t.Fatalf("CloneURL() = %q, want the repository path itself", got)
	}

	// The same three strings WITHOUT the kind are the half-configured
	// GitHub-shaped seam the original Validate has always refused: the kind is
	// what makes an empty API root legitimate, nothing else.
	asGitHub := seam
	asGitHub.Kind = HostKindGitHub
	if err := asGitHub.Validate(); !errors.Is(err, ErrHostSeamIncomplete) {
		t.Fatalf("a GitHub-kind seam with no API root: Validate() = %v, want errors.Is ErrHostSeamIncomplete", err)
	}
}

// TestHostSeamValidateRefusesABareRepoSeamThatMixesInAnAPIOrGitHub is row 6's
// second half, on the bare-repo side: whatever real-GitHub or API value finds
// its way into a bare-repo seam is refused as a mix, and named.
func TestHostSeamValidateRefusesABareRepoSeamThatMixesInAnAPIOrGitHub(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		seam HostSeam
	}{
		{"real GitHub API root", HostSeam{Kind: HostKindBareRepo, APIRoot: "https://api.github.com", RemoteURL: "/tmp/livee2e-local/space.git", WebRoot: "/tmp/livee2e-local/space.git"}},
		{"loopback API root", HostSeam{Kind: HostKindBareRepo, APIRoot: "http://127.0.0.1:8080", RemoteURL: "/tmp/livee2e-local/space.git", WebRoot: "/tmp/livee2e-local/space.git"}},
		{"real GitHub remote", HostSeam{Kind: HostKindBareRepo, RemoteURL: "https://github.com/acme-org/live-e2e-space.git", WebRoot: "/tmp/livee2e-local/space.git"}},
		{"real GitHub web root", HostSeam{Kind: HostKindBareRepo, RemoteURL: "/tmp/livee2e-local/space.git", WebRoot: "https://github.com/acme-org/live-e2e-space"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.seam.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error for a bare-repo seam that mixes in %s", tc.name)
			}
			if !errors.Is(err, ErrHostSeamMixedHost) {
				t.Fatalf("Validate() = %v, want errors.Is ErrHostSeamMixedHost", err)
			}
		})
	}
}

func TestHostSeamValidateNamesEachEmptyBareRepoField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		seam  HostSeam
		field string
	}{
		{"RemoteURL empty", HostSeam{Kind: HostKindBareRepo, WebRoot: "/tmp/livee2e-local/space.git"}, "RemoteURL"},
		{"WebRoot empty", HostSeam{Kind: HostKindBareRepo, RemoteURL: "/tmp/livee2e-local/space.git"}, "WebRoot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.seam.Validate()
			if !errors.Is(err, ErrHostSeamIncomplete) {
				t.Fatalf("Validate() = %v, want errors.Is ErrHostSeamIncomplete", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate() = %q, want it to name %q", err.Error(), tc.field)
			}
		})
	}
}

func TestHostSeamValidateRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	seam := NewLiveHostSeam("acme-org", "live-e2e-space")
	seam.Kind = HostKind(99)
	err := seam.Validate()
	if err == nil {
		t.Fatalf("Validate() = nil, want an error for an unknown seam kind")
	}
	if !strings.Contains(err.Error(), "unknown(99)") {
		t.Fatalf("Validate() = %q, want it to name the unknown kind", err.Error())
	}
}

// bareRepoFixture makes a bare repository under a test-owned temp dir, marked
// as a local space the way the product marks one: a2a.host=local in its own git
// config (internal/host/local_prepare.go's localMarkerKey, which a test file
// here may not import without growing the cross-layer import ceiling). When
// marked is false the repository is a plain bare one, the shape every GitHub
// rig's origin has.
func bareRepoFixture(t *testing.T, marked bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "space.git")
	run := func(args ...string) {
		t.Helper()
		// #nosec G204 -- args are fixed literals and a test-owned temp dir.
		out, err := exec.Command("git", gitfixture.Args(args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--bare", "-q", dir)
	gitfixture.HardenRepo(t, dir)
	if marked {
		run("--git-dir="+dir, "config", "--local", "a2a.host", "local")
	}
	return dir
}

// TestHostSeamCapabilitiesAreTheOpenedHostsOwnAnswer pins what the land step
// reads: the seam's capabilities are the product's host's declaration for the
// seam's address, so a synchronous host is recognised by what it declares and
// a seam built over an UNMARKED bare repository (a GitHub rig's origin) is not
// mistaken for one by its kind.
func TestHostSeamCapabilitiesAreTheOpenedHostsOwnAnswer(t *testing.T) {
	t.Parallel()

	t.Run("a marked bare repository lands synchronously and needs no credential", func(t *testing.T) {
		t.Parallel()
		caps, err := newBareRepoHostSeam(bareRepoFixture(t, true)).Capabilities()
		if err != nil {
			t.Fatalf("Capabilities() on a marked bare-repo seam: %v", err)
		}
		if !caps.LandsSynchronously {
			t.Errorf("LandsSynchronously = false, want true for a local space")
		}
		if caps.NeedsCredential {
			t.Errorf("NeedsCredential = true, want false for a local space")
		}
	})

	t.Run("the loopback fake GitHub over an unmarked origin does not", func(t *testing.T) {
		t.Parallel()
		seam := NewLocalHostSeam("http://127.0.0.1:8080", bareRepoFixture(t, false))
		caps, err := seam.Capabilities()
		if err != nil {
			t.Fatalf("Capabilities() on the loopback-fake seam: %v", err)
		}
		if caps.LandsSynchronously {
			t.Errorf("LandsSynchronously = true, want false: GitHub merges after the required check")
		}
		if !caps.NeedsCredential {
			t.Errorf("NeedsCredential = false, want true for the GitHub shape")
		}
	})

	t.Run("a bare-repo seam over an unmarked repository reports what it really is", func(t *testing.T) {
		t.Parallel()
		caps, err := newBareRepoHostSeam(bareRepoFixture(t, false)).Capabilities()
		if err != nil {
			t.Fatalf("Capabilities(): %v", err)
		}
		if caps.LandsSynchronously {
			t.Errorf("LandsSynchronously = true for a repository carrying no local-space marker: the seam kind must not decide it")
		}
	})

	t.Run("the live seam is GitHub", func(t *testing.T) {
		t.Parallel()
		caps, err := NewLiveHostSeam("acme-org", "live-e2e-space").Capabilities()
		if err != nil {
			t.Fatalf("Capabilities() on the live seam: %v", err)
		}
		if caps.LandsSynchronously {
			t.Errorf("LandsSynchronously = true for real GitHub")
		}
	})
}
