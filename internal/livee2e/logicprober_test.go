package livee2e

import (
	"context"
	"errors"
	"testing"
)

// TestLogicProberSatisfiesRunPreflight calls the REAL RunPreflight
// (preflight.go) with a well-built LogicProber, rather than stubbing
// RunPreflight out — proving the identity model the live tier asserts
// (spec 36 §T5: two distinct logins, participant not admin, provisioner
// admin) is the same shape the local tier asserts, just answered from a
// static map instead of a network call.
func TestLogicProberSatisfiesRunPreflight(t *testing.T) {
	t.Parallel()

	prober := NewLogicProber("prov-token", "logic-provisioner", "part-token", "logic-participant")
	cfg := Config{Org: "acme-org", ProvisionerToken: "prov-token", ParticipantToken: "part-token"}

	pre, err := RunPreflight(context.Background(), prober, cfg, "acme-org", "logic-space")
	if err != nil {
		t.Fatalf("RunPreflight() = %v, want nil", err)
	}
	if pre.ProvisionerLogin != "logic-provisioner" {
		t.Errorf("ProvisionerLogin = %q, want %q", pre.ProvisionerLogin, "logic-provisioner")
	}
	if pre.ParticipantLogin != "logic-participant" {
		t.Errorf("ParticipantLogin = %q, want %q", pre.ParticipantLogin, "logic-participant")
	}
}

// TestLogicProberSameLoginRefused builds a DELIBERATELY mis-shaped prober —
// both tokens resolve to the same login — and proves RunPreflight still
// refuses it with ErrSameLogin. Without this, a prober that always answers
// "distinct" by construction would make the guard pass vacuously.
func TestLogicProberSameLoginRefused(t *testing.T) {
	t.Parallel()

	prober := NewLogicProber("prov-token", "same-login", "part-token", "same-login")
	cfg := Config{Org: "acme-org", ProvisionerToken: "prov-token", ParticipantToken: "part-token"}

	_, err := RunPreflight(context.Background(), prober, cfg, "acme-org", "logic-space")
	if !errors.Is(err, ErrSameLogin) {
		t.Fatalf("RunPreflight() = %v, want errors.Is ErrSameLogin", err)
	}
}

// TestLogicProberParticipantAdminRefused builds a DELIBERATELY mis-shaped
// prober — the participant credential reports admin — and proves
// RunPreflight still refuses it with ErrParticipantIsAdmin, the anti-vacuity
// guard AC-960.4/spec 36 §T5 depends on.
func TestLogicProberParticipantAdminRefused(t *testing.T) {
	t.Parallel()

	prober := NewLogicProber("prov-token", "logic-provisioner", "part-token", "logic-participant")
	prober.ParticipantPermission = PermissionAdmin
	cfg := Config{Org: "acme-org", ProvisionerToken: "prov-token", ParticipantToken: "part-token"}

	_, err := RunPreflight(context.Background(), prober, cfg, "acme-org", "logic-space")
	if !errors.Is(err, ErrParticipantIsAdmin) {
		t.Fatalf("RunPreflight() = %v, want errors.Is ErrParticipantIsAdmin", err)
	}
}

// TestLogicProberUnknownTokenRefused proves the prober does not silently
// answer for a token it was not built with.
func TestLogicProberUnknownTokenRefused(t *testing.T) {
	t.Parallel()

	prober := NewLogicProber("prov-token", "logic-provisioner", "part-token", "logic-participant")

	if _, err := prober.Login(context.Background(), "unknown-token"); !errors.Is(err, ErrLogicProberUnknownToken) {
		t.Fatalf("Login(unknown) = %v, want errors.Is ErrLogicProberUnknownToken", err)
	}
	if _, err := prober.RepoPermission(context.Background(), "acme-org", "logic-space", "unknown-token"); !errors.Is(err, ErrLogicProberUnknownToken) {
		t.Fatalf("RepoPermission(unknown) = %v, want errors.Is ErrLogicProberUnknownToken", err)
	}
}
