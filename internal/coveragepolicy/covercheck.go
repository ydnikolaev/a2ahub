//go:build ignore

// covercheck is the CI/local coverage gate. It reads a Go coverage profile
// and exits non-zero if any package — or the global aggregate — is below
// its threshold in the coveragepolicy SSOT. It lives here (not scripts/,
// which is private harness) so the PUBLIC repo and CI can run it. Invoked
// by `make coverage`:
//
//	go test -race -covermode=atomic -coverprofile=coverage.out ./... && \
//	  go run internal/coveragepolicy/covercheck.go coverage.out
package main

import (
	"fmt"
	"os"

	"github.com/ydnikolaev/a2ahub/internal/coveragepolicy"
)

func main() {
	profile := "coverage.out"
	if len(os.Args) > 1 {
		profile = os.Args[1]
	}
	failures, err := coveragepolicy.Check(profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "coverage gate error:", err)
		os.Exit(1)
	}
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "Coverage below threshold:")
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "  ✗ "+f)
		}
		fmt.Fprintln(os.Stderr, "\nRaise coverage, or (deliberately) adjust internal/coveragepolicy/policy.go.")
		os.Exit(1)
	}
	fmt.Println("coverage: OK — every package and the global floor met")
}
