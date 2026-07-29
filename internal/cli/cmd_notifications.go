package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/notification"
)

// notificationsBackend is the consumer-side seam between the CLI transport
// and notification's application service. Parsing and rendering live here;
// project defaults, component lifecycle, preferences, leases, and routes do
// not.
type notificationsBackend interface {
	Install(context.Context, notification.InstallRequest) (notification.OperationResult, error)
	Status(context.Context, notification.StatusRequest) (notification.Status, error)
	Test(context.Context, notification.TestRequest) (notification.OperationResult, error)
	Uninstall(context.Context, notification.UninstallRequest) (notification.OperationResult, error)
	Preference(context.Context, notification.PreferenceRequest) (notification.PreferenceResult, error)
	Claim(context.Context, notification.ClaimRequest) (notification.ClaimResult, error)
	Ack(context.Context, notification.AckRequest) (notification.AckResult, error)
	Open(context.Context, notification.OpenRequest) (notification.OpenResult, error)
}

// NotificationsCommand implements the `a2a notifications` command family.
type NotificationsCommand struct {
	backend notificationsBackend
}

// NewNotificationsCommand constructs the notification CLI transport.
func NewNotificationsCommand(backend notificationsBackend) *NotificationsCommand {
	return &NotificationsCommand{backend: backend}
}

// Name implements Command.
func (c *NotificationsCommand) Name() string { return "notifications" }

// Synopsis implements Command.
func (c *NotificationsCommand) Synopsis() string {
	return "install, inspect, test, and control native notification surfaces"
}

// Run parses and dispatches one notifications subcommand.
func (c *NotificationsCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		notificationsUsage(stdio.Stderr)
		return 2
	}
	if IsHelpArg(args[0]) {
		notificationsUsage(stdio.Stdout)
		return 0
	}
	subcommand, rest := args[0], args[1:]
	switch subcommand {
	case "install":
		return c.runInstall(ctx, rest, stdio)
	case "status":
		return c.runStatus(ctx, rest, stdio)
	case "test":
		return c.runTest(ctx, rest, stdio)
	case "uninstall":
		return c.runUninstall(ctx, rest, stdio)
	case "preference":
		return c.runPreference(ctx, rest, stdio)
	case "open":
		return c.runOpen(ctx, rest, stdio)
	case "claim":
		return c.runClaim(ctx, rest, stdio)
	case "ack":
		return c.runAck(ctx, rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "notifications: unknown subcommand %q\n", subcommand)
		notificationsUsage(stdio.Stderr)
		return 2
	}
}

var _ Command = (*NotificationsCommand)(nil)

// NotificationsSubcommands is the SSOT used by command catalogs and shell
// completion.
func NotificationsSubcommands() []string {
	return []string{"install", "status", "test", "uninstall", "preference", "open", "claim", "ack"}
}

func (c *NotificationsCommand) runInstall(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications install", stdio)
	channel := fs.String("channel", "all", "notification channel: all, macos, or vscode")
	profile := fs.String("profile", "", "exact VS Code profile name")
	codePath := fs.String("code", "", "exact VS Code CLI path")
	project := fs.String("project", "", "project root")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if code := notificationsParse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 0 {
		return notificationsRefuse(stdio, "install", "unexpected positional arguments")
	}
	channels, all, ok := notificationsChannels(*channel, true)
	if !ok {
		return notificationsRefuse(stdio, "install", "--channel must be all, macos, or vscode")
	}
	result, err := c.backend.Install(ctx, notification.InstallRequest{
		Root: *project, Channels: channels, All: all, Profile: *profile, CodePath: *codePath,
	})
	return notificationsOperationResult(stdio, "install", result, *jsonOut, err)
}

func (c *NotificationsCommand) runStatus(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications status", stdio)
	channel := fs.String("channel", "all", "notification channel: all, macos, or vscode")
	project := fs.String("project", "", "project root")
	probe := fs.Bool("probe", false, "include bounded platform health probes")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if code := notificationsParse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 0 {
		return notificationsRefuse(stdio, "status", "unexpected positional arguments")
	}
	channels, all, ok := notificationsChannels(*channel, true)
	if !ok {
		return notificationsRefuse(stdio, "status", "--channel must be all, macos, or vscode")
	}
	var selected notification.Channel
	if !all {
		selected = channels[0]
	}
	result, err := c.backend.Status(ctx, notification.StatusRequest{Root: *project, Channel: selected, Probe: *probe})
	if err != nil {
		return notificationsRuntimeError(stdio, "status", err)
	}
	notificationsNormalizeStatus(&result)
	if *jsonOut {
		return notificationsJSON(stdio, "status", result)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "available: %s\ninstalled: %s\noffer: %s\n",
		notificationsChannelText(result.AvailableChannels),
		notificationsChannelText(result.InstalledChannels),
		result.Offer.State)
	return 0
}

func (c *NotificationsCommand) runTest(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications test", stdio)
	channel := fs.String("channel", "all", "notification channel: all, macos, or vscode")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if code := notificationsParse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 0 {
		return notificationsRefuse(stdio, "test", "unexpected positional arguments")
	}
	channels, all, ok := notificationsChannels(*channel, true)
	if !ok {
		return notificationsRefuse(stdio, "test", "--channel must be all, macos, or vscode")
	}
	result, err := c.backend.Test(ctx, notification.TestRequest{
		Channels: channels, All: all,
	})
	return notificationsOperationResult(stdio, "test", result, *jsonOut, err)
}

func (c *NotificationsCommand) runUninstall(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications uninstall", stdio)
	channel := fs.String("channel", "all", "notification channel: all, macos, or vscode")
	profile := fs.String("profile", "", "exact VS Code profile name")
	codePath := fs.String("code", "", "exact VS Code CLI path")
	purge := fs.Bool("purge", false, "also remove notification-local state")
	yes := fs.Bool("yes", false, "confirm effects on other enrolled projects")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if code := notificationsParse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 0 {
		return notificationsRefuse(stdio, "uninstall", "unexpected positional arguments")
	}
	channels, all, ok := notificationsChannels(*channel, true)
	if !ok {
		return notificationsRefuse(stdio, "uninstall", "--channel must be all, macos, or vscode")
	}
	result, err := c.backend.Uninstall(ctx, notification.UninstallRequest{
		Channels: channels, All: all, Profile: *profile, CodePath: *codePath, Purge: *purge, Yes: *yes,
	})
	return notificationsOperationResult(stdio, "uninstall", result, *jsonOut, err)
}

func (c *NotificationsCommand) runPreference(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications preference", stdio)
	project := fs.String("project", "", "project root")
	remind := fs.String("remind-in", "", "snooze offers for 1-365 whole days")
	never := fs.Bool("never", false, "suppress future offers")
	reset := fs.Bool("reset", false, "reset offer state to ask")
	global := fs.Bool("global", false, "apply supported preference globally")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if code := notificationsParse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 0 {
		return notificationsRefuse(stdio, "preference", "unexpected positional arguments")
	}
	selected := 0
	if *remind != "" {
		selected++
	}
	if *never {
		selected++
	}
	if *reset {
		selected++
	}
	if selected != 1 {
		return notificationsRefuse(stdio, "preference", "choose exactly one of --remind-in, --never, or --reset")
	}
	days := 0
	if *remind != "" {
		var err error
		days, err = strconv.Atoi(*remind)
		if err != nil || days < 1 || days > 365 {
			return notificationsRefuse(stdio, "preference", "--remind-in must be a whole number from 1 to 365")
		}
	}
	if *global && (!*never && !*reset) {
		return notificationsRefuse(stdio, "preference", "--global is valid only with --never or --reset")
	}
	result, err := c.backend.Preference(ctx, notification.PreferenceRequest{
		Root: *project, RemindInDays: days, Never: *never, Reset: *reset, Global: *global,
	})
	if err != nil {
		return notificationsRuntimeError(stdio, "preference", err)
	}
	notificationsNormalizeProject(result.Project)
	if *jsonOut {
		return notificationsJSON(stdio, "preference", result)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "preference: %s (%s)\n", result.Offer.State, result.Offer.Scope)
	return 0
}

func (c *NotificationsCommand) runClaim(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications claim", stdio)
	channel := fs.String("channel", "", "notification channel: macos or vscode")
	consumer := fs.String("consumer", "", "stable adapter consumer ID")
	project := fs.String("project", "", "registered project ID")
	limit := fs.Int("limit", 0, "maximum entries to claim")
	jsonOut := fs.Bool("json", false, "emit JSON (required)")
	if code := notificationsParse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() != 0 {
		return notificationsRefuse(stdio, "claim", "unexpected positional arguments")
	}
	channels, all, ok := notificationsChannels(*channel, false)
	if !ok || all {
		return notificationsRefuse(stdio, "claim", "--channel is required and must be macos or vscode")
	}
	if *consumer == "" || *project == "" || *limit < 1 || !*jsonOut {
		return notificationsRefuse(stdio, "claim", "--channel, --consumer, --project, --limit (greater than zero), and --json are required")
	}
	result, err := c.backend.Claim(ctx, notification.ClaimRequest{
		Channel: channels[0], Consumer: *consumer, ProjectID: *project, Limit: *limit,
	})
	if err != nil {
		return notificationsRuntimeError(stdio, "claim", err)
	}
	if result.Entries == nil {
		result.Entries = []notification.Entry{}
	}
	return notificationsJSON(stdio, "claim", result)
}

func (c *NotificationsCommand) runAck(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications ack", stdio)
	lease := fs.String("lease", "", "lease token")
	var entries notificationsStringList
	fs.Var(&entries, "entry", "accepted entry ID (repeatable)")
	jsonOut := fs.Bool("json", false, "emit JSON (required)")
	positionals, code := notificationsParseAnyOrder(fs, args)
	if code >= 0 {
		return code
	}
	entries = append(entries, positionals...)
	if *lease == "" || len(entries) == 0 || notificationsHasEmpty(entries) || !*jsonOut {
		return notificationsRefuse(stdio, "ack", "--lease, one or more --entry or positional entry IDs, and --json are required")
	}
	result, err := c.backend.Ack(ctx, notification.AckRequest{
		LeaseToken: *lease, EntryIDs: append([]string(nil), entries...),
	})
	if err != nil {
		return notificationsRuntimeError(stdio, "ack", err)
	}
	if result.Acked == nil {
		result.Acked = []string{}
	}
	if result.AlreadyAcked == nil {
		result.AlreadyAcked = []string{}
	}
	return notificationsJSON(stdio, "ack", result)
}

func (c *NotificationsCommand) runOpen(ctx context.Context, args []string, stdio IO) int {
	fs := notificationsFlagSet("notifications open", stdio)
	jsonOut := fs.Bool("json", false, "emit JSON")
	positionals, code := notificationsParseAnyOrder(fs, args)
	if code >= 0 {
		return code
	}
	if len(positionals) != 1 || positionals[0] == "" {
		return notificationsRefuse(stdio, "open", "exactly one route token is required")
	}
	result, err := c.backend.Open(ctx, notification.OpenRequest{RouteToken: positionals[0]})
	if err != nil {
		return notificationsRuntimeError(stdio, "open", err)
	}
	if *jsonOut {
		return notificationsJSON(stdio, "open", result)
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "opened %s in project %s\n", result.Kind, result.ProjectID)
	return 0
}

func notificationsFlagSet(name string, stdio IO) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	return fs
}

func notificationsParse(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	return -1
}

func notificationsParseAnyOrder(fs *flag.FlagSet, args []string) ([]string, int) {
	positionals, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, 0
		}
		return nil, 2
	}
	return positionals, -1
}

func notificationsChannels(value string, allowAll bool) ([]notification.Channel, bool, bool) {
	switch value {
	case "all":
		if allowAll {
			return nil, true, true
		}
	case string(notification.ChannelMacOS):
		return []notification.Channel{notification.ChannelMacOS}, false, true
	case string(notification.ChannelVSCode):
		return []notification.Channel{notification.ChannelVSCode}, false, true
	}
	return nil, false, false
}

func notificationsOperationResult(stdio IO, operation string, result notification.OperationResult, jsonOut bool, err error) int {
	if err != nil {
		return notificationsRuntimeError(stdio, operation, err)
	}
	if result.Results == nil {
		result.Results = []notification.ChannelResult{}
	}
	notificationsNormalizeProject(result.Project)
	if jsonOut {
		if code := notificationsJSON(stdio, operation, result); code != 0 {
			return code
		}
		if result.Failed {
			return 1
		}
		return 0
	}
	if len(result.Results) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "no notification channels")
	} else {
		for _, item := range result.Results {
			if item.Reason == "" {
				_, _ = fmt.Fprintf(stdio.Stdout, "%s: %s\n", item.Channel, item.Status)
			} else {
				_, _ = fmt.Fprintf(stdio.Stdout, "%s: %s (%s)\n", item.Channel, item.Status, item.Reason)
			}
		}
	}
	if result.Failed {
		return 1
	}
	return 0
}

func notificationsJSON(stdio IO, operation string, value any) int {
	if err := json.NewEncoder(stdio.Stdout).Encode(value); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "notifications %s: encode JSON: %v\n", operation, err)
		return 1
	}
	return 0
}

func notificationsRuntimeError(stdio IO, operation string, err error) int {
	_, _ = fmt.Fprintf(stdio.Stderr, "notifications %s: %v\n", operation, err)
	return 1
}

func notificationsRefuse(stdio IO, operation, reason string) int {
	_, _ = fmt.Fprintf(stdio.Stderr, "notifications %s: %s\n", operation, reason)
	return 2
}

func notificationsNormalizeStatus(status *notification.Status) {
	if status.AvailableChannels == nil {
		status.AvailableChannels = []notification.Channel{}
	}
	if status.InstalledChannels == nil {
		status.InstalledChannels = []notification.Channel{}
	}
	if status.Components == nil {
		status.Components = []notification.ComponentHealth{}
	}
	notificationsNormalizeProject(status.Project)
}

func notificationsNormalizeProject(project *notification.Project) {
	if project != nil && project.Channels == nil {
		project.Channels = []notification.Channel{}
	}
}

func notificationsChannelText(channels []notification.Channel) string {
	if len(channels) == 0 {
		return "none"
	}
	values := make([]string, len(channels))
	for i, channel := range channels {
		values[i] = string(channel)
	}
	return strings.Join(values, ", ")
}

func notificationsHasEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func notificationsUsage(w interface{ Write([]byte) (int, error) }) {
	_, _ = fmt.Fprintln(w, "usage: a2a notifications <install|status|test|uninstall|preference|open|claim|ack> ...")
}

type notificationsStringList []string

func (l *notificationsStringList) String() string { return strings.Join(*l, ",") }

func (l *notificationsStringList) Set(value string) error {
	if value == "" {
		return errors.New("entry ID must not be empty")
	}
	*l = append(*l, value)
	return nil
}
