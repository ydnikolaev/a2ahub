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

// noEnv is the empty environment every case in this file states explicitly.
//
// The environment became a parameter for the same reason the OS user did.
// resolveActorFrom runs agent detection, so a process-env read would have made
// every assertion here depend on whichever harness happened to run the suite:
// green on a bare shell, red inside Claude Code, with neither result telling
// you anything about the code.
func noEnv(string) string { return "" }

func TestResolveActorRefusesWhenNothingNamesTheActor(t *testing.T) {
	t.Parallel()

	_, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "", noEnv)
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
	t.Parallel()

	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "some-os-user", noEnv)
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

// TestDetectionOutranksEveryNameACallerCanType is the assertion behind the
// whole change, and it is written from a defect in committed data rather than
// from a preference.
//
// The getvisa space holds `kind: agent, name: codex` on a publish codex did
// not perform. The name was typed (`--actor-name` / A2A_ACTOR_NAME), nothing
// verified it, and an append-only log now carries it forever. The old order
// put every one of those sources ABOVE any knowledge of the running process,
// so the false name won by construction.
//
// So this pins the inversion: with a detectable agent present, a flag, an env
// var, a harness default and a config value that all name a RIVAL AGENT are
// overridden regardless of where they came from. If this test is ever "fixed"
// by restoring caller priority, the codex defect becomes reachable again.
//
// The boundary is deliberately narrow — see TestANonAgentNameSurvivesDetection
// below and agentid.Contradicts. A wider rule was tried first and cost the
// logic tier a 20-minute timeout.
func TestDetectionOutranksEveryNameACallerCanType(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		switch k {
		case "CLAUDECODE":
			return "1"
		case "CLAUDE_CODE_EXECPATH":
			return "/Users/x/.local/share/claude/versions/2.1.223"
		case envActorName:
			return "codex"
		}
		return ""
	}

	a, err := resolveActorFrom(
		ActorFlags{Name: "codex"},
		HarnessDefaults{Name: "codex"},
		ConfigActor{Name: "codex"},
		"yuranikolaev", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Name != "claude-code" {
		t.Fatalf("Name = %q, want claude-code — a typed name must not be able to misattribute "+
			"the write to another vendor when the environment says which agent is running", a.Name)
	}
	if a.Kind != "agent" {
		t.Errorf("Kind = %q, want agent", a.Kind)
	}
}

// TestExplicitHumanSuppressesDetection is the escape hatch, and it has to
// exist: `kind: human` is a person claiming something about themselves, which
// is a different claim from "which binary is running". Without this, a person
// working inside an agent harness could never record their own act.
func TestExplicitHumanSuppressesDetection(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		if k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}

	a, err := resolveActorFrom(
		ActorFlags{Kind: "human", Name: "ydnikolaev"},
		HarnessDefaults{}, ConfigActor{}, "yuranikolaev", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Kind != "human" || a.Name != "ydnikolaev" {
		t.Fatalf("got %+v, want the declared human identity — detection must not overwrite "+
			"a person's explicit claim about themselves", a)
	}
}

// TestOSUsernameNeverLandsUnderAnAgentClaimWhenDetectionFires closes the other
// half of the original defect. `kind: agent, name: yuranikolaev` was a person's
// OS login recorded under a claim that a machine acted; with a detector present
// that combination must be unreachable.
func TestOSUsernameNeverLandsUnderAnAgentClaimWhenDetectionFires(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		if k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}

	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "yuranikolaev", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Name == "yuranikolaev" {
		t.Fatal("the OS username was recorded as the actor while kind claims an agent — " +
			"exactly the `kind: agent, name: yuranikolaev` shape this change exists to end")
	}
}

// --- §7.4 order, when no detector answers --------------------------------
//
// Moved here from adapters_test.go (package cli_test) on 2026-08-07. Every
// case drives resolveActorFrom with an explicit environment, so the order the
// repo promises is asserted against a stated environment rather than against
// whatever agent harness is running the suite.

func TestResolveActorOrderExplicitFlagWins(t *testing.T) {
	t.Parallel()
	a, err := resolveActorFrom(
		ActorFlags{Kind: "human", Name: "flag-name"},
		HarnessDefaults{Kind: "agent", Name: "harness-name"},
		ConfigActor{Kind: "agent", Name: "config-name"},
		"os-user", noEnv)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Kind != "human" || a.Name != "flag-name" {
		t.Fatalf("got %+v, want explicit flag values to win", a)
	}
}

func TestResolveActorOrderEnvBeatsHarnessAndConfig(t *testing.T) {
	t.Parallel()
	env := func(k string) string {
		if k == envActorName {
			return "env-name"
		}
		return ""
	}
	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{Name: "harness-name"}, ConfigActor{Name: "config-name"}, "os-user", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Name != "env-name" {
		t.Fatalf("Name = %q, want env-name", a.Name)
	}
}

func TestResolveActorOrderHarnessBeatsConfig(t *testing.T) {
	t.Parallel()
	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{Name: "harness-name"}, ConfigActor{Name: "config-name"}, "os-user", noEnv)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Name != "harness-name" {
		t.Fatalf("Name = %q, want harness-name", a.Name)
	}
}

func TestResolveActorOrderConfigFallback(t *testing.T) {
	t.Parallel()
	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{Name: "config-name"}, "os-user", noEnv)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Name != "config-name" {
		t.Fatalf("Name = %q, want config-name", a.Name)
	}
}

func TestResolveActorDefaultsKindToAgent(t *testing.T) {
	t.Parallel()
	a, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "os-user", noEnv)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Kind != "agent" {
		t.Fatalf("Kind = %q, want agent (default when no source names one)", a.Kind)
	}
}

// TestANonAgentNameSurvivesDetection is the other side of the inversion, and
// the conformance harness is the reason it exists.
//
// The harness writes as `live-e2e` and then LOOKS THE OPERATION UP by that
// name. The first version of this rule overruled it with the detected agent,
// every lookup missed, and the logic tier ran past a 20-minute timeout instead
// of its usual ~5 minutes. A caller naming a fixture, a service or a person is
// not claiming to be a different agent, and detection has no business
// rewriting it.
func TestANonAgentNameSurvivesDetection(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		if k == "CLAUDECODE" {
			return "1"
		}
		return ""
	}

	a, err := resolveActorFrom(
		ActorFlags{Kind: "agent", Name: "live-e2e"},
		HarnessDefaults{}, ConfigActor{}, "yuranikolaev", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if a.Name != "live-e2e" {
		t.Fatalf("Name = %q, want live-e2e — an explicit name that is not a rival agent id must "+
			"survive detection, or every caller that writes under a fixture identity and reads it "+
			"back breaks", a.Name)
	}
}
