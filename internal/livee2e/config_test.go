package livee2e

import (
	"errors"
	"strings"
	"testing"
)

// env builds a getenv func from a map, so no test mutates process state and
// they can all run in parallel.
func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		EnvOrg:              "a2ahub-live-e2e",
		EnvProvisionerToken: "tok-provisioner",
		EnvParticipantToken: "tok-participant",
	}
}

func TestLoadConfigResolvesBothCredentials(t *testing.T) {
	t.Parallel()
	cfg, err := LoadConfig(env(validEnv()))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Org != "a2ahub-live-e2e" || cfg.ProvisionerToken != "tok-provisioner" || cfg.ParticipantToken != "tok-participant" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

// AC-962.2: absent configuration is a refusal, never a silent skip. Each
// variable is asserted independently — a guard that only checks the first
// missing one lets the other two through.
func TestLoadConfigFailsClosedOnEachMissingVar(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{EnvOrg, EnvProvisionerToken, EnvParticipantToken} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			pairs := validEnv()
			delete(pairs, missing)
			_, err := LoadConfig(env(pairs))
			if !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("want ErrNotConfigured, got %v", err)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error does not name the missing variable %q: %v", missing, err)
			}
		})
	}
}

func TestLoadConfigRejectsNilLookup(t *testing.T) {
	t.Parallel()
	if _, err := LoadConfig(nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

// The retired single-token name is refused rather than honoured as a
// fallback: one credential cannot express the identity model, and accepting
// it would run the boundary scenarios vacuously (spec 36 §T5).
func TestLoadConfigRefusesRetiredSingleTokenVar(t *testing.T) {
	t.Parallel()
	pairs := validEnv()
	pairs[EnvRetiredToken] = "tok-legacy"

	_, err := LoadConfig(env(pairs))
	if !errors.Is(err, ErrRetiredTokenVar) {
		t.Fatalf("want ErrRetiredTokenVar, got %v", err)
	}
	// The refusal must point at the replacement, or an operator following
	// the old runbook is stuck with a bare rejection.
	for _, want := range []string{EnvProvisionerToken, EnvParticipantToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %v", want, err)
		}
	}
}

// Two tokens that are one secret authenticate as one login — the same
// vacuity as configuring a single token, arriving by a different route.
func TestLoadConfigRefusesIdenticalCredentials(t *testing.T) {
	t.Parallel()
	pairs := validEnv()
	pairs[EnvParticipantToken] = pairs[EnvProvisionerToken]

	if _, err := LoadConfig(env(pairs)); !errors.Is(err, ErrIdenticalCredentials) {
		t.Fatalf("want ErrIdenticalCredentials, got %v", err)
	}
}

// Reusing the ambient credential is how a mis-set env reaches a real space,
// and the harness holds admin. Both roles and both ambient names are covered
// because the guard is only as good as its least-checked combination.
func TestLoadConfigRefusesAmbientCredential(t *testing.T) {
	t.Parallel()
	for _, ambient := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		for _, role := range []string{EnvProvisionerToken, EnvParticipantToken} {
			t.Run(ambient+"/"+role, func(t *testing.T) {
				t.Parallel()
				pairs := validEnv()
				pairs[ambient] = pairs[role]

				_, err := LoadConfig(env(pairs))
				if !errors.Is(err, ErrAmbientCredential) {
					t.Fatalf("want ErrAmbientCredential, got %v", err)
				}
				if !strings.Contains(err.Error(), ambient) || !strings.Contains(err.Error(), role) {
					t.Errorf("refusal names neither the ambient var nor the role: %v", err)
				}
			})
		}
	}
}

// An ambient token that happens to be set but matches neither credential is
// not a problem — refusing it would train operators to unset variables that
// are none of the tier's business.
func TestLoadConfigAllowsUnrelatedAmbientToken(t *testing.T) {
	t.Parallel()
	pairs := validEnv()
	pairs["GITHUB_TOKEN"] = "some-other-token"

	if _, err := LoadConfig(env(pairs)); err != nil {
		t.Fatalf("unrelated ambient token should not refuse: %v", err)
	}
}
