package space

import (
	"context"
	"fmt"
	"os"
	gopath "path"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// WriteState is the funnel's own claim about a Submit outcome — the
// contract P7's cache persistence (out of this phase's footprint) is
// built against. Kept stable across phases per spec 05 §9.
type WriteState string

const (
	// WriteStatePendingMerge is returned after a fresh push+PR-open: the
	// PR is open and auto-merge is armed, but not yet merged.
	WriteStatePendingMerge WriteState = "pending-merge"
	// WriteStateAlreadyOpen is returned by the idempotent short-circuit
	// (step 0) when a re-run finds an already-open PR for the
	// deterministic branch.
	WriteStateAlreadyOpen WriteState = "already-open"
	// WriteStateAlreadyMerged is returned by the idempotent short-circuit
	// when the deterministic branch's PR already merged.
	WriteStateAlreadyMerged WriteState = "already-merged"
)

// FileWrite is one file the write funnel commits — a path (relative to
// the mirror clone's working directory) and its full content.
type FileWrite struct {
	Path    string
	Content []byte
}

// SubmitRequest is one write-funnel invocation (§4.2 D-002, §7): commit
// Files as ONE commit (an artifact file + its first lifecycle event, per
// D-026) on the deterministic branch a2a/<System>/<ArtifactID>, push, open
// a PR with auto-merge enabled.
type SubmitRequest struct {
	// RepoDir is the local mirror clone's working directory (already
	// cloned/fetched via CloneOrFetch) that the commit is made in.
	RepoDir string
	// System is the authoring system (branch name + section guard).
	System string
	// ArtifactID is the artifact's §3.3 id (branch name suffix).
	ArtifactID string
	// Files are committed together, exactly once. Every Path must be
	// under System's own section (or decisions/, the funnel-level
	// exception) — checked BEFORE any git action.
	Files []FileWrite
	// CommitMessage, CommitAuthorName/Email: P6 supplies these (the exact
	// "a2a(<type>): <id>" convention, OP-205, is a CLI-layer concern).
	CommitMessage     string
	CommitAuthorName  string
	CommitAuthorEmail string

	// RemoteURL is the push target (real GitHub URL, or a local fixture
	// path in tests).
	RemoteURL string
	// Repo identifies the GitHub repo for the OpenPR/FindPRByHeadBranch
	// calls (owner/name) — distinct from RemoteURL, which is what `git
	// push` uses.
	Repo host.Repo
	// BaseBranch is the PR's target branch (normatively "main", §4.2).
	BaseBranch string
	// PRTitle/PRBody are passed through to host.OpenPR verbatim.
	PRTitle string
	PRBody  string

	Credential host.Credential

	// MinBinaryVersion is space.yaml's pin for the CC-085 guard (caller
	// already parsed the manifest; the funnel does not parse YAML).
	MinBinaryVersion string
}

// WriteResult is what Submit returns: the contract P7's cache persistence
// (pending-merge marker) and P8's gated verbs are built against (spec 05
// §7, §9 — "keep that return contract stable across phases").
type WriteResult struct {
	Branch    string
	PRNumber  int
	PRURL     string
	CommitSHA string
	State     WriteState
}

// SubmitValidator is the consumer-side seam (rails ISP/DI) for V2
// validation of the artifact+event pair before it enters the write funnel
// (P3 internal/validate, wired for real at P6). internal/space depends on
// this interface only — never a concrete validate.Engine (ADR-001's
// import grant is a ceiling, not a mandate; plan 05 Placement decisions).
type SubmitValidator interface {
	// ValidateSubmit validates files about to be committed and returns a
	// non-nil error describing every violation found (or nil).
	ValidateSubmit(ctx context.Context, files []FileWrite) error
}

// WriteFunnel implements the D-002/D-026 single write funnel: the ONLY
// code path internal/space exposes for mutating a space (rails: "one
// write shape"). It is the sole caller of internal/host.
type WriteFunnel struct {
	host      host.Host
	validator SubmitValidator
	// binaryVersion is injected via constructor DI (plan 05 Placement
	// decision: "the version stamp lives in cmd/a2a; space never reads
	// build info itself") — used only for the CC-085 guard.
	binaryVersion string
}

// NewWriteFunnel constructs a WriteFunnel. h and validator are required
// (a nil dependency used at runtime is a constructor bug, rails
// anti-pattern #10) — callers wire fakes in tests, the real engines at
// cmd/a2a (P6).
func NewWriteFunnel(h host.Host, validator SubmitValidator, binaryVersion string) *WriteFunnel {
	return &WriteFunnel{host: h, validator: validator, binaryVersion: binaryVersion}
}

// Submit runs the write funnel end to end (spec 05 §7):
//
//	(0) FindPRByHeadBranch short-circuit for a2a/<system>/<id> — an
//	    existing open/merged PR returns immediately, no second
//	    push/open cycle (AC-301.1 idempotency).
//	(1) section guard (wrong-section files refused before any git
//	    action) + the min_binary_version guard (CC-085) + the
//	    SubmitValidator seam (V2).
//	(2) ONE commit = every req.Files entry.
//	(3) host.PushBranch to a2a/<system>/<id>.
//	(4) host.OpenPR with auto-merge enabled (uniform, D-002).
//	(5) return the write-result.
func (f *WriteFunnel) Submit(ctx context.Context, req SubmitRequest) (WriteResult, error) {
	const op = "Submit"
	branch := fmt.Sprintf("a2a/%s/%s", req.System, req.ArtifactID)

	// Step 0: idempotent-retry short-circuit — before ANY other check or
	// git action (spec 05 §7 idempotency note).
	existing, err := f.host.FindPRByHeadBranch(ctx, host.FindPRRequest{
		Repo: req.Repo, Branch: branch, Credential: req.Credential,
	})
	if err != nil {
		return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
	}
	if existing != nil {
		state := WriteStateAlreadyOpen
		if existing.State == "merged" {
			state = WriteStateAlreadyMerged
		}
		return WriteResult{Branch: branch, PRNumber: existing.Number, PRURL: existing.URL, State: state}, nil
	}

	// Step 1a: section guard — wrong-section files refused before any
	// git action (shared refusal path, AC-201.3 precondition).
	for _, file := range req.Files {
		if !sectionOK(req.System, file.Path) {
			return WriteResult{}, &Error{Op: op, Input: file.Path, Err: ErrWrongSection}
		}
	}

	// Step 1b: CC-085 min_binary_version guard — refuse write, stay
	// read-only. Fails CLOSED on an unparseable version (versionOlderThan
	// itself already fails closed).
	if req.MinBinaryVersion != "" {
		older, err := versionOlderThan(f.binaryVersion, req.MinBinaryVersion)
		if err != nil {
			return WriteResult{}, &Error{Op: op, Err: err}
		}
		if older {
			return WriteResult{}, &Error{
				Op: op,
				Input: fmt.Sprintf("local binary %s < space.yaml min_binary_version %s",
					f.binaryVersion, req.MinBinaryVersion),
				Err: ErrStaleBinaryVersion,
			}
		}
	}

	// Step 1c: V2 validation via the submit-validator seam.
	if f.validator != nil {
		if err := f.validator.ValidateSubmit(ctx, req.Files); err != nil {
			return WriteResult{}, &Error{Op: op, Err: err}
		}
	}

	// Step 2: assemble ONE commit = every req.Files entry (D-026).
	sha, err := f.commitOne(ctx, req, branch)
	if err != nil {
		return WriteResult{}, &Error{Op: op, Err: err}
	}

	// Step 3: push the ephemeral branch.
	if _, err := f.host.PushBranch(ctx, host.PushBranchRequest{
		RepoDir: req.RepoDir, LocalRef: branch, Branch: branch,
		RemoteURL: req.RemoteURL, Credential: req.Credential,
	}); err != nil {
		return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
	}

	// Step 4: open the PR — UNIFORM, auto-merge always (D-002; spec 05
	// §T1 "Gating needs no OpenPR parameter").
	pr, err := f.host.OpenPR(ctx, host.OpenPRRequest{
		Repo: req.Repo, Head: branch, Base: req.BaseBranch,
		Title: req.PRTitle, Body: req.PRBody, Credential: req.Credential,
	})
	if err != nil {
		return WriteResult{}, &Error{Op: op, Input: branch, Err: err}
	}

	// Step 5: return the write-result (cache persistence is P7's, not
	// this phase's — spec 05 §7).
	return WriteResult{
		Branch: branch, PRNumber: pr.Number, PRURL: pr.URL,
		CommitSHA: sha, State: WriteStatePendingMerge,
	}, nil
}

// sectionOK reports whether path is inside system's own section, or under
// the space-level decisions/ exception (the one path the single-writer
// rule does not enforce per-system, §4.2 decision flow).
//
// The path must be a clean, relative, forward-slash space path: any
// absolute path, any `..` segment, or any non-canonical form (e.g.
// `axon/../other/evil.md`) is rejected outright — otherwise a crafted
// FileWrite.Path could collapse into a sibling system's section, or
// outside the repo entirely, while still passing the guard whose whole
// job is to enforce the single-writer boundary (D-014 data-stays-data,
// the "one write shape" rail).
func sectionOK(system, path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	// A path is safe only if it is already in cleaned, non-escaping form.
	// path.Clean collapses `..`/`.`/double-slashes; if the input differs
	// from its cleaned form, or the cleaned form still escapes, reject.
	if cleaned := gopath.Clean(path); cleaned != path || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return false
	}
	if path == "decisions" || hasPathPrefix(path, "decisions/") {
		return true
	}
	return path == system || hasPathPrefix(path, system+"/")
}

func hasPathPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix
}

// commitOne checks out branch (creating it from the current HEAD), writes
// every req.Files entry to disk under req.RepoDir, stages, and commits
// them as ONE commit — the D-026 shape. Returns the new commit SHA.
func (f *WriteFunnel) commitOne(ctx context.Context, req SubmitRequest, branch string) (string, error) {
	if len(req.Files) == 0 {
		return "", fmt.Errorf("space: commitOne: no files to commit")
	}

	if err := runGit(ctx, req.RepoDir, "checkout", "-B", branch); err != nil {
		return "", err
	}

	paths := make([]string, 0, len(req.Files))
	for _, file := range req.Files {
		full := filepath.Join(req.RepoDir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, file.Content, 0o644); err != nil {
			return "", err
		}
		paths = append(paths, file.Path)
	}

	addArgs := append([]string{"add"}, paths...)
	if err := runGit(ctx, req.RepoDir, addArgs...); err != nil {
		return "", err
	}

	authorName := req.CommitAuthorName
	if authorName == "" {
		authorName = "a2a"
	}
	authorEmail := req.CommitAuthorEmail
	if authorEmail == "" {
		authorEmail = "a2a@a2ahub.invalid"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + authorName, "GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + authorName, "GIT_COMMITTER_EMAIL=" + authorEmail,
	}
	msg := req.CommitMessage
	if msg == "" {
		msg = "a2a: submit " + req.ArtifactID
	}
	if _, err := runGitOutput(ctx, req.RepoDir, env, "commit", "-m", msg); err != nil {
		return "", err
	}

	sha, err := runGitOutput(ctx, req.RepoDir, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return sha, nil
}
