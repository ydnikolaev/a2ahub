package skill_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/skill"
)

// TestEmbedHoldsNoHiddenFile pins that the embedded skill tree carries only
// the documentation the repository tracks. An `all:` embed prefix pulls in
// dot- and underscore-files, so an untracked Finder `.DS_Store` in the build
// checkout shipped inside a locally built binary and `a2a skill install`
// wrote it into every consumer repo (fb-20261107-3779ff). The tree tracks no
// such file, so any one found here came from the build machine.
func TestEmbedHoldsNoHiddenFile(t *testing.T) {
	t.Parallel()

	err := fs.WalkDir(skill.Files, ".", func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		for _, part := range strings.Split(p, "/") {
			if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
				t.Errorf("embedded skill tree holds hidden path %q", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded skill tree: %v", err)
	}
}
