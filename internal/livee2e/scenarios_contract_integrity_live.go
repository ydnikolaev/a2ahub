//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// runContractIntegrityScenarios drives spec 37's AC-973.1 row: the full
// producer<->consumer contract-integrity story against real GitHub, using
// BOTH systems. It is this family's ONE exported-shape entry, matching
// runHappyScenarios' own convention (plan §"Wave-3 design notes": "the
// fan-out seam is four functions, not a registry"). Every helper below is
// prefixed ac973* — this phase's own scenario-family namespace, mirroring
// scenarios_happy_live.go's happy* convention, so identifiers cannot
// collide across the package's scenario families.
func runContractIntegrityScenarios(ctx context.Context, h *harness) []Result {
	// Two rows, one narrative apart: ac973 is the producer<->consumer
	// integrity story, rwRollingWindow is that story one release on, with
	// several version lines live at once (P4, agent-ops-2026-07). The
	// rolling-window row runs SECOND — it is the more expensive of the two
	// and depends on nothing ac973 leaves behind, so a failure in the
	// cheaper row is reported before the costlier one spends its PRs.
	results := []Result{ac973ContractIntegrity(ctx, h), rwRollingWindow(ctx, h)}

	// Same parking discipline runHappyScenarios' own tail documents: leave
	// both checkouts on a clean main so whichever family runs next does not
	// inherit a dirty working tree that looks like its own bug. Best-effort;
	// a failure here does not change the already-recorded result above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// ac973Scenario is this row's catalogue name (catalogue.go).
const ac973Scenario = "contract-integrity-registered-consumer"

// ac973Slug is this row's lowercase-kebab standing-slug base. The actual
// artifact is scoped to h.PRFloor through liveRunSlug so a destructive reset
// cannot collide with a merged semantic operation retained by GitHub.
const ac973Slug = "ac973-registered-consumer"

// ac973AnnouncementIDPattern extracts an XA- id from `a2a contract
// deprecate`'s own composite branch name (a2a/<sys>/contract-deprecate/
// <XA-id>+<XC-id> — buildRequest's sorted, "+"-joined ArtifactID), never
// from stdout text: parsing the branch the row ALREADY resolved through
// pullForBranchContaining (the Part-1 fix) means this extraction keeps
// working even if the CLI's own "opened PR ... for X, Y" sentence changes,
// and it doubles as this row's own live exercise of that fix.
var ac973AnnouncementIDPattern = regexp.MustCompile(`XA-[A-Za-z0-9-]+`)

// ac973ResultFromErr renders err into this row's ONE Result, tagging
// Expected with step so a failing report line says WHICH step of the
// sequence broke, never just "contract integrity row failed" (brief: "the
// report must name which step failed"). Delegates the verdict mapping to
// happyVerdictForErr (scenarios_happy_live.go) — the one place a
// network/timeout class is distinguished from a real product failure — so
// this family does not re-decide that rule a second time.
func ac973ResultFromErr(step string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	res := Result{
		Scenario: ac973Scenario, System: SystemA, Surface: SurfaceCLI,
		Verdict: verdict, Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: err.Error(),
	}
	if errors.Is(err, ErrNoPRForBranch) || errors.Is(err, ErrNoBranchMatch) || errors.Is(err, ErrAmbiguousBranchMatch) {
		res.Detail = err.Error()
	}
	return res
}

// ac973Fail builds a VerdictFail row for an assertion this family observed
// directly (not from a propagated error) — the CLI ran, but what it did was
// wrong. Also step-tagged, same reasoning as ac973ResultFromErr.
func ac973Fail(step, observed, expected, detail string) Result {
	return Result{
		Scenario: ac973Scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// ac973DraftContractExcludingPeer drafts a contract through the public
// authoring API with ONE explicit field override. The default targets the
// peer, and this
// harness's topology has exactly two systems — so with the default fill, B
// would ALWAYS already be a member of the contract's authoring-time `to:`,
// which would make this row's whole premise (AC-971.1: a system registered
// ONLY via `contract adopt`, never in `to:`, still receives the
// deprecation) unconstructible. This edit — self-addressing `to:
// [<own system>]` — is the smallest input that keeps `to:` non-empty
// (schemas/envelope/v1/base.schema.json's own `to` requires either
// `minItems: 1` or the literal `"all"` — REF-006, cmd_contract.go's own doc
// comment on runDeprecate) while genuinely excluding the peer. It is a
func ac973DraftContractExcludingPeer(ctx context.Context, h *harness, c *checkout) (submitted, error) {
	id, _, err := c.Draft(ctx, "contract",
		"--slug", liveRunSlug(ac973Slug, h.PRFloor),
		"--field", "to=["+c.System+"]",
	)
	if err != nil {
		return submitted{}, err
	}
	return h.submitDrafted(ctx, c, id)
}

// ac973BreakSchema overwrites the contract's D-D-scaffolded schema in A's
// own STAGING tree with a genuinely INCOMPATIBLE one: the scaffolded schema
// declares `example` a string (internal/template/scaffold/
// contract.schema.json) and the scaffolded fixture is
// `{"example": "replace-me"}` (a string); this rewrite narrows `example` to
// an integer, so the EXISTING fixture no longer validates — a real breaking
// change, not a synthetic refusal. fixtureCompatKey is the key
// CheckComputedCompatibility's own refusal message names it by
// ("<sub>/<file>" relative to the descriptor's own directory —
// "fixtures/valid/<slug>.json" here, NOT the repo-relative path), for the
// row's own assertion to match.
//
// STAGING, not the mirror — and getting that wrong is the entire story of
// live run 3 (2026-07-25). This helper used to write into A's MIRROR
// working tree, which read as the obvious place: `contract publish` reads
// schema/** from a directory, and that was the directory. The row reported
// `contract publish --bump minor exited 0` on a genuinely breaking edit,
// and the diagnosis was that the write never reached the check at all:
//
//	`a2a contract <verb>` resolves its deps through cmd/a2a/wire.go's
//	runContract -> resolveLifecycleDeps -> space.CloneOrFetch ->
//	checkoutRemoteHead, which runs `git checkout -B <branch>
//	origin/<branch>` + `git reset --hard origin/<branch>` UNCONDITIONALLY,
//	on every invocation ("a mirror is a cache, so the move is
//	unconditional"). The very next `a2a contract publish` wiped this edit
//	BEFORE runPublish read schema/** out of the working tree — so both
//	sides of the compat comparison were the landed bytes, and the check
//	compared a version against itself.
//
// P37 wave I answered it: `contract publish` now folds `.a2a/staging/` over
// the landed tree and carries the staged sidecars in the same commit as the
// version bump. Staging is the one surface the reset never touches, so it
// is where an author's schema edit has to live — which is exactly what this
// row must exercise, because it is what a real producer does.
//
// An earlier revision of this comment claimed the digest recorded for
// v2.0.0 covered "content that was never actually pushed". It did not: the
// edit was gone by the time the digest was computed. Retracted here rather
// than left to be re-derived.
// ac973SchemaDir is this family's ONE place that names the contract's
// space-relative schema directory, so the staging write below and the
// on-main read at step 4 cannot drift to different paths — which is the
// failure this row is least able to notice, since a read of the wrong path
// looks exactly like a publish that carried nothing.
func ac973SchemaDir(system string) (string, error) {
	layout, err := space.NewLayout(system)
	if err != nil {
		return "", err
	}
	return layout.ProvidesSchemaDir(ac973Slug), nil
}

func ac973BreakSchema(a *checkout, id string) (fixtureCompatKey string, err error) {
	schemaDir, err := ac973SchemaDir(a.System)
	if err != nil {
		return "", err
	}
	// The same path `a2a contract new` scaffolded (template.
	// ScaffoldContractInStaging), so this OVERWRITES the author's own staged
	// schema rather than adding a second file beside it.
	schemaPath := filepath.Join(a.Dir, ".a2a", "staging", filepath.FromSlash(schemaDir), ac973Slug+".schema.json")
	if _, statErr := os.Stat(schemaPath); statErr != nil {
		// Fail loudly. A missing staged scaffold would make os.WriteFile
		// happily create a file the publish overlay still picks up, so the
		// row would stay green while silently no longer testing the thing
		// it names — an author EDITING a published contract's schema.
		return "", fmt.Errorf("livee2e: expected `a2a contract new`'s staged schema at %s: %w", schemaPath, statErr)
	}
	broken := []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "example": { "type": "integer" }
  },
  "additionalProperties": true
}
`)
	if err := os.WriteFile(schemaPath, broken, 0o644); err != nil {
		return "", fmt.Errorf("livee2e: write breaking schema %s: %w", schemaPath, err)
	}
	return "fixtures/valid/" + ac973Slug + ".json", nil
}

// ac973ExtractAnnouncementID pulls the deprecation announcement's id out of
// the composite branch pullForBranchContaining already resolved
// (a2a/<sys>/contract-deprecate/<XA-id>+<XC-id>) rather than parsing the
// CLI's own stdout sentence — it stays correct even if that sentence's
// wording changes, and it is this row's own live exercise of the Part-1
// fix's return value.
func ac973ExtractAnnouncementID(headRef, contractID string) (string, error) {
	matches := ac973AnnouncementIDPattern.FindAllString(headRef, -1)
	for _, m := range matches {
		if m != contractID {
			return m, nil
		}
	}
	return "", fmt.Errorf("livee2e: branch %q carries no XA- id distinct from %s", headRef, contractID)
}

// ac973ContractIntegrity is AC-973.1's own row: publish -> adopt (by the
// OTHER system, never in the contract's own `to:`) -> a breaking change
// declared as a minor is refused, naming the fixture, and opens no PR ->
// the SAME change published correctly as a major -> deprecate the prior
// version (registered-consumer addressing, F3) -> the consumer sees it in
// `a2a inbox --actionable` though it was never in `to:` (AC-971.1, live) ->
// the consumer acks -> retire succeeds because the one registered consumer
// acked.
//
// F2/atomicity is DELIBERATELY NOT ENFORCED (spec 37 §11's 2026-07-25
// amendment: internal/fold models a contract's lifecycle per SUBJECT, not
// per VERSION, so a `deprecate` before a later `publish` would brick the
// contract's remaining lifecycle on an illegal-transition refusal that has
// nothing to do with this phase). This row therefore publishes BOTH
// versions before deprecating either — the one ordering the state machine
// actually allows — and never asserts a major publish is refused for a
// missing deprecation.
func ac973ContractIntegrity(ctx context.Context, h *harness) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return ac973ResultFromErr("sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh contract-integrity run")
	}

	// --- 1. A publishes a JSON-Schema contract (schema + valid fixture,
	// D-D scaffolded), addressed to itself so B is NOT in the authoring
	// `to:` — the condition AC-971.1 needs to be meaningful. ---
	sub, err := ac973DraftContractExcludingPeer(ctx, h, a)
	if err != nil {
		return ac973ResultFromErr("draft-submit-contract", err, "draft+submit a JSON-Schema contract (schema+valid fixture scaffolded), `to:` excluding the peer, opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return ac973ResultFromErr("publish-land-sync", err, "the published contract lands on main and reaches A's mirror")
	}

	// --- 1b. Register v1.0.0 as a REAL published version. A raw `a2a
	// submit` writes a TPublish event with no `version` field
	// (cmd_submit.go's submitEventDoc), so it does not count toward
	// contractPublishedVersions — without this explicit registration, the
	// step-3 compat check below would see zero prior versions and treat
	// itself as the FIRST publish (compat-check-exempt), and the breaking
	// change would NOT be refused. This sub-step is not literal in the
	// brief's own step 1 wording; it is required by the product's actual
	// behavior and is recorded as a deviation in the wave report.
	if _, stderr, err := a.Run(ctx, "contract", "publish", sub.ID, "--version", "1.0.0"); err != nil {
		return ac973ResultFromErr("register-baseline-v1", fmt.Errorf("%w: %s", err, stderr), "`a2a contract publish --version 1.0.0` registers the first REAL published version (isFirstPublish, G1-gated, no compat baseline yet)")
	}
	publishBranch := space.BranchName(a.System, "contract-publish", sub.ID)
	baselinePR, err := h.pullForBranch(ctx, publishBranch)
	if err != nil {
		return ac973ResultFromErr("register-baseline-v1", err, "the v1.0.0 registration opens its own PR on the contract-publish branch")
	}
	if err := happyLandAndSync(ctx, h, a, baselinePR.Number); err != nil {
		return ac973ResultFromErr("register-baseline-land-sync", err, "the v1.0.0 registration lands on main and reaches A's mirror")
	}

	// --- 2. B adopts the contract: a REGISTERED consumer, deliberately
	// never in the contract's own `to:`. ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return ac973ResultFromErr("consumer-sync-before-adopt", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed contract before adopting it")
	}
	if _, stderr, err := b.Run(ctx, "contract", "adopt", sub.ID); err != nil {
		return ac973ResultFromErr("consumer-adopt", fmt.Errorf("%w: %s", err, stderr), "`a2a contract adopt` registers B as a consumer, opening its own PR")
	}
	adoptPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "contract-adopt", sub.ID))
	if err != nil {
		return ac973ResultFromErr("consumer-adopt", err, "contract adopt's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, adoptPR.Number); err != nil {
		return ac973ResultFromErr("consumer-adopt-land-sync", err, "B's adoption lands on main and reaches B's mirror")
	}

	// --- 3. A attempts a BREAKING change declared as a minor: refused,
	// naming the fixture (AC-970.1), and opens NO PR. ---
	fixtureCompatKey, err := ac973BreakSchema(a, sub.ID)
	if err != nil {
		return ac973ResultFromErr("break-schema", err, "the contract's schema can be rewritten to narrow `example` from string to integer, breaking the existing fixture")
	}

	beforeCount, err := h.countPRsForBranch(ctx, publishBranch)
	if err != nil {
		return ac973ResultFromErr("breaking-minor-refused", err, "can list PRs on the contract-publish branch before the refused attempt")
	}
	_, minorStderr, minorErr := a.Run(ctx, "contract", "publish", sub.ID, "--bump", "minor")
	if minorErr == nil {
		return ac973Fail("breaking-minor-refused", "a2a contract publish --bump minor exited 0",
			"AC-970.1: a breaking change declared as a minor is REFUSED, naming the offending fixture", "")
	}
	if !strings.Contains(minorStderr, "POL-007") || !strings.Contains(minorStderr, fixtureCompatKey) {
		return ac973Fail("breaking-minor-refused", minorStderr,
			fmt.Sprintf("AC-970.1: the refusal names POL-007 and the offending fixture (%s)", fixtureCompatKey), minorStderr)
	}
	afterCount, err := h.countPRsForBranch(ctx, publishBranch)
	if err != nil {
		return ac973ResultFromErr("breaking-minor-no-pr-opened", err, "can list PRs on the contract-publish branch after the refused attempt")
	}
	if afterCount != beforeCount {
		return ac973Fail("breaking-minor-no-pr-opened",
			fmt.Sprintf("PR count on %s went from %d to %d", publishBranch, beforeCount, afterCount),
			"a refused publish never reaches the write funnel, so it opens NO new PR", "")
	}

	// --- 4. A publishes the SAME change correctly, as a major (D-B: a
	// major bump is not compat-checked, and says so). ---
	if _, stderr, err := a.Run(ctx, "contract", "publish", sub.ID, "--bump", "major"); err != nil {
		return ac973ResultFromErr("major-publish", fmt.Errorf("%w: %s", err, stderr), "the SAME breaking change declared as a major publishes successfully (majors are not compat-checked)")
	}
	majorPR, err := h.pullForBranch(ctx, publishBranch)
	if err != nil {
		return ac973ResultFromErr("major-publish", err, "the major publish opens its own PR on the contract-publish branch (delete_branch_on_merge means the branch name is safely reused)")
	}
	if err := happyLandAndSync(ctx, h, a, majorPR.Number); err != nil {
		return ac973ResultFromErr("major-publish-land-sync", err, "v2.0.0 lands on main and reaches A's mirror")
	}

	// The carry, proven on main. Before P37 wave I `contract publish` wrote
	// only [descriptor, event], so a schema change could not reach the space
	// through ANY verb — this assertion is the one that would have caught
	// that, and it reads the mirror AFTER a sync, i.e. it is reading
	// origin/main rather than anything local. Without it the row could pass
	// on a publish that refused the minor for the right reason and then
	// landed a major carrying nothing.
	landedSchemaDir, err := ac973SchemaDir(a.System)
	if err != nil {
		return ac973ResultFromErr("major-publish-carries-schema", err,
			"the contract's schema directory resolves for A's own system")
	}
	landedSchema, err := os.ReadFile(filepath.Join(a.MirrorDir(),
		filepath.FromSlash(landedSchemaDir), ac973Slug+".schema.json"))
	if err != nil {
		return ac973ResultFromErr("major-publish-carries-schema", err,
			"the contract's schema is readable on main after the major publish landed")
	}
	if !strings.Contains(string(landedSchema), `"integer"`) {
		return ac973Fail("major-publish-carries-schema", string(landedSchema),
			"the major publish CARRIES the staged breaking schema onto main (P37 wave I) — `example` is typed integer there, not the scaffold's string",
			"the schema on main is still the scaffolded one, so publish landed a version bump describing content it did not deliver")
	}

	// --- 5. A deprecates the PRIOR version. --version is REQUIRED now
	// (two distinct published versions: 1.0.0 and 2.0.0 — AC-972.1). ---
	successor := sub.ID + "@2.0.0"
	if _, stderr, err := a.Run(ctx, "contract", "deprecate", sub.ID, "--version", "1.0.0", "--successor", successor, "--sunset", "2020-01-01"); err != nil {
		return ac973ResultFromErr("deprecate-prior-version", fmt.Errorf("%w: %s", err, stderr), "`a2a contract deprecate --version 1.0.0` succeeds and opens its own PR")
	}
	// Part-1 fix in action: `contract deprecate`'s branch is
	// a2a/<sys>/contract-deprecate/<XA-id>+<XC-id> (buildRequest's sorted,
	// "+"-joined composite of both ids the write touched), which
	// pullForBranch's exact-HeadRef lookup cannot find from the bare
	// contract id alone (the P36 defect this brief's Part 1 fixes).
	deprecatePR, err := h.pullForBranchContaining(ctx, a.System, "contract-deprecate", sub.ID)
	if err != nil {
		return ac973ResultFromErr("deprecate-prior-version", err, "contract deprecate's own composite branch has an open PR")
	}
	announcementID, err := ac973ExtractAnnouncementID(deprecatePR.HeadRef, sub.ID)
	if err != nil {
		return ac973ResultFromErr("deprecate-prior-version", err, "the deprecate PR's own branch names the linked deprecation announcement")
	}
	if err := happyLandAndSync(ctx, h, a, deprecatePR.Number); err != nil {
		return ac973ResultFromErr("deprecate-land-sync", err, "the deprecation lands on main and reaches A's mirror")
	}

	// --- 6. B sees it: AC-971.1 proven live. B was never in the
	// contract's authoring-time `to:` (step 1's own override) — only a
	// registered consumer via `contract adopt` — and still receives the
	// deprecation in its actionable inbox. ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return ac973ResultFromErr("consumer-sync-sees-deprecation", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed deprecation")
	}
	inboxOut, inboxStderr, err := b.Run(ctx, "inbox", "--actionable", "--json")
	if err != nil {
		return ac973ResultFromErr("consumer-inbox-actionable", fmt.Errorf("%w: %s", err, inboxStderr), "B's a2a inbox --actionable succeeds after sync")
	}
	if !strings.Contains(inboxOut, announcementID) {
		return ac973Fail("consumer-inbox-actionable", inboxOut,
			fmt.Sprintf("AC-971.1 (live): B's `a2a inbox --actionable` lists %s even though B was never in the contract's authoring `to:` — only a registered consumer via `contract adopt`", announcementID),
			inboxOut)
	}

	// --- 7. B acks. ---
	if _, stderr, err := b.Run(ctx, "ack", announcementID); err != nil {
		return ac973ResultFromErr("consumer-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack on the deprecation announcement succeeds and opens its own PR")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", announcementID))
	if err != nil {
		return ac973ResultFromErr("consumer-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return ac973ResultFromErr("consumer-ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- 8. A retires the deprecated version: SUCCEEDS, because the one
	// registered consumer acked. ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return ac973ResultFromErr("producer-sync-before-retire", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's ack before retiring")
	}
	if _, stderr, err := a.Run(ctx, "contract", "retire", sub.ID, "--version", "1.0.0"); err != nil {
		return ac973ResultFromErr("retire-succeeds", fmt.Errorf("%w: %s", err, stderr), "`a2a contract retire --version 1.0.0` succeeds ungated: the one registered consumer (B) acked")
	}
	retirePR, err := h.pullForBranch(ctx, space.BranchName(a.System, "contract-retire", sub.ID))
	if err != nil {
		return ac973ResultFromErr("retire-succeeds", err, "contract retire's own branch has an open PR")
	}

	return Result{
		Scenario: ac973Scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf(
			"%s: v1.0.0 (PR #%d) -> B adopts (PR #%d) -> breaking minor refused (no PR) -> v2.0.0 major (PR #%d) -> deprecate v1.0.0 (PR #%d, announcement %s) -> B sees it unaddressed-by-`to:` -> B acks (PR #%d) -> retire succeeds (PR #%d)",
			sub.ID, baselinePR.Number, adoptPR.Number, majorPR.Number, deprecatePR.Number, announcementID, ackPR.Number, retirePR.Number),
	}
}
