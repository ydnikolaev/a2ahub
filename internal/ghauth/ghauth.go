// Package ghauth reads the GitHub token the GitHub CLI is already logged in
// with.
//
// It exists because two independent layers need the same fact and neither may
// depend on the other. internal/space asks "is a gh login available?" to seed
// a machine-config credential reference; internal/release needs the token
// itself so its api.github.com reads are authenticated. internal/release
// deliberately imports almost nothing (internal/version alone), and
// internal/host imports nothing of ours at all, so a leaf package is the only
// home that serves both without inventing a dependency edge or duplicating the
// probe.
//
// Why this is worth a package rather than eight copied lines: an unauthenticated
// GitHub read is not a degraded read, it is a read on a 60-per-hour per-IP
// budget shared by every anonymous caller on that address. A machine that has a
// working `gh` login and still spends that budget is failing for no reason —
// which is exactly what shipped, in five call sites at once, because the one
// function that resolved this token looked only at environment variables.
package ghauth

import (
	"context"
	"os/exec"
	"strings"
)

// Token returns the token `gh auth token` prints, and whether one was
// obtained.
//
// It runs an explicit argv and never a shell, matching the machine-config
// `cmd:` resolver's own rail: a token source is executed, never interpreted.
// The token is returned to the caller and is neither logged nor persisted
// here; callers must keep it in memory for the life of the process and no
// longer.
//
// A missing `gh`, a logged-out `gh`, or any non-zero exit is an ordinary
// "no token available" — not an error worth propagating, because every caller
// has a legitimate anonymous fallback and failing loudly here would break
// machines that never had `gh` installed.
func Token(ctx context.Context) (string, bool) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return "", false
	}
	out, err := exec.CommandContext(ctx, path, "auth", "token").Output()
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(string(out))
	return token, token != ""
}

// Available reports whether Token would yield one, discarding the token.
//
// Kept separate so a caller that only needs the yes/no — seeding a credential
// REFERENCE rather than resolving a credential — never holds the secret at
// all.
func Available(ctx context.Context) bool {
	_, ok := Token(ctx)
	return ok
}
