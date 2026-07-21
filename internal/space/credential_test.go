package space

import (
	"context"
	"errors"
	"testing"
)

// reason: mutates process env (t.Setenv) — not safe to run in parallel
// with other env-mutating tests (rails pre-flight checklist #7).
func TestResolveCredentialExplicitEnvWins(t *testing.T) {
	t.Setenv("A2A_TEST_TOKEN", "explicit-secret")

	ref := CredentialReference{Kind: "env", Env: "A2A_TEST_TOKEN_CONFIGURED"} // deliberately unset
	got, err := ResolveCredential(context.Background(), "A2A_TEST_TOKEN", ref)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if got.Token != "explicit-secret" {
		t.Fatalf("Token = %q, want explicit-secret (explicit env must win over configured ref)", got.Token)
	}
}

// reason: mutates process env (t.Setenv).
func TestResolveCredentialFallsBackToConfiguredEnvRef(t *testing.T) {
	t.Setenv("A2A_TEST_TOKEN_CONFIGURED", "configured-secret")

	ref := CredentialReference{Kind: "env", Env: "A2A_TEST_TOKEN_CONFIGURED"}
	got, err := ResolveCredential(context.Background(), "A2A_TEST_TOKEN_UNSET", ref)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if got.Token != "configured-secret" {
		t.Fatalf("Token = %q, want configured-secret", got.Token)
	}
}

func TestResolveCredentialCmdReference(t *testing.T) {
	t.Parallel()

	ref := CredentialReference{Kind: "cmd", Argv: []string{"echo", "cmd-helper-secret"}}
	got, err := ResolveCredential(context.Background(), "", ref)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if got.Token != "cmd-helper-secret" {
		t.Fatalf("Token = %q, want cmd-helper-secret", got.Token)
	}
}

// reason: relies on two specific env vars being ABSENT; run serially so no
// sibling test in this file can Setenv the same names concurrently.
func TestResolveCredentialMissingFailsLoudNeverLiteralFallback(t *testing.T) {
	ref := CredentialReference{Kind: "env", Env: "A2A_TEST_TOKEN_ALSO_UNSET_XYZ"}
	got, err := ResolveCredential(context.Background(), "A2A_TEST_TOKEN_ALSO_UNSET_ABC", ref)
	if !errors.Is(err, ErrCredentialUnresolved) {
		t.Fatalf("ResolveCredential error = %v, want ErrCredentialUnresolved", err)
	}
	if got.Token != "" {
		t.Fatalf("Token = %q, want empty (no literal fallback)", got.Token)
	}
}
