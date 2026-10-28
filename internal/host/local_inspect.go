package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalSpaceInspection is what a doctor reads about a local space's own
// machinery: whether its pre-receive hook is installed and current, whether
// anything other than a land has moved the base branch, and whether this
// process can write the repository.
type LocalSpaceInspection struct {
	// HookPresent: the pre-receive hook git would run for a push exists as a
	// file. That is <hooks dir>/pre-receive, where the hooks directory is the
	// one git resolves for the space (core.hooksPath from any config level,
	// else $GIT_DIR/hooks), so a hook in the space's own hooks/ that git does
	// not run is not present.
	HookPresent bool
	// HookExecutable: it has an executable bit (git skips a hook that has none).
	HookExecutable bool
	// HookCurrent: git runs hooks from the space's own hooks/ directory, and
	// the hook there is executable and equal, byte for byte, to what
	// PrepareLocalSpace writes.
	HookCurrent bool
	// HooksDirElsewhere is the directory git runs the space's hooks from when
	// that is not the space's own hooks/ (a global or system core.hooksPath,
	// say); "" when it is. When set, the guard is not in force whatever is in
	// hooks/, and PrepareLocalSpace (or `git --git-dir=<space> config
	// core.hooksPath hooks`) puts it back.
	HooksDirElsewhere string
	// MainNonLandCommits lists, newest first, every commit on the base
	// branch's first-parent chain after the root that is not a two-parent land
	// merge: a sign that something moved the branch around the land.
	MainNonLandCommits []string
	// Writable: this process can create files under refs/ and objects/.
	Writable bool
	// WritableDetail names what blocked a write when Writable is false.
	WritableDetail string
}

// LocalSpaceInspector is the optional capability a host implements to let
// doctor look at a local space's own machinery. Only *LocalHost implements it;
// obtain it by type assertion on a Host.
type LocalSpaceInspector interface {
	InspectLocalSpace(ctx context.Context) (LocalSpaceInspection, error)
}

// InspectLocalSpace implements LocalSpaceInspector. The base branch is the one
// the repository's HEAD names, which is how a local space is created and the
// branch the hook guards; a repository whose HEAD names no usable branch is an
// ErrInvalidRequest, since there is no base to inspect.
func (h *LocalHost) InspectLocalSpace(ctx context.Context) (LocalSpaceInspection, error) {
	const op = "InspectLocalSpace"
	base, err := h.baseBranch(ctx, op)
	if err != nil {
		return LocalSpaceInspection{}, err
	}
	var out LocalSpaceInspection
	hookPath, err := h.effectiveHookPath(ctx, op)
	if err != nil {
		return LocalSpaceInspection{}, err
	}
	ownDir := filepath.Join(h.dir, "hooks")
	if !sameDir(filepath.Dir(hookPath), ownDir) {
		out.HooksDirElsewhere = filepath.Dir(hookPath)
	}
	if info, statErr := os.Stat(hookPath); statErr == nil && info.Mode().IsRegular() {
		out.HookPresent = true
		out.HookExecutable = info.Mode().Perm()&0o111 != 0
		out.HookCurrent = out.HooksDirElsewhere == "" && hookIsCurrent(hookPath, []byte(preReceiveHook(base)))
	}
	if out.MainNonLandCommits, err = h.nonLandCommits(ctx, op, base); err != nil {
		return LocalSpaceInspection{}, err
	}
	out.Writable, out.WritableDetail = h.probeWritable()
	return out, nil
}

// effectiveHookPath asks git where it would look for the space's pre-receive
// hook: `rev-parse --git-path` applies core.hooksPath from every config level
// (and its ~ expansion) the way receive-pack does. A relative answer is
// relative to the directory hooks run in, which for a bare repository is the
// repository itself, so it is joined onto h.dir rather than this process's
// working directory.
func (h *LocalHost) effectiveHookPath(ctx context.Context, op string) (string, error) {
	res := h.run(ctx, 4096, "rev-parse", "--git-path", "hooks/"+preReceiveHookName)
	if res.err != nil || res.exitCode != 0 {
		return "", localGitError(op, "resolve the hooks directory", res)
	}
	path := strings.TrimSpace(string(res.stdout))
	if path == "" {
		return "", &Error{Op: op, Err: fmt.Errorf("%w: resolve the hooks directory: git printed no path", errLocalGit)}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.dir, path)
	}
	return filepath.Clean(path), nil
}

// sameDir reports whether a and b name one directory: the same inode when both
// can be stat'ed (so /var and /private/var on macOS agree), else equal cleaned
// paths.
func sameDir(a, b string) bool {
	ai, aErr := os.Stat(a)
	bi, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(ai, bi)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// baseBranch reads the branch HEAD names; "" when HEAD is not a branch ref.
func (h *LocalHost) baseBranch(ctx context.Context, op string) (string, error) {
	res := h.run(ctx, 1024, "symbolic-ref", "--quiet", "HEAD")
	if res.err != nil {
		return "", localGitError(op, "read HEAD", res)
	}
	if res.exitCode != 0 {
		return "", &Error{Op: op, Err: fmt.Errorf("%w: HEAD does not name a branch", ErrInvalidRequest)}
	}
	ref := strings.TrimSpace(string(res.stdout))
	base, ok := strings.CutPrefix(ref, headRefPrefix)
	if !ok || !safeBaseName(base) {
		return "", &Error{Op: op, Input: ref, Err: fmt.Errorf("%w: HEAD does not name a usable base branch", ErrInvalidRequest)}
	}
	return base, nil
}

// nonLandCommits walks the base branch's first-parent chain and returns every
// commit after the root that does not have exactly two parents.
func (h *LocalHost) nonLandCommits(ctx context.Context, op, base string) ([]string, error) {
	if _, exists, err := h.readRef(ctx, op, headRefPrefix+base); err != nil || !exists {
		return nil, err
	}
	res := h.run(ctx, maxRecoveryGitOutput, "rev-list", "--first-parent", "--parents", headRefPrefix+base)
	if res.err != nil || res.exitCode != 0 {
		return nil, localGitError(op, "walk the first-parent chain", res)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(res.stdout)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		parents := len(fields) - 1
		if parents == 0 || parents == 2 {
			continue
		}
		out = append(out, fields[0])
	}
	return out, nil
}

// probeWritable creates and removes a file under refs/ and objects/, the two
// directories a land writes. The first one that refuses is named.
func (h *LocalHost) probeWritable() (writable bool, detail string) {
	for _, sub := range []string{"refs", "objects"} {
		f, err := os.CreateTemp(filepath.Join(h.dir, sub), ".a2a-probe-")
		if err != nil {
			return false, fmt.Sprintf("cannot write under %s/: %v", sub, err)
		}
		name := f.Name()
		closeErr := f.Close()
		removeErr := os.Remove(name)
		if closeErr != nil || removeErr != nil {
			return false, fmt.Sprintf("cannot write under %s/: %v", sub, errors.Join(closeErr, removeErr))
		}
	}
	return true, ""
}
