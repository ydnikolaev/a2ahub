package livee2e

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// gitMainContains proves both halves of a refreshed mirror's read contract:
// the merged commit is reachable from origin/main, and the working tree from
// which product readers fold data is parked on that exact remote tip.
func gitMainContains(ctx context.Context, dir, sha string) (bool, error) {
	if !looksLikeGitObjectID(sha) {
		return false, fmt.Errorf("livee2e: merged commit %q is not a full git object id", sha)
	}
	head, err := gitRevParse(ctx, dir, "HEAD")
	if err != nil {
		return false, err
	}
	main, err := gitRevParse(ctx, dir, "origin/main")
	if err != nil {
		return false, err
	}
	if head != main {
		return false, nil
	}

	// sha has passed the full hex object-id check above; it cannot be parsed
	// as a flag or another argv shape.
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", sha, "origin/main") //nolint:gosec // reason: provider SHA is constrained to a full 40/64-character hexadecimal git object id
	cmd.Dir = dir
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("livee2e: test whether origin/main contains %s: %w: %s",
		sha, err, strings.TrimSpace(errBuf.String()))
}

func gitRevParse(ctx context.Context, dir, ref string) (string, error) {
	var cmd *exec.Cmd
	switch ref {
	case "HEAD":
		cmd = exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	case "origin/main":
		cmd = exec.CommandContext(ctx, "git", "rev-parse", "origin/main")
	default:
		return "", fmt.Errorf("livee2e: refusing unexpected mirror ref %q", ref)
	}
	cmd.Dir = dir
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("livee2e: resolve %s in mirror: %w: %s", ref, err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func looksLikeGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
