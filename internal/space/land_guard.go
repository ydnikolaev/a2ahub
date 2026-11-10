package space

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

const (
	// landGuardHistoryDepth is how many first-parent commits of the tip the
	// high-water read walks (D-045 "recent" history).
	landGuardHistoryDepth = 200
	// maxLandGuardHistoryBytes bounds the commit bodies that read holds. A write
	// that cannot be judged is refused, so the bound is well above any real
	// history: 200 messages carrying a recovery trailer stay under a megabyte.
	maxLandGuardHistoryBytes = 8 << 20
)

// AheadError is the refusal of a binary older than a version the space's
// history already carries. It is in the ErrStaleBinaryVersion family, so every
// surface that handles that error handles this one, but keeps its own text:
// the sentinel's names min_binary_version, which is not what was compared here.
type AheadError struct {
	SpaceVersion  string
	BinaryVersion string
}

// Error implements error.
func (e *AheadError) Error() string {
	return fmt.Sprintf("the space is ahead of this binary: space has %s, this is %s; run `a2a update` and retry", e.SpaceVersion, e.BinaryVersion)
}

// Is reports membership of the ErrStaleBinaryVersion family.
func (e *AheadError) Is(target error) bool { return target == ErrStaleBinaryVersion }

// NewLandGuard builds the version guard a space judged by this binary runs
// before a write is landed (D-045). It reads the checkout at the tip the write
// would land on, so neither answer can be a stale mirror's:
//
//   - min_binary_version from the tip's space.yaml, compared as the funnel's own
//     floor is;
//   - the high-water, the highest version in an A2A-Built-By line on the tip's
//     last landGuardHistoryDepth first-parent commits.
//
// A binary strictly below either is refused and nothing moves. The same
// version compares equal, so a local build and a release of one X.Y.Z pass each
// other (D-045 accepts that gap). A build version that does not parse fails
// closed the way the funnel's floor does, once a floor or a high-water exists to
// compare it against: a development build keeps the refusal it has today. A tip
// that cannot be read is an error, never a pass.
func NewLandGuard(build version.Build) host.LandGuardFunc {
	return func(ctx context.Context, checkoutDir, tipSHA string) error {
		raw, err := os.ReadFile(filepath.Join(checkoutDir, "space.yaml")) //nolint:gosec // reason: the host built this checkout; the name is a constant
		if err != nil {
			return &Error{Op: "LandGuard", Input: tipSHA, Err: fmt.Errorf("read the tip's space.yaml: %w", err)}
		}
		manifest, err := ParseManifest(raw)
		if err != nil {
			return &Error{Op: "LandGuard", Input: tipSHA, Err: fmt.Errorf("parse the tip's space.yaml: %w", err)}
		}
		if floor := manifest.MinBinaryVersion; floor != "" {
			older, err := versionOlderThan(build.Version, floor)
			if err != nil {
				return err
			}
			if older {
				return fmt.Errorf("%w: the space is ahead of this binary: space requires min_binary_version %s, this is %s; run `a2a update` and retry",
					ErrStaleBinaryVersion, floor, build.Version)
			}
		}
		highWater, err := spaceHighWater(ctx, checkoutDir, tipSHA)
		if err != nil {
			return &Error{Op: "LandGuard", Input: tipSHA, Err: err}
		}
		if highWater == "" {
			return nil
		}
		older, err := versionOlderThan(build.Version, highWater)
		if err != nil {
			return err
		}
		if older {
			return &AheadError{SpaceVersion: highWater, BinaryVersion: build.Version}
		}
		return nil
	}
}

// spaceHighWater is the highest version in the A2A-Built-By lines of tip's recent
// first-parent history, or "" when none parses.
//
// It reads whole commit bodies and matches lines by prefix. git's trailer parser
// cannot be used: the build note is a paragraph of its own above the approval
// trailers, so `%(trailers)` does not see it on a commit that carries both.
func spaceHighWater(ctx context.Context, checkoutDir, tipSHA string) (string, error) {
	out, err := gitPlumbingBytes(ctx, checkoutDir, nil, nil, maxLandGuardHistoryBytes,
		"log", "--first-parent", "-n", strconv.Itoa(landGuardHistoryDepth), "--format=%B", tipSHA, "--")
	if err != nil {
		return "", fmt.Errorf("read the tip's build history: %w", err)
	}
	highest := ""
	for _, line := range strings.Split(string(out), "\n") {
		candidate, ok := builtByVersion(line)
		if !ok {
			continue
		}
		if highest == "" {
			highest = candidate
			continue
		}
		if older, olderErr := version.OlderThan(highest, candidate); olderErr == nil && older {
			highest = candidate
		}
	}
	return highest, nil
}

// builtByVersion is the X.Y.Z of one build-note line: `A2A-Built-By: a2a <v> (…)`,
// the shapes version.Build.Stamp writes. Anything else is not a version claim:
// a partial version would otherwise read as X.0.0 and could raise the high-water.
func builtByVersion(line string) (string, bool) {
	stamp, ok := strings.CutPrefix(line, builtByTrailerName+": a2a ")
	if !ok {
		return "", false
	}
	candidate, _, ok := strings.Cut(stamp, " (")
	if !ok {
		return "", false
	}
	canonical, err := version.Canonical(candidate)
	if err != nil || canonical != strings.TrimPrefix(candidate, "v") {
		return "", false
	}
	return canonical, true
}
