package host

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxRecoveryGitOutput      = 1 << 20
	maxRecoveryGitErrorOutput = 64 << 10
	maxRecoveryCommitMessage  = 256 << 10
	maxRecoveryPathBytes      = 4096
	maxRecoveryChangedPaths   = 256
	maxRecoveryBlobBytes      = 16 << 20
	maxRecoveryAggregateBytes = 32 << 20
)

// RemoteRecoveryChange is the exact before/after Git-tree evidence for one
// changed path. Empty before fields mean the path was added; empty after
// fields mean it was deleted. Digests use the canonical sha256:<hex> wire
// form and modes are restricted to regular-file 100644/100755.
type RemoteRecoveryChange struct {
	BeforeDigest string
	BeforeMode   string
	AfterDigest  string
	AfterMode    string
}

// ReadRemoteRecoveryCommit fetches exactly one remote branch into an
// invocation-private ref and returns the complete first-parent change set.
// It deliberately uses only Git object plumbing: a recovery probe must never
// observe or mutate a shared worktree.
func (h *GitHubHost) ReadRemoteRecoveryCommit(
	ctx context.Context,
	repoDir string,
	remoteURL string,
	repository Repo,
	branch string,
	credential Credential,
) (
	observedRepository Repo,
	observedBranch string,
	headSHA string,
	parentSHA string,
	commitMessage string,
	changedFiles map[string]RemoteRecoveryChange,
	exists bool,
	err error,
) {
	const op = "ReadRemoteRecoveryCommit"
	if repoDir == "" || remoteURL == "" || strings.HasPrefix(remoteURL, "-") ||
		repository.Owner == "" || repository.Name == "" || branch == "" ||
		!utf8.ValidString(branch) || len(branch) > maxRecoveryPathBytes {
		return Repo{}, "", "", "", "", nil, false, recoveryRequestError(op)
	}
	remoteRef := "refs/heads/" + branch
	if result := runRecoveryGit(ctx, maxRecoveryGitOutput,
		"check-ref-format", remoteRef,
	); result.err != nil || result.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryRequestError(op)
	}

	authArgs := recoveryGitAuthArgs(credential)
	lsArgs := append([]string{"-C", repoDir}, authArgs...)
	lsArgs = append(lsArgs, "ls-remote", "--exit-code", "--heads", remoteURL, remoteRef)
	ls := runRecoveryGit(ctx, 4096, lsArgs...)
	if ls.err != nil {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read remote ref", ls.err)
	}
	if ls.exitCode == 2 && len(ls.stdout) == 0 {
		return repository, branch, "", "", "", nil, false, nil
	}
	if ls.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read remote ref", nil)
	}
	if _, ok := parseExactRemoteRef(ls.stdout, remoteRef); !ok {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "malformed remote ref", nil)
	}

	privateRef, randomErr := recoveryPrivateRef()
	if randomErr != nil {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "allocate private ref", randomErr)
	}
	// The ref name is random and therefore cannot pre-exist legitimately.
	// Arm cleanup before fetch because git may update the ref and still fail
	// later (including when its bounded diagnostic output overflows).
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			_ = runRecoveryGit(context.WithoutCancel(ctx), 1024,
				"-C", repoDir, "update-ref", "-d", privateRef,
			)
		}
	}()

	fetchArgs := append([]string{"-C", repoDir}, authArgs...)
	fetchArgs = append(fetchArgs, "fetch", "--no-tags", "--no-write-fetch-head", remoteURL, "+"+remoteRef+":"+privateRef)
	fetched := runRecoveryGit(ctx, maxRecoveryGitOutput, fetchArgs...)
	if fetched.err != nil || fetched.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "fetch exact remote ref", fetched.err)
	}
	resolved := runRecoveryGit(ctx, 256, "-C", repoDir, "rev-parse", "--verify", privateRef+"^{commit}")
	if resolved.err != nil || resolved.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "resolve fetched commit", resolved.err)
	}
	headSHA = strings.TrimSuffix(string(resolved.stdout), "\n")
	if !validRecoveryGitOID(headSHA) || strings.Contains(headSHA, "\n") {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "malformed fetched commit", nil)
	}

	parentsResult := runRecoveryGit(ctx, 1024, "-C", repoDir, "rev-list", "--parents", "-n", "1", headSHA)
	if parentsResult.err != nil || parentsResult.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read commit parents", parentsResult.err)
	}
	parentFields := strings.Fields(string(parentsResult.stdout))
	if len(parentFields) != 2 || parentFields[0] != headSHA || !validRecoveryGitOID(parentFields[1]) {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "root or merge commit is not recoverable", nil)
	}
	parentSHA = parentFields[1]

	messageResult := runRecoveryGit(ctx, maxRecoveryCommitMessage, "-C", repoDir, "show", "-s", "--format=%B", headSHA)
	if messageResult.err != nil || messageResult.exitCode != 0 ||
		!utf8.Valid(messageResult.stdout) || bytes.IndexByte(messageResult.stdout, 0) >= 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read canonical commit message", messageResult.err)
	}

	diffResult := runRecoveryGit(ctx, maxRecoveryGitOutput,
		"-C", repoDir, "diff-tree", "-r", "--no-commit-id", "--name-status", "-z",
		"--no-renames", parentSHA, headSHA, "--",
	)
	if diffResult.err != nil || diffResult.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read complete commit diff", diffResult.err)
	}
	statuses, parseErr := parseRecoveryDiff(diffResult.stdout)
	if parseErr != nil {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "invalid commit diff", parseErr)
	}

	changes := make(map[string]RemoteRecoveryChange, len(statuses))
	totalBytes := 0
	for _, change := range statuses {
		var observed RemoteRecoveryChange
		if change.status == "M" || change.status == "D" {
			beforeDigest, beforeMode, beforeExists, readErr := readRecoveryTreeBlob(ctx, repoDir, parentSHA, change.path, &totalBytes)
			if readErr != nil || !beforeExists {
				return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read prior changed blob", readErr)
			}
			observed.BeforeDigest, observed.BeforeMode = beforeDigest, beforeMode
		}
		if change.status == "A" || change.status == "M" {
			afterDigest, afterMode, afterExists, readErr := readRecoveryTreeBlob(ctx, repoDir, headSHA, change.path, &totalBytes)
			if readErr != nil || !afterExists {
				return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "read resulting changed blob", readErr)
			}
			observed.AfterDigest, observed.AfterMode = afterDigest, afterMode
		}
		changes[change.path] = observed
	}

	cleanup := runRecoveryGit(context.WithoutCancel(ctx), 1024, "-C", repoDir, "update-ref", "-d", privateRef)
	if cleanup.err != nil || cleanup.exitCode != 0 {
		return Repo{}, "", "", "", "", nil, false, recoveryGitError(op, "remove private ref", cleanup.err)
	}
	cleanupNeeded = false
	return repository, branch, headSHA, parentSHA, string(messageResult.stdout), changes, true, nil
}

func readRecoveryTreeBlob(ctx context.Context, repoDir, commitSHA, filePath string, totalBytes *int) (digest, mode string, exists bool, err error) {
	entryResult := runRecoveryGit(ctx, maxRecoveryPathBytes+256,
		"-C", repoDir, "ls-tree", "-z", "--full-tree", commitSHA, "--", filePath,
	)
	if entryResult.err != nil || entryResult.exitCode != 0 {
		return "", "", false, entryResult.err
	}
	if len(entryResult.stdout) == 0 {
		return "", "", false, nil
	}
	if entryResult.stdout[len(entryResult.stdout)-1] != 0 || bytes.Count(entryResult.stdout, []byte{0}) != 1 {
		return "", "", false, errors.New("tree entry is not one NUL-terminated record")
	}
	record := string(entryResult.stdout[:len(entryResult.stdout)-1])
	metadata, observedPath, ok := strings.Cut(record, "\t")
	fields := strings.Fields(metadata)
	if !ok || observedPath != filePath || len(fields) != 3 || fields[1] != "blob" ||
		(fields[0] != "100644" && fields[0] != "100755") || !validRecoveryGitOID(fields[2]) {
		return "", "", false, errors.New("tree entry is not an exact regular file")
	}
	sizeResult := runRecoveryGit(ctx, 128, "-C", repoDir, "cat-file", "-s", fields[2])
	if sizeResult.err != nil || sizeResult.exitCode != 0 {
		return "", "", false, sizeResult.err
	}
	sizeText := strings.TrimSuffix(string(sizeResult.stdout), "\n")
	size, sizeErr := strconv.Atoi(sizeText)
	if sizeErr != nil || size < 0 || size > maxRecoveryBlobBytes || size > maxRecoveryAggregateBytes-*totalBytes {
		return "", "", false, errors.New("changed blob exceeds recovery bounds")
	}
	blobResult := runRecoveryGit(ctx, size, "-C", repoDir, "cat-file", "blob", fields[2])
	if blobResult.err != nil || blobResult.exitCode != 0 || len(blobResult.stdout) != size {
		return "", "", false, errors.New("changed blob read is incomplete")
	}
	*totalBytes += size
	hash := sha256.Sum256(blobResult.stdout)
	return "sha256:" + hex.EncodeToString(hash[:]), fields[0], true, nil
}

type recoveryDiffChange struct {
	status string
	path   string
}

func parseRecoveryDiff(raw []byte) ([]recoveryDiffChange, error) {
	if len(raw) == 0 {
		return []recoveryDiffChange{}, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, errors.New("diff is not NUL terminated")
	}
	fields := bytes.Split(raw[:len(raw)-1], []byte{0})
	if len(fields)%2 != 0 {
		return nil, errors.New("diff field count is invalid")
	}
	changes := make([]recoveryDiffChange, 0, len(fields)/2)
	seen := make(map[string]struct{}, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		status := string(fields[i])
		if status != "A" && status != "M" && status != "D" {
			return nil, errors.New("rename, copy, type, or unmerged status is not recoverable")
		}
		if len(changes) >= maxRecoveryChangedPaths {
			return nil, errors.New("changed path count exceeds recovery bound")
		}
		filePath := string(fields[i+1])
		if !validRecoveryPath(filePath) {
			return nil, errors.New("changed path is malformed")
		}
		if _, duplicate := seen[filePath]; duplicate {
			return nil, errors.New("changed path is duplicated")
		}
		seen[filePath] = struct{}{}
		changes = append(changes, recoveryDiffChange{status: status, path: filePath})
	}
	return changes, nil
}

func validRecoveryPath(value string) bool {
	if value == "" || len(value) > maxRecoveryPathBytes || !utf8.ValidString(value) ||
		strings.HasPrefix(value, "/") || strings.ContainsRune(value, 0) {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func parseExactRemoteRef(raw []byte, wantRef string) (string, bool) {
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", false
	}
	line := strings.TrimSuffix(string(raw), "\n")
	if strings.Contains(line, "\n") {
		return "", false
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 2 || fields[1] != wantRef || !validRecoveryGitOID(fields[0]) {
		return "", false
	}
	return fields[0], true
}

func validRecoveryGitOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func recoveryPrivateRef() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return "refs/a2a/recovery/" + hex.EncodeToString(entropy[:]), nil
}

func recoveryGitAuthArgs(credential Credential) []string {
	if credential.Token == "" {
		return nil
	}
	basicAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + credential.Token))
	return []string{"-c", "http.extraheader=AUTHORIZATION: basic " + basicAuth}
}

type boundedRecoveryBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedRecoveryBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		toWrite := len(p)
		if toWrite > remaining {
			toWrite = remaining
		}
		_, _ = b.buffer.Write(p[:toWrite]) // bytes.Buffer.Write never returns an error.
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

type recoveryGitResult struct {
	stdout   []byte
	exitCode int
	err      error
}

func runRecoveryGit(ctx context.Context, stdoutLimit int, args ...string) recoveryGitResult {
	if stdoutLimit < 0 {
		return recoveryGitResult{exitCode: -1, err: errors.New("negative output bound")}
	}
	stdout := &boundedRecoveryBuffer{limit: stdoutLimit}
	stderr := &boundedRecoveryBuffer{limit: maxRecoveryGitErrorOutput}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return recoveryGitResult{exitCode: -1, err: errors.New("git output exceeded bound")}
	}
	if err == nil {
		return recoveryGitResult{stdout: append([]byte(nil), stdout.buffer.Bytes()...), exitCode: 0}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return recoveryGitResult{stdout: append([]byte(nil), stdout.buffer.Bytes()...), exitCode: exitErr.ExitCode()}
	}
	return recoveryGitResult{exitCode: -1, err: err}
}

func recoveryRequestError(op string) error {
	return &Error{Op: op, Err: ErrInvalidRequest}
}

func recoveryGitError(op, stage string, cause error) error {
	if cause != nil && errors.Is(cause, context.Canceled) {
		return &Error{Op: op, Err: fmt.Errorf("%w: %s: %w", ErrRequestFailed, stage, context.Canceled)}
	}
	if cause != nil && errors.Is(cause, context.DeadlineExceeded) {
		return &Error{Op: op, Err: fmt.Errorf("%w: %s: %w", ErrRequestFailed, stage, context.DeadlineExceeded)}
	}
	// Never render git stderr, argv, or exec's command string here. They can
	// contain the transient Basic header and remote/provider-controlled data.
	return &Error{Op: op, Err: fmt.Errorf("%w: %s", ErrRequestFailed, stage)}
}
