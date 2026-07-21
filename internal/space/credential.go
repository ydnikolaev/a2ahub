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
