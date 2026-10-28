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
	"context"
	"encoding/json"
	"fmt"
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
//
// "request" (the express flow's consumer ask) is a real `data` sub-verb, so it
// follows verify, in the order the CLI's sub-verb list gives it.
var DataActions = []string{"pack", "deliver", "fetch", "verify", "request", "attach", "fetch-blob"}

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

// DataFetchBlobRequest is a2a_data action=fetch-blob's resolved input — MCP's
// own copy of cli.FetchRequest-shaped input (ADR-001: no shared Go type).
// P10 (agent-exchange-2026-08) spec 10 §3 seam 6: the MCP twin of the
// designated top-level CLI verb `a2a fetch <BL-id> --to <dir>`. It cannot be
// named plain "fetch" — a2a_data already has a `fetch` action (a DP-
// package's own manifest-bound fetch, spec 05a) — so it is
// "fetch-blob" here and projects to the bare "fetch" verb via a dedicated
// case in cmd/a2a/mcp_parity_test.go's toolAction.verb(), mirroring how
// "attach" already projects to its own bare verb name for the identical
// reason (that function's own doc comment).
type DataFetchBlobRequest struct {
	Space       string
	Ref         string
	Destination string
}

// DataFetchBlobResult is action=fetch-blob's own result shape, mirroring
// cli.FetchResult's own three fields (ADR-001: no shared Go type).
type DataFetchBlobResult struct {
	Ref         string `json:"ref,omitempty"`
	Destination string `json:"destination,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
}

// DataFetchBlobOperations mirrors DataAttachOperations' own optional-
// capability shape (this file's doc comment above) for the same reason:
// added here rather than widening DataOperations, so an existing
// DataOperations implementation that has not yet grown FetchBlob keeps
// compiling, and newDataHandler below reports "not configured" instead of
// failing to build.
type DataFetchBlobOperations interface {
	FetchBlob(context.Context, DataFetchBlobRequest) (DataFetchBlobResult, error)
}

// DataRequestRequest is a2a_data action=request's resolved input — MCP's own
// copy of the express flow's consumer ask (ADR-001: no shared Go type).
type DataRequestRequest struct {
	Space              string
	Producer           string
	Contract           string
	Title              string
	NeededBy           string
	InterimBehavior    string
	AcceptanceCriteria []string
	Timeout            time.Duration
	Actor              ActorInput
}

// DataDeliverForRequest is a2a_data action=deliver with `for` set: the
// producer answers one work_request end to end.
type DataDeliverForRequest struct {
	Space       string
	For         string
	From        string
	Profile     string
	Format      string
	Contract    string
	Expires     time.Duration
	MaxAttempts int
	Timeout     time.Duration
	Actor       ActorInput
}

// DataVerifyForRequest is a2a_data action=verify with `for` set: the consumer
// judges, and with Record closes or disputes, one work_request.
type DataVerifyForRequest struct {
	Space    string
	For      string
	Record   bool
	To       string
	Verdicts []string
	Reason   string
	Timeout  time.Duration
	Actor    ActorInput
}

// DataTransition names the event transition a composite step wrote or found
// already written: one of the closed event.schema.json `transition` values.
// MCP's own copy of cli.DataTransition (ADR-001: no shared Go type); it
// encodes as the bare string, so the tool result is unchanged.
type DataTransition string

// DataStep is one inner step of a composite action, in the order it ran.
type DataStep struct {
	Step       string             `json:"step"`
	Subject    string             `json:"subject,omitempty"`
	Transition DataTransition     `json:"transition,omitempty"`
	Outcome    string             `json:"outcome"`
	Write      *space.WriteResult `json:"write,omitempty"`
	Note       string             `json:"note,omitempty"`
}

// DataExpressResult is what the composite actions return: the ids they
// produced, every inner step in order, and the command the OTHER party runs
// next (empty when nothing is owed).
type DataExpressResult struct {
	WorkRequest string              `json:"work_request,omitempty"`
	Handoff     string              `json:"handoff,omitempty"`
	Package     string              `json:"package,omitempty"`
	Response    string              `json:"response,omitempty"`
	Report      *datapackage.Report `json:"report,omitempty"`
	Steps       []DataStep          `json:"steps"`
	Next        string              `json:"next,omitempty"`
	Error       string              `json:"error,omitempty"`
}

// DataExpressOperations is the composite actions' own optional capability,
// kept SEPARATE from DataOperations for the reason DataAttachOperations is:
// widening the required method set would break every existing implementation.
// newDataHandler type-asserts it at call time and reports "not configured"
// when the concrete ops value does not implement it.
type DataExpressOperations interface {
	Request(context.Context, DataRequestRequest) (DataExpressResult, error)
	DeliverFor(context.Context, DataDeliverForRequest) (DataExpressResult, error)
	VerifyFor(context.Context, DataVerifyForRequest) (DataExpressResult, error)
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
		Name: DataToolName,
		// Derived from DataActions, never repeated. The literal it replaced
		// said "action=pack|deliver|fetch|verify" while DataActions carried
		// six — `attach` had been invisible for a wave and `fetch-blob` would
		// have joined it. This string is the ONLY thing an MCP client reads to
		// learn which actions exist, so a stale copy is not cosmetic: it is an
		// action that ships and cannot be found. Derivation removes the copy
		// rather than correcting it.
		Description: "contract data exchange loop: action=" + strings.Join(DataActions, "|"),
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
	// For selects the express (composite) mode of deliver and verify: the
	// work_request id the call answers. Producer/Title/NeededBy/
	// InterimBehavior/AcceptanceCriteria are action=request's own fields;
	// Verdicts/Reason/Timeout belong to the composite modes.
	For                string   `json:"for,omitempty"`
	Producer           string   `json:"producer,omitempty"`
	Title              string   `json:"title,omitempty"`
	NeededBy           string   `json:"needed_by,omitempty"`
	InterimBehavior    string   `json:"interim_behavior,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Verdicts           []string `json:"verdicts,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	Timeout            string   `json:"timeout,omitempty"`
	// Draft/Role/ConformsTo/Verification/Retention are action=attach's own
	// fields (mirrors cli.AttachCommand's own flags: draft, --role,
	// --conforms-to, --verification, --retention).
	Draft        string `json:"draft,omitempty"`
	Role         string `json:"role,omitempty"`
	ConformsTo   string `json:"conforms_to,omitempty"`
	Verification string `json:"verification,omitempty"`
	Retention    string `json:"retention,omitempty"`
	// Ref is action=fetch-blob's own field (mirrors cli.FetchRequest.Ref):
	// the BL- id to resolve. Destination is shared with action=fetch's own
	// `to` field — both name "where the bytes land on disk", and giving
	// fetch-blob a second field for the identical concept would be a second
	// name for the same thing, not a distinct one.
	Ref string `json:"ref,omitempty"`
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
			if err := dataForbidInput(in, "staging_root", "expect_pack", "package_id", "to", "record", "draft", "role", "conforms_to", "verification", "retention", "ref", "for", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason", "timeout"); err != nil {
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
			if in.For != "" {
				return dataDeliverFor(ctx, deps, in)
			}
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "max_attempts", "package_id", "to", "record", "draft", "role", "conforms_to", "verification", "retention", "ref", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason", "timeout"); err != nil {
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
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "record", "actor", "draft", "role", "conforms_to", "verification", "retention", "ref", "for", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason", "timeout"); err != nil {
				return nil, "", err
			}
			if in.PackageID == "" || in.To == "" {
				return nil, "", fmt.Errorf("a2a_data fetch: package_id and to are required")
			}
			result, callErr := deps.Operations.Fetch(ctx, DataFetchRequest{Space: in.Space, PackageID: in.PackageID, Destination: in.To})
			return result, dataSummary("fetch", result), callErr
		case "verify":
			if in.For != "" {
				return dataVerifyFor(ctx, deps, in)
			}
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "to", "draft", "role", "conforms_to", "verification", "retention", "ref", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason", "timeout"); err != nil {
				return nil, "", err
			}
			if in.PackageID == "" {
				return nil, "", fmt.Errorf("a2a_data verify: package_id is required")
			}
			result, callErr := deps.Operations.Verify(ctx, DataVerifyRequest{Space: in.Space, PackageID: in.PackageID, Record: in.Record, Actor: in.Actor})
			return result, dataSummary("verify", result), callErr
		case "request":
			if err := dataForbidInput(in, "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "package_id", "to", "record", "draft", "role", "conforms_to", "verification", "retention", "ref", "for", "verdicts", "reason"); err != nil {
				return nil, "", err
			}
			if in.Producer == "" || in.Contract == "" || in.InterimBehavior == "" {
				return nil, "", fmt.Errorf("a2a_data request: producer, contract and interim_behavior are required")
			}
			timeout, err := dataTimeout(in.Timeout)
			if err != nil {
				return nil, "", err
			}
			express, ok := deps.Operations.(DataExpressOperations)
			if !ok {
				return nil, "", fmt.Errorf("a2a_data request: service is not configured")
			}
			result, callErr := express.Request(ctx, DataRequestRequest{
				Space: in.Space, Producer: in.Producer, Contract: in.Contract, Title: in.Title, NeededBy: in.NeededBy,
				InterimBehavior: in.InterimBehavior, AcceptanceCriteria: in.AcceptanceCriteria, Timeout: timeout, Actor: in.Actor,
			})
			return result, dataExpressSummary("request", result), callErr
		case "attach":
			if err := dataForbidInput(in, "contract", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "package_id", "to", "record", "actor", "ref", "for", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason", "timeout"); err != nil {
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
		case "fetch-blob":
			if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "package_id", "record", "actor", "draft", "role", "conforms_to", "verification", "retention", "for", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason", "timeout"); err != nil {
				return nil, "", err
			}
			if in.Ref == "" || in.To == "" {
				return nil, "", fmt.Errorf("a2a_data fetch-blob: ref and to are required")
			}
			fetcher, ok := deps.Operations.(DataFetchBlobOperations)
			if !ok {
				return nil, "", fmt.Errorf("a2a_data fetch-blob: service is not configured")
			}
			result, callErr := fetcher.FetchBlob(ctx, DataFetchBlobRequest{Space: in.Space, Ref: in.Ref, Destination: in.To})
			return result, dataFetchBlobSummary(result), callErr
		default:
			panic("unreachable closed a2a_data action")
		}
	}
}

// dataDeliverFor is action=deliver's express mode (`for` set): `for` derives
// the staging root, the request being fulfilled and the supersession chain, so
// accepting any of them beside it would let a caller believe it had overridden
// what the action derives.
func dataDeliverFor(ctx context.Context, deps DataToolDeps, in DataInput) (any, string, error) {
	if err := dataForbidInput(in, "staging_root", "fulfills", "supersedes", "expect_pack", "package_id", "to", "record", "draft", "role", "conforms_to", "verification", "retention", "ref", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria", "verdicts", "reason"); err != nil {
		return nil, "", err
	}
	if in.From == "" || in.Profile == "" || in.Format == "" {
		return nil, "", fmt.Errorf("a2a_data deliver: with for, from, profile and format are required")
	}
	expires, err := dataDuration(in.Expires, "expires")
	if err != nil {
		return nil, "", err
	}
	timeout, err := dataTimeout(in.Timeout)
	if err != nil {
		return nil, "", err
	}
	express, ok := deps.Operations.(DataExpressOperations)
	if !ok {
		return nil, "", fmt.Errorf("a2a_data deliver: service is not configured")
	}
	result, callErr := express.DeliverFor(ctx, DataDeliverForRequest{
		Space: in.Space, For: in.For, From: in.From, Profile: in.Profile, Format: in.Format, Contract: in.Contract,
		Expires: expires, MaxAttempts: in.MaxAttempts, Timeout: timeout, Actor: in.Actor,
	})
	return result, dataExpressSummary("deliver", result), callErr
}

// dataVerifyFor is action=verify's express mode (`for` set): `for` finds the
// latest handoff itself, so a package id beside it is refused.
func dataVerifyFor(ctx context.Context, deps DataToolDeps, in DataInput) (any, string, error) {
	if err := dataForbidInput(in, "contract", "from", "profile", "format", "expires", "fulfills", "supersedes", "max_attempts", "staging_root", "expect_pack", "package_id", "draft", "role", "conforms_to", "verification", "retention", "ref", "producer", "title", "needed_by", "interim_behavior", "acceptance_criteria"); err != nil {
		return nil, "", err
	}
	timeout, err := dataTimeout(in.Timeout)
	if err != nil {
		return nil, "", err
	}
	express, ok := deps.Operations.(DataExpressOperations)
	if !ok {
		return nil, "", fmt.Errorf("a2a_data verify: service is not configured")
	}
	result, callErr := express.VerifyFor(ctx, DataVerifyForRequest{
		Space: in.Space, For: in.For, Record: in.Record, To: in.To, Verdicts: in.Verdicts, Reason: in.Reason, Timeout: timeout, Actor: in.Actor,
	})
	return result, dataExpressSummary("verify", result), callErr
}

// dataTimeout parses the composite actions' wait bound; empty means the
// operation's own default (zero). An explicit value must be positive: "0s"
// is refused rather than silently read as the default.
func dataTimeout(raw string) (time.Duration, error) {
	value, err := dataDuration(raw, "timeout")
	if err != nil {
		return 0, err
	}
	if raw != "" && value <= 0 {
		return 0, fmt.Errorf("a2a_data: timeout must be a positive Go duration")
	}
	return value, nil
}

// dataExpressSummary is the composite actions' one-line body: one line per
// inner step, then the report verdict and the command the other party runs.
func dataExpressSummary(action string, result DataExpressResult) string {
	var lines []string
	for _, step := range result.Steps {
		line := step.Step + " " + step.Outcome
		if step.Subject != "" {
			line += " " + step.Subject
		}
		lines = append(lines, line)
	}
	if result.Report != nil {
		lines = append(lines, fmt.Sprintf("%s verify %s: %s", result.Report.ID, result.Report.Package.ID, result.Report.Result()))
	}
	if result.Next != "" {
		lines = append(lines, "next: "+result.Next)
	}
	if len(lines) == 0 {
		return action
	}
	return strings.Join(lines, "\n")
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
		case "ref":
			present = in.Ref != ""
		case "for":
			present = in.For != ""
		case "producer":
			present = in.Producer != ""
		case "title":
			present = in.Title != ""
		case "needed_by":
			present = in.NeededBy != ""
		case "interim_behavior":
			present = in.InterimBehavior != ""
		case "acceptance_criteria":
			present = len(in.AcceptanceCriteria) != 0
		case "verdicts":
			present = len(in.Verdicts) != 0
		case "reason":
			present = in.Reason != ""
		case "timeout":
			present = in.Timeout != ""
		default:
			panic("unknown a2a_data forbidden field " + field)
		}
		if present {
			return fmt.Errorf("a2a_data %s: %s is forbidden for this action", in.Action, field)
		}
	}
	return nil
}

// decodeDataInput is a2a_data's own thin wrapper over the package's ONE
// decode helper (tools.go's decodeStrict, agent-exchange-2026-08 spec 04
// D4) — see tools_work.go's decodeWorkInput for the full reasoning: this
// function and that one used to each carry their OWN byte-identical copy
// of decodeStrict's body, which is exactly the duplication ADR-019
// forbids. Both now call through to the one helper.
func decodeDataInput(raw json.RawMessage, out *DataInput) error {
	return decodeStrict(raw, out, "a2a_data", maximumDataToolInput)
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

// dataFetchBlobSummary is dataSummary's own sibling for action=fetch-blob's
// distinct result type (DataFetchBlobResult, not DataResult) — mirrors
// cli.FetchCommand's own text-mode line ("<outcome> <ref> -> <destination>",
// cmd_fetch.go).
func dataFetchBlobSummary(result DataFetchBlobResult) string {
	if result.Outcome == "" {
		return ""
	}
	return fmt.Sprintf("%s %s -> %s", result.Outcome, result.Ref, result.Destination)
}

func dataToolSchema() json.RawMessage {
	return groupedSchema("action", "the data-package action to run: "+strings.Join(DataActions, "|"), DataActions, map[string]propSpec{
		"space":               {"string", "the connected space this call targets"},
		"contract":            {"string", "pack only: the contract ref (id@version) this package's payload conforms to"},
		"from":                {"string", "pack only: the local path or manifest this package is built from"},
		"profile":             {"string", "pack only: the named profile controlling what this package includes"},
		"format":              {"string", "pack only: the package format to produce"},
		"expires":             {"string", "pack only: a Go duration string after which this package's staged artifact expires"},
		"fulfills":            {"string", "deliver only: the request id this delivery fulfills"},
		"supersedes":          {"string", "deliver only: a prior delivery this one supersedes"},
		"max_attempts":        {"integer", "fetch only: the maximum retry attempts before giving up"},
		"staging_root":        {"string", "pack/deliver only: the staging directory root to write into"},
		"expect_pack":         {"string", "deliver only: the package id this delivery must match (idempotency guard)"},
		"package_id":          {"string", "deliver/fetch/verify only: the data package id (DP-...) this call targets"},
		"to":                  {"string", "fetch/fetch-blob only: the local destination path to fetch into"},
		"record":              {"boolean", "verify only: whether to record this verification's own verdict"},
		"actor":               {"object", "the identity performing this action, when it differs from this session's default"},
		"draft":               {"string", "attach only: the draft id this attachment belongs to"},
		"role":                {"string", "attach only: the attachment's own role classification"},
		"conforms_to":         {"string", "attach only: the schema/contract ref this attachment conforms to"},
		"verification":        {"string", "attach only: this attachment's own verification classification"},
		"retention":           {"string", "attach only: this attachment's own retention classification"},
		"ref":                 {"string", "fetch-blob only: the blob ref (BL-...) to resolve"},
		"for":                 {"string", "deliver/verify only: the work_request id (XW-...) to answer end to end; selects the express mode"},
		"producer":            {"string", "request only: the system asked to deliver the data"},
		"title":               {"string", "request only: the request title (default derived from the contract ref)"},
		"needed_by":           {"string", "request only: YYYY-MM-DD the data is needed by"},
		"interim_behavior":    {"string", "request only: what the consumer does while it waits"},
		"acceptance_criteria": {"array", "request only: extra acceptance criterion texts; ac1 is always the verify criterion"},
		"verdicts":            {"array", "verify with for only: <index-or-criterion-id>:<met|unmet|not_warranted|not_exercised>:<cause_owner> entries passed through to close"},
		"reason":              {"string", "verify with for only: overrides the derived dispute reason"},
		"timeout":             {"string", "request, and deliver/verify with for: a positive Go duration bounding each wait for a pending merge"},
	})
}
