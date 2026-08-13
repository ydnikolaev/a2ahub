package cache

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// TestInboxActionable_FiveConditionsPlusControl is AC row 4: `a2a inbox
// --actionable` returns exactly the union of the 5 OP-207 conditions, no
// more, no fewer, over a fixture space with one item per condition plus
// one non-matching control item.
func TestInboxActionable_FiveConditionsPlusControl(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	// Condition 1: addressed to me (axon), no ack by me yet (submitted).
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-c1.md", wr("XW-seomatrix-20260701-c1", "cond1", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(100), evt("XW-seomatrix-20260701-c1", "submit", "seomatrix", base))

	// Condition 2: I (axon) am the requester; target responded, awaiting
	// my verify/close.
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-c2.md", wr("XW-axon-20260701-c2", "cond2", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", fxULID(101), evt("XW-axon-20260701-c2", "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(102), evt("XW-axon-20260701-c2", "acknowledge", "seomatrix", base.Add(time.Hour)))
	fx.commitEvent("seomatrix", fxULID(103), evt("XW-axon-20260701-c2", "respond", "seomatrix", base.Add(2*time.Hour)))

	// Condition 3: disputed toward me (axon is target; seomatrix, the
	// owner, disputes the response).
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-c3.md", wr("XW-seomatrix-20260701-c3", "cond3", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(110), evt("XW-seomatrix-20260701-c3", "submit", "seomatrix", base))
	fx.commitEvent("axon", fxULID(111), evt("XW-seomatrix-20260701-c3", "acknowledge", "axon", base.Add(time.Hour)))
	fx.commitArtifactAndEvent(
		"axon/exchanges/XS-axon-20260701-c3resp.md",
		responseFields("XS-axon-20260701-c3resp", "XW-seomatrix-20260701-c3", "axon", "seomatrix"),
		"resp body",
		"axon", fxULID(112),
		evt("XW-seomatrix-20260701-c3", "respond", "axon", base.Add(2*time.Hour)),
	)
	fx.commitEvent("seomatrix", fxULID(113), evt("XS-axon-20260701-c3resp", "dispute", "seomatrix", base.Add(3*time.Hour)))

	// Condition 4: p1, open state, I (axon) am the owner.
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-c4.md", wr("XW-axon-20260701-c4", "cond4", "axon", []string{"seomatrix"}, "p1", false), "body")
	fx.commitEvent("axon", fxULID(120), evt("XW-axon-20260701-c4", "submit", "axon", base))

	// Condition 5: gate pending on me — decision proposed, axon is a
	// required approver who has not yet approved.
	fx.commitArtifact("decisions/XD-axon-20260701-c5.md", decisionFields("XD-axon-20260701-c5", "axon", []string{"axon", "seomatrix"}), "body")
	fx.commitEvent("axon", fxULID(130), evt("XD-axon-20260701-c5", "propose", "axon", base))

	// Control: addressed to axon, already acknowledged — no condition
	// should match.
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-ctrl.md", wr("XW-seomatrix-20260701-ctrl", "control", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(140), evt("XW-seomatrix-20260701-ctrl", "submit", "seomatrix", base))
	fx.commitEvent("axon", fxULID(141), evt("XW-seomatrix-20260701-ctrl", "acknowledge", "axon", base.Add(time.Hour)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(24 * time.Hour) }, 0)

	items, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}

	got := map[string][]string{}
	for _, it := range items {
		got[it.ID] = it.Reasons
	}

	wantIDs := []string{
		"XW-seomatrix-20260701-c1", "XW-axon-20260701-c2", "XW-seomatrix-20260701-c3",
		"XW-axon-20260701-c4", "XD-axon-20260701-c5",
	}
	for _, id := range wantIDs {
		if _, ok := got[id]; !ok {
			t.Errorf("expected actionable item %q missing; got ids=%v", id, itemIDs(items))
		}
	}
	if _, ok := got["XW-seomatrix-20260701-ctrl"]; ok {
		t.Errorf("control item unexpectedly actionable: reasons=%v", got["XW-seomatrix-20260701-ctrl"])
	}
	if len(items) != len(wantIDs) {
		t.Errorf("len(items) = %d, want %d (got ids=%v)", len(items), len(wantIDs), itemIDs(items))
	}

	snapshot, err := store.InboxSnapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("InboxSnapshot: %v", err)
	}
	var gate Item
	for _, item := range snapshot {
		if item.ID == "XD-axon-20260701-c5" {
			gate = item
			break
		}
	}
	if gate.ID == "" || !gate.YourMove || !containsString(gate.Reasons, "gate-pending-on-me") {
		t.Fatalf("passive snapshot lost canonical gate/move annotations: %+v", gate)
	}
	if gate.HumanGate != "G3" {
		t.Fatalf("passive snapshot HumanGate = %q, want G3 from the same pendency verdict", gate.HumanGate)
	}
	if gate.RuleIdentity != "decision/proposed" {
		t.Fatalf("passive snapshot RuleIdentity = %q, want decision/proposed from the same pendency verdict", gate.RuleIdentity)
	}
}

func TestItemHumanGateJSONIsAdditiveAndOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	baseline := Item{
		Space: "sp1", ID: "XD-axon-20260701-c5", Type: "decision", Title: "review",
		From: "seomatrix", To: []string{"axon"}, State: "proposed", Priority: "p2",
		New: true, Reasons: []string{string(ReasonGatePendingOnMe)},
		WaitingOn: []string{"axon"}, ExpectedTransition: "approve", Why: "quorum rule",
		RuleIdentity: "decision/proposed",
	}
	withoutGate, err := json.Marshal(baseline)
	if err != nil {
		t.Fatalf("marshal empty gate: %v", err)
	}
	var before map[string]any
	if err := json.Unmarshal(withoutGate, &before); err != nil {
		t.Fatalf("decode empty gate JSON: %v", err)
	}
	if _, exists := before["human_gate"]; exists {
		t.Fatalf("empty HumanGate emitted human_gate: %s", withoutGate)
	}
	if _, exists := before["rule_identity"]; exists {
		t.Fatalf("dashboard-only RuleIdentity leaked into stable Item JSON: %s", withoutGate)
	}

	baseline.HumanGate = "G3"
	withGate, err := json.Marshal(baseline)
	if err != nil {
		t.Fatalf("marshal populated gate: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(withGate, &after); err != nil {
		t.Fatalf("decode populated gate JSON: %v", err)
	}
	if got := after["human_gate"]; got != "G3" {
		t.Fatalf("human_gate = %#v, want G3", got)
	}
	delete(after, "human_gate")
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("adding HumanGate changed pre-existing JSON keys/values:\nbefore=%v\nafter=%v", before, after)
	}
}

// TestInboxActionable_JSONRoundTrip is AC row 7: JSON output is stable
// and parses under the documented shape.
func TestInboxActionable_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-j1.md", wr("XW-seomatrix-20260701-j1", "j1", "seomatrix", []string{"axon"}, "p1", true), "body")
	fx.commitEvent("seomatrix", fxULID(200), evt("XW-seomatrix-20260701-j1", "submit", "seomatrix", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)
	items, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var round []Item
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if len(round) != len(items) || len(round) == 0 {
		t.Fatalf("round-trip length mismatch: got %d, want %d (>0)", len(round), len(items))
	}
	if round[0].ID != items[0].ID || round[0].State != items[0].State {
		t.Fatalf("round-trip content mismatch: %+v vs %+v", round[0], items[0])
	}
}

func TestOpenInboxSnapshotExcludesTerminalHistory(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-open.md", wr("XW-seomatrix-20260701-open", "open", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(700), evt("XW-seomatrix-20260701-open", "submit", "seomatrix", base))

	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-closed.md", wr("XW-seomatrix-20260701-closed", "closed", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(710), evt("XW-seomatrix-20260701-closed", "submit", "seomatrix", base))
	fx.commitEvent("seomatrix", fxULID(711), evt("XW-seomatrix-20260701-closed", "cancel", "seomatrix", base.Add(time.Hour)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(3 * time.Hour) }, 0)
	all, err := store.InboxSnapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("InboxSnapshot: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ordinary inbox len = %d, want terminal history retained (2)", len(all))
	}

	open, err := store.OpenInboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenInboxSnapshot: %v", err)
	}
	if got := itemIDs(open); len(got) != 1 || got[0] != "XW-seomatrix-20260701-open" {
		t.Fatalf("open inbox ids = %v, want only active request", got)
	}

	archive, err := store.ExchangeArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ExchangeArchiveSnapshot: %v", err)
	}
	if got := itemIDs(archive); len(got) != 1 || got[0] != "XW-seomatrix-20260701-closed" {
		t.Fatalf("archive ids = %v, want only terminal request", got)
	}
}

func TestExchangeSnapshotsArchiveAcknowledgedAnnouncement(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	id := "XA-axon-20260701-delivered"
	fields := map[string]any{
		"schema": "envelope/v1", "id": id, "type": "announcement",
		"title": "delivered announcement", "space": "fixture-space",
		"from": "axon", "to": []string{"seomatrix"},
		"actor":          map[string]any{"kind": "agent", "name": "axon-bot"},
		"created":        fxAt(base),
		"priority":       "p3",
		"blocking":       false,
		"ack_requested":  true,
		"classification": "internal",
		"thread":         id,
		"category":       "notice",
	}
	fx.commitArtifact("axon/announcements/"+id+".md", fields, "hello")
	fx.commitEvent("axon", fxULID(720), evt(id, "create", "axon", base))
	fx.commitEvent("axon", fxULID(721), evt(id, "publish", "axon", base.Add(time.Minute)))

	newStore := func(system string) *Store {
		return NewStore(system, t.TempDir(), []SpaceMirror{{
			SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
		}}, func() time.Time { return base.Add(time.Hour) }, 0)
	}

	ownerActive, err := newStore("axon").OutboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OutboxSnapshot before acknowledge: %v", err)
	}
	if !containsItemID(ownerActive, id) {
		t.Fatalf("published announcement disappeared before recipient acknowledgement: %v", itemIDs(ownerActive))
	}
	recipientActive, err := newStore("seomatrix").OpenInboxSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenInboxSnapshot before acknowledge: %v", err)
	}
	if !containsItemID(recipientActive, id) {
		t.Fatalf("recipient cannot see unacknowledged announcement: %v", itemIDs(recipientActive))
	}

	fx.commitEvent("seomatrix", fxULID(722), evt(id, "acknowledge", "seomatrix", base.Add(2*time.Minute)))
	for _, system := range []string{"axon", "seomatrix"} {
		store := newStore(system)
		var active []Item
		if system == "axon" {
			active, err = store.OutboxSnapshot(context.Background())
		} else {
			active, err = store.OpenInboxSnapshot(context.Background())
		}
		if err != nil {
			t.Fatalf("active exchange for %s after acknowledge: %v", system, err)
		}
		if containsItemID(active, id) {
			t.Fatalf("delivered announcement still active for %s: %v", system, itemIDs(active))
		}
		archive, archiveErr := store.ExchangeArchiveSnapshot(context.Background())
		if archiveErr != nil {
			t.Fatalf("ExchangeArchiveSnapshot for %s: %v", system, archiveErr)
		}
		if !containsItemID(archive, id) {
			t.Fatalf("delivered announcement missing from %s archive: %v", system, itemIDs(archive))
		}
	}
}

func TestExchangeSnapshotsArchiveAnnouncementWithoutRequestedAcknowledgement(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	id := "XA-axon-20260701-no-ack"
	fields := map[string]any{
		"schema": "envelope/v1", "id": id, "type": "announcement",
		"title": "informational announcement", "space": "fixture-space",
		"from": "axon", "to": []string{"seomatrix"},
		"actor":          map[string]any{"kind": "agent", "name": "axon-bot"},
		"created":        fxAt(base),
		"priority":       "p3",
		"blocking":       false,
		"ack_requested":  false,
		"classification": "internal",
		"thread":         id,
		"category":       "notice",
	}
	fx.commitArtifact("axon/announcements/"+id+".md", fields, "hello")
	fx.commitEvent("axon", fxULID(730), evt(id, "create", "axon", base))
	fx.commitEvent("axon", fxULID(731), evt(id, "publish", "axon", base.Add(time.Minute)))

	for _, system := range []string{"axon", "seomatrix"} {
		store := NewStore(system, t.TempDir(), []SpaceMirror{{
			SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
		}}, func() time.Time { return base.Add(time.Hour) }, 0)
		var active []Item
		var err error
		if system == "axon" {
			active, err = store.OutboxSnapshot(context.Background())
		} else {
			active, err = store.OpenInboxSnapshot(context.Background())
		}
		if err != nil {
			t.Fatalf("active exchange for %s: %v", system, err)
		}
		if containsItemID(active, id) {
			t.Fatalf("announcement without requested acknowledgement is active for %s: %v", system, itemIDs(active))
		}
		archive, err := store.ExchangeArchiveSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ExchangeArchiveSnapshot for %s: %v", system, err)
		}
		if !containsItemID(archive, id) {
			t.Fatalf("announcement without requested acknowledgement missing from %s archive: %v", system, itemIDs(archive))
		}
	}
}

// TestInboxActionable_Federation2Space is AC row 8: a system connected
// to 2 spaces sees one aggregated inbox, each item attributable to its
// origin space.
func TestInboxActionable_Federation2Space(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx1 := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	fx1.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-f1.md", wr("XW-seomatrix-20260701-f1", "f1", "seomatrix", []string{"axon"}, "p1", false), "body")
	fx1.commitEvent("seomatrix", fxULID(300), evt("XW-seomatrix-20260701-f1", "submit", "seomatrix", base))

	fx2 := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "getvisa"})
	fx2.commitArtifact("getvisa/exchanges/XW-getvisa-20260701-f2.md", wr("XW-getvisa-20260701-f2", "f2", "getvisa", []string{"axon"}, "p1", false), "body")
	fx2.commitEvent("getvisa", fxULID(301), evt("XW-getvisa-20260701-f2", "submit", "getvisa", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{
		{SpaceID: "space-one", Dir: fx1.dir, Manifest: mustManifest(t, fx1)},
		{SpaceID: "space-two", Dir: fx2.dir, Manifest: mustManifest(t, fx2)},
	}, func() time.Time { return base.Add(time.Hour) }, 0)

	items, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (got %v)", len(items), itemIDs(items))
	}
	bySpace := map[string]string{}
	for _, it := range items {
		bySpace[it.ID] = it.Space
	}
	if bySpace["XW-seomatrix-20260701-f1"] != "space-one" {
		t.Errorf("f1 space = %q, want space-one", bySpace["XW-seomatrix-20260701-f1"])
	}
	if bySpace["XW-getvisa-20260701-f2"] != "space-two" {
		t.Errorf("f2 space = %q, want space-two", bySpace["XW-getvisa-20260701-f2"])
	}
}

// TestInboxActionable_CursorPersistence is AC row 10: a second `a2a
// inbox` run against an unchanged cache does not re-flag already-read
// items as "new".
func TestInboxActionable_CursorPersistence(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/XW-seomatrix-20260701-p1.md", wr("XW-seomatrix-20260701-p1", "p1", "seomatrix", []string{"axon"}, "p1", false), "body")
	fx.commitEvent("seomatrix", fxULID(400), evt("XW-seomatrix-20260701-p1", "submit", "seomatrix", base))

	cacheDir := t.TempDir()
	store := NewStore("axon", cacheDir, []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)

	first, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("first Inbox: %v", err)
	}
	if len(first) != 1 || !first[0].New {
		t.Fatalf("first run: want 1 item marked new, got %+v", first)
	}

	second, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("second Inbox: %v", err)
	}
	newCount := 0
	for _, it := range second {
		if it.New {
			newCount++
		}
	}
	if newCount != 0 {
		t.Fatalf("second run: new count = %d, want 0 (items=%+v)", newCount, second)
	}
}

func itemIDs(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// --- shared fixture-field builders (used by inbox/outbox/statusline tests) ---

func wr(id, title, from string, to []string, priority string, blocking bool) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "work_request", "title": title,
		"space": "fixture-space", "from": from, "to": to,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(time.Now()), "priority": priority, "blocking": blocking,
		"classification": "internal",
	}
}

func responseFields(id, parent, from string, to string) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "response", "title": "response",
		"space": "fixture-space", "from": from, "to": []string{to}, "parent": parent, "result": "answered",
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(time.Now()), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

func decisionFields(id, from string, requiredApprovers []string) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "decision", "title": "decision",
		"space": "fixture-space", "from": from, "to": "all",
		"required_approvers": requiredApprovers, "context": "ctx", "options_considered": []string{"a", "b"},
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(time.Now()), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

func evt(subject, transition, actorSystem string, at time.Time) map[string]any {
	return map[string]any{
		"subject": subject, "transition": transition,
		"actor": map[string]any{"kind": "agent", "name": actorSystem + "-bot", "system": actorSystem},
		"at":    fxAt(at),
	}
}
