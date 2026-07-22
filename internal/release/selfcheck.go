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

	match := versionStampPattern.FindStringSubmatch(strings.TrimSpace(out))
	if match == nil {
		return &Error{Op: op, Input: out, Err: fmt.Errorf("%w: unparseable version stamp", ErrSelfCheckFailed)}
	}

	got := strings.TrimPrefix(match[1], "v")
	want := strings.TrimPrefix(wantBareVersion, "v")
	if got != want {
		return &Error{Op: op, Input: fmt.Sprintf("got %s, want %s", got, want), Err: ErrSelfCheckFailed}
	}
	return nil
}
