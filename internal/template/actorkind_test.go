package template

import (
	"strings"
	"testing"
	"time"
)

// TestFillActorKeepsATemplateDeclaredKind is P3's D3, and the decision
// template is its live counter-example: it declares
// `actor: {kind: human, name: <human-name>}` with a comment saying a decision
// typically carries a human actor because approving one is a G3 gate.
//
// fillActor overwrote that with "agent" on every render. The resolver's
// "agent" is a DEFAULT when no source named a kind, and a default must not
// silently contradict a template's own documented intent.
func TestFillActorKeepsATemplateDeclaredKind(t *testing.T) {
	t.Parallel()

	out, err := Render(Input{
		Type:    "decision",
		ID:      "XD-axon-20260721-k3f9",
		Created: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
		// Kind carries the resolver's default, and KindClaimed says nobody
		// chose it — exactly what `a2a new decision` produces with no flags.
		Actor: Actor{Kind: "agent", KindClaimed: false, Name: "somebody"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "kind: human") {
		t.Fatalf("the decision template's own `kind: human` was overwritten by an unclaimed default:\n%s", firstLines(string(out), 12))
	}
}

// TestFillActorAppliesAClaimedKind is the other direction: an explicit
// --actor-kind, or a detected agent, IS a claim and must win over the
// template's literal. Without this the fix would make the override
// unreachable, which is a worse defect than the one it repairs.
func TestFillActorAppliesAClaimedKind(t *testing.T) {
	t.Parallel()

	out, err := Render(Input{
		Type:    "decision",
		ID:      "XD-axon-20260721-k3f9",
		Created: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
		Actor:   Actor{Kind: "agent", KindClaimed: true, Name: "claude-code"},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "kind: agent") {
		t.Fatalf("a claimed kind did not reach the draft:\n%s", firstLines(string(out), 12))
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
