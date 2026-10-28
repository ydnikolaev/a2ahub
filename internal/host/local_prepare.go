package host

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PreReceiveRefusal is the sentence the space's pre-receive hook prints to a
// `git push` that touches the base branch or refs/a2a/*; %s is the refused
// ref. It names the ref and the command that lands a write. The hook script
// PrepareLocalSpace writes embeds this constant verbatim as a printf format,
// so it must contain exactly one verb (%s) and no single quote, backslash or
// further percent sign; a test pins that.
const PreReceiveRefusal = "a2a: refusing %s: this local space lands writes only through a2a (run `a2a submit`), never through git push"

const (
	// localMarkerKey is the git-config key that marks a bare repository as a
	// local space; localMarkerValue is its value. host.Open reads it.
	localMarkerKey   = "a2a.host"
	localMarkerValue = "local"
	// preReceiveHookName is the hook file PrepareLocalSpace owns.
	preReceiveHookName = "pre-receive"
	// markerProbeTimeout bounds the one git call Open makes to read the marker.
	markerProbeTimeout = 10 * time.Second
)

// PrepareLocalSpace turns the bare repository at dir into a local space: it
// writes the a2a.host=local marker into its git config and the pre-receive
// hook that refuses a push to refs/heads/<base> and to refs/a2a/*. It is
// idempotent: a re-run rewrites the hook to the current bytes and mode, and is
// otherwise a no-op (an up-to-date hook is not rewritten).
//
// Only `space init --local` calls it. Connecting to a space reads the marker
// through Open and writes nothing, so an unmarked bare repository is never
// turned into a local space by a participant.
func PrepareLocalSpace(ctx context.Context, dir, base string) error {
	const op = "PrepareLocalSpace"
	if dir == "" || !safeBaseName(base) {
		return &Error{Op: op, Input: base, Err: ErrInvalidRequest}
	}
	gitDir, err := filepath.Abs(dir)
	if err != nil {
		return &Error{Op: op, Input: dir, Err: fmt.Errorf("%w: %w", ErrInvalidRequest, err)}
	}
	bare := runRecoveryGit(ctx, 64, "--git-dir="+gitDir, "rev-parse", "--is-bare-repository")
	if bare.err != nil || bare.exitCode != 0 || strings.TrimSpace(string(bare.stdout)) != "true" {
		return &Error{Op: op, Input: dir, Err: fmt.Errorf("%w: not a bare git repository", ErrInvalidRequest)}
	}
	if !localMarked(ctx, gitDir) {
		set := runRecoveryGit(ctx, 1024, "--git-dir="+gitDir, "config", "--local", localMarkerKey, localMarkerValue)
		if set.err != nil || set.exitCode != 0 {
			return localGitError(op, "write the "+localMarkerKey+" marker", set)
		}
	}
	return installPreReceiveHook(op, gitDir, base)
}

// installPreReceiveHook writes hooks/pre-receive unless it is already current.
// The new bytes go to a sibling file created with the executable mode and are
// renamed over the hook: os.WriteFile keeps the mode of a file that already
// exists, so writing in place could not restore the executable bit of a hook
// that was edited down to 0644.
func installPreReceiveHook(op, gitDir, base string) error {
	hooksDir := filepath.Join(gitDir, "hooks")
	hookPath := filepath.Join(hooksDir, preReceiveHookName)
	want := []byte(preReceiveHook(base))
	if hookIsCurrent(hookPath, want) {
		return nil
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return &Error{Op: op, Input: hooksDir, Err: fmt.Errorf("%w: %w", errLocalGit, err)}
	}
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return &Error{Op: op, Input: hookPath, Err: fmt.Errorf("%w: %w", errLocalGit, err)}
	}
	staged := hookPath + ".new-" + hex.EncodeToString(entropy[:])
	if err := os.WriteFile(staged, want, 0o755); err != nil {
		return &Error{Op: op, Input: hookPath, Err: fmt.Errorf("%w: %w", errLocalGit, err)}
	}
	if err := os.Rename(staged, hookPath); err != nil {
		removeErr := os.Remove(staged)
		return &Error{Op: op, Input: hookPath, Err: fmt.Errorf("%w: %w", errLocalGit, errors.Join(err, removeErr))}
	}
	return nil
}

// hookIsCurrent reports whether the file at path has exactly the wanted bytes
// and an executable bit. A missing or unreadable file is not current.
func hookIsCurrent(path string, want []byte) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return false
	}
	// Read through a root on the hooks directory, so a hook that is a symlink
	// out of the repository is read as "not current" rather than followed.
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false
	}
	got, readErr := root.ReadFile(filepath.Base(path))
	closeErr := root.Close()
	return readErr == nil && closeErr == nil && bytes.Equal(got, want)
}

// preReceiveHook is the one copy of the hook's bytes: PrepareLocalSpace writes
// it and InspectLocalSpace compares against it. It refuses any update, create
// or delete of refs/heads/<base> and of refs/a2a/*, and passes everything else
// (refs/heads/a2a/... head branches included). base has been vetted by
// safeBaseName, so single-quoting it is exact.
func preReceiveHook(base string) string {
	return "#!/bin/sh\n" +
		"# Written by a2a (host.PrepareLocalSpace); a re-run restores it. Do not edit.\n" +
		"status=0\n" +
		"while read -r old new ref; do\n" +
		"\tcase \"$ref\" in\n" +
		"\t'refs/heads/" + base + "' | 'refs/a2a/'*)\n" +
		"\t\tprintf '" + PreReceiveRefusal + "\\n' \"$ref\" >&2\n" +
		"\t\tstatus=1\n" +
		"\t\t;;\n" +
		"\tesac\n" +
		"done\n" +
		"exit \"$status\"\n"
}

// safeBaseName admits the branch names a hook script can embed without
// quoting: letters, digits and . _ - /, valid as a git branch name. Anything
// else (a quote, a dollar sign, a space) is refused rather than escaped.
func safeBaseName(base string) bool {
	if base == "" || strings.HasPrefix(base, "/") || strings.HasSuffix(base, "/") ||
		strings.Contains(base, "//") || strings.Contains(base, "..") ||
		strings.HasSuffix(base, ".") || strings.HasSuffix(base, ".lock") {
		return false
	}
	for _, c := range base {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-' || c == '/':
		default:
			return false
		}
	}
	return true
}

// localMarked reports whether the git directory gitDir carries the
// a2a.host=local marker. It reads that repository's own config only
// (--git-dir and --local): no discovery walks up into an enclosing repository
// and no global or system config can mark a path. Any failure, including a path
// that is not a repository at all, reads as "not marked".
func localMarked(ctx context.Context, gitDir string) bool {
	res := runRecoveryGit(ctx, 256, "--git-dir="+gitDir, "config", "--local", "--get", localMarkerKey)
	return res.err == nil && res.exitCode == 0 && strings.TrimSpace(string(res.stdout)) == localMarkerValue
}
