//go:build livee2e

package livee2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// runDataLoopScenarios drives spec 05a's contract data exchange loop against
// the real private space: A publishes a JSON-Schema contract and a
// work_request addressed to B; B packs and delivers a FAILING attempt
// (a record_count disagreement the digest cannot see, exactly spec 05a
// §T2.1's "observed" row), A records the failure, B packs and delivers a
// SECOND attempt that SUPERSEDES the first, A records a pass, B answers the
// original work_request and A closes it. It is this family's ONE
// exported-shape entry, matching runContractIntegrityScenarios/
// runSubmittedFamilyScenarios' own convention (plan §"Wave-3 design notes":
// "the fan-out seam is four functions, not a registry").
//
// This mirrors internal/e2e/data_loop_test.go's own
// TestDataLoopFailSupersedePassCloseInboxNeverAccumulates — that hermetic
// test's own doc comment names it "spec 05a §6.3's own decisive scenario" —
// rather than inventing a live sequence from scratch. Every helper below is
// prefixed dloop*, this family's own namespace, so identifiers cannot
// collide with ac973*/subfam*/happy* across the package's other scenario
// families.
//
// Every one of the five product defects internal/e2e/data_loop_test.go's own
// package doc comment catalogued (Provenance never set, `data verify`
// rejecting the digest suffix `data pack` itself writes,
// ContractSupersededBy never populated) is READ AS FIXED here, verified
// against this session's own current source rather than assumed from that
// file's prose: cmd/a2a/data_wiring.go's pack() already sets
// datapackage.Provenance (OriginSystem/ExtractedAt), internal/space/
// data_resolve.go's splitDataContractReference already parses AND checks the
// `#<digest>` suffix pack's own pinnedRef return produces (its own doc
// comment records the fix explicitly), and dataCore.verify already computes
// ContractSupersededBy. This family therefore does not carry a hermetic-
// harness compensation layer (this file's own analogue of
// dataLoopFixProvenance) — if any of those three findings were still live,
// this row would fail loudly at the step it broke, which is what a live
// family that CONFIRMS something, rather than assumes it, is supposed to do.
func runDataLoopScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{dataLoopFailSupersedePass(ctx, h)}

	// Same parking discipline every other family in this package documents
	// (runHappyScenarios/runContractIntegrityScenarios/
	// runSubmittedFamilyScenarios' own tails): leave both checkouts on a
	// clean main so whichever family runs next does not inherit a dirty
	// working tree that looks like its own bug. Best-effort; a failure here
	// does not change the already-recorded result above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// dataLoopScenario is this row's catalogue name (catalogue.go).
const dataLoopScenario = "data-loop-fail-supersede-pass"

// dataLoopResultFromErr renders err into this family's ONE Result,
// step-tagging Expected exactly as ac973ResultFromErr/subfamResultFromErr do
// — this row is a long multi-step chain, and a bare "the row failed" would
// not say which of its many writes broke.
func dataLoopResultFromErr(step string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	res := Result{
		Scenario: dataLoopScenario, System: SystemA, Surface: SurfaceCLI,
		Verdict: verdict, Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: err.Error(),
	}
	if errors.Is(err, ErrNoPRForBranch) || errors.Is(err, ErrNoBranchMatch) || errors.Is(err, ErrAmbiguousBranchMatch) {
		res.Detail = err.Error()
	}
	return res
}

// dataLoopFail builds a VerdictFail row for an assertion this family observed
// directly (the CLI ran, but what it did was wrong) — same step-tagging as
// dataLoopResultFromErr.
func dataLoopFail(step, observed, expected, detail string) Result {
	return Result{
		Scenario: dataLoopScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// dataLoopPack runs `a2a data pack --json` from c and decodes its result. It
// never writes to the space (pack is judge/stage-only), so — unlike deliver
// and verify below — a non-nil exec error IS always a real failure and there
// is no branch/PR to resolve.
func dataLoopPack(ctx context.Context, c *checkout, contractRef, from, fulfills, supersedes string) (cli.DataResult, error) {
	args := []string{
		"data", "pack",
		"--contract", contractRef, "--from", from,
		"--profile", "synthetic", "--format", "json", "--expires", "24h",
		"--fulfills", fulfills, "--json",
	}
	if supersedes != "" {
		args = append(args, "--supersedes", supersedes)
	}
	stdout, stderr, err := c.Run(ctx, args...)
	if err != nil {
		return cli.DataResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr))
	}
	var result cli.DataResult
	if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr != nil {
		return cli.DataResult{}, fmt.Errorf("decode data pack JSON: %w: %s", jsonErr, stdout)
	}
	if result.Manifest == nil {
		return cli.DataResult{}, fmt.Errorf("data pack result carries no manifest: %s", stdout)
	}
	return result, nil
}

// dataLoopStagingRoot reads the staging root `a2a data pack` itself reports
// (cli.DataResult.StagingRoot, the `staging_root` field of `pack --json`) —
// the exact argument `a2a data deliver` takes next.
//
// It is READ, never rebuilt from the minted package id. This file used to
// derive it from dataCore.writeStagedPackage's own path convention, which
// worked but coupled the live tier to an internal layout AND contradicted
// what reference/data-exchange.md tells every agent to do ("use the path it
// printed; do not reconstruct it from the minted id"). Consuming the field
// instead makes this row the only place that proves it end to end against
// real GitHub — nothing else does.
func dataLoopStagingRoot(result cli.DataResult, attempt string) (string, error) {
	if result.StagingRoot == "" {
		return "", fmt.Errorf("data pack (%s) reported no staging_root; `a2a data deliver` has no argument to take", attempt)
	}
	return result.StagingRoot, nil
}

// dataLoopCorruptRecordCount declares a record_count that disagrees with
// entryPath's actual byte content, by delta — without touching the entry's
// own digest (record_count is manifest metadata; aggregate_digest covers
// path->digest pairs only), so `a2a data deliver`'s digest/shape checks
// still accept the package, and only `a2a data verify`'s consumer-side
// recount (internal/datapackage/verify.go) can catch the disagreement —
// exactly spec 05a §T2.1's "observed" row: "a disagreement between the two
// is recorded as a finding in checks[]". This family's own copy of
// internal/e2e/data_loop_test.go's dataLoopCorruptRecordCount — that
// package is off this brief's allowlist, so the shape is restated here
// rather than imported.
// dataLoopManifestMaxBytes bounds the staged manifest read below, matching
// the repo-wide bounded-read idiom (contractSidecarMaxBytes, internal/
// template/contract_scaffold.go's own doc comment: "each package owns its
// own cap constant... none shared across package boundaries").
const dataLoopManifestMaxBytes = 1 << 20 // 1 MiB

func dataLoopCorruptRecordCount(stagingRoot, entryPath string, delta int64) error {
	manifestPath := filepath.Join(stagingRoot, "manifest.json")
	raw, err := readBoundedRuntimeFile(manifestPath, dataLoopManifestMaxBytes)
	if err != nil {
		return fmt.Errorf("read staged manifest %s: %w", manifestPath, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("decode staged manifest %s: %w", manifestPath, err)
	}
	entries, ok := doc["entries"].([]any)
	if !ok {
		return fmt.Errorf("staged manifest %s has no entries[] array", manifestPath)
	}
	found := false
	for _, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok || entry["path"] != entryPath {
			continue
		}
		current, ok := entry["record_count"].(float64)
		if !ok {
			return fmt.Errorf("entry %q has no numeric record_count to corrupt", entryPath)
		}
		entry["record_count"] = current + float64(delta)
		found = true
	}
	if !found {
		return fmt.Errorf("staged manifest %s has no entry %q to corrupt", manifestPath, entryPath)
	}
	patched, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("re-encode patched manifest %s: %w", manifestPath, err)
	}
	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		return fmt.Errorf("write patched manifest %s: %w", manifestPath, err)
	}
	return nil
}

// dataLoopWritePayload creates dir and writes one JSON payload.json entry
// into it — the "flat file at the source root" shape `a2a data pack` accepts
// without a schema-stem subdirectory when the pinned contract declares
// exactly one schema (dataPackDeclaration, cmd/a2a/data_wiring.go), which is
// what this row's own D-D-scaffolded contract always does.
func dataLoopWritePayload(dir, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s/payload.json: %w", dir, err)
	}
	return nil
}

// dataLoopOperationPull runs a data verb that WRITES (deliver, verify
// --record) with --json appended, and resolves the pull request its OWN
// funnel write opened — from the command's own JSON output, never a
// guessed branch. This is contractPublishPull's (scenarios_contract_
// integrity_live.go) exact recipe, restated here because `a2a data deliver`
// and `a2a data verify --record` mint their branch the SAME way `contract
// publish` does and that file's own doc comment warns about: BOTH set
// SubmitRequest.OperationKey (dataDeliverOperationKey(packageID) /
// dataVerifyOperationKey(reportID), internal/space/data_delivery.go), so the
// funnel branches on that opaque key (`branchID = req.OperationKey`,
// internal/space/funnel.go) rather than on space.BranchName(system, verb,
// id) — a guessed name would miss it exactly the way a guessed
// contract-publish branch already cost this project a whole candidate.
//
// `a2a data verify --record` additionally EXITS 1 whenever the report's own
// verdict is a fail (cmd_data.go's own runVerify: "if
// result.Report.Result() != datapackage.ResultPass { return 1 }") even
// though the funnel write itself SUCCEEDED — a fail verdict is a legitimate,
// EXPECTED outcome for this row's own first attempt, not a command error.
// So a non-nil exec error is not on its own proof nothing was written: the
// JSON is decoded first regardless of the exit code, and only a result that
// cannot be decoded, or decodes with no Write at all, is treated as the
// command having genuinely failed (in which case the exec error, carrying
// stderr, is what the caller sees).
func dataLoopOperationPull(ctx context.Context, h *harness, c *checkout, args []string) (cli.DataResult, PullState, error) {
	stdout, stderr, runErr := c.Run(ctx, append(args, "--json")...)
	var result cli.DataResult
	if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr != nil {
		if runErr != nil {
			return result, PullState{}, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(stderr))
		}
		return result, PullState{}, fmt.Errorf("decode data verb JSON: %w: %s", jsonErr, stdout)
	}
	if result.Write == nil {
		if runErr != nil {
			return result, PullState{}, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(stderr))
		}
		return result, PullState{}, fmt.Errorf("data verb result carries no write result: %s", stdout)
	}
	if result.Write.PRNumber > 0 {
		pull, err := h.Prov.Pull(ctx, h.Org, h.Repo, result.Write.PRNumber)
		return result, pull, err
	}
	if result.Write.Branch == "" {
		return result, PullState{}, fmt.Errorf("data verb reported neither a pull request nor a branch: %s", stdout)
	}
	pull, err := h.pullForBranch(ctx, result.Write.Branch)
	return result, pull, err
}

// dataLoopContractSlug is the base of the standing id this row's contract is
// minted from; liveRunSlug scopes it to the current destructive generation.
const dataLoopContractSlug = "dl-data-exchange"

// dataLoopFailSupersedePass is this family's MANDATORY row: the whole loop,
// on one thread, ending with the ORIGINAL work_request reaching a closed
// state — not merely a second package reaching verify-pass. A loop that
// only works when the first attempt is correct is not the feature under
// test; this row's own first attempt is deliberately built to fail (a
// record_count disagreement the digest cannot see), so the supersede leg is
// exercised for real, live, exactly as internal/e2e/data_loop_test.go's own
// decisive hermetic scenario is.
func dataLoopFailSupersedePass(ctx context.Context, h *harness) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh contract-data-loop run")
	}

	// --- 1. A publishes a JSON-Schema contract (schema + valid fixture,
	// D-D scaffolded) that the loop's own packages will pin. ---
	// The slug is REQUIRED and run-scoped, exactly as every other standing
	// draft in this tier is. `a2a new` refuses a standing type without one,
	// so the version of this line that omitted it could never have reached
	// GitHub; and an unscoped slug would collide with the previous
	// destructive generation's merged pull requests (see liveRunSlug).
	sub, err := h.DraftAndSubmit(ctx, a, "contract", "--slug", liveRunSlug(dataLoopContractSlug, h.PRFloor))
	if err != nil {
		return dataLoopResultFromErr("draft-submit-contract", err, "draft+submit a JSON-Schema contract (schema+valid fixture scaffolded) opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return dataLoopResultFromErr("publish-land-sync", err, "the published contract lands on main and reaches A's mirror")
	}
	contractPaths, err := contractPathsForID(sub.ID)
	if err != nil {
		return dataLoopResultFromErr("resolve-contract-staging", err, "the current contract id resolves to its contained staging root")
	}

	// --- 1b. Register v1.0.0 as a REAL published version — exactly ac973's
	// own baseline registration step, and for the same reason: a raw `a2a
	// submit` writes no `version` field, so nothing is resolvable through
	// space.ResolveDataContractSchemas until a real `contract publish`
	// registers one. ---
	_, baselinePR, err := contractPublishPull(ctx, h, a, contractPublishVersionArgs(sub.ID, "1.0.0", contractPaths.StagingRoot))
	if err != nil {
		return dataLoopResultFromErr("register-baseline-v1", err, "`a2a contract publish --version 1.0.0` registers the first REAL published version")
	}
	if err := happyLandAndSync(ctx, h, a, baselinePR.Number); err != nil {
		return dataLoopResultFromErr("register-baseline-land-sync", err, "the v1.0.0 registration lands on main and reaches A's mirror")
	}
	contractRef := sub.ID + "@1.0.0"

	// --- 2. A opens a work_request addressed to B — the ordinary public
	// authoring path, identical to subfamExchangeLifecycle's own draft step. ---
	wr, err := h.DraftAndSubmit(ctx, a, "work_request")
	if err != nil {
		return dataLoopResultFromErr("draft-submit-work-request", err, "draft+submit a work_request addressed to B opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, wr.PRNumber); err != nil {
		return dataLoopResultFromErr("work-request-land-sync", err, "the work_request lands on main and reaches A's mirror")
	}

	// --- 3. B sees it and acknowledges: submitted -> acknowledged. ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("producer-sync-before-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed contract and work_request")
	}
	seen, inboxOut, err := subfamInboxContains(ctx, b, wr.ID)
	if err != nil {
		return dataLoopResultFromErr("producer-sees-work-request", err, "B's a2a inbox succeeds after sync")
	}
	if !seen {
		return dataLoopFail("producer-sees-work-request", inboxOut,
			fmt.Sprintf("B's `a2a inbox --json` lists %s after sync", wr.ID), inboxOut)
	}
	if _, stderr, err := b.Run(ctx, "ack", wr.ID); err != nil {
		return dataLoopResultFromErr("producer-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack succeeds and opens its own PR")
	}
	ackWRPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", wr.ID))
	if err != nil {
		return dataLoopResultFromErr("producer-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackWRPR.Number); err != nil {
		return dataLoopResultFromErr("ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- 4. Attempt 1: B packs a corrupted attempt and delivers it. ---
	from1 := filepath.Join(b.Dir, "dloop-attempt1")
	if err := dataLoopWritePayload(from1, `{"example":"attempt-1"}`+"\n"); err != nil {
		return dataLoopResultFromErr("stage-attempt-1", err, "attempt 1's source payload can be written to disk")
	}
	pack1, err := dataLoopPack(ctx, b, contractRef, from1, wr.ID, "")
	if err != nil {
		return dataLoopResultFromErr("pack-attempt-1", err, "`a2a data pack` stages attempt 1 against the pinned contract")
	}
	staging1, err := dataLoopStagingRoot(pack1, "attempt 1")
	if err != nil {
		return dataLoopResultFromErr("pack-attempt-1", err, "`a2a data pack` reports the staging root `a2a data deliver` takes next")
	}
	if err := dataLoopCorruptRecordCount(staging1, "payload.json", 1); err != nil {
		return dataLoopResultFromErr("corrupt-attempt-1", err, "attempt 1's staged manifest declares a record_count the digest-verified payload does not have")
	}
	deliver1, deliver1Pull, err := dataLoopOperationPull(ctx, h, b, []string{"data", "deliver", staging1, "--fulfills", wr.ID})
	if err != nil {
		return dataLoopResultFromErr("deliver-attempt-1", err, "`a2a data deliver` accepts the corrupted attempt (digest still verifies; only the record count disagrees) and opens its own PR")
	}
	if deliver1.HandoffID == "" {
		return dataLoopFail("deliver-attempt-1", fmt.Sprintf("%+v", deliver1), "deliver's own JSON result carries a handoff_id", "")
	}
	if err := happyLandAndSync(ctx, h, b, deliver1Pull.Number); err != nil {
		return dataLoopResultFromErr("deliver-attempt-1-land-sync", err, "attempt 1's delivery lands on main and reaches B's mirror")
	}
	handoff1 := deliver1.HandoffID

	// --- 5. A acknowledges the handoff, then judges and RECORDS attempt 1's
	// failing verdict: acknowledged -> rejected. ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("consumer-sync-before-ack", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches attempt 1's delivery")
	}
	if _, stderr, err := a.Run(ctx, "ack", handoff1); err != nil {
		return dataLoopResultFromErr("consumer-ack-handoff-1", fmt.Errorf("%w: %s", err, stderr), "A's a2a ack on the handoff succeeds and opens its own PR")
	}
	ackHandoff1PR, err := h.pullForBranch(ctx, space.BranchName(a.System, "ack", handoff1))
	if err != nil {
		return dataLoopResultFromErr("consumer-ack-handoff-1", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, ackHandoff1PR.Number); err != nil {
		return dataLoopResultFromErr("consumer-ack-handoff-1-land-sync", err, "the ack lands on main and reaches A's mirror")
	}
	verify1, verify1Pull, err := dataLoopOperationPull(ctx, h, a, []string{"data", "verify", pack1.Manifest.ID, "--record"})
	if err != nil {
		return dataLoopResultFromErr("verify-record-attempt-1", err, "`a2a data verify --record` judges attempt 1, records a FAILING verdict (record_count disagreement), and opens its own PR")
	}
	if verify1.Report == nil || verify1.Report.Result() != datapackage.ResultFail {
		observed := "<nil report>"
		if verify1.Report != nil {
			observed = string(verify1.Report.Result())
		}
		return dataLoopFail("verify-record-attempt-1", observed, "attempt 1's verdict is `fail` (spec 05a §T2.1's own record_count-disagreement finding)", "")
	}
	if err := happyLandAndSync(ctx, h, a, verify1Pull.Number); err != nil {
		return dataLoopResultFromErr("verify-record-attempt-1-land-sync", err, "attempt 1's verify-fail lands on main and reaches A's mirror")
	}
	if state, err := subfamShowState(ctx, a, handoff1); err != nil {
		return dataLoopResultFromErr("attempt-1-terminal-state-read", err, "a2a show reads attempt 1's rejected handoff back")
	} else if state != "rejected" {
		return dataLoopFail("attempt-1-terminal-state-read", state, "attempt 1's handoff reaches `rejected` after its recorded verify-fail", state)
	}

	// --- 6. Attempt 2: B packs a CORRECT attempt that SUPERSEDES attempt 1
	// and delivers it. This is the row's own decisive leg: a loop that only
	// works when the first attempt is correct is not the feature. ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("producer-sync-before-attempt-2", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches attempt 1's recorded verify-fail")
	}
	from2 := filepath.Join(b.Dir, "dloop-attempt2")
	if err := dataLoopWritePayload(from2, `{"example":"attempt-2"}`+"\n"); err != nil {
		return dataLoopResultFromErr("stage-attempt-2", err, "attempt 2's source payload can be written to disk")
	}
	pack2, err := dataLoopPack(ctx, b, contractRef, from2, wr.ID, pack1.Manifest.ID)
	if err != nil {
		return dataLoopResultFromErr("pack-attempt-2", err, "`a2a data pack --supersedes` stages attempt 2 against the pinned contract")
	}
	if pack2.Manifest.Attempt != 2 {
		return dataLoopFail("pack-attempt-2", fmt.Sprintf("attempt=%d", pack2.Manifest.Attempt), "attempt 2's manifest carries attempt=2", "")
	}
	if pack2.Manifest.Supersedes == "" {
		return dataLoopFail("pack-attempt-2", "supersedes=\"\"", "attempt 2's manifest carries a non-empty supersedes reference to attempt 1", "")
	}
	staging2, err := dataLoopStagingRoot(pack2, "attempt 2")
	if err != nil {
		return dataLoopResultFromErr("pack-attempt-2", err, "`a2a data pack` reports the staging root `a2a data deliver` takes next")
	}
	deliver2, deliver2Pull, err := dataLoopOperationPull(ctx, h, b, []string{"data", "deliver", staging2, "--fulfills", wr.ID})
	if err != nil {
		return dataLoopResultFromErr("deliver-attempt-2", err, "`a2a data deliver` accepts attempt 2 and opens its own PR — a DIFFERENT branch from attempt 1's (the operation key is minted from the package id, which differs per attempt)")
	}
	if deliver2.HandoffID == "" {
		return dataLoopFail("deliver-attempt-2", fmt.Sprintf("%+v", deliver2), "deliver's own JSON result carries a handoff_id", "")
	}
	// The property worth asserting is not "the two minted handoff ids
	// differ" (artifact.MintExchangeIDAt draws its own entropy per call and
	// can never collide, so that comparison could never fail — the brief's
	// own named defect class, "a condition true regardless of product
	// behaviour"). What can genuinely fail is the operation-key property
	// dataDeliverOperationKey exists for: attempt 2's delivery must open a
	// DIFFERENT branch/PR than attempt 1's, because the key is minted from
	// the PACKAGE id, which differs per attempt. Were it ever derived from
	// something stable across attempts instead (the work_request id, the
	// handoff id), the funnel's own idempotent-retry short-circuit would
	// treat attempt 2 as a RETRY of attempt 1 and repair attempt 1's PR
	// rather than opening a new one — silently landing attempt 2 on top of
	// attempt 1's already-rejected delivery.
	if deliver2Pull.Number == deliver1Pull.Number {
		return dataLoopFail("deliver-attempt-2", fmt.Sprintf("PR #%d (same as attempt 1's)", deliver2Pull.Number),
			"attempt 2's delivery opens a DIFFERENT pull request than attempt 1's (the operation key is minted from the package id, which differs per attempt)", "")
	}
	if err := happyLandAndSync(ctx, h, b, deliver2Pull.Number); err != nil {
		return dataLoopResultFromErr("deliver-attempt-2-land-sync", err, "attempt 2's delivery lands on main and reaches B's mirror")
	}
	handoff2 := deliver2.HandoffID

	// --- 7. A acknowledges the second handoff, then judges and RECORDS
	// attempt 2's passing verdict: acknowledged -> accepted. ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("consumer-sync-before-ack-2", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches attempt 2's delivery")
	}
	if _, stderr, err := a.Run(ctx, "ack", handoff2); err != nil {
		return dataLoopResultFromErr("consumer-ack-handoff-2", fmt.Errorf("%w: %s", err, stderr), "A's a2a ack on the second handoff succeeds and opens its own PR")
	}
	ackHandoff2PR, err := h.pullForBranch(ctx, space.BranchName(a.System, "ack", handoff2))
	if err != nil {
		return dataLoopResultFromErr("consumer-ack-handoff-2", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, ackHandoff2PR.Number); err != nil {
		return dataLoopResultFromErr("consumer-ack-handoff-2-land-sync", err, "the ack lands on main and reaches A's mirror")
	}
	verify2, verify2Pull, err := dataLoopOperationPull(ctx, h, a, []string{"data", "verify", pack2.Manifest.ID, "--record"})
	if err != nil {
		return dataLoopResultFromErr("verify-record-attempt-2", err, "`a2a data verify --record` judges attempt 2, records a PASSING verdict, and opens its own PR")
	}
	if verify2.Report == nil || verify2.Report.Result() != datapackage.ResultPass {
		observed := "<nil report>"
		if verify2.Report != nil {
			observed = string(verify2.Report.Result())
		}
		return dataLoopFail("verify-record-attempt-2", observed, "attempt 2's verdict is `pass`", "")
	}
	if err := happyLandAndSync(ctx, h, a, verify2Pull.Number); err != nil {
		return dataLoopResultFromErr("verify-record-attempt-2-land-sync", err, "attempt 2's verify-pass lands on main and reaches A's mirror")
	}
	if state, err := subfamShowState(ctx, a, handoff2); err != nil {
		return dataLoopResultFromErr("attempt-2-terminal-state-read", err, "a2a show reads attempt 2's accepted handoff back")
	} else if state != "accepted" {
		return dataLoopFail("attempt-2-terminal-state-read", state, "attempt 2's handoff reaches the terminal `accepted` state", state)
	}

	// --- 8. B discharges its own obligation on the ORIGINAL work_request
	// (respond, not a fresh submit — same D-026 draft+submit collapse
	// subfamExchangeLifecycle's own respond step exercises); A closes it. ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("producer-sync-before-respond", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches attempt 2's recorded verify-pass")
	}
	respondKey, respondBranch := respondOperation(b.System, wr.ID, "delivered")
	if _, stderr, err := b.Run(ctx, respondCommandArgs(wr.ID, "delivered")...); err != nil {
		return dataLoopResultFromErr("producer-respond", fmt.Errorf("%w: %s", err, stderr), "B's a2a respond succeeds and opens its own PR")
	}
	respondPR, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		return dataLoopResultFromErr("producer-respond", err, "respond's semantic-operation branch has a PR")
	}
	if _, err := operationArtifactID(respondPR.Body, respondKey, wr.ID, "XS-"); err != nil {
		return dataLoopResultFromErr("producer-respond", err, "respond's PR metadata names the new XS response artifact")
	}
	if err := subfamAwaitGreenAndLand(ctx, h, b, respondPR.Number); err != nil {
		return dataLoopResultFromErr("respond-land-sync", err, "the response lands on main and reaches B's mirror (required check concludes success)")
	}

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return dataLoopResultFromErr("owner-sync-before-close", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's response before closing")
	}
	// rules-that-reach-2026-08 P1: `close` carries verdicts[] too, and REF-023
	// judges it against the parent's declared criteria — a work_request
	// declares one (draftfields.go), so a close naming none is now refused at
	// submit. A question declares none, which is why the question-family close
	// elsewhere needs no verdict.
	closeOut, stderr, err := a.Run(ctx, "close", wr.ID, "--verdict", "0:met:"+a.System)
	if err != nil {
		return dataLoopResultFromErr("owner-close", fmt.Errorf("%w: %s", err, stderr), "A's a2a close succeeds and opens its own PR")
	}
	// Supplying --verdict moves the branch off BranchName(system, verb, id)
	// onto operation.Verify's content-derived key, so the PR number is read
	// from the verb's own output instead.
	closePRNumber, err := prNumberFromVerbOutput(closeOut)
	if err != nil {
		return dataLoopResultFromErr("owner-close", err, "close's own PR number is readable from its success line")
	}
	closePR := branchPull{Number: closePRNumber}
	if err := happyLandAndSync(ctx, h, a, closePR.Number); err != nil {
		return dataLoopResultFromErr("close-land-sync", err, "the close lands on main and reaches A's mirror")
	}

	// The decisive assertion: the ORIGINAL work_request reached a closed
	// state — not merely that the second package got verify-pass.
	finalState, err := subfamShowState(ctx, a, wr.ID)
	if err != nil {
		return dataLoopResultFromErr("terminal-state-read", err, "a2a show reads the closed work_request back")
	}
	if finalState != "closed" {
		return dataLoopFail("terminal-state-read", finalState, "the work_request reaches the terminal `closed` state", finalState)
	}

	// This family is deliberately OUTSIDE the LE-OC-01..08 evidence manifest
	// (plan decision D-7): it proves itself through the retained report.txt
	// and nothing else. Report.Render() prints Detail only on a FAILING row
	// and PassEvidence only on a passing one, so a pass carrying Detail alone
	// renders as the bare line `data-loop-fail-supersede-pass A cli pass` —
	// byte-identical to a row that silently did nothing, which is exactly the
	// evidence-free pass D-7 rests against. Both fields carry the narrative,
	// matching thread-chain's own decisive row.
	narrative := fmt.Sprintf(
		"%s: contract %s (PR #%d) -> work_request %s (PR #%d, ack PR #%d) -> attempt 1 %s FAILS (deliver PR #%d, ack PR #%d, verify-fail PR #%d, handoff %s rejected) -> attempt 2 %s supersedes and PASSES (deliver PR #%d, ack PR #%d, verify-pass PR #%d, handoff %s accepted) -> respond PR #%d -> close PR #%d",
		wr.ID, contractRef, baselinePR.Number, wr.ID, wr.PRNumber, ackWRPR.Number,
		pack1.Manifest.ID, deliver1Pull.Number, ackHandoff1PR.Number, verify1Pull.Number, handoff1,
		pack2.Manifest.ID, deliver2Pull.Number, ackHandoff2PR.Number, verify2Pull.Number, handoff2,
		respondPR.Number, closePR.Number,
	)
	return Result{
		Scenario: dataLoopScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       narrative,
		PassEvidence: narrative,
	}
}
