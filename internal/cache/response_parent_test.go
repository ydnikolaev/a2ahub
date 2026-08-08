package cache

import (
	"context"
	"slices"
	"testing"
	"time"
)

// findItemByID returns the Item named id, failing the test if none matches.
func findItemByID(t *testing.T, items []Item, id string) Item {
	t.Helper()
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("item %q not found; got ids=%v", id, itemIDs(items))
	return Item{}
}

// TestResponseParentReachesInboxAndOutbox pins the fix for this epic's live
// divergence: `a2a thread --json` and `a2a inbox`/`a2a outbox --json` used
// to disagree about whose move it is on a submitted response artifact,
// because Store.inbox and Store.outbox each resolved the response's
// pendency verdict with parentFrom="" — internal/pendency's
// {KindResponse, StateSubmitted} row (parentOwnerRow(fold.TVerify, ...))
// silently degrades to Owners=nil/Expected="" without a real parentFrom —
// while threadview.go's buildOpenItems already resolved the parent's own
// `from` correctly. One artifact, two answers.
//
// buildExchangeThread (threadview_test.go) is the reference fixture
// TestThreadView_ParentResolutionTrap already pins on the thread surface:
// axon asks a question, seomatrix acknowledges/accepts/responds. The
// response's parent (the question) is `from: axon`, so seomatrix's response
// names axon — not merely "someone" — as who owes the next move (`verify`).
// This test pins the SAME answer on the inbox surface (viewed as axon, the
// party addressed by the response) and the outbox surface (viewed as
// seomatrix, the response's own author), and asserts all three surfaces —
// inbox, outbox, thread — agree exactly.
func TestResponseParentReachesInboxAndOutbox(t *testing.T) {
	t.Parallel()
	fx, _, parentID, responseID := buildExchangeThread(t)

	// --- inbox, viewed as axon (the response's `to`) ---
	inboxStore := newThreadStore(t, fx, "sp1") // ownSystem "axon"
	inboxItems, err := inboxStore.InboxSnapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("InboxSnapshot: %v", err)
	}
	inboxItem := findItemByID(t, inboxItems, responseID)

	// An empty-vs-non-empty assertion would have passed while this bug was
	// live: pendency.Verdict{} is a legitimate, well-formed zero value on
	// the degraded path (Why is populated even then), so only naming the
	// specific system id — not merely "non-empty" — proves the parent was
	// actually resolved.
	if len(inboxItem.WaitingOn) != 1 || inboxItem.WaitingOn[0] != "axon" {
		t.Fatalf("inbox waiting_on = %v, want exactly [axon] (parent %s's own `from`)", inboxItem.WaitingOn, parentID)
	}
	if inboxItem.ExpectedTransition == "" {
		t.Fatal("inbox item carries no expected_transition")
	}
	if inboxItem.Why == "" {
		t.Fatal("inbox item carries no why")
	}

	// --- outbox, viewed as seomatrix (the response's own `from`) ---
	outboxStore := NewStore("seomatrix", t.TempDir(),
		[]SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		time.Now, 0)
	outboxItems, err := outboxStore.OutboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OutboxSnapshot: %v", err)
	}
	outboxItem := findItemByID(t, outboxItems, responseID)
	if len(outboxItem.WaitingOn) != 1 || outboxItem.WaitingOn[0] != "axon" {
		t.Fatalf("outbox waiting_on = %v, want exactly [axon] (parent %s's own `from`)", outboxItem.WaitingOn, parentID)
	}

	// --- thread, from each store — the reference answer, both sides must
	// equal it exactly (not merely agree with each other: crosssurface_test
	// .go's own "agreement is not correctness" — the literal [axon]
	// assertions above are what close that gap). ---
	inboxThreadItem := threadOpenItem(t, inboxStore, responseID)
	if inboxThreadItem.WaitingOn == nil || len(inboxThreadItem.WaitingOn) != 1 || inboxThreadItem.WaitingOn[0] != "axon" {
		t.Fatalf("thread (via inbox store) waiting_on = %v, want exactly [axon]", inboxThreadItem.WaitingOn)
	}

	if inboxItem.ExpectedTransition != inboxThreadItem.ExpectedTransition {
		t.Errorf("inbox expected_transition = %q, thread expected_transition = %q — one artifact, two answers",
			inboxItem.ExpectedTransition, inboxThreadItem.ExpectedTransition)
	}
	if inboxItem.Why != inboxThreadItem.Why {
		t.Errorf("inbox why = %q, thread why = %q — one artifact, two answers", inboxItem.Why, inboxThreadItem.Why)
	}
	if !slices.Equal(inboxItem.WaitingOn, inboxThreadItem.WaitingOn) {
		t.Errorf("inbox waiting_on = %v, thread waiting_on = %v", inboxItem.WaitingOn, inboxThreadItem.WaitingOn)
	}

	outboxThreadItem := threadOpenItem(t, outboxStore, responseID)
	if outboxItem.ExpectedTransition != outboxThreadItem.ExpectedTransition {
		t.Errorf("outbox expected_transition = %q, thread expected_transition = %q — one artifact, two answers",
			outboxItem.ExpectedTransition, outboxThreadItem.ExpectedTransition)
	}
	if outboxItem.Why != outboxThreadItem.Why {
		t.Errorf("outbox why = %q, thread why = %q — one artifact, two answers", outboxItem.Why, outboxThreadItem.Why)
	}
	if !slices.Equal(outboxItem.WaitingOn, outboxThreadItem.WaitingOn) {
		t.Errorf("outbox waiting_on = %v, thread waiting_on = %v", outboxItem.WaitingOn, outboxThreadItem.WaitingOn)
	}

	// The inbox and outbox halves must also agree with each other directly —
	// same artifact, same relation, two different systems asking.
	if inboxItem.ExpectedTransition != outboxItem.ExpectedTransition {
		t.Errorf("inbox expected_transition = %q, outbox expected_transition = %q", inboxItem.ExpectedTransition, outboxItem.ExpectedTransition)
	}
	if inboxItem.Why != outboxItem.Why {
		t.Errorf("inbox why = %q, outbox why = %q", inboxItem.Why, outboxItem.Why)
	}
}

// threadOpenItem resolves store's ThreadView for id and returns its own
// OpenItem, failing the test if either the view or the item is absent.
func threadOpenItem(t *testing.T, store *Store, id string) OpenItem {
	t.Helper()
	view, err := store.ThreadView(context.Background(), id, "")
	if err != nil {
		t.Fatalf("ThreadView(%s): %v", id, err)
	}
	for _, item := range view.OpenItems {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("no open item for %s in thread view: %+v", id, view.OpenItems)
	return OpenItem{}
}
