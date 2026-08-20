//go:build livee2e

package livee2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// runThreadChainScenarios drives spec 46's OP-210 `a2a thread` read model
// (catalogue.go's thread-chain-reads-identically-from-both-sides row)
// against a REAL two-participant chain — the one thing a hermetic test
// cannot prove: that two independent GitHub identities, each reading
// through their OWN local mirror and their OWN `--system` identity, see the
// SAME thread (same id, same members, same order) while correctly
// DISAGREEING about whose move it is.
//
// It is this row's ONE exported-shape entry, matching every sibling
// family's own convention (runHappyScenarios/runSubmittedFamilyScenarios/
// runContractIntegrityScenarios — plan §"Wave-3 design notes": "the fan-out
// seam is four functions, not a registry", one more file per phase). Every
// helper below is prefixed threadChain* — this row's own scenario
// namespace, so concurrent scenario-family agents sharing this package
// cannot collide on an identifier.
func runThreadChainScenarios(ctx context.Context, h *harness) []Result {
	res := threadChainRun(ctx, h)

	// Same parking discipline every sibling family's own tail documents:
	// leave both checkouts on a clean main so whichever family runs next
	// does not inherit a dirty working tree that looks like its own bug.
	// Best-effort; a failure here does not change the already-recorded
	// result above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return []Result{res}
}

// threadChainScenario is this row's catalogue.go name (the literal
// duplicate is deliberate — catalogue.go is untagged and cannot reference a
// tagged const, the same split scenarios_submitted_live.go's own
// subfam*Scenario consts already live with).
const threadChainScenario = "thread-chain-reads-identically-from-both-sides"

// threadChainResultFromErr renders err into this row's Result, step-tagging
// Expected exactly as subfamResultFromErr does (scenarios_submitted_live.go)
// — this row is a long, multi-step chain (submit -> ack -> respond -> dual
// read -> verify), and a bare "the row failed" would not say which leg
// broke.
func threadChainResultFromErr(step string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	res := Result{
		Scenario: threadChainScenario, System: SystemA, Surface: SurfaceCLI,
		Verdict: verdict, Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: err.Error(),
	}
	if errors.Is(err, ErrNoPRForBranch) || errors.Is(err, ErrNoBranchMatch) || errors.Is(err, ErrAmbiguousBranchMatch) {
		res.Detail = err.Error()
	}
	return res
}

// threadChainFail builds a VerdictFail row for an assertion this row
// observed directly (the CLI ran, but what it returned was wrong) — same
// step-tagging as threadChainResultFromErr.
func threadChainFail(step, observed, expected, detail string) Result {
	return Result{
		Scenario: threadChainScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// threadChainArtifactDoc/threadChainEventDoc/threadChainEntryDoc/
// threadChainOpenItemDoc/threadChainDoc are this row's own minimal decode of
// `a2a thread --json`'s output — the same "shell out to the CLI surface a
// real agent uses, never import internal/cache" convention subfamShowDoc's
// own doc comment records (scenarios_submitted_live.go), applied to
// cache.ThreadResult's JSON shape instead of cache.ShowResult's.
type threadChainArtifactDoc struct {
	ID string `json:"id"`
}

type threadChainEventDoc struct {
	Subject    string `json:"subject"`
	Transition string `json:"transition"`
}

type threadChainEntryDoc struct {
	Kind     string                  `json:"kind"`
	Artifact *threadChainArtifactDoc `json:"artifact,omitempty"`
	Event    *threadChainEventDoc    `json:"event,omitempty"`
}

type threadChainOpenItemDoc struct {
	ID       string `json:"id"`
	YourMove bool   `json:"your_move"`
}

type threadChainDoc struct {
	Thread     string                   `json:"thread"`
	Order      string                   `json:"order"`
	Artifacts  []threadChainArtifactDoc `json:"artifacts"`
	Transcript []threadChainEntryDoc    `json:"transcript"`
	OpenItems  []threadChainOpenItemDoc `json:"open_items"`
	Flags      []json.RawMessage        `json:"flags"`
	Unresolved []json.RawMessage        `json:"unresolved"`
}

// threadChainShowDoc is this row's own minimal decode of `a2a show --json`
// for the one field its step-3 propagation assertion needs: the response
// artifact's own `thread` field, read back after a real PR round trip —
// never internal/cache.ShowResult (same reasoning as subfamShowDoc).
type threadChainShowDoc struct {
	Thread string `json:"thread"`
}

// threadChainReadJSON runs `a2a thread <ref> --json` from c and decodes it.
func threadChainReadJSON(ctx context.Context, c *checkout, ref string) (threadChainDoc, error) {
	stdout, stderr, err := c.Run(ctx, "thread", ref, "--json")
	if err != nil {
		return threadChainDoc{}, fmt.Errorf("livee2e: a2a thread %s (%s): %w: %s", ref, c.System, err, strings.TrimSpace(stderr))
	}
	var doc threadChainDoc
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		return threadChainDoc{}, fmt.Errorf("livee2e: decode `a2a thread %s --json` output (%s): %w", ref, c.System, jerr)
	}
	return doc, nil
}

// threadChainShowThread runs `a2a show <ref> --json` from c and returns the
// artifact's own `thread` field.
func threadChainShowThread(ctx context.Context, c *checkout, ref string) (string, error) {
	stdout, stderr, err := c.Run(ctx, "show", ref, "--json")
	if err != nil {
		return "", fmt.Errorf("livee2e: a2a show %s (%s): %w: %s", ref, c.System, err, strings.TrimSpace(stderr))
	}
	var doc threadChainShowDoc
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		return "", fmt.Errorf("livee2e: decode `a2a show %s --json` output (%s): %w", ref, c.System, jerr)
	}
	return doc.Thread, nil
}

// threadChainMemberIDs returns arts' ids, sorted — the "same set of member
// artifact ids" comparison compares THIS, not the raw (order-sensitive)
// artifacts array.
func threadChainMemberIDs(arts []threadChainArtifactDoc) []string {
	out := make([]string, 0, len(arts))
	for _, a := range arts {
		out = append(out, a.ID)
	}
	sort.Strings(out)
	return out
}

// threadChainTranscriptSignature reduces entries to a stable,
// order-preserving signature string — "artifact:<id>" or
// "event:<transition>:<subject>" per entry — so a caller can compare two
// transcripts for "same content, same order" without depending on any field
// (seq, timestamp) that is legitimately allowed to differ in representation
// between two independently-computed reads of the SAME underlying commits.
func threadChainTranscriptSignature(entries []threadChainEntryDoc) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		switch {
		case e.Kind == "artifact" && e.Artifact != nil:
			out = append(out, "artifact:"+e.Artifact.ID)
		case e.Kind == "event" && e.Event != nil:
			out = append(out, "event:"+e.Event.Transition+":"+e.Event.Subject)
		default:
			out = append(out, "malformed:"+e.Kind)
		}
	}
	return out
}

// threadChainOpenItem finds the open item for id, if any.
func threadChainOpenItem(items []threadChainOpenItemDoc, id string) (threadChainOpenItemDoc, bool) {
	for _, oi := range items {
		if oi.ID == id {
			return oi, true
		}
	}
	return threadChainOpenItemDoc{}, false
}

// threadChainRun drives this row's whole chain. It reuses
// scenarios_submitted_live.go's own helpers (h.DraftAndSubmit,
// happyLandAndSync, subfamInboxContains, subfamAwaitGreenAndLand,
// operationArtifactID) rather than re-deriving the submit -> ack ->
// respond sequence — this is the SAME spine subfamResponseLifecycle already
// walks, up to and including "B responds".
//
// The dual `a2a thread --json` read happens BETWEEN "B's response lands"
// and "A verifies" — not after, which matters: D-024 auto-closes the parent
// question in the SAME PR as `a2a verify` when it is the response's only
// sibling (subfamResponseLifecycle's own doc comment), and cache's own
// buildOpenItems (threadview.go) omits any member whose state is not "open"
// (isOpen, cache/types.go). Reading after verify would leave BOTH the
// parent (closed) and the response (verified) terminal, so open_items would
// be empty on BOTH sides and `your_move` would be false-false everywhere —
// the very assertion this row exists to make would be structurally
// unsatisfiable.
//
// Reading here, while the response artifact is still `submitted`, IS the
// window where exactly one side's your_move differs: fold's own
// responseRows (internal/fold/table.go) make verify/dispute RoleOwner-only,
// resolved against the PARENT's own envelope (internal/fold/legality.go's
// TVerify/TDispute branch) — i.e. A, and only A. This row therefore reads
// `your_move` off the RESPONSE artifact's own open item, not the parent
// question's: the question's own open item ALSO carries a legal `respond`
// at StateResponded (exchangeRows' multi-response reopening row, RoleTarget
// resolved against the question's own `to`, i.e. B) alongside `close`
// (RoleOwner=A) — so BOTH sides would read your_move=true on the parent,
// which is not the "exactly one true, one false" this row's brief demands.
// The response artifact's own open item has no such second claimant.
func threadChainRun(ctx context.Context, h *harness) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return threadChainResultFromErr("sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}

	parent, err := h.DraftAndSubmit(ctx, a, "question")
	if err != nil {
		return threadChainResultFromErr("draft-submit", err, "draft+submit a question opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, parent.PRNumber); err != nil {
		return threadChainResultFromErr("publish-land-sync", err, "the submitted question lands on main and reaches A's mirror")
	}

	// --- the counterpart actually sees it ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return threadChainResultFromErr("counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed question")
	}
	seen, inboxOut, err := subfamInboxContains(ctx, b, parent.ID)
	if err != nil {
		return threadChainResultFromErr("counterpart-sees-it", err, "B's a2a inbox succeeds after sync")
	}
	if !seen {
		return threadChainFail("counterpart-sees-it", inboxOut,
			fmt.Sprintf("B's `a2a inbox --json` lists %s after sync", parent.ID), inboxOut)
	}

	// --- B acknowledges: submitted -> acknowledged ---
	if _, stderr, err := b.Run(ctx, "ack", parent.ID); err != nil {
		return threadChainResultFromErr("counterpart-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack succeeds and opens its own PR")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", parent.ID))
	if err != nil {
		return threadChainResultFromErr("counterpart-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return threadChainResultFromErr("ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- B responds: draft+submit collapsed (D-026) into one PR that
	// authors BOTH the new XS response artifact and the parent's own
	// `respond` event under D6's opaque semantic-operation branch.
	// subfamAwaitGreenAndLand, not happyLandAndSync, at this
	// step — scenarios_submitted_live.go's own top-of-file KNOWN RISK
	// comment on `a2a respond`'s unfilled `to:` field is exactly why. ---
	respondKey, respondBranch := respondOperation(b.System, parent.ID, "answered")
	if _, stderr, err := b.Run(ctx, respondCommandArgs(parent.ID, "answered")...); err != nil {
		return threadChainResultFromErr("counterpart-respond", fmt.Errorf("%w: %s", err, stderr), "B's a2a respond succeeds and opens its own PR")
	}
	respondPR, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		return threadChainResultFromErr("counterpart-respond", err, "respond's semantic-operation branch has a PR")
	}
	responseID, err := operationArtifactID(respondPR.Body, respondKey, parent.ID, "XS-")
	if err != nil {
		return threadChainResultFromErr("counterpart-respond", err, "respond's PR metadata names the new XS response artifact")
	}
	if err := subfamAwaitGreenAndLand(ctx, h, b, respondPR.Number); err != nil {
		return threadChainResultFromErr("respond-land-sync", err, "the response lands on main and reaches B's mirror (required check concludes success)")
	}

	// --- both sides synced immediately before the dual read: a lagging
	// mirror on EITHER side would make the "same member set"/"same order"
	// comparisons below fail for staleness, not for a real defect. ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return threadChainResultFromErr("owner-sync-before-read", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's response before the dual thread read")
	}
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return threadChainResultFromErr("counterpart-sync-before-read", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync is current before the dual thread read")
	}

	// --- THE assertion: both sides read `a2a thread --json` on the SAME
	// ref and must see one identical conversation. ---
	docA, err := threadChainReadJSON(ctx, a, parent.ID)
	if err != nil {
		return threadChainResultFromErr("thread-read-a", err, "A's `a2a thread --json` succeeds")
	}
	docB, err := threadChainReadJSON(ctx, b, parent.ID)
	if err != nil {
		return threadChainResultFromErr("thread-read-b", err, "B's `a2a thread --json` succeeds")
	}

	if docA.Thread == "" || docB.Thread != docA.Thread {
		return threadChainFail("same-thread-id", fmt.Sprintf("A=%q B=%q", docA.Thread, docB.Thread),
			"both sides resolve the SAME non-empty thread id", fmt.Sprintf("A=%q B=%q", docA.Thread, docB.Thread))
	}

	idsA, idsB := threadChainMemberIDs(docA.Artifacts), threadChainMemberIDs(docB.Artifacts)
	joinedA, joinedB := strings.Join(idsA, ","), strings.Join(idsB, ",")
	if joinedA != joinedB {
		return threadChainFail("same-member-set", fmt.Sprintf("A={%s} B={%s}", joinedA, joinedB),
			"both sides render the SAME set of member artifact ids", fmt.Sprintf("A={%s} B={%s}", joinedA, joinedB))
	}
	if len(idsA) < 2 {
		return threadChainFail("same-member-set", fmt.Sprintf("%d member(s): {%s}", len(idsA), joinedA),
			"the thread carries at least the question and its response as members", joinedA)
	}

	if docA.Order != docB.Order {
		return threadChainFail("same-order", fmt.Sprintf("A order=%q B order=%q", docA.Order, docB.Order),
			"both sides compute the transcript under the SAME ordering guarantee", fmt.Sprintf("A order=%q B order=%q", docA.Order, docB.Order))
	}
	sigA := strings.Join(threadChainTranscriptSignature(docA.Transcript), "|")
	sigB := strings.Join(threadChainTranscriptSignature(docB.Transcript), "|")
	if sigA != sigB || sigA == "" {
		return threadChainFail("same-order", fmt.Sprintf("A=[%s] B=[%s]", sigA, sigB),
			"both sides render the transcript in the SAME order", fmt.Sprintf("A=[%s] B=[%s]", sigA, sigB))
	}

	if len(docA.Flags) != 0 || len(docB.Flags) != 0 {
		return threadChainFail("flags-empty", fmt.Sprintf("A flags=%d B flags=%d", len(docA.Flags), len(docB.Flags)),
			"a healthy chain carries no flags on either side", fmt.Sprintf("A=%s B=%s", docA.Flags, docB.Flags))
	}
	if len(docA.Unresolved) != 0 || len(docB.Unresolved) != 0 {
		return threadChainFail("unresolved-empty", fmt.Sprintf("A unresolved=%d B unresolved=%d", len(docA.Unresolved), len(docB.Unresolved)),
			"a healthy chain carries no unresolved facts on either side", fmt.Sprintf("A=%s B=%s", docA.Unresolved, docB.Unresolved))
	}

	// --- your_move must disagree, in the correct direction, on the
	// RESPONSE's own open item (the only unambiguous one — see this
	// function's own doc comment). ---
	oiA, okA := threadChainOpenItem(docA.OpenItems, responseID)
	oiB, okB := threadChainOpenItem(docB.OpenItems, responseID)
	if !okA || !okB {
		return threadChainFail("your-move-disagrees", fmt.Sprintf("A found=%v B found=%v", okA, okB),
			fmt.Sprintf("both sides carry an open_items entry for the still-unverified response %s", responseID),
			fmt.Sprintf("A open_items=%v B open_items=%v", docA.OpenItems, docB.OpenItems))
	}
	if !oiA.YourMove || oiB.YourMove {
		return threadChainFail("your-move-disagrees", fmt.Sprintf("A your_move=%v B your_move=%v", oiA.YourMove, oiB.YourMove),
			"A (the response's own verifier, RoleOwner against the parent's envelope) reads your_move=true and B reads your_move=false",
			fmt.Sprintf("A your_move=%v B your_move=%v", oiA.YourMove, oiB.YourMove))
	}

	// --- propagation: the response artifact's OWN `thread` field, read
	// back through a real PR round trip, carries the question's thread. ---
	responseThread, err := threadChainShowThread(ctx, a, responseID)
	if err != nil {
		return threadChainResultFromErr("response-carries-thread", err, "a2a show reads the response's own thread field back")
	}
	if responseThread != docA.Thread {
		return threadChainFail("response-carries-thread", responseThread,
			fmt.Sprintf("the response %s carries the question's own thread %q", responseID, docA.Thread), responseThread)
	}

	evidence := fmt.Sprintf(
		"thread=%s; members={%s}; transcript=[%s]; your_move: A=%v B=%v (response %s); response.thread=%q; question PR #%d, ack PR #%d, respond PR #%d",
		docA.Thread, joinedA, sigA, oiA.YourMove, oiB.YourMove, responseID, responseThread,
		parent.PRNumber, ackPR.Number, respondPR.Number,
	)

	// --- finish the chain: A verifies (submitted -> verified), completing
	// the full spine this row's brief names. Driven AFTER every assertion
	// above, deliberately — see this function's own doc comment. ---
	// rules-that-reach-2026-08 P1: verify refuses at SUBMIT when verdicts[] does
	// not name every criterion the parent declares (REF-023), and the PR number
	// comes from the verb's own output because --verdict switches the funnel's
	// dedup key to operation.Verify's content-derived one — the branch stops
	// carrying the response id. Same shape as the submitted-family scenario.
	verifyOut, stderr, err := a.Run(ctx, "verify", responseID, "--verdict", "0:met:"+a.System)
	if err != nil {
		return threadChainResultFromErr("owner-verify", fmt.Errorf("%w: %s", err, stderr), "A's a2a verify succeeds and opens its own PR")
	}
	verifyPRNumber, err := prNumberFromVerbOutput(verifyOut)
	if err != nil {
		return threadChainResultFromErr("owner-verify", err, "verify's own PR number is readable from its success line")
	}
	verifyPR := branchPull{Number: verifyPRNumber}
	if err := happyLandAndSync(ctx, h, a, verifyPR.Number); err != nil {
		return threadChainResultFromErr("verify-land-sync", err, "the verify (+ D-024 auto-close) lands on main and reaches A's mirror")
	}

	return Result{
		Scenario: threadChainScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       fmt.Sprintf("verify PR #%d", verifyPR.Number),
		PassEvidence: evidence,
	}
}
