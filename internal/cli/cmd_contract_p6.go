package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// ContractPublicationRequest is the transport-owned, filesystem-free input
// passed to cmd/a2a's adapter. The adapter selects and freezes the candidate
// source before calling the shared space publication service.
type ContractPublicationRequest struct {
	ID         string
	Version    string
	Bump       string
	Staging    string
	ExpectPlan string
	Actor      ActorFlags
}

// ContractPublicationOperations is the consumer-side seam over P6's shared
// publication service. CLI owns flags and rendering only.
type ContractPublicationOperations interface {
	Preflight(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error)
	Publish(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error)
}

type ContractMaterializeRequest struct {
	Ref         string
	Destination string
}

type ContractMaterializeOperation interface {
	MaterializeContract(context.Context, ContractMaterializeRequest) (space.ContractMaterializeResult, error)
}

type ContractCheckRequest struct {
	Ref         string
	PayloadPath string
	SchemaPath  string
	Suite       bool
}

type ContractCheckOperation interface {
	CheckContract(context.Context, ContractCheckRequest) (contract.ConformanceResult, error)
}

type ContractDiffRequest struct {
	ID string
	V1 string
	V2 string
}

type ContractDiffResult struct {
	Added              []string `json:"added"`
	Removed            []string `json:"removed"`
	Changed            []string `json:"changed"`
	FrontmatterChanged []string `json:"frontmatter_changed"`
}

type ContractVerifyExportRequest struct {
	Local string
	Ref   string
}

type ContractVerifyExportResult struct {
	ID          string             `json:"id"`
	Matches     bool               `json:"matches"`
	LocalDigest string             `json:"local_digest"`
	WantDigest  string             `json:"want_digest"`
	Diff        ContractDiffResult `json:"diff,omitempty"`
}

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

func parseContractPublicationArgs(verb string, args []string, stdio IO, allowExpect bool) (ContractPublicationRequest, bool, bool) {
	fs := flag.NewFlagSet("contract "+verb, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	version := fs.String("version", "", "explicit semver")
	bump := fs.String("bump", "", "major|minor|patch")
	staging := fs.String("staging", "", "contained project-relative candidate directory")
	expectPlan := fs.String("expect-plan", "", "expected sha256 plan digest")
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
		ExpectPlan: *expectPlan, Actor: ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel},
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
