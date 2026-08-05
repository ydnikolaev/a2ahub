package ghauth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestTokenWithNoGitHubCLI covers the branch every machine without `gh` takes,
// and the one whose behaviour is load-bearing: it must be an ordinary "no
// token", never an error, because both callers have a legitimate anonymous
// fallback and failing here would break CI runners and fresh installs to fix
// a rate limit.
func TestTokenWithNoGitHubCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if token, ok := Token(context.Background()); ok || token != "" {
		t.Fatalf("Token() = %q, %v; want \"\", false when gh is not installed", token, ok)
	}
	if Available(context.Background()) {
		t.Fatal("Available() = true with no gh on PATH")
	}
}

// TestTokenReadsWhatTheCLIPrints uses a stub `gh` on PATH rather than the
// machine's real one. Never the real binary: a test that reads the operator's
// live token can print it in a failure message, which is how one reached a log
// file on 2026-08-05.
func TestTokenReadsWhatTheCLIPrints(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	// Trailing newline included deliberately — the real CLI prints one, and
	// trimming it is this package's job, not the caller's.
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf 'stub-token\\n'\n"), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}
	t.Setenv("PATH", dir)

	token, ok := Token(context.Background())
	if !ok || token != "stub-token" {
		t.Fatalf("Token() = %q, %v; want \"stub-token\", true (the trailing newline must be trimmed)", token, ok)
	}
	if !Available(context.Background()) {
		t.Fatal("Available() = false while Token() succeeded")
	}
}

// TestTokenTreatsAFailingCLIAsNoToken covers a gh that exists but is logged
// out — a non-zero exit, which must read as "no token available" rather than
// propagating.
func TestTokenTreatsAFailingCLIAsNoToken(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}
	t.Setenv("PATH", dir)

	if token, ok := Token(context.Background()); ok || token != "" {
		t.Fatalf("Token() = %q, %v; want \"\", false for a logged-out gh", token, ok)
	}
}

// TestTokenTreatsEmptyOutputAsNoToken guards the case that would otherwise
// authenticate a request with an empty bearer token — worse than anonymous,
// because it looks authenticated and is rejected.
func TestTokenTreatsEmptyOutputAsNoToken(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '   \\n'\n"), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}
	t.Setenv("PATH", dir)

	if token, ok := Token(context.Background()); ok || token != "" {
		t.Fatalf("Token() = %q, %v; want \"\", false for whitespace-only output", token, ok)
	}
}
