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
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const maxPublishedBodyBytes = 1 << 20

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("releasebody", flag.ContinueOnError)
	flags.SetOutput(stderr)
	requested := flags.String("version", "", "release version without a leading v")
	verification := flags.String("verification", "", "optional verified release-gate summary")
	verifyStdin := flags.Bool("verify-stdin", false, "compare stdin with the rendered body")
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
	if *verifyStdin {
		published, readErr := io.ReadAll(io.LimitReader(stdin, maxPublishedBodyBytes+1))
		if readErr != nil {
			// reason: the exit code still carries the failure if stderr itself is unavailable.
			_, _ = fmt.Fprintf(stderr, "releasebody: read published body: %v\n", readErr)
			return 1
		}
		if len(published) > maxPublishedBodyBytes {
			// reason: the exit code still carries the refusal if stderr itself is unavailable.
			_, _ = fmt.Fprintf(stderr, "releasebody: published body exceeds %d bytes\n", maxPublishedBodyBytes)
			return 1
		}
		// GitHub/GoReleaser may normalize one or more terminal line endings.
		// Everything before them remains byte-exact.
		if strings.TrimRight(body, "\r\n") != strings.TrimRight(string(published), "\r\n") {
			// reason: the exit code still carries the mismatch if stderr itself is unavailable.
			_, _ = fmt.Fprintln(stderr, "releasebody: published GitHub body differs from the authored corpus")
			return 1
		}
		return 0
	}
	if _, err := fmt.Fprint(stdout, body); err != nil {
		// reason: both output channels may be broken; the exit code remains observable.
		_, _ = fmt.Fprintf(stderr, "releasebody: write stdout: %v\n", err)
		return 1
	}
	return 0
}
