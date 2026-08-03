package space

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const maxTreeCommitMetadataBytes = 64 * 1024

var errTreeCommitOutputLimit = errors.New("space: git plumbing output exceeds limit")

// treeCommitRequest describes one worktree-independent commit. BaseRef is
// resolved once and becomes the new commit's parent; UpdateRef is moved only
// after every precondition has been checked and the commit object exists.
// Mutations must already have passed the funnel's authorization checks.
type treeCommitRequest struct {
	RepoDir    string
	BaseRef    string
	UpdateRef  string
	Mutations  []Mutation
	Message    string
	AuthorName string
	AuthorMail string
}

// commitTreeFromBase creates a commit through a private temporary Git index.
// It never reads or writes a worktree path: both preconditions and mutations
// are evaluated against BaseRef's tree. The caller must hold the mirror lock
// for RepoDir, which serializes the object/ref transaction with every other
// funnel writer using this shared clone. Fresh is false when the resulting
// tree equals BaseRef's tree; UpdateRef is still created or reset to BaseRef.
func commitTreeFromBase(ctx context.Context, lock *MirrorLock, req treeCommitRequest) (string, bool, error) {
	if err := mutateTree(lock, req.RepoDir); err != nil {
		return "", false, err
	}
	if err := validateTreeCommitRequest(ctx, req); err != nil {
		return "", false, err
	}

	baseSHA, err := gitPlumbingOutput(ctx, req.RepoDir, nil, nil, maxTreeCommitMetadataBytes,
		"rev-parse", "--verify", "--end-of-options", req.BaseRef+"^{commit}")
	if err != nil {
		return "", false, fmt.Errorf("resolve tree commit base %q: %w", req.BaseRef, err)
	}
	baseSHA = strings.TrimSpace(baseSHA)
	if baseSHA == "" {
		return "", false, fmt.Errorf("resolve tree commit base %q: empty object id", req.BaseRef)
	}
	baseTreeSHA, err := gitPlumbingOutput(ctx, req.RepoDir, nil, nil, maxTreeCommitMetadataBytes,
		"rev-parse", "--verify", baseSHA+"^{tree}")
	if err != nil {
		return "", false, fmt.Errorf("resolve base tree %s: %w", baseSHA, err)
	}

	// This full read-only pass precedes hash-object, commit-tree, and
	// update-ref. A stale item therefore cannot partially publish another
	// item from the same mutation set.
	for _, mutation := range req.Mutations {
		if err := verifyBaseTreeMutation(ctx, req.RepoDir, baseSHA, mutation); err != nil {
			return "", false, err
		}
	}

	tmpDir, err := os.MkdirTemp("", "a2a-tree-index-")
	if err != nil {
		return "", false, fmt.Errorf("create private git index directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir) // reason: best-effort cleanup of an invocation-owned temporary directory.
	}()
	indexEnv := []string{"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index")}
	if _, err := gitPlumbingOutput(ctx, req.RepoDir, indexEnv, nil, maxTreeCommitMetadataBytes,
		"read-tree", baseSHA); err != nil {
		return "", false, fmt.Errorf("seed private git index: %w", err)
	}

	zeroOID := strings.Repeat("0", len(baseSHA))
	for _, mutation := range req.Mutations {
		var indexEntry string
		switch mutation.Operation {
		case MutationWrite:
			blobSHA, hashErr := gitPlumbingOutput(ctx, req.RepoDir, indexEnv, mutation.Bytes,
				maxTreeCommitMetadataBytes, "hash-object", "-w", "--stdin")
			if hashErr != nil {
				return "", false, fmt.Errorf("hash tree mutation %s: %w", mutation.Path, hashErr)
			}
			mode := mutationResultMode(mutation)
			indexEntry = mode + " " + strings.TrimSpace(blobSHA) + "\t" + mutation.Path + "\x00"
		case MutationDelete:
			indexEntry = "0 " + zeroOID + "\t" + mutation.Path + "\x00"
		default:
			return "", false, fmt.Errorf("%w: unknown operation %q", ErrMutationInvalid, mutation.Operation)
		}
		if _, err := gitPlumbingOutput(ctx, req.RepoDir, indexEnv, []byte(indexEntry), maxTreeCommitMetadataBytes,
			"update-index", "-z", "--index-info"); err != nil {
			return "", false, fmt.Errorf("stage tree mutation %s: %w", mutation.Path, err)
		}
	}

	treeSHA, err := gitPlumbingOutput(ctx, req.RepoDir, indexEnv, nil, maxTreeCommitMetadataBytes, "write-tree")
	if err != nil {
		return "", false, fmt.Errorf("write mutated tree: %w", err)
	}
	treeSHA = strings.TrimSpace(treeSHA)
	if treeSHA == strings.TrimSpace(baseTreeSHA) {
		if _, err := gitPlumbingOutput(ctx, req.RepoDir, indexEnv, nil, maxTreeCommitMetadataBytes,
			"update-ref", req.UpdateRef, baseSHA); err != nil {
			return "", false, fmt.Errorf("publish unchanged tree ref %s: %w", req.UpdateRef, err)
		}
		return baseSHA, false, nil
	}
	authorEnv := append(indexEnv,
		"GIT_AUTHOR_NAME="+req.AuthorName,
		"GIT_AUTHOR_EMAIL="+req.AuthorMail,
		"GIT_COMMITTER_NAME="+req.AuthorName,
		"GIT_COMMITTER_EMAIL="+req.AuthorMail,
	)
	commitSHA, err := gitPlumbingOutput(ctx, req.RepoDir, authorEnv, []byte(req.Message), maxTreeCommitMetadataBytes,
		"commit-tree", treeSHA, "-p", baseSHA, "-F", "-")
	if err != nil {
		return "", false, fmt.Errorf("create tree commit: %w", err)
	}
	commitSHA = strings.TrimSpace(commitSHA)
	if _, err := gitPlumbingOutput(ctx, req.RepoDir, indexEnv, nil, maxTreeCommitMetadataBytes,
		"update-ref", req.UpdateRef, commitSHA); err != nil {
		return "", false, fmt.Errorf("publish tree commit ref %s: %w", req.UpdateRef, err)
	}
	return commitSHA, true, nil
}

func validateTreeCommitRequest(ctx context.Context, req treeCommitRequest) error {
	if req.RepoDir == "" || req.BaseRef == "" || req.UpdateRef == "" || req.Message == "" ||
		req.AuthorName == "" || req.AuthorMail == "" || len(req.Mutations) == 0 || len(req.Mutations) > maxMutationCount {
		return fmt.Errorf("%w: incomplete tree commit request", ErrMutationInvalid)
	}
	for _, value := range []string{req.BaseRef, req.UpdateRef, req.AuthorName, req.AuthorMail} {
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%w: tree commit metadata contains a control character", ErrMutationInvalid)
		}
	}
	if strings.ContainsRune(req.Message, '\x00') {
		return fmt.Errorf("%w: commit message contains NUL", ErrMutationInvalid)
	}
	// The funnel publishes only local branch refs. Besides documenting that
	// integration boundary, this prefix prevents a ref value from being
	// interpreted as an update-ref option before Git validates its grammar.
	if !strings.HasPrefix(req.UpdateRef, "refs/heads/") {
		return fmt.Errorf("%w: update ref %q is not a local branch", ErrMutationInvalid, req.UpdateRef)
	}
	if _, err := gitPlumbingOutput(ctx, req.RepoDir, nil, nil, maxTreeCommitMetadataBytes,
		"check-ref-format", req.UpdateRef); err != nil {
		return fmt.Errorf("%w: invalid update ref %q", ErrMutationInvalid, req.UpdateRef)
	}
	seen := make(map[string]struct{}, len(req.Mutations))
	total := 0
	for _, mutation := range req.Mutations {
		if !isCleanRelativePath(mutation.Path) || strings.ContainsRune(mutation.Path, '\x00') {
			return fmt.Errorf("%w: path %q is not clean and relative", ErrMutationInvalid, mutation.Path)
		}
		if _, duplicate := seen[mutation.Path]; duplicate {
			return fmt.Errorf("%w: %s", ErrMutationDuplicatePath, mutation.Path)
		}
		seen[mutation.Path] = struct{}{}
		switch mutation.Operation {
		case MutationWrite:
			if mutation.Bytes == nil || len(mutation.Bytes) > maxMutationBytes || (mutation.Mode != "" && mutation.Mode != "100644" && mutation.Mode != "100755") {
				return fmt.Errorf("%w: write mutation %q has an invalid shape", ErrMutationInvalid, mutation.Path)
			}
			total += len(mutation.Bytes)
			if total > maxMutationAggregateBytes {
				return fmt.Errorf("%w: aggregate write bytes exceed %d", ErrMutationInvalid, maxMutationAggregateBytes)
			}
			if mutation.ExpectedBefore != nil && !validMutationPrecondition(*mutation.ExpectedBefore) {
				return fmt.Errorf("%w: write mutation %q has an invalid precondition", ErrMutationInvalid, mutation.Path)
			}
		case MutationDelete:
			if mutation.Bytes != nil || mutation.ExpectedBefore == nil || !validMutationPrecondition(*mutation.ExpectedBefore) {
				return fmt.Errorf("%w: delete mutation %q has an invalid shape", ErrMutationInvalid, mutation.Path)
			}
		default:
			return fmt.Errorf("%w: unknown operation %q", ErrMutationInvalid, mutation.Operation)
		}
	}
	return nil
}

func mutationResultMode(mutation Mutation) string {
	if mutation.Mode == "100644" || mutation.Mode == "100755" {
		return mutation.Mode
	}
	if mutation.ExpectedBefore != nil && mutation.ExpectedBefore.Mode == "100755" {
		return "100755"
	}
	return "100644"
}

type baseTreeEntry struct {
	mode, kind, oid string
}

func verifyBaseTreeMutation(ctx context.Context, repoDir, baseSHA string, mutation Mutation) error {
	entry, exists, err := readBaseTreeEntry(ctx, repoDir, baseSHA, mutation.Path)
	if err != nil {
		return err
	}
	if mutation.ExpectedBefore == nil {
		if exists && (entry.kind != "blob" || (entry.mode != "100644" && entry.mode != "100755")) {
			return mutationPreconditionError(mutation.Path, errors.New("base-tree destination is not a regular file"))
		}
		return nil
	}
	if !exists || entry.kind != "blob" || entry.mode != mutation.ExpectedBefore.Mode {
		return mutationPreconditionError(mutation.Path, nil)
	}
	sizeText, err := gitPlumbingOutput(ctx, repoDir, nil, nil, maxTreeCommitMetadataBytes,
		"cat-file", "-s", entry.oid)
	if err != nil {
		return mutationPreconditionError(mutation.Path, err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeText), 10, 64)
	if err != nil || size < 0 || size > maxMutationBytes {
		return mutationPreconditionError(mutation.Path, errors.New("base-tree blob exceeds the bounded read limit"))
	}
	raw, err := gitPlumbingBytes(ctx, repoDir, nil, nil, size+1, "cat-file", "blob", entry.oid)
	if err != nil {
		return mutationPreconditionError(mutation.Path, err)
	}
	if int64(len(raw)) != size {
		return mutationPreconditionError(mutation.Path, errors.New("base-tree blob size changed while reading"))
	}
	digest := sha256.Sum256(raw)
	if "sha256:"+fmt.Sprintf("%x", digest[:]) != mutation.ExpectedBefore.Digest {
		return mutationPreconditionError(mutation.Path, nil)
	}
	return nil
}

func readBaseTreeEntry(ctx context.Context, repoDir, baseSHA, path string) (baseTreeEntry, bool, error) {
	raw, err := gitPlumbingBytes(ctx, repoDir, nil, nil, maxTreeCommitMetadataBytes,
		"ls-tree", "-z", "--full-tree", baseSHA, "--", path)
	if err != nil {
		return baseTreeEntry{}, false, fmt.Errorf("inspect base-tree path %s: %w", path, err)
	}
	if len(raw) == 0 {
		return baseTreeEntry{}, false, nil
	}
	if raw[len(raw)-1] != 0 || bytes.Count(raw, []byte{0}) != 1 {
		return baseTreeEntry{}, false, fmt.Errorf("inspect base-tree path %s: unexpected ls-tree record", path)
	}
	record := string(raw[:len(raw)-1])
	metadata, gotPath, ok := strings.Cut(record, "\t")
	fields := strings.Fields(metadata)
	if !ok || gotPath != path || len(fields) != 3 {
		return baseTreeEntry{}, false, fmt.Errorf("inspect base-tree path %s: malformed ls-tree record", path)
	}
	return baseTreeEntry{mode: fields[0], kind: fields[1], oid: fields[2]}, true, nil
}

func gitPlumbingOutput(ctx context.Context, dir string, extraEnv []string, stdin []byte, limit int64, args ...string) (string, error) {
	raw, err := gitPlumbingBytes(ctx, dir, extraEnv, stdin, limit, args...)
	return string(bytes.TrimSpace(raw)), err
}

func gitPlumbingBytes(ctx context.Context, dir string, extraEnv []string, stdin []byte, limit int64, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout := &boundedCommandBuffer{remaining: limit}
	stderr := &boundedCommandBuffer{remaining: maxTreeCommitMetadataBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

type boundedCommandBuffer struct {
	bytes.Buffer
	remaining int64
}

func (b *boundedCommandBuffer) Write(p []byte) (int, error) {
	if int64(len(p)) > b.remaining {
		allowed := int(b.remaining)
		if allowed > 0 {
			_, _ = b.Buffer.Write(p[:allowed]) // bytes.Buffer.Write never fails.
			b.remaining = 0
		}
		return allowed, errTreeCommitOutputLimit
	}
	n, err := b.Buffer.Write(p)
	b.remaining -= int64(n)
	return n, err
}
