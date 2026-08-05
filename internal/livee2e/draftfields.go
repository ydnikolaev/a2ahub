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
