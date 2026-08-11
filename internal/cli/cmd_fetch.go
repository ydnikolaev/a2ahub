// cmd_fetch.go implements `a2a fetch` — P10 (agent-exchange-2026-08) spec 10
// §3 seam 6: a designated TOP-LEVEL verb (mirroring cmd_attach.go's own
// placement decision), NOT a `data` sub-verb, because it resolves ANY
// blob-shaped attachment (a `BL-` id) whether or not it was contract-pinned
// — the general case `a2a data fetch` (a `DP-` package's own manifest-bound
// fetch, unchanged by this wave — spec 10 AC5) does not cover. Spec 10 §6
// (the design note this phase implements): "`a2a fetch <BL-id> --to <dir>`
// works for any attachment, contract-pinned or not".
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// FetchRequest is `a2a fetch <BL-id> --to <dir>`'s resolved input.
type FetchRequest struct {
	Ref         string
	Destination string
}

// FetchResult is what FetchOperations reports back — Outcome mirrors
// DataResult's own "installed"|"already-present" string
// (datapackage.InstallOutcome, cmd_data.go's own precedent), never a
// second, independently-named enum.
type FetchResult struct {
	Ref         string `json:"ref"`
	Destination string `json:"destination"`
	Outcome     string `json:"outcome"`
}

// FetchOperations is the one seam `a2a fetch` needs: resolve ref through
// the space (space.ResolveBlob, already digest-verified — see this
// command's own Run doc comment for why it does not re-verify) and install
// the result at destination.
type FetchOperations interface {
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
}

// FetchCommand implements `a2a fetch`.
type FetchCommand struct {
	ops FetchOperations
}

// NewFetchCommand constructs the fetch command. ops may be nil (e.g. a
// degraded/offline registration, or the catalog's own nil-dep construction,
// cmd/a2a/catalog.go) — Run reports "service is not configured" rather than
// dereferencing it, matching DataCommand's own nil-ops precedent
// (cmd_data.go).
func NewFetchCommand(ops FetchOperations) *FetchCommand {
	return &FetchCommand{ops: ops}
}

// Name implements cli.Command.
func (c *FetchCommand) Name() string { return "fetch" }

// Synopsis implements cli.Command.
func (c *FetchCommand) Synopsis() string {
	return "fetch any attachment by its blob id, contract-pinned or not: fetch <BL-id> --to <dir>"
}

const fetchUsage = "usage: a2a fetch <BL-id> --to <dir> [--json]"

// Run implements cli.Command. Exit codes: 2 = usage (missing ref/--to, a
// ref that is not even shaped like a BL- id, malformed flags); 1 = the
// service is unavailable or the space refused (not found, digest mismatch,
// a ceiling, an install-time containment/divergence refusal); 0 = success.
//
// ResolveBlob (internal/space) already verifies the digest, unconditionally,
// before this command ever sees the bytes (its own doc comment) — so this
// command does NOT re-verify; it writes FetchOperations' returned bytes out
// byte-identically via the SAME install path `a2a data fetch` uses
// (datapackage.InstallLocalFiles, cmd/a2a's own wiring), which is this
// repo's one precedent for what "--to already exists" does: accept a byte-
// identical destination as AlreadyPresent, refuse a divergent one — never a
// blanket "--to must be empty" check this command would otherwise have to
// invent a second time.
func (c *FetchCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) != 0 && IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, fetchUsage)
		return 0
	}
	if c.ops == nil {
		return fetchServiceUnavailable(stdio)
	}

	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	to := fs.String("to", "", "destination directory")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || !validBlobRef(positionals[0]) || *to == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, fetchUsage)
		return 2
	}

	result, ferr := c.ops.Fetch(ctx, FetchRequest{Ref: positionals[0], Destination: *to})
	if ferr != nil {
		return fetchRefuse(stdio, ferr, *asJSON)
	}
	if *asJSON {
		return fetchEncodeJSON(stdio, result)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "%s %s -> %s\n", result.Outcome, result.Ref, result.Destination)
	return 0
}

var _ Command = (*FetchCommand)(nil)

// validBlobRef is a CLI-level usage check only (rejects an obviously wrong
// token before ever calling the space) — space.ResolveBlob (called
// downstream, inside the FetchOperations implementation) is the actual
// authority on whether ref is a well-formed BL- id, mirroring
// validDataPackageRef's own doc comment (cmd_data.go).
func validBlobRef(ref string) bool {
	return strings.HasPrefix(ref, datapackage.BlobPrefix+"-")
}

func fetchServiceUnavailable(stdio IO) int {
	_, _ = fmt.Fprintln(stdio.Stderr, "fetch: service is not configured")
	return 1
}

// fetchErrorEnvelope mirrors dataErrorEnvelope's own --json refusal shape
// (cmd_data.go).
type fetchErrorEnvelope struct {
	Error string `json:"error"`
}

// fetchRefuse mirrors dataRefuse's own both-modes rendering (cmd_data.go).
func fetchRefuse(stdio IO, err error, asJSON bool) int {
	if asJSON {
		if encErr := json.NewEncoder(stdio.Stdout).Encode(fetchErrorEnvelope{Error: err.Error()}); encErr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "fetch: encode error: %v\n", encErr)
		}
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "fetch: %v\n", err)
	return 1
}

func fetchEncodeJSON(stdio IO, result FetchResult) int {
	if err := json.NewEncoder(stdio.Stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "fetch: encode result: %v\n", err)
		return 1
	}
	return 0
}
