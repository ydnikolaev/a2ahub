package cli

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// TestCIBaseLegalityChecker_ContractActivateFromPublished is the land half
// of fb-20261106-28c7e6: a published contract with operational debt owes
// `activate` (pendency's contract/published row), and the merge-time check
// refused every activation LFC-001 because fold's pre-write primitive had
// no branch for the transition-free activate. The test drives the land's
// own seam, ciBaseLegalityChecker, over a merge-base history of two
// publishes — the reporter's shape — with no git and no filesystem: the
// non-response path reads neither.
func TestCIBaseLegalityChecker_ContractActivateFromPublished(t *testing.T) {
	t.Parallel()

	const contractID = "XC-axon-widget"
	manifest := func(producer fold.MembershipStatus) space.Manifest {
		return space.Manifest{Participants: []space.Participant{
			{System: "axon", Status: producer},
			{System: "seomatrix", Status: fold.MembershipMember},
		}}
	}
	actor := func(system string) lifecycleEventActor {
		return lifecycleEventActor{Kind: "agent", Name: "bot", System: system}
	}
	base := []lifecycleEventDoc{
		{Event: "01J40A7M9P1S3V5W7Y9A1C3F01", Subject: contractID, Transition: fold.TPublish, Version: "1.0.0", Actor: actor("axon")},
		{Event: "01J40A7M9P1S3V5W7Y9A1C3F02", Subject: contractID, Transition: fold.TPublish, Version: "1.0.1", Actor: actor("axon")},
	}

	cases := []struct {
		name     string
		system   string
		producer fold.MembershipStatus
		want     validate.Verdict
	}{
		{name: "producer_activates_its_published_contract", system: "axon", producer: fold.MembershipMember, want: validate.VerdictLegal},
		{name: "consumer_may_not_activate_the_producers_contract", system: "seomatrix", producer: fold.MembershipMember, want: validate.VerdictUnauthorizedActor},
		{name: "departed_producer_may_not_activate", system: "axon", producer: fold.MembershipLeft, want: validate.VerdictUnauthorizedActor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checker := ciBaseLegalityChecker{manifest: manifest(tc.producer), baseEvents: base}
			got, err := checker.CheckLegality(validate.CandidateEvent{
				Subject:    contractID,
				Transition: fold.TActivate,
				Version:    "1.0.1",
				Actor:      validate.Actor{Kind: "agent", Name: "bot", System: tc.system},
				Envelope:   validate.Envelope{ID: contractID, Kind: string(fold.KindContract), From: "axon", To: []string{"seomatrix"}},
			})
			if err != nil {
				t.Fatalf("CheckLegality: %v", err)
			}
			if got != tc.want {
				t.Fatalf("land verdict for activate by %s = %v, want %v", tc.system, got, tc.want)
			}
		})
	}
}
