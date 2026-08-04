// Spec 05a (contract data exchange loop) T1 CLI surface: `a2a data
// pack|deliver|fetch|verify`. One bare switch fanning out to sub-verbs, a
// single narrow operation interface as a struct field — the same shape
// cmd_contract.go/cmd_contract_p6.go already ship for `contract`
// preflight/publish/materialize/check, mirrored here per this wave's own
// brief ("contract is the exact shipped shape to mirror").
//
// This file and cmd/a2a's own dataCore-shaped adapter are the two sides of
// one wave's seam: DataPackRequest/DataDeliverRequest/DataFetchRequest/
// DataVerifyRequest/DataResult/DataOperations below are declared once here
// and never redeclared — cmd/a2a compiles an adapter against them, this
// file never reaches into internal/datapackage or internal/space to build
// a request itself. internal/datapackage.Document/Report are reused
// verbatim as DataResult's own wire shapes (never re-typed) — the same
// "no second wire shape" discipline every other verb in this package
// already follows for space.ContractPublicationResult and friends.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// --- The seam: both agents in this wave code against these declarations --

// DataPackRequest is "a2a data pack" resolved input.
type DataPackRequest struct {
	ContractRef string // <XC-id>@<version>, as typed
	From        string // local source directory
	Profile     string // synthetic | sanitized
	Format      string // json | ndjson
	Expires     time.Duration
	Fulfills    string // the work_request this delivery answers
	Supersedes  string // prior package id, empty for a first attempt
	MaxAttempts int    // 0 = unset = nothing is refused (L-1 shipped default)
}

// DataDeliverRequest is "a2a data deliver" resolved input.
type DataDeliverRequest struct {
	StagingRoot string // the packed staging root produced by pack
	Fulfills    string
	Supersedes  string
	ExpectPack  string // aggregate digest the caller expects, empty to skip the binding
	Actor       ActorFlags
}

// DataFetchRequest is "a2a data fetch" resolved input.
type DataFetchRequest struct {
	PackageID   string
	Destination string
}

// DataVerifyRequest is "a2a data verify" resolved input.
type DataVerifyRequest struct {
	PackageID string
	// Record performs ONE funnel write carrying the verification-report/v1
	// document AND the lifecycle event on the handoff. Its DIRECTION
	// (verify-pass or verify-fail) is derived from the report's own result
	// by the core — there is no field here to choose it (spec 05a plan
	// D-12): a forged pass must be unrepresentable, not merely refused.
	Record bool
	Actor  ActorFlags
}

// DataResult is what every data verb returns to the surface. The JSON
// payload and the human lines are both derived from it, never assembled
// twice. JSON tags are additive metadata only — they change no field name
// or method signature the seam declares, and exist so `--json` emits the
// same lower_snake_case shape every other wire type in this package uses,
// rather than Go's bare exported field names.
type DataResult struct {
	Manifest  *datapackage.Document `json:"manifest,omitempty"`   // pack, deliver, fetch: the package involved
	Report    *datapackage.Report   `json:"report,omitempty"`     // verify: the verdict
	Outcome   string                `json:"outcome,omitempty"`    // fetch: installed | already-present
	Write     *space.WriteResult    `json:"write,omitempty"`      // deliver: branch, pull request, commit
	HandoffID string                `json:"handoff_id,omitempty"` // deliver: the handoff carrying the package
	// StagingRoot is where pack wrote the attempt. It is the exact
	// argument `data deliver` takes next, and it is returned rather than
	// left to be derived: an agent that has to reconstruct a path from a
	// minted id will eventually reconstruct it wrong, and the tool already
	// knows the answer.
	StagingRoot string `json:"staging_root,omitempty"`
}

// DataOperations is the whole data surface behind one interface, so CLI,
// MCP and their tests share one seam rather than four.
type DataOperations interface {
	Pack(ctx context.Context, req DataPackRequest) (DataResult, error)
	Deliver(ctx context.Context, req DataDeliverRequest) (DataResult, error)
	Fetch(ctx context.Context, req DataFetchRequest) (DataResult, error)
	Verify(ctx context.Context, req DataVerifyRequest) (DataResult, error)
}

// --- DataCommand ------------------------------------------------------

// DataCommand implements `a2a data <pack|deliver|fetch|verify>` (spec 05a
// T1).
type DataCommand struct {
	ops DataOperations
}

// NewDataCommand constructs the data command. ops may be nil (e.g. in a
// degraded/offline registration); every sub-verb reports "service is not
// configured" rather than panicking, matching contractServiceUnavailable's
// own precedent.
func NewDataCommand(ops DataOperations) *DataCommand {
	return &DataCommand{ops: ops}
}

// Name implements cli.Command.
func (c *DataCommand) Name() string { return "data" }

// Synopsis implements cli.Command.
func (c *DataCommand) Synopsis() string {
	return "contract data exchange loop: pack | deliver | fetch | verify"
}

// Run implements cli.Command.
func (c *DataCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a data <pack|deliver|fetch|verify> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	if IsHelpArg(sub) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a data <pack|deliver|fetch|verify> ...")
		for _, s := range DataSubcommands() {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %-10s %s\n", s.Name, s.Synopsis)
		}
		return 0
	}
	switch sub {
	case "pack":
		return c.runPack(ctx, rest, stdio)
	case "deliver":
		return c.runDeliver(ctx, rest, stdio)
	case "fetch":
		return c.runFetch(ctx, rest, stdio)
	case "verify":
		return c.runVerify(ctx, rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "data: unknown subcommand %q\n", sub)
		return 2
	}
}

var _ Command = (*DataCommand)(nil)

// defaultPackExpiry is how long a packed attempt stays fetchable when the
// caller does not say.
//
// It is not zero, and that is the whole point: `expires_at` is stamped as
// now + this duration, and `data fetch` refuses an expired package. A zero
// default therefore produced a package that was expired the instant it was
// created — every first-time producer would have shipped one their
// counterpart could never fetch, and the failure would surface on the OTHER
// side of a long feedback cycle. A week comfortably outlives a review round
// without keeping a payload fetchable indefinitely.
const defaultPackExpiry = 168 * time.Hour

// runPack implements `a2a data pack --contract <XC-id>@<version> --from
// <dir> --profile synthetic|sanitized --format json|ndjson [--expires
// <duration>] [--fulfills <id>] [--supersedes <package-id>]
// [--max-attempts <n>] [--json]`.
//
// DEVIATION (recorded, not silent): spec 05a §T1's own flag column for
// `data pack` lists only --contract/--from/--profile/--expires/--json.
// The frozen seam's DataPackRequest also carries Format (§T2.1: a required
// closed enum with no default), Fulfills, Supersedes and MaxAttempts
// (§T2.5 L-1) — none of which this command can synthesize on the
// caller's behalf. --format/--fulfills/--supersedes/--max-attempts are
// therefore added here so every seam field has a way to be set from the
// CLI.
func (c *DataCommand) runPack(ctx context.Context, args []string, stdio IO) int {
	if c.ops == nil {
		return dataServiceUnavailable(stdio, "pack")
	}
	fs := flag.NewFlagSet("data pack", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	contractRef := fs.String("contract", "", "pinned <XC-id>@<version>")
	from := fs.String("from", "", "local source directory")
	profile := fs.String("profile", "", "synthetic|sanitized")
	format := fs.String("format", "", "json|ndjson")
	expires := fs.Duration("expires", defaultPackExpiry, "how long the packed attempt remains fetchable")
	fulfills := fs.String("fulfills", "", "the work_request this delivery will answer")
	supersedes := fs.String("supersedes", "", "prior package id this attempt supersedes, empty for a first attempt")
	maxAttempts := fs.Int("max-attempts", 0, "attempt ceiling; 0 = unset, nothing refused (L-1)")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if *expires <= 0 {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"data pack: --expires must be positive (got %s): expires_at is stamped as now + this duration, and `a2a data fetch` refuses an expired package, so a non-positive value produces an attempt the consumer can never fetch\n",
			*expires)
		return 2
	}
	// Usage shape only — --profile and --format's own enum membership
	// (including the refusal of --profile production) is the CORE's job,
	// not this thin frontend's: ErrProductionProfile and "unknown format"
	// are both named sentinels/messages a caller acts on, and a bare CLI
	// usage error here would swallow that naming (ADR-001 "thin frontend,
	// zero business rules").
	if len(positionals) != 0 || *contractRef == "" || *from == "" || *profile == "" || *format == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a data pack --contract <XC-id>@<version> --from <dir> --profile synthetic|sanitized --format json|ndjson [--expires <duration>] [--fulfills <id>] [--supersedes <package-id>] [--max-attempts <n>] [--json]")
		return 2
	}
	result, err := c.ops.Pack(ctx, DataPackRequest{
		ContractRef: *contractRef, From: *from, Profile: *profile, Format: *format,
		Expires: *expires, Fulfills: *fulfills, Supersedes: *supersedes, MaxAttempts: *maxAttempts,
	})
	if err != nil {
		return dataRefuse(stdio, "pack", err, *asJSON)
	}
	if *asJSON {
		return encodeDataJSON(stdio, "pack", result)
	}
	renderDataPackText(stdio, result)
	return 0
}

// runDeliver implements `a2a data deliver <packed-staging-root> --fulfills
// <request-id> [--supersedes <package-id>] [--expect-pack <digest>]
// [--json]`.
func (c *DataCommand) runDeliver(ctx context.Context, args []string, stdio IO) int {
	if c.ops == nil {
		return dataServiceUnavailable(stdio, "deliver")
	}
	fs := flag.NewFlagSet("data deliver", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	fulfills := fs.String("fulfills", "", "the work_request this delivery answers")
	supersedes := fs.String("supersedes", "", "prior package id this attempt supersedes")
	expectPack := fs.String("expect-pack", "", "expected aggregate digest, empty to skip the binding")
	asJSON := fs.Bool("json", false, "emit JSON")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || *fulfills == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a data deliver <packed-staging-root> --fulfills <request-id> [--expect-pack <digest>] [--json]")
		return 2
	}
	if *supersedes != "" {
		// The flag is accepted so this message can exist. Supersession is
		// baked into the manifest at pack time — deliver only ships what pack
		// produced, so honouring it here is impossible and IGNORING it is
		// worse than refusing: an agent that set it would believe the chain
		// was recorded when it was not, and would find out one feedback cycle
		// later.
		_, _ = fmt.Fprintln(stdio.Stderr,
			"data deliver: --supersedes is set at pack time, not here: the supersession chain lives in the manifest deliver ships. Re-run `a2a data pack ... --supersedes <prior-package-id>` and deliver the staging root it prints.")
		return 2
	}
	result, err := c.ops.Deliver(ctx, DataDeliverRequest{
		StagingRoot: positionals[0], Fulfills: *fulfills, Supersedes: *supersedes, ExpectPack: *expectPack,
		Actor: ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel},
	})
	if err != nil {
		return dataRefuse(stdio, "deliver", err, *asJSON)
	}
	if *asJSON {
		return encodeDataJSON(stdio, "deliver", result)
	}
	renderDataDeliverText(stdio, result)
	return 0
}

// runFetch implements `a2a data fetch <DP-package-id> --to <dir>
// [--json]`.
func (c *DataCommand) runFetch(ctx context.Context, args []string, stdio IO) int {
	if c.ops == nil {
		return dataServiceUnavailable(stdio, "fetch")
	}
	fs := flag.NewFlagSet("data fetch", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	to := fs.String("to", "", "destination directory")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || !validDataPackageRef(positionals[0]) || *to == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a data fetch <DP-package-id> --to <dir> [--json]")
		return 2
	}
	result, err := c.ops.Fetch(ctx, DataFetchRequest{PackageID: positionals[0], Destination: *to})
	if err != nil {
		return dataRefuse(stdio, "fetch", err, *asJSON)
	}
	if *asJSON {
		return encodeDataJSON(stdio, "fetch", result)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "%s %s -> %s\n", result.Outcome, positionals[0], *to)
	return 0
}

// runVerify implements `a2a data verify <DP-package-id> [--record]
// [--json]`.
//
// There is deliberately no --pass flag (spec 05a plan D-12): alone, verify
// judges and prints, writing nothing; --record performs the ONE funnel
// write, and its direction is derived from the report's own result by the
// core, never chosen here.
func (c *DataCommand) runVerify(ctx context.Context, args []string, stdio IO) int {
	if c.ops == nil {
		return dataServiceUnavailable(stdio, "verify")
	}
	fs := flag.NewFlagSet("data verify", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	record := fs.Bool("record", false, "perform ONE funnel write carrying the report and the lifecycle event; direction is derived from the report's own result, never chosen")
	asJSON := fs.Bool("json", false, "emit JSON")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || !validDataPackageRef(positionals[0]) {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a data verify <DP-package-id> [--record] [--json]")
		return 2
	}
	result, err := c.ops.Verify(ctx, DataVerifyRequest{
		PackageID: positionals[0], Record: *record,
		Actor: ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel},
	})
	if err != nil {
		return dataRefuse(stdio, "verify", err, *asJSON)
	}
	if *asJSON {
		if code := encodeDataJSON(stdio, "verify", result); code != 0 {
			return code
		}
	} else {
		renderDataVerifyText(stdio, result)
	}
	if result.Report != nil && result.Report.Result() != datapackage.ResultPass {
		return 1
	}
	return 0
}

// --- shared helpers ---------------------------------------------------

func dataServiceUnavailable(stdio IO, verb string) int {
	_, _ = fmt.Fprintf(stdio.Stderr, "data %s: service is not configured\n", verb)
	return 1
}

// dataErrorEnvelope is the --json refusal shape. DEVIATION from
// cmd_contract_p6.go's own precedent (renderContractPublication only
// encodes on success; every contract sub-verb prints a refusal to stderr
// regardless of --json): this wave's own brief requires a refusal to
// "render usefully in BOTH modes: name the offending value and path, and
// the record number when an ndjson record is at fault." The seam returns
// (DataResult, error) with no richer structured-error type, so the honest
// shape available without renegotiating the seam is the error's own
// String() — which internal/datapackage's own error wrapping already
// composes as "<sentinel>: <value/path>" (and, for ndjson,
// "... record <n>: <message>" via describeFailingCheck) — carried through
// verbatim rather than re-parsed or reformatted here.
type dataErrorEnvelope struct {
	Error string `json:"error"`
}

// dataRefuse renders a refusal in both text and --json: the text always
// goes to stderr (matching every other verb in this package); when --json
// was requested, the SAME error text is also emitted as a small JSON
// envelope to stdout, so a machine parsing --json output still gets the
// full aimed message rather than an empty/absent result on failure.
func dataRefuse(stdio IO, verb string, err error, asJSON bool) int {
	if asJSON {
		if encErr := json.NewEncoder(stdio.Stdout).Encode(dataErrorEnvelope{Error: err.Error()}); encErr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "data %s: encode error: %v\n", verb, encErr)
		}
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "data %s: %v\n", verb, err)
	return 1
}

func encodeDataJSON(stdio IO, verb string, result DataResult) int {
	if err := json.NewEncoder(stdio.Stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "data %s: encode result: %v\n", verb, err)
		return 1
	}
	return 0
}

// validDataPackageRef is a CLI-level usage check only (rejects an obviously
// wrong token before ever calling the core) — datapackage.ParsePackageID
// (called downstream, inside the DataOperations implementation) is the
// actual authority on whether id is a well-formed DP- package id.
func validDataPackageRef(id string) bool {
	return strings.HasPrefix(id, datapackage.PackagePrefix+"-")
}

// renderDataPackText renders `data pack`'s text-mode output: the manifest
// preview + per-entry digest/size/record count spec 05a §T1 asks for.
func renderDataPackText(stdio IO, result DataResult) {
	m := result.Manifest
	if m == nil {
		return
	}
	for _, e := range m.Entries {
		if e.RecordCount != nil {
			_, _ = fmt.Fprintf(stdio.Stdout, "%s  role=%s digest=%s size=%d records=%d\n", e.Path, e.Role, e.Digest, e.SizeBytes, *e.RecordCount)
		} else {
			_, _ = fmt.Fprintf(stdio.Stdout, "%s  role=%s digest=%s size=%d\n", e.Path, e.Role, e.Digest, e.SizeBytes)
		}
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "packed %s (%s) attempt %d entries=%d size=%d records=%d aggregate=%s expires=%s\n",
		m.ID, m.Contract, m.Attempt, len(m.Entries), m.SizeBytes, m.RecordCount, m.AggregateDigest, m.ExpiresAt)
	if result.StagingRoot != "" {
		// The next command's own argument, printed rather than implied.
		_, _ = fmt.Fprintf(stdio.Stdout, "staged at %s\n", result.StagingRoot)
		_, _ = fmt.Fprintf(stdio.Stdout, "next: a2a data deliver %s --fulfills <request-id>\n", result.StagingRoot)
	}
}

func renderDataDeliverText(stdio IO, result DataResult) {
	id := ""
	if result.Manifest != nil {
		id = result.Manifest.ID
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "delivered %s handoff %s\n", id, result.HandoffID)
	if result.Write != nil {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s %s (%s)\n", result.Write.State, result.Write.PRURL, result.Write.Branch)
	}
}

// renderDataVerifyText names every failing entry and rule (AC-5), plus the
// record number when the violation came from an ndjson record, and — when
// --record drove a write — the resulting branch/PR (result.Write), the same
// shape renderDataDeliverText already prints for `data deliver`.
func renderDataVerifyText(stdio IO, result DataResult) {
	r := result.Report
	if r == nil {
		return
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "%s verify %s: %s (%d checks)\n", r.ID, r.Package.ID, r.Result(), len(r.Checks))
	for _, check := range r.Checks {
		if check.Status == datapackage.CheckPass {
			continue
		}
		if len(check.Violations) == 0 {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %s (%s): %s\n", check.Path, check.ID, check.Status)
			continue
		}
		for _, v := range check.Violations {
			if v.Record != nil {
				_, _ = fmt.Fprintf(stdio.Stdout, "  %s (%s) record %d: %s\n", check.Path, check.ID, *v.Record, v.Message)
			} else {
				_, _ = fmt.Fprintf(stdio.Stdout, "  %s (%s): %s\n", check.Path, check.ID, v.Message)
			}
		}
	}
	if result.Write != nil {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s %s (%s)\n", result.Write.State, result.Write.PRURL, result.Write.Branch)
	}
}
