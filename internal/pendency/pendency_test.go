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
			name:         "requirement/acknowledged: target owes satisfy",
			in:           Input{Kind: fold.KindRequirement, State: fold.StateAcknowledged, From: "sys-a", To: []string{"sys-b"}},
			wantOwners:   []string{"sys-b"},
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
	covered := make(map[key]bool, len(cases))
	for _, tc := range cases {
		covered[key{Kind: tc.in.Kind, State: tc.in.State}] = true
	}
	if len(covered) != 35 {
		t.Fatalf("TestResolveRows: %d distinct (kind, state) pairs covered, want 35 (one per fold.SubjectStates() pair)", len(covered))
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

	// And the qualifier still governs: no ack requested, nobody owes one,
	// however the addressee was established.
	noAck := late
	noAck.AckRequested = false
	quiet, err := Resolve(noAck)
	if err != nil {
		t.Fatalf("Resolve() returned error: %v", err)
	}
	if len(quiet.Owners) != 0 {
		t.Errorf("Owners = %v, want nobody when ack_requested is false", quiet.Owners)
	}
}
