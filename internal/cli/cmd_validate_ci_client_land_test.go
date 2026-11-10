package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ciCommittedBase commits manifest and returns its sha.
func ciCommittedBase(t *testing.T, manifest string) (root, base string) {
	t.Helper()
	root = ciRepo(t, manifest, nil)
	ciTestGit(t, root, "init", "-q", "-b", "main")
	ciTestGit(t, root, "add", "space.yaml")
	ciTestGit(t, root, "commit", "-q", "-m", "base")
	return root, strings.TrimSpace(ciTestGit(t, root, "rev-parse", "HEAD"))
}

// TestValidateCI_HostReviewsFollowsTheBaseManifest: under `validation: local` an
// optional workflow agrees with the land check on POL-028, and only the base can
// say so.
func TestValidateCI_HostReviewsFollowsTheBaseManifest(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const key = "min_binary_version: 0.1.0\n"
	autonomousLocal := strings.Replace(ciSpaceYAML, key, key+"gates: autonomous\nvalidation: local\n", 1)
	tests := []struct {
		name      string
		base      string
		wantCode  int
		wantCodes []string
	}{
		{name: "a local base accepts what the land check accepts", base: strings.Replace(ciSpaceYAML, key, key+"validation: local\n", 1), wantCode: 0},
		{name: "a base with no key refuses, whatever the pull request claims", base: ciSpaceYAML, wantCode: 1, wantCodes: []string{"POL-028"}},
		{name: "an actions base refuses", base: strings.Replace(ciSpaceYAML, key, key+"validation: actions\n", 1), wantCode: 1, wantCodes: []string{"POL-028"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root, base := ciCommittedBase(t, tt.base)
			if err := os.WriteFile(filepath.Join(root, "space.yaml"), []byte(autonomousLocal), 0o644); err != nil {
				t.Fatalf("write the pull request's manifest: %v", err)
			}
			code, rep, errOut := runCI(t, engine, root, fakeGit("space.yaml"), "v3-pr", base, "ydnikolaev")
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d; stderr = %s, report = %+v", code, tt.wantCode, errOut, rep)
			}
			if got := ciManifestReportCodes(rep); !slices.Equal(got, tt.wantCodes) {
				t.Errorf("codes = %v, want %v", got, tt.wantCodes)
			}
		})
	}
}

func TestBaseValidatedLocallyFailsToTheReviewingHost(t *testing.T) {
	t.Parallel()
	root, base := ciCommittedBase(t, ciSpaceYAML)
	tests := []struct {
		name string
		mode string
		base string
	}{
		{"a full-repo scan has no base", "v3-full-repo", base},
		{"a base git cannot read", "v3-pr", strings.Repeat("a", 40)},
		{"a base with no key", "v3-pr", base},
	}
	for _, tt := range tests {
		if baseValidatedLocally(context.Background(), root, tt.mode, tt.base) {
			t.Errorf("%s: read as validation: local", tt.name)
		}
	}
}
