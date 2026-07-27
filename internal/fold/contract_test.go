package fold

import "testing"

// contractOwnerEnv/contractOwnerActor are this file's shared fixtures for
// a contract's own envelope/actor — RoleOwner resolves to env.From for
// every publish/deprecate/retire event, per the plan's "Authorization is
// RoleOwner ... exactly as the table rows already say".
func contractOwnerEnv() Envelope {
	return Envelope{ID: "XC-acme-widget", Kind: KindContract, From: "acme"}
}

func contractOwnerActor() Actor {
	return Actor{Kind: "agent", Name: "bot", System: "acme"}
}

// TestApplyContractScopedFallback pins THE CRITICAL FALLBACK: a
// version-less event against a contract with no recorded versions yet
// defers to today's table path unchanged — this is the whole migration
// guarantee (spec §5.1), asserted directly against applyContractScoped's
// own entry condition rather than only indirectly via the property test.
func TestApplyContractScopedFallback(t *testing.T) {
	t.Parallel()

	t.Run("version_less_publish_against_empty_versions_uses_the_table_path", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateDraft}
		event := Event{ULID: "01FALLBACK00000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if got.State != StatePublished {
			t.Fatalf("state = %q, want published (via the legacy table path)", got.State)
		}
		if len(got.Versions) != 0 {
			t.Fatalf("Versions must stay empty on the fallback path, got %+v", got.Versions)
		}
		if len(got.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", got.Flags)
		}
	})

	t.Run("a_version_carrying_event_never_hits_the_fallback_even_on_first_publish", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateDraft}
		event := Event{ULID: "01FALLBACK00000000000002", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: "1.0.0", Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if got.Versions["1.0.0"] != StatePublished {
			t.Fatalf("Versions[1.0.0] = %q, want published — a version-carrying event must go through the per-version path even as the very first event", got.Versions["1.0.0"])
		}
		if got.State != StatePublished {
			t.Fatalf("projected State = %q, want published", got.State)
		}
	})

	t.Run("once_any_version_is_recorded_a_version_less_event_no_longer_falls_back", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
		// A version-less publish once Versions is non-empty has no meaning
		// in this model — contractVersionVerdict refuses it (illegal), it
		// does NOT fall back to the table (which would have accepted
		// published->publish as a no-op).
		event := Event{ULID: "01FALLBACK00000000000003", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if got.Versions["1.0.0"] != StatePublished {
			t.Fatalf("existing version must be untouched: %+v", got.Versions)
		}
		if _, exists := got.Versions[""]; exists {
			t.Fatalf("a version-less publish must never write an empty-string key: %+v", got.Versions)
		}
		assertFlag(t, got, FlagIllegalTransition, event.ULID)
	})
}

// TestApplyContractScopedPublish pins publish v's rule (plan §3): illegal
// if Versions[v] is already set (any state), legal and sets `published`
// otherwise.
func TestApplyContractScopedPublish(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		versions map[string]State
		version  string
		wantFlag bool
	}{
		{name: "new_version_is_legal", versions: map[string]State{"1.0.0": StatePublished}, version: "2.0.0", wantFlag: false},
		{name: "republish_of_published_version_is_illegal", versions: map[string]State{"1.0.0": StatePublished}, version: "1.0.0", wantFlag: true},
		{name: "republish_of_deprecated_version_is_illegal", versions: map[string]State{"1.0.0": StateDeprecated}, version: "1.0.0", wantFlag: true},
		{name: "republish_of_retired_version_is_illegal", versions: map[string]State{"1.0.0": StateRetired}, version: "1.0.0", wantFlag: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := contractOwnerEnv()
			prior := Result{Kind: KindContract, State: StatePublished, Versions: tc.versions}
			event := Event{ULID: "01PUBLISH0000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: tc.version, Actor: contractOwnerActor()}

			got := Apply(KindContract, env, prior, event, alwaysMember)

			if tc.wantFlag {
				assertFlag(t, got, FlagIllegalTransition, event.ULID)
				if got.Versions[tc.version] == StatePublished && tc.versions[tc.version] != StatePublished {
					t.Fatalf("an illegal publish must not mutate Versions: %+v", got.Versions)
				}
			} else {
				if len(got.Flags) != 0 {
					t.Fatalf("unexpected flags: %+v", got.Flags)
				}
				if got.Versions[tc.version] != StatePublished {
					t.Fatalf("Versions[%s] = %q, want published", tc.version, got.Versions[tc.version])
				}
			}
		})
	}
}

// TestApplyContractScopedDeprecate pins deprecate v's rule: requires
// Versions[v] == published, else illegal; sets deprecated. Plus the
// version-less WHOLE form (03-domain.md §3.4.1).
func TestApplyContractScopedDeprecate(t *testing.T) {
	t.Parallel()

	t.Run("scoped_deprecate_requires_published", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated}}
		event := Event{ULID: "01DEPRECATE000000000001", CommitSeq: 1, Subject: env.ID, Transition: TDeprecate, Version: "1.0.0", Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		assertFlag(t, got, FlagIllegalTransition, event.ULID)
		if got.Versions["1.0.0"] != StateDeprecated {
			t.Fatalf("an illegal deprecate must not mutate the version: %+v", got.Versions)
		}
	})

	t.Run("scoped_deprecate_of_published_version_succeeds_and_leaves_others_untouched", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished, "2.0.0": StatePublished}}
		event := Event{ULID: "01DEPRECATE000000000002", CommitSeq: 1, Subject: env.ID, Transition: TDeprecate, Version: "1.0.0", Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", got.Flags)
		}
		if got.Versions["1.0.0"] != StateDeprecated {
			t.Fatalf("Versions[1.0.0] = %q, want deprecated", got.Versions["1.0.0"])
		}
		if got.Versions["2.0.0"] != StatePublished {
			t.Fatalf("2.0.0 must be untouched by a scoped deprecate of 1.0.0: %+v", got.Versions)
		}
		// 2.0.0 is still published, so the SUBJECT-level projection stays
		// `published` — this is AC-7/US-2's whole point.
		if got.State != StatePublished {
			t.Fatalf("projected subject state = %q, want published (2.0.0 is still live)", got.State)
		}
	})

	t.Run("whole_deprecate_moves_every_published_version_and_only_those", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{
			"1.0.0": StatePublished, "2.0.0": StatePublished, "0.9.0": StateRetired,
		}}
		event := Event{ULID: "01DEPRECATE000000000003", CommitSeq: 1, Subject: env.ID, Transition: TDeprecate, Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", got.Flags)
		}
		if got.Versions["1.0.0"] != StateDeprecated || got.Versions["2.0.0"] != StateDeprecated {
			t.Fatalf("every published version should become deprecated: %+v", got.Versions)
		}
		if got.Versions["0.9.0"] != StateRetired {
			t.Fatalf("an already-retired version must be untouched by a whole deprecate: %+v", got.Versions)
		}
	})

	t.Run("whole_deprecate_with_no_published_version_is_illegal", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated}}
		event := Event{ULID: "01DEPRECATE000000000004", CommitSeq: 1, Subject: env.ID, Transition: TDeprecate, Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		assertFlag(t, got, FlagIllegalTransition, event.ULID)
		if got.Versions["1.0.0"] != StateDeprecated {
			t.Fatalf("an illegal whole deprecate must not mutate any version: %+v", got.Versions)
		}
	})
}

// TestApplyContractScopedRetire pins retire v's rule: requires
// Versions[v] == deprecated, else illegal; sets retired. Plus the
// version-less WHOLE form, legal only when every recorded version is
// already deprecated.
func TestApplyContractScopedRetire(t *testing.T) {
	t.Parallel()

	t.Run("scoped_retire_requires_deprecated", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
		event := Event{ULID: "01RETIRE00000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TRetire, Version: "1.0.0", Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		assertFlag(t, got, FlagIllegalTransition, event.ULID)
	})

	t.Run("scoped_retire_of_deprecated_version_succeeds", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StatePublished}}
		event := Event{ULID: "01RETIRE00000000000000002", CommitSeq: 1, Subject: env.ID, Transition: TRetire, Version: "1.0.0", Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", got.Flags)
		}
		if got.Versions["1.0.0"] != StateRetired {
			t.Fatalf("Versions[1.0.0] = %q, want retired", got.Versions["1.0.0"])
		}
		// AC-7: {1.0: retired, 2.0: published} must project as published.
		if got.State != StatePublished {
			t.Fatalf("AC-7: projected state = %q, want published (2.0 is still live)", got.State)
		}
	})

	t.Run("whole_retire_requires_every_recorded_version_deprecated", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StatePublished}}
		event := Event{ULID: "01RETIRE00000000000000003", CommitSeq: 1, Subject: env.ID, Transition: TRetire, Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		assertFlag(t, got, FlagIllegalTransition, event.ULID)
		if got.Versions["1.0.0"] != StateDeprecated || got.Versions["2.0.0"] != StatePublished {
			t.Fatalf("an illegal whole retire must not mutate any version: %+v", got.Versions)
		}
	})

	t.Run("whole_retire_when_every_version_is_deprecated_succeeds", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StateDeprecated}}
		event := Event{ULID: "01RETIRE00000000000000004", CommitSeq: 1, Subject: env.ID, Transition: TRetire, Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("unexpected flags: %+v", got.Flags)
		}
		if got.Versions["1.0.0"] != StateRetired || got.Versions["2.0.0"] != StateRetired {
			t.Fatalf("every version should become retired: %+v", got.Versions)
		}
		if got.State != StateRetired {
			t.Fatalf("projected state = %q, want retired", got.State)
		}
	})
}

// TestApplyContractScopedAuthorization pins that publish/deprecate/retire
// on the per-version path still require RoleOwner, via the existing
// `authorized` helper — the plan explicitly forbids weakening this.
func TestApplyContractScopedAuthorization(t *testing.T) {
	t.Parallel()

	env := contractOwnerEnv()
	prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}}
	event := Event{ULID: "01AUTHZ00000000000000001", CommitSeq: 1, Subject: env.ID, Transition: TDeprecate, Version: "1.0.0", Actor: Actor{System: "mallory"}}

	got := Apply(KindContract, env, prior, event, alwaysMember)

	assertFlag(t, got, FlagUnauthorizedActor, event.ULID)
	if got.Versions["1.0.0"] != StatePublished {
		t.Fatalf("an unauthorized event must not mutate any version: %+v", got.Versions)
	}
}

// TestContractVersionRepublishAfterDeprecateIsIllegal pins a deliberate,
// INTENDED divergence from the pre-P4 engine (see contract.go's own doc
// comment on contractVersionVerdict, and TestMigrationCutoverProperty's
// doc comment for why the property test itself does not cover this
// input). The pre-P4 interim row (table.go, `(deprecated, publish) ->
// published`) is mechanically version-blind: from the SUBJECT's own
// point of view, "deprecated, then publish" always meant a NEW version
// arriving. The per-version engine can now tell the difference, and
// correctly refuses a republish of the SAME version number once it has
// been deprecated — a version identifier is permanent (spec §5.4a).
//
// TEETH: change contractVersionVerdict's publish case to only check
// `versions[version] == StatePublished` (rather than "already set" at
// all) and this test reds — that reading incorrectly LEGALIZES this
// exact republish, exactly the property this test exists to pin.
func TestContractVersionRepublishAfterDeprecateIsIllegal(t *testing.T) {
	t.Parallel()

	env := contractOwnerEnv()
	prior := Result{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated}}
	event := Event{ULID: "01REPUBLISH0000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: "1.0.0", Actor: contractOwnerActor()}

	got := Apply(KindContract, env, prior, event, alwaysMember)

	assertFlag(t, got, FlagIllegalTransition, event.ULID)
	if got.Versions["1.0.0"] != StateDeprecated {
		t.Fatalf("Versions[1.0.0] must stay deprecated (no mutation on an illegal event): %+v", got.Versions)
	}
	if got.State != StateDeprecated {
		t.Fatalf("projected state must stay deprecated: %q", got.State)
	}
}

// TestProjectContractState pins spec 04 §3's projection rules directly
// (Result.State becomes a PROJECTION over Versions), independent of any
// particular event sequence that could reach a given map.
func TestProjectContractState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		versions map[string]State
		want     State
	}{
		{name: "any_published_wins_over_deprecated_and_retired", versions: map[string]State{"1.0.0": StateRetired, "2.0.0": StatePublished, "1.5.0": StateDeprecated}, want: StatePublished},
		{name: "deprecated_wins_over_retired_when_nothing_published", versions: map[string]State{"1.0.0": StateRetired, "1.5.0": StateDeprecated}, want: StateDeprecated},
		{name: "all_retired", versions: map[string]State{"1.0.0": StateRetired, "2.0.0": StateRetired}, want: StateRetired},
		{name: "single_published", versions: map[string]State{"1.0.0": StatePublished}, want: StatePublished},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := projectContractState(tc.versions)
			if got != tc.want {
				t.Fatalf("projectContractState(%+v) = %q, want %q", tc.versions, got, tc.want)
			}
		})
	}
}

// TestContractClaimComparesAgainstVersionNotProjection is plan decision 6,
// pinned in BOTH directions per the brief. `deprecate 1.0` while 2.0 stays
// published claims `deprecated`; the correctly-computed SUBJECT-level
// projection is `published` (2.0 is still live). checkClaim must compare
// the claim against 1.0's OWN outcome (`deprecated`), never against the
// projected Result.State — comparing against the projection would raise
// a spurious FlagStateClaimMismatch on the exact steady state this phase
// exists to enable.
//
// TEETH: pass `result.State` (the projection) instead of `outcome` (the
// version's own resulting state) to checkClaim in applyContractScoped,
// and the "matching_claim" subtest below reds with a spurious
// state-claim-mismatch flag, while the "mismatched_claim" subtest goes
// green for the wrong reason (a real claim-checking regression that
// silently drops the check entirely would instead red the second
// subtest, not the first) — both directions are needed to catch either
// failure mode.
func TestContractClaimComparesAgainstVersionNotProjection(t *testing.T) {
	t.Parallel()

	t.Run("matching_version_level_claim_is_not_flagged_despite_a_different_projection", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished, "2.0.0": StatePublished}}
		event := Event{
			ULID: "01CLAIMVER0000000000001", CommitSeq: 1, Subject: env.ID,
			Transition: TDeprecate, Version: "1.0.0", Actor: contractOwnerActor(),
			ClaimedState: StateDeprecated, // correct for 1.0.0's own outcome
		}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if got.State != StatePublished {
			t.Fatalf("precondition broken: projected state = %q, want published (2.0.0 still live)", got.State)
		}
		if len(got.Flags) != 0 {
			t.Fatalf("a claim that matches the VERSION's own outcome must not be flagged, even though it disagrees with the projection: %+v", got.Flags)
		}
	})

	t.Run("mismatched_version_level_claim_is_still_flagged", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished, "2.0.0": StatePublished}}
		event := Event{
			ULID: "01CLAIMVER0000000000002", CommitSeq: 1, Subject: env.ID,
			Transition: TDeprecate, Version: "1.0.0", Actor: contractOwnerActor(),
			ClaimedState: StateRetired, // wrong regardless of which state it's compared to
		}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		assertFlag(t, got, FlagStateClaimMismatch, event.ULID)
	})
}

// TestContractBranchNeverConsultsSubjectStateTable is plan decision 7,
// pinned in both directions. `Versions{1.0: retired}` projects as
// `retired` — a TERMINAL state in the pre-P4 table (no outgoing rows at
// all, CC-020's own "any transition from terminal state" test). If the
// per-version path fell through to that table lookup, publishing 2.0
// here would be refused. It is not, because contractVersionVerdict reads
// Versions alone.
func TestContractBranchNeverConsultsSubjectStateTable(t *testing.T) {
	t.Parallel()

	t.Run("the_old_tables_own_row_is_absent_for_this_state", func(t *testing.T) {
		t.Parallel()
		if _, exists := transitionTable[tableKey{Kind: KindContract, From: StateRetired, Transition: TPublish}]; exists {
			t.Fatal("table.go now has a (retired, publish) row — this test's whole premise (that the subject-state table refuses this) no longer holds; re-check the divergence this test pins")
		}
	})

	t.Run("successor_publish_is_legal_via_Versions_alone", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		prior := Result{Kind: KindContract, State: StateRetired, Versions: map[string]State{"1.0.0": StateRetired}}
		event := Event{ULID: "01DECISION7000000000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: "2.0.0", Actor: contractOwnerActor()}

		got := Apply(KindContract, env, prior, event, alwaysMember)

		if len(got.Flags) != 0 {
			t.Fatalf("publish 2.0.0 must be legal despite the projected subject state being the terminal `retired` — the contract branch must decide from Versions alone: %+v", got.Flags)
		}
		if got.Versions["2.0.0"] != StatePublished {
			t.Fatalf("Versions[2.0.0] = %q, want published", got.Versions["2.0.0"])
		}
		if got.State != StatePublished {
			t.Fatalf("projected state = %q, want published", got.State)
		}
	})

	t.Run("the_full_sequence_publish_deprecate_retire_publish_is_all_legal", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		events := []Event{
			{ULID: "01DECISION7SEQ00000001", CommitSeq: 1, Subject: env.ID, Transition: TPublish, Version: "1.0.0", Actor: contractOwnerActor()},
			{ULID: "01DECISION7SEQ00000002", CommitSeq: 2, Subject: env.ID, Transition: TDeprecate, Version: "1.0.0", Actor: contractOwnerActor()},
			{ULID: "01DECISION7SEQ00000003", CommitSeq: 3, Subject: env.ID, Transition: TRetire, Version: "1.0.0", Actor: contractOwnerActor()},
			{ULID: "01DECISION7SEQ00000004", CommitSeq: 4, Subject: env.ID, Transition: TPublish, Version: "2.0.0", Actor: contractOwnerActor()},
		}
		got := Fold(KindContract, env, events, alwaysMember)
		if len(got.Flags) != 0 {
			t.Fatalf("the whole retire-then-publish-successor sequence must be all-legal: %+v", got.Flags)
		}
		if got.State != StatePublished {
			t.Fatalf("final state = %q, want published", got.State)
		}
		if got.Versions["1.0.0"] != StateRetired || got.Versions["2.0.0"] != StatePublished {
			t.Fatalf("final Versions = %+v, want {1.0.0: retired, 2.0.0: published}", got.Versions)
		}
	})

	t.Run("CheckCandidate_agrees_and_CheckLegality_would_have_refused", func(t *testing.T) {
		t.Parallel()
		env := contractOwnerEnv()
		actor := contractOwnerActor()
		prior := Result{Kind: KindContract, State: StateRetired, Versions: map[string]State{"1.0.0": StateRetired}}

		if got := CheckCandidate(KindContract, prior, TPublish, "2.0.0", env, actor, MembershipMember); got != VerdictLegal {
			t.Fatalf("CheckCandidate = %q, want legal", got)
		}
		// CheckLegality has no version dimension: given only the projected
		// subject state (retired, a terminal state), it correctly refuses —
		// which is exactly the wall decision 7 exists to let CheckCandidate
		// route around, and exactly why CheckLegality's signature is left
		// unchanged rather than made to see Versions.
		if got := CheckLegality(KindContract, StateRetired, TPublish, env, actor, MembershipMember); got != VerdictIllegalTransition {
			t.Fatalf("CheckLegality = %q, want illegal-transition (it has no version dimension to see past `retired`)", got)
		}
	})
}
