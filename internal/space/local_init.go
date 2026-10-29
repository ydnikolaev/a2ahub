package space

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LocalSpaceBaseBranch is the branch a local space's bare repository is created
// with, that HEAD names, and that the pre-receive hook guards.
const LocalSpaceBaseBranch = "main"

// localInitAuthorName and localInitAuthorMail author the root commit. They are
// fixed on purpose: the commit is the product's own bootstrap, not a participant's
// write, and it must not depend on (or fail for lack of) a git identity.
const (
	localInitAuthorName = "a2a"
	localInitAuthorMail = "a2a@local.a2a.invalid"
	localInitMessage    = "a2a(space-init): create the local space"
	// localInitReadLimit bounds the one blob read back by ReadLocalSpaceRepo.
	localInitReadLimit = 1 << 20
)

// InitLocalSpaceRepo creates the bare repository at dir with LocalSpaceBaseBranch
// as its initial branch and as HEAD, and writes ONE root commit on
// refs/heads/<base> holding files. It touches no working tree, pushes nothing
// and installs no hook: the caller arms the guard AFTER this returns, because a
// pre-receive hook would refuse the bootstrap itself.
//
// dir may be missing (it is created, parents included) or an existing empty
// directory; the caller has already refused a non-empty one. The repository is
// created without git's template directory, so a user-level template cannot
// plant a hook in a repository whose safety is its hook. It returns the root
// commit's id.
//
// The tree is built through a private temporary index, the same shape
// commitTreeFromBase uses for a write against an existing base, minus the base:
// a root commit has none.
func InitLocalSpaceRepo(ctx context.Context, dir string, files []FileWrite) (string, error) {
	if dir == "" || len(files) == 0 || len(files) > maxMutationCount {
		return "", fmt.Errorf("%w: a local space needs a directory and at least one file", ErrMutationInvalid)
	}
	seen := make(map[string]struct{}, len(files))
	for _, f := range files {
		if !isCleanRelativePath(f.Path) || strings.ContainsRune(f.Path, '\x00') || len(f.Content) > maxMutationBytes {
			return "", fmt.Errorf("%w: local space file %q has an invalid shape", ErrMutationInvalid, f.Path)
		}
		if _, dup := seen[f.Path]; dup {
			return "", fmt.Errorf("%w: %s", ErrMutationDuplicatePath, f.Path)
		}
		seen[f.Path] = struct{}{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create local space directory: %w", err)
	}
	branchRef := "refs/heads/" + LocalSpaceBaseBranch
	if _, err := gitPlumbingOutput(ctx, dir, nil, nil, maxTreeCommitMetadataBytes,
		"init", "--bare", "--quiet", "--template=", "--initial-branch="+LocalSpaceBaseBranch, "."); err != nil {
		return "", fmt.Errorf("git init the local space: %w", err)
	}
	gitDir := "--git-dir=" + dir

	tmpDir, err := os.MkdirTemp("", "a2a-local-init-")
	if err != nil {
		return "", fmt.Errorf("create private git index directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir) // reason: best-effort cleanup of an invocation-owned temporary directory.
	}()
	indexEnv := []string{"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index")}
	for _, f := range files {
		blob, hashErr := gitPlumbingOutput(ctx, dir, indexEnv, f.Content, maxTreeCommitMetadataBytes,
			gitDir, "hash-object", "-w", "--stdin")
		if hashErr != nil {
			return "", fmt.Errorf("hash local space file %s: %w", f.Path, hashErr)
		}
		entry := "100644 " + blob + "\t" + f.Path + "\x00"
		if _, stageErr := gitPlumbingOutput(ctx, dir, indexEnv, []byte(entry), maxTreeCommitMetadataBytes,
			gitDir, "update-index", "--add", "-z", "--index-info"); stageErr != nil {
			return "", fmt.Errorf("stage local space file %s: %w", f.Path, stageErr)
		}
	}
	tree, err := gitPlumbingOutput(ctx, dir, indexEnv, nil, maxTreeCommitMetadataBytes, gitDir, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write the local space tree: %w", err)
	}
	authorEnv := append(append([]string(nil), indexEnv...),
		"GIT_AUTHOR_NAME="+localInitAuthorName, "GIT_AUTHOR_EMAIL="+localInitAuthorMail,
		"GIT_COMMITTER_NAME="+localInitAuthorName, "GIT_COMMITTER_EMAIL="+localInitAuthorMail,
	)
	commit, err := gitPlumbingOutput(ctx, dir, authorEnv, []byte(localInitMessage), maxTreeCommitMetadataBytes,
		gitDir, "-c", "commit.gpgsign=false", "commit-tree", tree, "-F", "-")
	if err != nil {
		return "", fmt.Errorf("create the local space root commit: %w", err)
	}
	// The all-zero old value makes the ref creation a compare-and-swap against
	// "does not exist yet".
	if _, err := gitPlumbingOutput(ctx, dir, nil, nil, maxTreeCommitMetadataBytes,
		gitDir, "update-ref", branchRef, commit, strings.Repeat("0", len(commit))); err != nil {
		return "", fmt.Errorf("publish the local space root commit: %w", err)
	}
	return commit, nil
}

// LocalSpaceRepoState is what ReadLocalSpaceRepo reports about a directory that
// may be a local space (or the half-created one a crash leaves behind).
type LocalSpaceRepoState struct {
	// IsBareRepo: the directory is a bare git repository.
	IsBareRepo bool
	// SpaceID is the `space:` value declared by space.yaml on the base branch;
	// empty when the branch or the file is absent or unparseable.
	SpaceID string
	// RootOnly: the base branch is exactly one commit deep (the root commit
	// InitLocalSpaceRepo wrote, nothing landed since).
	RootOnly bool
	// Commits is the number of commits on the base branch; 0 when it is absent.
	Commits int
	// Manifest is space.yaml on the base branch, valid when SpaceID is set.
	Manifest Manifest
}

// ReadLocalSpaceRepo reads, without writing, the facts `space init --local`
// needs to tell its own earlier run from a foreign directory. Every question
// that git cannot answer (not a repository, no base branch, no space.yaml)
// reads as the zero value, so the result is a pure function of what is on disk
// and the error is reserved for a malformed call. It addresses the repository
// with --git-dir, so a directory inside some other repository is never
// mistaken for it.
func ReadLocalSpaceRepo(ctx context.Context, dir string) (LocalSpaceRepoState, error) {
	if dir == "" {
		return LocalSpaceRepoState{}, fmt.Errorf("%w: a directory is required", ErrMutationInvalid)
	}
	gitDir := "--git-dir=" + dir
	branchRef := "refs/heads/" + LocalSpaceBaseBranch
	var state LocalSpaceRepoState
	if bare, ok := localGitAnswer(ctx, dir, gitDir, "rev-parse", "--is-bare-repository"); !ok || bare != "true" {
		return state, nil
	}
	state.IsBareRepo = true
	if _, ok := localGitAnswer(ctx, dir, gitDir, "rev-parse", "--verify", "--quiet", "--end-of-options", branchRef+"^{commit}"); !ok {
		return state, nil
	}
	if count, ok := localGitAnswer(ctx, dir, gitDir, "rev-list", "--count", "--end-of-options", branchRef); ok {
		if n, convErr := strconv.Atoi(count); convErr == nil {
			state.Commits = n
			state.RootOnly = n == 1
		}
	}
	if raw, ok := localGitAnswer(ctx, dir, gitDir, "show", branchRef+":space.yaml"); ok {
		if manifest, parseErr := ParseManifest([]byte(raw)); parseErr == nil {
			state.SpaceID = manifest.Space
			state.Manifest = manifest
		}
	}
	return state, nil
}

// localGitAnswer runs one read-only git question and reports whether git
// answered it; the reason it did not (not a repository, unknown ref) is
// exactly the "no" the caller wants.
func localGitAnswer(ctx context.Context, dir string, args ...string) (string, bool) {
	out, err := gitPlumbingBytes(ctx, dir, nil, nil, localInitReadLimit, args...)
	return string(bytes.TrimSpace(out)), err == nil
}
