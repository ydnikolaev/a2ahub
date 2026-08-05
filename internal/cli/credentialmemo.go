package cli

import (
	"context"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// memoizeCredentialResolver wraps resolve so it is invoked at most once per
// distinct (explicitEnvVar, ref) input for the lifetime of the returned
// resolver — one `a2a doctor` command invocation, since NewDoctorCommand
// constructs a fresh resolver per process and never persists or shares it
// across runs.
//
// This exists because DoctorCommand's checks each resolve credentials
// independently inside their own loop over the connected spaces: six
// distinct call sites in cmd_doctor.go (credentials, space access,
// scaffolding-current's "who can fix it", auto-merge, stuck-green-PRs,
// CODEOWNERS) all resolve the SAME space's credential the SAME way — and a
// seventh, visibility.go's doctorVisibilityRows ("repository visibility"),
// goes through this exact same field too. Without this wrapper, a machine
// config entry of the form `cmd:gh auth token` spawns a subprocess for
// every one of those checks — up to seven `gh` invocations per connected
// space on a single `a2a doctor` run. Wrapping the resolver here collapses
// that to one resolution per distinct input, with zero changes at any call
// site: fixing the seam fixes every current AND future caller of this
// field, not just the ones enumerated above.
//
// Keying on the FULL input (not resolving once globally) is deliberate and
// load-bearing: it is what keeps different spaces resolving to different
// credentials. Only a REPEAT resolution of the SAME space's SAME reference
// is the waste this removes; per-space resolution correctness is preserved.
//
// The wrapped resolver's ERROR is cached too, alongside the value, mirroring
// internal/space/work_runtime.go's resolveOnce (which caches both value and
// error for the same reason): a resolver that fails for one space must not
// be retried at every remaining site for that same space — an unresolvable
// `cmd:` reference would otherwise still spawn up to six failing
// subprocesses instead of one.
//
// The cached token lives only inside this decorator's closure, which itself
// lives only as long as the DoctorCommand that wired it — one command's
// lifetime. It is never logged, persisted, or serialized anywhere; it exists
// solely to short-circuit the repeat resolutions described above.
//
// A nil resolve returns a nil decorator, not a wrapper that panics on call.
// credentialResolver is a plain func type, not an interface, so a nil
// decorator is indistinguishable from a nil resolve to every caller:
// mirrorCredential's own `resolve == nil` guard (mirrorcredential.go) still
// sees nil and degrades exactly as it does today, and none of the six
// direct call sites in cmd_doctor.go nil-checks c.resolveCredential before
// calling it — they all rely on NewDoctorCommand always wiring a non-nil
// real resolver. Returning a non-nil wrapper around a nil resolve would
// silently defeat mirrorCredential's guard and change that behavior; nil
// stays nil to keep both invariants exactly as they were.
func memoizeCredentialResolver(resolve credentialResolver) credentialResolver {
	if resolve == nil {
		return nil
	}
	var (
		mu    sync.Mutex
		cache = make(map[string]credentialMemoEntry)
	)
	return func(ctx context.Context, explicitEnvVar string, ref space.CredentialReference) (host.Credential, error) {
		key := credentialMemoKey(explicitEnvVar, ref)

		mu.Lock()
		defer mu.Unlock()

		if entry, ok := cache[key]; ok {
			return entry.credential, entry.err
		}
		credential, err := resolve(ctx, explicitEnvVar, ref)
		cache[key] = credentialMemoEntry{credential: credential, err: err}
		return credential, err
	}
}

// credentialMemoEntry is one memoised resolution: the credential AND the
// error the wrapped resolver produced for a given input, cached together so
// a failing resolution is never silently retried as a successful one.
type credentialMemoEntry struct {
	credential host.Credential
	err        error
}

// credentialMemoKey builds a comparable cache key from a credentialResolver
// call's inputs. space.CredentialReference carries an Argv []string field
// (internal/space/config.go), which makes the struct itself non-comparable
// and therefore unusable as a map key directly.
//
// The separator is "\x00", matching internal/space/config.go's own
// component-key convention (`component.Channel + "\x00" + component.Profile
// + "\x00" + component.CodePath`) rather than inventing a new one: NUL
// cannot appear in an env var name, and it cannot appear in an Argv element
// either — space.ParseCredentialReference splits `cmd:` arguments with
// strings.Fields, so no element can itself contain NUL or whitespace. Kind
// is included so an "env:" reference and a "cmd:" reference can never
// collide on the same key.
func credentialMemoKey(explicitEnvVar string, ref space.CredentialReference) string {
	parts := make([]string, 0, 3+len(ref.Argv))
	parts = append(parts, explicitEnvVar, ref.Kind, ref.Env)
	parts = append(parts, ref.Argv...)
	return strings.Join(parts, "\x00")
}
