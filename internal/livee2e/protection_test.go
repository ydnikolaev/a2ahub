package livee2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checklistPath is the operator-facing document this file holds to account.
const checklistPath = "../../space-template/BRANCH-PROTECTION.md"

// TestChecklistStatesWhatTheRigActuallyApplies is §6's "checklist ↔ reality"
// row, and it is the gate that did not exist while the two disagreed.
//
// The template shipped a branch-protection checklist that no space we operate
// followed, in three places at once, and every one of the three was
// load-bearing:
//
//   - `required_approving_review_count` was UNSTATED. The natural reading is 1,
//     which applies to every PR regardless of path — blocking every legal
//     artifact PR and defeating the auto-merge row two lines below it.
//   - `require_code_owner_reviews` was described as merely ON, while in
//     production it is the ONLY thing gating G4.
//   - `enforce_admins` was claimed true, while every real space runs it false
//     for a reason (a sole code owner must merge their own space.yaml edits).
//
// Nothing compared the document to the configuration, so nothing could notice.
// This test compares them: every key/value ProtectionBody() and
// RepoSettingsBody() send to GitHub must appear, as a literal, in the file an
// operator is told to follow.
//
// It asserts in ONE direction on purpose — every applied value is documented.
// The reverse (every documented value is applied) is not checkable here and
// should not be faked: the checklist legitimately carries rows a2a never
// applies at all, because a2a mutates no repo settings and no branch
// protection by design (spec 35's boundary). Claiming a two-way check would be
// the more comforting and less true assertion.
func TestChecklistStatesWhatTheRigActuallyApplies(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Clean(checklistPath))
	if err != nil {
		t.Fatalf("read %s: %v — the checklist an operator follows must be readable from here, or this "+
			"gate is guarding nothing", checklistPath, err)
	}
	doc := string(raw)

	for _, want := range flattenProtection(t) {
		if !strings.Contains(doc, want.literal) {
			t.Errorf("%s does not state %s.\n"+
				"The rig applies %s, and an operator following this document would not know to.\n"+
				"Every one of the three drifts this gate exists for was of exactly this shape: a value "+
				"the configuration has and the checklist leaves to inference.",
				checklistPath, want.literal, want.what)
		}
	}
}

// TestChecklistDoesNotClaimAdminsAreBoundWhileTheyAreNot catches the class the
// value-by-value comparison above structurally cannot see: a PROSE claim that
// contradicts a value.
//
// The checklist said "Direct pushes to `main`: forbidden for all actors,
// including admin" two rows above `enforce_admins: false`, which is what makes
// exactly that not true. Both rows passed every check there was — the value
// comparison only reads keys that appear in the applied body, and no key is
// named "direct pushes are forbidden for admins".
//
// The cost is not academic. The published onboarding told an operator to
// "confirm by hand that a direct push to main is rejected"; on 2026-07-26 that
// push SUCCEEDED against a space configured exactly as this checklist says,
// with GitHub answering `remote: Bypassed rule violations for refs/heads/main`.
// Following the documentation made a correct space look broken.
//
// So: while `enforce_admins` is false, the document must say what an admin
// actually experiences, in GitHub's own words — a positive assertion, because
// requiring the ABSENCE of a wrong phrase is satisfied by any rewording of it.
func TestChecklistDoesNotClaimAdminsAreBoundWhileTheyAreNot(t *testing.T) {
	t.Parallel()

	enforceAdmins, ok := ProtectionBody()["enforce_admins"].(bool)
	if !ok {
		t.Fatal("enforce_admins is not a bool in ProtectionBody() — this gate cannot reason about it")
	}
	if enforceAdmins {
		// Declared, with the reason, in skipDeclarations() (skipdeclarations.go)
		// — P8 AC5's own registry. TestSkipDeclarationsCoverEverySkip
		// (skipdeclarations_test.go) reds if this call ever drifts from that
		// entry.
		t.Skip("enforce_admins is true, so admins ARE bound and the document may say so")
	}

	raw, err := os.ReadFile(filepath.Clean(checklistPath))
	if err != nil {
		t.Fatalf("read %s: %v", checklistPath, err)
	}
	doc := string(raw)

	if !strings.Contains(doc, "Bypassed rule violations") {
		t.Errorf("%s never mentions GitHub's own `Bypassed rule violations` answer, while the "+
			"configuration it documents sets enforce_admins: false.\n"+
			"An admin's direct push to main SUCCEEDS under this configuration. A reader who is told "+
			"otherwise, and who checks, concludes their space is misconfigured — which is what happened "+
			"on 2026-07-26.", checklistPath)
	}
	// The specific sentence that was wrong. Named as a literal because it is
	// the one an operator acted on.
	if strings.Contains(doc, "including admin (bypass reserved") {
		t.Errorf("%s still carries the old claim that direct pushes are forbidden \"including admin\", "+
			"which `enforce_admins: false` contradicts", checklistPath)
	}
}

// TestChecklistShipsTheCallThatAppliesIt is AC-1020.2. The checklist is
// followed by a human under time pressure, and prose has already been
// translated into an API shape wrongly once — so it ships the call, not a
// description of the call.
func TestChecklistShipsTheCallThatAppliesIt(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Clean(checklistPath))
	if err != nil {
		t.Fatalf("read %s: %v", checklistPath, err)
	}
	doc := string(raw)

	for _, want := range []struct{ substr, why string }{
		{"gh api", "ship the call, not a description of it — the values above have to be applied by somebody"},
		{"-X PUT", "branch protection is a PUT: a PATCH silently leaves the unnamed settings at whatever they were"},
		{"/branches/main/protection", "name the endpoint, so the call is complete rather than a fragment"},
		{"-X PATCH", "the two repo settings are a PATCH on the repo itself — a different call from protection"},
	} {
		if !strings.Contains(doc, want.substr) {
			t.Errorf("%s is missing %q — %s", checklistPath, want.substr, want.why)
		}
	}
}

type protectionLiteral struct {
	literal string // what must appear in the document
	what    string // how to describe it in a failure
}

// flattenProtection turns the two bodies into the literals a reader must be
// able to find. Derived from the maps rather than hand-listed: a hand-listed
// expectation is a third copy, and it would go stale in exactly the way the two
// it is meant to reconcile already did.
func flattenProtection(t *testing.T) []protectionLiteral {
	t.Helper()

	var out []protectionLiteral
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			switch val := v.(type) {
			case map[string]any:
				walk(prefix+k+".", val)
			case bool, int:
				out = append(out, protectionLiteral{
					literal: fmt.Sprintf("%s: %v", k, val),
					what:    fmt.Sprintf("%s%s = %v", prefix, k, val),
				})
			default:
				// `restrictions: nil` and the checks slice: not scalar
				// settings, so not compared as literals. Named here rather
				// than silently skipped — a switch with a quiet default is how
				// a value drops out of a gate unnoticed.
				t.Logf("not literal-compared: %s%s (%T)", prefix, k, v)
			}
		}
	}
	walk("", ProtectionBody())
	walk("", RepoSettingsBody())
	return out
}
