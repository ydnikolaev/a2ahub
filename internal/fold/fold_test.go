package fold

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func alwaysMember(string) MembershipStatus { return MembershipMember }

func rowEnv(kind Kind) Envelope {
	return Envelope{
		ID:                "X-acme-fixture",
		Kind:              kind,
		From:              "acme",
		To:                []string{"beta"},
		RequiredApprovers: []string{"appr1", "appr2"},
	}
}

func actorFor(role Role, env Envelope) string {
	switch role {
	case RoleOwner:
		return env.From
	case RoleTarget:
		return env.To0()
	case RoleApprover:
		return env.RequiredApprovers[0]
	case RoleEitherParty:
		return env.From
	case RoleAny:
		return "anyone"
	default:
		return ""
	}
}

func rowName(i int, r Row) string {
	name := fmt.Sprintf("%03d_%s_%s_%s_to_%s", i, r.Kind, r.From, r.Transition, r.To)
	if r.Scenario != "" {
		name += "_" + r.Scenario
	}
	return strings.ReplaceAll(name, " ", "_")
}

// TestTransitionTable is spec 04 §8 AC 1 and (via its row-count meta-test
// below) AC 3: every §3.4.1-§3.4.7 row, exploded into rows.go's data
// table, gets exactly one subtest, and the folded `to` state is asserted
// against the table.
func TestTransitionTable(t *testing.T) {
	var mu sync.Mutex
	executed := 0
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if executed != len(rows) {
			t.Errorf("AC3 meta-test: executed %d subtests for %d table rows — row-count must equal exercised-subtest-count", executed, len(rows))
		}
	})

	for i, r := range rows {
		i, r := i, r
		t.Run(rowName(i, r), func(t *testing.T) {
			t.Parallel()
			mu.Lock()
			executed++
			mu.Unlock()

			switch {
			case r.Transition == TUnblock:
				testUnblockRow(t, r)
			case r.Kind == KindDecision && r.Transition == TApprove:
				testApproveRow(t, r)
			case r.Kind == KindResponse && (r.Transition == TVerify || r.Transition == TDispute):
				testResponseClosureRow(t, r)
			case r.Transition == TDispute:
				// The parent-level view of D-024's dispute reopen: same
				// event, same mechanism as the KindResponse "dispute"
				// row above — Apply always routes a dispute event
				// through the response-scoped path regardless of the
				// primary kind (spec's own callout: subject resolution
				// branches on transition name), so this row is
				// exercised via that same mechanism, asserting the
				// PARENT side of the effect.
				testParentDisputeRow(t, r)
			default:
				testGenericRow(t, r)
			}
		})
	}
}

func testGenericRow(t *testing.T, r Row) {
	t.Helper()
	env := rowEnv(r.Kind)
	actor := actorFor(r.Role, env)
	prior := Result{Kind: r.Kind, State: r.From}
	event := Event{ULID: "01FIXTURE0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: r.Transition, Actor: Actor{System: actor}}

	got := Apply(r.Kind, env, prior, event, alwaysMember)

	if got.State != r.To {
		t.Fatalf("state = %q, want %q (row %+v)", got.State, r.To, r)
	}
	if len(got.Flags) != 0 {
		t.Fatalf("unexpected flags on a legal row: %+v", got.Flags)
	}
}

func testUnblockRow(t *testing.T, r Row) {
	t.Helper()
	pre := State(strings.TrimPrefix(r.Scenario, "pre-block="))
	env := rowEnv(r.Kind)

	prior := Result{Kind: r.Kind, State: pre}
	blockEvent := Event{ULID: "01FIXTURE0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TBlock, Actor: Actor{System: "beta"}}
	blocked := Apply(r.Kind, env, prior, blockEvent, alwaysMember)
	if blocked.State != StateBlocked {
		t.Fatalf("after block: state = %q, want blocked", blocked.State)
	}
	if blocked.PreBlockState != pre {
		t.Fatalf("PreBlockState = %q, want %q", blocked.PreBlockState, pre)
	}

	unblockEvent := Event{ULID: "01FIXTURE0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TUnblock, Actor: Actor{System: "beta"}}
	final := Apply(r.Kind, env, blocked, unblockEvent, alwaysMember)
	if final.State != pre {
		t.Fatalf("after unblock: state = %q, want recovered pre-block state %q", final.State, pre)
	}
	if len(final.Flags) != 0 {
		t.Fatalf("unexpected flags: %+v", final.Flags)
	}
}

func testApproveRow(t *testing.T, r Row) {
	t.Helper()
	env := rowEnv(KindDecision)
	prior := Result{Kind: KindDecision, State: StateProposed}

	first := Event{ULID: "01FIXTURE0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TApprove, Actor: Actor{System: "appr1"}}
	afterFirst := Apply(KindDecision, env, prior, first, alwaysMember)
	if afterFirst.State != StateProposed {
		t.Fatalf("after first (non-quorum) approve: state = %q, want proposed", afterFirst.State)
	}

	switch r.Scenario {
	case "quorum-not-reached":
		if len(afterFirst.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", afterFirst.Flags)
		}
	case "quorum-reached":
		second := Event{ULID: "01FIXTURE0000000000000002", CommitSeq: 2, Subject: env.ID, Transition: TApprove, Actor: Actor{System: "appr2"}}
		afterSecond := Apply(KindDecision, env, afterFirst, second, alwaysMember)
		if afterSecond.State != StateApproved {
			t.Fatalf("after last required approve: state = %q, want approved", afterSecond.State)
		}
		if len(afterSecond.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", afterSecond.Flags)
		}
	default:
		t.Fatalf("unrecognised approve scenario %q", r.Scenario)
	}
}

func testParentDisputeRow(t *testing.T, r Row) {
	t.Helper()
	env := rowEnv(r.Kind)
	prior := Result{Kind: r.Kind, State: r.From, Responses: map[string]State{"XS-fixture": StateSubmitted}}

	event := Event{ULID: "01FIXTURE0000000000000001", CommitSeq: 1, Subject: "XS-fixture", Transition: TDispute, Actor: Actor{System: actorFor(r.Role, env)}}
	got := Apply(r.Kind, env, prior, event, alwaysMember)

	if got.State != r.To {
		t.Fatalf("parent state after dispute = %q, want %q", got.State, r.To)
	}
	if got.Responses["XS-fixture"] != StateDisputed {
		t.Fatalf("response sub-state after dispute = %q, want disputed", got.Responses["XS-fixture"])
	}
	if len(got.Flags) != 0 {
		t.Fatalf("unexpected flags: %+v", got.Flags)
	}
}

func testResponseClosureRow(t *testing.T, r Row) {
	t.Helper()
	env := Envelope{ID: "XQ-acme-fixture", Kind: KindQuestion, From: "acme", To: []string{"beta"}}
	prior := Result{Kind: KindQuestion, State: StateResponded, Responses: map[string]State{"XS-fixture": r.From}}

	event := Event{ULID: "01FIXTURE0000000000000001", CommitSeq: 1, Subject: "XS-fixture", Transition: r.Transition, Actor: Actor{System: "acme"}}
	got := Apply(KindQuestion, env, prior, event, alwaysMember)

	if got.Responses["XS-fixture"] != r.To {
		t.Fatalf("response sub-state = %q, want %q", got.Responses["XS-fixture"], r.To)
	}
	if r.Transition == TDispute {
		if got.State != StateInProgress {
			t.Fatalf("dispute must reopen parent responded->in_progress; got %q", got.State)
		}
	} else if got.State != StateResponded {
		t.Fatalf("verify must not change parent state; got %q", got.State)
	}
	if len(got.Flags) != 0 {
		t.Fatalf("unexpected flags: %+v", got.Flags)
	}
}
