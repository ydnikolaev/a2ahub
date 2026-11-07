package cache

import (
	"context"
	"testing"
	"time"
)

// fb-20261106-28c7e6: a published contract with a registered consumer owes
// `contract activate` — the pendency verdict says so (expected_transition
// activate, waiting_on the author) — but activate is transition-free, so
// fold.LegalNextFor has no table row for it and neither next-action list
// offered the very move the verdict names. Both builders now append it from
// the same verdict, the broadcast-acknowledge shape beside it.
func TestNextActionsOfferOwedContractActivation(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"
	threadID := "thread:axon-20260801-widget"

	fields := odContractFields(contractID, "axon", nil, base)
	fields["thread"] = threadID
	fx.commitArtifact("axon/provides/widget/contract.md", fields, "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)
	now := base.Add(2 * time.Hour)

	assertActivate := func(t *testing.T, surface string, actions []NextAction) {
		t.Helper()
		for _, a := range actions {
			if a.Transition == "activate" {
				if len(a.By) != 1 || a.By[0] != "axon" {
					t.Fatalf("%s: activate.by = %v, want [axon]", surface, a.By)
				}
				return
			}
		}
		t.Fatalf("%s: next actions %+v offer no activate, though the verdict owes it", surface, actions)
	}

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		func() time.Time { return now }, time.Hour)
	view, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	var found bool
	for _, item := range view.OpenItems {
		if item.ID == contractID {
			found = true
			if len(item.WaitingOn) != 1 || item.WaitingOn[0] != "axon" {
				t.Fatalf("thread view waiting_on = %v, want [axon]: the fixture does not owe activate", item.WaitingOn)
			}
			assertActivate(t, "thread view", item.NextActions)
		}
	}
	if !found {
		t.Fatalf("thread view has no open item %s: %+v", contractID, view.OpenItems)
	}

	idx, _, err := BuildNotifyIndex(context.Background(), "sp1", fx.dir, mustManifest(t, fx), now)
	if err != nil {
		t.Fatalf("BuildNotifyIndex: %v", err)
	}
	found = false
	for _, a := range idx {
		if a.ID == contractID {
			found = true
			if a.Verdict.Expected != "activate" {
				t.Fatalf("notify index verdict expected = %q, want activate", a.Verdict.Expected)
			}
			assertActivate(t, "notify index", a.Actions)
		}
	}
	if !found {
		t.Fatalf("notify index has no %s", contractID)
	}
}
