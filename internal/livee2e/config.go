package livee2e

import (
	"errors"
	"fmt"
	"regexp"
)

// Environment variables the live tier reads. Deliberately dedicated names:
// reusing an ambient GITHUB_TOKEN/GH_TOKEN is how a mis-set env reaches a
// REAL space, and a dedicated name is what makes "not configured"
// unambiguous — which AC-962.2's fail-closed "not run" depends on
// (spec 36 §9.5.4).
//
// gosec's G101 fires on the three *_TOKEN names below. They are the NAMES of
// environment variables, not credentials: no token value is ever a literal in
// this package — every one arrives through the injected lookup in LoadConfig,
// and Config carries no struct tags so it cannot be marshaled by accident
// (the same secrecy discipline as host.Credential). The suppression is
// declaration-scoped rather than repo-wide, so G101 stays live everywhere a
// hardcoded secret would actually be one.
//
//nolint:gosec // G101: env-var NAMES, not credentials — rationale above.
const (
	// EnvOrg names the dedicated throwaway org. Every repo the tier touches
	// must be owned by it (AC-960.3).
	EnvOrg = "A2A_LIVE_E2E_ORG"
	// EnvProvisionerToken is the admin credential that creates, resets and
	// protects the test space, and acts as the code owner.
	EnvProvisionerToken = "A2A_LIVE_E2E_PROVISIONER_TOKEN"
	// EnvParticipantToken is the NON-admin credential that authors every
	// boundary assertion (spec 36 §T5).
	EnvParticipantToken = "A2A_LIVE_E2E_PARTICIPANT_TOKEN"

	// EnvRetiredToken is the single-token name spec 36 §9.5 used before the
	// identity model. It is read ONLY to produce a precise refusal: a
	// harness that silently accepted one token could not tell the
	// provisioner from the participant, which is the exact confusion the
	// preflight guards exist to refuse.
	EnvRetiredToken = "A2A_LIVE_E2E_TOKEN"

	// EnvFamilies optionally narrows a run to a comma-separated subset of
	// scenario families — an ITERATION aid while a family is being written,
	// never a release verdict.
	//
	// It needs no guard of its own to stay honest, and that is by
	// construction rather than by luck: a skipped family's rows keep their
	// declared VerdictNotRun, and Report.ExitCode() returns non-zero for any
	// verdict that is not a pass. So a narrowed run CANNOT exit 0, and its
	// report shows the untouched families as not-run rather than as absent.
	// The fail-closed matrix already answers the question a filter would
	// otherwise reopen.
	//
	// It exists because the full matrix costs tens of minutes of Actions
	// latency, and iterating on one family at that price is what pushes an
	// author toward not running it at all.
	EnvFamilies = "A2A_LIVE_E2E_FAMILIES"

	// EnvCandidateSHA is the immutable commit in the public product repo that
	// both the reusable-workflow `uses:` ref and its `a2a-ref` input must run.
	// A live release verdict against a branch or tag is intentionally refused.
	EnvCandidateSHA = "A2A_LIVE_E2E_CANDIDATE_SHA"
)

var candidateSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Ambient credential variables the tier refuses to be handed. Not a
// stylistic preference: these are the variables that are already exported in
// a shell that has been operating on real spaces.
var ambientCredentialVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}

var (
	// ErrNotConfigured is returned when a required variable is absent or
	// empty. The tier reports "not run" and exits non-zero on this — that
	// is the specified behaviour, not a degraded mode (AC-962.2).
	ErrNotConfigured = errors.New("livee2e: not configured")

	// ErrRetiredTokenVar is returned when the retired single-token variable
	// is set. Refused rather than accepted as a fallback: honouring it would
	// mean running the boundary scenarios with one identity, which makes
	// them pass vacuously (spec 36 §T5).
	ErrRetiredTokenVar = errors.New("livee2e: retired single-token variable is set")

	// ErrAmbientCredential is returned when a supplied token is byte-equal
	// to an ambient GITHUB_TOKEN/GH_TOKEN — the credential most likely to
	// have write access to a space that matters.
	ErrAmbientCredential = errors.New("livee2e: token is the ambient GitHub credential")

	// ErrIdenticalCredentials is returned when the provisioner and
	// participant tokens are the same secret. They would then authenticate
	// as one login, and every boundary assertion would be self-satisfied.
	ErrIdenticalCredentials = errors.New("livee2e: provisioner and participant share one credential")
)

// Config is the resolved live-tier configuration.
type Config struct {
	// Org is the dedicated throwaway org that owns every repo this tier may
	// touch.
	Org string
	// ProvisionerToken holds repo admin: create/reset/protect, `space init`,
	// and re-triggering another login's PR.
	ProvisionerToken string
	// ParticipantToken is the non-admin participant B credential. Its
	// narrowness is an assertion, not thrift: it is exactly what a real
	// participating system holds (spec 36 §9.5.3).
	ParticipantToken string
	// CandidateSHA is the immutable public product commit under test.
	CandidateSHA string
}

// LoadConfig resolves the configuration from an environment lookup —
// injected rather than read from os.Getenv directly, so the guards below are
// testable without mutating process state.
//
// Fails CLOSED: any missing variable, any refused shape, and there is no
// partially-configured mode. Callers render ErrNotConfigured as VerdictNotRun
// for every scenario and exit non-zero.
func LoadConfig(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("%w: no environment lookup supplied", ErrNotConfigured)
	}

	// Checked BEFORE the required vars, so an operator following the old
	// §9.5 gets the reason rather than a bare "not configured".
	if getenv(EnvRetiredToken) != "" {
		return Config{}, fmt.Errorf("%w: %s — the live tier now needs two credentials, %s (admin) and %s (non-admin); see spec 36 §9.5.3",
			ErrRetiredTokenVar, EnvRetiredToken, EnvProvisionerToken, EnvParticipantToken)
	}

	cfg := Config{
		Org:              getenv(EnvOrg),
		ProvisionerToken: getenv(EnvProvisionerToken),
		ParticipantToken: getenv(EnvParticipantToken),
		CandidateSHA:     getenv(EnvCandidateSHA),
	}

	for _, missing := range []struct {
		name  string
		value string
	}{
		{EnvOrg, cfg.Org},
		{EnvProvisionerToken, cfg.ProvisionerToken},
		{EnvParticipantToken, cfg.ParticipantToken},
		{EnvCandidateSHA, cfg.CandidateSHA},
	} {
		if missing.value == "" {
			return Config{}, fmt.Errorf("%w: %s is unset", ErrNotConfigured, missing.name)
		}
	}
	if !candidateSHAPattern.MatchString(cfg.CandidateSHA) {
		return Config{}, fmt.Errorf("%w: %s must be a full lowercase 40-hex public commit SHA",
			ErrNotConfigured, EnvCandidateSHA)
	}

	if cfg.ProvisionerToken == cfg.ParticipantToken {
		return Config{}, fmt.Errorf("%w: %s and %s hold the same secret, so both would authenticate as one login and every boundary assertion would self-satisfy",
			ErrIdenticalCredentials, EnvProvisionerToken, EnvParticipantToken)
	}

	for _, ambient := range ambientCredentialVars {
		value := getenv(ambient)
		if value == "" {
			continue
		}
		switch value {
		case cfg.ProvisionerToken:
			return Config{}, fmt.Errorf("%w: %s equals $%s", ErrAmbientCredential, EnvProvisionerToken, ambient)
		case cfg.ParticipantToken:
			return Config{}, fmt.Errorf("%w: %s equals $%s", ErrAmbientCredential, EnvParticipantToken, ambient)
		}
	}

	return cfg, nil
}
