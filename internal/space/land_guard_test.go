package space

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

func landGuardGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func landGuardManifest(floor string) string {
	return "schema: space/v1\nspace: acme\nmin_binary_version: " + floor + "\nparticipants: []\n"
}

// landGuardRepo commits floor's manifest, then one empty commit per message, and
// returns the checkout and its tip.
func landGuardRepo(t *testing.T, floor string, messages ...string) (dir, tip string) {
	t.Helper()
	dir = t.TempDir()
	landGuardGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(landGuardManifest(floor)), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
	landGuardGit(t, dir, "add", "space.yaml")
	landGuardGit(t, dir, "commit", "-q", "-m", "root")
	for _, message := range messages {
		landGuardGit(t, dir, "commit", "-q", "--allow-empty", "-m", message)
	}
	return dir, landGuardGit(t, dir, "rev-parse", "HEAD")
}

// builtBy is the line the funnel writes for a build of v, from the one composer.
func builtBy(v string, channel string) string {
	return builtByTrailerName + ": " + version.Build{Version: v, Commit: "abc123", Channel: channel}.Stamp()
}

// withApprovalTrailers is the real shape on a gated write: the build note is a
// paragraph of its own above the approval trailers, which is why git's trailer
// parser cannot be used.
func withApprovalTrailers(buildLine string) string {
	return "a2a(submit): x\n\n" + buildLine + "\n\nA2A-Approved-Gates: G1\n"
}

func TestLandGuard(t *testing.T) {
	t.Parallel()
	local := version.Build{Channel: version.ChannelLocal}.Channel

	tests := []struct {
		name     string
		floor    string
		messages []string
		binary   string
		wantErr  error
		wantText string
	}{
		{name: "no trailers passes", floor: "0.1.0", messages: []string{"plain"}, binary: "0.2.0"},
		{name: "floor above the binary refuses", floor: "0.5.0", binary: "0.4.0", wantErr: ErrStaleBinaryVersion,
			wantText: "the space is ahead of this binary: space requires min_binary_version 0.5.0, this is 0.4.0; run `a2a update` and retry"},
		{name: "floor equal to the binary passes", floor: "0.4.0", binary: "0.4.0"},
		{name: "high-water above the binary refuses", floor: "0.1.0", messages: []string{"x\n\n" + builtBy("0.28.0", "")}, binary: "0.27.1",
			wantErr: ErrStaleBinaryVersion, wantText: "the space is ahead of this binary: space has 0.28.0, this is 0.27.1; run `a2a update` and retry"},
		{name: "high-water above, in the paragraph above approval trailers", floor: "0.1.0", messages: []string{withApprovalTrailers(builtBy("0.28.0", ""))}, binary: "0.27.1",
			wantErr: ErrStaleBinaryVersion},
		{name: "high-water equal passes", floor: "0.1.0", messages: []string{"x\n\n" + builtBy("0.27.1", "")}, binary: "0.27.1"},
		{name: "local-build line counts and equal passes", floor: "0.1.0", messages: []string{"x\n\n" + builtBy("0.27.1", local)}, binary: "0.27.1"},
		{name: "local-build line above refuses", floor: "0.1.0", messages: []string{"x\n\n" + builtBy("0.28.0", local)}, binary: "0.27.1", wantErr: ErrStaleBinaryVersion},
		{name: "highest wins, not newest", floor: "0.1.0", messages: []string{"a\n\n" + builtBy("0.28.0", ""), "b\n\n" + builtBy("0.26.0", "")}, binary: "0.27.1", wantErr: ErrStaleBinaryVersion},
		{name: "binary above the high-water passes", floor: "0.1.0", messages: []string{"x\n\n" + builtBy("0.26.0", "")}, binary: "0.27.1"},
		{name: "garbage lines are skipped", floor: "0.1.0", messages: []string{
			"a\n\nA2A-Built-By: a2a garbage (abc)", "b\n\nA2A-Built-By: a2a 9", "c\n\nA2A-Built-By: a2a 9.9.9",
			"d\n\nA2A-Built-By: gh 9.9.9 (abc)", "e\n\nnot-A2A-Built-By: a2a 9.9.9 (abc)", "f\n\nA2A-Built-By: a2a 9.9.9-rc1 (abc)",
		}, binary: "0.27.1"},
		{name: "a development build refuses once there is something to compare", floor: "0.1.0", binary: "dev", wantErr: ErrInvalidVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, tip := landGuardRepo(t, tt.floor, tt.messages...)
			before := landGuardGit(t, dir, "rev-parse", "HEAD")

			err := NewLandGuard(version.Build{Version: tt.binary})(context.Background(), dir, tip)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantText)
			}
			if after := landGuardGit(t, dir, "rev-parse", "HEAD"); after != before {
				t.Errorf("the guard moved HEAD from %s to %s", before, after)
			}
		})
	}
}

func TestLandGuardReadsTheFirstParentHistoryOnly(t *testing.T) {
	t.Parallel()
	dir, _ := landGuardRepo(t, "0.1.0")
	landGuardGit(t, dir, "checkout", "-q", "-b", "side")
	landGuardGit(t, dir, "commit", "-q", "--allow-empty", "-m", "head\n\n"+builtBy("0.99.0", ""))
	landGuardGit(t, dir, "checkout", "-q", "main")
	landGuardGit(t, dir, "merge", "-q", "--no-ff", "-m", "merge\n\n"+builtBy("0.27.0", ""), "side")
	tip := landGuardGit(t, dir, "rev-parse", "HEAD")

	if err := NewLandGuard(version.Build{Version: "0.27.0"})(context.Background(), dir, tip); err != nil {
		t.Fatalf("a head commit's build note counted as first-parent history: %v", err)
	}
}

func TestLandGuardStopsAtTheHistoryDepth(t *testing.T) {
	t.Parallel()
	dir, _ := landGuardRepo(t, "0.1.0", "old\n\n"+builtBy("0.99.0", ""))
	parent := landGuardGit(t, dir, "rev-parse", "HEAD")
	tree := landGuardGit(t, dir, "rev-parse", "HEAD^{tree}")
	for range landGuardHistoryDepth {
		parent = landGuardGit(t, dir, "commit-tree", tree, "-p", parent, "-m", "filler")
	}

	if err := NewLandGuard(version.Build{Version: "0.27.0"})(context.Background(), dir, parent); err != nil {
		t.Fatalf("a build note beyond the last %d commits was read: %v", landGuardHistoryDepth, err)
	}
}

func TestLandGuardFailsClosedOnAnUnreadableTip(t *testing.T) {
	t.Parallel()
	guard := NewLandGuard(version.Build{Version: "9.9.9"})

	t.Run("space.yaml is missing", func(t *testing.T) {
		t.Parallel()
		if err := guard(context.Background(), t.TempDir(), "deadbeef"); err == nil {
			t.Fatal("a tip with no space.yaml passed the guard")
		}
	})
	t.Run("space.yaml does not parse", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(":\n  - ["), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := guard(context.Background(), dir, "deadbeef"); err == nil {
			t.Fatal("an unparseable manifest passed the guard")
		}
	})
	t.Run("the tip is not in the repository", func(t *testing.T) {
		t.Parallel()
		dir, _ := landGuardRepo(t, "0.1.0")
		if err := guard(context.Background(), dir, strings.Repeat("a", 40)); err == nil {
			t.Fatal("an unreadable history passed the guard")
		}
	})
}
