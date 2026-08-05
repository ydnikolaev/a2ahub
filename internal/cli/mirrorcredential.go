package cli

import (
	"context"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// credentialResolver is space.ResolveCredential's shape, named here so the
// three commands that refresh a mirror (sync, doctor, connect) can hold the
// same seam and share mirrorCredential below. DoctorCommand already carried
// this field for its API-side checks; sync and connect gained it when the
// mirror refresh learned to authenticate.
type credentialResolver func(ctx context.Context, explicitEnvVar string, ref space.CredentialReference) (host.Credential, error)

// mirrorCredential resolves the credential a mirror REFRESH should carry for
// spaceID, following the same precedence a write does — the explicit
// A2A_TOKEN_<SPACE_ID> override first, this machine's configured reference
// second — and returning the empty credential when neither resolves.
//
// Degrading instead of failing is deliberate and is the difference between
// this and the write path. Every read verb works today against a public space
// with no token configured at all, and `a2a connect` against a public space
// is frequently the very first command an operator runs, before any
// credential exists. Making the refresh depend on a resolvable token would
// break all of that to produce a worse error than git's own.
//
// What the empty credential costs is exactly the private case, and there the
// failure is not silent: git returns the remote's own 404 and doctor's
// space-access check translates it (see doctorPrivateRepoHint).
func mirrorCredential(ctx context.Context, resolve credentialResolver, spaceID string, machine space.MachineConfig) host.Credential {
	if resolve == nil {
		return host.Credential{}
	}
	var ref space.CredentialReference
	if raw, present := machine.Credentials[spaceID]; present {
		if parsed, err := space.ParseCredentialReference(raw); err == nil {
			ref = parsed
		}
	}
	credential, err := resolve(ctx, space.CredentialEnvVar(spaceID), ref)
	if err != nil {
		return host.Credential{}
	}
	return credential
}
