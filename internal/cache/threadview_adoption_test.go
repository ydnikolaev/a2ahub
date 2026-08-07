package cache

// threadview_adoption_test.go covers buildTranscript's TranscriptKindDerived
// rows (spec: the operator's own report — a counterparty's adoption of a
// contract was invisible in the thread transcript, "я как будто сам с собой
// разговариваю"). contractAdoptions reads consumes.yaml straight off the
// mirror's checked-out working tree (like registered_consumers_test.go's own
// fixtures), so these tests write consumes.yaml directly rather than
// committing it — matching rcWriteConsumesYAML's own convention.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// taContractFields builds a minimal contract envelope, the same shape
// TestThreadView_ContractRollingWindowNextActions already uses in
// threadview_test.go.
func taContractFields(id, threadID, from string, created time.Time) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "contract", "title": "widget contract",
		"space": "fixture-space", "from": from, "to": []string{"seomatrix"}, "thread": threadID,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(created), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

// taWriteConsumes writes system's own consumes.yaml directly onto the
// mirror's working tree, naming exactly one dependency. since is written
// verbatim, and OMITTED entirely (no `since:` key at all, not an empty
// string) when since == "" — exercising the honesty rule's own missing-date
// case, distinct from rcWriteConsumesYAML (registered_consumers_test.go),
// which always writes one.
func taWriteConsumes(t *testing.T, dir, system, contractID string, major int, since string) {
	t.Helper()
	content := "schema: consumes/v1\nsystem: " + system + "\ndependencies:\n" +
		"  - contract: " + contractID + "\n    major: " + strconv.Itoa(major) + "\n"
	if since != "" {
		content += "    since: \"" + since + "\"\n"
	}
	rcWriteFile(t, dir, system+"/consumes.yaml", content)
}

// taDerivedRows filters a transcript down to its TranscriptKindDerived rows,
// preserving transcript order.
func taDerivedRows(entries []TranscriptEntry) []TranscriptEntry {
	var out []TranscriptEntry
	for _, e := range entries {
		if e.Kind == TranscriptKindDerived {
			out = append(out, e)
		}
	}
	return out
}

// TestThreadView_AdoptedContractProducesOneDerivedRowPerConsumer is the
// brief's core assertion: an adopted contract produces exactly one derived
// transcript row per consumer, each carrying the consuming system, the
// major it pinned, and the since date from ITS OWN registry — and each row
// is NOT of the event kind (nor the artifact kind): a reader checking Kind
// alone must be able to tell it apart from a real committed fact.
func TestThreadView_AdoptedContractProducesOneDerivedRowPerConsumer(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"}, fixtureParticipant{System: "thirdsys"})
	threadID := "thread:axon-20260801-adopt1"
	contractID := "XC-axon-widget"
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/provides/widget/contract.md", taContractFields(contractID, threadID, "axon", base), "contract body")

	taWriteConsumes(t, fx.dir, "seomatrix", contractID, 1, "2026-01-05")
	taWriteConsumes(t, fx.dir, "thirdsys", contractID, 2, "2026-01-01")

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}

	derived := taDerivedRows(result.Transcript)
	if len(derived) != 2 {
		t.Fatalf("derived rows = %+v, want exactly 2 (one per consumer)", derived)
	}

	bySystem := map[string]TranscriptEntry{}
	for _, d := range derived {
		if d.Kind == "event" {
			t.Fatalf("derived row rendered as kind=event: %+v", d)
		}
		if d.Kind != TranscriptKindDerived {
			t.Fatalf("derived row kind = %q, want %q", d.Kind, TranscriptKindDerived)
		}
		if d.Event != nil {
			t.Fatalf("derived row carries a non-nil Event payload: %+v", d)
		}
		if d.Derived == nil {
			t.Fatalf("derived row carries no Derived payload: %+v", d)
		}
		bySystem[d.Derived.System] = d
	}

	seomatrix, ok := bySystem["seomatrix"]
	if !ok {
		t.Fatalf("no derived row for seomatrix: %+v", derived)
	}
	if seomatrix.Derived.ContractID != contractID || seomatrix.Derived.Major != 1 || seomatrix.Derived.Since != "2026-01-05" {
		t.Fatalf("seomatrix derived row = %+v, want {contract=%s major=1 since=2026-01-05}", seomatrix.Derived, contractID)
	}

	thirdsys, ok := bySystem["thirdsys"]
	if !ok {
		t.Fatalf("no derived row for thirdsys: %+v", derived)
	}
	if thirdsys.Derived.ContractID != contractID || thirdsys.Derived.Major != 2 || thirdsys.Derived.Since != "2026-01-01" {
		t.Fatalf("thirdsys derived row = %+v, want {contract=%s major=2 since=2026-01-01}", thirdsys.Derived, contractID)
	}
}

// TestThreadView_UnadoptedContractProducesNoDerivedRows is the negative
// case: a contract with NO consuming system's consumes.yaml naming it must
// not manufacture a derived row out of nothing.
func TestThreadView_UnadoptedContractProducesNoDerivedRows(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID := "thread:axon-20260801-noadopt"
	contractID := "XC-axon-lonely"
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/provides/lonely/contract.md", taContractFields(contractID, threadID, "axon", base), "contract body")

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}

	derived := taDerivedRows(result.Transcript)
	if len(derived) != 0 {
		t.Fatalf("derived rows = %+v, want none (nobody adopted this contract)", derived)
	}
}

// TestThreadView_DerivedRowsOrderedBySince pins the ordering rule: two
// adoptions of the SAME contract are placed into the transcript by their
// own `since` date, earliest first — regardless of the consuming system's
// name sorting the other way (thirdsys's `since` is earlier than
// seomatrix's, but "seomatrix" < "thirdsys" lexically, so a name-ordered or
// insertion-ordered implementation would put this in the wrong order by
// accident-proof construction).
func TestThreadView_DerivedRowsOrderedBySince(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"}, fixtureParticipant{System: "thirdsys"})
	threadID := "thread:axon-20260801-order1"
	contractID := "XC-axon-ordered"
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/provides/ordered/contract.md", taContractFields(contractID, threadID, "axon", base), "contract body")

	// seomatrix adopted FIRST (earlier since), thirdsys adopted LATER —
	// written in the OPPOSITE order here so insertion order cannot be
	// mistaken for the ordering key under test.
	taWriteConsumes(t, fx.dir, "thirdsys", contractID, 1, "2026-03-01")
	taWriteConsumes(t, fx.dir, "seomatrix", contractID, 1, "2026-01-01")

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}

	derived := taDerivedRows(result.Transcript)
	if len(derived) != 2 {
		t.Fatalf("derived rows = %+v, want exactly 2", derived)
	}
	if derived[0].Derived.System != "seomatrix" || derived[1].Derived.System != "thirdsys" {
		t.Fatalf("derived order = [%s, %s], want [seomatrix, thirdsys] (earliest since first)",
			derived[0].Derived.System, derived[1].Derived.System)
	}
}

// TestThreadView_AdoptionMissingSinceRendersUndatedRow is the honesty rule,
// pinned: a consumes.yaml dependency with no `since:` field must still
// produce a derived row (the fact of adoption is never dropped), but that
// row's Since must stay empty — never invented, never "now".
func TestThreadView_AdoptionMissingSinceRendersUndatedRow(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID := "thread:axon-20260801-nosince"
	contractID := "XC-axon-undated"
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/provides/undated/contract.md", taContractFields(contractID, threadID, "axon", base), "contract body")
	taWriteConsumes(t, fx.dir, "seomatrix", contractID, 1, "")

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}

	derived := taDerivedRows(result.Transcript)
	if len(derived) != 1 {
		t.Fatalf("derived rows = %+v, want exactly 1 (the fact of adoption must not be dropped)", derived)
	}
	if derived[0].Derived.System != "seomatrix" || derived[0].Derived.Major != 1 {
		t.Fatalf("derived row = %+v, want system=seomatrix major=1", derived[0].Derived)
	}
	if derived[0].Derived.Since != "" {
		t.Fatalf("Since = %q, want empty — a missing since: must never be invented", derived[0].Derived.Since)
	}
}

// TestThreadView_AdoptionNeverPrecedesItsOwnContract: a consumer's own
// consumes.yaml `since:` date can predate the CONTRACT's own commit into
// this space's history (the registry entry was written by the consumer
// against real wall-clock time; the contract's own `created` here is a
// fixture, not causally verified against it). A comparator that ordered a
// derived row purely by `since` against every other candidate's `at`
// would render "seomatrix adopted this" ahead of "this contract exists" —
// this pins that a derived row's anchor is its own contract's commit
// position, never its since date alone, in the (default, git-backed)
// committed order mode.
func TestThreadView_AdoptionNeverPrecedesItsOwnContract(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID := "thread:axon-20260801-precedes"
	contractID := "XC-axon-precedes"
	// The contract is committed in AUGUST 2026...
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/provides/precedes/contract.md", taContractFields(contractID, threadID, "axon", base), "contract body")
	// ...but seomatrix's own registry declares a `since` in JANUARY 2026,
	// earlier than the contract's own committed creation.
	taWriteConsumes(t, fx.dir, "seomatrix", contractID, 1, "2026-01-05")

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if result.Order != ThreadOrderCommitted {
		t.Fatalf("order = %q, want committed (this regression is specifically about the git-backed ordering mode)", result.Order)
	}

	contractIdx, derivedIdx := -1, -1
	for i, e := range result.Transcript {
		if e.Kind == "artifact" && e.Artifact.ID == contractID {
			contractIdx = i
		}
		if e.Kind == TranscriptKindDerived && e.Derived.ContractID == contractID {
			derivedIdx = i
		}
	}
	if contractIdx == -1 || derivedIdx == -1 {
		t.Fatalf("expected both the contract artifact and its derived adoption row: %+v", result.Transcript)
	}
	if derivedIdx < contractIdx {
		t.Fatalf("derived adoption row (idx %d) rendered BEFORE its own contract (idx %d), despite an earlier `since` date — "+
			"an adoption must never render as though it preceded the thing it adopts", derivedIdx, contractIdx)
	}
}

// TestThreadView_UndatedAdoptionSortsLastNotFirst: a derived row with no
// `since:` at all must not let the zero-time degradation place it FIRST
// in the transcript — that would assert "this adoption happened before
// anything else in the thread", which is exactly the invented claim the
// honesty rule forbids, just expressed as a position instead of a date.
//
// This is deliberately built in DECLARED order mode (an unreadable/absent
// git history — commitOrder degrades, same fixture shape as
// TestThreadView_UnreadableHistoryDeclaredOrder in threadview_test.go),
// because that is the ONE mode where the committed-mode seq anchor
// (TestThreadView_AdoptionNeverPrecedesItsOwnContract) offers no
// protection at all: declared order compares raw `at` timestamps
// directly, so an undated row's zero time has nothing else standing
// between it and "sorts before everything real".
func TestThreadView_UndatedAdoptionSortsLastNotFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // deliberately NOT a git repo
	threadID := "thread:axon-20260801-undatedlast"
	contractID := "XC-axon-undatedlast"
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	writeThreadArtifact(t, dir, "axon/provides/undatedlast/contract.md", taContractFields(contractID, threadID, "axon", base))
	taWriteConsumes(t, dir, "thirdsys", contractID, 1, "")

	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}, {System: "seomatrix", Status: "active"}}}
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, time.Now, 0)

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if result.Order != ThreadOrderDeclared {
		t.Fatalf("order = %q, want declared (this regression is specifically about the ordering mode with no seq anchor)", result.Order)
	}
	if len(result.Transcript) == 0 {
		t.Fatal("empty transcript")
	}
	last := result.Transcript[len(result.Transcript)-1]
	if last.Kind != TranscriptKindDerived || last.Derived == nil || last.Derived.System != "thirdsys" {
		t.Fatalf("transcript[last] = %+v, want the undated derived adoption row (it must sort after every dated/real entry)", last)
	}
}
