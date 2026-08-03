package space

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
)

const workDefaultBaseBranch = "main"

// WorkCredentialResolver resolves an invocation-local credential. Keeping it
// behind this consumer-side seam lets heartbeat and status remain completely
// local: neither operation calls WorkRuntime.SubmissionRuntime.
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
	baseBranch string
	credential WorkCredentialResolver
	manifest   ManifestValidator
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
		baseBranch: workDefaultBaseBranch, credential: credential, manifest: manifest,
	}, nil
}

// ResolveWorkManifestFloor implements WorkManifestFloorResolver.
func (r *WorkRuntime) ResolveWorkManifestFloor(ctx context.Context) (WorkManifestFloor, error) {
	manifest, _, err := r.refresh(ctx)
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
	manifest, baseCommit, err := r.refresh(ctx)
	if err != nil {
		return WorkPreparationRuntime{}, err
	}
	membership := fold.MembershipUnknown
	for _, participant := range manifest.Participants {
		if participant.System != r.ownSystem {
			continue
		}
		switch participant.Status {
		case "active":
			membership = fold.MembershipMember
		case "left":
			membership = fold.MembershipLeft
		}
		break
	}
	return WorkPreparationRuntime{
		ProjectID: r.projectID, Space: r.spaceID,
		MinimumBinaryVersion: manifest.MinBinaryVersion,
		Repository:           r.repository, RemoteURL: r.remoteURL,
		BaseBranch: r.baseBranch, BaseCommitSHA: baseCommit,
		ReporterMembership: membership,
		CommitAuthorName:   r.ownSystem, CommitAuthorEmail: r.ownSystem + "@a2a.local",
	}, nil
}

// SubmissionRuntime implements WorkSubmissionRuntimeProvider. Credential
// resolution is deliberately last, after the current target/floor is known.
func (r *WorkRuntime) SubmissionRuntime(ctx context.Context) (SubmissionRuntime, error) {
	manifest, _, err := r.refresh(ctx)
	if err != nil {
		return SubmissionRuntime{}, err
	}
	credential, err := r.credential(ctx)
	if err != nil {
		return SubmissionRuntime{}, fmt.Errorf("space: resolve work credential: %w", err)
	}
	return SubmissionRuntime{
		RepoDir: r.mirrorDir, RemoteURL: r.remoteURL, Credential: credential,
		TargetIdentity: repositoryIdentity(r.repository), CurrentSpaceFloor: manifest.MinBinaryVersion,
	}, nil
}

func (r *WorkRuntime) refresh(ctx context.Context) (Manifest, string, error) {
	if err := CloneOrFetch(ctx, r.mirrorDir, r.remoteURL); err != nil {
		return Manifest{}, "", fmt.Errorf("space: refresh work mirror: %w", err)
	}
	raw, err := readBounded(filepath.Join(r.mirrorDir, "space.yaml"))
	if err != nil {
		return Manifest{}, "", fmt.Errorf("space: read work manifest: %w", err)
	}
	manifest, err := LoadManifest(ctx, raw, r.manifest)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("space: validate work manifest: %w", err)
	}
	if manifest.Space != r.spaceID {
		return Manifest{}, "", fmt.Errorf("%w: configured %q, manifest %q", ErrWorkAuthoringScopeMismatch, r.spaceID, manifest.Space)
	}
	baseCommit, err := contractGitResolveCommit(ctx, r.mirrorDir, "origin/"+r.baseBranch)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("space: resolve work base commit: %w", err)
	}
	baseCommit = strings.ToLower(strings.TrimSpace(baseCommit))
	if !validRemoteRecoveryOID(baseCommit) {
		return Manifest{}, "", ErrWorkPreparationBaseInvalid
	}
	return manifest, baseCommit, nil
}

var _ WorkManifestFloorResolver = (*WorkRuntime)(nil)
var _ WorkPreparationRuntimeProvider = (*WorkRuntime)(nil)
var _ WorkSubmissionRuntimeProvider = (*WorkRuntime)(nil)
