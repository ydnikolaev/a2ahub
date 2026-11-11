package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/go-foundation/units/adoption"
)

// TestDoctorCheckAdoption drives the "adoption" row through each state of the
// repository's clock and of git's view of the file it lives in (D-043).
func TestDoctorCheckAdoption(t *testing.T) {
	t.Parallel()
	tracked := adoption.ClockFileStatus{Tracked: true}
	cases := []struct {
		name    string
		through string
		ahead   []string
		binary  string
		file    adoption.ClockFileStatus
		gitErr  error
		status  doctorCheckStatus
		want    []string
	}{
		{name: "an ignored clock fails and names the rule and the re-include", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			file: adoption.ClockFileStatus{IgnoredBy: ".gitignore:67:/.a2a/*"}, status: doctorCheckFail,
			want: []string{".a2a/config.yaml", "ignored by .gitignore:67:/.a2a/*", "`!/.a2a/config.yaml`", "commit"}},
		{name: "an ignored clock fails even before the first commit", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			file: adoption.ClockFileStatus{IgnoredBy: ".gitignore:1:/.a2a/*", Unborn: true}, status: doctorCheckFail,
			want: []string{"ignored by .gitignore:1:/.a2a/*"}},
		{name: "an untracked clock fails once the repository has commits", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			file: adoption.ClockFileStatus{}, status: doctorCheckFail, want: []string{"not committed, so the clock is machine-local"}},
		{name: "an untracked clock before the first commit warns", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			file: adoption.ClockFileStatus{Unborn: true}, status: doctorCheckWarn,
			want: []string{".a2a/config.yaml", "not committed yet", "no commit", "with the first one"}},
		{name: "an untracked clock before the first commit warns and names pending steps", through: "0.1.0", binary: "0.2.0",
			file: adoption.ClockFileStatus{Unborn: true}, status: doctorCheckWarn,
			want: []string{"not committed yet", "pending since v0.1.0", "`a2a adapt`"}},
		{name: "pending steps are advisory, not a failure", through: "0.1.0", binary: "0.2.0",
			file: tracked, status: doctorCheckUnverified, want: []string{"pending since v0.1.0", "`a2a adapt`"}},
		{name: "a repository ahead of the binary fails and names the executable", through: "0.9.0", binary: "0.2.0",
			file: tracked, status: doctorCheckFail, want: []string{"newer than this binary (v0.2.0 at /opt/a2a)"}},
		{name: "settled and committed passes", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			file: tracked, status: doctorCheckPass},
		{name: "outside git a settled clock passes and says the commit half does not apply", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			gitErr: adoption.ErrNotAGitRepo, status: doctorCheckPass, want: []string{"not a git work tree", "does not apply"}},
		{name: "a git probe that failed is not a pass", through: "0.2.0", ahead: []string{"RN-5"}, binary: "0.2.0",
			gitErr: errors.New("git: broken"), status: doctorCheckUnverified, want: []string{"could not tell whether .a2a/config.yaml is committed", "git: broken"}},
		{name: "a dev build cannot judge", through: "0.1.0", binary: "dev",
			file: tracked, status: doctorCheckUnverified, want: []string{"not a release"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewDoctorCommand(nil, tc.binary, "/repo/.a2a/config.yaml", "", "/repo")
			cmd.loadNotes = func() ([]adoption.Release, error) { return adoptionCorpus(), nil }
			cmd.loadKnownIssues = func() ([]adoption.Change, error) { return nil, nil }
			cmd.execPath = func() (string, error) { return "/opt/a2a", nil }
			cmd.clockFile = func(context.Context, string, string) (adoption.ClockFileStatus, error) { return tc.file, tc.gitErr }
			status, detail := cmd.doctorCheckAdoption(context.Background(), space.ProjectConfig{AdaptedThrough: tc.through, AdaptedAhead: tc.ahead})
			if status != tc.status {
				t.Fatalf("status = %s, want %s (detail %q)", status, tc.status, detail)
			}
			for _, w := range tc.want {
				if !strings.Contains(detail, w) {
					t.Errorf("detail %q lacks %q", detail, w)
				}
			}
		})
	}
}

// TestDoctorAdoptionPendingKeepsExitZero: a fresh release owes steps by
// design, so a CI job running doctor must stay green while they are owed.
func TestDoctorAdoptionPendingKeepsExitZero(t *testing.T) {
	t.Parallel()
	run := func(through string) (int, string) {
		cmd := newTestDoctorCommand()
		cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
			return space.ProjectConfig{AdaptedThrough: through, AdaptedAhead: []string{"RN-5"}}, nil
		}
		cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
		cmd.loadNotes = func() ([]adoption.Release, error) { return adoptionCorpus(), nil }
		cmd.binaryVersion = "0.2.0"
		cmd.lookupGit = func() error { return nil }
		var stdout, stderr bytes.Buffer
		code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
		return code, stdout.String()
	}
	settledCode, settled := run("0.2.0")
	if !strings.Contains(settled, "adoption: PASS") {
		t.Fatalf("settled clock did not pass:\n%s", settled)
	}
	pendingCode, pending := run("0.1.0")
	if !strings.Contains(pending, "adoption: UNVERIFIED: this repository has 1 adoption step(s) pending since v0.1.0") {
		t.Fatalf("no advisory adoption row:\n%s", pending)
	}
	// Other rows may fail against the fake host; what this claims is that the
	// pending row alone does not move the exit code.
	if pendingCode != settledCode {
		t.Fatalf("exit code moved with pending steps: settled %d, pending %d", settledCode, pendingCode)
	}
}

// TestDoctorAdoptionAgainstRealGit drives the adoption row over a real work
// tree through each state of the clock's file, because reading git's exit
// codes (an unborn HEAD among them) is the part a fake cannot prove. Before
// the first commit an untracked clock is WARN; after it, the same file is
// FAIL; an ignored one is FAIL quoting the rule; a committed one passes.
func TestDoctorAdoptionAgainstRealGit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	cfg := filepath.Join(root, ".a2a", "config.yaml")
	gitIn := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", gitfixture.Args(append([]string{"-C", root}, args...)...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("system: beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	row := func() (doctorCheckStatus, string) {
		t.Helper()
		cmd := NewDoctorCommand(nil, "0.2.0", cfg, "", root)
		cmd.loadNotes = func() ([]adoption.Release, error) { return adoptionCorpus(), nil }
		cmd.loadKnownIssues = func() ([]adoption.Change, error) { return nil, nil }
		return cmd.doctorCheckAdoption(ctx, space.ProjectConfig{AdaptedThrough: "0.2.0", AdaptedAhead: []string{"RN-5"}})
	}
	expect := func(label string, want doctorCheckStatus, fragment string) {
		t.Helper()
		status, detail := row()
		if status != want || !strings.Contains(detail, fragment) {
			t.Fatalf("%s: status %s detail %q, want %s containing %q", label, status, detail, want, fragment)
		}
	}

	expect("untracked, no commit yet", doctorCheckWarn, "not committed yet")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn("add", "--", "README")
	gitIn("-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "-m", "first")
	expect("untracked after the first commit", doctorCheckFail, "not committed, so the clock is machine-local")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.a2a/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("ignored", doctorCheckFail, "ignored by .gitignore:1:/.a2a/*")
	gitIn("add", "-f", "--", cfg)
	expect("tracked", doctorCheckPass, "")
	outside := t.TempDir()
	if _, err := adoption.ClockFile(ctx, outside, filepath.Join(outside, "config.yaml")); !errors.Is(err, adoption.ErrNotAGitRepo) {
		t.Fatalf("outside a work tree: err = %v, want adoption.ErrNotAGitRepo", err)
	}
}
