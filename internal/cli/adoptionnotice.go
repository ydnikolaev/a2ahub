package cli

import (
	"encoding/json"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
)

// RepoAdoptionNotice returns the one-line advisory for a repository whose own
// adoption clock (adapted_through plus the change ids adopted ahead of a
// release) is out of step with the running binary, or "" when there is
// nothing to say (D-043): adoption.Notice over the embedded corpus, the
// repository's own clock and a2a's policy and verbs, the one reader both
// surfaces call.
//
// The binary is per machine and adoption is per repository, so before this
// only `a2a update` ran `adapt`, and only in the repository it was invoked in:
// `make deliver` reached every repository with no adoption at all. Asking at
// use makes the arrival path irrelevant. No project config means this is not
// an a2a repository, and a corpus that will not load is `a2a adapt`'s to
// name: both are silent here, as is a dev build.
func RepoAdoptionNotice(projectConfigPath, binaryVersion, execPath string) string {
	cfg, err := space.LoadProjectConfig(projectConfigPath)
	if err != nil {
		return ""
	}
	all, err := adoption.Load(releasenotes.FS, notes.CorpusPattern)
	if err != nil {
		return ""
	}
	standing, err := adoption.LoadStanding(releasenotes.FS, notes.StandingPath)
	if err != nil {
		return ""
	}
	return adoption.Notice(all, standing, adaptClock(cfg), binaryVersion, execPath, notes.Policy(), notes.Verbs())
}

// WriteAdoptionAdvisory renders RepoAdoptionNotice's sentence on stderr on the same
// terms as WriteSkillAdvisory: nothing when there is nothing to say, one JSON
// object when the verb was asked for JSON.
func WriteAdoptionAdvisory(stdio IO, sentence string, jsonOut bool) {
	if sentence == "" {
		return
	}
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(struct { // reason: an advisory on stderr; a write error cannot change the verb's outcome
			AdoptionNotice string `json:"adoption_notice"`
		}{sentence})
		return
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", sentence)
}
