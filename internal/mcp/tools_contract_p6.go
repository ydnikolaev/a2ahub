package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// ContractPublicationRequest is the MCP transport's filesystem-free request.
// cmd/a2a owns the adapter that selects/freeze the rooted candidate and calls
// the shared space publication service.
type ContractPublicationRequest struct {
	Space      string
	ID         string
	Version    string
	Bump       string
	Staging    string
	ExpectPlan string
	Actor      ActorInput
}

// ContractPublicationOperations performs publication planning and writes.
type ContractPublicationOperations interface {
	Preflight(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error)
	Publish(context.Context, ContractPublicationRequest) (space.ContractPublicationResult, error)
}

// ContractMaterializeRequest selects an exported contract tree and destination.
type ContractMaterializeRequest struct {
	Space       string
	Ref         string
	Destination string
}

// ContractMaterializeOperation materializes an exported contract tree.
type ContractMaterializeOperation interface {
	MaterializeContract(context.Context, ContractMaterializeRequest) (space.ContractMaterializeResult, error)
}

// ContractCheckRequest selects contract conformance-check inputs.
type ContractCheckRequest struct {
	Space       string
	Ref         string
	PayloadPath string
	SchemaPath  string
	Suite       bool
}

// ContractCheckOperation evaluates contract conformance.
type ContractCheckOperation interface {
	CheckContract(context.Context, ContractCheckRequest) (contract.ConformanceResult, error)
}

// ContractDiffRequest selects two contract versions to compare.
type ContractDiffRequest struct {
	Space string
	ID    string
	V1    string
	V2    string
}

// ContractDiffResult lists changed paths between contract versions.
type ContractDiffResult struct {
	Added              []string `json:"added"`
	Removed            []string `json:"removed"`
	Changed            []string `json:"changed"`
	FrontmatterChanged []string `json:"frontmatter_changed,omitempty"`
}

// ContractVerifyExportRequest selects a local export and expected source ref.
type ContractVerifyExportRequest struct {
	Space string
	Local string
	Ref   string
}

// ContractVerifyExportResult reports whether an export matches its source.
type ContractVerifyExportResult struct {
	ID          string             `json:"id"`
	Matches     bool               `json:"matches"`
	LocalDigest string             `json:"local_digest"`
	WantDigest  string             `json:"want_digest"`
	Diff        ContractDiffResult `json:"diff,omitempty"`
}

// ContractInspectionOperations exposes read-only contract inspection operations.
type ContractInspectionOperations interface {
	DiffContract(context.Context, ContractDiffRequest) (ContractDiffResult, error)
	VerifyContractExport(context.Context, ContractVerifyExportRequest) (ContractVerifyExportResult, error)
}

// ContractPreflightInput is a preflight handler's structured MCP input.
type ContractPreflightInput struct {
	Space   string `json:"space,omitempty"`
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Bump    string `json:"bump,omitempty"`
	Staging string `json:"staging,omitempty"`
}

// ContractMaterializeInput is a materialize handler's structured MCP input.
type ContractMaterializeInput struct {
	Space string `json:"space,omitempty"`
	Ref   string `json:"ref"`
	To    string `json:"to"`
}

// ContractCheckInput is a check handler's structured MCP input.
type ContractCheckInput struct {
	Space   string `json:"space,omitempty"`
	Ref     string `json:"ref"`
	Mode    string `json:"mode"`
	Payload string `json:"payload,omitempty"`
	Schema  string `json:"schema,omitempty"`
}

func newContractPreflightHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		if deps.Publication == nil {
			return nil, "", fmt.Errorf("contract preflight: P6 service is not configured")
		}
		var in ContractPreflightInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract preflight: invalid input: %w", err)
		}
		if err := validateContractPublicationInput(in.ID, in.Version, in.Bump); err != nil {
			return nil, "", fmt.Errorf("contract preflight: %w", err)
		}
		result, err := deps.Publication.Preflight(ctx, ContractPublicationRequest{Space: in.Space, ID: in.ID, Version: in.Version, Bump: in.Bump, Staging: in.Staging})
		if err != nil {
			return nil, "", fmt.Errorf("contract preflight: %w", err)
		}
		return result, fmt.Sprintf("%s %s@%s", result.Status, result.Plan.Contract, result.Plan.TargetVersion), nil
	}
}

func newP6ContractPublishHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractPublishInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract publish: invalid input: %w", err)
		}
		if err := validateContractPublicationInput(in.ID, in.Version, in.Bump); err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		if in.GeneratedFromDigest != "" {
			return nil, "", fmt.Errorf("contract publish: generated_from.source_digest is finalized by the shared publication planner")
		}
		result, err := deps.Publication.Publish(ctx, ContractPublicationRequest{
			Space: in.Space, ID: in.ID, Version: in.Version, Bump: in.Bump, Staging: in.Staging,
			ExpectPlan: in.ExpectPlan, Actor: in.Actor,
		})
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		return result, fmt.Sprintf("%s %s@%s", result.Status, result.Plan.Contract, result.Plan.TargetVersion), nil
	}
}

func validateContractPublicationInput(id, version, bump string) error {
	if id == "" || (version == "") == (bump == "") {
		return fmt.Errorf("id and exactly one of version or bump are required")
	}
	if bump != "" && bump != "major" && bump != "minor" && bump != "patch" {
		return fmt.Errorf("bump must be major, minor, or patch")
	}
	return nil
}

func newContractMaterializeHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		if deps.Materialize == nil {
			return nil, "", fmt.Errorf("contract materialize: P6 service is not configured")
		}
		var in ContractMaterializeInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract materialize: invalid input: %w", err)
		}
		if !validMCPContractRef(in.Ref) || in.To == "" {
			return nil, "", fmt.Errorf("contract materialize: ref and to are required")
		}
		result, err := deps.Materialize.MaterializeContract(ctx, ContractMaterializeRequest{Space: in.Space, Ref: in.Ref, Destination: in.To})
		if err != nil {
			return nil, "", fmt.Errorf("contract materialize: %w", err)
		}
		return result, fmt.Sprintf("%s %s@%s -> %s", result.Outcome, result.ContractID, result.Version, result.Destination), nil
	}
}

func newContractCheckHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		if deps.Check == nil {
			return nil, "", fmt.Errorf("contract check: P6 service is not configured")
		}
		var in ContractCheckInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract check: invalid input: %w", err)
		}
		suite := in.Mode == string(contract.ConformanceModeSuite)
		payload := in.Mode == string(contract.ConformanceModePayload)
		if !validMCPContractRef(in.Ref) || suite == payload || (payload && in.Payload == "") || (suite && (in.Payload != "" || in.Schema != "")) {
			return nil, "", fmt.Errorf("contract check: ref and exactly one valid mode (payload|suite) are required")
		}
		result, err := deps.Check.CheckContract(ctx, ContractCheckRequest{Space: in.Space, Ref: in.Ref, PayloadPath: in.Payload, SchemaPath: in.Schema, Suite: suite})
		if err != nil {
			return nil, "", fmt.Errorf("contract check: %w", err)
		}
		return result, fmt.Sprintf("%s: %s (passed=%t)", result.Ref, result.Outcome, result.Passed), nil
	}
}

func validMCPContractRef(ref string) bool {
	id, version, found := strings.Cut(ref, "@")
	return found && id != "" && version != "" && !strings.Contains(version, "@")
}
