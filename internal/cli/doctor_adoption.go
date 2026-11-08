package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// doctorClockFile is what git says about the file that holds the repository's
// adoption clock: whether it is tracked and, if not, which ignore rule (as
// `git check-ignore -v` prints it, "<source>:<line>:<pattern>") keeps it out.
type doctorClockFile struct {
	Tracked   bool
	IgnoredBy string
}

// errDoctorNotAGitRepo is the clock-file probe's answer outside a work tree,
// where "is it committed" has no meaning.
var errDoctorNotAGitRepo = errors.New("not inside a git work tree")

// runDoctorClockFile asks git, in two read-only calls, whether path is tracked
// and, when it is not, which rule ignores it. A tracked file stays tracked
// whatever .gitignore says, so the ignore rule matters only for an untracked
// one.
func runDoctorClockFile(ctx context.Context, root, path string) (doctorClockFile, error) {
	tracked := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--error-unmatch", "--", path)
	err := tracked.Run()
	if err == nil {
		return doctorClockFile{Tracked: true}, nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return doctorClockFile{}, err
	}
	if exit.ExitCode() == 128 {
		return doctorClockFile{}, errDoctorNotAGitRepo
	}
	out, err := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-v", "--", path).Output()
	if err == nil {
		rule, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\t")
		return doctorClockFile{IgnoredBy: rule}, nil
	}
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return doctorClockFile{}, nil
	}
	return doctorClockFile{}, err
}

// doctorCheckAdoption is the "adoption" row (D-043): the per-verb notice's
// verdict, plus the one thing only doctor runs git for — whether the file the
// clock lives in is committed. An ignored or untracked clock is machine-local,
// so every clone and worktree owes every step again and nothing records that
// it was done; axon lost its whole clock that way.
//
// Pending steps are UNVERIFIED, not FAIL: a fresh release owes steps by
// design, and a CI job that runs doctor must not turn red on the day a release
// lands.
func (c *DoctorCommand) doctorCheckAdoption(ctx context.Context, cfg space.ProjectConfig) (doctorCheckStatus, string) {
	all, err := c.loadNotes()
	if err != nil {
		return doctorCheckUnverified, fmt.Sprintf("could not load the embedded release notes: %v", err)
	}
	issues, err := c.loadKnownIssues()
	if err != nil {
		return doctorCheckUnverified, fmt.Sprintf("could not load the embedded known issues: %v", err)
	}
	proj, pendErr := notes.Pending(all, issues, cfg.AdaptedThrough, c.binaryVersion, cfg.AdaptedAhead)
	if errors.Is(pendErr, notes.ErrBaselineAheadOfBinary) {
		return doctorCheckFail, AdoptionNotice(all, issues, cfg.AdaptedThrough, cfg.AdaptedAhead, c.binaryVersion, c.executable())
	}
	if errors.Is(pendErr, notes.ErrBinaryVersionUnusable) {
		return doctorCheckUnverified, fmt.Sprintf("this build (%q) is not a release, so it cannot say which adoption steps are owed", c.binaryVersion)
	}
	if pendErr != nil {
		return doctorCheckUnverified, pendErr.Error()
	}

	rel := c.projectConfigPath
	if r, err := filepath.Rel(c.projectRoot, c.projectConfigPath); err == nil && !strings.HasPrefix(r, "..") {
		rel = r
	}
	file, gitErr := c.clockFile(ctx, c.projectRoot, c.projectConfigPath)
	switch {
	case gitErr == nil && file.IgnoredBy != "":
		return doctorCheckFail, fmt.Sprintf("%s holds this repository's adoption clock but is ignored by %s, so the clock is machine-local and every clone and worktree owes every step again — re-include it (for a `/.a2a/*` rule, add `!/%s` after it) and commit the file", rel, file.IgnoredBy, filepath.ToSlash(rel))
	case gitErr == nil && !file.Tracked:
		return doctorCheckFail, fmt.Sprintf("%s holds this repository's adoption clock but is not committed, so the clock is machine-local — commit it", rel)
	}
	if len(proj.Items) > 0 {
		return doctorCheckUnverified, AdoptionNotice(all, issues, cfg.AdaptedThrough, cfg.AdaptedAhead, c.binaryVersion, c.executable())
	}
	// Outside a work tree there is nothing to commit the clock to, so the
	// question does not apply — the visibility rows' "n/a" shape. Any other
	// git answer is a probe that failed, and that is not a pass.
	if errors.Is(gitErr, errDoctorNotAGitRepo) {
		return doctorCheckPass, fmt.Sprintf(" · not a git work tree, so whether %s is committed does not apply", rel)
	}
	if gitErr != nil {
		return doctorCheckUnverified, fmt.Sprintf("adapted through v%s, but could not tell whether %s is committed: %v", cfg.AdaptedThrough, rel, gitErr)
	}
	return doctorCheckPass, ""
}

// executable names the running binary for the ahead-of-binary sentence.
func (c *DoctorCommand) executable() string {
	if c.execPath != nil {
		if p, err := c.execPath(); err == nil {
			return p
		}
	}
	return "a2a"
}

func doctorLoadNotes() ([]notes.ReleaseNotes, error) { return notes.Load(releasenotes.FS) }

func doctorLoadKnownIssues() ([]notes.Change, error) {
	return notes.LoadCurrentKnownIssues(releasenotes.FS)
}
