//go:build livee2e

package livee2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// runBytesScenarios drives spec 11 (agent-exchange-2026-08, P11-verification)
// wave D2's AC5: a two-agent exchange in a fresh space delivers a document
// AND bytes end to end, driven by the real binary, both directions, byte-
// for-byte at each end. It is this family's ONE exported-shape entry,
// matching runContractIntegrityScenarios/runSubmittedFamilyScenarios/
// runDataLoopScenarios' own convention (plan §"Wave-3 design notes": "the
// fan-out seam is four functions, not a registry"). Every helper below is
// prefixed bytes* — this family's own namespace, so identifiers cannot
// collide across the package's other scenario families.
//
// A byte fetch (`a2a fetch`/`a2a data fetch`) has NO fold transition of its
// own — it is a read, never a lifecycle event — so this is a scenario
// FAMILY, not a conformance Path: pathcoverage_test.go unions
// PathTransitions against fold.TransitionRows(), and a transition-less Step
// would pollute that union. pathcatalogue_paths.go's own header already sets
// the precedent for acts with no fold row (contract adoption, a broadcast
// ack): named in prose/Intent, not modelled as a Path.
//
// The rig is NOT reinvented: this family runs on the same harness every
// other family shares — checkout.Run execs the real `a2a` binary,
// testkit/fakegithub serves the fake host, h.Seam.IsRealGitHub() is false
// for the whole tier (asserted in TestLogicMatrix before any write) — spec
// 11 §8's wave-A5 finding this brief's own allowlist rests on.
//
// Sequence, reusing rather than re-deriving internal/e2e's own hermetic
// coverage (internal/e2e/blob_loop_test.go's TestAttachSubmitLandsWithout
// ContractOrFulfillsPin/TestFetchRoundTripsUnpinnedAttachment/TestFetch
// RefusesCorruptedBlob, internal/e2e/data_loop_test.go) at the CLI-sequence
// level, and reusing this package's own data-loop family helpers
// (dataLoopPack/dataLoopStagingRoot/dataLoopOperationPull,
// scenarios_data_loop_live.go) rather than a second, drifting copy:
//
//  1. A attaches real bytes to a fresh work_request and submits it — two
//     separate funnel writes, landed in order (spec 10 §1's decided
//     ordering: the blob lands on main BEFORE the artifact referencing it
//     is ever submitted).
//  2. B syncs, acks, fetches the attached bytes (`a2a fetch <BL-id>`), and
//     the scenario compares them BYTE-FOR-BYTE against what A attached, and
//     independently recomputes the sha256 digest the product itself
//     declared (attach --json's own attachment.digest) rather than trusting
//     it.
//  3. A registers a real published contract; B packs and delivers a data
//     package back, fulfilling A's ORIGINAL work_request — the row's own
//     decisive property: the SAME exchange carries a document (the
//     work_request) and returns bytes (the data package) on the SAME
//     thread, not two unrelated writes.
//  4. A syncs, acks, verifies (`a2a data verify --record`, a real PASS
//     verdict), fetches the returned package (`a2a data fetch <DP-id>`),
//     and the scenario compares those bytes byte-for-byte too.
//
// The corruption twin (bytesCorruptionTwin) repeats both legs' own
// SETUP, then tampers the COMMITTED bytes directly on origin/main — a raw
// git commit that bypasses the write funnel entirely, exactly
// TestFetchRefusesCorruptedBlob's own idiom — and asserts the refusal each
// leg actually has: `a2a fetch` refuses ErrBlobDigestMismatch
// (internal/space/blob_resolve.go) and `a2a data fetch` refuses
// ErrDigestMismatch (internal/datapackage/fetch.go, VerifyEntrySet). Both
// directions get a twin because the product genuinely refuses on both — see
// bytesCorruptionTwin's own doc comment for the one refusal this row does
// NOT repeat (`a2a data verify` on a tampered STAGED attempt is a JUDGED
// FAIL verdict, not a refusal, already covered by data-loop-fail-supersede-
// pass; this row's own tamper happens to bytes already LANDED on main,
// which is a different mechanism from that row's pre-delivery corruption).
func runBytesScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{bytesRoundTrip(ctx, h), bytesCorruptionTwin(ctx, h)}

	// Same parking discipline every other family in this package documents
	// (runHappyScenarios/runContractIntegrityScenarios/
	// runSubmittedFamilyScenarios/runDataLoopScenarios' own tails): leave
	// both checkouts on a clean main so whichever family runs next does not
	// inherit a dirty working tree that looks like its own bug. Best-effort;
	// a failure here does not change the already-recorded results above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// This family's two catalogue names (catalogue.go carries the literal
// duplicates — catalogue.go is untagged and cannot reference these
// build-tag-gated constants, the same split ac973Scenario/subfamQuestion
// Scenario already live with).
const (
	bytesRoundTripScenario    = "bytes-round-trip"
	bytesCorruptionScenario   = "bytes-round-trip-corrupted-refused"
	bytesAttachPayloadName    = "bytes-payload.bin"
	bytesDataPayloadName      = "payload.json"
	bytesRoundTripContractTag = "bytes-round-trip"
	bytesCorruptContractTag   = "bytes-round-trip-corrupted"
)

// bytesResultFromErr renders err into this family's Result, step-tagging
// Expected exactly as subfamResultFromErr/dataLoopResultFromErr do — both
// rows are multi-step chains, and a bare "the row failed" would not say
// which leg broke. Parameterized on scenario (unlike ac973's single-name
// helper) because this file's TWO rows share one shape, mirroring
// subfamResultFromErr's own reason for taking scenario as its first
// argument.
func bytesResultFromErr(scenario, step string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	res := Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI,
		Verdict: verdict, Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: err.Error(),
	}
	if errors.Is(err, ErrNoPRForBranch) || errors.Is(err, ErrNoBranchMatch) || errors.Is(err, ErrAmbiguousBranchMatch) {
		res.Detail = err.Error()
	}
	return res
}

// bytesFail builds a VerdictFail row for an assertion this family observed
// directly (the CLI ran, but what it did was wrong, or a refusal that
// should have fired did not) — same step-tagging as bytesResultFromErr.
func bytesFail(scenario, step, observed, expected, detail string) Result {
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// bytesGeneratePayload builds a real, deterministic byte string carrying
// non-text bytes (0x00, 0x01, 0x02, 0xFE, 0xFF) — the property that makes a
// byte-for-byte compare a proof about BYTES, not merely about a string that
// happened to round-trip through a few text-safe encodings. tag scopes it
// per call site so the round-trip row and the corruption-twin row never
// attach byte-identical content (a defect this family's own comparisons
// would not notice: two IDENTICAL payloads landing on two DIFFERENT blobs
// would still assert byte-equality "successfully" even if fetch quietly
// resolved the WRONG blob).
func bytesGeneratePayload(tag string) []byte {
	payload := []byte("bytes-e2e:" + tag + ":")
	payload = append(payload, 0x00, 0x01, 0x02, 0xFE, 0xFF)
	payload = append(payload, []byte(tag)...)
	return payload
}

// bytesFlipFirstByte returns a copy of src with its first byte flipped —
// this file's own corruption primitive. A copy, never an in-place mutation:
// every caller still holds the ORIGINAL bytes to assert the refusal against
// (the corrupted content must never equal what was legitimately committed).
func bytesFlipFirstByte(src []byte) []byte {
	out := append([]byte(nil), src...)
	if len(out) > 0 {
		out[0] ^= 0xFF
	}
	return out
}

// bytesDiffDetail renders a byte mismatch so a failing report line names
// WHERE the two slices disagree, not merely THAT they do — the brief's own
// requirement that the comparison "reddens naming both actual and wanted".
func bytesDiffDetail(got, want []byte) string {
	if len(got) != len(want) {
		return fmt.Sprintf("length mismatch: got %d bytes, want %d bytes (got=%s want=%s)",
			len(got), len(want), hex.EncodeToString(got), hex.EncodeToString(want))
	}
	for i := range got {
		if got[i] != want[i] {
			lo, hi := i-4, i+4
			if lo < 0 {
				lo = 0
			}
			if hi > len(got) {
				hi = len(got)
			}
			return fmt.Sprintf("first differing byte at index %d of %d: got %s, want %s (window [%d:%d))",
				i, len(got), hex.EncodeToString(got[lo:hi]), hex.EncodeToString(want[lo:hi]), lo, hi)
		}
	}
	return "bytes.Equal reported a mismatch but no differing index was found (unreachable)"
}

// bytesAttachJSONResult decodes `a2a attach --json`'s wire shape
// (attachJSONResult, internal/cli/cmd_attach.go), narrowed to the two
// fields this file needs.
type bytesAttachJSONResult struct {
	Draft      string `json:"draft"`
	Attachment struct {
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
	} `json:"attachment"`
}

// bytesFetchJSONResult decodes `a2a fetch --json`'s wire shape (FetchResult,
// internal/cli/cmd_fetch.go).
type bytesFetchJSONResult struct {
	Ref         string `json:"ref"`
	Destination string `json:"destination"`
	Outcome     string `json:"outcome"`
}

// bytesAttachPull resolves the ONE pull request `a2a attach`'s own write
// opened, WITHOUT reproducing blobDeliverOperationKey's private hash
// (internal/space/blob_delivery.go). This is a genuine wire-shape gap,
// reported in this wave's own findings: unlike `data deliver`/`data
// verify` — whose --json result carries Write.Branch/.PRNumber
// (dataLoopOperationPull, scenarios_data_loop_live.go) — AttachCommand's
// own --json result (attachJSONResult) carries NO Write field at all, only
// Draft and Attachment; the branch/PR only appear in attach's PLAIN-TEXT
// stdout, which this package's own precedent (DraftAndSubmit's doc
// comment, harness_live.go) explicitly avoids scraping.
//
// Matching by the funnel's own deterministic branch PREFIX
// (`a2a/<system>/attach/`, BranchName's own grammar — branchmatch.go) and
// picking the highest PR number under it is safe — including across this
// family's TWO rows, each of which attaches once from A — because
// testkit/fakegithub numbers pull requests with a single monotonically
// increasing counter (`s.next++`, fakegithub.go's own createPull), never
// reused and never per-branch: the highest-numbered PR under this prefix at
// call time is unconditionally the one THIS call's own attach just opened,
// regardless of how many earlier attaches (this row's or an earlier row's)
// also match the prefix. Mirrors pullForBranch's own "prefer open, else
// highest number" tie-break (harness_live.go), generalized across a prefix
// instead of one exact branch equality.
func bytesAttachPull(ctx context.Context, h *harness, system string) (PullState, error) {
	prefix := "a2a/" + system + "/attach/"
	var best PullState
	err := awaitLookupVisibility(
		ctx,
		prVisibilityAttempts,
		func() error {
			pulls, err := h.runPulls(ctx)
			if err != nil {
				return err
			}
			var found bool
			for _, p := range pulls {
				if !strings.HasPrefix(p.HeadRef, prefix) {
					continue
				}
				if !found || p.Number > best.Number {
					best, found = p, true
				}
			}
			if !found {
				return fmt.Errorf("%w: no pull request under %q (a2a attach's own branch prefix)", ErrNoPRForBranch, prefix)
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, ErrNoPRForBranch) },
		h.pauseForPRVisibility,
	)
	if err != nil {
		return PullState{}, err
	}
	return best, nil
}

// bytesAttached is bytesDraftAttachSubmit's own result: the two ids and the
// digest the product declared, everything a caller needs to fetch, compare
// and fulfil this work_request later.
type bytesAttached struct {
	WorkRequestID string
	BlobID        string
	Digest        string
}

// bytesDraftAttachSubmit drafts a fresh work_request from c, attaches
// payload to it (a real network write, landed on its own), then submits the
// SAME draft (a second, separate write, landed on its own) — the exact
// sequence internal/e2e/blob_loop_test.go's own TestAttachSubmitLandsWithout
// ContractOrFulfillsPin proves hermetically, driven here through the real
// binary against the fake host.
func bytesDraftAttachSubmit(ctx context.Context, h *harness, c *checkout, payload []byte) (bytesAttached, error) {
	id, _, err := c.Draft(ctx, "work_request")
	if err != nil {
		return bytesAttached{}, fmt.Errorf("draft work_request: %w", err)
	}

	sourcePath := filepath.Join(c.Dir, bytesAttachPayloadName)
	if err := os.WriteFile(sourcePath, payload, 0o644); err != nil {
		return bytesAttached{}, fmt.Errorf("write local source %s: %w", sourcePath, err)
	}

	// The bare draft id, never a hand-built `.a2a/staging/<id>.md` path:
	// AttachCommand.Run resolves its one positional through the SAME
	// resolveSubmitTarget `a2a submit` uses (internal/cli/cmd_attach.go,
	// cmd_submit.go) — a bare id with no "/" and no ".md" suffix resolves
	// against the staging dir internally, exactly like h.submitDrafted's own
	// `c.Run(ctx, "submit", id)` below never builds that path either.
	// draftfields_test.go's own TestLiveScenariosHaveNoFrontmatterPatchPath
	// bans `*_live.go` from constructing it directly — the harness used to
	// reopen staged Markdown and regex-patch it, and this keeps that second
	// authoring path structurally absent.
	stdout, stderr, err := c.Run(ctx, "attach", id, "--from", sourcePath, "--verification", "offered", "--json")
	if err != nil {
		return bytesAttached{}, fmt.Errorf("a2a attach %s: %w: %s", id, err, strings.TrimSpace(stderr))
	}
	var attached bytesAttachJSONResult
	if jerr := json.Unmarshal([]byte(stdout), &attached); jerr != nil {
		return bytesAttached{}, fmt.Errorf("decode attach --json result: %w: %s", jerr, stdout)
	}
	if attached.Attachment.Ref == "" {
		return bytesAttached{}, fmt.Errorf("attach --json result carries no attachment.ref: %s", stdout)
	}

	attachPull, err := bytesAttachPull(ctx, h, c.System)
	if err != nil {
		return bytesAttached{}, fmt.Errorf("resolve attach's own pull request: %w", err)
	}
	if err := happyLandAndSync(ctx, h, c, attachPull.Number); err != nil {
		return bytesAttached{}, fmt.Errorf("land the attach PR: %w", err)
	}

	sub, err := h.submitDrafted(ctx, c, id)
	if err != nil {
		return bytesAttached{}, fmt.Errorf("a2a submit %s: %w", id, err)
	}
	if err := happyLandAndSync(ctx, h, c, sub.PRNumber); err != nil {
		return bytesAttached{}, fmt.Errorf("land the submit PR: %w", err)
	}

	return bytesAttached{WorkRequestID: id, BlobID: attached.Attachment.Ref, Digest: attached.Attachment.Digest}, nil
}

// bytesSyncCheckInboxAck syncs c, optionally confirms id is visible in c's
// own inbox (AC-980.1's own "the counterpart actually sees it" leg,
// subfamInboxContains, scenarios_submitted_live.go), acks id, and lands the
// ack. checkInbox is false for a handoff ack (data-loop-fail-supersede-
// pass's own precedent: only the work_request ack checks inbox visibility).
func bytesSyncCheckInboxAck(ctx context.Context, h *harness, c *checkout, id string, checkInbox bool) error {
	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return fmt.Errorf("sync: %w: %s", err, strings.TrimSpace(stderr))
	}
	if checkInbox {
		seen, inboxOut, err := subfamInboxContains(ctx, c, id)
		if err != nil {
			return fmt.Errorf("inbox: %w", err)
		}
		if !seen {
			return fmt.Errorf("inbox does not list %s after sync: %s", id, inboxOut)
		}
	}
	if _, stderr, err := c.Run(ctx, "ack", id); err != nil {
		return fmt.Errorf("ack %s: %w: %s", id, err, strings.TrimSpace(stderr))
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(c.System, "ack", id))
	if err != nil {
		return fmt.Errorf("resolve ack PR for %s: %w", id, err)
	}
	if err := happyLandAndSync(ctx, h, c, ackPR.Number); err != nil {
		return fmt.Errorf("land ack PR for %s: %w", id, err)
	}
	return nil
}

// bytesFetchAttachmentAndCompare runs `a2a fetch <BL-id> --to <dir> --json`
// from c, and compares the installed bytes BYTE-FOR-BYTE against want — the
// exact bytes the attaching system attached — never against a length or a
// digest string alone. It also independently recomputes want's own sha256
// digest (runtimeDigest, report.go — the same "sha256:<hex>" form
// artifact.Digest produces, entryset.go's digestForPayload single-entry
// branch) and asserts it equals wantDigest, the value attach --json itself
// declared: the product's OWN computed digest, checked rather than trusted.
func bytesFetchAttachmentAndCompare(ctx context.Context, c *checkout, blobID, wantDigest string, want []byte) error {
	if computed := runtimeDigest(want); computed != wantDigest {
		return fmt.Errorf("attach's own declared digest %s does not match sha256 of the attached bytes (%s)", wantDigest, computed)
	}

	dest := filepath.Join(c.Dir, "bytes-fetch-"+blobID)
	stdout, stderr, err := c.Run(ctx, "fetch", blobID, "--to", dest, "--json")
	if err != nil {
		return fmt.Errorf("a2a fetch %s: %w: %s", blobID, err, strings.TrimSpace(stderr))
	}
	var fetched bytesFetchJSONResult
	if jerr := json.Unmarshal([]byte(stdout), &fetched); jerr != nil {
		return fmt.Errorf("decode fetch --json result: %w: %s", jerr, stdout)
	}
	if fetched.Outcome != "installed" {
		return fmt.Errorf("fetch outcome = %q, want %q", fetched.Outcome, "installed")
	}

	got, err := os.ReadFile(filepath.Join(dest, bytesAttachPayloadName))
	if err != nil {
		return fmt.Errorf("read fetched payload: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("fetched bytes do not match attached bytes byte-for-byte: %s", bytesDiffDetail(got, want))
	}
	return nil
}

// bytesRegisterContract publishes a real JSON-Schema contract (D-D
// scaffolded) and registers v1.0.0 as an actually-published version — the
// exact two-step baseline dataLoopFailSupersedePass's own steps 1/1b
// establish (scenarios_data_loop_live.go), restated here under this row's
// own slug so this family's contract can never collide with data-loop's.
// slugTag scopes the standing slug per CALLER (bytesRoundTrip vs
// bytesCorruptionTwin both call this) so the two rows' own contracts never
// collide with EACH OTHER either — `liveRunSlug` alone only scopes to the
// run/PRFloor generation, not to which row within it is calling.
func bytesRegisterContract(ctx context.Context, h *harness, c *checkout, slugTag string) (contractRef string, err error) {
	sub, err := h.DraftAndSubmit(ctx, c, "contract", "--slug", liveRunSlug(slugTag, h.PRFloor))
	if err != nil {
		return "", fmt.Errorf("draft+submit contract: %w", err)
	}
	if err := happyLandAndSync(ctx, h, c, sub.PRNumber); err != nil {
		return "", fmt.Errorf("land contract submit: %w", err)
	}
	paths, err := contractPathsForID(sub.ID)
	if err != nil {
		return "", fmt.Errorf("resolve contract staging: %w", err)
	}
	_, pub, err := contractPublishPull(ctx, h, c, contractPublishVersionArgs(sub.ID, "1.0.0", paths.StagingRoot))
	if err != nil {
		return "", fmt.Errorf("register v1.0.0: %w", err)
	}
	if err := happyLandAndSync(ctx, h, c, pub.Number); err != nil {
		return "", fmt.Errorf("land v1.0.0 registration: %w", err)
	}
	return sub.ID + "@1.0.0", nil
}

// bytesPacked is bytesPackDeliverAndLand's own result.
type bytesPacked struct {
	HandoffID string
	PackageID string
}

// bytesPackDeliverAndLand packs content against contractRef, delivers it
// fulfilling fulfillsID, and lands the delivery — reusing scenarios_data_
// loop_live.go's own dataLoopPack/dataLoopStagingRoot/dataLoopOperationPull
// (this package's already-proven recipe for "read the staging_root/branch
// the product's own JSON reported, never guess or reconstruct either")
// rather than a second, drifting copy.
func bytesPackDeliverAndLand(ctx context.Context, h *harness, c *checkout, contractRef, fulfillsID string, content []byte) (bytesPacked, error) {
	// c's mirror may still be parked on the state it last synced to BEFORE
	// bytesRegisterContract landed the pinned contract on main (e.g. B's
	// own last sync happened while acking/fetching the attachment, before A
	// registered the contract this call is about to pin) — `data pack`
	// resolves the contract out of c's LOCAL mirror
	// (space.ResolveDataPackageSchemas), never over the network, so a stale
	// mirror here refuses "contract version is not published" even though
	// the real space already carries it. Synced unconditionally rather than
	// left to whichever caller happens to have synced last.
	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return bytesPacked{}, fmt.Errorf("sync before pack: %w: %s", err, strings.TrimSpace(stderr))
	}

	from := filepath.Join(c.Dir, "bytes-package-source")
	if err := os.MkdirAll(from, 0o755); err != nil {
		return bytesPacked{}, fmt.Errorf("mkdir %s: %w", from, err)
	}
	if err := os.WriteFile(filepath.Join(from, bytesDataPayloadName), content, 0o644); err != nil {
		return bytesPacked{}, fmt.Errorf("write %s: %w", from, err)
	}

	pack, err := dataLoopPack(ctx, c, contractRef, from, fulfillsID, "")
	if err != nil {
		return bytesPacked{}, fmt.Errorf("data pack: %w", err)
	}
	staging, err := dataLoopStagingRoot(pack, "bytes-package")
	if err != nil {
		return bytesPacked{}, err
	}
	deliver, deliverPull, err := dataLoopOperationPull(ctx, h, c, []string{"data", "deliver", staging, "--fulfills", fulfillsID})
	if err != nil {
		return bytesPacked{}, fmt.Errorf("data deliver: %w", err)
	}
	if deliver.HandoffID == "" {
		return bytesPacked{}, fmt.Errorf("deliver result carries no handoff_id: %+v", deliver)
	}
	if err := happyLandAndSync(ctx, h, c, deliverPull.Number); err != nil {
		return bytesPacked{}, fmt.Errorf("land deliver PR: %w", err)
	}
	return bytesPacked{HandoffID: deliver.HandoffID, PackageID: pack.Manifest.ID}, nil
}

// bytesVerifyPackageAndFetch acks the handoff, runs `a2a data verify
// --record` (asserting a real PASS verdict, not a literal — the digest the
// product itself computed against what B packed), lands it, then fetches
// the package (`a2a data fetch <DP-id> --to <dir> --json`) and compares its
// installed bytes BYTE-FOR-BYTE against want.
func bytesVerifyPackageAndFetch(ctx context.Context, h *harness, c *checkout, handoffID, packageID string, want []byte) error {
	if err := bytesSyncCheckInboxAck(ctx, h, c, handoffID, false); err != nil {
		return fmt.Errorf("ack handoff: %w", err)
	}

	verify, verifyPull, err := dataLoopOperationPull(ctx, h, c, []string{"data", "verify", packageID, "--record"})
	if err != nil {
		return fmt.Errorf("data verify --record: %w", err)
	}
	if verify.Report == nil || verify.Report.Result() != datapackage.ResultPass {
		observed := "<nil report>"
		if verify.Report != nil {
			observed = string(verify.Report.Result())
		}
		return fmt.Errorf("verify verdict = %s, want %s", observed, datapackage.ResultPass)
	}
	if err := happyLandAndSync(ctx, h, c, verifyPull.Number); err != nil {
		return fmt.Errorf("land verify PR: %w", err)
	}

	dest := filepath.Join(c.Dir, "bytes-fetch-"+packageID)
	stdout, stderr, err := c.Run(ctx, "data", "fetch", packageID, "--to", dest, "--json")
	if err != nil {
		return fmt.Errorf("a2a data fetch %s: %w: %s", packageID, err, strings.TrimSpace(stderr))
	}
	var result cli.DataResult
	if jerr := json.Unmarshal([]byte(stdout), &result); jerr != nil {
		return fmt.Errorf("decode data fetch --json result: %w: %s", jerr, stdout)
	}
	if result.Outcome != "installed" {
		return fmt.Errorf("data fetch outcome = %q, want %q", result.Outcome, "installed")
	}
	got, err := os.ReadFile(filepath.Join(dest, bytesDataPayloadName))
	if err != nil {
		return fmt.Errorf("read fetched package payload: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("fetched package bytes do not match packed bytes byte-for-byte: %s", bytesDiffDetail(got, want))
	}
	return nil
}

// bytesRoundTrip is this family's mandatory row: A attaches real bytes to a
// request and submits it; B syncs, fetches the bytes and verifies them
// byte-for-byte plus the digest the product computed; B then packs and
// delivers a data package back FULFILLING THE SAME work_request; A syncs,
// verifies it, and compares those bytes byte-for-byte at A's own end too —
// a document and bytes, both directions, one thread, spec 11 AC5's own
// wording.
func bytesRoundTrip(ctx context.Context, h *harness) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh round trip")
	}

	attachPayload := bytesGeneratePayload("round-trip-attach")
	attached, err := bytesDraftAttachSubmit(ctx, h, a, attachPayload)
	if err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "attach-submit", err, "A attaches real bytes to a fresh work_request and submits it, both writes landing on their own")
	}

	if err := bytesSyncCheckInboxAck(ctx, h, b, attached.WorkRequestID, true); err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "producer-ack", err, "B syncs, sees the work_request in its inbox, and acknowledges it")
	}
	if err := bytesFetchAttachmentAndCompare(ctx, b, attached.BlobID, attached.Digest, attachPayload); err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "fetch-compare", err, "B fetches the attached bytes; they match byte-for-byte and the declared digest is the bytes' own sha256")
	}

	contractRef, err := bytesRegisterContract(ctx, h, a, bytesRoundTripContractTag)
	if err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "register-contract", err, "A registers a real published contract the return package pins")
	}

	packagePayload := []byte(`{"example":"bytes-round-trip-return"}` + "\n")
	packed, err := bytesPackDeliverAndLand(ctx, h, b, contractRef, attached.WorkRequestID, packagePayload)
	if err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "pack-deliver", err, "B packs and delivers a data package fulfilling A's original work_request")
	}

	if err := bytesVerifyPackageAndFetch(ctx, h, a, packed.HandoffID, packed.PackageID, packagePayload); err != nil {
		return bytesResultFromErr(bytesRoundTripScenario, "verify-fetch-compare", err, "A acks the handoff, verify-passes it, fetches the returned package, and its bytes match byte-for-byte")
	}

	narrative := fmt.Sprintf(
		"work_request %s <- blob %s (digest %s, %d bytes) attached+submitted by A, fetched+verified by B -> contract %s registered by A -> package %s (handoff %s, %d bytes) packed+delivered by B, verify-passed and fetched by A",
		attached.WorkRequestID, attached.BlobID, attached.Digest, len(attachPayload),
		contractRef, packed.PackageID, packed.HandoffID, len(packagePayload),
	)
	return Result{
		Scenario: bytesRoundTripScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       narrative,
		PassEvidence: narrative,
	}
}

// bytesCorruptCommittedFile clones origin fresh, overwrites spaceRelativePath
// with corrupted, and pushes a direct commit to main — bypassing the write
// funnel entirely, exactly internal/e2e/blob_loop_test.go's own
// TestFetchRefusesCorruptedBlob idiom (a mirror clone, a direct commit, a
// push), restated here against h.Seam.CloneURL() (the local bare origin the
// logic tier's fake serves, hostseam.go/logicseam.go) rather than a
// hermetic fixture's own remote. Uses this package's own runCmd
// (provision_live.go) — the same helper provisionLocalSpace's own `git
// clone --bare` call already goes through — so every git invocation here is
// wrapped in the SAME error-reporting shape the rest of this package uses;
// R1's per-site gitfixture.Args guard (testkit/gitfixture/hygiene_test.go)
// does not apply to this non-_test.go file's call sites (r1Applies is
// `_test.go` or `testkit/**` only), and the child git processes still
// inherit this package's TestMain-hardened GIT_CONFIG_* environment
// (main_test.go's gitfixture.RunTests), so user.name/user.email and the
// gc-disabling config are already set without this function repeating them.
func bytesCorruptCommittedFile(ctx context.Context, h *harness, spaceRelativePath string, corrupted []byte, message string) error {
	mirror, err := os.MkdirTemp("", "livee2e-bytes-corrupt-*")
	if err != nil {
		return fmt.Errorf("mkdir corruption clone: %w", err)
	}
	defer func() { _ = os.RemoveAll(mirror) }()

	if err := runCmd(ctx, "git", "clone", "-q", h.Seam.CloneURL(), mirror); err != nil {
		return fmt.Errorf("clone origin for direct tamper: %w", err)
	}
	target := filepath.Join(mirror, filepath.FromSlash(spaceRelativePath))
	if err := os.WriteFile(target, corrupted, 0o644); err != nil {
		return fmt.Errorf("write tampered %s: %w", target, err)
	}
	if err := runCmd(ctx, "git", "-C", mirror, "add", "-A"); err != nil {
		return fmt.Errorf("git add tampered file: %w", err)
	}
	if err := runCmd(ctx, "git", "-C", mirror, "commit", "-q", "-m", message); err != nil {
		return fmt.Errorf("git commit tampered file: %w", err)
	}
	if err := runCmd(ctx, "git", "-C", mirror, "push", "-q", "origin", "main"); err != nil {
		return fmt.Errorf("git push tampered file: %w", err)
	}
	return nil
}

// bytesCorruptionTwin repeats bytesRoundTrip's own two legs, under its own
// fresh work_request/blob/contract/package (never bytesRoundTrip's — every
// row in this family authors its own artifacts, the same "no shared-state
// mutation" property runner_live_test.go's driveFamilies documents for this
// family's placement), and on EACH leg tampers the already-landed bytes
// directly on origin/main before the reading system ever looks:
//
//   - the blob leg: `a2a fetch` must refuse ErrBlobDigestMismatch
//     (internal/space/blob_resolve.go: "resolves to bytes that do not match
//     its own committed digest sidecar"), and must not create --to's
//     destination.
//   - the data-package leg: `a2a data fetch` must refuse ErrDigestMismatch
//     (internal/datapackage/fetch.go's VerifyEntrySet: "declared digest does
//     not match computed digest"), and must not create --to's destination
//     either (Fetch's own doc comment, fetch.go: "No byte reaches
//     destination before step 5, and step 5 is the only step that writes").
//
// Both directions get a twin here because the product genuinely refuses on
// both — this row does NOT also repeat `a2a data verify`'s own corruption
// path: data-loop-fail-supersede-pass already proves that a tampered
// STAGED attempt (before delivery) surfaces as a JUDGED FAIL VERDICT, not a
// refusal (dataLoopCorruptRecordCount, scenarios_data_loop_live.go) — a
// different mechanism from this row's own tamper of bytes ALREADY LANDED on
// main, and inventing a second corruption path onto an already-covered
// mechanism would not be a new twin, only a duplicate of one.
func bytesCorruptionTwin(ctx context.Context, h *harness) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh corruption-twin run")
	}

	// --- leg 1 setup + tamper: the attached blob ---
	attachPayload := bytesGeneratePayload("corruption-attach")
	attached, err := bytesDraftAttachSubmit(ctx, h, a, attachPayload)
	if err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "attach-submit", err, "A attaches real bytes to a fresh work_request and submits it")
	}
	if err := bytesSyncCheckInboxAck(ctx, h, b, attached.WorkRequestID, true); err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "producer-ack", err, "B syncs, sees the work_request in its inbox, and acknowledges it")
	}

	corruptedBlob := bytesFlipFirstByte(attachPayload)
	blobPath := path.Join(a.System, "blobs", attached.BlobID, bytesAttachPayloadName)
	if err := bytesCorruptCommittedFile(ctx, h, blobPath, corruptedBlob, "corrupt attached blob payload (test fixture, direct commit)"); err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "corrupt-blob", err, "the committed blob payload can be tampered by a direct commit to origin/main")
	}

	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "sync-corrupted-blob", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the tampered blob commit")
	}
	blobDest := filepath.Join(b.Dir, "bytes-corrupt-blob-fetch")
	blobStdout, blobStderr, blobFetchErr := b.Run(ctx, "fetch", attached.BlobID, "--to", blobDest, "--json")
	if blobFetchErr == nil {
		return bytesFail(bytesCorruptionScenario, "fetch-corrupted-blob", blobStdout,
			"a2a fetch of a corrupted blob refuses (ErrBlobDigestMismatch), exit != 0", "")
	}
	blobMsg := blobStdout + blobStderr
	const blobRefusalText = "resolves to bytes that do not match its own committed digest sidecar"
	if !strings.Contains(blobMsg, blobRefusalText) {
		return bytesFail(bytesCorruptionScenario, "fetch-corrupted-blob", blobMsg,
			fmt.Sprintf("refusal names ErrBlobDigestMismatch (%q)", blobRefusalText), "")
	}
	if _, statErr := os.Stat(blobDest); statErr == nil {
		return bytesFail(bytesCorruptionScenario, "fetch-corrupted-blob-no-write", blobDest,
			"a refused fetch must not create its destination directory", "")
	}

	// --- leg 2 setup + tamper: the returned data package ---
	contractRef, err := bytesRegisterContract(ctx, h, a, bytesCorruptContractTag)
	if err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "register-contract", err, "A registers a real published contract the return package pins")
	}
	packagePayload := []byte(`{"example":"bytes-round-trip-corrupted"}` + "\n")
	packed, err := bytesPackDeliverAndLand(ctx, h, b, contractRef, attached.WorkRequestID, packagePayload)
	if err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "pack-deliver", err, "B packs and delivers a data package fulfilling A's original work_request")
	}

	corruptedPackage := bytesFlipFirstByte(packagePayload)
	packagePath := path.Join(b.System, "data", packed.PackageID, bytesDataPayloadName)
	if err := bytesCorruptCommittedFile(ctx, h, packagePath, corruptedPackage, "corrupt delivered data package payload (test fixture, direct commit)"); err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "corrupt-package", err, "the committed data package payload can be tampered by a direct commit to origin/main")
	}

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return bytesResultFromErr(bytesCorruptionScenario, "sync-corrupted-package", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches the tampered package commit")
	}
	packageDest := filepath.Join(a.Dir, "bytes-corrupt-package-fetch")
	pkgStdout, pkgStderr, pkgFetchErr := a.Run(ctx, "data", "fetch", packed.PackageID, "--to", packageDest, "--json")
	if pkgFetchErr == nil {
		return bytesFail(bytesCorruptionScenario, "fetch-corrupted-package", pkgStdout,
			"a2a data fetch of a corrupted package refuses (ErrDigestMismatch), exit != 0", "")
	}
	pkgMsg := pkgStdout + pkgStderr
	const packageRefusalText = "declared digest does not match computed digest"
	if !strings.Contains(pkgMsg, packageRefusalText) {
		return bytesFail(bytesCorruptionScenario, "fetch-corrupted-package", pkgMsg,
			fmt.Sprintf("refusal names ErrDigestMismatch (%q)", packageRefusalText), "")
	}
	if _, statErr := os.Stat(packageDest); statErr == nil {
		return bytesFail(bytesCorruptionScenario, "fetch-corrupted-package-no-write", packageDest,
			"a refused data fetch must not create its destination directory", "")
	}

	narrative := fmt.Sprintf(
		"blob %s (work_request %s) tampered on origin/main -> `a2a fetch` refused (%s); package %s (handoff %s, contract %s) tampered on origin/main -> `a2a data fetch` refused (%s)",
		attached.BlobID, attached.WorkRequestID, blobRefusalText, packed.PackageID, packed.HandoffID, contractRef, packageRefusalText,
	)
	return Result{
		Scenario: bytesCorruptionScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       narrative,
		PassEvidence: narrative,
	}
}
