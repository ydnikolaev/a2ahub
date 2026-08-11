package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestThreadJSONMarksTransitionFreeOnlyOnTransitionFreeEvents is P0's AC3,
// and it exists because the criterion named a verify method that was never
// built.
//
// specs/00-derived-verdicts.md §8 row 3 says to verify AC3 with
// "internal/e2e/thread_transitionfree_test.go". `git log --all` over that path
// is empty: the file never existed, in any commit, in any branch. The 2026-08-11
// pre-release audit found it while checking what actually proves each criterion.
//
// The BEHAVIOUR was real the whole time — `TranscriptEvent.TransitionFree` is
// `json:"transition_free,omitempty"` and is set from `fold.TransitionFree`, the
// same registry Apply dispatches on. What did not exist was anything asserting
// it through the built binary, which is exactly the tier the criterion asks
// for. The closest thing (internal/html's projection reflection test) proves the
// field reaches a Go type and says nothing about the rendered JSON.
//
// The `omitempty` half is the point, and it is why a "does the key equal false"
// test would not do. AC3 asks that the key be ABSENT on a state-moving event,
// not present-and-false: a reader distinguishing "this event changed nothing"
// from "this event moved the artifact" reads presence, and a false-valued key
// would make every ordinary event look like it had been considered and cleared.
// So this decodes into map[string]any and asks whether the KEY is there.
func TestThreadJSONMarksTransitionFreeOnlyOnTransitionFreeEvents(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	id := "XQ-axon-20260811-tf01"
	r.stageQuestion(id, "beta")
	r.mustRun("submit", id)

	// `note` is the canonical transition-free move: it changes no state on any
	// kind. `accept` moves the artifact, and is the control — without one, a
	// test asserting only the note's key could pass against a binary that
	// stamped `transition_free` onto everything.
	// Each write must LAND before the next is judged: the funnel refuses a
	// second move while the previous one is pending-merge (LFC-001), which is
	// correct and is why a sync sits between them.
	beta := r.peer("beta")
	beta.mustRun("sync")
	beta.mustRun("ack", id)
	beta.mustRun("sync")
	beta.mustRun("accept", id)
	beta.mustRun("sync")
	beta.mustRun("note", id, "--note", "reading this now")

	r.mustRun("sync")
	out := r.mustRun("thread", e2eFixtureThread, "--json")

	// The payload nests each event under a transcript entry (cache.ThreadView's
	// own shape); decoding into map[string]any at the EVENT level is what lets
	// the omitempty check below ask about key PRESENCE rather than value.
	var thread struct {
		Transcript []struct {
			Event map[string]any `json:"event"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal([]byte(out), &thread); err != nil {
		// The thread payload's shape is not this test's subject; if it moves,
		// say so plainly rather than failing on a nil map further down.
		t.Fatalf("decode `a2a thread --json`: %v\n%s", err, out)
	}
	if len(thread.Transcript) == 0 {
		t.Fatalf("thread carries no transcript — the fixture never landed:\n%s", out)
	}

	var sawNote, sawMoving bool
	for _, entry := range thread.Transcript {
		ev := entry.Event
		if ev == nil {
			continue // an artifact or derived row, not an event
		}
		transition, _ := ev["transition"].(string)
		_, keyPresent := ev["transition_free"]
		switch transition {
		case "note":
			sawNote = true
			if !keyPresent {
				t.Errorf("a `note` event omits transition_free; AC3 requires it present and true, because a reader cannot otherwise tell a comment from a move: %+v", ev)
			} else if ev["transition_free"] != true {
				t.Errorf("a `note` event carries transition_free=%v, want true: %+v", ev["transition_free"], ev)
			}
		case "":
			// Not an event row shape this test knows; ignore rather than guess.
		default:
			sawMoving = true
			if keyPresent {
				t.Errorf("a state-moving `%s` event CARRIES the transition_free key; AC3 requires it omitted, so presence itself is the signal: %+v", transition, ev)
			}
		}
	}

	// Both arms must have run. A test that silently exercised neither would be
	// green forever — the defect class this epic kept finding.
	if !sawNote {
		t.Fatalf("no `note` event reached the transcript; the transition-free arm asserted nothing:\n%s", out)
	}
	if !sawMoving {
		t.Fatalf("no state-moving event reached the transcript; the control arm asserted nothing:\n%s", out)
	}
	if strings.Count(out, "transition_free") == 0 {
		t.Fatalf("the rendered payload names transition_free nowhere at all:\n%s", out)
	}
}
