package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// AdoptionNotice returns the one-line advisory for a repository whose own
// adoption clock (adapted_through plus the change ids adopted ahead of a
// release) is out of step with the running binary, or "" when there is
// nothing to say (D-043).
//
// The binary is per machine and adoption is per repository, so before this
// only `a2a update` ran `adapt`, and only in the repository it was invoked in:
// `make deliver` reached every repository with no adoption at all. Asking at
// use makes the arrival path irrelevant.
//
// It speaks in both directions. Behind: how many steps are owed and since
// when. Ahead: the repository adapted to a newer release than this binary,
// which is almost always an older a2a first in PATH, so it names the
// executable. A dev build, or any other answer Pending cannot give, is silent:
// `a2a adapt` and `a2a doctor` name those.
func AdoptionNotice(all []notes.ReleaseNotes, currentIssues []notes.Change, adaptedThrough string, adaptedAhead []string, binaryVersion, execPath string) string {
	proj, err := notes.Pending(all, currentIssues, adaptedThrough, binaryVersion, adaptedAhead)
	if errors.Is(err, notes.ErrBaselineAheadOfBinary) {
		return fmt.Sprintf("this repository adapted through v%s, newer than this binary (v%s at %s) — update a2a, or check which a2a comes first in PATH", adaptedThrough, binaryVersion, execPath)
	}
	if err != nil || len(proj.Items) == 0 {
		return ""
	}
	if proj.StartedFromOldest {
		return fmt.Sprintf("this repository has never recorded adoption: %d step(s) since v%s (this binary is v%s) — run `a2a adapt`, do or verify each, then `a2a adapt --done`", len(proj.Items), proj.Oldest, binaryVersion)
	}
	return fmt.Sprintf("this repository has %d adoption step(s) pending since v%s (this binary is v%s) — run `a2a adapt`, do them, then `a2a adapt --done`", len(proj.Items), adaptedThrough, binaryVersion)
}

// RepoAdoptionNotice is AdoptionNotice over the embedded corpus and the
// repository's own clock, the one reader both surfaces call. No project
// config means this is not an a2a repository, and a corpus that will not load
// is `a2a adapt`'s to name: both are silent here.
func RepoAdoptionNotice(projectConfigPath, binaryVersion, execPath string) string {
	cfg, err := space.LoadProjectConfig(projectConfigPath)
	if err != nil {
		return ""
	}
	all, err := notes.Load(releasenotes.FS)
	if err != nil {
		return ""
	}
	currentIssues, err := notes.LoadCurrentKnownIssues(releasenotes.FS)
	if err != nil {
		return ""
	}
	return AdoptionNotice(all, currentIssues, cfg.AdaptedThrough, cfg.AdaptedAhead, binaryVersion, execPath)
}

// WriteAdoptionAdvisory renders AdoptionNotice's sentence on stderr on the same
// terms as WriteSkillAdvisory: nothing when there is nothing to say, one JSON
// object when the verb was asked for JSON.
func WriteAdoptionAdvisory(stdio IO, sentence string, jsonOut bool) {
	if sentence == "" {
		return
	}
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(struct {
			AdoptionNotice string `json:"adoption_notice"`
		}{sentence})
		return
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", sentence)
}
