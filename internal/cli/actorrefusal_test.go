package cli

import (
	"errors"
	"testing"
)

// TestResolveActorRefusesWhenNothingNamesTheActor is AC-1013.1.
//
// In a minimal container os/user has no passwd entry and $USER is unset, so
// every §7.4 source is empty and even the OS-user fallback resolves to
// nothing. The write was ALREADY refused in that case — actor.name carries a
// minLength in both event/v1 and envelope/v1, and that backstop still stands —
// but the message an agent received was a schema violation about a field it
// never knowingly set, naming neither `--actor-name` nor A2A_ACTOR_NAME.
//
// This test lives in `package cli` and drives resolveActorFrom rather than the
// exported ResolveActor, because the first version of it cleared $USER and
// hoped os/user would fail. On a developer machine it does not, so that test
// SKIPPED — and a skipped test for a refusal is worse than no test, since it
// reads as coverage on the only machine anyone runs it on. Passing the fallback
// in as a value makes the empty case an ordinary argument.
//
// The refusal is at the single place the CLI resolves an actor, not in each of
// ~10 write verbs: "enforced only in the caller" is a shape this repo has
// already paid for twice.
//
// internal/mcp keeps its own resolver and its own schema-level refusal, by an
// explicit operator decision to leave that surface alone. The two surfaces have
// always had separate resolvers (internal/mcp/adapters.go), so this is not a
// new divergence — a fact worth stating because it was earlier assumed to be
// the opposite, without being checked.
func TestResolveActorRefusesWhenNothingNamesTheActor(t *testing.T) {
	// reason: reads A2A_ACTOR_* from the process env inside resolveActorFrom,
	// so it must not race a sibling that sets them.
	t.Setenv(envActorName, "")

	_, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "")
	if !errors.Is(err, ErrNoActorName) {
		t.Fatalf("err = %v, want ErrNoActorName — a write with nobody attached must be refused by name, "+
			"not minted and then rejected by the schema for a field the caller never set", err)
	}
}

// TestResolveActorAcceptsTheOSUserFallback is the other half of the pair, and
// it is what keeps the case above from being a tautology: with a non-empty
// OS-user value and nothing else, resolution SUCCEEDS. A refusal that fired
// whenever the explicit sources were empty would break every ordinary local
// invocation, which is the opposite failure and just as bad.
func TestResolveActorAcceptsTheOSUserFallback(t *testing.T) {
	t.Setenv(envActorName, "")

	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "some-os-user")
	if err != nil {
		t.Fatalf("resolveActorFrom with an OS user: %v — the refusal must fire only when NOTHING names "+
			"the actor, never merely because no flag or env var did", err)
	}
	if a.Name != "some-os-user" {
		t.Errorf("Name = %q, want the OS-user fallback", a.Name)
	}
	if a.Kind != "agent" {
		t.Errorf("Kind = %q, want the agent default", a.Kind)
	}
}
