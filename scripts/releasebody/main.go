// Command releasebody renders one releasenotes/<version>.yaml document as the
// GitHub Release body consumed by GoReleaser.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("releasebody", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requested := flags.String("version", "", "release version without a leading v")
	verification := flags.String("verification", "", "optional verified release-gate summary")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	normalized, err := version.Canonical(strings.TrimPrefix(strings.TrimSpace(*requested), "v"))
	if err != nil || flags.NArg() != 0 {
		// reason: the exit code still carries the refusal if stderr itself is unavailable.
		_, _ = fmt.Fprintln(stderr, "usage: go run ./scripts/releasebody --version X.Y.Z [--verification <text>]")
		return 2
	}

	all, err := notes.Load(releasenotes.FS)
	if err != nil {
		// reason: the exit code still carries the failure if stderr itself is unavailable.
		_, _ = fmt.Fprintf(stderr, "releasebody: load release-notes corpus: %v\n", err)
		return 1
	}
	var rn notes.ReleaseNotes
	for _, candidate := range all {
		if candidate.Version == normalized {
			rn = candidate
			break
		}
	}
	if rn.Version == "" {
		// reason: the exit code still carries the refusal if stderr itself is unavailable.
		_, _ = fmt.Fprintf(stderr, "releasebody: no authored release notes for %s\n", normalized)
		return 1
	}

	body := notes.RenderMarkdown(rn, notes.MarkdownOptions{
		RepositoryURL: "https://github.com/ydnikolaev/a2ahub",
		Verification:  *verification,
	})
	if _, err := fmt.Fprint(stdout, body); err != nil {
		// reason: both output channels may be broken; the exit code remains observable.
		_, _ = fmt.Fprintf(stderr, "releasebody: write stdout: %v\n", err)
		return 1
	}
	return 0
}
