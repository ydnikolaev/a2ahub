package cache

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// --- shared fixture-field builders (thread-carrying — store_inbox_test.go's
// own wr()/responseFields() carry no `thread` field) ---

func twWorkRequest(id, title, from string, to []string, thread string, created time.Time) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "work_request", "title": title,
		"space": "fixture-space", "from": from, "to": to, "thread": thread,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(created), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

func twResponse(id, parent, from, to, thread string, created time.Time) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "response", "title": "response",
		"space": "fixture-space", "from": from, "to": []string{to}, "parent": parent,
		"result": "answered", "thread": thread,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": fxAt(created), "priority": "p2", "blocking": false, "classification": "internal",
	}
}

// buildExchangeThread wires up ONE work_request -> response exchange shared
// by several ThreadView tests below: axon submits, seomatrix
// acknowledges/accepts/responds. The response's `created` is deliberately
// 90s BEFORE the parent's own `created` (clock skew) and carries NO events
// of its own beyond the co-committed `respond` event on the parent — the
// exact D3 shape (spec 46 §1 D3) plus the "created ordering is weaker"
// clock-skew case §T3's decision record calls out, in one fixture.
func buildExchangeThread(t *testing.T) (fx *fixtureSpace, threadID, parentID, responseID string) {
	t.Helper()
	fx = newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID = "thread:axon-20260701-tt01"
	parentID = "XW-axon-20260701-q1"
	responseID = "XS-seomatrix-20260701-r1"

	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/"+parentID+".md",
		twWorkRequest(parentID, "question", "axon", []string{"seomatrix"}, threadID, base), "body")
	fx.commitEvent("axon", fxULID(1), evt(parentID, "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(2), evt(parentID, "acknowledge", "seomatrix", base.Add(10*time.Minute)))
	fx.commitEvent("seomatrix", fxULID(3), evt(parentID, "accept", "seomatrix", base.Add(20*time.Minute)))

	// Clock skew: the responder's own `created` predates the question's.
	responseCreated := base.Add(-90 * time.Second)
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/"+responseID+".md",
		twResponse(responseID, parentID, "seomatrix", "axon", threadID, responseCreated),
		"resp body",
		"seomatrix", fxULID(4),
		evt(parentID, "respond", "seomatrix", base.Add(30*time.Minute)),
	)
	return fx, threadID, parentID, responseID
}

// verifiedChainThreadView extends buildExchangeThread by one commit: the
// asker verifies the response, closing the exchange. That single event is
// what used to poison the thread's Flags — see the test below.
func verifiedChainThreadView(t *testing.T) ThreadResult {
	t.Helper()
	fx, threadID, _, responseID := buildExchangeThread(t)
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	fx.commitEvent("axon", fxULID(5), evt(responseID, "verify", "axon", base.Add(40*time.Minute)))

	store := newThreadStore(t, fx, "sp1")
	res, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	return res
}

func TestThreadAndShowPreserveEventNote(t *testing.T) {
	t.Parallel()
	fx, threadID, parentID, _ := buildExchangeThread(t)
	note := "The result is valid; keep the synthetic-locale evidence attached for the next release."
	fields := evt(parentID, "note", "axon", time.Date(2026, 7, 1, 10, 35, 0, 0, time.UTC))
	fields["note"] = note
	fx.commitEvent("axon", fxULID(6), fields)

	store := newThreadStore(t, fx, "sp1")
	thread, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	found := false
	for _, entry := range thread.Transcript {
		if entry.Event != nil && entry.Event.Note == note {
			found = true
		}
	}
	if !found {
		t.Fatalf("thread transcript lost committed note: %+v", thread.Transcript)
	}

	show, err := store.Show(context.Background(), parentID)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	found = false
	for _, event := range show.Events {
		if event.Note == note {
			found = true
		}
	}
	if !found {
		t.Fatalf("artifact history lost committed note: %+v", show.Events)
	}
}

func TestThreadViewCarriesCommittedWorkReportOnStatusAnnouncement(t *testing.T) {
	t.Parallel()
	fx, threadID, parentID, _ := buildExchangeThread(t)
	reportedAt := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	validUntil := reportedAt.Add(30 * time.Minute)
	statusID := "XA-axon-20260701-status"
	fx.commitArtifact("axon/exchanges/"+statusID+".md", map[string]any{
		"schema": "envelope/v2", "id": statusID, "type": "announcement", "title": "Implementation checkpoint",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"}, "thread": threadID,
		"actor":   map[string]any{"kind": "agent", "name": "codex", "model": "gpt-5", "session": "session:01K20ABCDEFHJKMNPQRSTVWXYZ"},
		"created": reportedAt.Format(time.RFC3339), "category": "status", "priority": "p2", "blocking": false,
		"ack_requested": false, "classification": "internal", "refs": []map[string]any{{"ref": parentID}},
		"work": map[string]any{
			"id": "work:01K20ABCDEFHJKMNPQRSTVWXYZ", "semantic_sequence": 1, "mode": "implementing",
			"subject_ref": parentID, "summary": "Implementing the replay-safe capture path",
			"reported_at": reportedAt.Format(time.RFC3339), "valid_until": validUntil.Format(time.RFC3339),
		},
	}, "Durable checkpoint")

	store := newThreadStore(t, fx, "sp1")
	thread, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	for _, entry := range thread.Transcript {
		if entry.Artifact == nil || entry.Artifact.ID != statusID {
			continue
		}
		work := entry.Artifact.Work
		if work == nil {
			t.Fatalf("status announcement lost its committed work payload: %+v", entry.Artifact)
		}
		if work.SubjectRef != parentID || work.Mode != "implementing" || work.Summary != "Implementing the replay-safe capture path" ||
			work.Actor.Name != "codex" || work.Actor.Model != "gpt-5" ||
			work.Actor.Session != "session:01K20ABCDEFHJKMNPQRSTVWXYZ" || work.ReportedAt != reportedAt {
			t.Fatalf("work report = %+v", work)
		}
		return
	}
	t.Fatalf("status announcement %s not found in transcript: %+v", statusID, thread.Transcript)
}

func TestThreadViewWorkHistoryProjectsHostileLegacyTextSafely(t *testing.T) {
	t.Parallel()
	fx, threadID, parentID, _ := buildExchangeThread(t)
	reportedAt := time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC)
	statusID := "XA-axon-20260701-hostile-status"
	const (
		unsafeModel       = "password=model-secret"
		unsafeSession     = "token:super-secret"
		unsafeSummary     = "api_key=summary-secret"
		unsafeWaitSummary = "Bearer wait-summary-secret"
		wantSession       = "sha256:360f0b0743ceb50fd947fd94c0137516a7ddc7b52bbc5e9a4e6e855174e63a6a"
	)
	fx.commitArtifact("axon/exchanges/"+statusID+".md", map[string]any{
		"schema": "envelope/v2", "id": statusID, "type": "announcement", "title": "Legacy checkpoint",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"}, "thread": threadID,
		"actor": map[string]any{
			"kind": "agent", "name": "codex", "model": unsafeModel, "session": unsafeSession,
		},
		"created": reportedAt.Format(time.RFC3339), "category": "status", "priority": "p2", "blocking": false,
		"ack_requested": false, "classification": "internal", "refs": []map[string]any{{"ref": parentID}},
		"work": map[string]any{
			"id": "work:01K20ABCDEFHJKMNPQRSTVWXYZ", "semantic_sequence": 2, "mode": "waiting",
			"subject_ref": parentID, "summary": unsafeSummary,
			"reported_at": reportedAt.Format(time.RFC3339), "valid_until": reportedAt.Add(30 * time.Minute).Format(time.RFC3339),
			"waiting_on": []map[string]any{{"kind": "system", "id": "seomatrix", "summary": unsafeWaitSummary}},
		},
	}, "Legacy durable checkpoint")

	store := newThreadStore(t, fx, "sp1")
	thread, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	for _, entry := range thread.Transcript {
		if entry.Artifact == nil || entry.Artifact.ID != statusID {
			continue
		}
		work := entry.Artifact.Work
		if work == nil {
			t.Fatalf("hostile status announcement lost its work history: %+v", entry.Artifact)
		}
		if work.Actor.Kind != "agent" || work.Actor.Name != "codex" || work.Actor.System != "axon" ||
			work.Actor.Model != "[redacted unsafe text]" || work.Actor.Session != wantSession ||
			work.Summary != "[redacted unsafe text]" {
			t.Fatalf("safe work history = %+v", work)
		}
		if len(work.WaitingOn) != 1 || work.WaitingOn[0].Kind != "system" || work.WaitingOn[0].ID != "seomatrix" ||
			work.WaitingOn[0].Summary != "[redacted unsafe text]" {
			t.Fatalf("safe history waits = %+v", work.WaitingOn)
		}
		if work.SubjectRef != parentID || work.Mode != "waiting" || work.ReportedAt != reportedAt ||
			work.CommitSequence == 0 || work.CommitSequence != uint64(entry.Seq)+1 {
			t.Fatalf("safe projection changed history meaning or commit sequence: entry=%+v work=%+v", entry, work)
		}
		encoded, marshalErr := json.Marshal(work)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(work): %v", marshalErr)
		}
		for _, secret := range []string{unsafeModel, unsafeSession, unsafeSummary, unsafeWaitSummary} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("public work history leaked %q: %s", secret, encoded)
			}
		}
		return
	}
	t.Fatalf("status announcement %s not found in transcript: %+v", statusID, thread.Transcript)
}

func newThreadStore(t *testing.T, fx *fixtureSpace, spaceID string) *Store {
	t.Helper()
	return NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: spaceID, Dir: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, 0)
}

// TestThreadView_D3RegressionOrderAndClockSkew is D3's regression, with
// teeth against BOTH wrong orderings the old/considered-and-rejected
// designs would have produced: LatestEventAt (the response has no events
// of its own, so it would sort FIRST) and `created` (the response's
// `created` is 90s BEFORE the parent's, so a created-ordered reader would
// ALSO put it first — §T3's own decision record on why `created` is
// weaker). Commit-seq ordering must put the question first regardless.
func TestThreadView_D3RegressionOrderAndClockSkew(t *testing.T) {
	t.Parallel()
	fx, threadID, parentID, _ := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if result.Order != ThreadOrderCommitted {
		t.Fatalf("order = %q, want committed", result.Order)
	}
	if len(result.Transcript) == 0 || result.Transcript[0].Kind != "artifact" || result.Transcript[0].Artifact.ID != parentID {
		t.Fatalf("transcript[0] = %+v, want the question (%s) first", result.Transcript[0], parentID)
	}
}

// TestThreadView_SameCommitTieBreak is the D-026 co-commit shape: the
// response artifact and the parent's own `respond` event land in the SAME
// commit (same Seq) — artifacts sort before events at an equal seq (§T3).
func TestThreadView_SameCommitTieBreak(t *testing.T) {
	t.Parallel()
	fx, threadID, _, responseID := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	var artifactIdx, eventIdx = -1, -1
	for i, e := range result.Transcript {
		if e.Kind == "artifact" && e.Artifact.ID == responseID {
			artifactIdx = i
		}
		if e.Kind == "event" && e.Event.Transition == "respond" {
			eventIdx = i
		}
	}
	if artifactIdx == -1 || eventIdx == -1 {
		t.Fatalf("expected both the response artifact and the respond event in transcript: %+v", result.Transcript)
	}
	if result.Transcript[artifactIdx].Seq != result.Transcript[eventIdx].Seq {
		t.Fatalf("expected same-commit seq: artifact seq=%d, event seq=%d", result.Transcript[artifactIdx].Seq, result.Transcript[eventIdx].Seq)
	}
	if artifactIdx > eventIdx {
		t.Fatalf("artifact (idx %d) must sort before its same-commit event (idx %d)", artifactIdx, eventIdx)
	}
}

// TestThreadView_ParentResolutionTrap is the highest-stakes step (spec 46
// §T3): a `submitted` response with `from: seomatrix` under a parent with
// `from: axon` must yield `waiting_on == ["axon"]` (exactly — not merely
// containing it, which a RoleAny/every-participant bug would also satisfy)
// and `verify`/`dispute` both resolving `by: ["axon"]`.
func TestThreadView_ParentResolutionTrap(t *testing.T) {
	t.Parallel()
	fx, threadID, _, responseID := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	var item *OpenItem
	for i := range result.OpenItems {
		if result.OpenItems[i].ID == responseID {
			item = &result.OpenItems[i]
		}
	}
	if item == nil {
		t.Fatalf("no open item for response %s: %+v", responseID, result.OpenItems)
	}
	if item.State != "submitted" {
		t.Fatalf("response state = %q, want submitted (overlay must have fired)", item.State)
	}
	if len(item.WaitingOn) != 1 || item.WaitingOn[0] != "axon" {
		t.Fatalf("waiting_on = %v, want exactly [axon]", item.WaitingOn)
	}
	if !item.YourMove {
		t.Fatalf("expected your_move=true when viewing as axon")
	}
	seen := map[string][]string{}
	for _, a := range item.NextActions {
		seen[a.Transition] = a.By
	}
	for _, transition := range []string{"verify", "dispute"} {
		by, ok := seen[transition]
		if !ok {
			t.Fatalf("expected a %s next_action, got %+v", transition, item.NextActions)
		}
		if len(by) != 1 || by[0] != "axon" {
			t.Fatalf("%s.by = %v, want exactly [axon]", transition, by)
		}
	}
}

// TestThreadView_ExchangeDisputeRowOmittedOnParent: the parent exchange's
// own open item (state "responded") must NOT carry a `dispute` next
// action — table.go's exchangeRows() row documents D-024's reopen side
// effect but CheckLegality can never actually authorize firing "dispute"
// against the parent's own subject (its verify/dispute branch is always
// response-scoped), so surfacing it would print a next action nobody can
// legally take. The response's own open item (asserted above) already
// carries `dispute` with the correct `by`.
func TestThreadView_ExchangeDisputeRowOmittedOnParent(t *testing.T) {
	t.Parallel()
	fx, threadID, parentID, _ := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	var item *OpenItem
	for i := range result.OpenItems {
		if result.OpenItems[i].ID == parentID {
			item = &result.OpenItems[i]
		}
	}
	if item == nil {
		t.Fatalf("no open item for parent %s: %+v", parentID, result.OpenItems)
	}
	if item.State != "responded" {
		t.Fatalf("parent state = %q, want responded", item.State)
	}
	transitions := map[string]bool{}
	for _, a := range item.NextActions {
		transitions[a.Transition] = true
	}
	if transitions["dispute"] {
		t.Fatalf("parent's own open item must not carry dispute: %+v", item.NextActions)
	}
	if !transitions["close"] || !transitions["supersede"] {
		t.Fatalf("expected close+supersede on the parent's open item, got %+v", item.NextActions)
	}
}

// TestThreadView_ContractRollingWindowNextActions is P4's own
// highest-risk item (04-per-version-lifecycle.plan.md): once
// fold.Event.Version is populated, a contract's projected subject-level
// State is a PROJECTION over its per-version map, so answering "whose
// move is it" from that projected State alone (plain fold.LegalNext)
// reproduces the exact split brain fold.LegalNextFor was built inside
// internal/fold to close — one caller layer up, in buildOpenItems. This
// pins the {1.0: deprecated, 2.0: published} rolling-window shape
// directly: the contract's PROJECTED state is `published` (2.0 is live),
// so a state-only reader would report "publish"/whatever `published`'s
// OWN table row offers next — but the actual legal whole-contract moves
// are governed by fold.LegalNextFor(KindContract, prior, ""), which
// answers from the per-version map: `deprecate` (2.0 is still published,
// so the whole-form question "is anything left to deprecate" is legal)
// and NEITHER `publish` (a version-less publish is illegal once ANY
// version is recorded, fold/contract.go) NOR `retire` (1.0 is deprecated
// but 2.0 is not, so whole-contract retire is illegal). Teeth: reverting
// buildOpenItems' fold.LegalNextFor/legalSystems calls back to plain
// fold.LegalNext/fold.CheckLegality(kind, fa.Result.State, ...) makes
// this red (`publish` reappears; `deprecate`'s presence becomes
// coincidental rather than derived from the same rule table.go/contract.go
// share).
func TestThreadView_ContractRollingWindowNextActions(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID := "thread:axon-20260701-rollingwindow"
	contractID := "XC-axon-rollingwindow"
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/provides/rollingwindow/contract.md", map[string]any{
		"schema": "envelope/v1", "id": contractID, "type": "contract", "title": "rolling window",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"}, "thread": threadID,
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "contract body")

	publish1 := evt(contractID, "publish", "axon", base.Add(time.Minute))
	publish1["version"] = "1.0.0"
	fx.commitEvent("axon", fxULID(1), publish1)

	publish2 := evt(contractID, "publish", "axon", base.Add(2*time.Minute))
	publish2["version"] = "2.0.0"
	fx.commitEvent("axon", fxULID(2), publish2)

	deprecate1 := evt(contractID, "deprecate", "axon", base.Add(3*time.Minute))
	deprecate1["version"] = "1.0.0"
	fx.commitEvent("axon", fxULID(3), deprecate1)

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}

	var item *OpenItem
	for i := range result.OpenItems {
		if result.OpenItems[i].ID == contractID {
			item = &result.OpenItems[i]
		}
	}
	if item == nil {
		t.Fatalf("no open item for contract %s: %+v", contractID, result.OpenItems)
	}
	if item.State != "published" {
		t.Fatalf("contract state = %q, want published (2.0.0 keeps the subject live)", item.State)
	}

	transitions := map[string][]string{}
	for _, a := range item.NextActions {
		transitions[a.Transition] = a.By
	}
	// Publish MUST still be offered. An owner publishing the next version
	// is the commonest thing that happens to a live contract, and a
	// rolling window is the state in which it happens most; a thread view
	// that drops it the moment a contract has any version is telling the
	// owner their contract is finished. fold.LegalNextFor answers the
	// whole-contract form with `publish` deliberately, for exactly this
	// caller — see contractMoveAvailable's own doc comment.
	pubBy, ok := transitions["publish"]
	if !ok {
		t.Fatalf("publish must still be offered on a live contract: %+v", item.NextActions)
	}
	if len(pubBy) != 1 || pubBy[0] != "axon" {
		t.Fatalf("publish.by = %v, want exactly [axon] (RoleOwner resolves to the contract's own from)", pubBy)
	}
	if _, ok := transitions["retire"]; ok {
		t.Fatalf("retire must not be offered while 2.0.0 is still published: %+v", item.NextActions)
	}
	by, ok := transitions["deprecate"]
	if !ok {
		t.Fatalf("expected deprecate (2.0.0 is still published) among next_actions: %+v", item.NextActions)
	}
	if len(by) != 1 || by[0] != "axon" {
		t.Fatalf("deprecate.by = %v, want exactly [axon] (RoleOwner resolves to the contract's own from)", by)
	}
}

// TestThreadView_TwoSpacesAmbiguity is CC-073/T4: the same thread ID
// present in two connected spaces must refuse, never silently merge.
func TestThreadView_TwoSpacesAmbiguity(t *testing.T) {
	t.Parallel()
	threadID := "thread:axon-20260701-amb1"
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx1 := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	fx1.commitArtifact("axon/exchanges/XW-axon-sp1.md", twWorkRequest("XW-axon-sp1", "q1", "axon", []string{}, threadID, base), "body")

	fx2 := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	fx2.commitArtifact("axon/exchanges/XW-axon-sp2.md", twWorkRequest("XW-axon-sp2", "q2", "axon", []string{}, threadID, base), "body")

	store := NewStore("axon", t.TempDir(), []SpaceMirror{
		{SpaceID: "sp1", Dir: fx1.dir, Manifest: mustManifest(t, fx1)},
		{SpaceID: "sp2", Dir: fx2.dir, Manifest: mustManifest(t, fx2)},
	}, time.Now, 0)

	_, err := store.ThreadView(context.Background(), threadID, "")
	if err == nil {
		t.Fatal("ThreadView: want an ambiguity error, got nil")
	}
	var amb *ThreadAmbiguityError
	if !errors.As(err, &amb) {
		t.Fatalf("ThreadView error = %v (%T), want *ThreadAmbiguityError", err, err)
	}
	if len(amb.Spaces) != 2 {
		t.Fatalf("amb.Spaces = %+v, want 2 entries", amb.Spaces)
	}
	for _, sh := range amb.Spaces {
		if sh.MemberCount != 1 {
			t.Fatalf("space %s member count = %d, want 1", sh.Space, sh.MemberCount)
		}
		if sh.Opener.ID == "" {
			t.Fatalf("space %s opener not set: %+v", sh.Space, sh)
		}
	}
}

// TestThreadView_ArtifactIDResolves is US-6/D6: any member artifact id
// resolves to its own thread, and ResolvedFrom records it.
func TestThreadView_ArtifactIDResolves(t *testing.T) {
	t.Parallel()
	fx, threadID, parentID, responseID := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	result, err := store.ThreadView(context.Background(), responseID, "")
	if err != nil {
		t.Fatalf("ThreadView(%s): %v", responseID, err)
	}
	if result.Thread != threadID {
		t.Fatalf("thread = %q, want %q", result.Thread, threadID)
	}
	if result.ResolvedFrom != responseID {
		t.Fatalf("resolved_from = %q, want %q", result.ResolvedFrom, responseID)
	}
	if result.Opener.ID != parentID {
		t.Fatalf("opener = %+v, want id %s (lowest seq)", result.Opener, parentID)
	}
}

// TestThreadView_UnknownArtifactErrors is D6: an id matching nothing is a
// typed error, never an empty success.
func TestThreadView_UnknownArtifactErrors(t *testing.T) {
	t.Parallel()
	fx, _, _, _ := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	_, err := store.ThreadView(context.Background(), "XW-axon-nonexistent", "")
	if err == nil {
		t.Fatal("ThreadView: want error for unknown artifact id, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ThreadView error = %v, want wrapping ErrNotFound", err)
	}
}

// TestThreadView_FlagsAndUnresolvedNonNilEmpty is spec 46 §T3: on a clean
// thread `flags`/`unresolved` are ALWAYS present (non-nil), empty arrays
// included — never omitted, so a caller can assert `unresolved == []`.
func TestThreadView_FlagsAndUnresolvedNonNilEmpty(t *testing.T) {
	t.Parallel()
	fx, threadID, _, _ := buildExchangeThread(t)
	store := newThreadStore(t, fx, "sp1")

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if result.Flags == nil {
		t.Fatal("Flags is nil, want non-nil (possibly empty) slice")
	}
	if len(result.Flags) != 0 {
		t.Fatalf("Flags = %+v, want empty on a clean thread", result.Flags)
	}
	if result.Unresolved == nil {
		t.Fatal("Unresolved is nil, want non-nil (possibly empty) slice")
	}
	if len(result.Unresolved) != 0 {
		t.Fatalf("Unresolved = %+v, want empty on a clean thread", result.Unresolved)
	}
}

// TestThreadView_UnresolvedParentMissing: a response whose `parent` names
// no artifact anywhere in the space is rendered, never dropped, and
// flagged in Unresolved (§T3: "members whose parent/thread cannot be
// resolved ... rendered, never dropped").
func TestThreadView_UnresolvedParentMissing(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadID := "thread:seomatrix-20260701-orph1"
	respID := "XS-seomatrix-20260701-orph"
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("seomatrix/exchanges/"+respID+".md",
		twResponse(respID, "XW-axon-doesnotexist", "seomatrix", "axon", threadID, base), "body")

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	found := false
	for _, u := range result.Unresolved {
		if u.Kind == "parent" && u.ID == respID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Unresolved = %+v, want a parent-missing entry for %s", result.Unresolved, respID)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ID != respID {
		t.Fatalf("Artifacts = %+v, want the orphaned response rendered (never dropped)", result.Artifacts)
	}
}

// TestThreadView_UnresolvedEventSubjectMissing: a verify event attached to
// the parent's gathered event set (mirror.go's own D-024 correlation)
// whose subject (a response id) is NOT itself a rendered member of THIS
// thread — because that response carries a DIFFERENT thread value, the
// REF-009 violation this phase's validator (off limits to this wave)
// would otherwise reject — is still rendered in the transcript and
// flagged in Unresolved, never silently dropped.
func TestThreadView_UnresolvedEventSubjectMissing(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	threadA := "thread:axon-20260701-fork1"
	threadB := "thread:axon-20260701-fork2"
	parentID := "XW-axon-20260701-forkq"
	respID := "XS-seomatrix-20260701-forkr"
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/exchanges/"+parentID+".md",
		twWorkRequest(parentID, "q", "axon", []string{"seomatrix"}, threadA, base), "body")
	fx.commitEvent("axon", fxULID(20), evt(parentID, "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(21), evt(parentID, "acknowledge", "seomatrix", base.Add(time.Hour)))
	fx.commitEvent("seomatrix", fxULID(22), evt(parentID, "accept", "seomatrix", base.Add(2*time.Hour)))

	// The response is on a DIFFERENT thread than its own parent (a fork).
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/"+respID+".md",
		twResponse(respID, parentID, "seomatrix", "axon", threadB, base.Add(3*time.Hour)),
		"resp body",
		"seomatrix", fxULID(23),
		evt(parentID, "respond", "seomatrix", base.Add(3*time.Hour)),
	)
	// axon verifies the response — subject == response id (D-024).
	fx.commitEvent("axon", fxULID(24), evt(respID, "verify", "axon", base.Add(4*time.Hour)))

	store := newThreadStore(t, fx, "sp1")
	result, err := store.ThreadView(context.Background(), threadA, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ID != parentID {
		t.Fatalf("Artifacts = %+v, want only the parent (response is on a different thread)", result.Artifacts)
	}
	found := false
	for _, u := range result.Unresolved {
		if u.Kind == "event-subject" && u.ID == respID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Unresolved = %+v, want an event-subject-missing entry for %s", result.Unresolved, respID)
	}
	inTranscript := false
	for _, e := range result.Transcript {
		if e.Kind == "event" && e.Event.Subject == respID && e.Event.Transition == "verify" {
			inTranscript = true
		}
	}
	if !inTranscript {
		t.Fatal("the verify event must still be rendered in the transcript, never dropped")
	}
}

// TestThreadView_UnreadableHistoryDeclaredOrder is §T3's designed
// degradation: an unreadable/absent git history falls back to `created`
// ordering and reports "declared" instead of "committed" — never silent.
// IDs are chosen so their LEXICAL order is the REVERSE of the intended
// timestamp order, so the assertion cannot pass by accident via the tie
// break alone.
func TestThreadView_UnreadableHistoryDeclaredOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // deliberately NOT a git repo — commitOrder degrades
	threadID := "thread:axon-20260701-decl1"
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	// "XW-axon-zzz-first" sorts LAST lexically but is created FIRST.
	writeThreadArtifact(t, dir, "axon/exchanges/XW-axon-zzz-first.md",
		twWorkRequest("XW-axon-zzz-first", "first", "axon", []string{"seomatrix"}, threadID, base))
	// "XW-axon-aaa-second" sorts FIRST lexically but is created SECOND.
	writeThreadArtifact(t, dir, "axon/exchanges/XW-axon-aaa-second.md",
		twWorkRequest("XW-axon-aaa-second", "second", "axon", []string{"seomatrix"}, threadID, base.Add(time.Hour)))

	manifest := space.Manifest{Participants: []space.Participant{{System: "axon", Status: "active"}, {System: "seomatrix", Status: "active"}}}
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, time.Now, 0)

	result, err := store.ThreadView(context.Background(), threadID, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if result.Order != ThreadOrderDeclared {
		t.Fatalf("order = %q, want declared", result.Order)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("Artifacts = %+v, want 2", result.Artifacts)
	}
	if result.Artifacts[0].ID != "XW-axon-zzz-first" || result.Artifacts[1].ID != "XW-axon-aaa-second" {
		t.Fatalf("Artifacts order = [%s, %s], want created-time order despite reversed IDs",
			result.Artifacts[0].ID, result.Artifacts[1].ID)
	}
}

func writeThreadArtifact(t *testing.T, dir, relPath string, fields map[string]any) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := yaml.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	content := "---\n" + string(raw) + "---\nbody\n"
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// TestThreadViewVerifiedResponseCarriesNoSpuriousFlag pins the fix for a
// defect found while building P46's e2e chain fixture: a correctly verified
// response used to produce an `unauthorized-actor` flag against its own event.
//
// Why it is worth its own test rather than riding along in an ordering test:
// the flag was invisible. The closure-state overlay overwrites Result.State
// and never Result.Flags, so every state a reader printed was correct while
// the flags beside them said the exchange was suspect. Spec 46 promises a
// thread carrying such a flag never renders clean — so the reader would have
// reported a healthy conversation as broken, which is the authoritative-
// looking wrong answer this whole phase exists to prevent, produced by the
// phase's own reader.
//
// The assertion is deliberately on the THREAD view rather than on fold: what
// must stay true is that a healthy chain reads healthy end to end.
func TestThreadViewVerifiedResponseCarriesNoSpuriousFlag(t *testing.T) {
	t.Parallel()
	res := verifiedChainThreadView(t)
	if len(res.Flags) != 0 {
		t.Fatalf("a correctly verified response flagged the thread: %+v\n"+
			"A healthy exchange must read healthy — spec 46 §T3 makes `flags` the signal "+
			"that something is wrong, so a false positive there is worse than no flag at all.",
			res.Flags)
	}
	if len(res.Unresolved) != 0 {
		t.Fatalf("unexpected unresolved facts on a complete chain: %+v", res.Unresolved)
	}
}

// TestThreadViewThreadlessArtifactRefuses pins a wart that only reading real
// output against a live space exposed: an artifact committed before threads
// were minted has an empty thread, and without a guard that empty value falls
// through and matches every OTHER threadless artifact in the space. The reader
// then presents a "conversation" with an empty id, assembled from documents
// that have nothing to do with each other, and a caller cannot tell it from a
// real thread.
//
// That is strictly worse than the error: the whole feature exists to stop an
// authoritative-looking view of a relationship that does not exist.
func TestThreadViewThreadlessArtifactRefuses(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	// TWO threadless artifacts, so a missing guard would visibly group them.
	first := "XW-axon-20260701-n1"
	second := "XW-axon-20260701-n2"
	for _, id := range []string{first, second} {
		fx.commitArtifact("axon/exchanges/"+id+".md",
			twWorkRequest(id, "question", "axon", []string{"seomatrix"}, "", base), "body")
	}

	store := newThreadStore(t, fx, "sp1")
	_, err := store.ThreadView(context.Background(), first, "")
	if err == nil {
		t.Fatal("a threadless artifact rendered a thread: an empty thread id matches every other " +
			"threadless artifact, so this would present unrelated documents as one conversation")
	}
	if !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("err = %v, want ErrThreadNotFound so callers can distinguish it", err)
	}
	if !strings.Contains(err.Error(), "carries no thread") {
		t.Fatalf("the error must name the actual condition, got: %v", err)
	}
}
