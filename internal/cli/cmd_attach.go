// P4 (agent-exchange-2026-08) plan 04-possession, wave D: `a2a attach` — a
// designated top-level verb (plan's "The MCP surface" section, Option A),
// NOT a `data` sub-verb. It is the general case cmd_data.go's own header
// already names: "deliver becomes attach + conforms_to + verification:
// required + the handoff" (plan D4). This file never changes any exported
// behaviour of DataCommand/its sub-verbs.
//
// Placement, decided against cmd_new.go and cmd_data.go, the two
// neighbouring verbs read before writing this file: attach operates ONLY on
// a local staged draft plus local source bytes — no space credential, no
// mirror sync, no contract lookup — exactly NewCommand's own shape (mints
// into `.a2a/staging/`, calls a core package directly, no per-space
// Operations indirection), not DataCommand's (whose ops layer exists
// because pack/deliver/fetch/verify each resolve a target SPACE). So this
// command calls internal/datapackage's attach core directly, the way
// NewCommand calls internal/template directly, rather than adding an
// Operations seam cmd/a2a would have to wire per-space for no reason this
// verb has.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"gopkg.in/yaml.v3"
)

// AttachDefaultRetention is the retention `a2a attach` applies when the
// caller does not say — mirrors DefaultDataPackExpiry's own rationale
// (cmd_data.go): an attachment default must not be zero (an attachment
// "pinned" the instant it lapses is not a default anyone wants), and a week
// comfortably outlives a review round. Unlike --expires, this is a plain
// duration STRING (the schema's own attachments[].retention shape,
// work_request.schema.json:101-109 — "pinned" or a Go-duration-shaped
// string), not a flag.Duration: time.Duration(...).String() on this value
// already matches the schema's repeated-group pattern
// (`^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`), so no second format is
// invented for the default alone.
const AttachDefaultRetention = "168h"

// AttachCommand implements `a2a attach`: it reads the draft named on the
// command line, reads the source bytes (a file or a directory) named by
// --from, mints an Attachment via internal/datapackage's pure core, and
// writes the attachments[] entry onto the draft's frontmatter.
//
// verification carries NO default (Run's own flag parsing below): the
// brief's own constraint is that a default silently claiming
// `verification: required`
// on bytes nobody promised to judge is exactly the fused-axes bug this
// phase exists to separate, and defaulting to any OTHER enum member is the
// same silent claim in a different direction. The caller states it or the
// command refuses — there is no third option that is not a guess.
type AttachCommand struct {
	stagingDir string
	bounds     datapackage.Bounds

	now       func() time.Time
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, perm os.FileMode) error
}

// NewAttachCommand constructs the attach command. stagingDir is
// `.a2a/staging/`'s path — a bare draft id on the command line resolves
// against it via resolveSubmitTarget (cmd_submit.go), the same resolution
// `a2a submit <id>` already uses, so an author never has to spell the
// staging path out. bounds is the datapackage.Bounds this verb enforces on
// the source bytes — production wiring passes datapackage.DefaultBounds();
// exposed as a constructor argument (not hardcoded) so a caller can inject
// a narrower bound, the same DI discipline every other core dependency in
// this package already follows (rails anti-pattern #10).
func NewAttachCommand(stagingDir string, bounds datapackage.Bounds) *AttachCommand {
	return &AttachCommand{
		stagingDir: stagingDir,
		bounds:     bounds,
		now:        time.Now,
		readFile:   os.ReadFile,
		writeFile:  os.WriteFile,
	}
}

// Name implements cli.Command.
func (c *AttachCommand) Name() string { return "attach" }

// Synopsis implements cli.Command.
func (c *AttachCommand) Synopsis() string {
	return "attach bytes to a drafted artifact: attach <draft-id-or-path> --from <file-or-dir> --verification required|offered|none [--role <role>] [--conforms-to <XC-id>@<version>] [--retention <duration>|pinned]"
}

const attachUsage = "usage: a2a attach <draft-id-or-path> --from <file-or-dir> --verification required|offered|none [--role <role>] [--conforms-to <XC-id>@<version>] [--retention <duration>|pinned] [--json]"

// Run implements cli.Command. Exit codes: 2 = usage (missing draft/--from/
// --verification, malformed flags); 1 = the draft or source could not be
// read, the draft is not an attachments[]-bearing type/generation, or
// internal/datapackage's core refused (bounds exceeded, malformed
// verification/retention); 0 = success.
func (c *AttachCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) != 0 && IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, attachUsage)
		return 0
	}

	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	from := fs.String("from", "", "local source: a file or a directory")
	role := fs.String("role", "", "attachments[].role (optional)")
	conformsTo := fs.String("conforms-to", "", "attachments[].conforms_to: <XC-id>@<version> (optional)")
	verification := fs.String("verification", "", "attachments[].verification: required|offered|none (no default — see this command's own doc comment)")
	retention := fs.String("retention", AttachDefaultRetention, "attachments[].retention: a Go duration string or \"pinned\"")
	asJSON := fs.Bool("json", false, "emit JSON")
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positionals) != 1 || *from == "" || *verification == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, attachUsage)
		return 2
	}

	draftPath := resolveSubmitTarget(c.stagingDir, positionals[0])
	raw, rerr := c.readFile(draftPath)
	if rerr != nil {
		return attachRefuse(stdio, fmt.Errorf("cannot read draft %s: %w", draftPath, rerr), *asJSON)
	}
	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		return attachRefuse(stdio, fmt.Errorf("draft %s: %w", draftPath, ferr), *asJSON)
	}

	var probe struct {
		Type   string `yaml:"type"`
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return attachRefuse(stdio, fmt.Errorf("draft %s: cannot decode frontmatter: %w", draftPath, err), *asJSON)
	}
	// attachments[] is declared on ONE schema today: envelope/v2/work_request
	// (plan D1/D2 — a new field lands only on a schema that is itself new).
	// Every per-type schema closes with unevaluatedProperties: false, so
	// writing this key onto any other type/generation would pass this
	// command and be refused SCH-003 at the very next `a2a validate` — the
	// exact silent-drift failure class this phase exists to remove. This
	// check is asked here (not deferred to validate) precisely because Q1's
	// re-anchor names "refused locally, before any pull request" as the
	// point of this whole phase.
	if probe.Type != "work_request" || probe.Schema != "envelope/v2" {
		return attachRefuse(stdio, fmt.Errorf(
			"draft %s is type=%q schema=%q: attachments[] is only defined on work_request drafts using envelope/v2 (run `a2a new work_request`, which authors envelope/v2 by default)",
			draftPath, probe.Type, probe.Schema), *asJSON)
	}

	info, serr := os.Stat(*from)
	if serr != nil {
		return attachRefuse(stdio, fmt.Errorf("cannot access source %s: %w", *from, serr), *asJSON)
	}

	var attachment datapackage.Attachment
	if info.IsDir() {
		attachment, err = datapackage.NewAttachmentFromDirectory(ctx, *from, *role, *conformsTo, *verification, *retention, c.bounds)
	} else {
		var data []byte
		data, err = c.readFile(*from)
		if err != nil {
			return attachRefuse(stdio, fmt.Errorf("cannot read source %s: %w", *from, err), *asJSON)
		}
		attachment, err = datapackage.NewAttachmentFromBytes(data, *role, *conformsTo, *verification, *retention, c.bounds)
	}
	if err != nil {
		return attachRefuse(stdio, err, *asJSON)
	}

	// Resolved BEFORE the write, not after: expires_at is part of the entry
	// the draft carries now, so a retention this command cannot resolve must
	// refuse before anything is written rather than leave a draft on disk
	// carrying an entry whose lapse date nobody could compute.
	entry, eerr := datapackage.NewAttachmentManifestEntry(attachment, c.now())
	if eerr != nil {
		return attachRefuse(stdio, fmt.Errorf("draft %s: cannot resolve retention: %w", draftPath, eerr), *asJSON)
	}

	updated, uerr := attachAppendEntry(fm, attachment, entry.ExpiresAt)
	if uerr != nil {
		return attachRefuse(stdio, fmt.Errorf("draft %s: %w", draftPath, uerr), *asJSON)
	}
	if werr := c.writeFile(draftPath, updated, 0o644); werr != nil {
		return attachRefuse(stdio, fmt.Errorf("cannot write %s: %w", draftPath, werr), *asJSON)
	}

	if *asJSON {
		return attachEncodeJSON(stdio, draftPath, entry)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "attach: %s -> %s\n", attachment.Digest, draftPath)
	if entry.ExpiresAt != "" {
		_, _ = fmt.Fprintf(stdio.Stdout, "expires %s\n", entry.ExpiresAt)
	}
	return 0
}

var _ Command = (*AttachCommand)(nil)

// attachAppendEntry decodes raw's frontmatter into a generic map (the same
// shape contractAddFrontmatterFields already uses, cmd_contract.go), reads
// the existing `attachments` array (absent or nil = none yet), appends
// attachment's own fields — via a yaml round-trip of the Attachment struct
// itself, so the entry's keys are the struct's own yaml tags and never
// drift from datapackage.Attachment's shape — and re-serializes.
//
// The entry carries Attachment's own fields (ref, digest, role,
// conforms_to, verification, retention) PLUS the resolved expires_at.
//
// expires_at used to be deliberately omitted, because the schema declared no
// such property and attachments[].additionalProperties is false — writing it
// would have been SCH-003 drift. That omission then made AC5 unprovable:
// `retention: 168h` is a RECIPE, and nothing on a committed artifact records
// when the bytes were attached, so a reader had no anchor to apply it to. A
// reader reaching for `created` instead would compute a confidently wrong
// lapse date, which is the false-verdict class this phase exists to remove.
// The schema carries expires_at now and requires it exactly when retention is
// a duration; `pinned` carries none, which is what "pinned" means.
func attachAppendEntry(fm artifact.Frontmatter, attachment datapackage.Attachment, expiresAt string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return nil, fmt.Errorf("cannot decode frontmatter: %w", err)
	}

	entryRaw, err := yaml.Marshal(attachment)
	if err != nil {
		return nil, fmt.Errorf("cannot encode attachment entry: %w", err)
	}
	var entry map[string]any
	if err := yaml.Unmarshal(entryRaw, &entry); err != nil {
		return nil, fmt.Errorf("cannot decode attachment entry: %w", err)
	}
	// Empty exactly when retention is `pinned`, and the schema refuses the
	// key in that case — so the conditional here and the conditional there
	// are the same rule stated twice, which is the point: neither can drift
	// without the other refusing.
	if expiresAt != "" {
		entry["expires_at"] = expiresAt
	}

	var attachments []any
	if raw, ok := doc["attachments"]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("attachments is not an array")
		}
		attachments = list
	}
	doc["attachments"] = append(attachments, entry)

	newYAML, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("cannot encode frontmatter: %w", err)
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body}), nil
}

// attachJSONResult is `a2a attach --json`'s wire shape.
type attachJSONResult struct {
	Draft      string                              `json:"draft"`
	Attachment datapackage.AttachmentManifestEntry `json:"attachment"`
}

func attachEncodeJSON(stdio IO, draftPath string, entry datapackage.AttachmentManifestEntry) int {
	if err := json.NewEncoder(stdio.Stdout).Encode(attachJSONResult{Draft: draftPath, Attachment: entry}); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "attach: encode result: %v\n", err)
		return 1
	}
	return 0
}

// attachErrorEnvelope mirrors dataErrorEnvelope's own --json refusal shape
// (cmd_data.go): the offending value is already named in err's own text
// (internal/datapackage's own error wrapping composes
// "<sentinel>: <value>"), carried through verbatim rather than re-parsed.
type attachErrorEnvelope struct {
	Error string `json:"error"`
}

// attachRefuse mirrors dataRefuse's own both-modes rendering (cmd_data.go):
// text always to stderr; when --json was requested, the same text is ALSO
// emitted as a small JSON envelope to stdout, so a machine parsing --json
// output still gets the full message on failure rather than nothing.
func attachRefuse(stdio IO, err error, asJSON bool) int {
	if asJSON {
		if encErr := json.NewEncoder(stdio.Stdout).Encode(attachErrorEnvelope{Error: err.Error()}); encErr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "attach: encode error: %v\n", encErr)
		}
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "attach: %v\n", err)
	return 1
}
