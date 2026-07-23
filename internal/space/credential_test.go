package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestCredentialEnvVar(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"getvisa": "A2A_TOKEN_GETVISA",
		"a2a":     "A2A_TOKEN_A2A",
	} {
		if got := CredentialEnvVar(in); got != want {
			t.Fatalf("CredentialEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

// reason: mutates process PATH (t.Setenv) so the `gh` probe sees a stub
// instead of whatever is installed on the machine running the tests.
func TestDefaultCredentialReference(t *testing.T) {
	t.Run("prefers gh when it yields a token", func(t *testing.T) {
		dir := t.TempDir()
		writeStubGH(t, dir, "printf 'ghp_stub\\n'\n")
		t.Setenv("PATH", dir)

		if got := DefaultCredentialReference(context.Background(), "getvisa"); got != "cmd:gh auth token" {
			t.Fatalf("DefaultCredentialReference = %q, want the gh cmd reference", got)
		}
	})

	t.Run("falls back to the env convention when gh fails", func(t *testing.T) {
		dir := t.TempDir()
		writeStubGH(t, dir, "exit 1\n")
		t.Setenv("PATH", dir)

		if got := DefaultCredentialReference(context.Background(), "getvisa"); got != "env:A2A_TOKEN_GETVISA" {
			t.Fatalf("DefaultCredentialReference = %q, want the env reference", got)
		}
	})

	t.Run("falls back to the env convention when gh is absent", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		if got := DefaultCredentialReference(context.Background(), "getvisa"); got != "env:A2A_TOKEN_GETVISA" {
			t.Fatalf("DefaultCredentialReference = %q, want the env reference", got)
		}
	})

	t.Run("an empty token is not a credential", func(t *testing.T) {
		dir := t.TempDir()
		writeStubGH(t, dir, "printf '   \\n'\n")
		t.Setenv("PATH", dir)

		if got := DefaultCredentialReference(context.Background(), "getvisa"); got != "env:A2A_TOKEN_GETVISA" {
			t.Fatalf("DefaultCredentialReference = %q, want the env reference (blank output is not a token)", got)
		}
	})
}

// writeStubGH drops an executable `gh` shim into dir whose body is script.
func writeStubGH(t *testing.T, dir, script string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
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
