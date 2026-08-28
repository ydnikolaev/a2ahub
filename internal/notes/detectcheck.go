//go:build ignore

// detectcheck is P13's forward-only release-note authoring gate's scanning
// half (spec 13 §"The authoring gate (US-5, ACs 13-14)"). It shells no
// YAML parsing of its own into bash — scripts/check-release-note-detect.sh
// `go run`s this file exactly the internal/lane/lanecheck.go precedent
// (D-7: the corpus already has a typed parser, this package's own Load; a
// bash reimplementation of "walk every change, read action.scope/detect"
// would be a second hand-written copy of the schema's own shape).
//
// Prints one change id per line: every change under the given directory
// (a releasenotes/-shaped tree — every top-level *.yaml file, Load's own
// contract) whose action.scope is exactly "local" or "space" (a POSITIVE
// membership test, never "not none" — a change with no action block at all
// must never be silently counted as an obligation) and carries no
// detect:. The caller (scripts/check-release-note-detect.sh) diffs that
// set against scripts/lib/release-note-detect-budget.txt.
//
// With no argument, scans the real embedded corpus (releasenotes.FS).
// With one argument, scans that directory instead via os.DirFS — the seam
// the gate's own --teeth uses to point this at a disposable fixture tree
// without rebuilding the embed.
//
// Run from the repo root:
//
//	go run internal/notes/detectcheck.go [dir]
package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

func main() {
	var fsys fs.FS = releasenotes.FS
	if len(os.Args) > 1 {
		fsys = os.DirFS(os.Args[1])
	}

	all, err := notes.Load(fsys)
	if err != nil {
		fmt.Fprintf(os.Stderr, "detectcheck: %v\n", err)
		os.Exit(1)
	}

	for _, rn := range all {
		for _, ch := range rn.Changes {
			switch ch.Action.Scope {
			case "local", "space":
			default:
				continue
			}
			if len(ch.Action.Detect) > 0 {
				continue
			}
			fmt.Println(ch.ID)
		}
	}
}
