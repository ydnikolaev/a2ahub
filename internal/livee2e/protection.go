package livee2e

// protection.go holds the branch-protection and repo-settings bodies the rig
// applies, as DATA rather than as a literal buried in a provisioning function.
//
// It is untagged on purpose — the same reason spacehealth.go and
// probeenvelope.go are. Everything behind `//go:build livee2e` is invisible to
// `make check`, and what needs checking here is not the HTTP call but the
// AGREEMENT between two documents:
//
//	space-template/BRANCH-PROTECTION.md  — what an operator is told to apply
//	these maps                           — what the rig actually applies
//
// Those two drifted, in three places at once, and the drift was load-bearing:
// the checklist left `required_approving_review_count` unstated (the natural
// reading is 1, which blocks every legal artifact PR and defeats the
// auto-merge row two lines below), described `require_code_owner_reviews` as
// ON while production ran it as the ONLY thing gating G4, and claimed
// `enforce_admins: true` while every real space runs it false for a reason.
//
// Nothing compared them, so nothing could notice. protection_test.go compares
// them now, on every commit, and that is the whole point of these values
// living out here where a hermetic test can read them.

// requiredCheckContext is the compound required check branch protection names
// (spec 33 §11 / spec 34 §2) — the exact string internal/host's CheckStatus
// resolves against (AC-961.1).
//
// It lives HERE rather than in provision_live.go so the untagged half of this
// package can read it: a second const with the same value, one visible to
// `make check` and one not, is the duplication this file exists to remove.
const requiredCheckContext = "a2a-validate / validate"

// RepoSettingsBody is the repo-level settings the rig applies — the two that
// are OFF by default on every newly created GitHub repo and that a space does
// not work without.
//
// `allow_auto_merge` is the one the write funnel's central promise depends on:
// `a2a submit` opens a PR and arms auto-merge, and GitHub refuses to arm it
// when this is off, so the write sits green and terminal. It was false on the
// live getvisa space, which is how every submit came to report pending-merge
// over a PR nothing would ever merge.
func RepoSettingsBody() map[string]any {
	return map[string]any{
		"allow_auto_merge":       true,
		"delete_branch_on_merge": true,
	}
}

// ProtectionBody is the branch-protection body the rig applies to `main`, and
// therefore the definition of "the configuration production runs" that
// BRANCH-PROTECTION.md is checked against.
//
// It MIRRORS production deliberately rather than being maximally strict —
// spec 36 §T6-a. `enforce_admins: false` in particular is not laxity: a sole
// code owner must be able to merge their own `space.yaml` edits, and a rig
// that armed it true would certify a configuration no space we operate uses.
//
// Returned fresh each call rather than held in a package var: a caller that
// mutated a shared map before handing it to GitHub would change what every
// later provision applies, silently and in a way no test would attribute.
func ProtectionBody() map[string]any {
	return map[string]any{
		"required_status_checks": map[string]any{
			"strict": false,
			"checks": []map[string]any{{"context": requiredCheckContext}},
		},
		"enforce_admins": false,
		"required_pull_request_reviews": map[string]any{
			"require_code_owner_reviews":      true,
			"required_approving_review_count": 0,
		},
		"restrictions":       nil,
		"allow_force_pushes": false,
		"allow_deletions":    false,
	}
}
