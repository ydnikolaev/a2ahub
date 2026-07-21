// OP-203 `a2a new`, OP-219 `a2a template list/show` (spec 06 T1). This
// file's only package-level symbols are NewCommand/TemplateCommand + their
// NewXCommand constructors plus file-private, uniquely-named helpers
// (new* prefix) and the newTypePrefix table — no shared helper, no
// package var beyond that lookup table, per this phase's plan Placement
// decision (avoids collision with P7/P8/P9's parallel verb files in this
// package).
package cli

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// newTypePrefix maps an envelope type to its §3.3 ID prefix + mint class
// (standing vs exchange/broadcast) — cmd_new's own small, table-driven
// lookup (Future-proofing table, §9: "no per-type branch to hand-edit").
// No core package exports this mapping as a queryable table today (each
// schema file's own `id` pattern encodes it, but only as a regex).
var newTypePrefix = map[string]struct {
	Prefix string
	Class  artifact.Class
}{
	"contract":     {"XC", artifact.ClassStanding},
	"requirement":  {"XR", artifact.ClassStanding},
	"question":     {"XQ", artifact.ClassExchangeBroadcast},
	"work_request": {"XW", artifact.ClassExchangeBroadcast},
	"decision":     {"XD", artifact.ClassExchangeBroadcast},
	"response":     {"XS", artifact.ClassExchangeBroadcast},
	"handoff":      {"XH", artifact.ClassExchangeBroadcast},
	"announcement": {"XA", artifact.ClassExchangeBroadcast},
}

// NewCommand implements `a2a new <type>`: mints an ID (§3.3), resolves
// the actor (§7.4), renders the canonical template (internal/template),
// and writes the draft under .a2a/staging/ — drafts never enter the
// space (§3.4). Non-interactive input (--field k=v, --body-file) is
// normative; this phase does not implement $EDITOR/TTY prompting (see
// this phase's Deviations report — sugar over the same code path, never
// load-bearing for any acceptance row).
type NewCommand struct {
	stagingDir string
	ownSystem  string

	now          func() time.Time
	entropy      io.Reader
	resolveActor func(ActorFlags) template.Actor
	writeFile    func(path string, data []byte, perm os.FileMode) error
}

// NewNewCommand constructs the new command. ownSystem is this project's
// configured own system id (§7.4, used as the minted id's <system> token
// and the draft's default `from`). resolveActor closes over the §7.4
// harness/config fallbacks (cmd/a2a supplies
// `func(f cli.ActorFlags) template.Actor { return cli.ResolveActor(f, harness, cfg) }`);
// it must not be nil (rails anti-pattern #10).
func NewNewCommand(stagingDir, ownSystem string, resolveActor func(ActorFlags) template.Actor) *NewCommand {
	return &NewCommand{
		stagingDir:   stagingDir,
		ownSystem:    ownSystem,
		now:          time.Now,
		entropy:      rand.Reader,
		resolveActor: resolveActor,
		writeFile:    os.WriteFile,
	}
}

// Name implements cli.Command.
func (c *NewCommand) Name() string { return "new" }

// Synopsis implements cli.Command.
func (c *NewCommand) Synopsis() string {
	return "draft a new artifact from its canonical template: new <type> [--field k=v]... [--body-file <path>]"
}

// Run implements cli.Command. Exit codes: 2 = usage (missing/unknown
// type, missing --slug for a standing type, malformed --field); 1 =
// render/write failure; 0 = success.
func (c *NewCommand) Run(_ context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a new <type> [--field k=v]... [--body-file <path>] [--thread <id>] [--slug <slug>]")
		return 2
	}
	typ := args[0]
	prefixInfo, ok := newTypePrefix[typ]
	if !ok {
		_, _ = fmt.Fprintf(stdio.Stderr, "new: unknown type %q\n", typ)
		return 2
	}

	fs := flag.NewFlagSet("new "+typ, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	var fieldFlags newStringList
	fs.Var(&fieldFlags, "field", "k=v field override (repeatable)")
	bodyFile := fs.String("body-file", "", "path to a file whose contents replace the draft body")
	thread := fs.String("thread", "", "thread id to attach")
	slug := fs.String("slug", "", "slug for a standing-type id (required for contract/requirement)")
	actorKind := fs.String("actor-kind", "", "explicit actor.kind override")
	actorName := fs.String("actor-name", "", "explicit actor.name override")
	actorModel := fs.String("actor-model", "", "explicit actor.model override")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	fields, err := newParseFields(fieldFlags)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "new: %v\n", err)
		return 2
	}
	if *thread != "" {
		fields["thread"] = *thread
	}
	if _, has := fields["from"]; !has {
		fields["from"] = c.ownSystem
	}

	now := c.now()
	var mintedID string
	switch prefixInfo.Class {
	case artifact.ClassStanding:
		s := *slug
		if s == "" {
			s = fields["slug"]
		}
		delete(fields, "slug")
		if s == "" {
			_, _ = fmt.Fprintln(stdio.Stderr, "new: --slug (or --field slug=<slug>) is required for standing types (contract, requirement)")
			return 2
		}
		id, err := artifact.MintStandingID(prefixInfo.Prefix, c.ownSystem, s)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "new: cannot mint id: %v\n", err)
			return 1
		}
		mintedID = id
	case artifact.ClassExchangeBroadcast:
		id, err := artifact.MintExchangeIDAt(prefixInfo.Prefix, c.ownSystem, now, c.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "new: cannot mint id: %v\n", err)
			return 1
		}
		mintedID = id
	}

	var bodyOverride []byte
	if *bodyFile != "" {
		body, err := os.ReadFile(*bodyFile)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "new: cannot read --body-file %s: %v\n", *bodyFile, err)
			return 1
		}
		bodyOverride = body
	}

	resolvedActor := c.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})

	draft, err := template.Render(template.Input{
		Type:    typ,
		ID:      mintedID,
		Actor:   resolvedActor,
		Created: now,
		Fields:  fields,
		Body:    bodyOverride,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "new: render failed: %v\n", err)
		return 1
	}

	if err := os.MkdirAll(c.stagingDir, 0o755); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "new: cannot create staging directory: %v\n", err)
		return 1
	}
	path := filepath.Join(c.stagingDir, mintedID+".md")
	if err := c.writeFile(path, draft, 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "new: cannot write %s: %v\n", path, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "new: drafted %s -> %s\n", mintedID, path)
	return 0
}

// newParseFields splits each "k=v" --field value into a map; a value with
// no "=" is a usage error (never silently dropped or half-applied).
func newParseFields(raw []string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for _, kv := range raw {
		k, v, found := strings.Cut(kv, "=")
		if !found || k == "" {
			return nil, fmt.Errorf("malformed --field %q (want k=v)", kv)
		}
		out[k] = v
	}
	return out, nil
}

// newStringList is a repeatable string flag (flag.Value), stdlib-only.
type newStringList []string

func (l *newStringList) String() string { return strings.Join(*l, ",") }
func (l *newStringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

var _ Command = (*NewCommand)(nil)

// TemplateCommand implements OP-219 `a2a template list` / `a2a template
// show <type>`: read-only inspection of the same embedded templates
// NewCommand renders.
type TemplateCommand struct{}

// NewTemplateCommand constructs the template command.
func NewTemplateCommand() *TemplateCommand { return &TemplateCommand{} }

// Name implements cli.Command.
func (c *TemplateCommand) Name() string { return "template" }

// Synopsis implements cli.Command.
func (c *TemplateCommand) Synopsis() string { return "inspect canonical templates: list | show <type>" }

// Run implements cli.Command. Exit codes: 2 = usage; 1 = unknown type
// (show); 0 = success.
func (c *TemplateCommand) Run(_ context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a template list | a2a template show <type>")
		return 2
	}
	switch args[0] {
	case "list":
		for _, t := range template.Types() {
			_, _ = fmt.Fprintln(stdio.Stdout, t)
		}
		return 0
	case "show":
		if len(args) != 2 || args[1] == "" {
			_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a template show <type>")
			return 2
		}
		raw, err := template.Show(args[1])
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "template show: %v\n", err)
			return 1
		}
		_, _ = stdio.Stdout.Write(raw)
		return 0
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "template: unknown subcommand %q (want list|show)\n", args[0])
		return 2
	}
}

var _ Command = (*TemplateCommand)(nil)
