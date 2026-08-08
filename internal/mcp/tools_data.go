// tools_data.go is the MCP half of spec 05a's contract data exchange loop
// (T1): the grouped `a2a_data` tool, mirroring the shape `a2a_work`
// (tools_work.go) already ships — one tool, one closed action enum, ADR-001
// keeps this package from importing internal/cli, so the request/result
// types below are this package's OWN copies of the internal/cli seam
// (cmd_data.go), not a shared type. cmd/a2a is the single place able to
// import both and wire one production core to both surfaces (the same
// split contract_p6_wiring.go already uses for cliContractP6Adapter /
// mcpContractP6Adapter).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

const (
	// DataToolName is exported for lead-owned registry wiring.
	DataToolName         = "a2a_data"
	maximumDataToolInput = 64 * 1024
)

// DataActions is the closed a2a_data discriminator enum.
//
// DEVIATION, recorded rather than hidden: pack/deliver/fetch/verify are kept
// byte-identical, name-for-name and in order, to cli.DataSubcommands()
// (cmd/a2a/mcp_parity_test.go's TestDataSubcommandsMatchMCPDataActions
// proves it). "attach" is appended after them and breaks that DeepEqual on
// purpose — attach is P4 possession plan 04's wave D, Option A: a
// DESIGNATED TOP-LEVEL CLI verb (`a2a attach`), never a `data` sub-verb, so
// it has no cli.DataSubcommands() row to mirror. The plan's own "MCP
// surface" section still requires it reachable as an a2a_data action
// ("a2a_data gains an attach action, mirroring how deliver is already
// reachable"), and TestMCPParityBijection's verb() (cmd/a2a/
// mcp_parity_test.go, off this wave's allowlist) unconditionally maps every
// a2a_data action to a "data-"-prefixed verb — so (a2a_data, "attach")
// projects to "data-attach", not the bare "attach" the designated verb set
// actually contains, and NEITHER parity test can go green from this file
// alone. Both fixes live in cmd/a2a/mcp_parity_test.go (a verb() case for
// (a2a_data, attach), and TestDataSubcommandsMatchMCPDataActions needs to
// stop asserting a bare DeepEqual against the full list) — reported to the
// lead rather than worked around here.
var DataActions = []string{"pack", "deliver", "fetch", "verify", "attach"}

// DataPackRequest is a2a_data action=pack's resolved input.
type DataPackRequest struct {
	Space       string
	ContractRef string
	From        string
	Profile     string
	Format      string
	Expires     time.Duration
	Fulfills    string
	Supersedes  string
	MaxAttempts int
}

// DataDeliverRequest is a2a_data action=deliver's resolved input.
type DataDeliverRequest struct {
	Space       string
	StagingRoot string
	Fulfills    string
	Supersedes  string
	ExpectPack  string
	Actor       ActorInput
}

// DataFetchRequest is a2a_data action=fetch's resolved input.
type DataFetchRequest struct {
	Space       string
	PackageID   string
	Destination string
}

// DataVerifyRequest is a2a_data action=verify's resolved input.
type DataVerifyRequest struct {
	Space     string
	PackageID string
	// Record performs ONE funnel write carrying the verification-report/v1
	// document AND the lifecycle event on the handoff. Its DIRECTION
	// (verify-pass or verify-fail) is derived from the report's own result
	// by the core — there is no field here to choose it (mirrors
	// cli.DataVerifyRequest's own Record, spec 05a plan D-12): a forged
	// pass must be unrepresentable, not merely refused. There is
	// deliberately no Pass field: alone, verify judges and prints, writing
	// nothing.
	Record bool
	Actor  ActorInput
}

// DataAttachRequest is a2a_data action=attach's resolved input — MCP's own
// copy of cli.AttachRequest-shaped input (ADR-001: no shared Go type).
type DataAttachRequest struct {
	Space        string
	Draft        string
	From         string
	Role         string
	ConformsTo   string
	Verification string
	Retention    string
}

// DataAttachResult is action=attach's own result shape — the resolved
// attachment entry plus the draft path it was written onto. Deliberately
// its own type rather than a DataResult field: DataResult's fields are all
// pointers to internal/datapackage's own manifest/report types, and an
// Attachment is neither.
type DataAttachResult struct {
	Draft      string                               `json:"draft,omitempty"`
	Attachment *datapackage.AttachmentManifestEntry `json:"attachment,omitempty"`
}

// DataAttachOperations is action=attach's own narrow capability,
// deliberately kept SEPARATE from DataOperations below rather than adding
// an Attach method to that interface: DataOperations already has a
// production implementation (cmd/a2a/data_wiring.go's mcpDataAdapter, off
// this wave's allowlist; asserted at data_wiring.go:1294 via
// `var _ mcp.DataOperations = mcpDataAdapter{}`), and widening the required
// method set there would fail that assertion — a compile break in a file
// this wave cannot touch. newDataHandler below type-asserts
// DataOperations against this interface at call time (the same optional-
// capability idiom adapters.go's own skipReporter already uses in this
// package) and reports "not configured" when the concrete ops value does
// not (yet) implement it, exactly like DataToolDeps.Operations == nil
// already does for the whole tool.
type DataAttachOperations interface {
	Attach(context.Context, DataAttachRequest) (DataAttachResult, error)
}

// DataResult mirrors cli.DataResult's own wire shape field-for-field —
// internal/datapackage.Document/Report reused verbatim, never re-typed.
type DataResult struct {
	Manifest  *datapackage.Document `json:"manifest,omitempty"`
	Report    *datapackage.Report   `json:"report,omitempty"`
	Outcome   string                `json:"outcome,omitempty"`
	Write     *space.WriteResult    `json:"write,omitempty"`
	HandoffID string                `json:"handoff_id,omitempty"`
	// StagingRoot is pack's own output — the exact argument action=deliver
	// takes next (mirrors cli.DataResult's own StagingRoot doc comment):
	// without it an MCP caller that packed a delivery would have no way to
	// name the staging root deliver requires, ever again.
	StagingRoot string `json:"staging_root,omitempty"`
}

// DataOperations is the whole data surface behind one interface — the MCP
// transport's own copy of cli.DataOperations (ADR-001: this package cannot
// import internal/cli, so it cannot share the Go type, only the shape).
type DataOperations interface {
	Pack(context.Context, DataPackRequest) (DataResult, error)
	Deliver(context.Context, DataDeliverRequest) (DataResult, error)
	Fetch(context.Context, DataFetchRequest) (DataResult, error)
	Verify(context.Context, DataVerifyRequest) (DataResult, error)
}

// DataToolDeps are a2a_data's construction-time dependencies. Operations
// may be nil (a degraded registration, mirroring registerContractTool's own
// "list the tool, refuse at call time" precedent) — every action-handler
// branch below checks it before dereferencing.
type DataToolDeps struct {
	Operations DataOperations
}

// NewDataTool constructs the closed a2a_data tool. Registration
// (BuildRegistry's own r.Register call) is lead-owned wiring, exactly like
// NewWorkTool's own doc comment says of a2a_work.
func NewDataTool(deps DataToolDeps) (ToolSpec, error) {
	return ToolSpec{
		Name:        DataToolName,
		Description: "contract data exchange loop: action=pack|deliver|fetch|verify",
		InputSchema: dataToolSchema(),
		Handler:     newDataHandler(deps),
	}, nil
}

// DataInput is the a2a_data handler's structured MCP input — the union of
// every action's own fields, exactly as WorkInput/ContractInput already do
// for their own grouped tools.
type DataInput struct {
	Action      string     `json:"action"`
	Space       string     `json:"space,omitempty"`
	Contract    string     `json:"contract,omitempty"`
	From        string     `json:"from,omitempty"`
	Profile     string     `json:"profile,omitempty"`
	Format      string     `json:"format,omitempty"`
	Expires     string     `json:"expires,omitempty"`
	Fulfills    string     `json:"fulfills,omitempty"`
	Supersedes  string     `json:"supersedes,omitempty"`
	MaxAttempts int        `json:"max_attempts,omitempty"`
	StagingRoot string     `json:"staging_root,omitempty"`
	ExpectPack  string     `json:"expect_pack,omitempty"`
	PackageID   string     `json:"package_id,omitempty"`
	To          string     `json:"to,omitempty"`
	Record      bool       `json:"record,omitempty"`
	Actor       ActorInput `json:"actor,omitempty"`
	// Draft/Role/ConformsTo/Verification/Retention are action=attach's own
	// fields (mirrors cli.AttachCommand's own flags: draft, --role,
	// --conforms-to, --verification, --retention).
	Draft        string `json:"draft,omitempty"`
	Role         string `json:"role,omitempty"`
	ConformsTo   string `json:"conforms_to,omitempty"`
	Verification string `json:"verification,omitempty"`
	Retention    string `json:"retention,omitempty"`
}

func newDataHandler(deps DataToolDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in DataInput
		if err := decodeDataInput(args, &in); err != nil {
			return nil, "", err
		}
		if !workStringIn(in.Action, DataActions) {
			return nil, "", fmt.Errorf("a2a_data: action is required and must be one of %s", strings.Join(DataActions, "|"))
		}
		if deps.Operations == nil {
			return nil, "", fmt.Errorf("a2a_data %s: service is not configured", in.Action)
		}

		switch in.Action {
		case "pack":
			if err := dataForbidInput(in, "staging_root", "expect_pack", "package_id", "to", "record", "draft", "role", "conforms_to", "verification", "retention"); err != nil {
				return nil, "", err
			}
			if in.Contract == "" || in.From == "" || in.Profile == "" || in.Format == "" {
				return nil, "", fmt.Errorf("a2a_data pack: contract, from, profile and format are required")
			}
			expires, err := dataDuration(in.Expires, "expires")
			if err != nil {
				return nil, "", err
			}
			result, callErr := deps.Operations.Pack(ctx, DataPackRequest{
				Space: in.Space, ContractRef: in.Contract, From: in.From, Profile: in.Profile, Format: in.Format,
				Expires: expires, Fulfills: in.Fulfills, Supersedes: in.Supersedes, MaxAttempts: in.MaxAttempts,
			})
			return result, dataSummary("pack", result), callErr
		case "deliver":
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "max_attempts", "package_id", "to", "record", "draft", "role", "conforms_to", "verification", "retention"); err != nil {
				return nil, "", err
			}
			if in.StagingRoot == "" || in.Fulfills == "" {
				return nil, "", fmt.Errorf("a2a_data deliver: staging_root and fulfills are required")
			}
			result, callErr := deps.Operations.Deliver(ctx, DataDeliverRequest{
				Space: in.Space, StagingRoot: in.StagingRoot, Fulfills: in.Fulfills, Supersedes: in.Supersedes,
				ExpectPack: in.ExpectPack, Actor: in.Actor,
			})
			return result, dataSummary("deliver", result), callErr
		case "fetch":
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "record", "actor", "draft", "role", "conforms_to", "verification", "retention"); err != nil {
				return nil, "", err
			}
			if in.PackageID == "" || in.To == "" {
				return nil, "", fmt.Errorf("a2a_data fetch: package_id and to are required")
			}
			result, callErr := deps.Operations.Fetch(ctx, DataFetchRequest{Space: in.Space, PackageID: in.PackageID, Destination: in.To})
			return result, dataSummary("fetch", result), callErr
		case "verify":
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "to", "draft", "role", "conforms_to", "verification", "retention"); err != nil {
				return nil, "", err
			}
			if in.PackageID == "" {
				return nil, "", fmt.Errorf("a2a_data verify: package_id is required")
			}
			result, callErr := deps.Operations.Verify(ctx, DataVerifyRequest{Space: in.Space, PackageID: in.PackageID, Record: in.Record, Actor: in.Actor})
			return result, dataSummary("verify", result), callErr
		case "attach":
			if err := dataForbidInput(in, "contract", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "package_id", "to", "record", "actor"); err != nil {
				return nil, "", err
			}
			if in.Draft == "" || in.From == "" || in.Verification == "" {
				return nil, "", fmt.Errorf("a2a_data attach: draft, from and verification are required")
			}
			attacher, ok := deps.Operations.(DataAttachOperations)
			if !ok {
				return nil, "", fmt.Errorf("a2a_data attach: service is not configured")
			}
			result, callErr := attacher.Attach(ctx, DataAttachRequest{
				Space: in.Space, Draft: in.Draft, From: in.From, Role: in.Role,
				ConformsTo: in.ConformsTo, Verification: in.Verification, Retention: in.Retention,
			})
			return result, dataAttachSummary(result), callErr
		default:
			panic("unreachable closed a2a_data action")
		}
	}
}

// dataForbidInput mirrors workForbidInput's own idiom (tools_work.go): a
// closed action must not silently accept another action's fields.
func dataForbidInput(in DataInput, fields ...string) error {
	for _, field := range fields {
		present := false
		switch field {
		case "space":
			present = in.Space != ""
		case "contract":
			present = in.Contract != ""
		case "from":
			present = in.From != ""
		case "profile":
			present = in.Profile != ""
		case "format":
			present = in.Format != ""
		case "expires":
			present = in.Expires != ""
		case "fulfills":
			present = in.Fulfills != ""
		case "supersedes":
			present = in.Supersedes != ""
		case "max_attempts":
			present = in.MaxAttempts != 0
		case "staging_root":
			present = in.StagingRoot != ""
		case "expect_pack":
			present = in.ExpectPack != ""
		case "package_id":
			present = in.PackageID != ""
		case "to":
			present = in.To != ""
		case "record":
			present = in.Record
		case "actor":
			present = in.Actor != (ActorInput{})
		case "draft":
			present = in.Draft != ""
		case "role":
			present = in.Role != ""
		case "conforms_to":
			present = in.ConformsTo != ""
		case "verification":
			present = in.Verification != ""
		case "retention":
			present = in.Retention != ""
		default:
			panic("unknown a2a_data forbidden field " + field)
		}
		if present {
			return fmt.Errorf("a2a_data %s: %s is forbidden for this action", in.Action, field)
		}
	}
	return nil
}

func decodeDataInput(raw json.RawMessage, out *DataInput) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > maximumDataToolInput {
		return fmt.Errorf("a2a_data: input exceeds %d bytes", maximumDataToolInput)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("a2a_data: invalid input: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("a2a_data: invalid input: multiple JSON values")
		}
		return fmt.Errorf("a2a_data: invalid input: %w", err)
	}
	return nil
}

func dataDuration(raw, field string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("a2a_data: %s must be a Go duration: %w", field, err)
	}
	return value, nil
}

// dataSummary builds a2a_data's one-line body summary per action, mirroring
// the CLI's own text-mode rendering (cmd_data.go's renderDataPackText/
// renderDataDeliverText) closely enough that both surfaces read the same
// result the same way, without sharing a render function across the
// ADR-001 boundary.
func dataSummary(action string, result DataResult) string {
	switch action {
	case "pack":
		if result.Manifest == nil {
			return ""
		}
		return fmt.Sprintf("packed %s (%s) attempt %d", result.Manifest.ID, result.Manifest.Contract, result.Manifest.Attempt)
	case "deliver":
		id := ""
		if result.Manifest != nil {
			id = result.Manifest.ID
		}
		return fmt.Sprintf("delivered %s handoff %s", id, result.HandoffID)
	case "fetch":
		id := ""
		if result.Manifest != nil {
			id = result.Manifest.ID
		}
		return fmt.Sprintf("%s %s", result.Outcome, id)
	case "verify":
		if result.Report == nil {
			return ""
		}
		return fmt.Sprintf("%s verify %s: %s", result.Report.ID, result.Report.Package.ID, result.Report.Result())
	default:
		return ""
	}
}

// dataAttachSummary is dataSummary's own sibling for action=attach's
// distinct result type (DataAttachResult, not DataResult) — mirrors
// cmd_attach.go's own text-mode line ("attach: <digest> -> <draft>").
func dataAttachSummary(result DataAttachResult) string {
	if result.Attachment == nil {
		return ""
	}
	return fmt.Sprintf("attached %s -> %s", result.Attachment.Digest, result.Draft)
}

func dataToolSchema() json.RawMessage {
	return groupedSchema("action", DataActions, map[string]string{
		"space": "string", "contract": "string", "from": "string", "profile": "string",
		"format": "string", "expires": "string", "fulfills": "string", "supersedes": "string",
		"max_attempts": "integer", "staging_root": "string", "expect_pack": "string",
		"package_id": "string", "to": "string", "record": "boolean", "actor": "object",
		"draft": "string", "role": "string", "conforms_to": "string",
		"verification": "string", "retention": "string",
	})
}
