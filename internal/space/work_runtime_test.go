package space

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

type acceptingWorkManifestValidator struct{ calls int }

func (v *acceptingWorkManifestValidator) ValidateManifest(context.Context, []byte) error {
	v.calls++
	return nil
}

func TestWorkRuntimeIsLazyAndResolvesAuthoritativeFacts(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirror := filepath.Join(t.TempDir(), "mirror")
	manifest := &acceptingWorkManifestValidator{}
	credentialCalls := 0
	runtime, err := NewWorkRuntime(
		"sha256:project", "fixture-space", "axon", mirror, fx.RemoteURL(),
		host.Repo{Owner: "acme", Name: "fixture-space"},
		func(context.Context) (host.Credential, error) {
			credentialCalls++
			return host.Credential{Token: "secret"}, nil
		},
		manifest,
	)
	if err != nil {
		t.Fatalf("NewWorkRuntime: %v", err)
	}
	if credentialCalls != 0 || manifest.calls != 0 {
		t.Fatalf("constructor performed I/O: credential=%d manifest=%d", credentialCalls, manifest.calls)
	}

	floor, err := runtime.ResolveWorkManifestFloor(context.Background())
	if err != nil {
		t.Fatalf("ResolveWorkManifestFloor: %v", err)
	}
	if floor.ProjectID != "sha256:project" || floor.Space != "fixture-space" || floor.MinimumBinaryVersion != "0.0.0" {
		t.Fatalf("floor = %+v", floor)
	}
	// The credential is memoised: at most one resolver invocation for the entire
	// runtime lifetime. For a private space the mirror refresh may trigger that
	// first invocation here; for a public space it is deferred until
	// SubmissionRuntime. Either way, the count must not exceed 1.
	if credentialCalls > 1 {
		t.Fatalf("floor lookup resolved credential %d times: want at most 1 (resolve once, cache)", credentialCalls)
	}
	afterFloor := credentialCalls

	preparation, err := runtime.ResolveWorkPreparation(context.Background())
	if err != nil {
		t.Fatalf("ResolveWorkPreparation: %v", err)
	}
	if preparation.ReporterMembership != fold.MembershipMember || preparation.BaseCommitSHA == "" || preparation.Space != "fixture-space" {
		t.Fatalf("preparation = %+v", preparation)
	}
	// A second refresh must NOT invoke the resolver again — the memoised value is reused.
	if credentialCalls != afterFloor {
		t.Fatalf("preparation resolved credential again (%d→%d): want memoised (at most one total invocation)", afterFloor, credentialCalls)
	}

	submission, err := runtime.SubmissionRuntime(context.Background())
	if err != nil {
		t.Fatalf("SubmissionRuntime: %v", err)
	}
	// After SubmissionRuntime the resolver must have been called exactly once total,
	// and the memoised credential must have reached the returned SubmissionRuntime.
	if credentialCalls != 1 || submission.Credential.Token != "secret" || submission.CurrentSpaceFloor != "0.0.0" {
		t.Fatalf("submission=%+v credential calls=%d (want 1 total invocation)", submission, credentialCalls)
	}
}

func TestWorkRuntimeFailsClosedOnScopeAndCredential(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	wantCredential := errors.New("credential unavailable")
	runtime, err := NewWorkRuntime(
		"sha256:project", "other-space", "axon", filepath.Join(t.TempDir(), "mirror"), fx.RemoteURL(),
		host.Repo{Owner: "acme", Name: "fixture-space"},
		func(context.Context) (host.Credential, error) { return host.Credential{}, wantCredential },
		&acceptingWorkManifestValidator{},
	)
	if err != nil {
		t.Fatalf("NewWorkRuntime: %v", err)
	}
	if _, err := runtime.ResolveWorkManifestFloor(context.Background()); !errors.Is(err, ErrWorkAuthoringScopeMismatch) {
		t.Fatalf("scope error = %v, want ErrWorkAuthoringScopeMismatch", err)
	}

	runtime.spaceID = "fixture-space"
	if _, err := runtime.SubmissionRuntime(context.Background()); !errors.Is(err, wantCredential) {
		t.Fatalf("credential error = %v, want %v", err, wantCredential)
	}
}

func TestNewWorkRuntimeRefusesIncompleteDependencies(t *testing.T) {
	t.Parallel()
	if runtime, err := NewWorkRuntime("", "", "", "", "", host.Repo{}, nil, nil); err == nil || runtime != nil {
		t.Fatalf("NewWorkRuntime = %+v, %v; want refusal", runtime, err)
	}
}

// workRuntimeRenameOriginBranch renames a spacefixture origin's default
// branch after construction — spacefixture.New only ever seeds "main"
// (spacefixture.go's own doc), so a test that needs a non-"main" space
// reuses the fixture's real, schema-shaped space.yaml (spacefixture.go's
// own comment records what it cost to get that shape right) instead of
// hand-rolling a second copy of it, and renames the ref directly: git
// plumbing, no working tree needed since the origin is bare.
func workRuntimeRenameOriginBranch(t *testing.T, origin, from, to string) {
	t.Helper()
	ctx := context.Background()
	sha, err := runGitOutput(ctx, origin, nil, "rev-parse", "refs/heads/"+from)
	if err != nil {
		t.Fatalf("resolve refs/heads/%s: %v", from, err)
	}
	if err := runGit(ctx, origin, "update-ref", "refs/heads/"+to, sha); err != nil {
		t.Fatalf("create refs/heads/%s: %v", to, err)
	}
	if err := runGit(ctx, origin, "symbolic-ref", "HEAD", "refs/heads/"+to); err != nil {
		t.Fatalf("move HEAD to refs/heads/%s: %v", to, err)
	}
	if err := runGit(ctx, origin, "update-ref", "-d", "refs/heads/"+from); err != nil {
		t.Fatalf("delete refs/heads/%s: %v", from, err)
	}
}

// TestWorkRuntimeResolvesNonMainDefaultBranch is no-silent-yes-2026-08
// P2b's S-2 acceptance: workDefaultBaseBranch used to hardcode "main" for
// EVERY connected space, so durable work on a "master"-default space
// resolved its preparation/base commit against a branch the space never
// published. WorkRuntime must derive the branch from the mirror's own
// remote HEAD (space.ResolveBaseBranch) instead.
func TestWorkRuntimeResolvesNonMainDefaultBranch(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	workRuntimeRenameOriginBranch(t, fx.OriginDir, "main", "master")

	mirror := filepath.Join(t.TempDir(), "mirror")
	runtime, err := NewWorkRuntime(
		"sha256:project", "fixture-space", "axon", mirror, fx.RemoteURL(),
		host.Repo{Owner: "acme", Name: "fixture-space"},
		func(context.Context) (host.Credential, error) { return host.Credential{}, nil },
		&acceptingWorkManifestValidator{},
	)
	if err != nil {
		t.Fatalf("NewWorkRuntime: %v", err)
	}

	preparation, err := runtime.ResolveWorkPreparation(context.Background())
	if err != nil {
		t.Fatalf("ResolveWorkPreparation: %v", err)
	}
	if preparation.BaseBranch != "master" {
		t.Fatalf("BaseBranch = %q, want %q (the space's own resolved default, never a hardcoded \"main\")", preparation.BaseBranch, "master")
	}
	if preparation.BaseCommitSHA == "" {
		t.Fatal("BaseCommitSHA is empty — resolved against the wrong (nonexistent) branch entirely")
	}
}
