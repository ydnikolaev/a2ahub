package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

// SkillNotice returns the one-line advisory for a repository whose installed
// skill differs from the tree this binary carries, or "" when there is nothing
// to advise. `.a2ahub/skill/` is a committed copy, so a new binary changes
// nothing in it, and before this only `a2a doctor` ever looked.
//
// It compares the tree rather than the stamp: a local build (D-040) keeps the
// last release's version while its skill moves, and a stamp several releases
// old over an unchanged skill owes nothing.
//
// Advisory only, so every "cannot judge" is silent and stays doctor's to name:
// no install, a tree a2ahub does not own, a stamp or binary with no comparable
// version, a stamp newer than this binary (installing would move it backwards),
// or a file that cannot be read.
func SkillNotice(files fs.FS, projectRoot, skillDir, binaryVersion string) string {
	target := filepath.Join(projectRoot, filepath.FromSlash(skillDir))
	nonEmpty, owned, err := skillTargetState(target)
	if err != nil || !nonEmpty || !owned {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(target, skillProvenanceFile)) //nolint:gosec // reason: target is the resolved skill-install directory under the project root, not artifact content.
	if err != nil {
		return ""
	}
	match := doctorSkillManualVersionPattern.FindStringSubmatch(string(data))
	if match == nil {
		return ""
	}
	if newer, err := version.OlderThan(binaryVersion, match[1]); err != nil || newer {
		return ""
	}
	res, err := doctorSkillTreeCompare(files, target)
	if err != nil || res.undecidable != "" || !res.hasDiff() {
		return ""
	}
	n := len(res.missing) + len(res.modified) + len(res.unexpected)
	return fmt.Sprintf("the a2a skill at %s differs from what this binary carries (%d file(s)) — run `a2a skill install` and commit it; a long-running `a2a mcp` needs a restart first", skillDir, n)
}

// WriteSkillAdvisory renders SkillNotice's sentence on stderr, on the same
// terms as WriteUpdateAdvisory: nothing when there is nothing to say, one JSON
// object when the verb was asked for JSON.
func WriteSkillAdvisory(stdio IO, sentence string, jsonOut bool) {
	if sentence == "" {
		return
	}
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(struct {
			SkillNotice string `json:"skill_notice"`
		}{sentence})
		return
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", sentence)
}
