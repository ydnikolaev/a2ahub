package livee2e

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Permission is a credential's own access level on a repository, as the host
// reports it — never as the token's declared scopes imply it. Two reasons the
// distinction is load-bearing: a fine-grained PAT does not advertise scopes at
// all (internal/host.TokenScopes reports reported=false for them), and
// declared capability is not effective access in any case.
type Permission string

const (
	// PermissionAdmin is the level that makes a participant credential
	// unusable for boundary assertions (AC-960.4) and a provisioner
	// credential mandatory.
	PermissionAdmin Permission = "admin"
	// PermissionWrite is what the runbook provisions for participant B —
	// enough to author PRs, not enough to bypass the gate.
	PermissionWrite Permission = "write"
	// PermissionRead is read-only access.
	PermissionRead Permission = "read"
	// PermissionNone means the credential has no access to the repo at all.
	PermissionNone Permission = "none"
)

var (
	// ErrRepoOutsideOrg is returned for any repo not owned by Config.Org
	// (AC-960.3). The guard exists so a mis-set env can never point the
	// harness — which holds admin — at a space that matters.
	ErrRepoOutsideOrg = errors.New("livee2e: repo is outside the dedicated test org")

	// ErrParticipantIsAdmin is THE anti-vacuity guard (AC-960.4, spec 36
	// §T5). Production runs `enforce_admins: false`, so branch protection
	// does not bind admins; a boundary assertion authored by an admin passes
	// without testing anything. Refusing to run is the only honest outcome —
	// a report is worth exactly what its weakest identity assumption is.
	ErrParticipantIsAdmin = errors.New("livee2e: participant credential holds admin on the space")

	// ErrProvisionerNotAdmin is returned when the provisioner cannot in fact
	// administer the space. Refused up front rather than discovered halfway
	// through provisioning, where a partial reset is the worst outcome.
	ErrProvisionerNotAdmin = errors.New("livee2e: provisioner credential lacks admin on the space")

	// ErrSameLogin is returned when both credentials authenticate as one
	// GitHub account. diff-authz compares changed paths against the PR
	// AUTHOR's section, so with one login the cross-section scenario is not
	// merely weak — it cannot be constructed (spec 36 §T6-c).
	ErrSameLogin = errors.New("livee2e: both credentials authenticate as the same login")
)

// Prober is the read-only host seam the guards need: who a credential is, and
// what it may do on a repo. Kept minimal and separate from internal/host's
// 5-primitive interface — this is harness introspection, not a product
// capability, and widening the product's interface to serve its own test tier
// would be the wrong direction of dependency.
type Prober interface {
	// Login returns the account a credential authenticates as.
	Login(ctx context.Context, token string) (string, error)
	// RepoPermission returns the credential's own permission on owner/name.
	RepoPermission(ctx context.Context, owner, name, token string) (Permission, error)
}

// Preflight is what the guards established, carried into the run so the
// report can state the identities its assertions actually rest on. A report
// that does not name them cannot be audited later.
type Preflight struct {
	ProvisionerLogin string
	ParticipantLogin string
}

// CheckRepo refuses any repo outside the dedicated org (AC-960.3). Owner
// comparison is case-insensitive because GitHub logins are.
func (c Config) CheckRepo(owner, name string) error {
	if owner == "" || name == "" {
		return fmt.Errorf("%w: repo %q/%q is not fully qualified", ErrRepoOutsideOrg, owner, name)
	}
	if !strings.EqualFold(owner, c.Org) {
		return fmt.Errorf("%w: %s/%s is not under %s", ErrRepoOutsideOrg, owner, name, c.Org)
	}
	return nil
}

// RunPreflight establishes the identity model on a real host before any
// scenario runs (spec 36 §T5). Every failure is a refusal to run, never a
// warning: each one of these conditions turns the run's boundary assertions
// into passes that prove nothing, and a green report nobody can trust is
// worse than no report.
//
// Order is deliberate — cheapest and most decisive first, so an operator
// misconfiguration is named precisely rather than surfacing as a confusing
// permission error three calls later.
func RunPreflight(ctx context.Context, p Prober, cfg Config, owner, name string) (Preflight, error) {
	if p == nil {
		return Preflight{}, fmt.Errorf("%w: no host prober supplied", ErrNotConfigured)
	}
	if err := cfg.CheckRepo(owner, name); err != nil {
		return Preflight{}, err
	}

	provisionerLogin, err := p.Login(ctx, cfg.ProvisionerToken)
	if err != nil {
		return Preflight{}, fmt.Errorf("livee2e: resolve provisioner login: %w", err)
	}
	participantLogin, err := p.Login(ctx, cfg.ParticipantToken)
	if err != nil {
		return Preflight{}, fmt.Errorf("livee2e: resolve participant login: %w", err)
	}
	if provisionerLogin == "" || participantLogin == "" {
		return Preflight{}, fmt.Errorf("%w: host reported an empty login", ErrNotConfigured)
	}
	if strings.EqualFold(provisionerLogin, participantLogin) {
		return Preflight{}, fmt.Errorf("%w: both are %q — spec 36 §9.5.2 requires a separate machine account for participant B",
			ErrSameLogin, provisionerLogin)
	}

	participantPerm, err := p.RepoPermission(ctx, owner, name, cfg.ParticipantToken)
	if err != nil {
		return Preflight{}, fmt.Errorf("livee2e: read participant permission: %w", err)
	}
	if participantPerm == PermissionAdmin {
		return Preflight{}, fmt.Errorf("%w: %q is admin on %s/%s, so branch protection would not bind it and every boundary assertion would pass without testing anything (spec 36 §T5)",
			ErrParticipantIsAdmin, participantLogin, owner, name)
	}

	provisionerPerm, err := p.RepoPermission(ctx, owner, name, cfg.ProvisionerToken)
	if err != nil {
		return Preflight{}, fmt.Errorf("livee2e: read provisioner permission: %w", err)
	}
	if provisionerPerm != PermissionAdmin {
		return Preflight{}, fmt.Errorf("%w: %q has %q on %s/%s", ErrProvisionerNotAdmin, provisionerLogin, provisionerPerm, owner, name)
	}

	return Preflight{ProvisionerLogin: provisionerLogin, ParticipantLogin: participantLogin}, nil
}
