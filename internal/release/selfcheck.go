package release

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Runner executes path with args and returns its combined output, exactly
// the seam os/exec.Command normally fills — injected so SelfCheckVersion is
// unit-testable without a real binary (the same DI shape as internal/host's
// Client/BaseURL injection).
type Runner func(ctx context.Context, path string, args ...string) (string, error)

// DefaultRunner runs path via os/exec, returning its stdout. It is the
// SelfCheckVersion default when run is nil.
func DefaultRunner(ctx context.Context, path string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// versionStampPattern parses cmd/a2a's versionStamp() output
// ("a2a <version> (<sha>)", cmd/a2a/main.go) — the post-download self-check
// consumes this exact contract verbatim.
var versionStampPattern = regexp.MustCompile(`^a2a\s+(\S+)\s+\([^)]*\)\s*$`)

// ParseVersionStamp reads the stamped bare version out of a binary's own
// `version` stdout. It is THE definition of that contract, exported so the
// producing side can be tested against the same expression the consuming side
// applies, rather than against a copy of it.
//
// IT READS THE FIRST LINE ONLY, and that is a fix rather than a convenience.
// The pattern is anchored at both ends, so until 2026-08-22 it required the
// WHOLE of stdout to be the stamp line. v0.25.0 gave `a2a version` a
// three-axis report on stdout, and every already-installed binary — whose copy
// of this parser is frozen — then read that as "unparseable version stamp" and
// ABORTED THE UPDATE. `a2a update` was broken for every existing install, by
// the release whose own headline was that a2a tells you which versions you are
// running.
//
// Two things had to change and they protect different people. Reading one line
// protects the NEXT format change from breaking updates again; putting the
// axes back on stderr (internal/cli's version command) is what makes THIS
// release installable by the binaries already out there, because their parser
// cannot be fixed retroactively.
func ParseVersionStamp(out string) (string, bool) {
	line := out
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	match := versionStampPattern.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return "", false
	}
	return match[1], true
}

// SelfCheckVersion runs binPath's own `version` verb (via run, or
// DefaultRunner when run is nil) and requires its stamped bare version to
// equal wantBareVersion (T1 step 3: catches wrong-arch or
// corrupt-but-summed assets before Swap ever runs). A leading "v" is
// tolerated on either side (tag vs stamp convention).
func SelfCheckVersion(ctx context.Context, binPath, wantBareVersion string, run Runner) error {
	const op = "SelfCheckVersion"
	if run == nil {
		run = DefaultRunner
	}

	out, err := run(ctx, binPath, "version")
	if err != nil {
		return &Error{Op: op, Input: binPath, Err: fmt.Errorf("%w: %w", ErrSelfCheckFailed, err)}
	}

	stamp, ok := ParseVersionStamp(out)
	if !ok {
		return &Error{Op: op, Input: out, Err: fmt.Errorf("%w: unparseable version stamp", ErrSelfCheckFailed)}
	}

	got := strings.TrimPrefix(stamp, "v")
	want := strings.TrimPrefix(wantBareVersion, "v")
	if got != want {
		return &Error{Op: op, Input: fmt.Sprintf("got %s, want %s", got, want), Err: ErrSelfCheckFailed}
	}
	return nil
}
