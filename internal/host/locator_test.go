package host

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// legacyParseGitHubRepo is the pre-P1 slice, copied verbatim from the three
// byte-identical parsers P1 removed (cmd/a2a parseGitHubRepo, internal/mcp
// parseGitHubRepo, internal/cli doctorRepoOwnerName). It lives here, as the
// oracle of the differential test, and nowhere else.
func legacyParseGitHubRepo(url string) (owner, name string, err error) {
	s := strings.TrimSuffix(url, ".git")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return "", "", fmt.Errorf("cannot parse owner/name from repo URL %q", url)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

func TestParseLocator_Classifies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw       string
		wantClass locatorClass
		wantRepo  Repo
	}{
		{"https://github.com/acme/space.git", classURL, Repo{"acme", "space"}},
		{"https://github.com/o/r", classURL, Repo{"o", "r"}},
		{"git@github.com:acme/space", classURL, Repo{"acme", "space"}},
		{"git@github.com:acme/space.git", classURL, Repo{"acme", "space"}},
		{"https://ghe.corp/acme/space", classURL, Repo{"acme", "space"}},
		{"http://ghe.corp/acme/space.git", classURL, Repo{"acme", "space"}},
		{"ssh://git@host/o/r", classURL, Repo{"o", "r"}},
		{"/tmp/x/some-org/some-hub", classPath, Repo{"some-org", "some-hub"}},
		{"/tmp/x/some-org/some-hub.git", classPath, Repo{"some-org", "some-hub"}},
		{"file:///tmp/x/org/hub.git", classPath, Repo{"org", "hub"}},
		{"file://localhost/tmp/x/org/hub", classPath, Repo{"org", "hub"}},
		{"org/hub", classURL, Repo{"org", "hub"}},
		// scp form on another host: the legacy slice keeps the host in the owner.
		{"git@other.example:org/hub.git", classURL, Repo{"git@other.example:org", "hub"}},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			loc, err := ParseLocator(tc.raw)
			if err != nil {
				t.Fatalf("ParseLocator(%q): %v", tc.raw, err)
			}
			if loc.Raw != tc.raw {
				t.Errorf("Raw = %q, want %q", loc.Raw, tc.raw)
			}
			if loc.class != tc.wantClass {
				t.Errorf("class = %v, want %v", loc.class, tc.wantClass)
			}
			if loc.Repo != tc.wantRepo {
				t.Errorf("Repo = %+v, want %+v", loc.Repo, tc.wantRepo)
			}
		})
	}
}

func TestParseLocator_PathKeepsAbsolutePath(t *testing.T) {
	t.Parallel()
	loc, err := ParseLocator("/tmp/x/org/hub.git")
	if err != nil {
		t.Fatal(err)
	}
	if loc.path != "/tmp/x/org/hub.git" {
		t.Errorf("path = %q", loc.path)
	}
	loc, err = ParseLocator("file:///tmp/x/org/hub.git")
	if err != nil {
		t.Fatal(err)
	}
	if loc.path != "/tmp/x/org/hub.git" {
		t.Errorf("file:// path = %q", loc.path)
	}
}

func TestParseLocator_LegacyIdentity(t *testing.T) {
	t.Parallel()
	misslice := filepath.Join(t.TempDir(), "some-org", "some-hub")
	corpus := []string{
		// cmd/a2a wire_test.go TestParseGitHubRepo table.
		"https://github.com/getvisa/getvisa",
		"https://github.com/getvisa/getvisa.git",
		"git@github.com:r22d222/a2a.git",
		"https://github.com/org/repo",
		"not-a-url",
		"https://github.com/onlyowner",
		// internal/mcp wire_test.go cases.
		"https://github.com/acme/space.git",
		"git@github.com:acme/space",
		// the mis-slice case from cmd/a2a parse_github_repo_test.go.
		misslice,
		// edges.
		"",
		"/a",
		"/",
		"a/",
		"/a/",
		"//",
		"org/hub",
		"org/hub/",
		"https://ghe.corp/acme/space",
		"ssh://git@host/o/r",
		"file:///tmp/x/org/hub.git",
		"file:///hub",
		"git@github.com:",
		"git@github.com:solo",
		"https://github.com/",
		"https://github.com/a/b/c/d",
		"x.git",
		".git",
		"a/.git",
	}
	if len(corpus) < 20 {
		t.Fatalf("corpus has %d inputs, want at least 20", len(corpus))
	}
	for _, raw := range corpus {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			wantOwner, wantName, wantErr := legacyParseGitHubRepo(raw)
			loc, err := ParseLocator(raw)
			if (err != nil) != (wantErr != nil) {
				t.Fatalf("ParseLocator(%q) err = %v, legacy err = %v", raw, err, wantErr)
			}
			if err != nil {
				if err.Error() != wantErr.Error() {
					t.Errorf("error = %q, want the legacy %q", err, wantErr)
				}
				return
			}
			if loc.Repo.Owner != wantOwner || loc.Repo.Name != wantName {
				t.Errorf("ParseLocator(%q).Repo = %+v, legacy %s/%s", raw, loc.Repo, wantOwner, wantName)
			}
		})
	}
}
