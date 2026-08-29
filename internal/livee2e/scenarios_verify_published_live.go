//go:build livee2e

package livee2e

// scenarios_verify_published_live.go — answers-that-hold-2026-08 P7 (spec
// 07-where-do-my-contracts-stand.md): the live-e2e proof for `a2a contract
// verify-published`, matching runContractIntegrityScenarios'/runHappyScenarios'
// own "exported-shape entry, thin fan-out" convention. Every helper below is
// prefixed vpub* — this family's own namespace, so identifiers cannot
// collide with a sibling family's (scenarios_contract_integrity_live.go's
// own doc comment states the same discipline for its ac973* prefix).
//
// Three rows, one per acceptance shape this verb's spec names:
//
//   - vpubMatchedViaLocalOverride (ACs 1-2, system A): a real contract
//     publishes, its exact published version materializes into a tracked
//     local directory (never `.a2a/staging`), and `verify-published --local`
//     reports it "matched" with the version RESOLVED from the descriptor —
//     the invocation itself never names one.
//   - vpubZeroRowsWarns (AC-3, system B): B never publishes a contract of
//     its own anywhere in this suite (every contract-publishing helper in
//     this package runs against h.A) — so B is this family's real,
//     naturally-occurring "system providing zero contracts" subject, and
//     the run must print the denominator and exit 0 (a WARN), never refuse.
//   - vpubAbsentMirrorRefuses (AC-4, system B): B's own synced mirror is
//     removed and the same verb REFUSES, naming `a2a sync` — the asymmetry
//     spec 07 T1 states directly ("a zero-row run WARNS; a stale or absent
//     mirror IS a refusal").
//
// AC-5 (a present-but-stale mirror) and AC-7 (two connected spaces) are
// deliberately NOT this file's job: AC-7 is spec 07 §11's own 2026-08-28
// amendment ("explicitly NOT the livee2e tier... two spacefixture.New
// origins... proven in internal/cli"), and this harness has no lever for
// "synced, but stale" distinct from "never synced" — see this phase's
// Deviations report.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// vpubScenario is this row's catalogue name (pathcatalogue_paths.go,
// LEAD-OWNED — see this phase's Deviations report for the exact
// registration line).
const vpubScenario = "contract-verify-published-aggregate"

// vpubSlug is this row's lowercase-kebab standing-slug base, scoped to
// h.PRFloor by liveRunSlug (bytesRegisterContract's own caller-supplied
// slugTag) so a destructive reset cannot collide with a merged semantic
// operation retained by GitHub, and so this family's contract can never
// collide with a sibling family's own baseline (bytesRegisterContract's own
// doc comment on why slugTag exists at all).
const vpubSlug = "vpub-baseline"

// vpubResult/vpubRow mirror internal/cli's own ContractVerifyPublishedResult/
// ContractVerifyPublishedRow (ADR-001-style duplication, the same shape MCP
// already carries for the identical reason: this package drives the real
// compiled binary through its CLI surface and decodes its own --json output
// rather than importing internal/cli's wire type, matching this file's own
// sibling scenarios' contractPublishPull -> space.ContractPublicationResult
// idiom for a package this file DOES already share — verify-published's own
// result type has no such shared home yet, spec 07 §7/AC-8's own render-
// ledger gap, so it is decoded locally here instead).
type vpubRow struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Local   string `json:"local"`
	Detail  string `json:"detail"`
}

type vpubResult struct {
	System string    `json:"system"`
	Total  int       `json:"total"`
	Rows   []vpubRow `json:"rows"`
}

// runVerifyPublishedScenarios is this family's ONE exported-shape entry
// (plan §"Wave-3 design notes": "the fan-out seam is four functions, not a
// registry").
func runVerifyPublishedScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{
		vpubMatchedViaLocalOverride(ctx, h),
		vpubZeroRowsWarns(ctx, h),
		vpubAbsentMirrorRefuses(ctx, h),
	}

	// Same parking discipline runHappyScenarios'/runContractIntegrityScenarios'
	// own tails document: leave both checkouts on a clean, synced state so
	// whichever family runs next does not inherit a mirror this row
	// deliberately removed. Best-effort; a failure here does not change an
	// already-recorded result above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// vpubResultFromErr renders err into this row's ONE Result, tagging
// Expected with step (this family's own convention, matching
// ac973ResultFromErr) so a failing report line names WHICH step of the
// sequence broke. Delegates the verdict mapping to happyVerdictForErr, the
// one place a network/timeout class is distinguished from a real product
// failure, so this family does not re-decide that rule a second time.
func vpubResultFromErr(system, step string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	return Result{
		Scenario: vpubScenario, System: system, Surface: SurfaceCLI,
		Verdict: verdict, Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: err.Error(),
	}
}

// vpubFail builds a VerdictFail row for an assertion this family observed
// directly (the CLI ran, but what it reported was wrong) — also
// step-tagged, same reasoning as vpubResultFromErr.
func vpubFail(system, step, observed, expected, detail string) Result {
	return Result{
		Scenario: vpubScenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// vpubFindRow returns the row named id, or nil if the report carries none.
func vpubFindRow(result vpubResult, id string) *vpubRow {
	for i := range result.Rows {
		if result.Rows[i].ID == id {
			return &result.Rows[i]
		}
	}
	return nil
}

// vpubMatchedViaLocalOverride is ACs 1-2: a real published contract
// materializes as a tracked local subject (never `.a2a/staging` — US-4, the
// property this whole phase exists for), and `contract verify-published
// --local` reports it "matched" with the version RESOLVED from the
// published descriptor, though the invocation itself names none.
func vpubMatchedViaLocalOverride(ctx context.Context, h *harness) Result {
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return vpubResultFromErr(SystemA, "sync-before-baseline", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh verify-published run")
	}

	// bytesRegisterContract (scenarios_bytes_live.go) is this package's
	// already-proven "draft, submit, land, publish v1.0.0, register, land"
	// recipe — reused verbatim rather than re-derived (this phase's own
	// anti-duplication rule: AGGREGATE the comparison, never re-implement
	// what publishes and verifies it).
	ref, err := bytesRegisterContract(ctx, h, a, vpubSlug)
	if err != nil {
		return vpubResultFromErr(SystemA, "register-baseline", err, "a real contract publishes v1.0.0 (bytesRegisterContract's own two-step baseline)")
	}
	contractID, version, found := strings.Cut(ref, "@")
	if !found {
		return vpubResultFromErr(SystemA, "register-baseline", fmt.Errorf("livee2e: %q is not an <id>@<version> ref", ref), "bytesRegisterContract returns an exact <id>@<version> ref")
	}

	// The materialized destination is a TRACKED, project-relative directory
	// under this checkout's own working tree — exactly the "local subject
	// the producer can name" US-4 asks for, and the opposite of
	// `.a2a/staging` (untracked, rewritten by the next publish).
	slug := strings.TrimPrefix(contractID, "XC-"+a.System+"-")
	relDest := path.Join(".a2a", "vpub-check", slug)
	absDest := filepath.Join(a.Dir, filepath.FromSlash(relDest))
	if err := os.RemoveAll(absDest); err != nil {
		return vpubResultFromErr(SystemA, "materialize-local-subject", err, "a stale local check directory from a prior run can be cleared")
	}
	if _, stderr, err := a.Run(ctx, "contract", "materialize", ref, "--to", relDest); err != nil {
		return vpubResultFromErr(SystemA, "materialize-local-subject", fmt.Errorf("%w: %s", err, stderr), "the just-published version materializes as a tracked, real local subject")
	}

	stdout, stderr, err := a.Run(ctx, "contract", "verify-published", "--json", "--local", contractID+"="+relDest)
	if err != nil {
		return vpubResultFromErr(SystemA, "verify-published-matched", fmt.Errorf("%w: %s", err, stderr), "verify-published exits 0 when the materialized local subject matches the published bytes")
	}
	var result vpubResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return vpubResultFromErr(SystemA, "verify-published-matched", fmt.Errorf("decode --json output: %w", err), "verify-published --json decodes as the documented aggregate shape")
	}
	row := vpubFindRow(result, contractID)
	if row == nil {
		return vpubFail(SystemA, "verify-published-matched", stdout, fmt.Sprintf("AC-1: the report carries a row for %s", contractID), stdout)
	}
	if row.Status != "matched" {
		return vpubFail(SystemA, "verify-published-matched", row.Status, `AC-1: the freshly materialized local subject matches the published bytes (status "matched")`, stdout)
	}
	if row.Version != version {
		return vpubFail(SystemA, "verify-published-resolves-version", row.Version,
			fmt.Sprintf("AC-2: the version is RESOLVED from the published descriptor (%s), never asked of the caller — the invocation named none", version), stdout)
	}

	narrative := fmt.Sprintf("%s: published %s, materialized to %s, verify-published --local resolved version %s and reported \"matched\"", contractID, ref, relDest, row.Version)
	return Result{
		Scenario: vpubScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: narrative, PassEvidence: narrative,
	}
}

// vpubZeroRowsWarns is AC-3: B never publishes a contract of its own
// anywhere in this suite (every contract-publishing call in this package
// runs against h.A — grep confirms it), making B this family's real,
// naturally-occurring "system providing zero contracts" subject. The run
// must print the denominator (total=0) and exit 0 — a WARN, never a
// refusal, distinguishable from a clean multi-row pass by the printed
// count alone.
func vpubZeroRowsWarns(ctx context.Context, h *harness) Result {
	b := h.B

	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return vpubResultFromErr(SystemB, "sync-before-zero-row", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before the zero-row stanza")
	}
	stdout, stderr, err := b.Run(ctx, "contract", "verify-published", "--json")
	if err != nil {
		return vpubResultFromErr(SystemB, "verify-published-zero-rows", fmt.Errorf("%w: %s", err, stderr), "AC-3: a system providing zero contracts still exits 0 (a WARN, never a refusal)")
	}
	var result vpubResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return vpubResultFromErr(SystemB, "verify-published-zero-rows", fmt.Errorf("decode --json output: %w", err), "verify-published --json decodes as the documented aggregate shape")
	}
	if result.Total != 0 || len(result.Rows) != 0 {
		return vpubFail(SystemB, "verify-published-zero-rows", fmt.Sprintf("total=%d rows=%d", result.Total, len(result.Rows)),
			"AC-3: this consumer-only system provides no contracts of its own, so the denominator is exactly 0", stdout)
	}

	const narrative = "verify-published --json reported total=0 for a system providing no contracts — distinguishable from a clean multi-row pass by the printed count alone, and exits 0 (a WARN, never a refusal)"
	return Result{
		Scenario: vpubScenario, System: SystemB, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: narrative, PassEvidence: narrative,
	}
}

// vpubAbsentMirrorRefuses is AC-4: an absent mirror REFUSES the whole run
// and names `a2a sync` — never a zero-row pass, the asymmetry spec 07 T1
// states directly. B's own mirror is removed and restored (a fresh `a2a
// sync`) unconditionally, success or failure, so a later scenario family in
// the same run never inherits a mirror this row deliberately deleted.
func vpubAbsentMirrorRefuses(ctx context.Context, h *harness) Result {
	b := h.B

	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return vpubResultFromErr(SystemB, "sync-before-absent-mirror", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before the absent-mirror stanza")
	}
	mirrorDir := b.MirrorDir()
	if err := os.RemoveAll(mirrorDir); err != nil {
		return vpubResultFromErr(SystemB, "remove-mirror", err, "this system's own synced mirror can be removed to simulate an absent one")
	}

	stdout, stderr, runErr := b.Run(ctx, "contract", "verify-published")
	// Restore immediately, success or failure — see this function's own doc
	// comment.
	_, _, _ = b.Run(ctx, "sync")

	if runErr == nil {
		return vpubFail(SystemB, "verify-published-absent-mirror", stdout,
			"AC-4: an absent mirror REFUSES (nonzero exit), naming `a2a sync` — never a zero-row pass", stdout)
	}
	if !strings.Contains(stderr, "a2a sync") {
		return vpubFail(SystemB, "verify-published-absent-mirror", stderr,
			"AC-4: the refusal names the sync command (`a2a sync`)", stderr)
	}

	const narrative = "verify-published refused against a removed mirror, naming `a2a sync` — the same shape a stale mirror is expected to take (AC-5, proven narrowly in internal/cli — see this phase's Deviations report)"
	return Result{
		Scenario: vpubScenario, System: SystemB, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: narrative, PassEvidence: narrative,
	}
}
