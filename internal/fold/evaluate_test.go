package fold

import (
	"reflect"
	"strings"
	"testing"
)

func TestEvaluateCandidateScopesAndOutcomes(t *testing.T) {
	t.Parallel()

	questionEnv := rowEnv(KindQuestion)
	decisionEnv := rowEnv(KindDecision)
	contractEnv := contractOwnerEnv()
	announcementEnv := rowEnv(KindAnnouncement)

	tests := []struct {
		name           string
		kind           Kind
		env            Envelope
		prior          Result
		event          Event
		wantApplicable bool
		wantScope      EvaluationScope
		wantOutcome    State
	}{
		{
			name: "ordinary_primary_transition",
			kind: KindQuestion, env: questionEnv,
			prior:          Result{Kind: KindQuestion, State: StateSubmitted},
			event:          Event{ULID: "01EVALUATE000000000000001", Subject: questionEnv.ID, Transition: TAcknowledge, Actor: Actor{System: questionEnv.To0()}},
			wantApplicable: true,
			wantScope:      EvaluationScope{Kind: EvaluationScopePrimary, Subject: questionEnv.ID},
			wantOutcome:    StateAcknowledged,
		},
		{
			name: "dynamic_unblock_outcome",
			kind: KindQuestion, env: questionEnv,
			prior:          Result{Kind: KindQuestion, State: StateBlocked, PreBlockState: StateAccepted},
			event:          Event{ULID: "01EVALUATE000000000000002", Subject: questionEnv.ID, Transition: TUnblock, Actor: Actor{System: questionEnv.To0()}},
			wantApplicable: true,
			wantScope:      EvaluationScope{Kind: EvaluationScopePrimary, Subject: questionEnv.ID},
			wantOutcome:    StateAccepted,
		},
		{
			name: "decision_approval_before_quorum",
			kind: KindDecision, env: decisionEnv,
			prior:          Result{Kind: KindDecision, State: StateProposed},
			event:          Event{ULID: "01EVALUATE000000000000003", Subject: decisionEnv.ID, Transition: TApprove, Actor: Actor{System: decisionEnv.RequiredApprovers[0]}},
			wantApplicable: true,
			wantScope:      EvaluationScope{Kind: EvaluationScopePrimary, Subject: decisionEnv.ID},
			wantOutcome:    StateProposed,
		},
		{
			name: "decision_approval_reaching_quorum",
			kind: KindDecision, env: decisionEnv,
			prior:          Result{Kind: KindDecision, State: StateProposed, Approvals: map[string]bool{decisionEnv.RequiredApprovers[0]: true}},
			event:          Event{ULID: "01EVALUATE000000000000004", Subject: decisionEnv.ID, Transition: TApprove, Actor: Actor{System: decisionEnv.RequiredApprovers[1]}},
			wantApplicable: true,
			wantScope:      EvaluationScope{Kind: EvaluationScopePrimary, Subject: decisionEnv.ID},
			wantOutcome:    StateApproved,
		},
		{
			name: "response_verify",
			kind: KindQuestion, env: questionEnv,
			prior:          Result{Kind: KindQuestion, State: StateResponded, Responses: map[string]State{"XS-answer": StateSubmitted}},
			event:          Event{ULID: "01EVALUATE000000000000005", Subject: "XS-answer", Transition: TVerify, Actor: Actor{System: questionEnv.From}},
			wantApplicable: true,
			wantScope:      EvaluationScope{Kind: EvaluationScopeResponse, Subject: "XS-answer"},
			wantOutcome:    StateVerified,
		},
		{
			name: "response_dispute_is_multi_scope",
			kind: KindQuestion, env: questionEnv,
			prior: Result{Kind: KindQuestion, State: StateResponded, Responses: map[string]State{"XS-answer": StateSubmitted}},
			event: Event{ULID: "01EVALUATE000000000000006", Subject: "XS-answer", Transition: TDispute, Actor: Actor{System: questionEnv.From}},
		},
		{
			name: "respond_is_multi_scope",
			kind: KindQuestion, env: questionEnv,
			prior: Result{Kind: KindQuestion, State: StateAccepted},
			event: Event{ULID: "01EVALUATE000000000000007", Subject: questionEnv.ID, Transition: TRespond, ResponseID: "XS-answer", Actor: Actor{System: questionEnv.To0()}},
		},
		{
			name: "contract_version",
			kind: KindContract, env: contractEnv,
			prior:          Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished, "2.0.0": StatePublished}},
			event:          Event{ULID: "01EVALUATE000000000000008", Subject: contractEnv.ID, Transition: TDeprecate, Version: "1.0.0", Actor: contractOwnerActor()},
			wantApplicable: true,
			wantScope:      EvaluationScope{Kind: EvaluationScopeContractVersion, Subject: contractEnv.ID, Version: "1.0.0"},
			wantOutcome:    StateDeprecated,
		},
		{
			name: "whole_contract_transition",
			kind: KindContract, env: contractEnv,
			prior: Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished, "2.0.0": StatePublished}},
			event: Event{ULID: "01EVALUATE000000000000009", Subject: contractEnv.ID, Transition: TDeprecate, Actor: contractOwnerActor()},
		},
		{
			name: "transition_free_note",
			kind: KindQuestion, env: questionEnv,
			prior: Result{Kind: KindQuestion, State: StateClosed},
			event: Event{ULID: "01EVALUATE000000000000010", Subject: questionEnv.ID, Transition: TNote, Actor: Actor{System: questionEnv.From}},
		},
		{
			name: "transition_free_broadcast_ack",
			kind: KindAnnouncement, env: announcementEnv,
			prior: Result{Kind: KindAnnouncement, State: StatePublished},
			event: Event{ULID: "01EVALUATE000000000000011", Subject: announcementEnv.ID, Transition: TAcknowledge, Actor: Actor{System: "consumer"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := EvaluateCandidate(tc.kind, tc.prior, tc.event, tc.env, alwaysMember)
			if got.Verdict != VerdictLegal {
				t.Fatalf("Verdict = %q, want legal", got.Verdict)
			}
			if got.Applicable != tc.wantApplicable {
				t.Fatalf("Applicable = %v, want %v", got.Applicable, tc.wantApplicable)
			}
			if got.Scope != tc.wantScope {
				t.Fatalf("Scope = %+v, want %+v", got.Scope, tc.wantScope)
			}
			if got.Outcome != tc.wantOutcome {
				t.Fatalf("Outcome = %q, want %q", got.Outcome, tc.wantOutcome)
			}

			wantResult := Apply(tc.kind, tc.env, tc.prior, tc.event, alwaysMember)
			if !reflect.DeepEqual(got.Result, wantResult) {
				t.Fatalf("Result differs from Apply\n got:  %+v\n want: %+v", got.Result, wantResult)
			}
		})
	}
}

func TestEvaluateCandidateVerdictAndRejectedClone(t *testing.T) {
	t.Parallel()

	env := rowEnv(KindQuestion)
	prior := Result{
		Kind: KindQuestion, State: StateSubmitted, PreBlockState: StateAccepted,
		Responses: map[string]State{"XS-existing": StateVerified},
		Acks:      map[string]bool{"consumer": true},
		Approvals: map[string]bool{"approver": true},
		Versions:  map[string]State{"1.0.0": StatePublished},
		Flags:     []Flag{{Kind: FlagIllegalTransition, EventULID: "earlier", Subject: env.ID}},
		Applied:   map[string]bool{"earlier": true},
	}

	tests := []struct {
		name       string
		event      Event
		membership MembershipView
		want       Verdict
	}{
		{
			name:       "illegal_transition",
			event:      Event{ULID: "01EVALUATEREJECT00000001", Subject: env.ID, Transition: TClose, Actor: Actor{System: env.From}},
			membership: alwaysMember,
			want:       VerdictIllegalTransition,
		},
		{
			name:       "unauthorized_actor",
			event:      Event{ULID: "01EVALUATEREJECT00000002", Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: "outsider"}},
			membership: alwaysMember,
			want:       VerdictUnauthorizedActor,
		},
		{
			name:       "missing_membership_view_fails_closed",
			event:      Event{ULID: "01EVALUATEREJECT00000003", Subject: env.ID, Transition: TAcknowledge, Actor: Actor{System: env.To0()}},
			membership: nil,
			want:       VerdictUnauthorizedActor,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := EvaluateCandidate(KindQuestion, prior, tc.event, env, tc.membership)
			if got.Verdict != tc.want {
				t.Fatalf("Verdict = %q, want %q", got.Verdict, tc.want)
			}
			if got.Applicable || got.Scope != (EvaluationScope{}) || got.Outcome != StateNone {
				t.Fatalf("rejected candidate returned receipt data: applicable=%v scope=%+v outcome=%q", got.Applicable, got.Scope, got.Outcome)
			}
			if !reflect.DeepEqual(got.Result, prior) {
				t.Fatalf("rejected Result is not semantically equal to prior\n got:  %+v\n prior: %+v", got.Result, prior)
			}

			got.Result.Responses["XS-existing"] = StateDisputed
			got.Result.Acks["new"] = true
			got.Result.Approvals["new"] = true
			got.Result.Versions["1.0.0"] = StateRetired
			got.Result.Flags[0].Subject = "changed"
			got.Result.Applied["new"] = true
			if prior.Responses["XS-existing"] != StateVerified || prior.Acks["new"] || prior.Approvals["new"] ||
				prior.Versions["1.0.0"] != StatePublished || prior.Flags[0].Subject != env.ID || prior.Applied["new"] {
				t.Fatalf("rejected Result aliases prior: %+v", prior)
			}
		})
	}
}

func TestEvaluateCandidatePropertiesAcrossTransitionRows(t *testing.T) {
	t.Parallel()

	for i, row := range rows {
		kind, env, prior, event := evaluationFixtureForRow(i, row)
		got := EvaluateCandidate(kind, prior, event, env, alwaysMember)

		checkPrior := prior
		if event.Transition == TVerify || event.Transition == TDispute {
			checkPrior = Result{Kind: KindResponse, State: prior.Responses[event.Subject]}
		}
		wantVerdict := CheckCandidate(kind, checkPrior, event.Transition, event.Version, env, event.Actor, MembershipMember)
		if got.Verdict != wantVerdict {
			t.Fatalf("row %d (%+v): Verdict = %q, CheckCandidate = %q", i, row, got.Verdict, wantVerdict)
		}
		if got.Verdict != VerdictLegal {
			t.Fatalf("row %d (%+v): fixture must be legal, got %q", i, row, got.Verdict)
		}
		wantResult := Apply(kind, env, prior, event, alwaysMember)
		if !reflect.DeepEqual(got.Result, wantResult) {
			t.Fatalf("row %d (%+v): evaluator Result differs from Apply", i, row)
		}

		wantApplicable := row.Transition != TRespond && row.Transition != TDispute &&
			!(row.Kind == KindContract && (row.Transition == TDeprecate || row.Transition == TRetire))
		if got.Applicable != wantApplicable {
			t.Fatalf("row %d (%+v): Applicable = %v, want %v", i, row, got.Applicable, wantApplicable)
		}
		if got.Applicable {
			if got.Scope.Kind == "" || got.Scope.Subject == "" || got.Outcome == StateNone {
				t.Fatalf("row %d (%+v): applicable evaluation is incomplete: %+v", i, row, got)
			}
		} else if got.Scope != (EvaluationScope{}) || got.Outcome != StateNone {
			t.Fatalf("row %d (%+v): N/A evaluation returned scope/outcome: %+v", i, row, got)
		}
	}
}

func TestEvaluateCandidateIsPureDeterministicAndReplaySafe(t *testing.T) {
	t.Parallel()

	env := rowEnv(KindQuestion)
	prior := Result{
		Kind: KindQuestion, State: StateAccepted,
		Responses: map[string]State{"XS-old": StateVerified},
		Acks:      map[string]bool{"consumer": true},
		Approvals: map[string]bool{"approver": true},
		Flags:     []Flag{{Kind: FlagStateClaimMismatch, EventULID: "old", Subject: env.ID}},
		Applied:   map[string]bool{"old": true},
	}
	event := Event{
		ULID: "01EVALUATEPURE0000000001", Subject: env.ID, Transition: TBlock,
		ClaimedState: StateClosed, Actor: Actor{System: env.To0()},
	}

	priorSnapshot := prior.clone()
	envSnapshot := Envelope{
		ID: env.ID, Kind: env.Kind, From: env.From,
		To: append([]string(nil), env.To...), RequiredApprovers: append([]string(nil), env.RequiredApprovers...),
	}
	eventSnapshot := event

	first := EvaluateCandidate(KindQuestion, prior, event, env, alwaysMember)
	second := EvaluateCandidate(KindQuestion, prior, event, env, alwaysMember)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated evaluation with identical inputs diverged\n first:  %+v\n second: %+v", first, second)
	}
	if !reflect.DeepEqual(prior, priorSnapshot) || !reflect.DeepEqual(env, envSnapshot) || event != eventSnapshot {
		t.Fatalf("EvaluateCandidate mutated an input\n prior: %+v\n env: %+v\n event: %+v", prior, env, event)
	}

	want := Apply(KindQuestion, env, prior, event, alwaysMember)
	if !reflect.DeepEqual(first.Result, want) {
		t.Fatalf("Result differs from Apply\n got:  %+v\n want: %+v", first.Result, want)
	}
	replayed := Apply(KindQuestion, env, first.Result, event, alwaysMember)
	if !reflect.DeepEqual(replayed, first.Result) {
		t.Fatalf("replaying the evaluated candidate changed its Result\n before: %+v\n after:  %+v", first.Result, replayed)
	}
}

func evaluationFixtureForRow(i int, row Row) (Kind, Envelope, Result, Event) {
	env := rowEnv(row.Kind)
	kind := row.Kind
	prior := Result{Kind: row.Kind, State: row.From}
	event := Event{
		ULID:    "01EVALUATEROW" + strings.Repeat("0", 12) + string(rune('A'+i%26)),
		Subject: env.ID, Transition: row.Transition,
		Actor: Actor{System: actorFor(row.Role, env)},
	}

	switch {
	case row.Transition == TUnblock:
		prior.PreBlockState = State(strings.TrimPrefix(row.Scenario, "pre-block="))
	case row.Kind == KindDecision && row.Transition == TApprove && row.Scenario == "quorum-reached":
		prior.Approvals = map[string]bool{env.RequiredApprovers[0]: true}
		event.Actor.System = env.RequiredApprovers[1]
	case row.Kind == KindResponse && (row.Transition == TVerify || row.Transition == TDispute):
		kind = KindQuestion
		env = rowEnv(KindQuestion)
		prior = Result{Kind: KindQuestion, State: StateResponded, Responses: map[string]State{"XS-row": row.From}}
		event.Subject = "XS-row"
		event.Actor.System = env.From
	case row.Transition == TDispute:
		prior.Responses = map[string]State{"XS-row": StateSubmitted}
		event.Subject = "XS-row"
		event.Actor.System = env.From
	}

	return kind, env, prior, event
}
