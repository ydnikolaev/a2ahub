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
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
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

// AttachBlobRequest is what AttachCommand asks the ops layer to land in the
// space BEFORE it writes the local attachments: entry — spec 10
// (agent-exchange-2026-08) §1's decided ordering: "it writes them into the
// space ... before anything references them". Payload is blob-root-
// relative paths to bytes this command already read/walked locally
// (datapackage.NewAttachmentFromBytes/NewAttachmentFromDirectoryWithPayload,
// below) — the ops layer never re-reads the source, avoiding a second,
// independently-timed read of it.
type AttachBlobRequest struct {
	Payload map[string][]byte
}

// AttachBlobResult is what the ops layer reports back: the minted BL- id —
// this command's own attachments[].ref (spec 10 §2) — and the funnel write
// outcome, mirroring DataResult's own Write field (cmd_data.go) so a caller
// reads this write exactly the way it already reads `data deliver`'s.
type AttachBlobResult struct {
	BlobID string
	Write  space.WriteResult
}

// AttachOperations is the one seam `a2a attach` needs beyond the pure local
// core it already calls directly (internal/datapackage's attach.go):
// landing the payload bytes in the space. Mirrors DataOperations' shape
// (cmd_data.go) — a thin, single-method interface cmd/a2a wires to a real
// space.DeliverBlob call, and a test can fake trivially.
type AttachOperations interface {
	DeliverBlob(ctx context.Context, req AttachBlobRequest) (AttachBlobResult, error)
}

// AttachCommand implements `a2a attach`: it reads the draft named on the
// command line, reads the source bytes (a file or a directory) named by
// --from, mints an Attachment via internal/datapackage's pure core, LANDS
// those bytes in the space via ops.DeliverBlob (spec 10 §1's decided
// ordering — P10, agent-exchange-2026-08, wave B: this is a NETWORK WRITE
// now, not a local-only draft edit), and only then writes the
// attachments[] entry — carrying the minted BL- id as its ref — onto the
// draft's frontmatter.
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
	ops        AttachOperations

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
// this package already follows (rails anti-pattern #10). ops may be nil
// (e.g. a degraded/offline registration or the catalog's own nil-dep
// construction, cmd/a2a/catalog.go) — Run reports "service is not
// configured" rather than dereferencing it, matching DataCommand's own
// nil-ops precedent (cmd_data.go).
func NewAttachCommand(stagingDir string, bounds datapackage.Bounds, ops AttachOperations) *AttachCommand {
	return &AttachCommand{
		stagingDir: stagingDir,
		bounds:     bounds,
		ops:        ops,
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
		_, _ = fmt.Fprintln(stdio.Stdout, workflowLine("loop-send"))
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
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-send"))
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

	// attachment/payload are both built LOCALLY, no network access — the
	// digest datapackage's own core computes here (attachment.Digest) is
	// authoritative; space.DeliverBlob (called below) writes a sidecar that
	// reproduces the identical two-branch algorithm over the SAME payload
	// (internal/datapackage's digestForPayload / internal/space's
	// recomputeAttachmentDigest, proven to agree by
	// TestDigestForPayload_MatchesSpacePossessionAlgorithm and its space-side
	// sibling), so submit's possession check can never disagree with what
	// this command declares.
	var attachment datapackage.Attachment
	var payload map[string][]byte
	if info.IsDir() {
		attachment, payload, err = datapackage.NewAttachmentFromDirectoryWithPayload(ctx, *from, *role, *conformsTo, *verification, *retention, c.bounds)
	} else {
		var data []byte
		data, err = c.readFile(*from)
		if err != nil {
			return attachRefuse(stdio, fmt.Errorf("cannot read source %s: %w", *from, err), *asJSON)
		}
		attachment, err = datapackage.NewAttachmentFromBytes(data, *role, *conformsTo, *verification, *retention, c.bounds)
		payload = map[string][]byte{attachSinglePayloadKey(*from): data}
	}
	if err != nil {
		return attachRefuse(stdio, err, *asJSON)
	}

	// Resolved BEFORE the network write, not after: expires_at is part of
	// the entry the draft carries now, so a retention this command cannot
	// resolve must refuse before any bytes reach the space, rather than land
	// a blob nothing will ever reference.
	entry, eerr := datapackage.NewAttachmentManifestEntry(attachment, c.now())
	if eerr != nil {
		return attachRefuse(stdio, fmt.Errorf("draft %s: cannot resolve retention: %w", draftPath, eerr), *asJSON)
	}

	// Every local refusal above ran before this point. From here on, `a2a
	// attach` is a NETWORK WRITE (spec 10 §1's decided ordering: the bytes
	// land in the space BEFORE anything references them) — an agent that
	// expects a purely local draft edit will be surprised by the first
	// failure here, so the refusal and the success message both say so
	// explicitly (this command's own doc comment; epic-backlog B29: the
	// funnel's step-0 idempotency check can call GitHub before any
	// possession check, so an offline author may see a raw transport error
	// here rather than a possession-shaped one — not fixed by this command,
	// not hidden by it either).
	if c.ops == nil {
		return attachServiceUnavailable(stdio)
	}
	delivered, derr := c.ops.DeliverBlob(ctx, AttachBlobRequest{Payload: payload})
	if derr != nil {
		return attachRefuse(stdio, fmt.Errorf("write %s to the space: %w", draftPath, derr), *asJSON)
	}
	// The declared ref becomes the minted blob id (spec 10 §2) — everything
	// else about entry (Digest, Role, ConformsTo, Verification, Retention,
	// ExpiresAt) was already resolved above and is untouched by this write.
	entry.Ref = delivered.BlobID

	updated, uerr := attachAppendEntry(fm, entry.Attachment, entry.ExpiresAt)
	if uerr != nil {
		return attachRefuse(stdio, fmt.Errorf("draft %s: %w", draftPath, uerr), *asJSON)
	}
	if werr := c.writeFile(draftPath, updated, 0o644); werr != nil {
		return attachRefuse(stdio, fmt.Errorf("cannot write %s: %w", draftPath, werr), *asJSON)
	}

	if *asJSON {
		return attachEncodeJSON(stdio, draftPath, entry, delivered.Write)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "attach: %s written to the space\n", delivered.BlobID)
	_, _ = fmt.Fprintf(stdio.Stdout, "%s %s (%s)\n", delivered.Write.State, delivered.Write.PRURL, delivered.Write.Branch)
	_, _ = fmt.Fprintf(stdio.Stdout, "attach: digest %s -> %s\n", entry.Digest, draftPath)
	if entry.ExpiresAt != "" {
		_, _ = fmt.Fprintf(stdio.Stdout, "expires %s\n", entry.ExpiresAt)
	}
	return 0
}

// attachSinglePayloadKey names the ONE entry a single-file --from source
// becomes in the blob's own payload map. The name never affects the digest
// a single-file attach carries — digestForPayload's own single-entry branch
// (internal/datapackage's attach.go) ignores payload KEYS entirely — it only
// has to be a clean, blob-root-relative path (space.DeliverBlob's own
// isCleanRelativePath check). filepath.Base(from) is that path for an
// ordinary file; the handful of bases isCleanRelativePath would refuse
// ("..", ".", "", the OS separator — e.g. from == "/" or from == "..") fall
// back to a fixed literal name instead of surfacing a confusing path-shaped
// refusal for what is, from the caller's point of view, an ordinary --from
// argument.
func attachSinglePayloadKey(from string) string {
	switch base := filepath.Base(from); base {
	case "", ".", "..", string(filepath.Separator):
		return "payload"
	default:
		return base
	}
}

// attachServiceUnavailable mirrors dataServiceUnavailable's own message
// shape (cmd_data.go) for the same "not configured" degraded-registration
// case.
func attachServiceUnavailable(stdio IO) int {
	_, _ = fmt.Fprintln(stdio.Stderr, "attach: service is not configured")
	return 1
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
// attachAppendEntry appends one attachment entry to fm's frontmatter,
// through yaml.Node rather than map[string]any — and the difference is a
// shipped defect, not a style preference.
//
// THE BUG THIS FIXES, found 2026-08-27 by no-silent-yes-2026-08/P3 the moment
// `format` assertion was switched on. `yaml.Unmarshal` into a
// `map[string]any` resolves an unquoted `2026-12-31` to a **time.Time**, and
// marshalling it back writes `2026-12-31T00:00:00Z`. So `a2a attach` silently
// rewrote `needed_by` — a field the envelope schema declares `format: date` —
// into a value that violates its own declared format. Nothing refused it,
// because nothing asserted `format`. Measured on the real binary: the live
// matrix's `bytes-round-trip` and `bytes-round-trip-corrupted-refused` rows
// both went red with `SCH-012 needed_by: fails schema validation (format)` at
// the `a2a submit` AFTER the attach.
//
// The map round-trip also **alphabetically reordered the entire frontmatter**,
// so every attach rewrote fields the author never touched. A yaml.Node walk
// preserves scalars verbatim and leaves key order alone: this function now
// changes exactly one key, `attachments`, and nothing else.
func attachAppendEntry(fm artifact.Frontmatter, attachment datapackage.Attachment, expiresAt string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return nil, fmt.Errorf("cannot decode frontmatter: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cannot decode frontmatter: not a YAML mapping")
	}
	root := doc.Content[0]

	entryRaw, err := yaml.Marshal(attachment)
	if err != nil {
		return nil, fmt.Errorf("cannot encode attachment entry: %w", err)
	}
	var entryDoc yaml.Node
	if err := yaml.Unmarshal(entryRaw, &entryDoc); err != nil {
		return nil, fmt.Errorf("cannot decode attachment entry: %w", err)
	}
	if entryDoc.Kind != yaml.DocumentNode || len(entryDoc.Content) == 0 || entryDoc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("cannot decode attachment entry: not a YAML mapping")
	}
	entry := entryDoc.Content[0]

	// Empty exactly when retention is `pinned`, and the schema refuses the
	// key in that case — so the conditional here and the conditional there
	// are the same rule stated twice, which is the point: neither can drift
	// without the other refusing.
	if expiresAt != "" {
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "expires_at"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: expiresAt})
	}

	var seq *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "attachments" {
			seq = root.Content[i+1]
			break
		}
	}
	switch {
	case seq == nil:
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "attachments"}, seq)
	case seq.Tag == "!!null":
		// `attachments:` with no value — a declared-but-empty list.
		seq.Kind, seq.Tag, seq.Value = yaml.SequenceNode, "!!seq", ""
	case seq.Kind != yaml.SequenceNode:
		return nil, fmt.Errorf("attachments is not an array")
	}
	seq.Content = append(seq.Content, entry)

	newYAML, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("cannot encode frontmatter: %w", err)
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body}), nil
}

// attachJSONResult is `a2a attach --json`'s wire shape. Write mirrors
// DataResult's own field (cmd_data.go: `Write *space.WriteResult
// json:"write,omitempty"`) rather than inventing a second spelling of the
// same fact — attach is a network write exactly like `data deliver`, and a
// machine caller must be able to follow it the same way: decode Write.Branch
// and Write.PRURL rather than scraping the plain-text stdout lines below.
type attachJSONResult struct {
	Draft      string                              `json:"draft"`
	Attachment datapackage.AttachmentManifestEntry `json:"attachment"`
	Write      *space.WriteResult                  `json:"write,omitempty"`
}

func attachEncodeJSON(stdio IO, draftPath string, entry datapackage.AttachmentManifestEntry, write space.WriteResult) int {
	if err := json.NewEncoder(stdio.Stdout).Encode(attachJSONResult{Draft: draftPath, Attachment: entry, Write: &write}); err != nil {
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
