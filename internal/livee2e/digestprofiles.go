package livee2e

import (
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// digestprofiles.go wires universe 4 (answers-that-hold-2026-08 spec 10
// §"The input universes", row 4) to the reader-reader assertion shape: EACH
// registered digest profile (contract.DigestProfiles(), added for this wave
// in daf6f2e1) is an INPUT AXIS crossed with the descriptor's own bytes, not
// merely a driven id over a fixed cell — spec 10 §"The input universes":
// "the file a84550's two verbs disagree about is the descriptor, whose role
// is minted as a bare string outside the closed role vocabulary. Every role
// cell would be green on it."
//
// No per-profile branch is written here (AC-14: no profile list inside
// internal/livee2e) — the SAME descriptorRaw/descriptor/candidates is fed to
// contract.BuildCarriedSet once per profile in contract.DigestProfiles(),
// and exactly one uniform rule is checked for every profile, known or not:
// BuildCarriedSet must either HONOUR the requested profile (a returned
// CarriedSet whose own Profile field equals it) or REFUSE it with a
// declared, machine-readable contract.IssueUnsupportedProfile naming it —
// never silently build under a different profile than requested, and never
// refuse for an undeclared reason. A fourth profile registered tomorrow gets
// a cell under this same rule with no edit to this file.

// digestProfileCells is universe 4's own cross-product. profiles is a
// PARAMETER — the production caller passes contract.DigestProfiles()'s real
// answer; a fixture test (AC-9/AC-14) passes a synthetic profile the real
// package does not register, to prove this function reacts to whatever the
// registry publishes with no edit to this file. descriptorRaw/descriptor/
// candidates are the descriptor's own bytes this cell crosses every profile
// against — a84550's own axis.
func digestProfileCells(profiles []contract.DigestProfile, descriptorRaw []byte, descriptor contract.Descriptor, candidates []contract.CandidateFile) (evaluated []string, errs []error) {
	for _, profile := range profiles {
		id := string(profile)
		evaluated = append(evaluated, id)

		set, issues := contract.BuildCarriedSet(profile, descriptorRaw, descriptor, candidates)
		switch {
		case len(issues) == 0:
			if set.Profile != profile {
				errs = append(errs, fmt.Errorf(
					"digest profile %q: BuildCarriedSet raised no issue but returned a set built under "+
						"profile %q instead — the requested profile was silently substituted", id, set.Profile))
			}
		case unsupportedProfileIssue(issues, id):
			// A declared, machine-readable refusal naming this exact profile —
			// legitimate: not every registered profile has a CarriedSet builder
			// (export-source-v1 is a PROJECTION of a contract-set-v2 set, per
			// CarriedSet.ExportSource, never built directly).
		default:
			errs = append(errs, fmt.Errorf(
				"digest profile %q: BuildCarriedSet refused for a reason OTHER than an "+
					"IssueUnsupportedProfile naming this profile: %+v", id, issues))
		}
	}
	return evaluated, errs
}

// unsupportedProfileIssue reports whether issues contains a declared
// contract.IssueUnsupportedProfile whose own Detail names profileID — never
// merely THAT kind of issue occurred, but that it names the SAME profile
// this cell is checking, so a refusal meant for a different reason cannot
// pass by carrying the right Kind for the wrong Detail.
func unsupportedProfileIssue(issues []contract.Issue, profileID string) bool {
	for _, issue := range issues {
		if issue.Kind == contract.IssueUnsupportedProfile && strings.Contains(issue.Detail, profileID) {
			return true
		}
	}
	return false
}
