package e2e

import "testing"

// blockedByOwnerThread is this fixture's own thread — deliberately new and
// deliberately beta/gamma-rooted (never axon), the same discipline
// thread_chain_test.go documents: every T3 script runs AS axon, and content
// addressed to/from axon changes axon's own inbox/outbox/statusline
// (inbox_outbox.txtar, statusline.txtar hard-code axon's counts). A thread
// axon has no part in leaves both undisturbed.
const blockedByOwnerThread = "thread:beta-20260723-blk1"

const (
	blockedByOwnerWorkRequestID = "XW-beta-20260723-blk1"
	blockedByOwnerResponseID    = "XS-gamma-20260723-blk2"
)

// writeBlockedByOwnerWorkRequest seeds the chain's opening work_request:
// beta asks gamma. RoleOwner (beta) submits, RoleTarget (gamma) acknowledges
// then blocks it — exchangeRows' own TBlock row (internal/fold/table.go).
func writeBlockedByOwnerWorkRequest(t *testing.T, mirrorDir string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + blockedByOwnerWorkRequestID + "\n" +
		"type: work_request\n" +
		"title: replay the C0 fixture transcript\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [gamma]\n" +
		"thread: " + blockedByOwnerThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-23T09:00:00Z\n" +
		"category: investigation\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"interim_behavior: \"requester waits\"\n" +
		"acceptance_criteria:\n" +
		"  - \"divergences reported\"\n" +
		"classification: internal\n" +
		"---\nreplay the C0 fixture transcript.\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+blockedByOwnerWorkRequestID+".md", content)
}

// writeBlockedByOwnerResponse seeds gamma's response naming BETA — the
// work_request's own `from`, hence a legal `note` actor (fold.CheckLegality,
// RoleEitherParty) — as the system the wait is actually on
// (`blocked_by.owner`, response/v2's own field, P1 US-3): "the requester is
// what blocks me", the canonical case blockedRow's own doc comment names.
// Committed WITHOUT a `respond` event on the work_request's own subject —
// deliberately: this fixture asserts the OpenItem for the work_request
// while it is still folded `blocked` (the block event below), not
// `responded`; a `respond` event would move it past the state this
// scenario exists to observe.
func writeBlockedByOwnerResponse(t *testing.T, mirrorDir string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + blockedByOwnerResponseID + "\n" +
		"type: response\n" +
		"title: Response to " + blockedByOwnerWorkRequestID + "\n" +
		"space: fixture-space\n" +
		"from: gamma\n" +
		"to: [beta]\n" +
		"thread: " + blockedByOwnerThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-23T11:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"parent: " + blockedByOwnerWorkRequestID + "\n" +
		"result: partial\n" +
		"unmet: [0]\n" +
		"blocked_by: {reason_code: split-required, owner: beta, needs: decision}\n" +
		"classification: internal\n" +
		"---\nblocked on beta's own decision; see blocked_by above.\n"
	writeMirrorFile(t, mirrorDir, "gamma/exchanges/"+blockedByOwnerResponseID+".md", content)
}

// seedBlockedByOwnerFixture pushes README criterion 8's own fixture chain
// (agent-exchange-2026-08 P12 G4) to originDir's main branch as three
// separate commits: beta asks, gamma acknowledges then blocks, gamma names
// beta as the actual blocker. digest_possession_test.go / thread_chain_test.
// go's own seedThreadChainFixture is the precedent this follows — one
// commit per real turn, never a bulk write, so the folded STATE the
// scenario needs (`blocked`, via a real `block` event) is reached the same
// way a live space would reach it.
func seedBlockedByOwnerFixture(t *testing.T, originDir string) {
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
		writeBlockedByOwnerWorkRequest(t, dir)
		writeLifecycleEvent(t, dir, "beta", 20, blockedByOwnerWorkRequestID, "submit", "beta")
	}, "blocked-owner-fixture: beta asks")

	commit(func(dir string) {
		writeLifecycleEvent(t, dir, "gamma", 21, blockedByOwnerWorkRequestID, "acknowledge", "gamma")
		writeLifecycleEvent(t, dir, "gamma", 22, blockedByOwnerWorkRequestID, "block", "gamma")
	}, "blocked-owner-fixture: gamma blocks")

	commit(func(dir string) {
		writeBlockedByOwnerResponse(t, dir)
	}, "blocked-owner-fixture: gamma names beta as the actual blocker")
}
