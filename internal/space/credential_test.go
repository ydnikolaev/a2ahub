package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
		"getvisa":        "A2A_TOKEN_GETVISA",
		"a2a":            "A2A_TOKEN_A2A",
		"live-e2e-space": "A2A_TOKEN_LIVE_E2E_SPACE",
	} {
		if got := CredentialEnvVar(in); got != want {
			t.Fatalf("CredentialEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCredentialEnvVarIsAlwaysALegalShellIdentifier is AC-1050.9: the
// literal remedy `a2a connect`/`a2a doctor` print is `export
// <CredentialEnvVar(id)>=<token>` — bash and zsh both REFUSE `export` on a
// name that is not a valid identifier (verified: `export
// A2A_TOKEN_MY-SPACE=x` errors "not a valid identifier" in both shells).
// Every hyphenated id this table exercises — including the exact id
// `live-e2e-space` named by this phase's brief — must render a name built
// ONLY from [A-Z0-9_], never a hyphen.
func TestCredentialEnvVarIsAlwaysALegalShellIdentifier(t *testing.T) {
	t.Parallel()

	legalShellIdentifier := regexp.MustCompile(`^[A-Z0-9_]+$`)
	for _, id := range []string{
		"getvisa",
		"a2a",
		"live-e2e-space",
		"livee2e",
		"fixture-space",
		"my-space",
		"a-b-c-d",
	} {
		got := CredentialEnvVar(id)
		if !legalShellIdentifier.MatchString(got) {
			t.Fatalf("CredentialEnvVar(%q) = %q, contains a character bash/zsh `export` refuses (want only [A-Z0-9_])", id, got)
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

// writeCredentialStub drops an executable shell script named name into a fresh
// temp dir and returns its absolute path, for a `cmd:` reference to run.
func writeCredentialStub(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write credential stub: %v", err)
	}
	return path
}

// TestResolveCredentialFailingCommandIsNotUnresolved: a configured `cmd:`
// reference whose command fails is a broken setting, not "nothing configured"
// (AGENTS.md anti-pattern #22, local-space-2026-10 spec 08 US-4). It carries its
// own sentinel, which must not match ErrCredentialUnresolved, and it names the
// failure without echoing anything the command printed (its stdout may be a
// secret, and its stderr is not bounded to be safe).
//
// reason: not parallel: each case writes a script and execs it at once, and a
// sibling test forking concurrently can inherit the still-open write fd, which
// on Linux fails the exec with ETXTBSY instead of the exit this test asserts.
func TestResolveCredentialFailingCommandIsNotUnresolved(t *testing.T) {

	for _, tc := range []struct {
		name   string
		script string
		want   string
	}{
		{name: "non-zero exit", script: "echo leaked-stdout-secret\necho leaked-stderr-secret >&2\nexit 3\n", want: "exit status 3"},
		{name: "empty output", script: "printf '  \\n'\nexit 0\n", want: "printed no credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := writeCredentialStub(t, "cred", tc.script)
			got, err := ResolveCredential(context.Background(), "", CredentialReference{Kind: "cmd", Argv: []string{stub, "read"}})
			if !errors.Is(err, ErrCredentialCommandFailed) {
				t.Fatalf("ResolveCredential error = %v, want ErrCredentialCommandFailed", err)
			}
			if errors.Is(err, ErrCredentialUnresolved) {
				t.Fatalf("ResolveCredential error = %v also matches ErrCredentialUnresolved; a broken setting must not read as no credential", err)
			}
			if got.Token != "" {
				t.Fatalf("Token = %q, want empty", got.Token)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) || !strings.Contains(msg, stub) {
				t.Fatalf("error %q does not name the command %s and %q", msg, stub, tc.want)
			}
			if strings.Contains(msg, "leaked-") {
				t.Fatalf("error %q echoes the command's output", msg)
			}
		})
	}

	t.Run("missing executable", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "no-such-credential-helper")
		_, err := ResolveCredential(context.Background(), "", CredentialReference{Kind: "cmd", Argv: []string{missing}})
		if !errors.Is(err, ErrCredentialCommandFailed) || errors.Is(err, ErrCredentialUnresolved) {
			t.Fatalf("ResolveCredential error = %v, want ErrCredentialCommandFailed and not ErrCredentialUnresolved", err)
		}
	})
}
