// ac5_ref023_test.go is P1's AC5 (one-answer-2026-08 plan 01b): the last
// open acceptance criterion the phase's tracker holds it `in-progress` for.
// REF-023 (defects-fix-2026-08 P4, internal/validate/verdicts.go's
// checkVerdictCompleteness) refuses an incomplete `verdicts[]` — fewer
// entries than the parent's own declared acceptance_criteria[] — and, until
// rules-that-reach-2026-08 P1, fired ONLY at the merge-time gate,
// `a2a validate --ci --mode=v3-pr`: SubmitValidatorAdapter.ValidateSubmit
// (both internal/cli/adapters.go and internal/mcp/adapters.go) only ran
// validate.ValidateForSubmit over DRAFT files, never bare event files, so
// `a2a verify --verdict` with an incomplete array SUCCEEDED at submit
// regardless of completeness — verified empirically running this file's own
// first version, which found exactly that before the `validate --ci` step
// was added below.
//
// # rules-that-reach-2026-08 P1 changed this — on ONE surface, then P5
// # closed the other
//
// P1's own events-partition call to validate.ValidateEventWithContext made
// the CLI surface refuse the incomplete case AT SUBMIT (the "cli-authored"
// subtest below asserts the refusal directly, via `run` + exit code +
// REF-023 in stderr, rather than committing it and catching it downstream).
// The MCP surface's own SubmitValidatorAdapter gained the identical call
// site in P1, but internal/mcp.MirrorResolver did not yet implement
// validate.ParentCriteriaCounter/ParentCriteriaIDs/ResponseParentResolver
// the way internal/cli.MirrorResolver did, so checkVerdictIndexRange
// (verdicts.go) still degraded to "cannot check" on that surface — the
// "mcp-authored" subtest below used to assert exactly that gap (a clean
// submit, caught only later at `validate --ci`).
//
// P5 (this phase) moved those three capabilities into internal/cache
// (ADR-004, docs/decisions.md) and gave internal/mcp.MirrorResolver thin
// delegations to them, closing KI-02301-MCP-VERDICT-RESOLVER-GAP (retired,
// releasenotes/current/known-issues.yaml). The "mcp-authored" subtest below
// is now the FLIPPED proof — restructured to mirror "cli-authored"'s own
// shape (refusal AT SUBMIT, nothing committed for the incomplete case) —
// watched failing before P5's fix and green after. AC3 (below) additionally
// asserts the two surfaces' refusal text is byte-identical, which is why
// both subtests now arrange their parent under the SAME id
// (ac5RefusalParentID): the REF-023 message embeds the parent id, so two
// different ids would defeat a byte-for-byte comparison regardless of
// whether the underlying rule fired identically.
//
// This is a T3 row: nothing proves AC1/AC2/AC3 until the BUILT `a2a` binary
// itself — both surfaces cmd/a2a/wire.go wires (`a2a verify`) and
// internal/mcp/wire.go wires (`a2a mcp`'s own stdio JSON-RPC `a2a_exchange`
// verify action) — is driven end to end, at a space floor AT OR ABOVE
// contract.ContractPublicationFloor (wave33Floor, main_test.go's own
// harnessStampedVersion — read from the constant, never a literal: below
// that floor both surfaces fail closed to event/v1, where `verdicts[]`
// cannot exist at all, additionalProperties:false, and a test at "0.0.0"
// would be the B30/B32/B33 shape this epic keeps finding — a gate green
// because it cannot look).
//
// One parent (two declared acceptance_criteria — REF-023 needs count > 0,
// and a single criterion cannot distinguish "empty" from "incomplete": with
// two, naming only one is unambiguously incomplete), arranged directly
// (only the VERIFY event's own author is under test, not the parent's or
// the responses'), two tracked responses per surface — D-024's own
// single-response auto-close convenience must NOT fire, mirroring
// event_v2_binary_test.go's own TestT3CloseAndVerifyVerdictThroughRealBinary
// precedent for the identical reason: a second response keeps `verify` from
// closing the parent in the same PR, so the SECOND verify call is reached
// as a genuinely standalone write, over the SAME two declared criteria as
// the first. The FIRST response is verified with an incomplete verdicts[]
// (names only criterion 0 of 2); the SECOND with a complete one (both).
//
// The two subtests below deliberately do NOT call t.Parallel(): AC3's own
// comparison, at the bottom of the outer test function, reads both
// subtests' captured refusal text after both have run. A t.Parallel()
// subtest only resumes once its PARENT test function has already returned
// — so a parallel sibling cannot hand a value back to code that runs after
// both `t.Run` calls in the same parent. The outer test itself still runs
// parallel to the rest of this package (its own t.Parallel(), below).
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ac5Criteria is the two-criterion acceptance_criteria[] every AC5 fixture
// parent declares.
var ac5Criteria = []string{"first criterion is met", "second criterion is met"}

// ac5RefusalParentID is the ONE parent id both the "cli-authored" and
// "mcp-authored" subtests below arrange (in their own, fully isolated
// hostRigs — separate remote, separate temp dirs, so there is no
// collision) — required for AC3's byte-identical comparison:
// checkVerdictCompleteness's own REF-023 message
// (internal/validate/verdicts.go) embeds the parent id it counted against,
// so two different ids would defeat a literal string-equality assertion
// regardless of whether the rule itself fired identically on both
// surfaces.
const ac5RefusalParentID = "XQ-axon-20260820-ac5s"

// ac5WriteQuestionWithCriteria is writeQuestionArtifact's (helpers_test.go)
// own twin, widened to carry acceptance_criteria[] — base.schema.json
// declares that field at the base level (evaluated into question.schema.json's
// own `unevaluatedProperties: false` via its `allOf: [{$ref: base}]`), so a
// question legally carries it even though no other fixture in this package
// exercises that combination.
func ac5WriteQuestionWithCriteria(t *testing.T, mirrorDir, id, to string, criteria []string) {
	t.Helper()
	var criteriaYAML strings.Builder
	criteriaYAML.WriteString("acceptance_criteria:\n")
	for _, c := range criteria {
		criteriaYAML.WriteString("  - \"" + c + "\"\n")
	}
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: " + hostRigSpaceID + "\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		criteriaYAML.String() +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// ac5SeedAcceptedQuestionWithCriteria is seedAcceptedQuestion's own twin,
// widened to carry acceptance_criteria[] via ac5WriteQuestionWithCriteria.
func ac5SeedAcceptedQuestionWithCriteria(t *testing.T, mirrorDir, id, to string, criteria []string) {
	t.Helper()
	ac5WriteQuestionWithCriteria(t, mirrorDir, id, to, criteria)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, to, 1, id, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, to, 2, id, "accept", to)
}

// ac5ArrangeParent commits a two-criterion, accepted question straight to
// r's own origin main — the "arrange" idiom wave33_verify_test.go and
// event_v2_binary_test.go both already use (writeMirrorFile+gitRun+push):
// only the verify event's own author (CLI vs MCP) is under test here, so
// the parent itself is seeded once, identically, for both.
func ac5ArrangeParent(t *testing.T, r *hostRig, id string) {
	t.Helper()
	arrange := t.TempDir()
	gitRun(t, "", "clone", r.fx.RemoteURL(), arrange)
	ac5SeedAcceptedQuestionWithCriteria(t, arrange, id, "beta", ac5Criteria)
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: "+id+" accepted, 2 acceptance criteria (P1 AC5 fixture)")
	gitRun(t, arrange, "push", "origin", "main")
}

// --- `a2a validate --ci`'s own minimal JSON decode --------------------

// ac5CIViolation narrows validate.Violation to the one field this file
// checks — this package's own established idiom (crosssurface_test.go's
// package doc: "every layer in this repo owns its own minimal decode of
// the same underlying document").
type ac5CIViolation struct {
	Code string `json:"code"`
}

type ac5CIArtifactResult struct {
	Violations []ac5CIViolation `json:"violations"`
}

type ac5CIArtifact struct {
	Path   string               `json:"path"`
	Result *ac5CIArtifactResult `json:"result,omitempty"`
	Error  string               `json:"error,omitempty"`
}

type ac5CIReport struct {
	Mode      string          `json:"mode"`
	Valid     bool            `json:"valid"`
	Artifacts []ac5CIArtifact `json:"artifacts"`
}

// ac5CIHasViolation reports whether rep carries a violation of code against
// the artifact at path — internal/cli/cmd_validate_ci_test.go's own
// ciReportHasViolation, restated here (this package cannot import an
// unexported cli-package helper).
func ac5CIHasViolation(rep ac5CIReport, path, code string) bool {
	for _, a := range rep.Artifacts {
		if a.Path != path || a.Result == nil {
			continue
		}
		for _, v := range a.Result.Violations {
			if v.Code == code {
				return true
			}
		}
	}
	return false
}

// ac5RunValidateCI execs the BUILT `a2a validate --ci` — the T3 tier this
// AC requires — against dir (a real git checkout carrying both base and
// HEAD), decoding its JSON report regardless of exit code (production
// always writes JSON to stdout, even on a non-zero exit).
func ac5RunValidateCI(t *testing.T, dir, mode, base, author string) (code int, rep ac5CIReport, stderr string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "a2a"), "validate", "--ci", "--mode="+mode, "--base="+base, "--author="+author)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	if err != nil {
		exitErr := &exec.ExitError{}
		if !errors.As(err, &exitErr) {
			t.Fatalf("exec a2a validate --ci: %v", err)
		}
		code = exitErr.ExitCode()
	}
	if out.Len() > 0 {
		if derr := json.Unmarshal(out.Bytes(), &rep); derr != nil {
			t.Fatalf("decode ci report: %v\nstdout=%s\nstderr=%s", derr, out.String(), errBuf.String())
		}
	}
	return code, rep, errBuf.String()
}

// --- `a2a mcp`'s own minimal stdio JSON-RPC decode ---------------------

type ac5ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ac5RPCRequest struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      int                `json:"id"`
	Method  string             `json:"method"`
	Params  ac5ToolsCallParams `json:"params"`
}

type ac5ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ac5ToolsCallResult struct {
	Content []ac5ContentBlock `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

type ac5RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ac5RPCResponse struct {
	Result *ac5ToolsCallResult `json:"result,omitempty"`
	Error  *ac5RPCError        `json:"error,omitempty"`
}

// ac5VerdictArg is one `verdicts[]` wire entry a2a_exchange's `verify`
// action reads (mcp.VerifyInput.Verdicts / mcp.VerdictInputEntry's own wire
// shape, restated raw here rather than imported — this file drives the
// tool over stdio exactly as a real MCP client would, never through the
// production Go type).
type ac5VerdictArg struct {
	Index      *int   `json:"index,omitempty"`
	Verdict    string `json:"verdict"`
	CauseOwner string `json:"cause_owner"`
}

func ac5IndexPtr(i int) *int { return &i }

// ac5RunMCPTool execs `a2a mcp` in r's own project (the SAME config/
// credentials/host env hostRig.run already establishes for the CLI surface
// — internal/mcp/wire.go's ResolvePaths reads cwd+HOME exactly like
// cmd/a2a/wire.go's resolvePaths does), sends ONE tools/call request over
// stdio JSON-RPC 2.0, closes stdin (Serve returns on clean EOF), and
// decodes the sole response line.
func (r *hostRig) ac5RunMCPTool(toolName string, arguments any) ac5ToolsCallResult {
	r.t.Helper()
	argsRaw, err := json.Marshal(arguments)
	if err != nil {
		r.t.Fatalf("marshal mcp tool arguments: %v", err)
	}
	reqLine, err := json.Marshal(ac5RPCRequest{
		JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: ac5ToolsCallParams{Name: toolName, Arguments: argsRaw},
	})
	if err != nil {
		r.t.Fatalf("marshal mcp request: %v", err)
	}

	cmd := exec.Command(filepath.Join(binDir, "a2a"), "mcp")
	cmd.Dir = r.projectDir
	cmd.Env = append(os.Environ(),
		"HOME="+r.homeDir,
		"FIXTURE_TOKEN=dummy-token",
		"A2A_GITHUB_API="+r.gh.URL,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.t.Fatalf("mcp stdin pipe: %v", err)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Start(); err != nil {
		r.t.Fatalf("start a2a mcp: %v", err)
	}
	if _, err := stdin.Write(append(reqLine, '\n')); err != nil {
		r.t.Fatalf("write mcp request: %v\nstderr so far=%s", err, errBuf.String())
	}
	if err := stdin.Close(); err != nil {
		r.t.Fatalf("close mcp stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		r.t.Fatalf("a2a mcp: %v\nstdout=%s\nstderr=%s", err, out.String(), errBuf.String())
	}

	line := bytes.TrimSpace(out.Bytes())
	var resp ac5RPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		r.t.Fatalf("decode mcp response: %v\nstdout=%s\nstderr=%s", err, out.String(), errBuf.String())
	}
	if resp.Error != nil {
		r.t.Fatalf("mcp tools/call %s: JSON-RPC error %d: %s\nstderr=%s", toolName, resp.Error.Code, resp.Error.Message, errBuf.String())
	}
	if resp.Result == nil {
		r.t.Fatalf("mcp tools/call %s: response carries no result\nstdout=%s", toolName, out.String())
	}
	return *resp.Result
}

// ac5AssertMCPToolSucceeded fails the test, naming the tool's own error
// text, when result reports isError.
func ac5AssertMCPToolSucceeded(t *testing.T, toolName string, result ac5ToolsCallResult) {
	t.Helper()
	if !result.IsError {
		return
	}
	var msgs []string
	for _, c := range result.Content {
		msgs = append(msgs, c.Text)
	}
	t.Fatalf("mcp tools/call %s failed: %s", toolName, strings.Join(msgs, " | "))
}

// ac5AssertMCPToolFailed is ac5AssertMCPToolSucceeded's own inverse (P5):
// fails the test, naming the tool's own SUCCESS, when result does NOT
// report isError — and returns content[0]'s own text otherwise, the piece a
// caller asserts on for the refusal's own wording (AC1/AC2/AC3 below).
//
// server.go's tools/call error branch (`spec.Handler` returning a non-nil
// err) ALWAYS appends err.Error() as content[0] first, then a SECOND block
// only when the handler's own structured return value is non-nil — which
// this write-funnel path's own submitResult IS whenever
// hasWriteResult(result) is true, even on a failed call (eventdoc.go's own
// `submit`: a partial P4 outcome, e.g. a pushed branch the funnel could not
// validate, still carries a non-zero WriteResult). Measured empirically: a
// REF-023 refusal through `a2a_exchange` verify here carries content[0] =
// the error text and content[1] = that partial result's own JSON — joining
// both, as an earlier version of this helper did, corrupted AC3's own
// byte-identical comparison against the CLI surface's single-line stderr.
func ac5AssertMCPToolFailed(t *testing.T, toolName string, result ac5ToolsCallResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("mcp tools/call %s: succeeded, want a refusal", toolName)
	}
	if len(result.Content) == 0 {
		t.Fatalf("mcp tools/call %s: isError with no content block", toolName)
	}
	return strings.TrimSpace(result.Content[0].Text)
}

// TestP1AC5REF023RefusesAnIncompleteVerifyOnBothSurfaces is P1's AC5
// (REF-023 refuses AT SUBMIT on the CLI surface) widened by P5 into
// rules-that-reach-2026-08's own AC1/AC2/AC3: the same incomplete-
// `verdicts[]` shape internal/cli's own direct-construction proof already
// refuses (TestValidateCI_REF023FiresOnAnIncompleteVerdictsArray,
// cmd_validate_ci_test.go) is proven here at T3 — the BUILT binary, on both
// the CLI surface (`a2a verify`) and the MCP surface (`a2a mcp`'s
// `a2a_exchange` verify action) — at wave33Floor
// (contract.ContractPublicationFloor itself, never a literal), refusing AT
// SUBMIT identically on both, with byte-identical refusal text (AC3).
func TestP1AC5REF023RefusesAnIncompleteVerifyOnBothSurfaces(t *testing.T) {
	t.Parallel()

	var cliRefusalText, mcpRefusalText string

	t.Run("cli-authored", func(t *testing.T) {
		axon := newHostRig(t, "axon", "axon", "beta")
		beta := axon.peer("beta")
		raiseHostRigFloor(t, axon, wave33Floor, "axon", "beta")

		const parentID = ac5RefusalParentID
		ac5ArrangeParent(t, axon, parentID)

		axon.mustRun("sync")
		beta.mustRun("sync")

		r1 := beta.mustRun("respond", "--actor-name", "bot1", "--result", "answered", parentID)
		responseA := extractExchangeID(t, r1)
		axon.mustRun("sync")

		r2 := beta.mustRun("respond", "--actor-name", "bot2", "--result", "answered", parentID)
		responseB := extractExchangeID(t, r2)
		axon.mustRun("sync")

		mirrorDir := hostRigMirrorDir(axon)
		shaAfterResponses := strings.TrimSpace(gitOutput(t, axon.fx.RemoteURL(), "rev-parse", "main"))
		before := listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")

		// rules-that-reach-2026-08 P1: the CLI surface's SubmitValidatorAdapter
		// calls validate.ValidateEventWithContext over every event file in the
		// events partition, so an incomplete verdicts[] is refused AT SUBMIT.
		// `run`, not `mustRun`: the refusal is the assertion, and nothing is
		// committed for it.
		stdout, stderr, code := axon.run("verify", parentID, "--refs", responseA, "--verdict", "0:met:beta")
		if code == 0 {
			t.Fatalf("verify with an incomplete verdicts[] (CLI-authored): exit 0, want a submit-time refusal\nstdout=%s", stdout)
		}
		if !strings.Contains(stderr, "REF-023") {
			t.Fatalf("verify with an incomplete verdicts[] (CLI-authored) refused, but not with REF-023: stderr=%s", stderr)
		}
		cliRefusalText = strings.TrimSpace(stderr)
		axon.mustRun("sync")
		after := listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
		if len(after) != len(before) {
			t.Fatalf("verify refused at submit but the mirror gained event(s) anyway: before=%v after=%v", before, after)
		}

		before = listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
		axon.mustRun("verify", parentID, "--refs", responseB, "--verdict", "0:met:beta", "--verdict", "1:met:beta")
		axon.mustRun("sync")
		after = listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
		completePath := soleAddedPath(t, before, after)

		code2, rep, ciStderr := ac5RunValidateCI(t, mirrorDir, "v3-pr", shaAfterResponses, "axon-bot")
		if code2 != 0 || !rep.Valid {
			t.Fatalf("validate --ci over a complete verdicts[] (CLI-authored): code=%d valid=%v, want an accepted PR\nreport=%+v\nstderr=%s", code2, rep.Valid, rep, ciStderr)
		}
		if ac5CIHasViolation(rep, completePath, "REF-023") {
			t.Fatalf("validate --ci over a complete verdicts[] (CLI-authored) was refused by REF-023: %+v", rep)
		}
	})

	t.Run("mcp-authored", func(t *testing.T) {
		axon := newHostRig(t, "axon", "axon", "beta")
		beta := axon.peer("beta")
		raiseHostRigFloor(t, axon, wave33Floor, "axon", "beta")

		const parentID = ac5RefusalParentID
		ac5ArrangeParent(t, axon, parentID)

		axon.mustRun("sync")
		beta.mustRun("sync")

		r1 := beta.mustRun("respond", "--actor-name", "bot1", "--result", "answered", parentID)
		responseA := extractExchangeID(t, r1)
		axon.mustRun("sync")

		r2 := beta.mustRun("respond", "--actor-name", "bot2", "--result", "answered", parentID)
		responseB := extractExchangeID(t, r2)
		axon.mustRun("sync")

		mirrorDir := hostRigMirrorDir(axon)
		shaAfterResponses := strings.TrimSpace(gitOutput(t, axon.fx.RemoteURL(), "rev-parse", "main"))
		before := listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")

		// P5: internal/mcp.MirrorResolver now carries the same three
		// capabilities internal/cli.MirrorResolver does (ADR-004's shared
		// internal/cache seat), so an incomplete verdicts[] is refused AT
		// SUBMIT on this surface too — the same shape as "cli-authored" above,
		// no longer "succeeds, caught only at validate --ci".
		incompleteResult := axon.ac5RunMCPTool("a2a_exchange", map[string]any{
			"action": "verify", "targets": []string{parentID}, "refs": responseA,
			"verdicts": []ac5VerdictArg{{Index: ac5IndexPtr(0), Verdict: "met", CauseOwner: "beta"}},
		})
		mcpErrText := ac5AssertMCPToolFailed(t, "a2a_exchange (verify, incomplete)", incompleteResult)
		if !strings.Contains(mcpErrText, "REF-023") {
			t.Fatalf("a2a_exchange verify with an incomplete verdicts[] (MCP-authored) failed, but not with REF-023: %s", mcpErrText)
		}
		mcpRefusalText = mcpErrText

		axon.mustRun("sync")
		after := listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
		if len(after) != len(before) {
			t.Fatalf("verify refused at submit but the mirror gained event(s) anyway: before=%v after=%v", before, after)
		}

		before = listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
		completeResult := axon.ac5RunMCPTool("a2a_exchange", map[string]any{
			"action": "verify", "targets": []string{parentID}, "refs": responseB,
			"verdicts": []ac5VerdictArg{
				{Index: ac5IndexPtr(0), Verdict: "met", CauseOwner: "beta"},
				{Index: ac5IndexPtr(1), Verdict: "met", CauseOwner: "beta"},
			},
		})
		ac5AssertMCPToolSucceeded(t, "a2a_exchange (verify, complete)", completeResult)

		axon.mustRun("sync")
		after = listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
		completePath := soleAddedPath(t, before, after)

		code, rep, stderr := ac5RunValidateCI(t, mirrorDir, "v3-pr", shaAfterResponses, "axon-bot")
		if code != 0 || !rep.Valid {
			t.Fatalf("validate --ci over a complete verdicts[] (MCP-authored): code=%d valid=%v, want an accepted PR\nreport=%+v\nstderr=%s", code, rep.Valid, rep, stderr)
		}
		if ac5CIHasViolation(rep, completePath, "REF-023") {
			t.Fatalf("validate --ci over a complete verdicts[] (MCP-authored) was refused by REF-023: %+v", rep)
		}
	})

	// AC3: the refusal text must be byte-identical across surfaces for the
	// identical submission (same parent id, same incomplete verdicts[]
	// shape) — proof that ADR-004's shared internal/cache seat, not two
	// independently-worded adapters, produced this message. Skipped when
	// either subtest above already failed: a mismatch downstream of an
	// already-reported defect adds nothing.
	if t.Failed() {
		return
	}
	if cliRefusalText != mcpRefusalText {
		t.Fatalf("AC3: refusal text differs between surfaces for the identical incomplete verdicts[] submission:\n  cli: %s\n  mcp: %s", cliRefusalText, mcpRefusalText)
	}
	t.Logf("AC3 refusal text, side by side:\n  cli: %s\n  mcp: %s", cliRefusalText, mcpRefusalText)
}
