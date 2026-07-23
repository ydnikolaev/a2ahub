package space

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// os.Getenv lives ONLY in this file within internal/space (rails: "config
// & secrets" — env access confined to the config/credentials layer,
// §7.4/§10.5).

// CredentialEnvVar renders the per-space override env var every surface
// honours as ResolveCredential's precedence step (a) — "A2A_TOKEN_" plus
// the uppercased space id (§7.4/§10.5). It is the SSOT for that name:
// cmd/a2a, internal/mcp and `a2a doctor` all render it through here, so
// the var a user is TOLD to export can never diverge from the var a write
// path actually reads.
func CredentialEnvVar(spaceID string) string {
	return "A2A_TOKEN_" + strings.ToUpper(spaceID)
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
// currently yields a token. It runs the same explicit-argv command
// ResolveCredential's cmd path would run (never sh -c) and discards the
// output — the token itself is never returned, logged, or persisted here.
func ghAuthTokenAvailable(ctx context.Context) bool {
	path, err := exec.LookPath("gh")
	if err != nil {
		return false
	}
	out, err := exec.CommandContext(ctx, path, "auth", "token").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
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
