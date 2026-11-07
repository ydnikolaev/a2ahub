package pendency

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestEveryOwedMoveIsAcceptedByTheLandCheck is the seam fb-20261106-28c7e6
// asked for: every transition this table can name as owed must be accepted
// by the land's own legality primitive, fold.CheckCandidateWithSuccessor
// (internal/cli/validate_ci_lifecycle.go's ciBaseLegalityChecker), for that
// state and for every system the verdict names.
//
// TestEveryRowNamesAMoveItsOwnerMayActuallyMake (authority_gate_test.go)
// did not catch the defect, for two independent reasons this test removes:
//
//   - It asks fold.LegalNext and fold.RoleAuthorizes, which read the role
//     table. A transition-free move (broadcast-ack, contract activate) is
//     not in that table by design, so it had to be skipped there — and the
//     land check is NOT the table, it is CheckCandidateWithSuccessor with
//     its transition-free branches. This test asks that function directly.
//   - Its probe Input sets no conditional fact, so contract/published
//     resolved to "nobody" and the owed activate was never even reached.
//     Here every row is resolved under the all-false baseline AND with each
//     bool fact of Input switched on in turn, discovered by reflection, so a
//     conditional row added later is reached without anyone editing this
//     probe. (BlockedByOwner is the one string-valued conditional fact; its
//     branch deliberately owes no named transition, so it has nothing to
//     check here.)
//
// Rows are enumerated from the package's own table, never a copied list.
func TestEveryOwedMoveIsAcceptedByTheLandCheck(t *testing.T) {
	t.Parallel()

	const ownerSys, approverSys, parentOwnerSys = "owner-sys", "approver-sys", "parent-owner-sys"
	targets := []string{"target-sys", "target-sys-2", "target-sys-3"}

	base := Input{
		From:               ownerSys,
		To:                 targets,
		AckRequested:       true,
		RequiredApprovers:  []string{approverSys},
		ActiveParticipants: append([]string{ownerSys}, targets...),
		ParentFrom:         parentOwnerSys,
	}

	variants := []struct {
		name string
		in   Input
	}{{name: "baseline", in: base}}
	inputType := reflect.TypeOf(Input{})
	for i := 0; i < inputType.NumField(); i++ {
		field := inputType.Field(i)
		if field.Type.Kind() != reflect.Bool {
			continue
		}
		in := base
		reflect.ValueOf(&in).Elem().Field(i).SetBool(true)
		variants = append(variants, struct {
			name string
			in   Input
		}{name: field.Name, in: in})
	}

	keys := make([]key, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].State < keys[j].State
	})

	var problems []string
	checked := map[string]bool{}
	for _, k := range keys {
		for _, v := range variants {
			in := v.in
			in.Kind, in.State = k.Kind, k.State
			verdict, err := Resolve(in)
			if err != nil {
				t.Fatalf("Resolve(%s/%s) under %s: %v", k.Kind, k.State, v.name, err)
			}
			if len(verdict.Owners) == 0 || verdict.Expected == "" {
				continue
			}

			env := fold.Envelope{
				ID: "X-owner-sys-probe", Kind: k.Kind, From: ownerSys,
				To: targets, RequiredApprovers: []string{approverSys},
			}
			// A response's verify/dispute is judged against the PARENT's
			// envelope (legality.go's response-scoped trap), the same shape
			// the land's ciBaseLegalityChecker builds.
			if k.Kind == fold.KindResponse {
				env.From = parentOwnerSys
			}
			prior := fold.Result{Kind: k.Kind, State: k.State}
			version := ""
			if k.Kind == fold.KindContract {
				// The land passes the event's canonical version; a contract
				// past draft carries it in prior.Versions.
				version = "1.0.0"
				if k.State != fold.StateDraft {
					prior.Versions = map[string]fold.State{version: k.State}
				}
			}

			where := string(k.Kind) + "/" + string(k.State) + " [" + v.name + "]"
			checked[string(k.Kind)+"/"+string(k.State)+"/"+verdict.Expected] = true
			for _, sys := range verdict.Owners {
				// A precondition-bearing row (decision supersede) needs a
				// resolved successor; supply the satisfying one, authored by
				// the owed system, so this test asks only "is the owed move
				// legal for its owner", never "is any successor attached".
				successor := &fold.SuccessorFacts{Author: sys, State: fold.StateApproved}
				got := fold.CheckCandidateWithSuccessor(k.Kind, prior, verdict.Expected, version, env,
					fold.Actor{System: sys}, fold.MembershipMember, successor)
				if got != fold.VerdictLegal {
					problems = append(problems, where+": pendency says "+sys+" owes "+verdict.Expected+
						", but the land check answers "+string(got))
				}
			}
		}
	}

	if !checked[string(fold.KindContract)+"/"+string(fold.StatePublished)+"/activate"] {
		t.Fatal("the probe never reached contract/published's owed activate — the variant set lost OperationalDebtOwed, and this test no longer covers the defect it was written for")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d owed move(s) the land would refuse (%d distinct owed moves checked):\n  %s",
			len(problems), len(checked), strings.Join(problems, "\n  "))
	}
}
