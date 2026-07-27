package fold

import (
	"fmt"
	"reflect"
	"testing"
)

// TestLegalNextForContract pins item 8: on the per-version path,
// LegalNextFor answers about the NAMED VERSION, and it derives that answer
// from contractVersionVerdict rather than from a table lookup keyed on
// that version's state.
//
// The derivation is the point. An earlier revision of this function did
// `LegalNext(kind, prior.Versions[version])`, which is correct for a
// recorded version and silently wrong for an unrecorded one: the map
// returns StateNone, and StateNone's only contract row is `create`, so
// "what may I do with 3.0.0?" answered "create the contract". That is the
// class of defect this epic keeps finding — it compiles, it is
// deterministic, and it misreports during exactly the rolling window the
// phase exists to enable. The unrecorded_version subtest below is written
// to fail against that revision.
func TestLegalNextForContract(t *testing.T) {
	t.Parallel()

	// The steady state of AC-7: 1.0.0 deprecated while 2.0.0 is live, so
	// the projected subject state (published) is the WRONG answer for
	// 1.0.0 and the right one for 2.0.0.
	rolling := Result{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StatePublished}}

	cases := []struct {
		name    string
		kind    Kind
		prior   Result
		version string
		want    []NextMove
	}{
		{
			name:    "a_deprecated_version_may_only_be_retired",
			kind:    KindContract,
			prior:   rolling,
			version: "1.0.0",
			want:    []NextMove{{Transition: TRetire, To: StateRetired, Role: RoleOwner}},
		},
		{
			name:    "a_published_version_may_only_be_deprecated",
			kind:    KindContract,
			prior:   rolling,
			version: "2.0.0",
			want:    []NextMove{{Transition: TDeprecate, To: StateDeprecated, Role: RoleOwner}},
		},
		{
			// The discriminating case. Against a table lookup this
			// returned the `create` row.
			name:    "an_unrecorded_version_may_be_published_never_created",
			kind:    KindContract,
			prior:   rolling,
			version: "3.0.0",
			want:    []NextMove{{Transition: TPublish, To: StatePublished, Role: RoleOwner}},
		},
		{
			// The whole-contract form: no version named, but versions
			// exist. Deprecate is legal (2.0.0 is published); retire is
			// not (not every version is deprecated); publish has no
			// whole form.
			name:    "the_whole_contract_form_offers_only_what_the_predicate_allows",
			kind:    KindContract,
			prior:   rolling,
			version: "",
			want:    []NextMove{{Transition: TDeprecate, To: StateDeprecated, Role: RoleOwner}},
		},
		{
			name:    "no_versions_and_no_version_named_falls_back_to_subject_state",
			kind:    KindContract,
			prior:   Result{Kind: KindContract, State: StateDraft},
			version: "",
			want:    LegalNext(KindContract, StateDraft),
		},
		{
			name:    "non_contract_kind_always_delegates_to_subject_state",
			kind:    KindQuestion,
			prior:   Result{Kind: KindQuestion, State: StateAccepted},
			version: "irrelevant",
			want:    LegalNext(KindQuestion, StateAccepted),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := LegalNextFor(tc.kind, tc.prior, tc.version)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("LegalNextFor(%s, %q) = %+v, want %+v", tc.kind, tc.version, got, tc.want)
			}
		})
	}
}

// TestLegalNextForAgreesWithCheckCandidate is the agreement matrix this
// package already applies to its pre-write/post-write pairs: a move
// LegalNextFor offers must be a move CheckCandidate accepts, and one it
// withholds must be one CheckCandidate refuses. Two functions reading one
// predicate is only worth anything if something checks that they still do.
func TestLegalNextForAgreesWithCheckCandidate(t *testing.T) {
	t.Parallel()

	env := Envelope{ID: "XC-001", Kind: KindContract, From: "axon"}
	actor := Actor{System: "axon"}
	priors := []Result{
		{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StatePublished}},
		{Kind: KindContract, State: StatePublished, Versions: map[string]State{"1.0.0": StateDeprecated, "2.0.0": StatePublished}},
		{Kind: KindContract, State: StateDeprecated, Versions: map[string]State{"1.0.0": StateDeprecated}},
		{Kind: KindContract, State: StateRetired, Versions: map[string]State{"1.0.0": StateRetired}},
	}

	for i, prior := range priors {
		t.Run(fmt.Sprintf("prior_%d_%s", i, prior.State), func(t *testing.T) {
			t.Parallel()
			for _, version := range []string{"", "1.0.0", "2.0.0", "9.9.9"} {
				offered := map[string]bool{}
				for _, m := range LegalNextFor(KindContract, prior, version) {
					offered[m.Transition] = true
				}
				for _, tr := range []string{TPublish, TDeprecate, TRetire} {
					accepted := CheckCandidate(KindContract, prior, tr, version, env, actor, MembershipMember) == VerdictLegal
					if offered[tr] != accepted {
						t.Fatalf("version %q transition %q: LegalNextFor offered=%v but CheckCandidate accepted=%v", version, tr, offered[tr], accepted)
					}
				}
			}
		})
	}
}
