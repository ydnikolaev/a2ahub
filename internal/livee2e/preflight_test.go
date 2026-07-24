package livee2e

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubProber answers Login/RepoPermission from token-keyed maps — the seam
// wave 3 replaces with a real GitHub reader.
type stubProber struct {
	logins map[string]string
	perms  map[string]Permission
	err    error
}

func (s stubProber) Login(_ context.Context, token string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.logins[token], nil
}

func (s stubProber) RepoPermission(_ context.Context, _, _, token string) (Permission, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.perms[token], nil
}

func healthyProber() stubProber {
	return stubProber{
		logins: map[string]string{"tok-provisioner": "operator", "tok-participant": "a2ahub-e2e-bot"},
		perms:  map[string]Permission{"tok-provisioner": PermissionAdmin, "tok-participant": PermissionWrite},
	}
}

func healthyConfig() Config {
	return Config{Org: "a2ahub-live-e2e", ProvisionerToken: "tok-provisioner", ParticipantToken: "tok-participant"}
}

func TestRunPreflightAcceptsTheSpecifiedTopology(t *testing.T) {
	t.Parallel()
	got, err := RunPreflight(context.Background(), healthyProber(), healthyConfig(), "a2ahub-live-e2e", "space")
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if got.ProvisionerLogin != "operator" || got.ParticipantLogin != "a2ahub-e2e-bot" {
		t.Fatalf("preflight did not carry both identities: %+v", got)
	}
}

// AC-960.3. The harness holds admin; the whole point of the org fence is that
// a mis-set env cannot reach a space that matters.
func TestCheckRepoRefusesOutsideTheDedicatedOrg(t *testing.T) {
	t.Parallel()
	cfg := healthyConfig()

	for name, repo := range map[string]struct{ owner, name string }{
		"a real space":     {"ydnikolaev", "getvisa-space"},
		"a bare name":      {"", "space"},
		"a bare owner":     {"a2ahub-live-e2e", ""},
		"a lookalike org":  {"a2ahub-live-e2e-2", "space"},
		"the product repo": {"ydnikolaev", "a2ahub"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := cfg.CheckRepo(repo.owner, repo.name); !errors.Is(err, ErrRepoOutsideOrg) {
				t.Fatalf("want ErrRepoOutsideOrg for %s/%s, got %v", repo.owner, repo.name, err)
			}
		})
	}
}

// GitHub logins are case-insensitive; a fence that rejected the org's own
// repo on capitalisation would be worked around rather than respected.
func TestCheckRepoIsCaseInsensitiveOnTheOrg(t *testing.T) {
	t.Parallel()
	if err := healthyConfig().CheckRepo("A2AHub-Live-E2E", "space"); err != nil {
		t.Fatalf("case-different org should be accepted: %v", err)
	}
}

func TestRunPreflightRefusesRepoOutsideTheOrg(t *testing.T) {
	t.Parallel()
	_, err := RunPreflight(context.Background(), healthyProber(), healthyConfig(), "ydnikolaev", "getvisa-space")
	if !errors.Is(err, ErrRepoOutsideOrg) {
		t.Fatalf("want ErrRepoOutsideOrg, got %v", err)
	}
}

// AC-960.4 — THE anti-vacuity guard, and the reason this package has a
// preflight at all. With `enforce_admins: false` (which production runs, and
// the test space mirrors), branch protection does not bind admins: an admin
// participant makes every boundary assertion pass without testing anything.
//
// The teeth: the run must REFUSE. A test that merely checked "no panic" would
// still pass with the guard deleted.
func TestRunPreflightRefusesAnAdminParticipant(t *testing.T) {
	t.Parallel()
	p := healthyProber()
	p.perms["tok-participant"] = PermissionAdmin

	_, err := RunPreflight(context.Background(), p, healthyConfig(), "a2ahub-live-e2e", "space")
	if !errors.Is(err, ErrParticipantIsAdmin) {
		t.Fatalf("want ErrParticipantIsAdmin, got %v", err)
	}
	if !strings.Contains(err.Error(), "a2ahub-e2e-bot") {
		t.Errorf("refusal does not name the offending login: %v", err)
	}
}

// The write level the runbook provisions, plus the levels a misconfigured
// invite could leave behind — none of them is admin, so none may refuse.
func TestRunPreflightAcceptsAnyNonAdminParticipant(t *testing.T) {
	t.Parallel()
	for _, perm := range []Permission{PermissionWrite, PermissionRead, PermissionNone} {
		t.Run(string(perm), func(t *testing.T) {
			t.Parallel()
			p := healthyProber()
			p.perms["tok-participant"] = perm

			if _, err := RunPreflight(context.Background(), p, healthyConfig(), "a2ahub-live-e2e", "space"); err != nil {
				t.Fatalf("participant with %q must not be refused by the anti-vacuity guard: %v", perm, err)
			}
		})
	}
}

// diff-authz compares changed paths against the PR AUTHOR's section, so one
// login makes the cross-section scenario unconstructible rather than weak.
func TestRunPreflightRefusesOneLoginWearingTwoHats(t *testing.T) {
	t.Parallel()
	p := healthyProber()
	p.logins["tok-participant"] = "Operator" // same account, different casing

	_, err := RunPreflight(context.Background(), p, healthyConfig(), "a2ahub-live-e2e", "space")
	if !errors.Is(err, ErrSameLogin) {
		t.Fatalf("want ErrSameLogin, got %v", err)
	}
}

// Discovered up front rather than halfway through provisioning, where a
// partial reset of the test space is the worst place to learn it.
func TestRunPreflightRefusesANonAdminProvisioner(t *testing.T) {
	t.Parallel()
	p := healthyProber()
	p.perms["tok-provisioner"] = PermissionWrite

	_, err := RunPreflight(context.Background(), p, healthyConfig(), "a2ahub-live-e2e", "space")
	if !errors.Is(err, ErrProvisionerNotAdmin) {
		t.Fatalf("want ErrProvisionerNotAdmin, got %v", err)
	}
}

func TestRunPreflightRefusesAnEmptyLogin(t *testing.T) {
	t.Parallel()
	p := healthyProber()
	p.logins["tok-participant"] = ""

	_, err := RunPreflight(context.Background(), p, healthyConfig(), "a2ahub-live-e2e", "space")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestRunPreflightRefusesAMissingProber(t *testing.T) {
	t.Parallel()
	if _, err := RunPreflight(context.Background(), nil, healthyConfig(), "a2ahub-live-e2e", "space"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

// A host that cannot answer is not evidence of a safe topology. Every probe
// failure must surface as an error, never as an assumed-good default.
func TestRunPreflightPropagatesProbeFailures(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("api down")
	p := healthyProber()
	p.err = sentinel

	_, err := RunPreflight(context.Background(), p, healthyConfig(), "a2ahub-live-e2e", "space")
	if !errors.Is(err, sentinel) {
		t.Fatalf("want the probe error to surface, got %v", err)
	}
}
