package e2e

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// threadChainThread is the §3.8 thread show_thread.txtar's real chain lives
// on — deliberately distinct from e2eFixtureThread (XR-axon-demo's own
// thread, already seeded by seedOriginExtras): mixing the two would put an
// unrelated requirement into the transcript and muddy every ordering/open-
// item assertion this wave adds (spec 46 D7).
const threadChainThread = "thread:beta-20260722-c9h1"

// threadChainQuestionID / threadChainResponseID are the chain's two thread
// members — a real question (beta -> gamma) and its real response (gamma ->
// beta), linked by `parent` and sharing threadChainThread.
//
// Deliberately BETA/GAMMA, never axon: this fixture is pushed to the ONE
// shared origin TestT3Scripts' every other T3 script clones (via
// seedOriginExtras, txtar_test.go — off limits to this wave), and every
// script runs AS axon (that Setup's own project config). Content addressed
// to/from axon changes axon's own inbox/outbox/statusline — inbox_outbox.
// txtar and statusline.txtar (also off limits) hard-code axon's inbox as
// empty and statusline's one urgent item as XR-axon-demo; a thread axon has
// no part in leaves both undisturbed. See this phase's own report for the
// collateral failure this replaced (axon-rooted chain broke both scripts).
const (
	threadChainQuestionID = "XQ-beta-20260722-q1a2"
	threadChainResponseID = "XS-gamma-20260722-r3b4"
)

// writeThreadChainQuestion seeds the chain's opening question: beta asks
// gamma, on threadChainThread. Mirrors writeQuestionArtifact's own shape
// (category/priority/blocking/classification), on a thread of its own.
func writeThreadChainQuestion(t *testing.T, mirrorDir string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + threadChainQuestionID + "\n" +
		"type: question\n" +
		"title: does the export include soft-deleted rows\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [gamma]\n" +
		"thread: " + threadChainThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-22T09:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\ndoes the export include soft-deleted rows?\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+threadChainQuestionID+".md", content)
}

// writeThreadChainResponse seeds gamma's real response — parent set to the
// question above, thread INHERITED (never re-derived): the exact shape
// `a2a respond` itself writes (spec 46 §T1 R4/R5), constructed directly
// here because this package builds T3 fixtures by direct construction, not
// by driving the write funnel (see e2eFixtureThread's own doc comment).
func writeThreadChainResponse(t *testing.T, mirrorDir string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + threadChainResponseID + "\n" +
		"type: response\n" +
		"title: Response to " + threadChainQuestionID + "\n" +
		"space: fixture-space\n" +
		"from: gamma\n" +
		"to: [beta]\n" +
		"thread: " + threadChainThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-22T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"parent: " + threadChainQuestionID + "\n" +
		"result: answered\n" +
		"classification: internal\n" +
		"---\nno, soft-deleted rows are excluded by default; pass --include-deleted to change that.\n"
	writeMirrorFile(t, mirrorDir, "gamma/exchanges/"+threadChainResponseID+".md", content)
}

// writeThreadChainRespondEvent seeds the PARENT-side `respond` event — spec
// 46 D3's own "the response has no events of its own" (the response's OWN
// events remain empty until a verify/dispute arrives; the `respond`
// transition is recorded against the QUESTION's subject, not the
// response's). Subject is the question's id; refs[0] links the newly-
// authored response, exactly the shape cmd_lifecycle.go's RespondCommand.
// Run itself commits (Subject: parentID, Transition: respond, Refs:
// [{Ref: responseID}]) — never a re-derived shape. Must land in the SAME
// commit as the response artifact (D-026 "one commit, one event per
// artifact"; mirror.go's own responsesBySeqAndParent correlation depends
// on it) — seedThreadChainFixture's own "chain: gamma responds" commit
// enforces that.
func writeThreadChainRespondEvent(t *testing.T, mirrorDir string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("writeThreadChainRespondEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: respond\nactor: {kind: agent, name: bot, system: gamma}\nat: 2026-07-22T10:00:00Z\nrefs:\n  - ref: %s\n",
		id.String(), threadChainQuestionID, threadChainResponseID,
	)
	writeMirrorFile(t, mirrorDir, "gamma/events/2026/"+id.String()+".yaml", content)
}

// seedThreadChainFixture pushes the whole D2/D3-regression chain to
// originDir's main branch as FOUR SEPARATE commits — deliberately never
// one bulk commit. The ordering assertion show_thread.txtar exists to make
// (the question must transcript BEFORE the response) is a claim about
// internal/cache's own per-COMMIT sequence (mirror.go's commitOrder); a
// single commit would give every file in it the SAME sequence number,
// collapsing the very distinction the fold's committed-order (as opposed
// to declared-order) guarantee exists to make. Each commit stands in for
// one system's real turn: beta asks, gamma acknowledges, gamma answers
// (response + its parent's own `respond` event, D-026 "one commit"), beta
// verifies — two systems, four commits, one thread.
func seedThreadChainFixture(t *testing.T, originDir string) {
	t.Helper()

	commit := func(write func(dir string), message string) {
		dir := t.TempDir()
		gitRun(t, "", "clone", originDir, dir)
		write(dir)
		gitRun(t, dir, "add", "-A")
		gitRun(t, dir, "commit", "-m", message)
		gitRun(t, dir, "push", "origin", "main")
	}

	commit(func(dir string) {
		writeThreadChainQuestion(t, dir)
		writeLifecycleEvent(t, dir, "beta", 10, threadChainQuestionID, "submit", "beta")
	}, "chain: beta asks")

	commit(func(dir string) {
		writeLifecycleEvent(t, dir, "gamma", 11, threadChainQuestionID, "acknowledge", "gamma")
	}, "chain: gamma acknowledges")

	commit(func(dir string) {
		writeThreadChainResponse(t, dir)
		writeThreadChainRespondEvent(t, dir)
	}, "chain: gamma responds")

	commit(func(dir string) {
		writeLifecycleEvent(t, dir, "beta", 12, threadChainResponseID, "verify", "beta")
	}, "chain: beta verifies")
}

// buildThreadChainStore clones a fresh checkout of originDir and wraps it
// in a *cache.Store (ownSystem "axon" — the same system TestT3Scripts'
// project config uses, so a direct-construction assertion here and an
// exec'd `a2a thread` assertion in show_thread.txtar see the identical
// rendering; axon is not a participant in this thread, so its open item's
// `your_move` is false on both sides equally).
func buildThreadChainStore(t *testing.T, originDir string) *cache.Store {
	t.Helper()
	mirrorDir := t.TempDir()
	gitRun(t, "", "clone", originDir, mirrorDir)
	return cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{
		{SpaceID: "fixture-space", Dir: mirrorDir, Manifest: e2eManifest()},
	}, time.Now, 0)
}

// TestThreadChainFixtureOrdersQuestionBeforeResponse is the direct-
// construction half of spec 46 D7's fix (show_thread.txtar is the exec'd
// half): it proves internal/cache.Store.ThreadView, over the REAL commit
// history seedThreadChainFixture pushes, lists the question strictly
// before the response, that both real events on the question and the
// response's own verify event surface as transcript rows (D2 — the old
// reader listed artifacts only), that the open item correctly names BOTH
// parties still able to act (D-017's multi-response `respond` row keeps
// gamma live alongside beta's own `close`), and that resolving by the
// response's own id lands on the exact same thread as resolving by the
// thread id itself (US-6/D6).
//
// Order sensitivity was checked by hand, not assumed: temporarily
// reordering seedThreadChainFixture's four commits so "chain: gamma
// responds" pushed BEFORE "chain: beta asks" turned this test red on
// exactly the transcript-index assertion below (the response's commit seq
// became lower than the question's) — reverted before this file was
// submitted. See this phase's own report for the exact diff exercised.
func TestThreadChainFixtureOrdersQuestionBeforeResponse(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	origin := fx.RemoteURL()
	fixOriginManifest(t, origin, "fixture-space")
	seedThreadChainFixture(t, origin)

	store := buildThreadChainStore(t, origin)
	result, err := store.ThreadView(context.Background(), threadChainThread, "")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	if result.Order != cache.ThreadOrderCommitted {
		t.Fatalf("order = %q, want committed (a declared-order fallback would mean git history was unreadable)", result.Order)
	}

	questionIdx, responseIdx := -1, -1
	eventRows := 0
	for i, te := range result.Transcript {
		switch {
		case te.Kind == "artifact" && te.Artifact != nil && te.Artifact.ID == threadChainQuestionID:
			questionIdx = i
		case te.Kind == "artifact" && te.Artifact != nil && te.Artifact.ID == threadChainResponseID:
			responseIdx = i
		case te.Kind == "event":
			eventRows++
		}
	}
	if questionIdx == -1 || responseIdx == -1 {
		t.Fatalf("transcript missing a member: question at %d, response at %d; transcript=%+v", questionIdx, responseIdx, result.Transcript)
	}
	if questionIdx >= responseIdx {
		t.Fatalf("D3 regression: question (transcript[%d]) does not precede response (transcript[%d]); transcript=%+v", questionIdx, responseIdx, result.Transcript)
	}
	if eventRows == 0 {
		t.Fatalf("D2 regression: transcript carries zero event rows; transcript=%+v", result.Transcript)
	}

	if len(result.OpenItems) != 1 || result.OpenItems[0].ID != threadChainQuestionID {
		t.Fatalf("open_items = %+v, want exactly the question (the verified response is terminal)", result.OpenItems)
	}
	oi := result.OpenItems[0]
	wantWaiting := []string{"beta", "gamma"}
	if len(oi.WaitingOn) != len(wantWaiting) || oi.WaitingOn[0] != wantWaiting[0] || oi.WaitingOn[1] != wantWaiting[1] {
		t.Fatalf("waiting_on = %+v, want %+v (beta: close/supersede; gamma: respond again, D-017's multi-response allowance)", oi.WaitingOn, wantWaiting)
	}

	byRef, err := store.ThreadView(context.Background(), threadChainResponseID, "")
	if err != nil {
		t.Fatalf("ThreadView(%s): %v", threadChainResponseID, err)
	}
	if byRef.Thread != result.Thread || byRef.ResolvedFrom != threadChainResponseID {
		t.Fatalf("ThreadView(%s) = {Thread:%q ResolvedFrom:%q}, want {%q %q}",
			threadChainResponseID, byRef.Thread, byRef.ResolvedFrom, result.Thread, threadChainResponseID)
	}
}
