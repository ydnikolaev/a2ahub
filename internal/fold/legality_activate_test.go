package fold

import (
	"sort"
	"strings"
	"testing"
)

// TestCheckCandidateContractActivate is the regression for
// fb-20261106-28c7e6: `a2a contract activate` was refused LFC-001 at land
// for every published contract, while pendency named activate as the
// producer's owed move. activate is transition-free (transitionfree.go), so
// the pre-write check fell through to the role table, found no row and
// answered illegal-transition — the broadcast-ack defect's shape again.
//
// TEETH: delete the `kind == KindContract && transition == TActivate`
// branch from CheckCandidateWithSuccessor and owner_may_activate_* red with
// illegal-transition.
func TestCheckCandidateContractActivate(t *testing.T) {
	t.Parallel()

	env := Envelope{ID: "XC-acme-widget", Kind: KindContract, From: "acme", To: []string{"beta"}}
	published := Result{
		Kind:     KindContract,
		State:    StatePublished,
		Versions: map[string]State{"1.0.0": StatePublished, "1.0.1": StatePublished},
	}

	cases := []struct {
		name    string
		prior   Result
		version string
		actor   string
		status  MembershipStatus
		want    Verdict
	}{
		{name: "owner_may_activate_latest_published", prior: published, version: "1.0.1", actor: "acme", status: MembershipMember, want: VerdictLegal},
		{name: "owner_may_activate_versionless", prior: published, version: "", actor: "acme", status: MembershipMember, want: VerdictLegal},
		{name: "consumer_may_not_activate", prior: published, version: "1.0.1", actor: "beta", status: MembershipMember, want: VerdictUnauthorizedActor},
		{name: "stranger_may_not_activate", prior: published, version: "1.0.1", actor: "gamma", status: MembershipMember, want: VerdictUnauthorizedActor},
		{name: "departed_owner_may_not_activate", prior: published, version: "1.0.1", actor: "acme", status: MembershipLeft, want: VerdictUnauthorizedActor},
		{name: "unknown_membership_owner_may_not_activate", prior: published, version: "1.0.1", actor: "acme", status: MembershipUnknown, want: VerdictUnauthorizedActor},
		// applyContractActivation's predicate is the owner's role and
		// nothing else: it reads neither state nor version. The pre-write
		// check therefore must not refuse a version that was never
		// published, or the stricter of two guards silently wins again.
		// Whether that activation DISCHARGES the debt is a separate
		// question the mirror answers (activation.version must match the
		// latest publish), not a legality one.
		{name: "owner_activating_unrecorded_version_matches_the_fold", prior: published, version: "9.9.9", actor: "acme", status: MembershipMember, want: VerdictLegal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckCandidateWithSuccessor(KindContract, tc.prior, TActivate, tc.version, env, Actor{System: tc.actor}, tc.status, nil)
			if got != tc.want {
				t.Fatalf("CheckCandidateWithSuccessor(contract, published, activate, %q) by %s/%v = %q, want %q",
					tc.version, tc.actor, tc.status, got, tc.want)
			}
		})
	}

	// activate is transition-free on a CONTRACT only; on any other kind it
	// is an unmodelled transition on both sides.
	t.Run("activate_on_a_non_contract_is_illegal", func(t *testing.T) {
		t.Parallel()
		qenv := rowEnv(KindQuestion)
		got := CheckCandidateWithSuccessor(KindQuestion, Result{Kind: KindQuestion, State: StateInProgress}, TActivate, "", qenv, Actor{System: qenv.From}, MembershipMember, nil)
		if got != VerdictIllegalTransition {
			t.Fatalf("activate on a question = %q, want illegal-transition", got)
		}
	})
}

// TestEveryTransitionFreeHandlerAgreesWithCheckCandidate closes the CLASS,
// not the instance. transitionFreeHandlers is the one registry Apply
// dispatches transition-free events from, and each entry so far needed a
// hand-written pre-write branch beside it in legality.go. Broadcast-ack
// shipped without one (commit 325fd4a2), then contract activate did
// (fb-20261106-28c7e6): both times the fold stood ready to apply an event
// that the land refused.
//
// So every handler is probed, from every state its kind can sit in, by the
// owner, a target and a stranger at every membership status, and the
// pre-write verdict must be legal exactly when Apply raises no flag. A new
// transition-free handler without its legality branch reds here.
func TestEveryTransitionFreeHandlerAgreesWithCheckCandidate(t *testing.T) {
	t.Parallel()

	var problems []string
	probed := 0
	for key := range transitionFreeHandlers {
		kinds := []Kind{key.Kind}
		if key.Kind == "" {
			// An any-kind entry (note): probe every kind fold models.
			kinds = nil
			seen := map[Kind]bool{}
			for _, ks := range SubjectStates() {
				if !seen[ks.Kind] {
					seen[ks.Kind] = true
					kinds = append(kinds, ks.Kind)
				}
			}
		}
		for _, kind := range kinds {
			for _, ks := range SubjectStates() {
				if ks.Kind != kind {
					continue
				}
				env := rowEnv(kind)
				for _, actor := range []string{env.From, env.To0(), "stranger"} {
					for _, status := range []MembershipStatus{MembershipMember, MembershipLeft, MembershipUnknown} {
						probed++
						prior := Result{Kind: kind, State: ks.State}
						if kind == KindContract && ks.State != StateDraft {
							prior.Versions = map[string]State{"1.0.0": ks.State}
						}
						view := func(string) MembershipStatus { return status }
						event := Event{ULID: "01FREE0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: key.Transition, Version: "1.0.0", Actor: Actor{System: actor}}
						applied := Apply(kind, env, prior, event, view)
						postLegal := len(applied.Flags) == 0

						preLegal := CheckCandidateWithSuccessor(kind, prior, key.Transition, "1.0.0", env, Actor{System: actor}, status, nil) == VerdictLegal

						if preLegal != postLegal {
							problems = append(problems, string(kind)+"/"+string(ks.State)+" "+key.Transition+
								" by "+actor+" ("+membershipName(status)+"): pre-write legal="+boolWord(preLegal)+
								", Apply legal="+boolWord(postLegal))
						}
					}
				}
			}
		}
	}
	if probed == 0 {
		t.Fatal("no transition-free handler was probed — the registry or SubjectStates is empty, and a green result would mean nothing")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d transition-free probe(s) where the land and the fold disagree (%d probed):\n  %s",
			len(problems), probed, strings.Join(problems, "\n  "))
	}
}

func membershipName(s MembershipStatus) string {
	switch s {
	case MembershipMember:
		return "member"
	case MembershipLeft:
		return "left"
	default:
		return "unknown"
	}
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
