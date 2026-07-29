//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// P4's live row (agent-ops-2026-07, spec 04): the ROLLING WINDOW — several
// versions of one contract holding different states at the same time, which
// is the steady state of any maintained interface and was structurally
// inexpressible before the per-version lifecycle shipped.
//
// It lives in the contract-integrity family rather than a new one on
// purpose: it is the same producer<->consumer story one release further on,
// and a new family would mean a new entry in driveFamilies' dispatch table,
// in CanonicalFamilies(), and in the equality check between them — three
// places to get wrong for no gain over adding a second Result to the family
// that already owns this narrative.
//
// What this row proves that NOTHING hermetic can, which is the whole reason
// it exists (RN-01300-7 says so in the shipped notes):
//
//   - the write funnel's own DEDUPLICATION of a deprecation announcement.
//     Edge 3 exists because that dedup silently drops a late adopter — and
//     the dedup itself is never exercised by a unit test, because unit tests
//     never open a PR.
//   - CI's behaviour on a maintenance-line publish whose baseline is not the
//     globally-highest version. The merge gate runs `a2a validate --ci` on a
//     tree containing three live version lines; no hermetic fixture has ever
//     had one.
//   - that `a2a contract retire --version X` succeeds against real
//     protection while OTHER versions are published. This is the sentence
//     the whole phase exists for, and until this row runs it has only ever
//     been asserted against a hand-built mirror.
//
// One thing worth knowing before reading the steps: this row would have
// FAILED on 0.12.0. POL-011 — that release's fail-closed guard — refused
// exactly the retire step 7 below performs. The existing
// contract-integrity-registered-consumer row has the same step and would
// have failed too, and nobody saw it, because the live tier did not run
// between the guard shipping and the real fix replacing it. That is not a
// footnote; it is the argument for this row, made concrete.
const rwScenario = "contract-rolling-window"

// rwSlug is this row's lowercase-kebab standing-slug base, kept distinct so
// one run never mints two contracts of the same id. liveRunSlug adds the
// destructive-space generation before authoring.
const rwSlug = "rw-rolling-window"

func rwResultFromErr(step string, err error, expected string) Result {
	return Result{
		Scenario: rwScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Detail: fmt.Sprintf("step %s: %v", step, err), Expected: expected,
	}
}

func rwFail(step, observed, expected, detail string) Result {
	return Result{
		Scenario: rwScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Detail:   fmt.Sprintf("step %s: %s%s", step, observed, rwDetailSuffix(detail)),
		Expected: expected,
	}
}

func rwDetailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " — " + detail
}

// rwSchemaDir is this row's ONE place naming the contract's space-relative
// schema directory, so a staging write and an on-main read can never drift
// to different paths (ac973SchemaDir's own reason, restated: a read of the
// wrong path looks exactly like a publish that carried nothing).
func rwSchemaDir(system string) (string, error) {
	layout, err := space.NewLayout(system)
	if err != nil {
		return "", err
	}
	return layout.ProvidesSchemaDir(rwSlug), nil
}

// rwWriteSchema overwrites the contract's scaffolded schema in the AUTHOR's
// STAGING tree — never the mirror. The mirror is reset unconditionally on
// every `a2a contract` invocation (live run 3, 2026-07-25: an edit written
// there was gone before the compat check read it, so the check compared a
// version against itself). Staging is the one surface that reset never
// touches.
func rwWriteSchema(a *checkout, body string) error {
	schemaDir, err := rwSchemaDir(a.System)
	if err != nil {
		return err
	}
	schemaPath := filepath.Join(a.Dir, ".a2a", "staging", filepath.FromSlash(schemaDir), rwSlug+".schema.json")
	if _, statErr := os.Stat(schemaPath); statErr != nil {
		// Loudly, for the reason ac973BreakSchema documents: os.WriteFile
		// would happily create a file the publish overlay still picks up,
		// leaving the row green while no longer testing what it names.
		return fmt.Errorf("livee2e: expected `a2a contract new`'s staged schema at %s: %w", schemaPath, statErr)
	}
	return os.WriteFile(schemaPath, []byte(body), 0o644)
}

// rwSchemaNarrowedToInteger breaks the scaffolded fixture
// (`{"example": "replace-me"}`) by narrowing `example` from string to
// integer — a real breaking change against any line whose fixture is the
// scaffolded one, not a synthetic refusal.
const rwSchemaNarrowedToInteger = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "example": { "type": "integer" }
  },
  "additionalProperties": true
}
`

// rwSchemaWidened ADDS an optional property and keeps `example` a string —
// compatible with the 1.x line's own fixture, so a correctly-baselined
// maintenance publish passes the compat check rather than merely skipping
// it.
const rwSchemaWidened = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "example": { "type": "string" },
    "note": { "type": "string" }
  },
  "additionalProperties": true
}
`

// rwDraftContractExcludingPeer drafts a contract addressed to the author's
// OWN system, so the peer is genuinely not in the authoring-time `to:`.
// `to:` cannot simply be emptied (REF-006 requires minItems 1 or "all").
func rwDraftContractExcludingPeer(ctx context.Context, h *harness, c *checkout) (submitted, error) {
	id, _, err := c.Draft(ctx, "contract",
		"--slug", liveRunSlug(rwSlug, h.PRFloor),
		"--field", "to=["+c.System+"]",
	)
	if err != nil {
		return submitted{}, err
	}
	return h.submitDrafted(ctx, c, id)
}

// rwPublishAndLand runs one `a2a contract publish --version <v>` and lands
// its PR. Publish's branch is the plain contract-publish/<id> shape
// (delete_branch_on_merge makes the name safely reusable across versions).
func rwPublishAndLand(ctx context.Context, h *harness, a *checkout, id, version, step string) (int, error) {
	if _, stderr, err := a.Run(ctx, "contract", "publish", id, "--version", version); err != nil {
		return 0, fmt.Errorf("%s: %w: %s", step, err, stderr)
	}
	pr, err := h.pullForBranch(ctx, space.BranchName(a.System, "contract-publish", id))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", step, err)
	}
	if err := happyLandAndSync(ctx, h, a, pr.Number); err != nil {
		return 0, fmt.Errorf("%s: %w", step, err)
	}
	return pr.Number, nil
}

// rwRollingWindow is the row. Read the numbered steps as one narrative: a
// contract with three live version lines, a consumer who arrives in the
// middle of a sunset window, and a retirement that used to be impossible.
func rwRollingWindow(ctx context.Context, h *harness) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return rwResultFromErr("sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh rolling-window run")
	}

	// --- 1. A publishes 1.0.0 and 1.1.0 — one line, two versions. B is
	// deliberately NOT in the contract's authoring `to:`. ---
	sub, err := rwDraftContractExcludingPeer(ctx, h, a)
	if err != nil {
		return rwResultFromErr("draft-submit-contract", err, "draft+submit a JSON-Schema contract whose `to:` excludes the peer")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return rwResultFromErr("submit-land-sync", err, "the drafted contract lands on main and reaches A's mirror")
	}
	v100PR, err := rwPublishAndLand(ctx, h, a, sub.ID, "1.0.0", "publish-1.0.0")
	if err != nil {
		return rwResultFromErr("publish-1.0.0", err, "`a2a contract publish --version 1.0.0` registers the first real published version")
	}
	v110PR, err := rwPublishAndLand(ctx, h, a, sub.ID, "1.1.0", "publish-1.1.0")
	if err != nil {
		return rwResultFromErr("publish-1.1.0", err, "1.1.0 publishes on the same line")
	}

	// --- 2. A opens a SECOND line: 2.0.0, carrying a breaking schema. A
	// major is not compat-checked (D-B), which is what lets the 1.x line
	// keep a fixture 2.0 would reject — the asymmetry step 4 depends on. ---
	if err := rwWriteSchema(a, rwSchemaNarrowedToInteger); err != nil {
		return rwResultFromErr("stage-2.0.0-schema", err, "the contract's staged schema can be narrowed for the 2.x line")
	}
	v200PR, err := rwPublishAndLand(ctx, h, a, sub.ID, "2.0.0", "publish-2.0.0")
	if err != nil {
		return rwResultFromErr("publish-2.0.0", err, "2.0.0 opens a second line (a major bump is not compat-checked, D-B)")
	}

	// --- 3. A deprecates 1.1.0 while 2.0.0 is live. THIS is the shape the
	// old subject-scoped engine misreported: the contract is NOT deprecated
	// — 2.0.0 is published — and every read surface must say so. ---
	successor := sub.ID + "@2.0.0"
	deprecateKey, deprecateBranch := contractDeprecateOperation(
		a.System, sub.ID, "1.1.0", successor, "2020-01-01",
	)
	if _, stderr, err := a.Run(ctx, "contract", "deprecate", sub.ID,
		"--version", "1.1.0", "--successor", successor, "--sunset", "2020-01-01"); err != nil {
		return rwResultFromErr("deprecate-1.1.0", fmt.Errorf("%w: %s", err, stderr),
			"`a2a contract deprecate --version 1.1.0` succeeds while 2.0.0 is published")
	}
	deprecatePR, err := h.pullForBranch(ctx, deprecateBranch)
	if err != nil {
		return rwResultFromErr("deprecate-1.1.0", err, "contract deprecate's semantic-operation branch has a PR")
	}
	announcementID, err := operationArtifactID(deprecatePR.Body, deprecateKey, sub.ID, "XA-")
	if err != nil {
		return rwResultFromErr("deprecate-1.1.0", err, "the deprecate PR metadata names the linked deprecation announcement")
	}
	if err := happyLandAndSync(ctx, h, a, deprecatePR.Number); err != nil {
		return rwResultFromErr("deprecate-land-sync", err, "the deprecation lands on main and reaches A's mirror")
	}

	contractsOut, contractsErr, err := a.Run(ctx, "contracts", "--json")
	if err != nil {
		return rwResultFromErr("rolling-window-state", fmt.Errorf("%w: %s", err, contractsErr), "`a2a contracts --json` succeeds after the deprecation lands")
	}
	if !strings.Contains(contractsOut, `"state":"published"`) && !strings.Contains(contractsOut, `"state": "published"`) {
		return rwFail("rolling-window-state", contractsOut,
			"AC-7 (live): with 1.1.0 deprecated and 2.0.0 published, the contract's own state is `published` — the subject-level answer is a PROJECTION over the versions, not the last transition taken",
			"the old subject-scoped engine reported the whole contract deprecated here")
	}

	// --- 4. B adopts AFTER the deprecation was announced, and still sees
	// it. This is Edge 3 / AC-10, and it is the one step no hermetic test
	// can stand in for: `to:` was computed and frozen at step 3, when B was
	// not registered, and re-running `deprecate` would be DEDUPLICATED by
	// the write funnel rather than re-addressed. ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return rwResultFromErr("late-adopter-sync", fmt.Errorf("%w: %s", err, stderr), "B syncs before adopting")
	}
	if _, stderr, err := b.Run(ctx, "contract", "adopt", sub.ID, "--major", "1"); err != nil {
		return rwResultFromErr("late-adopt", fmt.Errorf("%w: %s", err, stderr), "B adopts on major 1 AFTER the deprecation was announced")
	}
	adoptPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "contract-adopt", sub.ID))
	if err != nil {
		return rwResultFromErr("late-adopt", err, "contract adopt's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, adoptPR.Number); err != nil {
		return rwResultFromErr("late-adopt-land-sync", err, "B's adoption lands on main and reaches B's mirror")
	}
	inboxOut, inboxStderr, err := b.Run(ctx, "inbox", "--actionable", "--json")
	if err != nil {
		return rwResultFromErr("late-adopter-inbox", fmt.Errorf("%w: %s", err, inboxStderr), "B's inbox --actionable succeeds after adopting")
	}
	if !strings.Contains(inboxOut, announcementID) {
		return rwFail("late-adopter-inbox", inboxOut,
			fmt.Sprintf("AC-10 (live): B adopted AFTER %s was published and was never in its frozen `to:`, and still sees it", announcementID),
			"the announcement's to: was computed before B registered, and the funnel would dedup a re-run deprecate")
	}

	// --- 5. A publishes a maintenance 1.2.0 while 2.0.0 is live. Edge 2 /
	// AC-8: the baseline is 1.1.0, the highest published version BELOW
	// 1.2.0 — NOT 2.0.0, the globally highest. The two answers are made
	// observably different: this schema is compatible with the 1.x
	// fixture, so a correct baseline lets the compat check RUN and pass;
	// under the old globally-highest rule the jump 2.0.0 -> 1.2.0 reads as
	// a major and the check is skipped entirely. ---
	if err := rwWriteSchema(a, rwSchemaWidened); err != nil {
		return rwResultFromErr("stage-1.2.0-schema", err, "the contract's staged schema can be widened for the maintenance line")
	}
	v120PR, err := rwPublishAndLand(ctx, h, a, sub.ID, "1.2.0", "publish-1.2.0-maintenance")
	if err != nil {
		return rwResultFromErr("publish-1.2.0-maintenance", err,
			"AC-8 (live): `publish --version 1.2.0` succeeds while 1.1.0 is deprecated and 2.0.0 is published — per-version legality allows it, and the compat check runs against 1.1.0")
	}

	// --- 6. B acks the deprecation, so the retire precondition is met for
	// the line B is registered on. ---
	if _, stderr, err := b.Run(ctx, "ack", announcementID); err != nil {
		return rwResultFromErr("consumer-ack", fmt.Errorf("%w: %s", err, stderr), "B's ack on the deprecation announcement succeeds")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", announcementID))
	if err != nil {
		return rwResultFromErr("consumer-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return rwResultFromErr("consumer-ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- 7. A retires 1.1.0 while 1.2.0 and 2.0.0 are published. THE
	// sentence this phase exists for. On 0.12.0 this was refused by
	// POL-011; before that guard it committed and left the contract
	// permanently inoperable. ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return rwResultFromErr("producer-sync-before-retire", fmt.Errorf("%w: %s", err, stderr), "A syncs B's ack before retiring")
	}
	if _, stderr, err := a.Run(ctx, "contract", "retire", sub.ID, "--version", "1.1.0"); err != nil {
		return rwResultFromErr("retire-1.1.0-while-others-published", fmt.Errorf("%w: %s", err, stderr),
			"AC-7 (live): `contract retire --version 1.1.0` SUCCEEDS while 1.2.0 and 2.0.0 are published — the act POL-011 refused and the pre-guard engine used to brick the contract on")
	}
	retirePR, err := h.pullForBranch(ctx, space.BranchName(a.System, "contract-retire", sub.ID))
	if err != nil {
		return rwResultFromErr("retire-1.1.0-while-others-published", err, "contract retire's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, retirePR.Number); err != nil {
		return rwResultFromErr("retire-land-sync", err, "the retirement lands on main and reaches A's mirror")
	}

	// The contract must still be OPERABLE afterwards — the failure mode
	// this whole phase exists to remove is a retire that succeeds and
	// leaves nothing else possible. A publish on the live line is the
	// cheapest proof that the subject did not fall into a terminal state.
	if _, stderr, err := a.Run(ctx, "contract", "publish", sub.ID, "--version", "2.1.0"); err != nil {
		return rwResultFromErr("contract-still-operable-after-retire", fmt.Errorf("%w: %s", err, stderr),
			"after retiring 1.1.0 the contract is still operable: publishing 2.1.0 on the live line succeeds, rather than every later operation being refused forever")
	}
	stillOperablePR, err := h.pullForBranch(ctx, space.BranchName(a.System, "contract-publish", sub.ID))
	if err != nil {
		return rwResultFromErr("contract-still-operable-after-retire", err, "the post-retire publish opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, stillOperablePR.Number); err != nil {
		return rwResultFromErr("contract-still-operable-land-sync", err, "2.1.0 lands on main")
	}

	return Result{
		Scenario: rwScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf(
			"%s: 1.0.0 (PR #%d) -> 1.1.0 (PR #%d) -> 2.0.0 (PR #%d) -> deprecate 1.1.0 (PR #%d, %s) while 2.0.0 live, contract still reads published -> B adopts LATE and still sees the deprecation -> maintenance 1.2.0 baselined on 1.1.0 (PR #%d) -> B acks (PR #%d) -> retire 1.1.0 while 1.2.0+2.0.0 published (PR #%d) -> contract still operable, 2.1.0 lands (PR #%d)",
			sub.ID, v100PR, v110PR, v200PR, deprecatePR.Number, announcementID, v120PR, ackPR.Number, retirePR.Number, stillOperablePR.Number),
	}
}
