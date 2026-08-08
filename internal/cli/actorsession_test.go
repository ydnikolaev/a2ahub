package cli

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/agentid"
)

// TestResolvedActorCarriesTheDetectedSession is P3's wave C stated as the
// fact it restores. agentid.Detect resolves a session id; before this phase
// template.Actor had no field for it, fillActor had no case for it, and every
// event writer's actor struct lacked it — so the value was detected and then
// discarded, while internal/validate's POL-016 bound-checked a field no
// first-party writer could produce.
func TestResolvedActorCarriesTheDetectedSession(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		switch k {
		case "CLAUDECODE":
			return "1"
		case "CLAUDE_CODE_SESSION_ID":
			return "session:detected-abc"
		}
		return ""
	}
	if _, ok := agentid.Detect(env); !ok {
		t.Skip("detection did not fire for this fixture environment")
	}

	got, err := resolveActorFrom(ActorFlags{}, HarnessDefaults{}, ConfigActor{}, "osuser", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if got.Session != "session:detected-abc" {
		t.Fatalf("Session = %q, want the detected session — a detected value that never reaches the actor is the defect this wave closes", got.Session)
	}
}

// TestExplicitSessionOutranksDetection pins the override order: an explicit
// value is a caller adding information, not contradicting the environment.
func TestExplicitSessionOutranksDetection(t *testing.T) {
	t.Parallel()

	env := func(k string) string {
		switch k {
		case "CLAUDECODE":
			return "1"
		case "CLAUDE_CODE_SESSION_ID":
			return "session:detected"
		}
		return ""
	}
	got, err := resolveActorFrom(ActorFlags{Session: "session:explicit"}, HarnessDefaults{}, ConfigActor{}, "osuser", env)
	if err != nil {
		t.Fatalf("resolveActorFrom: %v", err)
	}
	if got.Session != "session:explicit" {
		t.Fatalf("Session = %q, want the explicit override to win", got.Session)
	}
}
