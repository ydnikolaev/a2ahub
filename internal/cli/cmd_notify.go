// `a2a notify render` (space-notify-2026-08 P3, spec 03 §T1): turns a
// space-repo checkout (cwd) into an ordered JSON array of P3's message
// model. It reads `./space.yaml` and reuses cmd_validate_ci.go's own
// gitChangedFilesFunc seam for the push range — never a second `git diff`
// shell-out, never a second space.yaml reader.
//
// `notify` and `notifications` are two different verbs on purpose:
// `a2a notifications` owns the LOCAL native channels (macOS/VS Code) and
// their offer machine; `a2a notify` owns the SPACE-SIDE CI projection and
// delivery. This file's own Synopsis says so, per spec 03 T1's own
// requirement that each verb's help line name which plane it belongs to.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/ghauth"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/spacenotify"
)

// NotifySubcommand describes one `a2a notify <sub>` sub-verb for external
// surface enumeration — the same {Name, Synopsis} shape
// cli.ContractSubcommands() provides (spec 06 §"Closing the parity debt").
type NotifySubcommand struct {
	Name     string // e.g. "setup"
	Synopsis string
}

// NotifySubcommands is the SSOT list of the `a2a notify` family's
// sub-verbs for surface enumeration — the MCP parity test
// (cmd/a2a/mcp_parity_test.go) expands the bare `notify` designated verb
// through exactly this list, the same shape it already does for
// `cli.ContractSubcommands()` and `cli.DataSubcommands()`. The notify
// sub-verbs are dispatched by the bare switch in NotifyCommand.Run (they
// are NOT registered as individual cli.Command values / buildCommands
// keys), so this list is their only machine-enumerable home. KEEP IN SYNC
// with that switch: a sub-verb added there without a row here (or vice
// versa) is exactly the drift the parity gate exists to catch.
func NotifySubcommands() []NotifySubcommand {
	return []NotifySubcommand{
		{Name: "render", Synopsis: "turn a push range into P3's ordered JSON message array"},
		{Name: "send", Synopsis: "read a message array from stdin and deliver it to Telegram"},
		{Name: "setup", Synopsis: "walk a human from no bot to a proven, delivering route (spec 06)"},
		{Name: "discover", Synopsis: "list the chats the bot can currently see (one getUpdates call)"},
		{Name: "verify", Synopsis: "read-only: report the route/secret/delivery facts doctor also reports"},
	}
}

// NotifyCommand implements `a2a notify <render|send|setup|discover|verify>`.
// `render` is P3's; `send` is P4's; `setup`/`discover`/`verify` are P6's —
// see spec 03 T1's own "what adding a verb costs" note for why these all
// live on this SAME command rather than a new one.
type NotifyCommand struct {
	// gitChanged is the DI seam over `git diff --name-only`
	// (cmd_validate_ci.go's own gitChangedFilesFunc type + gitDiffNameOnly
	// implementation) — reused verbatim, never a second shell-out.
	gitChanged gitChangedFilesFunc
	// now is the injected clock (rails: no buried time.Now()).
	now func() time.Time
	// telegram is `send`'s own transport seam (spacenotify.Client's own
	// injected *http.Client/BaseURL) — real by default; tests construct
	// NotifyCommand directly (or call runNotifySend) with an httptest fake.
	telegram *spacenotify.Client

	// The following back `setup`/`discover`/`verify` (P6, spec 06). Every
	// one is a DI seam (rails: no buried side effect) real-defaulted by
	// NewNotifyCommand; tests override individually so the token-hygiene
	// and unproven-vs-configured paths are driven without a TTY, a `gh`
	// binary or a live GitHub/Telegram call.
	//
	// ghAvailable/ghToken mirror internal/ghauth's own two-function shape
	// (Available/Token) — this file adds NO second discovery path for
	// "is gh here and authenticated", it calls ghauth's.
	ghAvailable func(ctx context.Context) bool
	ghToken     func(ctx context.Context) (string, bool)
	// scopes reads the workflow token scope through the EXISTING
	// host.Host.TokenScopes primitive (spaceScopeChecker's own shape,
	// cmd_space.go) — no new host method (spec 06 §"Where the host calls
	// live").
	scopes spaceScopeChecker
	// setupAdapter performs the four GitHub REST reads/writes host.Host
	// does not offer: admin-permission read, workflow_dispatch trigger,
	// and run-conclusion/delivery-evidence read. It deliberately does NOT
	// set the secret — see notifySetupAdapter's own doc comment for why.
	setupAdapter *notifySetupAdapter
	// promptToken reads the bot token from the human's TTY, echo disabled,
	// and returns it in a redactingToken. Never called in
	// --non-interactive mode.
	promptToken func(stdio IO) (redactingToken, error)
	// ghSecretSet feeds `gh secret set <name> --repo <repo>` from STDIN —
	// never argv (spec 06 §T1 step 4).
	ghSecretSet func(ctx context.Context, secretName, repo string, value redactingToken) error
	// gitRemote resolves the local checkout's own `origin` remote URL —
	// `notify setup/verify` run standalone at the space-repo checkout
	// (cwd), exactly like `notify render`, so the space repo's identity
	// comes from the local git remote, never from `.a2a/config.yaml`
	// (that file describes a CONSUMER project's connected spaces, not a
	// space repo's own identity).
	gitRemote gitRemoteURLFunc
	// sleep backs the run-status poll loop (step 7); tests inject a no-op
	// so a poll never actually waits wall-clock time.
	sleep func(time.Duration)
}

// NewNotifyCommand constructs the real, wired NotifyCommand.
func NewNotifyCommand() *NotifyCommand {
	h := host.NewGitHubHost(http.DefaultClient, notifyGitHubAPIBase())
	return &NotifyCommand{
		gitChanged:   gitDiffNameOnly,
		now:          time.Now,
		telegram:     spacenotify.NewClient(),
		ghAvailable:  ghauth.Available,
		ghToken:      ghauth.Token,
		scopes:       h,
		setupAdapter: newNotifySetupAdapter(http.DefaultClient, notifyGitHubAPIBase()),
		promptToken:  readTokenFromTTY,
		ghSecretSet:  runGHSecretSet,
		gitRemote:    gitRemoteOriginURL,
		sleep:        time.Sleep,
	}
}

// Name implements cli.Command.
func (c *NotifyCommand) Name() string { return "notify" }

// Synopsis implements cli.Command.
func (c *NotifyCommand) Synopsis() string {
	return "space-side CI notification projection and delivery (SPACE plane — see `a2a notifications` for the local/native plane): render --base <sha>|--all|--only <id,id> [--limit <n>] [--json] — turn a push range into P3's ordered JSON message array; send [--dry-run] — read that array from stdin and deliver it to Telegram, one delivery record per message on stdout; setup [--non-interactive] — walk a human from no bot to a proven, delivering route; discover — list the chats the bot can currently see; verify — read-only route/secret/delivery report"
}

const notifyUsage = "usage: a2a notify render (--base <sha> | --all | --only <id,id>) [--limit <n>] [--json]\n" +
	"       a2a notify send [--dry-run]\n" +
	"       a2a notify setup [--space <id>] [--for <participant>] [--events <list>] [--locale <ru|en>] [--non-interactive]\n" +
	"       a2a notify discover [--timeout <duration>]\n" +
	"       a2a notify verify [--space <id>]"

// Run implements cli.Command.
func (c *NotifyCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 || IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, notifyUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "render":
		return runNotifyRender(ctx, ".", c.gitChanged, c.now, rest, stdio)
	case "send":
		return runNotifySend(ctx, c.telegram, rest, stdio)
	case "setup":
		return runNotifySetup(ctx, c, ".", rest, stdio)
	case "discover":
		return runNotifyDiscover(ctx, c, rest, stdio)
	case "verify":
		return runNotifyVerify(ctx, c, ".", rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify: unknown sub-verb %q\n%s\n", sub, notifyUsage)
		return 2
	}
}

// runNotifyRender is `render`'s own body, factored out (root/git/now as
// explicit parameters, mirroring runValidateCI's own shape) so a test can
// point it at a fixture checkout without going through NotifyCommand.Run.
// Exit codes: 2 = usage (no/conflicting range selector, bad --limit);
// 1 = a Render refusal (unknown --only id, undeclared secret) or a
// checkout/manifest read error; 0 = success (an empty array is still 0).
func runNotifyRender(ctx context.Context, root string, git gitChangedFilesFunc, now func() time.Time, args []string, stdio IO) int {
	fs := flag.NewFlagSet("notify render", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	base := fs.String("base", "", "the push range's base sha (mutually exclusive with --all/--only)")
	all := fs.Bool("all", false, "render every qualifying artifact, ignoring any push range")
	only := fs.String("only", "", "comma-separated artifact ids — bypasses the push range, the event filter and digest coalescing")
	limit := fs.Int("limit", 5, "the digest-coalescing threshold for the automatic (--base/--all) path")
	_ = fs.Bool("json", true, "JSON is the only output format in v1; this flag exists for CLI symmetry")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, notifyUsage)
		return 2
	}

	selectors := 0
	if *base != "" {
		selectors++
	}
	if *all {
		selectors++
	}
	if *only != "" {
		selectors++
	}
	if selectors != 1 {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: exactly one of --base, --all, --only is required\n%s\n", "a2a notify render", notifyUsage)
		return 2
	}
	if *limit <= 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a notify render: --limit must be positive")
		return 2
	}

	raw, err := os.ReadFile(filepath.Join(root, "space.yaml"))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: cannot read space.yaml: %v\n", err)
		return 1
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: %v\n", err)
		return 1
	}

	opts := spacenotify.Options{Limit: *limit, Now: now()}
	switch {
	case *only != "":
		opts.Mode = spacenotify.ModeOnly
		opts.OnlyIDs = splitNonEmpty(*only, ",")
	case *all:
		opts.Mode = spacenotify.ModeAll
	default:
		opts.Mode = spacenotify.ModePush
		changed, err := git(ctx, root, *base)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: %v\n", err)
			return 1
		}
		opts.Changed = changed
	}

	messages, accounting, err := spacenotify.Render(ctx, root, manifest, opts)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: %v\n", err)
		return 1
	}
	if messages == nil {
		messages = []spacenotify.Message{}
	}

	// P11 (spec 11 ACs 8-10): a per-route accounting line, printed to
	// STDERR — never mixed into stdout's JSON array. `notify send` reads
	// stdout, whole, as the message array (runNotifySend's own
	// io.ReadAll + json.Unmarshal); the reusable CI workflow redirects
	// only stdout to the file it hands `notify send`
	// (a2a-notify-reusable.yml: `a2a "${render_args[@]}" >"$rendered"`,
	// its own comment: "the CLI's own stderr ... is left unchanged") — so
	// stderr is exactly where a line that must never break that pipe
	// belongs. This is a DEVIATION from spec 11 AC-9's literal "on
	// stdout" wording; see this phase's own report for why stdout could
	// not stay both pure JSON and carry it.
	printNotifyAccounting(stdio.Stderr, accounting)

	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(messages); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: cannot encode JSON output: %v\n", err)
		return 1
	}
	return 0
}

const notifySendUsage = "usage: a2a notify send [--dry-run] (reads the message array `notify render` produces from stdin)"

// maxNotifySendInputBytes bounds the stdin read (rails: bounded reads
// everywhere) — generous for any realistic `notify render` output, far
// short of this process's own memory budget.
const maxNotifySendInputBytes = 16 << 20 // 16 MiB

// runNotifySend is `send`'s own body (spec 04 T1): read the message array
// `notify render` produced from stdin — and ONLY stdin, no second
// space.yaml read (AC13) — deliver each message, and print one
// spacenotify.DeliveryRecord per message, in send order, on stdout.
//
// Exit codes: 2 = usage (bad flags) or input that is not a valid message
// array; 1 = at least one delivery record's outcome is "failed" (AC4); 0 =
// every record is "sent" or "dry-run" (an empty array is still 0).
func runNotifySend(ctx context.Context, client *spacenotify.Client, args []string, stdio IO) int {
	fs := flag.NewFlagSet("notify send", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	dryRun := fs.Bool("dry-run", false, "render and print delivery records without calling the Telegram API (US-3's rehearsal path, spec 04 T1)")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, notifySendUsage)
		return 2
	}

	raw, err := io.ReadAll(io.LimitReader(stdio.Stdin, maxNotifySendInputBytes+1))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify send: cannot read stdin: %v\n", err)
		return 1
	}
	if len(raw) > maxNotifySendInputBytes {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a notify send: input exceeds the bounded read limit")
		return 1
	}

	var messages []spacenotify.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify send: input is not a valid message array: %v\n", err)
		return 2
	}

	records := spacenotify.Send(ctx, client, messages, *dryRun)
	if records == nil {
		records = []spacenotify.DeliveryRecord{}
	}

	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(records); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify send: cannot encode JSON output: %v\n", err)
		return 1
	}

	for _, r := range records {
		if r.Outcome == "failed" {
			return 1
		}
	}
	return 0
}

// printNotifyAccounting prints P11's own accounting report (spec 11 ACs
// 8-9): the qualifying-artifact denominator, then one kept-count line per
// DECLARED route (never omitted for a zero-keep route — see
// spacenotify.Accounting's own doc comment), each composed through
// denominatorLine so a zero-keep run is visibly distinct from a
// zero-qualifying run: the FIRST line already says "0 artifacts qualified"
// when nothing qualified at all, before any per-route line is even
// reached.
func printNotifyAccounting(w io.Writer, accounting spacenotify.Accounting) {
	_, _ = fmt.Fprintln(w, denominatorLine("notify render: artifacts qualified", accounting.Qualified))
	for _, ra := range accounting.PerRoute {
		_, _ = fmt.Fprintln(w, denominatorLine("  route "+notifyRouteLabel(ra.Route)+" kept", ra.Kept))
	}
}

// notifyRouteLabel identifies one route for a report line — the same
// (chat, topic, for) identity internal/validate's own dedup rule already
// keys routes by, since a notification route carries no separate name.
func notifyRouteLabel(r space.NotificationRoute) string {
	label := r.Chat
	if r.Topic != nil {
		label += fmt.Sprintf("/%d", *r.Topic)
	}
	if r.For != "" {
		label += " for=" + r.For
	}
	return label
}

// denominatorLine composes P11's own "a run that examined nothing must not
// look like a clean pass" report line (answers-that-hold-2026-08 spec 11
// §"Two phases of this epic reached [this] independently"): label followed
// by n, ALWAYS printed — including when n is 0 — so a caller never has to
// infer a zero denominator from an absent line. Deliberately generic
// (label, n) rather than notify-specific: spec 07's own aggregate
// zero-row report (P7, W3, same internal/cli package) needs the identical
// shape for "0 contracts published for <system>" and takes this function
// directly rather than writing a second implementation — the "one helper,
// two callers" AC-10 asks for.
func denominatorLine(label string, n int) string {
	return fmt.Sprintf("%s: %d", label, n)
}

// splitNonEmpty splits s on sep, trims each part and drops empties — the
// `--only a,b,,c` typo-tolerance every comma-list flag in this package
// applies.
func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
