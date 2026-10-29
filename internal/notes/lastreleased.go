//go:build ignore

// lastreleased prints the last published release: the highest version among
// the dated release notes under the given directory (notes.LastReleased).
// `make deliver` (scripts/deliver.sh) stamps a local build with it (D-040), so
// the stamp follows from the commit being built alone: no tag and no network
// are read, and no second YAML parser lives in shell (detectcheck.go's
// precedent). Exit 1 with a message on stderr when no note is dated.
//
// Run from the repo root:
//
//	go run internal/notes/lastreleased.go releasenotes
package main

import (
	"fmt"
	"os"

	"github.com/ydnikolaev/a2ahub/internal/notes"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run internal/notes/lastreleased.go <releasenotes-dir>")
		os.Exit(2)
	}
	all, err := notes.Load(os.DirFS(os.Args[1]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "lastreleased: %v\n", err)
		os.Exit(1)
	}
	v, err := notes.LastReleased(all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lastreleased: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(v)
}
