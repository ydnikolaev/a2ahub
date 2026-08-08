package cache

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// exchangeSeed is one membership arrangement of a published announcement,
// stated as the facts both `exchangeActive` and internal/pendency read.
type exchangeSeed struct {
	name string
	// me is whose exchange feed is being asked about.
	me   string
	from string
	to   any
	// status is the manifest membership of every system that appears.
	status       map[string]string
	acks         map[string]bool
	ackRequested bool
	// registeredConsumer is P4 Edge 3: `me` consumes the contract this
	// announcement deprecates, so it is addressed even though the frozen
	// `to:` predates its adoption.
	registeredConsumer bool
	// wantActive is what the exchange feed must say. It is stated
	// independently of both implementations, from the domain question
	// "does anything still have to happen before this is delivered".
	wantActive bool
	// wantOwners is WHO the relation must name. A boolean cannot see the
	// membership half of AC5: a departed sole target and a live one both
	// leave SOMEBODY owing, so `wantActive` stays true either way while
	// the answer changes from "the sender owes a re-route decision" to
	// "a system that can no longer write owes an acknowledgement". Drop
	// LeftParticipants from the call site and only this field reds.
	wantOwners []string
	// why records the reasoning, so a future edit that flips an expectation
	// has to argue with a sentence rather than with a bare bool.
	why string
}

// exchangeSeeds sweeps the membership arrangements that separate a liveness
// answer from a delivery-complete one.
//
// The last seed is the one that was wrong. Every other seed is a pin: this
// phase changes exactly one answer, and a change to any other would be a
// regression dressed up as a fix.
var exchangeSeeds = []exchangeSeed{
	{
		name: "sole target left before acking", me: "axon", from: "axon",
		to: []string{"gammaco"}, status: map[string]string{"axon": "active", "gammaco": "left"},
		ackRequested: true, wantActive: true, wantOwners: []string{"axon"},
		why: "the acknowledge can never land, so the sender owes a cancel-or-re-route decision (CC-062) — that is a live obligation, not a delivered announcement",
	},
	{
		name: "one target left, the other acked", me: "axon", from: "axon",
		to:     []string{"gammaco", "betaco"},
		status: map[string]string{"axon": "active", "gammaco": "left", "betaco": "active"},
		acks:   map[string]bool{"betaco": true}, ackRequested: true, wantActive: true, wantOwners: []string{"axon"},
		why: "same transfer: the departed target's ack is unreachable and the sender still owes the decision",
	},
	{
		name: "one target left, the other still owes", me: "axon", from: "axon",
		to:           []string{"gammaco", "betaco"},
		status:       map[string]string{"axon": "active", "gammaco": "left", "betaco": "active"},
		ackRequested: true, wantActive: true, wantOwners: []string{"betaco"},
		why: "a reachable target has not acknowledged",
	},
	{
		name: "sole target acked, then left", me: "axon", from: "axon",
		to:     []string{"gammaco"},
		status: map[string]string{"axon": "active", "gammaco": "left"},
		acks:   map[string]bool{"gammaco": true}, ackRequested: true, wantActive: false, wantOwners: nil,
		why: "delivery completed while the target was still a member; leaving afterwards does not reopen it",
	},
	{
		name: "broadcast with a departed non-acker", me: "axon", from: "axon",
		to:           "all",
		status:       map[string]string{"axon": "active", "gammaco": "left", "betaco": "active"},
		ackRequested: true, wantActive: true, wantOwners: []string{"betaco"},
		why: "the broadcast expands to ACTIVE participants, and betaco among them has not acknowledged",
	},
	{
		name: "broadcast, every active participant acked", me: "axon", from: "axon",
		to:     "all",
		status: map[string]string{"axon": "active", "gammaco": "left", "betaco": "active"},
		acks:   map[string]bool{"betaco": true}, ackRequested: true, wantActive: false, wantOwners: nil,
		why: "the departed system is not expected to acknowledge, so delivery is complete",
	},
	{
		name: "late adopter, flagged", me: "seomatrix", from: "axon",
		to:     []string{"betaco"},
		status: map[string]string{"axon": "active", "betaco": "active", "seomatrix": "active"},
		acks:   map[string]bool{"betaco": true}, ackRequested: true, registeredConsumer: true,
		wantActive: true, wantOwners: []string{"seomatrix"},
		why: "P4 Edge 3: the registry says seomatrix consumes the deprecated contract, so it is addressed and owes an acknowledge",
	},
	{
		// THE DEFECT. `ack_requested` gates the `to:`-matched half ONLY
		// (internal/pendency's unackedTargets, and 03-domain.md:115 conditions
		// retire on "all registered consumers acked" with no flag qualifier).
		// pendency was fixed for exactly this; exchangeActive held a fourth
		// copy of the rule and returned early on the flag, archiving a
		// deprecation out of the very consumer's feed while `contract retire`
		// refused on that same consumer's missing ack.
		name: "late adopter, deprecation with no ack_requested", me: "seomatrix", from: "axon",
		to:     []string{"betaco"},
		status: map[string]string{"axon": "active", "betaco": "active", "seomatrix": "active"},
		acks:   map[string]bool{"betaco": true}, ackRequested: false, registeredConsumer: true,
		wantActive: true, wantOwners: []string{"seomatrix"},
		why: "the registry-matched half carries no ack_requested qualifier — the consumer owes an acknowledge and must be able to see the announcement",
	},
}

// equalStrings compares two owner sets as SETS: internal/pendency builds its
// answer by ranging a map in one branch, so ordering is not a promise it
// makes and pinning it here would make the test flake rather than the code
// wrong.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, s := range got {
		seen[s]++
	}
	for _, s := range want {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func (s exchangeSeed) artifact() (foldedArtifact, space.Manifest) {
	var ps []space.Participant
	for system, status := range s.status {
		ps = append(ps, space.Participant{System: system, Status: status})
	}
	acks := s.acks
	if acks == nil {
		acks = map[string]bool{}
	}
	return foldedArtifact{
		Env: envelopeProbe{
			Type: string(fold.KindAnnouncement), ID: "XA-axon-20260808-seed",
			From: s.from, To: s.to, Category: "deprecation", AckRequested: s.ackRequested,
		},
		Result:                 fold.Result{State: fold.StatePublished, Acks: acks},
		DeprecatesMyDependency: s.registeredConsumer,
	}, space.Manifest{Participants: ps}
}

// TestExchangeActiveMatchesTheRelation is P2's AC5.
//
// `exchangeActive` decides whether a document is still in flight for the
// current system. For every kind but a published announcement that is a pure
// state question. For a published announcement it is an OBLIGATION question —
// "does anybody still owe an acknowledgement" — and that question has exactly
// one home in this repo (internal/pendency, I7).
//
// It used to have two. The copy here reproduced the shape of a defect
// internal/pendency had already fixed, and reproduced it silently: both
// answers are booleans, both compile, and no test crossed them.
func TestExchangeActiveMatchesTheRelation(t *testing.T) {
	t.Parallel()

	for _, seed := range exchangeSeeds {
		t.Run(seed.name, func(t *testing.T) {
			t.Parallel()
			fa, manifest := seed.artifact()

			if got := exchangeActive(fa, seed.me, manifest); got != seed.wantActive {
				t.Errorf("exchangeActive(%s) = %v, want %v — %s", seed.me, got, seed.wantActive, seed.why)
			}

			// And the relation must agree, because it is the same question.
			// Asserting the stated answer alone would let both sides drift
			// together; asserting agreement alone would let both be wrong
			// at once, which is what P2's own amendment says about this
			// phase. Both assertions are here for that reason.
			_, verdict := actionableReasons(fa, seed.me, manifest)
			owed := containsString(verdict.Owners, seed.me)
			if ownedByMe(fa, seed.me) {
				owed = len(verdict.Owners) > 0
			}
			if owed != seed.wantActive {
				t.Errorf("the relation says owners=%v (expected %q) for %s, which reads as %v; the exchange feed wants %v — %s",
					verdict.Owners, verdict.Expected, seed.me, owed, seed.wantActive, seed.why)
			}

			// WHO, not merely whether. The boolean above is blind to the
			// membership half of AC5 — a departed sole target and a live
			// one both leave somebody owing — so the departed seeds are
			// pinned on the name the relation returns, which is what
			// changes when the call site stops handing over
			// LeftParticipants.
			if !equalStrings(verdict.Owners, seed.wantOwners) {
				t.Errorf("the relation names %v, want %v — %s", verdict.Owners, seed.wantOwners, seed.why)
			}
		})
	}
}

// TestExchangeSeedsCoverBothHalvesOfTheAckRule is the meta-assertion.
//
// The seed table's value is that it separates the `to:`-matched half of the
// acknowledgement rule from the registry-matched half — the two halves that
// carry DIFFERENT `ack_requested` qualifiers and whose collapse is the defect
// this test exists for. A table that lost the flagless registry seed would
// still pass everything above while watching nothing.
func TestExchangeSeedsCoverBothHalvesOfTheAckRule(t *testing.T) {
	t.Parallel()

	var flaglessRegistry, flaggedTargets, departed int
	for _, s := range exchangeSeeds {
		if s.registeredConsumer && !s.ackRequested {
			flaglessRegistry++
		}
		if s.ackRequested && !s.registeredConsumer {
			flaggedTargets++
		}
		for _, status := range s.status {
			if status == "left" {
				departed++
				break
			}
		}
	}
	if flaglessRegistry == 0 {
		t.Error("no seed carries a registered consumer with ack_requested false — the half of the rule that has no flag qualifier is unwatched, and collapsing the two halves is the exact defect")
	}
	if flaggedTargets == 0 {
		t.Error("no seed carries `to:`-matched targets with the flag set — the half the flag DOES gate is unwatched")
	}
	if departed == 0 {
		t.Error("no seed seeds a departed participant — AC5 names it as the membership fact both sides must read")
	}
}
