package validate

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// operationalClaimFrontmatter is a RECONSTRUCTION of XC-seomatrix-c4-export's
// own declared shape — docs/inbox/defects/09-three-contracts-one-word.md's
// worked example table, quoted verbatim for x_binding/x_operational. That
// artifact lives in the real getvisa space this checkout does not have
// cloned; only its declared field shapes are drawn from the inbox note's own
// quote, matching declaration_block_test.go's own reconstruction convention.
//
// runtimePinnable/endpointState/credentialState are substituted per test
// case so every scenario decodes through the SAME production-shaped path
// (internal/artifact.ParseFrontmatter -> decodeEnvelope, the exact two calls
// engine.go's runCommonEnvelope makes before any policy check runs) rather
// than a hand-built map[string]any — the leaf predicate is not what's on
// trial here, the decode boundary is.
func operationalClaimFrontmatter(runtimePinnable bool, endpointState, credentialState string) string {
	binding := "x_binding: none\n"
	if runtimePinnable || endpointState != "" || credentialState != "" {
		pinnableLiteral := "false"
		if runtimePinnable {
			pinnableLiteral = "true"
		}
		binding = "x_binding:\n" +
			"  artifact_class: producer_capability\n" +
			"  compatibility_status: additive\n" +
			"  adoptable: true\n" +
			"  runtime_pinnable: " + pinnableLiteral + "\n"
	}
	operational := ""
	if endpointState != "" || credentialState != "" {
		operational = "x_operational:\n"
		if endpointState != "" {
			operational += "  - name: endpoint\n    state: " + endpointState + "\n"
		}
		if credentialState != "" {
			operational += "  - name: credential-channel\n    state: " + credentialState + "\n"
		}
	}
	return "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-seomatrix-c4-export\n" +
		"type: contract\n" +
		"title: C4 export contract\n" +
		"space: getvisa\n" +
		"from: seomatrix\n" +
		"to: [axon]\n" +
		"actor: {kind: agent, name: seomatrix-publisher}\n" +
		"created: 2026-08-01T09:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"1.0.0\"\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: additive\n" +
		binding +
		operational +
		"---\n" +
		"C4 export capability, published ahead of the endpoint going live.\n"
}

// decodeOperationalClaimInstance runs the SAME two decode calls
// engine.go's runCommonEnvelope makes (ParseFrontmatter, then
// decodeEnvelope) before it ever reaches a policy check — the production
// entry point this wave's allowlist can reach, since internal/validate/
// engine.go itself is lead-reserved and cannot be edited to add the literal
// checkOperationalClaim(...) call this check will need there (see this
// wave's lead_actions_needed).
func decodeOperationalClaimInstance(t *testing.T, raw string) any {
	t.Helper()
	fm, err := artifact.ParseFrontmatter([]byte(raw))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	_, instance, err := decodeEnvelope(fm.YAML)
	if err != nil {
		t.Fatalf("decodeEnvelope: %v", err)
	}
	return instance
}

// TestCheckOperationalClaim_ConjunctionWarnsNamingBoth is AC4 (spec 08,
// defects-fix-2026-08): runtime_pinnable:true declared alongside a declared
// absent operational item warns, at SeverityWarning (ADR-011 D2's shape:
// the conjunction is a claim ahead of the facts, not an invalid document),
// naming both the pinnable claim and the absent name(s) it depends on.
func TestCheckOperationalClaim_ConjunctionWarnsNamingBoth(t *testing.T) {
	t.Parallel()
	instance := decodeOperationalClaimInstance(t, operationalClaimFrontmatter(true, "absent", "absent"))

	violations := checkOperationalClaim(instance)
	if len(violations) != 1 {
		t.Fatalf("violations = %+v, want exactly 1", violations)
	}
	v := violations[0]
	if v.Code != "POL-023" || v.Class != ClassPolicy || v.Severity != SeverityWarning {
		t.Fatalf("violation = %+v, want POL-023/policy/warning", v)
	}
	if !strings.Contains(v.Message, "runtime_pinnable") || !strings.Contains(v.Message, "endpoint") || !strings.Contains(v.Message, "credential-channel") || !strings.Contains(v.Message, "a2a contract activate") {
		t.Fatalf("message = %q, want it to name runtime_pinnable, both absent items, and a2a contract activate", v.Message)
	}
}

// TestCheckOperationalClaim_RuntimePinnableAloneDoesNotWarn is AC5's first
// half: runtime_pinnable:true with no x_operational declared at all (every
// well-known item's state is undeclared, never a wire "absent") is not a
// claim ahead of any fact.
func TestCheckOperationalClaim_RuntimePinnableAloneDoesNotWarn(t *testing.T) {
	t.Parallel()
	instance := decodeOperationalClaimInstance(t, operationalClaimFrontmatter(true, "", ""))
	if violations := checkOperationalClaim(instance); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none — no x_operational field was ever declared", violations)
	}
}

// TestCheckOperationalClaim_RuntimePinnableAloneWithReadyItemsDoesNotWarn
// covers the same half with x_operational declared and every item ready —
// still no absent item, still no claim ahead of the facts.
func TestCheckOperationalClaim_RuntimePinnableAloneWithReadyItemsDoesNotWarn(t *testing.T) {
	t.Parallel()
	instance := decodeOperationalClaimInstance(t, operationalClaimFrontmatter(true, "ready", "ready"))
	if violations := checkOperationalClaim(instance); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none — nothing declared absent", violations)
	}
}

// TestCheckOperationalClaim_AbsentAloneDoesNotWarn is AC5's second half: a
// declared absent operational item with runtime_pinnable false is not an
// invitation to pin anything, so there is nothing to warn about.
func TestCheckOperationalClaim_AbsentAloneDoesNotWarn(t *testing.T) {
	t.Parallel()
	instance := decodeOperationalClaimInstance(t, operationalClaimFrontmatter(false, "absent", "ready"))
	if violations := checkOperationalClaim(instance); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none — runtime_pinnable is false", violations)
	}
}

// TestCheckOperationalClaim_AdoptableAloneIsNotTheConjunction is the spec's
// own explicitly named edge case (testing requirements table: "adoptable:
// true alone is not the conjunction"). adoptable and runtime_pinnable are
// two DIFFERENT x_binding facts (schemas/envelope/v2/contract.schema.json);
// this check reads only runtime_pinnable, so an adoptable, non-pinnable
// contract with an absent item must stay silent.
func TestCheckOperationalClaim_AdoptableAloneIsNotTheConjunction(t *testing.T) {
	t.Parallel()
	// adoptable:true is baked into operationalClaimFrontmatter's x_binding
	// block whenever it is emitted at all; runtimePinnable=false here is
	// the fact under test.
	instance := decodeOperationalClaimInstance(t, operationalClaimFrontmatter(false, "absent", "absent"))
	if violations := checkOperationalClaim(instance); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none — adoptable:true alone is not the conjunction this rule reads", violations)
	}
}

// TestCheckOperationalClaim_NonMapInstanceIsInert guards the defensive
// decode: an instance this check cannot read as a document (never produced
// by the real decode path, but the function's own contract) degrades to no
// violations, matching every other checkFoo(instance any) in this package.
func TestCheckOperationalClaim_NonMapInstanceIsInert(t *testing.T) {
	t.Parallel()
	if violations := checkOperationalClaim([]any{"not", "a", "map"}); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none for a non-object instance", violations)
	}
}
