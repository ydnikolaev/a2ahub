package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
)

// doctorCheckAdoption is the "adoption" row (D-043): the per-verb notice's
// verdict, plus the one thing only doctor runs git for — whether the file the
// clock lives in is committed. An ignored or untracked clock is machine-local,
// so every clone and worktree owes every step again and nothing records that
// it was done; axon lost its whole clock that way. The verdict on the file is
// adoption.ClockFileStatus.Severity(): an untracked clock in a repository with
// no commit yet is WARN, because it is owed at the first commit rather than
// broken (D7); once the repository has a commit it is FAIL, and an ignored
// one is FAIL whatever the history, because the rule will still ignore it.
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
	clock := adaptClock(cfg)
	notice := func() string {
		return adoption.Notice(all, issues, clock, c.binaryVersion, c.executable(), notes.Policy(), notes.Verbs())
	}
	proj, pendErr := adoption.Pending(all, issues, clock, c.binaryVersion, notes.Policy())
	if errors.Is(pendErr, adoption.ErrClockAheadOfBinary) {
		return doctorCheckFail, notice()
	}
	if errors.Is(pendErr, adoption.ErrBinaryVersionUnusable) {
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
	if gitErr == nil {
		switch {
		case file.Severity() == adoption.SeverityFail && file.IgnoredBy != "":
			return doctorCheckFail, fmt.Sprintf("%s holds this repository's adoption clock but is ignored by %s, so the clock is machine-local and every clone and worktree owes every step again — re-include it (for a `/.a2a/*` rule, add `!/%s` after it) and commit the file", rel, file.IgnoredBy, filepath.ToSlash(rel))
		case file.Severity() == adoption.SeverityFail:
			return doctorCheckFail, fmt.Sprintf("%s holds this repository's adoption clock but is not committed, so the clock is machine-local — commit it", rel)
		case file.Severity() == adoption.SeverityWarn:
			// Advisory: nothing can be committed before the first commit, so
			// a fresh repository is not yet wrong. Pending steps are named
			// too, because this row is the only one that carries them.
			detail := fmt.Sprintf("%s holds this repository's adoption clock and is not committed yet; this repository has no commit, so commit the file with the first one", rel)
			if len(proj.Items) > 0 {
				detail += "; " + notice()
			}
			return doctorCheckWarn, detail
		}
	}
	if len(proj.Items) > 0 {
		return doctorCheckUnverified, notice()
	}
	// Outside a work tree there is nothing to commit the clock to, so the
	// question does not apply — the visibility rows' "n/a" shape. Any other
	// git answer is a probe that failed, and that is not a pass.
	if errors.Is(gitErr, adoption.ErrNotAGitRepo) {
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

func doctorLoadNotes() ([]adoption.Release, error) {
	return adoption.Load(releasenotes.FS, notes.CorpusPattern)
}

func doctorLoadKnownIssues() ([]adoption.Change, error) {
	return adoption.LoadStanding(releasenotes.FS, notes.StandingPath)
}
