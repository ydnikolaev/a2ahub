package fold

import (
	"testing"
)

// allKinds/allStates are the exhaustive domains used to walk the full
// (kind, state) cross-product below — not just the pairs `rows` happens
// to populate. That is what catches a filter that leaks a row from a
// neighbouring kind, or returns something from a state no row ever
// names.
var allKinds = []Kind{
	KindContract, KindRequirement, KindQuestion, KindWorkRequest,
	KindDecision, KindHandoff, KindResponse, KindAnnouncement,
}

var allStates = []State{
	StateNone, StateDraft, StatePublished, StateDeprecated, StateRetired,
	StateAcknowledged, StateSatisfied, StateDeclined, StateWithdrawn, StateSuperseded,
	StateSubmitted, StateAccepted, StateInProgress, StateBlocked, StateResponded,
	StateClosed, StateCancelled, StateProposed, StateApproved, StateRejected,
	StateVerified, StateDisputed,
}

// rowKey mirrors table.go's tableKey but is local to this test file —
// LegalNext must not need it, only the agreement test does.
type rowKey struct {
	Kind       Kind
	From       State
	Transition string
}

// TestLegalNextRowGroupsAgreeOnToAndRole guards the dedupe LegalNext
// performs (rows that share one (Kind, From, Transition) key but differ
// only by Scenario collapse to one NextMove). If a future row addition
// ever gives two rows the same key with a DIFFERENT To or Role, that is
// a real distinction LegalNext's dedupe would silently drop — this test
// forces it to disagree loudly instead, the same spirit as buildTable's
// own duplicate-key panic.
func TestLegalNextRowGroupsAgreeOnToAndRole(t *testing.T) {
	t.Parallel()

	type seen struct {
		To   State
		Role Role
	}
	group := map[rowKey]seen{}
	for _, r := range rows {
		key := rowKey{Kind: r.Kind, From: r.From, Transition: r.Transition}
		want, ok := group[key]
		if !ok {
			group[key] = seen{To: r.To, Role: r.Role}
			continue
		}
		if want.To != r.To || want.Role != r.Role {
			t.Fatalf("rows for %+v disagree: (%v,%v) vs (%v,%v) — LegalNext's dedupe would silently drop a real distinction",
				key, want.To, want.Role, r.To, r.Role)
		}
	}
}

// TestLegalNextAgreesWithRows is the agreement test this lane exists
// for (spec §T3, "LegalNext agrees with CheckLegality for every row in
// the table"). It is driven FROM rows itself — never a hand-written
// expectation table, which would be exactly the second copy of the
// rules this lane exists to avoid.
//
// Two directions, both on the FULL (Transition, To, Role) triple, not
// just the transition name — a transposed Role is the entire point of
// this lane ("who may make them") and must fail loudly:
//
//  1. every row's own triple appears among LegalNext(row.Kind, row.From).
//  2. every (kind, from) pair in the full Kind x State cross-product —
//     not only pairs rows happens to populate — returns ONLY triples
//     that some row for that exact (kind, from) actually names. This
//     is what catches over-return: a filter leaking a neighbouring
//     kind's row, or a nonempty result from an unreachable state.
func TestLegalNextAgreesWithRows(t *testing.T) {
	t.Parallel()

	type triple struct {
		Transition string
		To         State
		Role       Role
	}

	// tableTriples[kind][from] is the set of real triples rows names for
	// that exact pair — built once, from rows, and used for both
	// directions below.
	tableTriples := map[Kind]map[State]map[triple]bool{}
	for _, r := range rows {
		byFrom, ok := tableTriples[r.Kind]
		if !ok {
			byFrom = map[State]map[triple]bool{}
			tableTriples[r.Kind] = byFrom
		}
		set, ok := byFrom[r.From]
		if !ok {
			set = map[triple]bool{}
			byFrom[r.From] = set
		}
		set[triple{Transition: r.Transition, To: r.To, Role: r.Role}] = true
	}

	t.Run("every_row_is_reachable_via_LegalNext", func(t *testing.T) {
		t.Parallel()
		for _, r := range rows {
			want := triple{Transition: r.Transition, To: r.To, Role: r.Role}
			got := LegalNext(r.Kind, r.From)
			found := false
			for _, m := range got {
				if triple(m) == want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("row %+v: LegalNext(%v, %v) = %+v, missing (%v -> %v, role %v)",
					r, r.Kind, r.From, got, r.Transition, r.To, r.Role)
			}
		}
	})

	t.Run("every_LegalNext_move_corresponds_to_a_real_row", func(t *testing.T) {
		t.Parallel()
		for _, kind := range allKinds {
			for _, from := range allStates {
				got := LegalNext(kind, from)
				want := tableTriples[kind][from]
				for _, m := range got {
					tr := triple(m)
					if !want[tr] {
						t.Fatalf("LegalNext(%v, %v) returned %+v, which no row for that exact (kind, from) names",
							kind, from, m)
					}
				}
				if len(want) == 0 && len(got) != 0 {
					t.Fatalf("LegalNext(%v, %v) = %+v, want empty — no row names this (kind, from) pair", kind, from, got)
				}
			}
		}
	})
}

// TestLegalNextResponseScoping pins spec §T3's own highest-stakes-bug
// callout: `verify` is reachable ONLY by querying KindResponse with the
// response's own closure sub-state, never by querying the parent
// exchange's own kind/state — responseRows() (table.go) is the only
// place `verify` has a row at all.
//
// `dispute` is more subtle and genuinely has TWO real rows, not one:
// responseRows() gives the response its own submitted->disputed row
// (RoleOwner, resolved against the PARENT's envelope per
// legality.go:29-35), and exchangeRows() SEPARATELY gives the parent
// exchange a responded->in_progress row (D-024's "dispute ADDITIONALLY
// reopens the parent", applyResponseScoped, fold.go) — that second row
// documents a real, caller-visible fact ("from `responded`, the owner
// may dispute, which reopens the exchange"), even though the dispatch
// switch (fold.go:89-98) always routes an actual `dispute` EVENT through
// applyResponseScoped by transition name, never through the parent's
// generic per-kind table lookup. LegalNext, being a pure filter over
// rows with no dispatch logic of its own, correctly reports both.
func TestLegalNextResponseScoping(t *testing.T) {
	t.Parallel()

	t.Run("parent_exchange_state_never_offers_verify", func(t *testing.T) {
		t.Parallel()
		for _, kind := range []Kind{KindQuestion, KindWorkRequest} {
			for _, s := range allStates {
				for _, m := range LegalNext(kind, s) {
					if m.Transition == TVerify {
						t.Fatalf("LegalNext(%v, %v) offered %q — verify must only ever come from LegalNext(KindResponse, ...)", kind, s, m.Transition)
					}
				}
			}
		}
	})

	t.Run("parent_exchange_offers_dispute_only_from_responded", func(t *testing.T) {
		t.Parallel()
		for _, kind := range []Kind{KindQuestion, KindWorkRequest} {
			for _, s := range allStates {
				var haveDispute bool
				for _, m := range LegalNext(kind, s) {
					if m.Transition == TDispute {
						haveDispute = true
					}
				}
				wantDispute := s == StateResponded
				if haveDispute != wantDispute {
					t.Fatalf("LegalNext(%v, %v) dispute-present=%v, want %v (D-024's parent reopen row lives only at responded)", kind, s, haveDispute, wantDispute)
				}
			}
		}
	})

	t.Run("response_submitted_offers_both_verify_and_dispute_as_owner", func(t *testing.T) {
		t.Parallel()
		got := LegalNext(KindResponse, StateSubmitted)
		wantVerify := NextMove{Transition: TVerify, To: StateVerified, Role: RoleOwner}
		wantDispute := NextMove{Transition: TDispute, To: StateDisputed, Role: RoleOwner}
		var haveVerify, haveDispute bool
		for _, m := range got {
			if m == wantVerify {
				haveVerify = true
			}
			if m == wantDispute {
				haveDispute = true
			}
		}
		if !haveVerify || !haveDispute {
			t.Fatalf("LegalNext(KindResponse, StateSubmitted) = %+v, want both %+v and %+v", got, wantVerify, wantDispute)
		}
	})
}

// TestLegalNextOmitsAnnouncementAcknowledge pins this phase's own
// decision (D-025's transition-free broadcast-ack is deliberately NOT
// reported by LegalNext — see legalnext.go's doc comment for why: the
// rule is membership-alone and LegalNext takes no membership parameter,
// so any Role it reported would misstate the rule rather than delegate
// it). A silent omission is not acceptable per this lane's brief; this
// test makes the omission an asserted, intentional fact instead.
func TestLegalNextOmitsAnnouncementAcknowledge(t *testing.T) {
	t.Parallel()

	got := LegalNext(KindAnnouncement, StatePublished)
	for _, m := range got {
		if m.Transition == TAcknowledge {
			t.Fatalf("LegalNext(KindAnnouncement, StatePublished) reported acknowledge (%+v) — this phase's chosen behavior is to omit it (D-025, membership-alone rule LegalNext cannot express)", m)
		}
	}
}

// TestLegalNextUnreachableStateIsEmpty pins that a state no row ever
// names as a fromState returns an empty slice, not a panic or a
// leaked entry from another (kind, from) pair.
func TestLegalNextUnreachableStateIsEmpty(t *testing.T) {
	t.Parallel()

	got := LegalNext(KindContract, StateSubmitted) // submitted is an exchange state, not a contract one
	if len(got) != 0 {
		t.Fatalf("LegalNext(KindContract, StateSubmitted) = %+v, want empty", got)
	}
}

// TestLegalNextDynamicRowsCollapseToOne pins the dedupe LegalNext
// performs on rows that share one (Kind, From, Transition) key but
// differ only by Scenario (table.go's own comment: Scenario "carries no
// runtime meaning"). Without dedupe, unblock from `blocked` would
// return three functionally-identical NextMove entries — one per
// possible pre-block state — which is not what "each legal transition"
// means to a reader deciding whose move it is.
func TestLegalNextDynamicRowsCollapseToOne(t *testing.T) {
	t.Parallel()

	t.Run("unblock_from_blocked", func(t *testing.T) {
		t.Parallel()
		got := LegalNext(KindQuestion, StateBlocked)
		count := 0
		for _, m := range got {
			if m.Transition == TUnblock {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("LegalNext(KindQuestion, StateBlocked) reported %d unblock moves, want 1 (deduped across pre-block-state scenarios): %+v", count, got)
		}
	})

	t.Run("decision_approve_from_proposed", func(t *testing.T) {
		t.Parallel()
		got := LegalNext(KindDecision, StateProposed)
		count := 0
		for _, m := range got {
			if m.Transition == TApprove {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("LegalNext(KindDecision, StateProposed) reported %d approve moves, want 1 (deduped across quorum scenarios): %+v", count, got)
		}
	})
}
