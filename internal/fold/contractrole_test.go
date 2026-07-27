package fold

import "testing"

// TestContractVersionRoleIsOneRule is the role dimension's agreement
// matrix, and it exists because this wave's early audit found the role
// dimension had none.
//
// The transition dimension was unified early — contractVersionVerdict is
// read by all three sites, and two agreement tests fail the moment they
// disagree. WHO may make the transition was not: RoleOwner sat as a bare
// literal at three call sites, and the audit measured what that costs by
// changing one of them to RoleTarget. It was caught, but only by accident:
// the property test happens to use the contract's owner as its actor, so
// the mutation surfaced as ~30 unrelated red subtests rather than as one
// test saying "these two sites disagree about who may publish". An
// accident is not a guard — the next mutation might be to a site the
// property test's fixtures do not exercise.
//
// So this test asks the three sites directly, over actors that are and are
// not the owner: does the pre-write check refuse exactly what the
// post-write fold flags, and does the move LegalNextFor advertises name
// the role both of them actually enforce?
func TestContractVersionRoleIsOneRule(t *testing.T) {
	t.Parallel()

	env := Envelope{ID: "XC-001", Kind: KindContract, From: "axon", To: []string{"seomatrix"}}
	member := func(string) MembershipStatus { return MembershipMember }

	actors := []struct {
		name       string
		system     string
		authorized bool
	}{
		{name: "the_owner", system: "axon", authorized: true},
		{name: "the_target_is_not_the_owner", system: "seomatrix", authorized: false},
		{name: "a_stranger", system: "unrelated", authorized: false},
	}

	// One prior per transition, each chosen so the TRANSITION is legal and
	// the only thing left to decide is the actor. Otherwise an illegal
	// transition would mask the role check at both sites and the test
	// would pass without ever exercising what it claims to.
	priors := []struct {
		transition string
		version    string
		prior      Result
	}{
		{transition: TPublish, version: "2.0.0", prior: Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}},
		{transition: TDeprecate, version: "1.0.0", prior: Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}},
		{transition: TRetire, version: "1.0.0", prior: Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated}}},
	}

	for _, p := range priors {
		for _, a := range actors {
			t.Run(p.transition+"_by_"+a.name, func(t *testing.T) {
				t.Parallel()
				actor := Actor{System: a.system}

				// Pre-write.
				verdict := CheckCandidate(KindContract, p.prior, p.transition, p.version, env, actor, MembershipMember)
				gotPre := verdict == VerdictLegal
				if gotPre != a.authorized {
					t.Fatalf("CheckCandidate(%s by %s) = %v, want legal=%v", p.transition, a.system, verdict, a.authorized)
				}

				// Post-write: the same decision, reached by the fold.
				after := Apply(KindContract, env, p.prior, Event{
					ULID: "01J0", Subject: env.ID, Transition: p.transition, Version: p.version, Actor: actor,
				}, member)
				unauthorized := false
				for _, f := range after.Flags {
					if f.Kind == FlagUnauthorizedActor {
						unauthorized = true
					}
				}
				if unauthorized == a.authorized {
					t.Fatalf("Apply(%s by %s) flagged unauthorized=%v, but CheckCandidate said legal=%v — the two sites disagree about who may act",
						p.transition, a.system, unauthorized, a.authorized)
				}

				// And the move LegalNextFor advertises must name the role
				// the other two enforce, not a role of its own.
				for _, m := range LegalNextFor(KindContract, p.prior, p.version) {
					if m.Transition != p.transition {
						continue
					}
					if !roleAuthorizes(m.Role, env, "axon") {
						t.Fatalf("LegalNextFor advertises %s with Role %q, which does not resolve to the owner the other two sites enforce", p.transition, m.Role)
					}
					if roleAuthorizes(m.Role, env, a.system) != a.authorized {
						t.Fatalf("LegalNextFor's Role %q resolves %s to %v; CheckCandidate/Apply resolve it to %v",
							m.Role, a.system, roleAuthorizes(m.Role, env, a.system), a.authorized)
					}
				}
			})
		}
	}
}
