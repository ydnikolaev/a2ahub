package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestMirrorCredentialFollowsTheWritePrecedenceAndDegrades(t *testing.T) {
	t.Parallel()

	machine := space.MachineConfig{Credentials: map[string]string{"getvisa": "env:CUSTOM_TOKEN"}}

	t.Run("passes the configured reference through", func(t *testing.T) {
		t.Parallel()

		var gotEnvVar string
		var gotRef space.CredentialReference
		resolve := func(_ context.Context, envVar string, ref space.CredentialReference) (host.Credential, error) {
			gotEnvVar, gotRef = envVar, ref
			return host.Credential{Token: "resolved"}, nil
		}

		got := mirrorCredential(context.Background(), resolve, "getvisa", machine)

		if got.Token != "resolved" {
			t.Fatalf("credential = %+v, want the resolved token", got)
		}
		if want := space.CredentialEnvVar("getvisa"); gotEnvVar != want {
			t.Fatalf("explicit env var = %q, want %q — the override is precedence step (a)", gotEnvVar, want)
		}
		if gotRef.Kind != "env" || gotRef.Env != "CUSTOM_TOKEN" {
			t.Fatalf("machine reference = %+v, want the parsed env:CUSTOM_TOKEN entry", gotRef)
		}
	})

	t.Run("an unresolvable credential degrades to tokenless", func(t *testing.T) {
		t.Parallel()

		resolve := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{}, errors.New("no credential configured")
		}
		if got := mirrorCredential(context.Background(), resolve, "getvisa", machine); got.Token != "" {
			t.Fatalf("credential = %+v, want empty", got)
		}
	})

	t.Run("an unparseable machine reference does not block the env override", func(t *testing.T) {
		t.Parallel()

		broken := space.MachineConfig{Credentials: map[string]string{"getvisa": "!!not-a-reference"}}
		resolve := func(_ context.Context, _ string, ref space.CredentialReference) (host.Credential, error) {
			if ref.Kind != "" {
				t.Errorf("a broken reference must be dropped, got %+v", ref)
			}
			return host.Credential{Token: "from-env"}, nil
		}
		if got := mirrorCredential(context.Background(), resolve, "getvisa", broken); got.Token != "from-env" {
			t.Fatalf("credential = %+v, want the env override to still win", got)
		}
	})
}

// TestMirrorRefreshingCommandsCarryACredentialResolver names the three
// commands that refresh a mirror today and asserts each wires a resolver.
// mirrorCredential returns the empty credential when its resolver is nil, so a
// command that forgets to wire one compiles, runs, passes every existing test,
// and silently fetches tokenless forever — precisely the state this repair
// exists to leave behind.
//
// It is NOT the guard, despite once claiming to be: this list is
// hand-maintained, so a FOURTH command never joins it. That is the hole
// TestMirrorRefreshingStructsWireACredentialResolver
// (mirrorcredential_ast_test.go) closes, by discovering the same invariant
// from the package's own sources. This test survives it as the readable
// statement of which commands exist and what each is for — the AST gate can
// say "every struct with cloneOrFetch", but it cannot say "sync, connect and
// doctor, and here is why each one refreshes a mirror".
func TestMirrorRefreshingCommandsCarryACredentialResolver(t *testing.T) {
	t.Parallel()

	if got := NewSyncCommand("p", "m", "r", NewNoopPendingMarker()).resolveCredential; got == nil {
		t.Error("sync refreshes every connected space's mirror but wires no credential resolver")
	}
	if got := NewConnectCommand("p", "m", "r").resolveCredential; got == nil {
		t.Error("connect performs the FIRST clone of a space — including a private one — but wires no credential resolver")
	}
	if got := NewDoctorCommand(host.NewFakeHost(), "0.0.0", "p", "m", "r").resolveCredential; got == nil {
		t.Error("doctor's space-access check fetches every mirror but wires no credential resolver")
	}
}
