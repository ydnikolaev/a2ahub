package pendency

// observed_consumption_test.go covers Input.ObservedConsumptionUndeclared —
// spec 03-observed-consumption.md §7's ONE new caller-resolved bool, in the
// shape of DeliveryUnresolvable: computed in internal/cache, threaded
// through Input, consumed BY NAME in exactly ONE table row (announcement/
// published).
//
// The bool changes the RATIONALE only. WHO owes rides the carrier that
// already exists for a registry-derived addressee (ExtraAddressees, P4 Edge
// 3), because this package cannot itself discover a system it was never
// handed — and minting a second carrier for the same kind of fact is what
// §5's anti-duplication bullet forbids.

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// observedAnnouncementInput is the (announcement, published) shape both
// tests below start from: a deprecation announcement seomatrix published,
// with no ack_requested flag (a deprecation's registry-matched half is
// deliberately not gated on it — unackedTargets' own doc comment, §18e/J1).
func observedAnnouncementInput() Input {
	return Input{
		Kind:  fold.KindAnnouncement,
		State: fold.StatePublished,
		From:  "seomatrix",
		To:    []string{"beta"},
	}
}

// TestObservedConsumptionNamesBothExitsInTheWhy is §8 criterion 3 (US-2):
// a consumer holding verify-passed deliveries pinning a contract it never
// declared, facing a live deprecation announcement for that contract, gets
// a computed next move that names BOTH exits — declare (`contract adopt`)
// or acknowledge — and no new transition (US-4, §8 criterion 5).
//
// TEETH: delete the ObservedConsumptionUndeclared branch from
// unackedTargetsRow's whyFor and this reds; the Owners half stays green,
// which is exactly why the Why is asserted separately.
func TestObservedConsumptionNamesBothExitsInTheWhy(t *testing.T) {
	t.Parallel()

	in := observedAnnouncementInput()
	in.ExtraAddressees = []string{"axon"}
	in.ObservedConsumptionUndeclared = true

	v, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(v.Owners) != 1 || v.Owners[0] != "axon" {
		t.Fatalf("Owners = %v, want [axon] — the observed consumer is the one owed a move", v.Owners)
	}
	if v.Expected != fold.TAcknowledge {
		t.Fatalf("Expected = %q, want %q — US-4: both exits use the vocabulary that already exists, no verb is minted", v.Expected, fold.TAcknowledge)
	}
	if !strings.Contains(v.Why, "adopt") {
		t.Fatalf("Why = %q — it must name the DECLARE exit (`a2a contract adopt`), not only the ack", v.Why)
	}
	if !strings.Contains(v.Why, "acknowledg") {
		t.Fatalf("Why = %q — it must name the ACKNOWLEDGE exit too (US-4: \"I saw it and I do not depend on this\")", v.Why)
	}
}

// TestObservedConsumptionIsInertWithoutTheFact is §8 criterion 4 (US-5),
// the narrow trigger: with the bool false, the SAME input resolves to
// byte-identical Owners, Expected and Why. The 2026-08-10 regression — a
// broad trigger reddening five declared paths — is not repeated by a fact
// that only ever fires where the caller established it.
func TestObservedConsumptionIsInertWithoutTheFact(t *testing.T) {
	t.Parallel()

	in := observedAnnouncementInput()
	in.ExtraAddressees = []string{"axon"}

	plain, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	in.ObservedConsumptionUndeclared = true
	observed, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if plain.Why == observed.Why {
		t.Fatalf("the Why is identical with and without the fact (%q) — the row does not read the bool at all", plain.Why)
	}
	if strings.Contains(plain.Why, "adopt") {
		t.Fatalf("the plain late-adopter Why = %q already names `adopt` — a system that DECLARED the dependency has only one exit left, and telling it to declare again is wrong", plain.Why)
	}
	if plain.Expected != observed.Expected {
		t.Fatalf("Expected differs: plain=%q observed=%q — the fact must not change WHICH transition is owed", plain.Expected, observed.Expected)
	}
}

// TestObservedConsumptionTouchesNoOtherRow pins §7's "consumed by name in
// ONE row": every other (kind, state) pair resolves identically with the
// bool set and unset. A fact that leaked into a second row would be a
// second trigger nobody declared.
func TestObservedConsumptionTouchesNoOtherRow(t *testing.T) {
	t.Parallel()

	pairs := fold.RestingStates()
	if len(pairs) == 0 {
		t.Fatal("fold.RestingStates() returned no pairs — the universe is empty and this gate would check nothing")
	}
	for _, p := range pairs {
		if p.Kind == fold.KindAnnouncement && p.State == fold.StatePublished {
			continue // the one row that is SUPPOSED to read it
		}
		base := Input{Kind: p.Kind, State: p.State, From: "sys-a", To: []string{"sys-b"}, ExtraAddressees: []string{"axon"}}
		plain, err := Resolve(base)
		if err != nil {
			t.Errorf("Resolve(%s/%s): %v", p.Kind, p.State, err)
			continue
		}
		base.ObservedConsumptionUndeclared = true
		observed, err := Resolve(base)
		if err != nil {
			t.Errorf("Resolve(%s/%s) with the fact: %v", p.Kind, p.State, err)
			continue
		}
		if plain.Why != observed.Why || plain.Expected != observed.Expected {
			t.Errorf("%s/%s changed on ObservedConsumptionUndeclared: Why %q -> %q, Expected %q -> %q",
				p.Kind, p.State, plain.Why, observed.Why, plain.Expected, observed.Expected)
		}
	}
}
