package space

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
)

// WorkCredentialResolver resolves an invocation-local credential. It is called
// at most once: the result is memoised after the first call (whether that
// happens during mirror refresh or inside SubmissionRuntime) so that
// heartbeat and status operations never pay more than one credential round-trip
// across the lifetime of the runtime, and SubmissionRuntime re-uses the cached
// value rather than re-resolving.
type WorkCredentialResolver func(context.Context) (host.Credential, error)

// WorkRuntime lazily resolves the authoritative space facts used by durable
// work reporting. Construction performs no Git, network, host, or credential
// operation; semantic commands cross that boundary only through one of the
// three resolver methods below.
type WorkRuntime struct {
	projectID  string
	spaceID    string
	ownSystem  string
	mirrorDir  string
	remoteURL  string
	repository host.Repo
	credential WorkCredentialResolver
	manifest   ManifestValidator

	// credMu serialises the single resolver invocation.
	credMu        sync.Mutex
	credResolved  bool
	cachedCred    host.Credential
	cachedCredErr error
}

// NewWorkRuntime binds one configured space without contacting it.
func NewWorkRuntime(
	projectID string,
	spaceID string,
	ownSystem string,
	mirrorDir string,
	remoteURL string,
	repository host.Repo,
	credential WorkCredentialResolver,
	manifest ManifestValidator,
) (*WorkRuntime, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(spaceID) == "" || strings.TrimSpace(ownSystem) == "" ||
		strings.TrimSpace(mirrorDir) == "" || strings.TrimSpace(remoteURL) == "" ||
		strings.TrimSpace(repository.Owner) == "" || strings.TrimSpace(repository.Name) == "" ||
		credential == nil || manifest == nil {
		return nil, fmt.Errorf("space: work runtime requires project, space, system, mirror, remote, repository, credential and manifest validator")
	}
	return &WorkRuntime{
		projectID: projectID, spaceID: spaceID, ownSystem: ownSystem,
		mirrorDir: mirrorDir, remoteURL: remoteURL, repository: repository,
		credential: credential, manifest: manifest,
	}, nil
}

// ResolveWorkManifestFloor implements WorkManifestFloorResolver.
func (r *WorkRuntime) ResolveWorkManifestFloor(ctx context.Context) (WorkManifestFloor, error) {
	manifest, _, _, err := r.refresh(ctx)
	if err != nil {
		return WorkManifestFloor{}, err
	}
	return WorkManifestFloor{
		ProjectID: r.projectID, Space: r.spaceID,
		MinimumBinaryVersion: manifest.MinBinaryVersion,
	}, nil
}

// ResolveWorkPreparation implements WorkPreparationRuntimeProvider.
func (r *WorkRuntime) ResolveWorkPreparation(ctx context.Context) (WorkPreparationRuntime, error) {
	manifest, baseCommit, baseBranch, err := r.refresh(ctx)
	if err != nil {
		return WorkPreparationRuntime{}, err
	}
	membership := fold.MembershipUnknown
	for _, participant := range manifest.Participants {
		if participant.System != r.ownSystem {
			continue
		}
		membership = participant.Status
		break
	}
	return WorkPreparationRuntime{
		ProjectID: r.projectID, Space: r.spaceID,
		MinimumBinaryVersion: manifest.MinBinaryVersion,
		Repository:           r.repository, RemoteURL: r.remoteURL,
		BaseBranch: baseBranch, BaseCommitSHA: baseCommit,
		ReporterMembership: membership,
		CommitAuthorName:   r.ownSystem, CommitAuthorEmail: r.ownSystem + "@a2a.local",
	}, nil
}

// SubmissionRuntime implements WorkSubmissionRuntimeProvider. Credential
// resolution is deferred until after the current target/floor is known. The
// resolved credential is memoised: if the mirror refresh already called
// resolveOnce, SubmissionRuntime returns the cached value without a second
// invocation of the resolver.
func (r *WorkRuntime) SubmissionRuntime(ctx context.Context) (SubmissionRuntime, error) {
	manifest, _, _, err := r.refresh(ctx)
	if err != nil {
		return SubmissionRuntime{}, err
	}
	credential, err := r.resolveOnce(ctx)
	if err != nil {
		return SubmissionRuntime{}, fmt.Errorf("space: resolve work credential: %w", err)
	}
	return SubmissionRuntime{
		RepoDir: r.mirrorDir, RemoteURL: r.remoteURL, Credential: credential,
		TargetIdentity: repositoryIdentity(r.repository), CurrentSpaceFloor: manifest.MinBinaryVersion,
	}, nil
}

// resolveOnce resolves the credential exactly once across the lifetime of the
// runtime; all callers after the first receive the memoised result.
//
// The context passed by the FIRST caller wins. Credential resolution reads
// from environment variables or the machine config file — not from the
// network — so the context matters only for deadline propagation, and the
// memoised value stays valid for the runtime's lifetime.
func (r *WorkRuntime) resolveOnce(ctx context.Context) (host.Credential, error) {
	r.credMu.Lock()
	defer r.credMu.Unlock()
	if !r.credResolved {
		r.cachedCred, r.cachedCredErr = r.credential(ctx)
		r.credResolved = true
	}
	return r.cachedCred, r.cachedCredErr
}

// readCredential resolves the space credential for a READ, degrading to the
// empty credential when none resolves.
//
// The asymmetry with SubmissionRuntime — which returns the resolution error —
// is the point, and it is the same asymmetry CloneOrFetch documents. A write
// with no credential cannot succeed, so failing loudly is the only honest
// answer. A read with no credential succeeds against every public space, and
// has to keep succeeding: making the mirror refresh depend on a resolvable
// token would break tokenless read verbs and CI in exchange for nothing. What
// a missing credential costs is exactly the private case, and that failure
// arrives from git itself, with the remote's own words.
func (r *WorkRuntime) readCredential(ctx context.Context) host.Credential {
	credential, _ := r.resolveOnce(ctx)
	return credential
}

// refresh fetches the mirror, resolves its ACTUAL default branch (never a
// guessed "main" — no-silent-yes-2026-08 P2b/S-2: workDefaultBaseBranch used
// to hardcode it, which pushed a "master"-default space's durable work at a
// branch nobody named), then reads the manifest and the base commit against
// that resolved branch. The resolved branch is not cached across calls:
// nothing else this function reads is either (the manifest and base commit
// are re-fetched on every call), so re-resolving here is consistent rather
// than a new staleness risk.
func (r *WorkRuntime) refresh(ctx context.Context) (Manifest, string, string, error) {
	if err := CloneOrFetch(ctx, r.mirrorDir, r.remoteURL, r.readCredential(ctx)); err != nil {
		return Manifest{}, "", "", fmt.Errorf("space: refresh work mirror: %w", err)
	}
	baseBranch, err := ResolveBaseBranch(ctx, r.mirrorDir)
	if err != nil {
		return Manifest{}, "", "", fmt.Errorf("space: resolve work base branch: %w", err)
	}
	raw, err := readBounded(filepath.Join(r.mirrorDir, "space.yaml"))
	if err != nil {
		return Manifest{}, "", "", fmt.Errorf("space: read work manifest: %w", err)
	}
	manifest, err := LoadManifest(ctx, raw, r.manifest)
	if err != nil {
		return Manifest{}, "", "", fmt.Errorf("space: validate work manifest: %w", err)
	}
	if manifest.Space != r.spaceID {
		return Manifest{}, "", "", fmt.Errorf("%w: configured %q, manifest %q", ErrWorkAuthoringScopeMismatch, r.spaceID, manifest.Space)
	}
	baseCommit, err := contractGitResolveCommit(ctx, r.mirrorDir, "origin/"+baseBranch)
	if err != nil {
		return Manifest{}, "", "", fmt.Errorf("space: resolve work base commit: %w", err)
	}
	baseCommit = strings.ToLower(strings.TrimSpace(baseCommit))
	if !validRemoteRecoveryOID(baseCommit) {
		return Manifest{}, "", "", ErrWorkPreparationBaseInvalid
	}
	return manifest, baseCommit, baseBranch, nil
}

var _ WorkManifestFloorResolver = (*WorkRuntime)(nil)
var _ WorkPreparationRuntimeProvider = (*WorkRuntime)(nil)
var _ WorkSubmissionRuntimeProvider = (*WorkRuntime)(nil)
