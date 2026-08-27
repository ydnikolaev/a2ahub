package validate

import "testing"

// classificationResolver is this file's own Resolver fake. It optionally
// implements ActiveParticipantLister (classification.go's consumer-side
// optional upgrade) — capable=false leaves it satisfying ONLY Resolver,
// exercising the capability-miss branch exactly the way a real, unwired
// production Resolver does today (no concrete Resolver in this tree
// implements ActiveParticipantLister yet — see this stage's own Deviations
// report).
type classificationResolver struct {
	capable  bool
	active   []string
	resolved bool
	// asked records whether ActiveParticipants was ever called — used to
	// prove the non-restricted short-circuit never probes the resolver at
	// all (a non-capable-typed embedding would already do that at compile
	// time; this also proves it for a CAPABLE resolver that must not be
	// asked when the rule does not apply).
	asked bool
}

func (r *classificationResolver) KnownArtifact(string) bool         { return true }
func (r *classificationResolver) Digest(string) (string, bool)      { return "", false }
func (r *classificationResolver) System(string) (member, left bool) { return true, false }
func (r *classificationResolver) ActiveParticipants() ([]string, bool) {
	r.asked = true
	return r.active, r.resolved
}

// classificationIncapableResolver satisfies ONLY Resolver — no
// ActiveParticipants method at all, so the type assertion in
// checkClassificationBilateral fails exactly the way a real,
// capability-missing concrete Resolver would.
type classificationIncapableResolver struct{}

func (classificationIncapableResolver) KnownArtifact(string) bool         { return true }
func (classificationIncapableResolver) Digest(string) (string, bool)      { return "", false }
func (classificationIncapableResolver) System(string) (member, left bool) { return true, false }

var _ Resolver = (*classificationResolver)(nil)
var _ ActiveParticipantLister = (*classificationResolver)(nil)
var _ Resolver = classificationIncapableResolver{}

// TestCheckClassificationBilateral is spec 03 §8 AC 4/5/13 (D3/D9): a
// `restricted` artifact refuses when the space's ACTIVE participants
// exceed {from} ∪ to, an EXACT bilateral match is legal, and a capability
// miss refuses with reject + unmeasured rather than passing silently.
func TestCheckClassificationBilateral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		classification string
		from           string
		to             any
		resolver       Resolver
		wantCodes      []string
	}{
		{
			name:           "non-restricted classification never probes the resolver",
			classification: "internal",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix", "getvisa"}},
		},
		{
			name:           "empty classification (schema default 'internal') never probes the resolver",
			classification: "",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix", "getvisa"}},
		},
		{
			name:           "exactly bilateral: from+to equals the active set — legal",
			classification: "restricted",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix"}},
		},
		{
			name:           "active participants exceed {from} u to — refused",
			classification: "restricted",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix", "getvisa"}},
			wantCodes:      []string{"POL-024"},
		},
		{
			name:           "a LEFT participant is not in ActiveParticipants at all — not counted, legal",
			classification: "restricted",
			from:           "axon",
			to:             []any{"seomatrix"},
			// The caller-resolved fact already excludes LEFT systems
			// (pendency.Input.ActiveParticipants' own doc: "ACTIVE
			// manifest participant systems"), so a third, departed
			// system never appears here at all.
			resolver: &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix"}},
		},
		{
			name:           "broadcast (to: all) is read as the full active set — never exceeded",
			classification: "restricted",
			from:           "axon",
			to:             "all",
			resolver:       &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix", "getvisa"}},
		},
		{
			name:           "capability miss (Resolver has no ActiveParticipantLister at all) — POL-024 (the ordinary reject, reused) + POL-026 (unmeasured)",
			classification: "restricted",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       classificationIncapableResolver{},
			wantCodes:      []string{"POL-024", "POL-026"},
		},
		{
			name:           "capability present but resolver reports 'cannot enumerate' (ok=false) — POL-024 + POL-026",
			classification: "restricted",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       &classificationResolver{capable: true, resolved: false},
			wantCodes:      []string{"POL-024", "POL-026"},
		},
		{
			name:           "nil resolver is a capability miss too, not a panic",
			classification: "restricted",
			from:           "axon",
			to:             []any{"seomatrix"},
			resolver:       nil,
			wantCodes:      []string{"POL-024", "POL-026"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := envelope{Classification: test.classification, From: test.from, To: test.to}
			got := checkClassificationBilateral(env, test.resolver)

			if len(got) != len(test.wantCodes) {
				t.Fatalf("checkClassificationBilateral() = %+v, want %d violation(s) (%v)", got, len(test.wantCodes), test.wantCodes)
			}
			for i, code := range test.wantCodes {
				if got[i].Code != code {
					t.Fatalf("violation[%d].Code = %q, want %q (got %+v)", i, got[i].Code, code, got)
				}
				if got[i].Class != ClassPolicy {
					t.Fatalf("violation[%d].Class = %q, want %q", i, got[i].Class, ClassPolicy)
				}
			}
			if r, ok := test.resolver.(*classificationResolver); ok && test.classification != "restricted" && test.classification != "" {
				// The empty-classification case legitimately never
				// probes either (schema default is "internal"), so
				// only non-restricted/non-empty is asserted here —
				// belt-and-suspenders on top of the mutation below.
				if r.asked {
					t.Fatalf("non-restricted classification probed the resolver, want no call at all")
				}
			}
		})
	}
}

// TestCheckClassificationBilateral_SeverityPairing pins D9's own
// requirement literally: the reject half of a capability miss is what
// flips Result.Valid, the unmeasured half never does on its own — read
// straight off the registered Severity, not inferred from ordering. Since
// wave 1's POL-025 was folded back into POL-024 (D9's own "an ordinary
// reject" — the rule's own code, reused, never a second reject code minted
// just for the capability-miss branch), the reject half here names the
// SAME code a genuine audience-exceeds finding does.
func TestCheckClassificationBilateral_SeverityPairing(t *testing.T) {
	t.Parallel()
	env := envelope{Classification: "restricted", From: "axon", To: []any{"seomatrix"}}
	got := checkClassificationBilateral(env, classificationIncapableResolver{})
	if len(got) != 2 {
		t.Fatalf("got %d violations, want 2: %+v", len(got), got)
	}
	reject, unmeasured := got[0], got[1]
	if reject.Code != "POL-024" || reject.Severity != SeverityReject {
		t.Fatalf("first violation = %+v, want Code=POL-024 Severity=reject", reject)
	}
	if unmeasured.Code != "POL-026" || unmeasured.Severity != SeverityUnmeasured {
		t.Fatalf("second violation = %+v, want Code=POL-026 Severity=unmeasured", unmeasured)
	}
}

// TestValidateForSubmit_ClassificationBilateral is the engine-level twin:
// AC 4's own wording ("refused") means Result.Valid, not just a violation
// slice — proven through the real ValidateForSubmit entry point, the
// same one AC 13's capability-miss row names.
func TestValidateForSubmit_ClassificationBilateral(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	const restrictedBody = "---\n" + `
schema: envelope/v1
id: XW-axon-20260731-p9d4
type: work_request
title: A restricted work request
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without normalization."
acceptance_criteria:
  - "Every code exists in the registry."
classification: restricted
` + "---\nBody text.\n"

	draft := Draft{Path: "axon/exchanges/XW-axon-20260731-p9d4.md", Raw: []byte(restrictedBody)}

	t.Run("active participants exceed {from} u to: Result.Valid is false, names POL-024", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateForSubmit(draft, nil, LocalContext{
			OwnSystem: "axon",
			Resolver:  &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix", "getvisa"}},
		})
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if result.Valid {
			t.Fatal("expected Result.Valid=false for a restricted artifact exceeding bilateral, got true")
		}
		if !hasCode(result.Violations, "POL-024") {
			t.Fatalf("expected POL-024 among violations, got %+v", result.Violations)
		}
	})

	t.Run("exactly bilateral: no POL-024", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateForSubmit(draft, nil, LocalContext{
			OwnSystem: "axon",
			Resolver:  &classificationResolver{capable: true, resolved: true, active: []string{"axon", "seomatrix"}},
		})
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if hasCode(result.Violations, "POL-024") {
			t.Fatalf("expected no POL-024 for an exactly-bilateral space, got %+v", result.Violations)
		}
	})

	t.Run("capability miss: Result.Valid is false, names POL-024 and POL-026, an unmeasured violation alone never blocks", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateForSubmit(draft, nil, LocalContext{
			OwnSystem: "axon",
			Resolver:  classificationIncapableResolver{},
		})
		if err != nil {
			t.Fatalf("ValidateForSubmit: %v", err)
		}
		if result.Valid {
			t.Fatal("expected Result.Valid=false for an unresolvable participant list, got true")
		}
		if !hasCode(result.Violations, "POL-024") {
			t.Fatalf("expected POL-024 among violations, got %+v", result.Violations)
		}
		if !hasCode(result.Violations, "POL-026") {
			t.Fatalf("expected POL-026 among violations, got %+v", result.Violations)
		}
	})
}
