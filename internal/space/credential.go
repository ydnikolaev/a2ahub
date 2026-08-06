package space

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/ghauth"
	"github.com/ydnikolaev/a2ahub/internal/host"
)

// os.Getenv lives ONLY in this file within internal/space (rails: "config
// & secrets" — env access confined to the config/credentials layer,
// §7.4/§10.5).

// CredentialEnvVar renders the per-space override env var every surface
// honours as ResolveCredential's precedence step (a) — "A2A_TOKEN_" plus
// the uppercased space id, with every `-` mapped to `_` first (§7.4/§10.5,
// P41 variant c). The mapping is required, not cosmetic: a hyphenated
// space id (e.g. "my-space") uppercases to a name bash/zsh both REFUSE as
// an identifier ("A2A_TOKEN_MY-SPACE" — `export` errors "not a valid
// identifier"), which is exactly the remedy `a2a connect`/`a2a doctor`
// print — an operator without `gh auth token` working could never type it.
// The mapping stays injective (two distinct ids can never collide on one
// var name) because schemas/manifest/v1/space.schema.json's `space`
// pattern forbids `_` in a real space id (P41): only `-` can turn into `_`,
// never the reverse. It is the SSOT for this name: cmd/a2a, internal/mcp
// and `a2a doctor` all render it through here, so the var a user is TOLD
// to export can never diverge from the var a write path actually reads.
func CredentialEnvVar(spaceID string) string {
	return "A2A_TOKEN_" + strings.ToUpper(strings.ReplaceAll(spaceID, "-", "_"))
}

// DefaultCredentialReference picks the machine-config credential reference
// to seed for a newly connected space, so a fresh install can write to a
// space without hand-editing YAML first:
//
//   - `cmd:gh auth token` when the GitHub CLI is installed AND already
//     authenticated (the overwhelmingly common developer setup, and the
//     one the operator otherwise ends up pasting into their shell rc by
//     hand);
//   - `env:A2A_TOKEN_<SPACE_ID>` otherwise — the documented convention,
//     satisfied by exporting that one variable.
//
// Either way the reference is a REFERENCE: no secret is ever written to
// disk, and the explicit A2A_TOKEN_<SPACE_ID> override still wins over it
// at resolve time (ResolveCredential's precedence (a)).
//
// This probe lives here because internal/space's credential layer is the
// only place allowed to look at the machine's environment (the file's own
// os.Getenv rail, §7.4/§10.5) — callers get a plain string back.
func DefaultCredentialReference(ctx context.Context, spaceID string) string {
	if ghAuthTokenAvailable(ctx) {
		return "cmd:gh auth token"
	}
	return "env:" + CredentialEnvVar(spaceID)
}

// ghAuthTokenAvailable reports whether `gh auth token` is installed and
// currently yields a token, discarding the token itself.
//
// The probe moved to internal/ghauth when internal/release turned out to need
// the same fact — and to have shipped without it, leaving every release read
// on GitHub's anonymous per-IP budget while a working gh login sat unused.
// Two layers needing one fact, neither able to import the other, is what that
// leaf package is for.
func ghAuthTokenAvailable(ctx context.Context) bool {
	return ghauth.Available(ctx)
}

// ResolveCredential resolves a write credential per the Open Q1 RESOLVED
// precedence (spec 05 §11 Amendments / Open questions #1): (a) the
// explicit override env var explicitEnvVar, if set and non-empty; else
// (b) the machine-config reference ref (env:<VAR> looked up via
// os.Getenv, or cmd:<argv...> run via os/exec with explicit argv — never
// sh -c, output trimmed and treated as the secret); else (c) an
// actionable error naming exactly what was checked. The resolved secret
// is never persisted or logged by this function; it is returned once, for
// immediate use in a host.Host call.
func ResolveCredential(ctx context.Context, explicitEnvVar string, ref CredentialReference) (host.Credential, error) {
	const op = "ResolveCredential"

	if explicitEnvVar != "" {
		if v := os.Getenv(explicitEnvVar); v != "" {
			return host.Credential{Token: v}, nil
		}
	}

	switch ref.Kind {
	case "env":
		if v := os.Getenv(ref.Env); v != "" {
			return host.Credential{Token: v}, nil
		}
	case "cmd":
		if len(ref.Argv) > 0 {
			out, err := exec.CommandContext(ctx, ref.Argv[0], ref.Argv[1:]...).Output()
			if err == nil {
				if secret := strings.TrimSpace(string(out)); secret != "" {
					return host.Credential{Token: secret}, nil
				}
			}
		}
	}

	return host.Credential{}, &Error{
		Op:    op,
		Input: describeChecked(explicitEnvVar, ref),
		Err:   ErrCredentialUnresolved,
	}
}

// ResolveWriteCredential is ResolveCredential plus one final step: the GitHub
// CLI login the machine already has.
//
// # Why this exists as a second function rather than a change to the first
//
// `a2a connect` seeds a space's credential reference as `cmd:gh auth token`
// whenever a gh login is available (DefaultCredentialReference above), so on a
// machine with working gh, a space connected TODAY writes fine. A space
// connected BEFORE gh was set up — or one whose machine config was
// hand-authored or copied between machines — carries a plain
// `env:A2A_TOKEN_<ID>` reference instead, and refuses forever on a machine that
// is perfectly well authenticated. The seed happens once, at connect time; the
// resolve happens on every write, and only the resolve knows what the machine
// looks like now.
//
// Found on 2026-08-06, immediately after the identical gap was closed for
// `a2a feedback submit`: feedback started falling through to the gh login while
// every REAL space write on the same machine still refused. Fixing the smaller
// surface and leaving the larger one is worse than fixing neither, because it
// makes the remaining refusal look like a space-specific problem.
//
// It is a separate function, and ResolveCredential is deliberately left as the
// exact-named-sources primitive, because a caller that composes several
// resolves needs the gh step to happen ONCE, at the end — folding it into the
// primitive would let it win over a later, more specific source that the caller
// had ordered ahead of it.
//
// ResolveWriteCredential is part of the public package API.
func ResolveWriteCredential(ctx context.Context, explicitEnvVar string, ref CredentialReference) (host.Credential, error) {
	credential, err := ResolveCredential(ctx, explicitEnvVar, ref)
	if err == nil {
		return credential, nil
	}
	if token, ok := ghauth.Token(ctx); ok {
		return host.Credential{Token: token}, nil
	}
	return host.Credential{}, err
}

// describeChecked names exactly what ResolveCredential checked, for the
// actionable-error requirement (Open Q1 RESOLVED: "an actionable error
// naming exactly which credential is missing and which of (a)/(b) was
// checked").
func describeChecked(explicitEnvVar string, ref CredentialReference) string {
	var parts []string
	if explicitEnvVar != "" {
		parts = append(parts, "env var "+explicitEnvVar)
	}
	switch ref.Kind {
	case "env":
		parts = append(parts, "configured env reference "+ref.Env)
	case "cmd":
		parts = append(parts, "configured cmd reference "+strings.Join(ref.Argv, " "))
	default:
		parts = append(parts, "no machine-config reference configured")
	}
	return strings.Join(parts, "; ")
}
