package cache

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestLifecycleProjectionOpenStates pins the cache projection of every state
// reachable by each artifact lifecycle. This is intentionally independent of
// fold's transition-table tests: fold proves which transition produces which
// state; cache must separately prove which of those durable states remains in
// an operational Exchange feed.
func TestLifecycleProjectionOpenStates(t *testing.T) {
	t.Parallel()
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "seomatrix", Status: "active"},
	}}

	tests := []struct {
		name string
		kind fold.Kind
		open []fold.State
		done []fold.State
	}{
		{
			name: "contract",
			kind: fold.KindContract,
			open: []fold.State{fold.StateDraft, fold.StatePublished, fold.StateDeprecated},
			done: []fold.State{fold.StateNone, fold.StateRetired},
		},
		{
			name: "requirement",
			kind: fold.KindRequirement,
			open: []fold.State{fold.StateDraft, fold.StatePublished, fold.StateAcknowledged},
			done: []fold.State{fold.StateNone, fold.StateSatisfied, fold.StateDeclined, fold.StateWithdrawn, fold.StateSuperseded},
		},
		{
			name: "question",
			kind: fold.KindQuestion,
			open: []fold.State{fold.StateDraft, fold.StateSubmitted, fold.StateAcknowledged, fold.StateAccepted, fold.StateInProgress, fold.StateBlocked, fold.StateResponded},
			done: []fold.State{fold.StateNone, fold.StateDeclined, fold.StateClosed, fold.StateCancelled, fold.StateSuperseded},
		},
		{
			name: "work request",
			kind: fold.KindWorkRequest,
			open: []fold.State{fold.StateDraft, fold.StateSubmitted, fold.StateAcknowledged, fold.StateAccepted, fold.StateInProgress, fold.StateBlocked, fold.StateResponded},
			done: []fold.State{fold.StateNone, fold.StateDeclined, fold.StateClosed, fold.StateCancelled, fold.StateSuperseded},
		},
		{
			name: "decision",
			kind: fold.KindDecision,
			open: []fold.State{fold.StateDraft, fold.StateProposed},
			done: []fold.State{fold.StateNone, fold.StateApproved, fold.StateRejected, fold.StateSuperseded},
		},
		{
			name: "handoff",
			kind: fold.KindHandoff,
			open: []fold.State{fold.StateDraft, fold.StateSubmitted, fold.StateAcknowledged},
			done: []fold.State{fold.StateNone, fold.StateAccepted, fold.StateRejected, fold.StateSuperseded},
		},
		{
			name: "response",
			kind: fold.KindResponse,
			open: []fold.State{fold.StateDraft, fold.StateSubmitted},
			done: []fold.State{fold.StateNone, fold.StateVerified, fold.StateDisputed},
		},
		{
			name: "announcement",
			kind: fold.KindAnnouncement,
			open: []fold.State{fold.StateDraft, fold.StatePublished},
			done: []fold.State{fold.StateNone, fold.StateSuperseded},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, state := range tt.open {
				if !isOpen(tt.kind, state) {
					t.Errorf("isOpen(%q, %q) = false, want true", tt.kind, state)
				}
				fa := foldedArtifact{
					Env: envelopeProbe{
						Type: string(tt.kind), From: "axon", To: []string{"seomatrix"},
						AckRequested: true,
					},
					Result: fold.Result{State: state, Acks: map[string]bool{}},
				}
				if !exchangeActive(fa, "axon", manifest) {
					t.Errorf("exchangeActive(%q, %q) = false, want true", tt.kind, state)
				}
			}
			for _, state := range tt.done {
				if isOpen(tt.kind, state) {
					t.Errorf("isOpen(%q, %q) = true, want false", tt.kind, state)
				}
				fa := foldedArtifact{
					Env:    envelopeProbe{Type: string(tt.kind), From: "axon", To: []string{"seomatrix"}},
					Result: fold.Result{State: state},
				}
				if exchangeActive(fa, "axon", manifest) {
					t.Errorf("exchangeActive(%q, %q) = true, want false", tt.kind, state)
				}
			}
		})
	}

	// A passed needed_by is an attention overlay, not a lifecycle transition:
	// responded stays operationally open until the requester explicitly closes
	// it. Keeping the date on the fixture guards against accidentally folding
	// overdue presentation into lifecycle membership later.
	overdue := foldedArtifact{
		Env:    envelopeProbe{Type: string(fold.KindWorkRequest), NeededBy: "2000-01-01"},
		Result: fold.Result{State: fold.StateResponded},
	}
	if !exchangeActive(overdue, "axon", space.Manifest{}) {
		t.Error("overdue responded work request is not active; needed_by must not close a lifecycle")
	}
}

func TestLifecycleProjectionVerifiedResponseArchivesBeforeParentClose(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	parentID := "XW-axon-20260801-verify"
	responseID := "XS-seomatrix-20260801-verify"

	fields := wr(parentID, "verify closure projection", "axon", []string{"seomatrix"}, "p2", false)
	fields["created"] = fxAt(base)
	fx.commitArtifact("axon/exchanges/"+parentID+".md", fields, "body")
	fx.commitEvent("axon", fxULID(810), evt(parentID, fold.TSubmit, "axon", base))
	fx.commitEvent("seomatrix", fxULID(811), evt(parentID, fold.TAcknowledge, "seomatrix", base.Add(time.Minute)))
	fx.commitEvent("seomatrix", fxULID(812), evt(parentID, fold.TAccept, "seomatrix", base.Add(2*time.Minute)))

	response := responseFields(responseID, parentID, "seomatrix", "axon")
	response["created"] = fxAt(base.Add(3 * time.Minute))
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/"+responseID+".md", response, "answer",
		"seomatrix", fxULID(813), evt(parentID, fold.TRespond, "seomatrix", base.Add(3*time.Minute)),
	)
	fx.commitEvent("axon", fxULID(814), evt(responseID, fold.TVerify, "axon", base.Add(4*time.Minute)))

	newStore := func() *Store {
		return NewStore("axon", t.TempDir(), []SpaceMirror{{
			SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
		}}, func() time.Time { return base.Add(time.Hour) }, 0)
	}

	store := newStore()
	active, err := store.OutboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OutboxSnapshot before close: %v", err)
	}
	if !containsItemID(active, parentID) {
		t.Fatalf("responded parent missing before explicit close: %v", itemIDs(active))
	}
	if containsItemID(active, responseID) {
		t.Fatalf("verified response remained active: %v", itemIDs(active))
	}

	archive, err := store.ExchangeArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ExchangeArchiveSnapshot before close: %v", err)
	}
	if !containsItemID(archive, responseID) {
		t.Fatalf("verified response missing from archive: %v", itemIDs(archive))
	}
	if containsItemID(archive, parentID) {
		t.Fatalf("responded parent archived before explicit close: %v", itemIDs(archive))
	}

	fx.commitEvent("axon", fxULID(815), evt(parentID, fold.TClose, "axon", base.Add(5*time.Minute)))
	store = newStore()
	active, err = store.OutboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OutboxSnapshot after close: %v", err)
	}
	if containsItemID(active, parentID) {
		t.Fatalf("closed parent remained active: %v", itemIDs(active))
	}
	archive, err = store.ExchangeArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ExchangeArchiveSnapshot after close: %v", err)
	}
	if !containsItemID(archive, parentID) {
		t.Fatalf("closed parent missing from archive: %v", itemIDs(archive))
	}
}

func TestLifecycleProjectionAnnouncementWaitingOnAcknowledgements(t *testing.T) {
	t.Parallel()

	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "seomatrix", Status: "active"},
	}}
	base := foldedArtifact{
		Env: envelopeProbe{
			ID: "XA-axon-20260801-lifecycle", Type: string(fold.KindAnnouncement),
			From: "axon", To: []string{"seomatrix"},
		},
		Result: fold.Result{State: fold.StatePublished, Acks: map[string]bool{}},
	}

	tests := []struct {
		name         string
		ackRequested bool
		acks         map[string]bool
		wantWaiting  []string
	}{
		{name: "publish completes delivery when no acknowledgement was requested", wantWaiting: []string{}},
		{name: "requested acknowledgement remains pending", ackRequested: true, wantWaiting: []string{"seomatrix"}},
		{name: "final acknowledgement settles the announcement", ackRequested: true, acks: map[string]bool{"seomatrix": true}, wantWaiting: []string{}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			announcement := base
			announcement.Env.AckRequested = tt.ackRequested
			announcement.Result.Acks = tt.acks
			items := buildOpenItems(
				[]foldedArtifact{announcement},
				map[string]foldedArtifact{announcement.Env.ID: announcement},
				manifest,
				"axon",
			)
			if len(items) != 1 {
				t.Fatalf("buildOpenItems len = %d, want durable published announcement retained", len(items))
			}
			if !slices.Equal(items[0].WaitingOn, tt.wantWaiting) {
				t.Fatalf("WaitingOn = %v, want %v", items[0].WaitingOn, tt.wantWaiting)
			}
		})
	}
}
