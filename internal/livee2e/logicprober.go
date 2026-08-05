package livee2e

import (
	"context"
	"errors"
	"fmt"
)

// ErrLogicProberUnknownToken is returned when a token this prober was not
// built with is presented to it. The local tier has no real identity
// service to ask, so a token outside the two it was constructed with is
// unconditionally unknown rather than answered with a guess.
var ErrLogicProberUnknownToken = errors.New("livee2e: logic prober: unknown token")

// LogicProber answers RunPreflight's two identity questions (preflight.go)
// for the local tier, where there is no real host to ask. It is a Prober
// (preflight.go) backed by a static token->identity map instead of a
// network call — harness introspection standing in for a provider fact the
// local tier has no provider to supply, the same role testkit/fakegithub
// plays for the git/PR surface (plan D-6).
//
// Every field is exported (rather than hidden behind NewLogicProber alone)
// so a test can build a DELIBERATELY mis-shaped prober directly — the same
// login for both tokens, or the participant at PermissionAdmin — to prove
// RunPreflight's anti-vacuity guards still refuse it even when the
// identities come from a static map instead of a network call.
type LogicProber struct {
	ProvisionerToken string
	ProvisionerLogin string
	ParticipantToken string
	ParticipantLogin string

	// ProvisionerPermission/ParticipantPermission are what RepoPermission
	// reports for each token. NewLogicProber sets the shape RunPreflight
	// requires (provisioner admin, participant write, spec 36 §T5); a test
	// overrides ParticipantPermission to PermissionAdmin to prove the
	// anti-vacuity guard is not dodged.
	ProvisionerPermission Permission
	ParticipantPermission Permission
}

// NewLogicProber builds a LogicProber with the shape RunPreflight requires
// from a real host (spec 36 §T5): two distinct logins, the provisioner at
// PermissionAdmin, the participant at PermissionWrite — the same level the
// runbook provisions for participant B on the live tier (preflight.go's own
// PermissionWrite doc).
func NewLogicProber(provisionerToken, provisionerLogin, participantToken, participantLogin string) *LogicProber {
	return &LogicProber{
		ProvisionerToken: provisionerToken, ProvisionerLogin: provisionerLogin,
		ParticipantToken: participantToken, ParticipantLogin: participantLogin,
		ProvisionerPermission: PermissionAdmin,
		ParticipantPermission: PermissionWrite,
	}
}

// Login implements Prober.
func (p *LogicProber) Login(_ context.Context, token string) (string, error) {
	switch token {
	case p.ProvisionerToken:
		return p.ProvisionerLogin, nil
	case p.ParticipantToken:
		return p.ParticipantLogin, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrLogicProberUnknownToken, token)
	}
}

// RepoPermission implements Prober. owner/name are accepted but unused: the
// local tier stands up exactly one space, so there is nothing to
// discriminate on — mirrors GitHubProber's own Prober-shaped signature
// (prober_github.go), which this local sibling has no need to widen.
func (p *LogicProber) RepoPermission(_ context.Context, _, _, token string) (Permission, error) {
	switch token {
	case p.ProvisionerToken:
		return p.ProvisionerPermission, nil
	case p.ParticipantToken:
		return p.ParticipantPermission, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrLogicProberUnknownToken, token)
	}
}

var _ Prober = (*LogicProber)(nil)
