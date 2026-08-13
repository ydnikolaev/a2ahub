package cache

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/pendency"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestDashboardSnapshotResolvesEachArtifactOnce is the resistance test for
// dashboard item/thread/prompt composition. The cache owns the relation call;
// all projections must reuse the verdict already carried on the dashboard
// snapshot. A response with a missing parent exercises the degraded
// responseParentFrom branch and must still consume exactly one evaluation.
func TestDashboardSnapshotResolvesEachArtifactOnce(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID := "thread:seomatrix-20260813-orphan"
	responseID := "XS-seomatrix-20260813-orphan"
	created := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/"+responseID+".md",
		twResponse(responseID, "XW-axon-missing", "seomatrix", "axon", threadID, created), "body")
	fx.commitEvent("seomatrix", fxULID(900), evt(responseID, "submit", "seomatrix", created.Add(time.Minute)))

	store := newThreadStore(t, fx, "sp1")
	count := 0
	store.resolvePendency = func(fa foldedArtifact, ownSystem string, manifest space.Manifest, parentFrom string) pendency.Verdict {
		count++
		return resolveVerdict(fa, ownSystem, manifest, parentFrom)
	}

	snapshot, err := store.DashboardSnapshot(context.Background())
	if err != nil {
		t.Fatalf("DashboardSnapshot: %v", err)
	}
	if count != 1 {
		t.Fatalf("pendency evaluations = %d, want exactly 1 for the one artifact across item/thread/prompt projections", count)
	}
	if len(snapshot.Inbox) != 1 || snapshot.Inbox[0].ID != responseID {
		t.Fatalf("Inbox = %+v, want degraded response %s", snapshot.Inbox, responseID)
	}
	if len(snapshot.Inbox[0].WaitingOn) != 1 || snapshot.Inbox[0].WaitingOn[0] != "seomatrix" {
		t.Fatalf("degraded response WaitingOn = %v, want fallback owner [seomatrix]", snapshot.Inbox[0].WaitingOn)
	}
	if len(snapshot.Threads) != 1 || len(snapshot.Threads[0].OpenItems) != 1 {
		t.Fatalf("Threads = %+v, want one thread/open item", snapshot.Threads)
	}
	open := snapshot.Threads[0].OpenItems[0]
	if open.ID != responseID || open.Why != snapshot.Inbox[0].Why || open.ExpectedTransition != snapshot.Inbox[0].ExpectedTransition {
		t.Fatalf("thread projection did not reuse carried item verdict: item=%+v open=%+v", snapshot.Inbox[0], open)
	}
}

// TestDashboardSnapshotCrossThreadParentUsesDegradedOwner pins the anomalous
// REF-009 shape where a response names a real parent in the same space but
// carries a different thread. The rendered response thread cannot resolve that
// parent, so every item/open-item projection must take the same degraded branch
// and use the response author's identity. A whole-space parent lookup here
// would produce a WaitingOn owner the response thread's own NextActions do not
// admit.
func TestDashboardSnapshotCrossThreadParentUsesDegradedOwner(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)
	parentID := "XW-axon-20260814-cross-thread"
	responseID := "XS-seomatrix-20260814-cross-thread"
	parentThread := "thread:axon-20260814-parent"
	responseThread := "thread:seomatrix-20260814-response"

	fx.commitArtifact("axon/exchanges/"+parentID+".md",
		twWorkRequest(parentID, "cross-thread parent", "axon", []string{"seomatrix"}, parentThread, base), "parent body")
	fx.commitEvent("axon", fxULID(920), evt(parentID, "submit", "axon", base.Add(time.Minute)))
	fx.commitArtifact("seomatrix/exchanges/"+responseID+".md",
		twResponse(responseID, parentID, "seomatrix", "axon", responseThread, base.Add(2*time.Minute)), "response body")
	fx.commitEvent("seomatrix", fxULID(921), evt(responseID, "submit", "seomatrix", base.Add(3*time.Minute)))

	store := newThreadStore(t, fx, "sp1")
	count := 0
	store.resolvePendency = func(fa foldedArtifact, ownSystem string, manifest space.Manifest, parentFrom string) pendency.Verdict {
		count++
		return resolveVerdict(fa, ownSystem, manifest, parentFrom)
	}

	snapshot, err := store.DashboardSnapshot(context.Background())
	if err != nil {
		t.Fatalf("DashboardSnapshot: %v", err)
	}
	if count != 2 {
		t.Fatalf("pendency evaluations = %d, want exactly one for each of 2 artifacts", count)
	}

	responseItem := findItemByID(t, snapshot.Inbox, responseID)
	if len(responseItem.WaitingOn) != 1 || responseItem.WaitingOn[0] != "seomatrix" {
		t.Fatalf("dashboard response WaitingOn = %v, want degraded fallback owner [seomatrix]", responseItem.WaitingOn)
	}

	var responseThreadResult ThreadResult
	for _, thread := range snapshot.Threads {
		if thread.Thread == responseThread {
			responseThreadResult = thread
			break
		}
	}
	if responseThreadResult.Thread == "" {
		t.Fatalf("response thread %q missing from snapshot: %+v", responseThread, snapshot.Threads)
	}
	unresolvedParent := false
	for _, fact := range responseThreadResult.Unresolved {
		if fact.Kind == "parent" && fact.ID == responseID {
			unresolvedParent = true
			break
		}
	}
	if !unresolvedParent {
		t.Fatalf("response thread Unresolved = %+v, want parent fact for %s", responseThreadResult.Unresolved, responseID)
	}
	if len(responseThreadResult.OpenItems) != 1 || responseThreadResult.OpenItems[0].ID != responseID {
		t.Fatalf("response thread OpenItems = %+v, want %s", responseThreadResult.OpenItems, responseID)
	}
	open := responseThreadResult.OpenItems[0]
	if len(open.WaitingOn) != 1 || open.WaitingOn[0] != "seomatrix" {
		t.Fatalf("response thread WaitingOn = %v, want degraded fallback owner [seomatrix]", open.WaitingOn)
	}
	ownerAdmitted := false
	for _, action := range open.NextActions {
		if action.Transition == open.ExpectedTransition && containsString(action.By, open.WaitingOn[0]) {
			ownerAdmitted = true
			break
		}
	}
	if !ownerAdmitted {
		t.Fatalf("carried WaitingOn owner %q is not admitted by matching %q NextActions.By: %+v",
			open.WaitingOn[0], open.ExpectedTransition, open.NextActions)
	}

	inbox, err := store.OpenInboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenInboxSnapshot: %v", err)
	}
	legacyInboxResponse := findItemByID(t, inbox, responseID)
	if len(legacyInboxResponse.WaitingOn) != 1 || legacyInboxResponse.WaitingOn[0] != "seomatrix" {
		t.Fatalf("OpenInboxSnapshot response WaitingOn = %v, want degraded fallback owner [seomatrix]", legacyInboxResponse.WaitingOn)
	}

	outboxStore := NewStore("seomatrix", t.TempDir(), []SpaceMirror{{
		SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return base.Add(time.Hour) }, 0)
	outbox, err := outboxStore.OutboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OutboxSnapshot: %v", err)
	}
	legacyOutboxResponse := findItemByID(t, outbox, responseID)
	if len(legacyOutboxResponse.WaitingOn) != 1 || legacyOutboxResponse.WaitingOn[0] != "seomatrix" {
		t.Fatalf("OutboxSnapshot response WaitingOn = %v, want degraded fallback owner [seomatrix]", legacyOutboxResponse.WaitingOn)
	}
}

// TestDashboardSnapshotMatchesExistingExchangeSnapshots pins the replacement
// API to the three passive reads it supersedes. Besides membership, the whole
// Item comparison covers cursor newness, pending markers, carried pendency,
// semantic attention facts, and the intentional absence of those dashboard
// annotations from completed archive rows.
func TestDashboardSnapshotMatchesExistingExchangeSnapshots(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	inboxID := "XW-seomatrix-20260813-inbox"
	outboxID := "XW-axon-20260813-outbox"
	archiveID := "XA-seomatrix-20260813-archive"

	fx.commitArtifact("seomatrix/exchanges/"+inboxID+".md",
		twWorkRequest(inboxID, "incoming", "seomatrix", []string{"axon"}, "thread:incoming", base), "incoming body")
	fx.commitEvent("seomatrix", fxULID(910), evt(inboxID, "submit", "seomatrix", base.Add(time.Minute)))
	fx.commitArtifact("axon/exchanges/"+outboxID+".md",
		twWorkRequest(outboxID, "outgoing", "axon", []string{"seomatrix"}, "thread:outgoing", base.Add(2*time.Minute)), "outgoing body")
	fx.commitEvent("axon", fxULID(911), evt(outboxID, "submit", "axon", base.Add(3*time.Minute)))
	announcement := deprecationAnnouncement(archiveID, "seomatrix", "XC-seomatrix-unused@1.0.0", []string{"axon"})
	announcement["thread"] = "thread:archive"
	announcement["ack_requested"] = false
	fx.commitArtifact("seomatrix/announcements/"+archiveID+".md", announcement, "archive body")
	fx.commitEvent("seomatrix", fxULID(912), evt(archiveID, "publish", "seomatrix", base.Add(4*time.Minute)))

	cacheDir := t.TempDir()
	store := NewStore("axon", cacheDir, []SpaceMirror{{
		SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return base.Add(48 * time.Hour) }, 0)
	if err := WriteMarker(cacheDir, "sp1", PendingMarker{ArtifactID: inboxID, State: "pending-merge"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := saveCursor(store.cursorPath(), cursorSnapshot{Items: map[string]string{inboxID: "submitted"}}); err != nil {
		t.Fatalf("saveCursor: %v", err)
	}

	wantInbox, err := store.OpenInboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenInboxSnapshot: %v", err)
	}
	wantOutbox, err := store.OutboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OutboxSnapshot: %v", err)
	}
	wantArchive, err := store.ExchangeArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ExchangeArchiveSnapshot: %v", err)
	}

	got, err := store.DashboardSnapshot(context.Background())
	if err != nil {
		t.Fatalf("DashboardSnapshot: %v", err)
	}
	for _, tc := range []struct {
		name string
		got  []Item
		want []Item
	}{
		{name: "inbox", got: got.Inbox, want: wantInbox},
		{name: "outbox", got: got.Outbox, want: wantOutbox},
		{name: "archive", got: got.Archive, want: wantArchive},
	} {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s snapshot mismatch\n got: %#v\nwant: %#v", tc.name, tc.got, tc.want)
		}
	}
}
