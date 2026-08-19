package livee2e

import "strings"

// draftFieldArgs is the live matrix's scenario data expressed only through
// the shipped authoring interface. Values are YAML where the template node is
// structured; dotted keys address nested mappings. Adding a new artifact kind
// without describing its public inputs fails closed.
func draftFieldArgs(artifactType, spaceID, me, peer string) ([]string, bool) {
	fields := []string{
		"title=matrix " + artifactType,
		"space=" + spaceID,
		"to=[" + peer + "]",
	}
	switch artifactType {
	case "announcement":
		fields = append(fields, "category=notice")
	case "contract":
		fields = append(fields, "category=other")
	case "requirement":
		fields = append(fields,
			"category=other",
			`interim_behavior=we proceed with the current shape`,
			"needed_by=2026-12-31",
			`acceptance_criteria=["the artifact validates"]`,
			`expected_response.shape=a short prose answer`,
		)
	case "question":
		fields = append(fields,
			"category=clarification",
			`expected_response.shape=a short prose answer`,
		)
	case "work_request":
		fields = append(fields,
			"category=data",
			`interim_behavior=we proceed with the current shape`,
			"needed_by=2026-12-31",
			`acceptance_criteria=["the artifact validates"]`,
			`expected_response.shape=a short prose answer`,
		)
	case "decision":
		fields = append(fields,
			"to=["+me+","+peer+"]",
			"required_approvers=["+me+","+peer+"]",
			`context=the live matrix needs this artifact`,
			`options_considered=["option A","option B"]`,
		)
	case "handoff":
		fields = append(fields,
			"fulfills=[XR-"+me+"-matrix-req]",
			"refs=[]",
			`deliverables=[{"name":"matrix artifact","ref":"live-e2e/matrix@1.0.0","kind":"doc"}]`,
			`verification=the matrix re-ran the suite; all green`,
			`acceptance_criteria=["the artifact validates"]`,
		)
	case "response":
		// Live responses are normally created by `a2a respond`. Keep this
		// public draft path complete for catalogue growth.
		fields = append(fields,
			"parent=XQ-"+peer+"-20260729-ab12",
			"result=answered",
		)
	default:
		return nil, false
	}

	args := make([]string, 0, len(fields)*2)
	for _, field := range fields {
		args = append(args, "--field", field)
	}
	if artifactType == "decision" {
		args = append(args, "--actor-kind", "human", "--actor-name", "live-e2e-operator")
	} else {
		args = append(args, "--actor-kind", "agent", "--actor-name", "live-e2e")
	}
	return args, true
}

// standingDraftTypes mirrors internal/cli's ClassStanding branch: `a2a new`
// mints a standing id from the slug and REFUSES without one
// ("--slug (or --field slug=<slug>) is required for standing types"). The
// mirror is small and deliberate — newTypePrefix is unexported, and the cost
// of getting this wrong is measured in hours.
//
// It is written down because the tier learned it the expensive way. The
// data-loop family shipped `DraftAndSubmit(ctx, a, "contract")` with no slug
// while all four sibling scenarios passed one. Its only prior live run died
// on an earlier step, so nothing ever executed that line — and the release's
// headline feature turned out to have an impossible FIRST authoring step,
// discovered 2h19m into a live matrix against real GitHub. checkout.Draft
// refuses it in microseconds instead.
var standingDraftTypes = map[string]bool{"contract": true, "requirement": true}

// hasSlugArg reports whether extra carries a slug in either shape `a2a new`
// accepts: the `--slug <value>` flag or `--field slug=<value>`.
func hasSlugArg(extra []string) bool {
	for index, arg := range extra {
		if arg == "--slug" && index+1 < len(extra) {
			return true
		}
		if strings.HasPrefix(arg, "--slug=") {
			return true
		}
		if strings.HasPrefix(arg, "slug=") {
			return true
		}
	}
	return false
}

// draftBody is the live matrix's BODY for artifact types whose body carries a
// shipped invariant, expressed the same way draftFieldArgs expresses fields:
// only through the public authoring interface (`a2a new --body-file`).
//
// It exists because POL-022 (defects-fix-2026-08 P9) refuses a handoff whose
// §16.2-required sections carry no content, and the matrix authored handoffs
// straight from the template — five headings and nothing under them. The rule
// caught the driver, which is the rule working: a handoff that says nothing is
// not a handoff, and a conformance path that submits one was proving a
// narrative no real producer could have written.
//
// The text is deliberately short and specific rather than lorem: what a
// section must NOT be is empty, and a driver padding them with filler would
// satisfy the letter while teaching the next reader that padding is the answer.
//
// An empty return means the type's body carries no invariant and the template
// stands as rendered.
func draftBody(artifactType string) (string, bool) {
	if artifactType == "handoff" {
		return strings.Join([]string{
			"## Context",
			"",
			"The live conformance matrix drives this path against the real binary.",
			"This handoff exists so the path can reach the transitions it declares.",
			"",
			"## What was built",
			"",
			"The deliverable named in `deliverables[]` above — a matrix artifact,",
			"referenced by the id the path's own step recorded.",
			"",
			"## How to verify",
			"",
			"Re-run the suite named in `verification` above; the matrix asserts the",
			"folded state after each step rather than trusting the command's exit.",
			"",
			"## How to operate",
			"",
			"Nothing to operate: this artifact is driven by the matrix and is never",
			"consumed by a running system.",
			"",
			"## Limitations & next steps",
			"",
			"It carries one deliverable and one criterion, which is the minimum a",
			"path needs. A real handoff carries what its own work produced.",
			"",
		}, "\n"), true
	}
	return "", false
}
