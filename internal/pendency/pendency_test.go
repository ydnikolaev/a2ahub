package pendency

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestI8Totality is the I8 gate: the table must carry a row for every
// (Kind, From) pair internal/fold.SubjectStates enumerates — the universe
// is derived, the verdicts are hand-authored (spec I8). A miss here names
// the exact pair, never fails silently as a generic count mismatch.
func TestI8Totality(t *testing.T) {
	pairs := fold.SubjectStates()
	if len(pairs) == 0 {
		t.Fatal("fold.SubjectStates() returned no pairs — universe is empty, gate cannot check anything")
	}

	var missing []string
	for _, p := range pairs {
		if _, ok := table[key{Kind: p.Kind, State: p.State}]; !ok {
			missing = append(missing, string(p.Kind)+"/"+string(p.State))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("pendency table has no row for %d/%d fold.SubjectStates() pairs: %v",
			len(missing), len(pairs), missing)
	}
}

// TestErrNoRowOnLookupMiss asserts a (kind, state) pair the table does
// not carry is an explicit, named refusal (ErrNoRow), never a silent
// zero-value Verdict.
func TestErrNoRowOnLookupMiss(t *testing.T) {
	t.Parallel()
	_, err := Resolve(Input{Kind: fold.Kind("no-such-kind"), State: fold.State("no-such-state")})
	if !errors.Is(err, ErrNoRow) {
		t.Fatalf("Resolve() on an unknown pair: want errors.Is(err, ErrNoRow), got %v", err)
	}
}

// resolveCase is one Resolve(in) expectation.
type resolveCase struct {
	name         string
	in           Input
	wantOwners   []string // nil means "nobody"
	wantExpected string   // "" when wantOwners is nil
}

// exchangeCases builds the 3.4.3 case set for one of question/work_request
// — the two kinds share one lifecycle (domain 3.4.3), so the case set is
// generated once and reused for both rather than hand-doubled.
func exchangeCases(k fold.Kind) []resolveCase {
	return []resolveCase{
		{
			name:         string(k) + "/draft: owner owes submit",
			in:           Input{Kind: k, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TSubmit,
		},
		{
			name:         string(k) + "/submitted: target owes acknowledge",
			in:           Input{Kind: k, State: fold.StateSubmitted, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TAcknowledge,
		},
		{
			name:         string(k) + "/acknowledged: target owes respond",
			in:           Input{Kind: k, State: fold.StateAcknowledged, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TRespond,
		},
		{
			name:         string(k) + "/accepted: target owes respond",
			in:           Input{Kind: k, State: fold.StateAccepted, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TRespond,
		},
		{
			name:         string(k) + "/in_progress: target owes respond",
			in:           Input{Kind: k, State: fold.StateInProgress, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TRespond,
		},
		{
			name:         string(k) + "/blocked: target owes unblock",
			in:           Input{Kind: k, State: fold.StateBlocked, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TUnblock,
		},
		{
			name:         string(k) + "/responded: sender (from) owes close",
			in:           Input{Kind: k, State: fold.StateResponded, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TClose,
		},
	}
}

// TestResolveRows is table-driven over every row the pendency table
// carries. Each "nobody" case sits next to a positive-control case for
// the same kind (its own draft/owner-owed row, or another productive
// state) in this same table, so a resolver mutated to uniformly return
// nil cannot pass the suite — the positive controls fail alongside the
// "nobody" cases they exist to guard.
func TestResolveRows(t *testing.T) {
	cases := []resolveCase{
		// --- 3.4.1 contract ---
		{
			name:         "contract/draft: owner owes publish",
			in:           Input{Kind: fold.KindContract, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TPublish,
		},
		{name: "contract/published: nobody (positive control above is contract/draft)",
			in: Input{Kind: fold.KindContract, State: fold.StatePublished, From: "sys-a"}},
		{name: "contract/deprecated: nobody",
			in: Input{Kind: fold.KindContract, State: fold.StateDeprecated, From: "sys-a"}},

		// --- 3.4.2 requirement ---
		{
			name:         "requirement/draft: owner owes publish",
			in:           Input{Kind: fold.KindRequirement, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TPublish,
		},
		{
			name:         "requirement/published: target owes acknowledge",
			in:           Input{Kind: fold.KindRequirement, State: fold.StatePublished, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TAcknowledge,
		},
		{
			// The domain splits here, and collapsing it is how this table
			// once told the target to emit an event fold refuses from it
			// (03-domain.md §3.4.2: "satisfy event is requester's").
			// Before a fulfilling response exists the TARGET owes the work
			// — and no requirement transition names that work, so Expected
			// is deliberately empty (Verdict's own doc comment).
			name:       "requirement/acknowledged, no response yet: target owes work no transition names",
			in:         Input{Kind: fold.KindRequirement, State: fold.StateAcknowledged, From: "sys-a", To: []string{"sys-b"}},
			wantOwners: []string{"sys-b"},
		},
		{
			name: "requirement/acknowledged, response landed: the REQUESTER owes satisfy",
			in: Input{
				Kind: fold.KindRequirement, State: fold.StateAcknowledged,
				From: "sys-a", To: []string{"sys-b"}, HasFulfillingResponse: true,
			},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TSatisfy,
		},
		{name: "requirement/satisfied: nobody (positive control above is requirement/draft)",
			in: Input{Kind: fold.KindRequirement, State: fold.StateSatisfied, From: "sys-a"}},
		{name: "requirement/declined: nobody",
			in: Input{Kind: fold.KindRequirement, State: fold.StateDeclined, From: "sys-a"}},
		{name: "requirement/withdrawn: nobody",
			in: Input{Kind: fold.KindRequirement, State: fold.StateWithdrawn, From: "sys-a"}},

		// --- 3.4.4 decision ---
		{
			name:         "decision/draft: owner owes propose",
			in:           Input{Kind: fold.KindDecision, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TPropose,
		},
		{
			name: "decision/proposed: pending approvers owe approve",
			in: Input{
				Kind: fold.KindDecision, State: fold.StateProposed, From: "sys-a",
				RequiredApprovers: []string{"sys-b", "sys-c"},
				Approvals:         map[string]bool{"sys-b": true},
			},
			wantOwners:   []string{"sys-c"},
			wantExpected: fold.TApprove,
		},
		{name: "decision/approved: nobody (positive control above is decision/draft and decision/proposed)",
			in: Input{Kind: fold.KindDecision, State: fold.StateApproved, From: "sys-a"}},
		{name: "decision/rejected: nobody",
			in: Input{Kind: fold.KindDecision, State: fold.StateRejected, From: "sys-a"}},

		// --- 3.4.5 handoff ---
		{
			name:         "handoff/draft: owner owes submit",
			in:           Input{Kind: fold.KindHandoff, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TSubmit,
		},
		{
			name:         "handoff/submitted: target owes acknowledge",
			in:           Input{Kind: fold.KindHandoff, State: fold.StateSubmitted, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TAcknowledge,
		},
		{
			name:         "handoff/acknowledged: target owes verify-pass",
			in:           Input{Kind: fold.KindHandoff, State: fold.StateAcknowledged, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TVerifyPass,
		},
		{
			name:         "handoff/rejected: owner (producer) owes supersede — the coincidence row, OWED not an escape hatch",
			in:           Input{Kind: fold.KindHandoff, State: fold.StateRejected, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TSupersede,
		},

		// --- 3.4.6 response ---
		{
			name:         "response/draft: owner owes submit",
			in:           Input{Kind: fold.KindResponse, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TSubmit,
		},
		{
			name:         "response/submitted: parent's from owes verify, never the response's own from",
			in:           Input{Kind: fold.KindResponse, State: fold.StateSubmitted, From: "sys-a", ParentFrom: "sys-c"},
			wantOwners:   []string{"sys-c"},
			wantExpected: fold.TVerify,
		},
		{
			name: "response/disputed: nobody — D-024 reopens the parent to in_progress, which already " +
				"sends the producer back through respond. 2026-08-08 gave this state a supersede exit; " +
				"2026-08-09 deleted that row (spec 06, epic-backlog B8) because no shipped verb ever " +
				"reached it, so this pair is no longer a fold.SubjectStates() member — it stays a case " +
				"here anyway because it is still a resting-only pair fold.RestingStates() enumerates and " +
				"pendency still answers for it",
			in: Input{Kind: fold.KindResponse, State: fold.StateDisputed, From: "sys-a", ParentFrom: "sys-c"},
		},

		// --- 3.4.7 announcement ---
		{
			name:         "announcement/draft: owner owes publish",
			in:           Input{Kind: fold.KindAnnouncement, State: fold.StateDraft, From: "sys-a"},
			wantOwners:   []string{"sys-a"},
			wantExpected: fold.TPublish,
		},
		{
			name: "announcement/published, ack_requested, directed: unacked target owes acknowledge",
			in: Input{
				Kind: fold.KindAnnouncement, State: fold.StatePublished, From: "sys-a",
				To: []string{"sys-b", "sys-c"}, AckRequested: true,
				Acks: map[string]bool{"sys-c": true},
			},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TAcknowledge,
		},
		{
			name: "announcement/published, ack_requested, broadcast: unacked active participants except the author owe acknowledge",
			in: Input{
				Kind: fold.KindAnnouncement, State: fold.StatePublished, From: "sys-a",
				Broadcast: true, AckRequested: true,
				ActiveParticipants: []string{"sys-a", "sys-b", "sys-c"},
				Acks:               map[string]bool{"sys-c": true},
			},
			wantOwners:   []string{"sys-b"},
			wantExpected: fold.TAcknowledge,
		},
		{
			name: "announcement/published, no ack_requested: nobody, delivery-complete why (positive control above is the ack_requested cases)",
			in: Input{
				Kind: fold.KindAnnouncement, State: fold.StatePublished, From: "sys-a",
				To: []string{"sys-b"}, AckRequested: false,
			},
		},
	}
	cases = append(cases, exchangeCases(fold.KindQuestion)...)
	cases = append(cases, exchangeCases(fold.KindWorkRequest)...)

	// Sanity: this table must exercise every (kind, state) pair the I8
	// gate requires — announcement/published gets three scenario cases
	// (directed, broadcast, no-ack-requested), so cases can outnumber
	// pairs, but every pair must be covered at least once.
	//
	// The count is 36, one more than fold.SubjectStates()'s 35: every
	// SubjectStates() pair is covered, PLUS response/disputed, which
	// dropped OUT of SubjectStates() on 2026-08-09 (its only departing row
	// was deleted — spec 06, epic-backlog B8) but is kept as a case here
	// because it is still a fold.RestingStates() pair pendency answers for.
	covered := make(map[key]bool, len(cases))
	for _, tc := range cases {
		covered[key{Kind: tc.in.Kind, State: tc.in.State}] = true
	}
	if len(covered) != 36 {
		t.Fatalf("TestResolveRows: %d distinct (kind, state) pairs covered, want 36 (35 fold.SubjectStates() pairs plus the retained response/disputed resting-only case)", len(covered))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v, err := Resolve(tc.in)
			if err != nil {
				t.Fatalf("Resolve(%+v) returned error: %v", tc.in, err)
			}

			if !reflect.DeepEqual(v.Owners, tc.wantOwners) {
				t.Errorf("Owners = %v, want %v", v.Owners, tc.wantOwners)
			}
			if v.Expected != tc.wantExpected {
				t.Errorf("Expected = %q, want %q", v.Expected, tc.wantExpected)
			}
			if v.Why == "" {
				t.Error("Why is empty — every verdict, including a settled/nobody one, must name its rule")
			}
		})
	}
}

// TestBlockedNamesTheOwnerInsteadOfTheTarget is P1's US-3, transferred to
// this field by the 2026-08-08 amendment ("as a blocked receiver, I do not
// want to be told I owe unblock when the requester is what blocks me"). A
// `blocked` question/work_request whose caller-resolved BlockedByOwner
// names a party must put the next move on THAT party, with Expected left
// "" (unblock stays the target's OWN event — domain 3.4.3 — so naming it
// for anyone else would be exactly the "surface names a move the tool
// refuses" failure the authority gate exists to catch). Absent
// BlockedByOwner, the row must be byte-identical to what shipped before
// this field existed: the target owes unblock.
func TestBlockedNamesTheOwnerInsteadOfTheTarget(t *testing.T) {
	t.Parallel()

	for _, k := range []fold.Kind{fold.KindQuestion, fold.KindWorkRequest} {
		t.Run(string(k)+"/blocked, no BlockedByOwner: target owes unblock (unchanged)", func(t *testing.T) {
			t.Parallel()
			v, err := Resolve(Input{Kind: k, State: fold.StateBlocked, From: "sys-a", To: []string{"sys-b"}})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !reflect.DeepEqual(v.Owners, []string{"sys-b"}) {
				t.Errorf("Owners = %v, want [sys-b]", v.Owners)
			}
			if v.Expected != fold.TUnblock {
				t.Errorf("Expected = %q, want %q", v.Expected, fold.TUnblock)
			}
		})

		t.Run(string(k)+"/blocked, BlockedByOwner=requester: the REQUESTER is named, not the target", func(t *testing.T) {
			t.Parallel()
			v, err := Resolve(Input{
				Kind: k, State: fold.StateBlocked, From: "sys-a", To: []string{"sys-b"},
				BlockedByOwner: "sys-a",
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			// The mutation this guards: falling back to target() would
			// produce [sys-b] here, not [sys-a] — asserting the exact
			// owner string, not merely "not the target", is what catches
			// a resolver mutated to ignore BlockedByOwner and return nil
			// (which without a positive assertion here would also make
			// v.Owners != []string{"sys-b"} true for the wrong reason).
			if !reflect.DeepEqual(v.Owners, []string{"sys-a"}) {
				t.Errorf("Owners = %v, want [sys-a]", v.Owners)
			}
			if v.Expected != "" {
				t.Errorf("Expected = %q, want \"\" — unblock stays the target's own event", v.Expected)
			}
			if v.HumanGate != "" {
				t.Errorf("HumanGate = %q, want \"\" for an Expected-less verdict", v.HumanGate)
			}
			if !strings.Contains(v.Why, "sys-a") {
				t.Errorf("Why = %q, want it to name sys-a", v.Why)
			}
		})

		t.Run(string(k)+"/blocked, BlockedByOwner set to a system not in from/to: still named — P-2's legality check is the CALLER's job, this package trusts what it is handed", func(t *testing.T) {
			t.Parallel()
			v, err := Resolve(Input{
				Kind: k, State: fold.StateBlocked, From: "sys-a", To: []string{"sys-b"},
				BlockedByOwner: "sys-z",
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !reflect.DeepEqual(v.Owners, []string{"sys-z"}) {
				t.Errorf("Owners = %v, want [sys-z]", v.Owners)
			}
		})
	}
}

// TestDeliveryUnresolvableNamesTheResponderInsteadOfTheSender is AC9's own
// unit test (spec 06 §8, row 9): "pendency.Resolve with
// DeliveryUnresolvable: true returns the responder and respond; with it
// false returns the requester and close, unchanged."
func TestDeliveryUnresolvableNamesTheResponderInsteadOfTheSender(t *testing.T) {
	t.Parallel()

	for _, k := range []fold.Kind{fold.KindQuestion, fold.KindWorkRequest} {
		t.Run(string(k)+"/responded, DeliveryUnresolvable=false: the SENDER owes close (unchanged)", func(t *testing.T) {
			t.Parallel()
			v, err := Resolve(Input{Kind: k, State: fold.StateResponded, From: "sys-a", To: []string{"sys-b"}})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if !reflect.DeepEqual(v.Owners, []string{"sys-a"}) {
				t.Errorf("Owners = %v, want [sys-a] (the sender/requester)", v.Owners)
			}
			if v.Expected != fold.TClose {
				t.Errorf("Expected = %q, want %q", v.Expected, fold.TClose)
			}
		})

		t.Run(string(k)+"/responded, DeliveryUnresolvable=true: the RESPONDER owes a fresh respond", func(t *testing.T) {
			t.Parallel()
			v, err := Resolve(Input{
				Kind: k, State: fold.StateResponded, From: "sys-a", To: []string{"sys-b"},
				DeliveryUnresolvable: true,
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			// The mutation this guards: falling back to owner() would
			// produce [sys-a] here, not [sys-b] — asserting the exact
			// owner string catches a resolver mutated to ignore
			// DeliveryUnresolvable.
			if !reflect.DeepEqual(v.Owners, []string{"sys-b"}) {
				t.Errorf("Owners = %v, want [sys-b] (the responder/target)", v.Owners)
			}
			if v.Expected != fold.TRespond {
				t.Errorf("Expected = %q, want %q — respond is legal from `responded` (table.go's own StateResponded row)", v.Expected, fold.TRespond)
			}
			if !strings.Contains(v.Why, "AC9") {
				t.Errorf("Why = %q, want it to name AC9", v.Why)
			}
		})
	}
}

// TestResolveDegradesOnMissingFacts asserts a resolver that would
// otherwise be productive, but is missing the envelope fact it needs,
// answers "nobody" with a Why that says the fact was missing — never a
// panic, and never the row's default settled-rationale text (which would
// misreport a missing fact as an intentional settle).
func TestResolveDegradesOnMissingFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		in         Input
		wantWhyHas string
	}{
		{
			name:       "owner row, no from: degrades, does not panic",
			in:         Input{Kind: fold.KindContract, State: fold.StateDraft},
			wantWhyHas: "no owner",
		},
		{
			name:       "target row, no to: degrades, does not panic",
			in:         Input{Kind: fold.KindRequirement, State: fold.StatePublished, From: "sys-a"},
			wantWhyHas: "no target",
		},
		{
			name:       "parentOwner row, no parent from: degrades, does not panic",
			in:         Input{Kind: fold.KindResponse, State: fold.StateSubmitted, From: "sys-a"},
			wantWhyHas: "parent envelope's `from` could not be resolved",
		},
		{
			name:       "pendingApprovers row, no required approvers: degrades, does not panic",
			in:         Input{Kind: fold.KindDecision, State: fold.StateProposed, From: "sys-a"},
			wantWhyHas: "no required approvers were recorded",
		},
		{
			name: "pendingApprovers row, all approved: degrades, does not panic",
			in: Input{
				Kind: fold.KindDecision, State: fold.StateProposed, From: "sys-a",
				RequiredApprovers: []string{"sys-b"},
				Approvals:         map[string]bool{"sys-b": true},
			},
			wantWhyHas: "every required approver has already recorded an approval",
		},
		{
			name: "unackedTargets row, ack_requested but every target already acked: degrades, does not panic",
			in: Input{
				Kind: fold.KindAnnouncement, State: fold.StatePublished, From: "sys-a",
				To: []string{"sys-b"}, AckRequested: true,
				Acks: map[string]bool{"sys-b": true},
			},
			wantWhyHas: "every addressed target has already acknowledged",
		},
		{
			name: "contract published, OperationalDebtOwed but no from: degrades, does not panic, and does not misreport a real debt as settled",
			in: Input{
				Kind: fold.KindContract, State: fold.StatePublished,
				OperationalDebtOwed: true,
			},
			wantWhyHas: "no owner",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, err := Resolve(tc.in)
			if err != nil {
				t.Fatalf("Resolve(%+v) returned error: %v", tc.in, err)
			}
			if len(v.Owners) != 0 {
				t.Fatalf("Owners = %v, want empty (the fact this case tests is missing)", v.Owners)
			}
			if v.Expected != "" {
				t.Errorf("Expected = %q, want empty when nobody owes anything", v.Expected)
			}
			if v.Why == "" {
				t.Fatal("Why is empty on a degraded resolution — must say the fact was missing")
			}
			if !strings.Contains(v.Why, tc.wantWhyHas) {
				t.Errorf("Why = %q, want it to contain %q", v.Why, tc.wantWhyHas)
			}
		})
	}
}

// TestResponseDisputedWhyIsConditionalOnTheParentReopen is spec 06's
// 2026-08-09 amendment's own gate: (response, disputed) always answers
// "nobody", but the RATIONALE must not lie. Ordinarily the debt genuinely
// moved to the parent (D-024's reopen), so the Why may say so. When the
// caller reports that this dispute's own reopen attempt was itself refused
// (fold.go's D-024 comment: the parent was not `responded` — already
// closed, or already reopened by an earlier dispute), the Why must say THAT
// instead, naming the flag, never the discharge that never happened.
func TestResponseDisputedWhyIsConditionalOnTheParentReopen(t *testing.T) {
	t.Parallel()

	base := Input{Kind: fold.KindResponse, State: fold.StateDisputed, From: "sys-a", ParentFrom: "sys-c"}

	ordinary, err := Resolve(base)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if len(ordinary.Owners) != 0 || ordinary.Expected != "" {
		t.Fatalf("ordinary case: Owners=%v Expected=%q, want nobody/\"\" — response/disputed owes nothing on this artifact either way", ordinary.Owners, ordinary.Expected)
	}
	if !strings.Contains(ordinary.Why, "the debt is on the parent") {
		t.Errorf("ordinary Why = %q, want it to say the debt moved to the parent", ordinary.Why)
	}
	if strings.Contains(ordinary.Why, "FlagIllegalTransition") {
		t.Errorf("ordinary Why = %q, must NOT claim a refusal that did not happen", ordinary.Why)
	}

	failed := base
	failed.ParentDisputeReopenFailed = true
	reopenFailed, err := Resolve(failed)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if len(reopenFailed.Owners) != 0 || reopenFailed.Expected != "" {
		t.Fatalf("reopen-failed case: Owners=%v Expected=%q, want nobody/\"\"", reopenFailed.Owners, reopenFailed.Expected)
	}
	if !strings.Contains(reopenFailed.Why, "FlagIllegalTransition") {
		t.Errorf("reopen-failed Why = %q, want it to name the refusal's flag", reopenFailed.Why)
	}
	if strings.Contains(reopenFailed.Why, "the debt is on the parent that the dispute reopened") {
		t.Errorf("reopen-failed Why = %q, must NOT claim the parent was reopened when it was refused", reopenFailed.Why)
	}
	if reopenFailed.Why == ordinary.Why {
		t.Error("reopen-failed and ordinary cases produced the identical Why — the branch is not conditional")
	}
}

// TestResolveDedupesAndSortsOwners asserts Owners is deterministic even
// when the input facts (a duplicated `to`, an ack set keyed by the same
// system twice via map semantics) could otherwise produce an unsorted or
// duplicated slice.
func TestResolveDedupesAndSortsOwners(t *testing.T) {
	t.Parallel()
	v, err := Resolve(Input{
		Kind: fold.KindRequirement, State: fold.StatePublished, From: "sys-a",
		To: []string{"sys-c", "sys-b", "sys-c"},
	})
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	want := []string{"sys-b", "sys-c"}
	if !reflect.DeepEqual(v.Owners, want) {
		t.Errorf("Owners = %v, want %v (deduplicated, sorted)", v.Owners, want)
	}
}

// TestExtraAddresseesJoinTheUnackedSet pins P4 Edge 3's late adopter: a
// consumer that adopted the contract AFTER the deprecation announcement's
// `to:` was frozen is owed the same acknowledge as everyone named in it.
// The caller establishes the fact (a registry read this package cannot do);
// the rule that turns it into a debt lives here, once.
//
// Its own negative control sits in the same test: without the extra
// addressee the identical announcement owes that system nothing, so a
// blanket "everyone owes an ack" implementation cannot pass.
func TestExtraAddresseesJoinTheUnackedSet(t *testing.T) {
	t.Parallel()
	base := Input{
		Kind: fold.KindAnnouncement, State: fold.StatePublished,
		From: "axon", To: []string{"seomatrix"}, AckRequested: true,
	}

	without, err := Resolve(base)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(without.Owners, []string{"seomatrix"}) {
		t.Fatalf("control: Owners = %v, want only the frozen addressee [seomatrix]", without.Owners)
	}

	late := base
	late.ExtraAddressees = []string{"lateadopter"}
	with, err := Resolve(late)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	want := []string{"lateadopter", "seomatrix"}
	if !reflect.DeepEqual(with.Owners, want) {
		t.Errorf("Owners = %v, want %v (union with the frozen `to:`, never a replacement)", with.Owners, want)
	}
	if with.Expected != fold.TAcknowledge {
		t.Errorf("Expected = %q, want %q", with.Expected, fold.TAcknowledge)
	}

	// An extra addressee who has ALREADY acked owes nothing — the ack set
	// subtracts from the union, not only from `to:`.
	acked := late
	acked.Acks = map[string]bool{"lateadopter": true, "seomatrix": true}
	settled, err := Resolve(acked)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if len(settled.Owners) != 0 {
		t.Errorf("Owners = %v, want nobody once every addressee has acked", settled.Owners)
	}

	// The qualifier governs P-m ONLY, and this pair is what says so.
	//
	// This assertion used to read "nobody owes one, however the addressee
	// was established" — it pinned the defect §18e/J1 names, and it was
	// written here in W1 as an intention rather than derived from the
	// sources. P-m carries the `ack_requested` qualifier; P-n does not (it
	// is registry-matched), and neither 03-domain.md:115 nor
	// validate.CheckRetirePrecondition — the code that actually blocks the
	// retire on this same fact — mentions the flag at all. A flagless
	// deprecation used to resolve to "nobody owes anything" while POL-006
	// refused its retire on the very consumer being told that.
	noAck := late
	noAck.AckRequested = false
	quiet, err := Resolve(noAck)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(quiet.Owners, []string{"lateadopter"}) {
		t.Errorf("Owners = %v, want only the registry-matched consumer [lateadopter]: "+
			"ack_requested gates the `to:`-matched half (P-m), never the registry-matched half (P-n)", quiet.Owners)
	}
	if quiet.Expected != fold.TAcknowledge {
		t.Errorf("Expected = %q, want %q — the consumer POL-006 blocks the retire on owes the acknowledge that unblocks it",
			quiet.Expected, fold.TAcknowledge)
	}

	// The paired control, without which the fix above would read as "the
	// qualifier does nothing": a `to:`-matched recipient with NO registry
	// match still owes nothing when the flag is false. That is domain
	// 3.4.7 — delivery completes on publish — and it must survive.
	plain := base
	plain.AckRequested = false
	settledPlain, err := Resolve(plain)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if len(settledPlain.Owners) != 0 {
		t.Errorf("Owners = %v, want nobody: a plain announcement with no ack requested and no registry-matched consumer owes no acknowledge (domain 3.4.7)",
			settledPlain.Owners)
	}
}

// TestALeftCounterpartyTransfersPendencyToTheSender is CC-062's own gate.
//
// A system whose manifest status is `left` cannot write to the space, so a
// debt named on it can be cleared by NO legal act — the row stays owed
// forever, the surfaces show an owner with no move, and no agent has an
// action that removes it. internal/validate already drew this exclusion for
// the same reason (RegisteredConsumer.Left: "excluded from the ack set
// entirely — they never block retire and are never counted as un-acked",
// §5.4 bullet (a)), so before this the two halves of one binary disagreed
// about the same consumer.
//
// The transfer, not merely the exclusion, is what spec 11 §18d's qualifier
// asks for: pendency moves to the SENDER as a cancel/re-route decision, and
// AC-102.2 wants such items flagged `orphaned-counterparty`. It uses
// Verdict's documented third shape (owners, no Expected) because which
// transition carries "cancel or re-route" differs by kind — naming one here
// would be this table inventing a move again, the §15 defect exactly.
func TestALeftCounterpartyTransfersPendencyToTheSender(t *testing.T) {
	t.Parallel()

	base := Input{
		Kind: fold.KindQuestion, State: fold.StateSubmitted,
		From: "alpha", To: []string{"departed"},
		ActiveParticipants: []string{"alpha", "departed"},
	}

	// Control first: while the counterparty is a member, it owes the ack.
	present, err := Resolve(base)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(present.Owners, []string{"departed"}) || present.Expected != fold.TAcknowledge {
		t.Fatalf("control: Owners=%v Expected=%q, want [departed] owing %q — without this the orphan case below proves nothing",
			present.Owners, present.Expected, fold.TAcknowledge)
	}

	gone := base
	gone.LeftParticipants = []string{"departed"}
	orphaned, err := Resolve(gone)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(orphaned.Owners, []string{"alpha"}) {
		t.Errorf("Owners = %v, want [alpha]: the pendency transfers to the sender, it does not vanish and it does not stay on a system that cannot write",
			orphaned.Owners)
	}
	if orphaned.Expected != "" {
		t.Errorf("Expected = %q, want \"\": cancel-or-re-route is a judgement about the exchange, and naming one transition here would be the table inventing a move",
			orphaned.Expected)
	}
	if !strings.Contains(orphaned.Why, "departed") || !strings.Contains(orphaned.Why, "CC-062") {
		t.Errorf("Why = %q, want it to name the orphan and the rule — an agent that cannot see WHICH counterparty left has nothing to act on",
			orphaned.Why)
	}
}

// TestOnlyTheLeftOwnerIsDroppedWhenOthersRemain is the paired control for
// the test above: with two addressees and one gone, the exchange is NOT
// orphaned — the remaining member still owes the move, and the sender must
// not be handed a cancel decision it does not need.
func TestOnlyTheLeftOwnerIsDroppedWhenOthersRemain(t *testing.T) {
	t.Parallel()

	in := Input{
		Kind: fold.KindQuestion, State: fold.StateSubmitted,
		From: "alpha", To: []string{"departed", "present"},
		ActiveParticipants: []string{"alpha", "present"},
		LeftParticipants:   []string{"departed"},
	}
	got, err := Resolve(in)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(got.Owners, []string{"present"}) {
		t.Errorf("Owners = %v, want [present]: only the departed system is dropped", got.Owners)
	}
	if got.Expected != fold.TAcknowledge {
		t.Errorf("Expected = %q, want %q: the exchange is still live for the member who remains", got.Expected, fold.TAcknowledge)
	}
}

// TestContractPublishedOperationalDebtIsTheP1Assertion is spec
// 05-declared-nature.md §6's own testing row, and the phase's whole
// point: "published version + registered consumer ⇒ a live integration
// with an owner, with NO voluntary field set — this is the P-1
// assertion; it must pass with every new field absent." Input here
// carries no x_operational, no known_gaps, nothing beyond the base
// envelope facts plus the ONE caller-resolved registration/floor fact —
// proving the derivation is not an opt-in field.
func TestContractPublishedOperationalDebtIsTheP1Assertion(t *testing.T) {
	t.Parallel()

	v, err := Resolve(Input{
		Kind: fold.KindContract, State: fold.StatePublished,
		From: "producer-sys",
		// No x_operational, no known_gaps, no descriptor fact of any kind
		// — only the fact a caller resolves from registration+floor.
		OperationalDebtOwed: true,
	})
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(v.Owners, []string{"producer-sys"}) {
		t.Errorf("Owners = %v, want [producer-sys]: the P-1 derivation must name the producer with no voluntary field set", v.Owners)
	}
	if v.Expected != "activate" {
		t.Errorf("Expected = %q, want %q: the producer owes the discharge act 26A shipped", v.Expected, "activate")
	}
	if !strings.Contains(v.Why, "P5 AC1") {
		t.Errorf("Why = %q, want it to name P5 AC1", v.Why)
	}
}

// TestContractPublishedOperationalDebtIsGatedOnTheFloor is §8 AC1's own
// gate: "GATED ON THE SPACE'S OWN FLOOR — below it, no derived row is
// emitted and the thread reads exactly as it does today." The caller
// resolves the floor into OperationalDebtOwed itself (pendency reads no
// floor config), so the row-level assertion is simply that the FALSE
// value — the one a below-floor space produces — reproduces today's
// unconditional "nobody" verdict byte-for-byte.
func TestContractPublishedOperationalDebtIsGatedOnTheFloor(t *testing.T) {
	t.Parallel()

	belowFloor, err := Resolve(Input{
		Kind: fold.KindContract, State: fold.StatePublished,
		From:                "producer-sys",
		OperationalDebtOwed: false, // what a below-floor space resolves to
	})
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if belowFloor.Owners != nil {
		t.Errorf("Owners = %v, want nil: below the floor no derived row is emitted", belowFloor.Owners)
	}
	if belowFloor.Expected != "" {
		t.Errorf("Expected = %q, want \"\"", belowFloor.Expected)
	}

	unset, err := Resolve(Input{
		Kind: fold.KindContract, State: fold.StatePublished, From: "producer-sys",
	})
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if !reflect.DeepEqual(unset, belowFloor) {
		t.Errorf("Verdict with OperationalDebtOwed unset = %+v, want it byte-identical to the explicit-false case %+v — "+
			"\"the thread reads exactly as it does today\"", unset, belowFloor)
	}
	wantWhy := "alive and settled: the owner MAY publish a successor or deprecate, but neither is a move anyone waits for"
	if unset.Why != wantWhy {
		t.Errorf("Why = %q, want the UNCHANGED settled text %q", unset.Why, wantWhy)
	}
}

// TestContractPublishedNoRegisteredConsumerStillOwesNobody is the paired
// control: OperationalDebtOwed only ever becomes true because a caller
// found a registered consumer AND cleared the floor — this test pins
// that a contract with neither (the ordinary case, most published
// contracts) is unaffected by this wave. "A rule that fires on
// everything is not a rule."
func TestContractPublishedNoRegisteredConsumerStillOwesNobody(t *testing.T) {
	t.Parallel()

	v, err := Resolve(Input{
		Kind: fold.KindContract, State: fold.StatePublished,
		From: "producer-sys", To: []string{"nobody-in-particular"},
	})
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if v.Owners != nil {
		t.Errorf("Owners = %v, want nil: no registered consumer means no derived debt", v.Owners)
	}
	if v.Expected != "" {
		t.Errorf("Expected = %q, want \"\"", v.Expected)
	}
}
