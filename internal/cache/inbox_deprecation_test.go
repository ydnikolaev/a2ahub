package cache

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// deprecationAnnouncement builds the envelope `contract deprecate` writes
// on both surfaces — the shape that matters here is `deprecates`, the
// machine-readable `<XC-id>@<version>` field, a `to:` that names someone
// else, and `ack_requested: true` — `contract deprecate` sets it on every
// announcement it emits (internal/mcp/tools_contract.go:426, pinned by
// internal/cli/cmd_contract_test.go:287), and P11 W1 makes
// `--actionable`'s condition 1 read that flag through internal/pendency,
// so a fixture omitting it no longer matches production shape.
func deprecationAnnouncement(id, from, deprecates string, to []string) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "announcement",
		"title": "deprecating " + deprecates, "space": "fixture-space",
		"from": from, "to": to,
		"actor":          map[string]any{"kind": "agent", "name": "bot"},
		"created":        "2026-07-01T08:00:00Z",
		"priority":       "p2",
		"blocking":       false,
		"ack_requested":  true,
		"classification": "internal",
		"thread":         id,
		"category":       "deprecation",
		"deprecates":     deprecates,
	}
}

// fxWriteConsumes writes a consumes/v1 registry for system into the
// fixture's working tree and commits it.
func fxWriteConsumes(t *testing.T, fx *fixtureSpace, system, contractID string, major int) {
	t.Helper()
	content := "schema: consumes/v1\nsystem: " + system + "\ndependencies:\n" +
		"  - contract: " + contractID + "\n    major: " + itoa(major) + "\n    since: \"2026-01-01\"\n"
	full := filepath.Join(fx.dir, system, "consumes.yaml")
	fxMkdirAll(t, filepath.Dir(full))
	fxWriteFile(t, full, content)
	fxCommitAndPush(t, fx.dir, "fixture: consumes for "+system)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestInboxDeprecationReachesALateAdopter is epic AC-10, and it is P4's
// Edge 3 in one sentence: a consumer that adopts AFTER a deprecation
// announcement was published must still see that deprecation.
//
// The FIXTURE ORDER is the test. The announcement is committed first, with
// a `to:` naming only the consumers registered at that moment, and axon's
// own consumes.yaml lands afterwards. Building it the other way round —
// registry first, announcement second — passes with and without Edge 3
// and proves nothing, because a real `contract deprecate` would then have
// addressed axon in `to:` anyway. The whole defect is that `to:` is frozen
// at deprecate time and the announcement's id is seeded without the
// recipient set, so re-running the verb after a new adopt lands on the
// funnel's dedup branch and the newcomer is never addressed.
//
// TEETH: drop the `fa.DeprecatesMyDependency` clause from addressedToMe
// and this test reds — the announcement disappears from a late adopter's
// inbox entirely, which is exactly what shipped before this wave.
func TestInboxDeprecationReachesALateAdopter(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	// 1. seomatrix deprecates XC-seomatrix-widget@1.0.0. At this moment
	//    only beta is registered, so beta is the whole of `to:`.
	fx.commitArtifact("seomatrix/announcements/XA-seomatrix-20260701-dep.md",
		deprecationAnnouncement("XA-seomatrix-20260701-dep", "seomatrix", "XC-seomatrix-widget@1.0.0", []string{"beta"}),
		"1.0.0 sunsets on 2026-09-01.")
	fx.commitEvent("seomatrix", fxULID(200), evt("XA-seomatrix-20260701-dep", "publish", "seomatrix", base))

	// 2. axon adopts afterwards. Re-running `contract deprecate` now would
	//    dedup on the announcement's own id and never address axon.
	fxWriteConsumes(t, fx, "axon", "XC-seomatrix-widget", 1)

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(24 * time.Hour) }, 0)

	items, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if !containsItemID(items, "XA-seomatrix-20260701-dep") {
		t.Fatalf("a consumer that adopted after the deprecation was announced does not see it: got %v", itemIDs(items))
	}

	// It must also be ACTIONABLE, through the same predicate rather than a
	// second copy of the rule: OP-207 condition 1 is "addressed to me with
	// no ack by me", and an unacked deprecation of a contract I depend on
	// is precisely that.
	actionable, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox(actionable): %v", err)
	}
	if !containsItemID(actionable, "XA-seomatrix-20260701-dep") {
		t.Fatalf("the deprecation is visible but not actionable — condition 1 must ride on addressedToMe, "+
			"not on a parallel rule: got %v", itemIDs(actionable))
	}
}

// TestInboxDeprecationIsAUnionNotAReplacement pins the lead decision that
// `to:` degrades to a courtesy snapshot and never loses anything.
//
// beta WAS addressed in the announcement's frozen `to:` and has since
// dropped the dependency — it has no consumes.yaml at all. Under a pure
// replacement (deliver by live registry INSTEAD of `to:`) the announcement
// would vanish from beta's inbox. Disappearing messages are not an
// acceptable price for fixing a delivery race in an append-only system,
// and a consumer that was told a contract is sunsetting has a reason to
// keep seeing it while it migrates off.
func TestInboxDeprecationIsAUnionNotAReplacement(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx.commitArtifact("seomatrix/announcements/XA-seomatrix-20260701-dep.md",
		deprecationAnnouncement("XA-seomatrix-20260701-dep", "seomatrix", "XC-seomatrix-widget@1.0.0", []string{"beta"}),
		"1.0.0 sunsets on 2026-09-01.")
	fx.commitEvent("seomatrix", fxULID(200), evt("XA-seomatrix-20260701-dep", "publish", "seomatrix", base))

	store := NewStore("beta", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(24 * time.Hour) }, 0)

	items, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if !containsItemID(items, "XA-seomatrix-20260701-dep") {
		t.Fatalf("beta was addressed in to: and must keep seeing the announcement even with no consumes.yaml: got %v", itemIDs(items))
	}
}

// TestInboxDeprecationIgnoresTheDeprecatedVersionsMajor pins the second
// lead decision: the match is on the CONTRACT ID alone.
//
// Edge 1 scoped the retire GATE to the major being retired; the deprecation
// ADDRESSEE set stayed unscoped on purpose, and skill/a2ahub/loops.md now
// documents exactly that asymmetry ("who was told" is the larger set, "who
// can block me" the smaller one). A consumer pinned to major 2 hears that
// 1.x is sunsetting — it is simply not blocked by it. Matching on the
// version here would quietly re-narrow the promise the skill makes.
func TestInboxDeprecationIgnoresTheDeprecatedVersionsMajor(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx.commitArtifact("seomatrix/announcements/XA-seomatrix-20260701-dep.md",
		deprecationAnnouncement("XA-seomatrix-20260701-dep", "seomatrix", "XC-seomatrix-widget@1.0.0", []string{"nobody"}),
		"1.0.0 sunsets.")
	fx.commitEvent("seomatrix", fxULID(200), evt("XA-seomatrix-20260701-dep", "publish", "seomatrix", base))

	// axon is pinned to major 2; the deprecation is of the 1.x line.
	fxWriteConsumes(t, fx, "axon", "XC-seomatrix-widget", 2)

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(24 * time.Hour) }, 0)

	items, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if !containsItemID(items, "XA-seomatrix-20260701-dep") {
		t.Fatalf("a consumer on major 2 must still HEAR that 1.x is sunsetting (it is only not BLOCKED by it): got %v", itemIDs(items))
	}
}

// TestInboxUnreadableOwnRegistryDegradesAndReports pins the third lead
// decision, and both halves of it matter.
//
// A malformed own consumes.yaml must not fail the whole inbox — refusing
// to render an inbox because one file is broken is worse than the
// condition. But it must not be silent either: the operator is now being
// shown LESS than they should be, and that is exactly the class of
// degradation skipped.go exists to report. The retire gate fails CLOSED on
// this same input, deliberately: there, unreadable means "I cannot prove
// nobody depends on this".
func TestInboxUnreadableOwnRegistryDegradesAndReports(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	// Addressed directly, so it survives the degradation — that is the
	// point of degrading to `to:` rather than erroring.
	fx.commitArtifact("seomatrix/announcements/XA-seomatrix-20260701-dep.md",
		deprecationAnnouncement("XA-seomatrix-20260701-dep", "seomatrix", "XC-seomatrix-widget@1.0.0", []string{"axon"}),
		"1.0.0 sunsets.")
	fx.commitEvent("seomatrix", fxULID(200), evt("XA-seomatrix-20260701-dep", "publish", "seomatrix", base))

	full := filepath.Join(fx.dir, "axon", "consumes.yaml")
	fxMkdirAll(t, filepath.Dir(full))
	fxWriteFile(t, full, "consumes: []\n") // the placeholder shape parseConsumesStrict refuses
	fxCommitAndPush(t, fx.dir, "fixture: malformed consumes")

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(24 * time.Hour) }, 0)

	items, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("Inbox must not fail on a malformed own registry: %v", err)
	}
	if !containsItemID(items, "XA-seomatrix-20260701-dep") {
		t.Fatalf("degraded to to:-only, so a directly addressed announcement must still appear: got %v", itemIDs(items))
	}

	skipped, err := store.SkippedFiles(context.Background(), "sp1")
	if err != nil {
		t.Fatalf("SkippedFiles: %v", err)
	}
	var found bool
	for _, s := range skipped {
		if s.Path == "axon/consumes.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the unreadable registry must be REPORTED, not silently swallowed — the operator is being "+
			"shown less than they should be: skipped=%+v", skipped)
	}
}

// containsItemID reports whether items carries an item with id.
func containsItemID(items []Item, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}
