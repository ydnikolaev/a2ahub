package fold

import "testing"

// TestCheckLegalityWrapperIsBitIdenticalToCheckCandidate pins plan
// decision 4: CheckLegality keeps its current signature and becomes a
// thin wrapper over CheckCandidate with a synthetic prior and an empty
// version. Driven across the SAME scenarios legality_test.go's
// TestCheckLegality already exercises, so a regression here is a
// regression against every one of CheckLegality's own existing callers,
// not a new behaviour this phase invents.
func TestCheckLegalityWrapperIsBitIdenticalToCheckCandidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		kind         Kind
		currentState State
		transition   string
		env          Envelope
		actor        Actor
		membership   MembershipStatus
	}{
		{name: "legal_question_acknowledge", kind: KindQuestion, currentState: StateSubmitted, transition: TAcknowledge, env: rowEnv(KindQuestion), actor: Actor{System: rowEnv(KindQuestion).To0()}, membership: MembershipMember},
		{name: "illegal_transition", kind: KindQuestion, currentState: StateClosed, transition: TRespond, env: rowEnv(KindQuestion), actor: Actor{System: rowEnv(KindQuestion).To0()}, membership: MembershipMember},
		{name: "unauthorized_actor", kind: KindQuestion, currentState: StateSubmitted, transition: TAcknowledge, env: rowEnv(KindQuestion), actor: Actor{System: "outsider"}, membership: MembershipMember},
		{name: "unblock_dynamic_row", kind: KindQuestion, currentState: StateBlocked, transition: TUnblock, env: rowEnv(KindQuestion), actor: Actor{System: rowEnv(KindQuestion).To0()}, membership: MembershipMember},
		{name: "decision_approve_dynamic_row", kind: KindDecision, currentState: StateProposed, transition: TApprove, env: rowEnv(KindDecision), actor: Actor{System: rowEnv(KindDecision).RequiredApprovers[0]}, membership: MembershipMember},
		{name: "announcement_ack_unaddressed_member", kind: KindAnnouncement, currentState: StatePublished, transition: TAcknowledge, env: rowEnv(KindAnnouncement), actor: Actor{System: "gamma"}, membership: MembershipMember},
		{name: "contract_publish_from_draft_legacy_path", kind: KindContract, currentState: StateDraft, transition: TPublish, env: Envelope{ID: "XC-fix", Kind: KindContract, From: "acme"}, actor: Actor{System: "acme"}, membership: MembershipMember},
		{name: "contract_successor_publish_after_deprecation_legacy_path", kind: KindContract, currentState: StateDeprecated, transition: TPublish, env: Envelope{ID: "XC-fix", Kind: KindContract, From: "acme"}, actor: Actor{System: "acme"}, membership: MembershipMember},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			legacy := CheckLegality(tc.kind, tc.currentState, tc.transition, tc.env, tc.actor, tc.membership)
			viaCandidate := CheckCandidate(tc.kind, Result{Kind: tc.kind, State: tc.currentState}, tc.transition, "", tc.env, tc.actor, tc.membership)
			if legacy != viaCandidate {
				t.Fatalf("CheckLegality = %q, CheckCandidate(synthetic prior, empty version) = %q — the wrapper must be bit-identical", legacy, viaCandidate)
			}
			if legacy == "" {
				t.Fatalf("both should return a real verdict")
			}
		})
	}
}

// TestCheckCandidateContractVersionPath is the pre-write mirror of
// contract_test.go's Apply-side tests, over the same rule
// (contractVersionVerdict) — driven directly, not indirectly via a fold.
func TestCheckCandidateContractVersionPath(t *testing.T) {
	t.Parallel()

	env := contractOwnerEnv()
	actor := contractOwnerActor()

	t.Run("publish_new_version_is_legal", func(t *testing.T) {
		t.Parallel()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
		if got := CheckCandidate(KindContract, prior, TPublish, "2.0.0", env, actor, MembershipMember); got != VerdictLegal {
			t.Fatalf("got %q, want legal", got)
		}
	})

	t.Run("publish_existing_version_is_illegal_regardless_of_its_state", func(t *testing.T) {
		t.Parallel()
		for _, existing := range []State{StatePublished, StateDeprecated, StateRetired} {
			t.Run(string(existing), func(t *testing.T) {
				t.Parallel()
				prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": existing}}
				if got := CheckCandidate(KindContract, prior, TPublish, "1.0.0", env, actor, MembershipMember); got != VerdictIllegalTransition {
					t.Fatalf("got %q, want illegal-transition", got)
				}
			})
		}
	})

	t.Run("deprecate_requires_published", func(t *testing.T) {
		t.Parallel()
		prior := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated}}
		if got := CheckCandidate(KindContract, prior, TDeprecate, "1.0.0", env, actor, MembershipMember); got != VerdictIllegalTransition {
			t.Fatalf("got %q, want illegal-transition", got)
		}
	})

	t.Run("retire_requires_deprecated", func(t *testing.T) {
		t.Parallel()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
		if got := CheckCandidate(KindContract, prior, TRetire, "1.0.0", env, actor, MembershipMember); got != VerdictIllegalTransition {
			t.Fatalf("got %q, want illegal-transition", got)
		}
	})

	t.Run("whole_deprecate_and_retire", func(t *testing.T) {
		t.Parallel()
		published := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
		if got := CheckCandidate(KindContract, published, TDeprecate, "", env, actor, MembershipMember); got != VerdictLegal {
			t.Fatalf("whole deprecate: got %q, want legal", got)
		}
		allDeprecated := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StateDeprecated}}
		if got := CheckCandidate(KindContract, allDeprecated, TRetire, "", env, actor, MembershipMember); got != VerdictLegal {
			t.Fatalf("whole retire (all deprecated): got %q, want legal", got)
		}
		notAllDeprecated := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StatePublished}}
		if got := CheckCandidate(KindContract, notAllDeprecated, TRetire, "", env, actor, MembershipMember); got != VerdictIllegalTransition {
			t.Fatalf("whole retire (not all deprecated): got %q, want illegal-transition", got)
		}
	})

	t.Run("authorization_still_required", func(t *testing.T) {
		t.Parallel()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
		if got := CheckCandidate(KindContract, prior, TDeprecate, "1.0.0", env, Actor{System: "mallory"}, MembershipMember); got != VerdictUnauthorizedActor {
			t.Fatalf("got %q, want unauthorized-actor", got)
		}
	})

	t.Run("version_less_fallback_when_no_versions_recorded", func(t *testing.T) {
		t.Parallel()
		prior := Result{Kind: KindContract, State: StateDraft}
		// No Versions recorded and no version named: falls through to the
		// legacy table lookup — draft->publish is legal there.
		if got := CheckCandidate(KindContract, prior, TPublish, "", env, actor, MembershipMember); got != VerdictLegal {
			t.Fatalf("got %q, want legal (legacy fallback)", got)
		}
	})
}

// TestCheckCandidateAgreesWithApplyOnLegality is this package's own
// pre/post agreement idiom (legality_test.go's
// TestCheckLegalityBroadcastAck/pre_write_and_post_write_agree): the
// pre-write CheckCandidate verdict for a contract version transition must
// agree with whether Apply, given the identical prior, actually flags the
// same event illegal — never a second, silently-diverging reading of
// contractVersionVerdict.
func TestCheckCandidateAgreesWithApplyOnLegality(t *testing.T) {
	t.Parallel()

	env := contractOwnerEnv()
	actor := contractOwnerActor()

	cases := []struct {
		name       string
		versions   map[string]State
		transition string
		version    string
	}{
		{name: "publish_new_version", versions: map[string]State{"1.0.0": StatePublished}, transition: TPublish, version: "2.0.0"},
		{name: "publish_existing_version", versions: map[string]State{"1.0.0": StatePublished}, transition: TPublish, version: "1.0.0"},
		{name: "deprecate_published_version", versions: map[string]State{"1.0.0": StatePublished}, transition: TDeprecate, version: "1.0.0"},
		{name: "deprecate_not_published_version", versions: map[string]State{"1.0.0": StateDeprecated}, transition: TDeprecate, version: "1.0.0"},
		{name: "retire_deprecated_version", versions: map[string]State{"1.0.0": StateDeprecated}, transition: TRetire, version: "1.0.0"},
		{name: "retire_not_deprecated_version", versions: map[string]State{"1.0.0": StatePublished}, transition: TRetire, version: "1.0.0"},
		{name: "whole_deprecate_with_published", versions: map[string]State{"1.0.0": StatePublished}, transition: TDeprecate, version: ""},
		{name: "whole_deprecate_without_published", versions: map[string]State{"1.0.0": StateDeprecated}, transition: TDeprecate, version: ""},
		{name: "whole_retire_all_deprecated", versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StateDeprecated}, transition: TRetire, version: ""},
		{name: "whole_retire_not_all_deprecated", versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StatePublished}, transition: TRetire, version: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			priorForCheck := Result{Kind: KindContract, State: projectContractState(tc.versions), Versions: tc.versions}
			verdict := CheckCandidate(KindContract, priorForCheck, tc.transition, tc.version, env, actor, MembershipMember)

			priorForApply := Result{Kind: KindContract, State: projectContractState(tc.versions), Versions: copyStateMap(tc.versions)}
			event := Event{ULID: "01AGREE0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: tc.transition, Version: tc.version, Actor: actor}
			got := Apply(KindContract, env, priorForApply, event, alwaysMember)
			applyLegal := len(got.Flags) == 0

			checkLegal := verdict == VerdictLegal
			if checkLegal != applyLegal {
				t.Fatalf("CheckCandidate says legal=%v but Apply says legal=%v (flags=%+v) for the identical prior/event", checkLegal, applyLegal, got.Flags)
			}
		})
	}
}
