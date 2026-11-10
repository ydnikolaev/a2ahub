package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLandedWriteIsVisibleToTheNextRead lands a publish and then a submit on a
// local space, whose host lands inside the call, and reads with no `a2a sync`
// in between. Each read runs with the mirror's FETCH_HEAD stamped as just
// fetched, which is what the write's own fetch leaves behind and what a read
// verb takes for fresh for the next 30 seconds.
func TestLandedWriteIsVisibleToTheNextRead(t *testing.T) {
	t.Parallel()
	g := newLocalGateRig(t, "autonomous")
	id, staging := g.stageAndSubmitContract()

	g.mustFree("contract", "publish", "--version", "1.0.0", "--staging", staging, id)
	g.stampMirrorJustFetched()
	if listed := g.mustFree("contracts", "--json"); !strings.Contains(listed, `"version": "1.0.0"`) {
		t.Fatalf("the read straight after the landed publish does not show %s at 1.0.0: %s", id, listed)
	}

	g.mustFree("contract", "new", "second")
	const secondID = "XC-axon-second"
	draft := filepath.Join(g.projectDir, ".a2a", "staging", secondID+".md")
	mustWrite(t, draft, strings.NewReplacer(
		"<human/agent-scannable title, <=120 chars>", "the second contract",
		"[<consumer-system>]", "[beta]",
		"<api|data-feed|vocabulary|event-feed|other>", "api",
	).Replace(mustReadFile(t, draft)))
	g.mustFree("submit", draft)
	g.stampMirrorJustFetched()
	if listed := g.mustFree("contracts", "--json"); !strings.Contains(listed, secondID) {
		t.Fatalf("the read straight after the landed submit does not list %s: %s", secondID, listed)
	}
}

// stampMirrorJustFetched marks every mirror this rig holds as fetched now, so
// the read that follows is inside its freshness window however slow the runner
// is.
func (g *localGateRig) stampMirrorJustFetched() {
	g.t.Helper()
	found := 0
	for _, root := range []string{g.homeDir, g.projectDir} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Name() == "FETCH_HEAD" && filepath.Base(filepath.Dir(path)) == ".git" {
				found++
				now := time.Now()
				return os.Chtimes(path, now, now)
			}
			return nil
		})
		if err != nil {
			g.t.Fatalf("walk %s: %v", root, err)
		}
	}
	if found == 0 {
		g.t.Fatal("no mirror with a FETCH_HEAD under this rig, so the read below proves nothing")
	}
}
