package cli

import (
	"testing"
)

// TestValidateCI_DecisionSupersedeJudgesTheSuccessor pins V3's half of the two
// decision-supersede rows (fold/table.go: `approved | supersede` gated by an
// approved successor, `rejected | supersede` gated by the successor's author).
//
// Before this test V3 built its lifecycle candidate with no subject envelope
// and no successor facts, so fold refused every such supersede for a nil
// successor and validate labelled the refusal LFC-002: the merge refused what
// the author's own CLI had just found legal.
//
// The successor is judged at the MERGE BASE, the same tree the predecessor's
// prior is folded from. The last subtest pins that: a successor approval that
// rides in the same PR as the supersede is not yet a fact the space agreed to.
func TestValidateCI_DecisionSupersedeJudgesTheSuccessor(t *testing.T) {
	t.Parallel()
	engine := ciEngine(t)
	const (
		predecessorID   = "XD-axon-20260730-pd12"
		successorID     = "XD-axon-20260730-sc34"
		absentID        = "XD-axon-20260730-ab56"
		predecessorPath = "decisions/" + predecessorID + ".md"
		successorPath   = "decisions/" + successorID + ".md"

		seomatrixSupersedePath = "seomatrix/events/2026/01J40A7M9P1S3V5W7Y9A1C3FZZ.yaml"
		axonSupersedePath      = "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3FZY.yaml"
	)

	// history renders a decision's base events: proposed by axon, then
	// carried to `approved` (both required approvers) or `rejected`
	// (seomatrix). ulidPrefix keeps the two decisions' event files apart.
	history := func(id, ulidPrefix, state string) map[string]string {
		out := map[string]string{
			"axon/events/2026/" + ulidPrefix + "0.yaml": ciLifecycleEvent(ulidPrefix+"0", id, "propose", "axon", ""),
		}
		switch state {
		case "approved":
			out["axon/events/2026/"+ulidPrefix+"1.yaml"] = ciLifecycleEvent(ulidPrefix+"1", id, "approve", "axon", "")
			out["seomatrix/events/2026/"+ulidPrefix+"2.yaml"] = ciLifecycleEvent(ulidPrefix+"2", id, "approve", "seomatrix", "")
		case "rejected":
			out["seomatrix/events/2026/"+ulidPrefix+"2.yaml"] = ciLifecycleEvent(ulidPrefix+"2", id, "reject", "seomatrix", "")
		case "proposed":
		default:
			t.Fatalf("history: unknown state %q", state)
		}
		return out
	}
	base := func(predecessorState, successorState string) map[string]string {
		files := map[string]string{predecessorPath: validDecision(predecessorID, "axon")}
		for p, body := range history(predecessorID, "01J40A7M9P1S3V5W7Y9A1C3F0", predecessorState) {
			files[p] = body
		}
		if successorState != "" {
			files[successorPath] = validDecision(successorID, "axon")
			for p, body := range history(successorID, "01J40A7M9P1S3V5W7Y9A1C3F1", successorState) {
				files[p] = body
			}
		}
		return files
	}
	supersede := func(path, actorSystem, ref string) string {
		ulid := path[len(path)-len("01J40A7M9P1S3V5W7Y9A1C3FZZ.yaml") : len(path)-len(".yaml")]
		return ciLifecycleEvent(ulid, predecessorID, "supersede", actorSystem, "", ref)
	}
	author := map[string]string{seomatrixSupersedePath: "misha-gh", axonSupersedePath: "ydnikolaev"}

	t.Run("approved predecessor, approved successor: green", func(t *testing.T) {
		t.Parallel()
		root, baseSHA := ciLifecycleCandidateRepo(t, base("approved", "approved"),
			seomatrixSupersedePath, supersede(seomatrixSupersedePath, "seomatrix", successorID))
		code, rep, errOut := runCI(t, engine, root, fakeGit(seomatrixSupersedePath), "v3-pr", baseSHA, author[seomatrixSupersedePath])
		if code != 0 || !rep.Valid {
			t.Fatalf("supersede by an approved successor refused: code=%d codes=%v errors=%v stderr=%s",
				code, ciViolationCodes(rep), ciReportErrors(rep), errOut)
		}
	})

	t.Run("rejected predecessor, superseded by the successor's author: green", func(t *testing.T) {
		t.Parallel()
		root, baseSHA := ciLifecycleCandidateRepo(t, base("rejected", "proposed"),
			axonSupersedePath, supersede(axonSupersedePath, "axon", successorID))
		code, rep, errOut := runCI(t, engine, root, fakeGit(axonSupersedePath), "v3-pr", baseSHA, author[axonSupersedePath])
		if code != 0 || !rep.Valid {
			t.Fatalf("supersede by the successor's own author refused: code=%d codes=%v errors=%v stderr=%s",
				code, ciViolationCodes(rep), ciReportErrors(rep), errOut)
		}
	})

	refusals := []struct {
		name                   string
		predecessor, successor string
		path, actor, ref       string
		wantUnresolved         bool
	}{
		{
			name: "approved predecessor, successor not approved: LFC-005", predecessor: "approved", successor: "proposed",
			path: seomatrixSupersedePath, actor: "seomatrix", ref: successorID,
		},
		{
			name: "rejected predecessor, actor not the successor's author: LFC-005", predecessor: "rejected", successor: "proposed",
			path: seomatrixSupersedePath, actor: "seomatrix", ref: successorID,
		},
		{
			name: "approved predecessor, successor absent: LFC-005 and LFC-006", predecessor: "approved", successor: "",
			path: seomatrixSupersedePath, actor: "seomatrix", ref: absentID, wantUnresolved: true,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, baseSHA := ciLifecycleCandidateRepo(t, base(tc.predecessor, tc.successor),
				tc.path, supersede(tc.path, tc.actor, tc.ref))
			code, rep, errOut := runCI(t, engine, root, fakeGit(tc.path), "v3-pr", baseSHA, author[tc.path])
			assertSupersedeRefusal(t, code, rep, errOut, tc.path, tc.wantUnresolved)
		})
	}

	// The lead's ruling, pinned: the successor's state is read at the merge
	// base. At base the successor is proposed and approved by axon only; the
	// PR adds seomatrix's approval (completing the quorum at HEAD) beside
	// seomatrix's supersede. The approval itself is legal and authorized;
	// the supersede must still be refused, because at the merge base the
	// successor is not approved.
	t.Run("successor approved only inside the same PR: LFC-005", func(t *testing.T) {
		t.Parallel()
		const approvePath = "seomatrix/events/2026/01J40A7M9P1S3V5W7Y9A1C3F1Z.yaml"
		files := base("approved", "proposed")
		files["axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3F11.yaml"] = ciLifecycleEvent(
			"01J40A7M9P1S3V5W7Y9A1C3F11", successorID, "approve", "axon", "")
		root, baseSHA := ciLifecycleCandidateRepo(t, files,
			seomatrixSupersedePath, supersede(seomatrixSupersedePath, "seomatrix", successorID))
		contractWriteFile(t, root, approvePath, ciLifecycleEvent("01J40A7M9P1S3V5W7Y9A1C3F1Z", successorID, "approve", "seomatrix", ""))

		code, rep, errOut := runCI(t, engine, root, fakeGit(approvePath, seomatrixSupersedePath), "v3-pr", baseSHA, "misha-gh")
		if len(rep.DiffAuthz) != 0 {
			t.Fatalf("the fixture must be diff-authz clean so only the successor reading decides it: %+v", rep.DiffAuthz)
		}
		for _, a := range rep.Artifacts {
			if a.Path == approvePath && (a.Error != "" || a.Result == nil || !a.Result.Valid) {
				t.Fatalf("the successor's own completing approval must be legal: %+v", a)
			}
		}
		assertSupersedeRefusal(t, code, rep, errOut, seomatrixSupersedePath, false)
	})
}

// assertSupersedeRefusal requires LFC-005 on path, never LFC-002, and LFC-006
// exactly when the successor could not be resolved.
func assertSupersedeRefusal(t *testing.T, code int, rep ciReport, errOut, path string, wantUnresolved bool) {
	t.Helper()
	codes := ciViolationCodes(rep)
	if code != 1 || rep.Valid {
		t.Fatalf("supersede not refused: code=%d valid=%v codes=%v errors=%v stderr=%s",
			code, rep.Valid, codes, ciReportErrors(rep), errOut)
	}
	if !ciReportHasViolation(rep, path, "LFC-005") {
		t.Errorf("LFC-005 missing on %s: got codes %v, errors %v; want LFC-005", path, codes, ciReportErrors(rep))
	}
	if ciReportHasViolation(rep, path, "LFC-002") {
		t.Errorf("LFC-002 on %s: got codes %v; want LFC-005, a decision supersede is never an LFC-002", path, codes)
	}
	if got := ciReportHasViolation(rep, path, "LFC-006"); got != wantUnresolved {
		t.Errorf("LFC-006 on %s = %v, want %v (codes %v)", path, got, wantUnresolved, codes)
	}
}

// ciViolationCodes lists every violation code in rep, in report order.
func ciViolationCodes(rep ciReport) []string {
	var out []string
	for _, a := range rep.Artifacts {
		if a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			out = append(out, a.Path+":"+v.Code)
		}
	}
	return out
}

// ciReportErrors lists every artifact-level error in rep, in report order.
func ciReportErrors(rep ciReport) []string {
	var out []string
	for _, a := range rep.Artifacts {
		if a.Error != "" {
			out = append(out, a.Path+": "+a.Error)
		}
	}
	return out
}
