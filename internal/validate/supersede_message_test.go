package validate

import (
	"strings"
	"testing"
)

// TestSupersedeRefusalMessagesTeachTheRule pins WHAT the LFC-005/LFC-006
// wording must say, which is a different obligation from the one
// cmd/a2a/mcp_equivalence_test.go discharges.
//
// # Why this test exists, and the hole it was written to fill
//
// The equivalence suite compares the CLI's refusal to the MCP's for byte
// equality. That caught drift only while the two surfaces carried
// INDEPENDENT copies of the string. On 2026-08-27 ADR-019's move-down
// collapsed the three copies (here, internal/cli, internal/mcp) to these two
// exported constants — and the moment it did, the equivalence test stopped
// being able to see a wording change at all: both surfaces now read the same
// symbol, so they move together and agree no matter what it says.
//
// Verified, not assumed: mutating the constant to "MUTATED-WORDING" left
// internal/validate, internal/cli, internal/mcp and cmd/a2a ALL GREEN. A
// consolidation that removes a test's ability to fail, and replaces it with
// nothing, is exactly the defect class no-silent-yes-2026-08 exists to end —
// committed by that epic, to itself, in its own last hour.
//
// # What is actually being asserted
//
// Spec 06's discoverability instrument (§8): "the refusal's own message names
// what would make the supersede legal — an approved successor — so an agent
// that trips it learns the rule from the refusal itself." That is a claim
// about CONTENT, and until now nothing held it. These assertions are the
// claim, not a snapshot: they check the message names both §3.4.4 branches
// and cites the section, and they say nothing about phrasing around that, so
// a better sentence is still free to be written.
func TestSupersedeRefusalMessagesTeachTheRule(t *testing.T) {
	t.Parallel()

	t.Run("LFC-005 names both §3.4.4 branches and cites the section", func(t *testing.T) {
		t.Parallel()
		for _, want := range []string{
			"§3.4.4",
			"authored by the successor's own author",
			"approved successor",
		} {
			if !strings.Contains(DecisionSupersedePreconditionMessage, want) {
				t.Errorf("DecisionSupersedePreconditionMessage does not name %q — an agent that trips LFC-005\n"+
					"cannot learn what would make the supersede legal.\ngot: %q", want, DecisionSupersedePreconditionMessage)
			}
		}
	})

	t.Run("LFC-006 says UNEVALUATED, not failed, and that absence refuses", func(t *testing.T) {
		t.Parallel()
		// D9's whole content: UNMEASURED is "could not check", and on a row
		// whose old behaviour was a GRANT, absence must refuse. A message
		// that reads like an ordinary failure loses both halves.
		for _, want := range []string{
			"could not be resolved",
			"could not be evaluated",
			"refusing rather than silently granting",
		} {
			if !strings.Contains(DecisionSupersedeUnresolvedMessage, want) {
				t.Errorf("DecisionSupersedeUnresolvedMessage does not say %q — D9's UNMEASURED half must read as\n"+
					"'could not check', never as an ordinary failure.\ngot: %q", want, DecisionSupersedeUnresolvedMessage)
			}
		}
	})

	t.Run("the two violations carry the exported wording, not a private copy", func(t *testing.T) {
		t.Parallel()
		// The move-down is only real if the builders reference the constants.
		// A re-inlined literal here would restore the three-copy state while
		// every other test stayed green.
		if got := decisionSupersedePreconditionViolation("event[0]").Message; got != DecisionSupersedePreconditionMessage {
			t.Errorf("LFC-005 violation message diverged from the exported constant:\n got:  %q\n want: %q", got, DecisionSupersedePreconditionMessage)
		}
		if got := decisionSupersedeUnresolvedViolation("event[0]").Message; got != DecisionSupersedeUnresolvedMessage {
			t.Errorf("LFC-006 violation message diverged from the exported constant:\n got:  %q\n want: %q", got, DecisionSupersedeUnresolvedMessage)
		}
	})
}
