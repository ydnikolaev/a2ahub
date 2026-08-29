package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/skillcoverage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// init registers ContractVerifyPublishedResult — defined in the sibling
// cmd_contract_verify_published.go, same package, no edit to that file
// needed — with internal/skillcoverage's shared surface registry (spec
// answers-that-hold-2026-08 03 §11's 2026-08-29 amendment: "THE SECOND
// TRAP", confirmed live at HEAD: cmd/a2a/catalog.go:265-273's
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
func init() {
	skillcoverage.Register("cli-contract-verify-published", reflect.TypeOf(ContractVerifyPublishedResult{}))
}

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
// source. Matches is retained for cmd_contract.go's existing plain-text
// render (wave-2-owned) and for internal/e2e's fixture — it is DERIVED,
// never a second computation: true iff Outcome == "matched". Outcome
// carries the full three-outcome vocabulary (contract.ExportVerification,
// D9-mapped at the render boundary — see cmd/a2a's contractExportOutcomeWord),
// so a consumer that reads it can distinguish drifted from UNMEASURED,
// which Matches alone cannot.
type ContractVerifyExportResult struct {
	ID          string             `json:"id"`
	Matches     bool               `json:"matches"`
	Outcome     string             `json:"outcome"`
	LocalDigest string             `json:"local_digest"`
	WantDigest  string             `json:"want_digest"`
	Diff        ContractDiffResult `json:"diff,omitempty"`
}

// MarshalJSON omits "matches" entirely when Outcome is "unmeasured"
// (answers-that-hold-2026-08 P3 AC-9, carrying spec P2's own AC-6): a run
// with nothing to compare against must not emit `"matches":false`, which
// reads identically to a genuine drift a caller can act on. matched/
// drifted keep the field — Matches stays their derived, retained
// convenience. Marshaled via a type alias plus a shadowing outer field,
// never a hand-typed copy of the other four fields: the exact class this
// phase's render ledger exists to prevent (encoding/json resolves a
// same-JSON-name conflict in favor of the SHALLOWER field, so the embedded
// alias's own "matches" is silently replaced, not duplicated).
//
// internal/skillcoverage.SurfaceKeys never invokes MarshalJSON (it reads
// struct tags only — see its own doc comment) — its derived field set for
// this type still includes "matches" unconditionally, which stays a
// correct OVER-approximation (a key this type CAN put on the wire, on some
// outcomes) rather than a wrong one.
func (r ContractVerifyExportResult) MarshalJSON() ([]byte, error) {
	type alias ContractVerifyExportResult
	if r.Outcome != "unmeasured" {
		return json.Marshal(alias(r))
	}
	return json.Marshal(struct {
		alias
		Matches *bool `json:"matches,omitempty"`
	}{alias: alias(r)})
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

// SetP6Inspection wires the optional read-only P6 inspection service.
func (c *ContractCommand) SetP6Inspection(inspection ContractInspectionOperations) {
	c.inspection = inspection
}

func (c *ContractCommand) runPreflight(ctx context.Context, args []string, stdio IO) int {
	if c.publication == nil {
		return contractServiceUnavailable(stdio, "preflight")
	}
	req, asJSON, ok := parseContractPublicationArgs("preflight", args, stdio, false)
	if !ok {
		return 2
	}
	result, err := c.publication.Preflight(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract preflight: %v\n", err)
		return 1
	}
	return renderContractPublication(stdio, result, asJSON)
}

func (c *ContractCommand) runP6Publish(ctx context.Context, args []string, stdio IO) int {
	req, asJSON, ok := parseContractPublicationArgs("publish", args, stdio, true)
	if !ok {
		return 2
	}
	result, err := c.publication.Publish(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contract publish: %v\n", err)
		return 1
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

func parseContractPublicationArgs(verb string, args []string, stdio IO, allowExpect bool) (ContractPublicationRequest, bool, bool) {
	fs := flag.NewFlagSet("contract "+verb, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "explicit semver")
	bump := fs.String("bump", "", "major|minor|patch")
	staging := fs.String("staging", "", "contained project-relative candidate directory")
	expectPlan := fs.String("expect-plan", "", "expected sha256 plan digest")
	allowEmptyBump := fs.Bool("allow-empty-bump", false, "acknowledge and proceed past a bump whose mutations touch no normative artifact")
	asJSON := fs.Bool("json", false, "emit JSON")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return ContractPublicationRequest{}, false, false
	}
	if len(positionals) != 1 || (*version == "") == (*bump == "") {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a contract %s <id> [--version <semver>|--bump major|minor|patch] [--staging <dir>]", verb)
		if allowExpect {
			_, _ = fmt.Fprint(stdio.Stderr, " [--expect-plan <sha256:...>]")
		}
		_, _ = fmt.Fprintln(stdio.Stderr, " [--json]")
		if line, ok := contractPublicationWorkflowLines["contract "+verb]; ok {
			_, _ = fmt.Fprintln(stdio.Stderr, line)
		}
		return ContractPublicationRequest{}, false, false
	}
	if !allowExpect && *expectPlan != "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract preflight: --expect-plan is only valid for publish")
		return ContractPublicationRequest{}, false, false
	}
	if *bump != "" && *bump != "major" && *bump != "minor" && *bump != "patch" {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract "+verb+": --bump must be major, minor, or patch")
		return ContractPublicationRequest{}, false, false
	}
	return ContractPublicationRequest{
		ID: positionals[0], Version: *version, Bump: *bump, Staging: *staging,
		ExpectPlan: *expectPlan, AllowEmptyBump: *allowEmptyBump,
		Actor: ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel},
	}, *asJSON, true
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
