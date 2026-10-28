package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/skillcoverage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// init registers ContractVerifyPublishedResult — defined in the sibling
// cmd_contract_verify_published.go, same package, no edit to that file
// needed — with internal/skillcoverage's shared surface registry (spec
// answers-that-hold-2026-08 03 §11's 2026-08-29 amendment: "THE SECOND
// TRAP", confirmed live at HEAD when this was written: cmd/a2a/catalog.go's
// catalogSurfaces() is a hand-written five-entry map, and P7's new result
// type is registered with NOTHING). skillcoverage.WithRegistered (the ONE
// line cmd/a2a/catalog.go's own catalogSurfaces() must apply — LEAD-OWNED,
// this phase only reports the line) merges every Register()ed surface into
// that map, so a caller of `a2a __catalog --surfaces --json` sees this
// type's field set the same way it already sees cli-inbox/cli-outbox/
// cli-thread/cli-show/mcp-item, DERIVED via skillcoverage.SurfaceKeys —
// never a second, hand-maintained field list.
//
// Deliberately the ONLY contract result type registered here: registering
// ContractDiffResult / ContractVerifyExportResult / space.
// ContractPublicationResult into this SAME shared registry would also
// enroll them in schemas/prose-coverage.yaml's binary-derived universe
// (that file is OFF-LIMITS to this phase — see its own Deviations report),
// reding that gate on ~30 fields nobody could add prose rows for this wave.
// Those three surfaces are governed by scripts/check-render-ledger.sh's
// OWN, independent derivation instead (internal/cli/cmd_contract_p6_test.go's
// TestRenderLedgerSurfaceDump), which never touches this registry or
// cmd/a2a/catalog.go at all.
// NOT an `init`, and the reason is the same one internal/space's
// registerSpaceGit already carries: `.golangci.yml` bans init functions
// (gochecknoinits) and the ban is right — implicit ordering with invisible
// side effects is exactly what a registry populated at import time would be.
// Lazy, idempotent registration keeps the property that matters ("a --json
// arm enrolls itself, and catalog.go needs no edit for the next one") while
// the call that triggers it is visible at the DI root.
var registerRenderSurfacesOnce = sync.OnceFunc(func() {
	skillcoverage.Register("cli-contract-verify-published", reflect.TypeOf(ContractVerifyPublishedResult{}))
})

// RegisterRenderSurfaces enrolls this package's registry-backed --json
// result types with internal/skillcoverage. cmd/a2a's catalogSurfaces()
// calls it before reading the registry; it is safe to call any number of
// times, from any goroutine.
func RegisterRenderSurfaces() { registerRenderSurfacesOnce() }

// ContractPublicationRequest is the transport-owned, filesystem-free input
// passed to cmd/a2a's adapter. The adapter selects and freezes the candidate
// source before calling the shared space publication service.
type ContractPublicationRequest struct {
	ID         string
	Version    string
	Bump       string
	Staging    string
	ExpectPlan string
	// AllowEmptyBump is `--allow-empty-bump`: acknowledges a non-first bump
	// whose mutations touch no normative artifact and proceeds instead of
	// refusing (P2 AC-3/4).
	AllowEmptyBump bool
	Actor          ActorFlags
	// ApprovedGates is the gates the operator approved with --approve, in
	// canonical order; empty when none was given or none is in force. Only
	// `contract publish` sets it, and only after the writer judged the plan's
	// gate under the space's mode (spec local-space-2026-10/04 AC6), so the
	// adapter that builds the SubmitRequest copies it and decides nothing.
	ApprovedGates []string
}

// ContractPublicationOperations is the consumer-side seam over P6's shared
// publication service. CLI owns flags and rendering only.
type ContractPublicationOperations interface {
	Preflight(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error)
	Publish(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error)
}

// ContractMaterializeRequest is the CLI request for an exported contract tree.
type ContractMaterializeRequest struct {
	Ref         string
	Destination string
}

// ContractMaterializeOperation materializes a contract into a rooted destination.
type ContractMaterializeOperation interface {
	MaterializeContract(context.Context, ContractMaterializeRequest) (space.ContractMaterializeResult, error)
}

// ContractCheckRequest is the CLI request for contract conformance checking.
type ContractCheckRequest struct {
	Ref         string
	PayloadPath string
	SchemaPath  string
	Suite       bool
}

// ContractCheckOperation checks a contract using the shared conformance service.
type ContractCheckOperation interface {
	CheckContract(context.Context, ContractCheckRequest) (contract.ConformanceResult, error)
}

// ContractDiffRequest selects two contract versions to compare.
type ContractDiffRequest struct {
	ID string
	V1 string
	V2 string
}

// ContractDiffResult describes paths that differ between two contract versions.
type ContractDiffResult struct {
	Added              []string `json:"added"`
	Removed            []string `json:"removed"`
	Changed            []string `json:"changed"`
	FrontmatterChanged []string `json:"frontmatter_changed"`
}

// ContractVerifyExportRequest selects a local export and its expected source ref.
type ContractVerifyExportRequest struct {
	Local string
	Ref   string
}

// ContractVerifyExportResult reports whether a local export matches its
// source. Outcome carries the full three-outcome vocabulary
// (contract.ExportVerification, D9-mapped at the render boundary — see
// cmd/a2a's contractExportOutcomeWord), so a consumer that reads it can
// distinguish drifted from UNMEASURED.
//
// THERE IS NO `Matches` FIELD, and its removal is this epic's own class
// caught inside the epic. It existed, carried a doc comment calling itself
// "DERIVED, never a second computation: true iff Outcome == matched", and
// was a settable struct field anyway — so internal/e2e's fixture set
// Matches and left Outcome empty, the text render branched on Outcome, and
// `verify-export` reported "digest mismatch" over two byte-identical
// digests. Every word of the explanation was true and the field was still
// a second answerer. No production code ever read it; the `matches` JSON
// key is now computed at the wire boundary by MarshalJSON, where no caller
// can disagree with it.
type ContractVerifyExportResult struct {
	ID          string             `json:"id"`
	Outcome     string             `json:"outcome"`
	LocalDigest string             `json:"local_digest"`
	WantDigest  string             `json:"want_digest"`
	Diff        ContractDiffResult `json:"diff,omitempty"`
}

// MarshalJSON adds the derived "matches" key — true iff Outcome is
// "matched" — and omits it entirely when Outcome is "unmeasured"
// (answers-that-hold-2026-08 P3 AC-9, carrying spec P2's own AC-6): a run
// with nothing to compare against must not emit `"matches":false`, which
// reads identically to a genuine drift a caller can act on.
//
// It is COMPUTED here rather than carried on the struct, which is the
// difference between a convention and a guard: `matches` is a display
// convenience retained from the pre-three-outcome era, Outcome is the
// authoritative vocabulary the CLI actually branches on, and a wire key
// that restates another field must not be settable independently of it.
// internal/mcp's own ContractVerifyExportResult never carried the field at
// all, so this also ends a difference between the two surfaces' shapes.
//
// Marshaled via a type alias plus an outer field rather than a hand-typed
// copy of the other four — the exact class this phase's render ledger
// exists to prevent.
func (r ContractVerifyExportResult) MarshalJSON() ([]byte, error) {
	type alias ContractVerifyExportResult
	// The words are the CONSTANTS' own values, never re-typed literals.
	// This function decides a wire key from Outcome, and Outcome is filled
	// at the cmd/a2a render boundary from contract.ExportVerification — so a
	// bare "matched" here would be a third value-equal definition with no
	// compiler link to the two that produce it, which is the same class one
	// level down from the field this function replaced. cmd/a2a's own
	// contractExportOutcomeWord renders ExportUnmeasured through
	// validate.SeverityUnmeasured (D9); the two are pinned equal by value in
	// contract_p6_wiring_test.go, so naming the contract constant here links
	// to the vocabulary this type actually carries without internal/cli
	// gaining an edge to internal/validate.
	if r.Outcome == string(contract.ExportUnmeasured) {
		return json.Marshal(struct {
			alias
			Matches *bool `json:"matches,omitempty"`
		}{alias: alias(r)})
	}
	matched := r.Outcome == string(contract.ExportMatched)
	return json.Marshal(struct {
		alias
		Matches *bool `json:"matches,omitempty"`
	}{alias: alias(r), Matches: &matched})
}

// ContractInspectionOperations provides read-only contract inspection operations.
type ContractInspectionOperations interface {
	DiffContract(context.Context, ContractDiffRequest) (ContractDiffResult, error)
	VerifyContractExport(context.Context, ContractVerifyExportRequest) (ContractVerifyExportResult, error)
}

// SetP6Operations wires the shared P6 services. Production wiring must supply
// all three; split interfaces keep each adapter small and independently fakeable.
func (c *ContractCommand) SetP6Operations(publication ContractPublicationOperations, materialize ContractMaterializeOperation, check ContractCheckOperation) {
	c.publication = publication
	c.materialize = materialize
	c.check = check
}

// SetHostCapabilities wires the opened host's declared capabilities: publish
// reads ReviewsWrites to decide whether a gated publish needs --approve.
func (c *ContractCommand) SetHostCapabilities(caps host.Capabilities) {
	c.deps.setHostCapabilities(caps)
}

// SetP6Inspection wires the optional read-only P6 inspection service.
func (c *ContractCommand) SetP6Inspection(inspection ContractInspectionOperations) {
	c.inspection = inspection
}

func (c *ContractCommand) runPreflight(ctx context.Context, args []string, stdio IO) int {
	if c.publication == nil {
		return contractServiceUnavailable(stdio, "preflight")
	}
	req, flags, ok := parseContractPublicationArgs("preflight", args, stdio, false, true, false)
	if !ok {
		return 2
	}
	asJSON, verbose := flags.json, flags.verbose
	result, err := c.publication.Preflight(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract preflight: %v\n", err)
		if verbose {
			writeContractPreflightVerboseDetail(stdio.Stderr, result.Plan)
		}
		return 1
	}
	if verbose {
		writeContractPreflightVerboseDetail(stdio.Stderr, result.Plan)
	}
	code := renderContractPublication(stdio, result, asJSON)
	// Spec 04 §T1: in a confirm space the gate line says what the publish will
	// ask for. Plain text only (stdout is the JSON document under --json), and
	// the exit stays 0: the gate is not an objection.
	if !asJSON && code == 0 && c.publishGateNeedsApproval(result.Plan) {
		_, _ = fmt.Fprintf(stdio.Stdout, "gate %s needs --approve in this space\n", result.Plan.Gate)
	}
	return code
}

// publishGateNeedsApproval reports whether plan's gate is in force and this
// space, read at the mirror's tip under the opened host's capabilities, is in
// confirm mode, the one mode where a publish behind a gate needs --approve.
func (c *ContractCommand) publishGateNeedsApproval(plan contract.PublicationPlan) bool {
	if plan.Gate == "" || plan.Gate == contract.GateNone {
		return false
	}
	return c.deps.manifest.EffectiveGates(c.deps.hostReviews) == contract.GatesModeConfirm
}

// writeContractPreflightVerboseDetail is preflight's --verbose channel
// (spec computed-not-listed-2026-08 P4 §8 row 9, AGENTS.md's Error-flow
// rule: "one actionable line + stable exit code on stderr; detail behind
// --verbose"). It writes ONLY to stderr, never to stdout — stdout is
// renderContractPublication's own contract (a pipeline reads it, --json
// included) and --verbose must never perturb it.
func writeContractPreflightVerboseDetail(w io.Writer, plan contract.PublicationPlan) {
	_, _ = fmt.Fprintf(w, "verbose: gate=%s compatibility=%s entries=%d mutations=%d warnings=%d violations=%d\n",
		plan.Gate, plan.Compatibility.Verdict, len(plan.Entries), len(plan.Mutations), len(plan.Warnings), len(plan.Violations))
	for _, violation := range plan.Violations {
		_, _ = fmt.Fprintf(w, "verbose: violation %s %s: %s\n", violation.Code, violation.Path, violation.Message)
	}
}

func (c *ContractCommand) runP6Publish(ctx context.Context, args []string, stdio IO) int {
	req, flags, ok := parseContractPublicationArgs("publish", args, stdio, true, false, true)
	if !ok {
		return 2
	}
	asJSON := flags.json
	if code, refused := gateApproveHasNoEffect(stdio, "contract publish", flags.approve, c.deps.hostReviews); refused {
		return code
	}
	// Only a confirm space asks anything of the operator here. Review (a host
	// that reviews writes) and autonomous leave the publish exactly as it was:
	// the pull request, or nobody, is the gate, and the plan prints its gate
	// line either way. The extra Preflight is therefore paid only where its
	// answer is the thing being judged.
	failed := func(err error) int {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
	}
	if c.deps.manifest.EffectiveGates(c.deps.hostReviews) == contract.GatesModeConfirm {
		planned, err := c.publication.Preflight(ctx, req)
		if err != nil {
			return failed(err)
		}
		var required []string
		if gate := planned.Plan.Gate; gate != "" && gate != contract.GateNone {
			required = []string{string(gate)}
		}
		approved, code, refused := gateJudgeWrite(stdio, "contract publish", contract.GatesModeConfirm, required, flags.approve)
		if refused {
			return code
		}
		req.ApprovedGates = approved
	}
	result, err := c.publication.Publish(ctx, req)
	if err != nil {
		return failed(err)
	}
	return renderContractPublication(stdio, result, asJSON)
}

// contractPublicationWorkflowLines is spec 08's mechanism 2
// (usageworkflow_dump_test.go's own doc comment): parseContractPublicationArgs
// is shared by `contract publish` and `contract preflight`, printed with a
// runtime "usage: a2a contract %s ..." Sprintf, so there is no per-verb
// literal for the AST walk to anchor on. Keyed by the FULL catalogue verb
// name (not the bare `verb` parameter parseContractPublicationArgs itself
// uses) so the walk's attribution matches `a2a __catalog`'s own rows.
var contractPublicationWorkflowLines = map[string]string{
	"contract publish":   workflowLine("loop-contract-change"),
	"contract preflight": workflowLine("loop-contract-change"),
}

// parseContractPublicationArgs is shared by `contract publish` and
// `contract preflight`. allowVerbose registers `--verbose` only for the verb
// that is meant to honour it (P4 row 9: preflight, "the verb you are already
// holding") — the flag is not registered at all for a verb that would parse
// it and then silently ignore it.
func parseContractPublicationArgs(verb string, args []string, stdio IO, allowExpect, allowVerbose, allowApprove bool) (ContractPublicationRequest, contractPublicationFlags, bool) {
	fs := flag.NewFlagSet("contract "+verb, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "explicit semver")
	bump := fs.String("bump", "", "major|minor|patch")
	staging := fs.String("staging", "", "contained project-relative candidate directory")
	expectPlan := fs.String("expect-plan", "", "expected sha256 plan digest")
	allowEmptyBump := fs.Bool("allow-empty-bump", false, "acknowledge and proceed past a bump whose mutations touch no normative artifact")
	asJSON := fs.Bool("json", false, "emit JSON")
	var verboseFlag, approveFlag *bool
	if allowVerbose {
		verboseFlag = fs.Bool("verbose", false, "emit additional detail on stderr")
	}
	if allowApprove {
		approveFlag = fs.Bool("approve", false, "approve the gate this publish sits behind (confirm spaces)")
	}
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return ContractPublicationRequest{}, contractPublicationFlags{}, false
	}
	if len(positionals) != 1 || (*version == "") == (*bump == "") {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a contract %s <id> [--version <semver>|--bump major|minor|patch] [--staging <dir>]", verb)
		if allowExpect {
			_, _ = fmt.Fprint(stdio.Stderr, " [--expect-plan <sha256:...>]")
		}
		if allowVerbose {
			_, _ = fmt.Fprint(stdio.Stderr, " [--verbose]")
		}
		if allowApprove {
			_, _ = fmt.Fprint(stdio.Stderr, " [--approve]")
		}
		_, _ = fmt.Fprintln(stdio.Stderr, " [--json]")
		if line, ok := contractPublicationWorkflowLines["contract "+verb]; ok {
			_, _ = fmt.Fprintln(stdio.Stderr, line)
		}
		return ContractPublicationRequest{}, contractPublicationFlags{}, false
	}
	if !allowExpect && *expectPlan != "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract preflight: --expect-plan is only valid for publish")
		return ContractPublicationRequest{}, contractPublicationFlags{}, false
	}
	if *bump != "" && *bump != "major" && *bump != "minor" && *bump != "patch" {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract "+verb+": --bump must be major, minor, or patch")
		return ContractPublicationRequest{}, contractPublicationFlags{}, false
	}
	flags := contractPublicationFlags{
		json:    *asJSON,
		verbose: verboseFlag != nil && *verboseFlag,
		approve: approveFlag != nil && *approveFlag,
	}
	return ContractPublicationRequest{
		ID: positionals[0], Version: *version, Bump: *bump, Staging: *staging,
		ExpectPlan: *expectPlan, AllowEmptyBump: *allowEmptyBump,
		Actor: ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel},
	}, flags, true
}

// contractPublicationFlags carries the parsed switches that are not part of
// the request the adapter receives.
type contractPublicationFlags struct {
	json, verbose, approve bool
}

func renderContractPublication(stdio IO, result space.ContractPublicationResult, asJSON bool) int {
	if asJSON {
		if err := json.NewEncoder(stdio.Stdout).Encode(result); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contract: encode result: %v\n", err)
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "%s %s@%s\nplan %s\n", result.Status, result.Plan.Contract, result.Plan.TargetVersion, result.Plan.PlanDigest)
	// P4 row 2 (spec computed-not-listed-2026-08/04, US-3): the JSON branch
	// above always carried plan.Gate because it serialises the whole struct;
	// the plain-text branch never printed it, so an operator previewing a
	// publish on the human path had no way to see that G1/G2 would require a
	// decision. Printed whenever a gate is in force (GateNone/"" is the
	// no-decision-required case and stays silent, matching the warnings
	// block below). Deliberately independent of Plan.Violations: a plan can
	// carry a gate with NO violations at all (first publish is always G1),
	// and that combination must still print the gate and exit 0 — coupling
	// this line to the violation check above would refuse correct work.
	if gate := result.Plan.Gate; gate != "" && gate != contract.GateNone {
		_, _ = fmt.Fprintf(stdio.Stdout, "gate %s\n", gate)
	}
	// fb-20260827-47069c (US-2/AC-3): both facts were computed already and
	// reachable only under --json — candidate_source.kind/.location named
	// which tree the plan actually read, and len(mutations) is the cheap
	// no-op-bump signal the report's own author asked for by hand. Printed
	// unconditionally (never behind a flag) so the FIRST command run already
	// carries the signal, matching the report's own "cheapest form" proposal.
	if source := result.Plan.CandidateSource; source.Location != "" {
		_, _ = fmt.Fprintf(stdio.Stdout, "candidate %s %s (%d mutation(s))\n", source.Kind, source.Location, len(result.Plan.Mutations))
	} else if source.Kind != "" {
		_, _ = fmt.Fprintf(stdio.Stdout, "candidate %s (%d mutation(s))\n", source.Kind, len(result.Plan.Mutations))
	}
	// AC-4: --allow-empty-bump's acknowledgement (contract.Finding{Code:
	// "empty-bump-acknowledged", ...}, carried on Plan.Warnings) must be
	// printed when used — under --json it was already visible as part of
	// the encoded result; the plain-text branch used to print only
	// status/contract/version/plan-digest and silently dropped every
	// warning, including this one.
	for _, warning := range result.Plan.Warnings {
		_, _ = fmt.Fprintf(stdio.Stdout, "warning %s: %s\n", warning.Code, warning.Message)
	}
	return 0
}

func (c *ContractCommand) runMaterialize(ctx context.Context, args []string, stdio IO) int {
	if c.materialize == nil {
		return contractServiceUnavailable(stdio, "materialize")
	}
	fs := flag.NewFlagSet("contract materialize", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	destination := fs.String("to", "", "project-relative destination")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || *destination == "" || !validContractRef(positionals[0]) {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract materialize <XC-id>@<version> --to <project-relative-dir> [--json]")
		return 2
	}
	result, err := c.materialize.MaterializeContract(ctx, ContractMaterializeRequest{Ref: positionals[0], Destination: *destination})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract materialize: %v\n", err)
		return 1
	}
	if *asJSON {
		return encodeContractJSON(stdio, result)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "%s %s@%s -> %s\n", result.Outcome, result.ContractID, result.Version, result.Destination)
	return 0
}

func (c *ContractCommand) runCheck(ctx context.Context, args []string, stdio IO) int {
	if c.check == nil {
		return contractServiceUnavailable(stdio, "check")
	}
	fs := flag.NewFlagSet("contract check", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	payload := fs.String("payload", "", "project-relative payload file")
	schemaPath := fs.String("schema", "", "declared schema path")
	suite := fs.Bool("suite", false, "run declared self-suite")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || !validContractRef(positionals[0]) || (*payload == "") == !*suite || (*suite && *schemaPath != "") {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contract check <XC-id>@<version> (--payload <project-relative-file> [--schema <declared-path>] | --suite) [--json]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-first-integration"))
		return 2
	}
	result, err := c.check.CheckContract(ctx, ContractCheckRequest{Ref: positionals[0], PayloadPath: *payload, SchemaPath: *schemaPath, Suite: *suite})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract check: %v\n", err)
		return 1
	}
	if *asJSON {
		if code := encodeContractJSON(stdio, result); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: %s (passed=%t)\n", result.Ref, result.Outcome, result.Passed)
	}
	if !result.Passed {
		return 1
	}
	return 0
}

func validContractRef(ref string) bool {
	id, version, found := strings.Cut(ref, "@")
	return found && id != "" && version != "" && !strings.Contains(version, "@")
}

func encodeContractJSON(stdio IO, value any) int {
	if err := json.NewEncoder(stdio.Stdout).Encode(value); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract: encode result: %v\n", err)
		return 1
	}
	return 0
}

func contractServiceUnavailable(stdio IO, verb string) int {
	_, _ = fmt.Fprintf(stdio.Stderr, "contract %s: P6 service is not configured\n", verb)
	return 1
}
