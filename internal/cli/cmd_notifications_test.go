package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/notification"
)

type fakeNotificationsBackend struct {
	called        string
	install       notification.InstallRequest
	status        notification.StatusRequest
	test          notification.TestRequest
	uninstall     notification.UninstallRequest
	preference    notification.PreferenceRequest
	claim         notification.ClaimRequest
	ack           notification.AckRequest
	open          notification.OpenRequest
	operationOut  notification.OperationResult
	statusOut     notification.Status
	preferenceOut notification.PreferenceResult
	claimOut      notification.ClaimResult
	ackOut        notification.AckResult
	openOut       notification.OpenResult
	err           error
}

func (f *fakeNotificationsBackend) Install(_ context.Context, request notification.InstallRequest) (notification.OperationResult, error) {
	f.called, f.install = "install", request
	return f.operationOut, f.err
}

func (f *fakeNotificationsBackend) Status(_ context.Context, request notification.StatusRequest) (notification.Status, error) {
	f.called, f.status = "status", request
	return f.statusOut, f.err
}

func (f *fakeNotificationsBackend) Test(_ context.Context, request notification.TestRequest) (notification.OperationResult, error) {
	f.called, f.test = "test", request
	return f.operationOut, f.err
}

func (f *fakeNotificationsBackend) Uninstall(_ context.Context, request notification.UninstallRequest) (notification.OperationResult, error) {
	f.called, f.uninstall = "uninstall", request
	return f.operationOut, f.err
}

func (f *fakeNotificationsBackend) Preference(_ context.Context, request notification.PreferenceRequest) (notification.PreferenceResult, error) {
	f.called, f.preference = "preference", request
	return f.preferenceOut, f.err
}

func (f *fakeNotificationsBackend) Claim(_ context.Context, request notification.ClaimRequest) (notification.ClaimResult, error) {
	f.called, f.claim = "claim", request
	return f.claimOut, f.err
}

func (f *fakeNotificationsBackend) Ack(_ context.Context, request notification.AckRequest) (notification.AckResult, error) {
	f.called, f.ack = "ack", request
	return f.ackOut, f.err
}

func (f *fakeNotificationsBackend) Open(_ context.Context, request notification.OpenRequest) (notification.OpenResult, error) {
	f.called, f.open = "open", request
	return f.openOut, f.err
}

func TestNotificationsSubcommandsAndMetadata(t *testing.T) {
	t.Parallel()
	want := []string{"install", "status", "test", "uninstall", "preference", "open", "claim", "ack"}
	if got := cli.NotificationsSubcommands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("NotificationsSubcommands() = %v, want %v", got, want)
	}
	cmd := cli.NewNotificationsCommand(&fakeNotificationsBackend{})
	if got := cmd.Name(); got != "notifications" {
		t.Fatalf("Name() = %q, want notifications", got)
	}
	if got := cmd.Synopsis(); !strings.Contains(got, "notification") {
		t.Fatalf("Synopsis() = %q, want notification description", got)
	}
}

func TestNotificationsCommand_ParsesExactArgv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want any
		got  func(*fakeNotificationsBackend) any
	}{
		{
			name: "install",
			args: []string{"install", "--channel", "vscode", "--profile", "work", "--code", "/opt/code", "--project", "/repo", "--json"},
			want: notification.InstallRequest{Root: "/repo", Channels: []notification.Channel{notification.ChannelVSCode}, Profile: "work", CodePath: "/opt/code"},
			got:  func(f *fakeNotificationsBackend) any { return f.install },
		},
		{
			name: "status all",
			args: []string{"status", "--channel", "all", "--project", "/repo", "--json"},
			want: notification.StatusRequest{Root: "/repo"},
			got:  func(f *fakeNotificationsBackend) any { return f.status },
		},
		{
			name: "test all",
			args: []string{"test", "--channel", "all", "--json"},
			want: notification.TestRequest{All: true},
			got:  func(f *fakeNotificationsBackend) any { return f.test },
		},
		{
			name: "uninstall",
			args: []string{"uninstall", "--channel", "macos", "--profile", "work", "--code", "/opt/code", "--purge", "--yes", "--json"},
			want: notification.UninstallRequest{Channels: []notification.Channel{notification.ChannelMacOS}, Profile: "work", CodePath: "/opt/code", Purge: true, Yes: true},
			got:  func(f *fakeNotificationsBackend) any { return f.uninstall },
		},
		{
			name: "preference remind",
			args: []string{"preference", "--project", "/repo", "--remind-in", "17", "--json"},
			want: notification.PreferenceRequest{Root: "/repo", RemindInDays: 17},
			got:  func(f *fakeNotificationsBackend) any { return f.preference },
		},
		{
			name: "claim",
			args: []string{"claim", "--channel", "vscode", "--consumer", "editor:work", "--project", "01PROJECT", "--limit", "9", "--json"},
			want: notification.ClaimRequest{Channel: notification.ChannelVSCode, Consumer: "editor:work", ProjectID: "01PROJECT", Limit: 9},
			got:  func(f *fakeNotificationsBackend) any { return f.claim },
		},
		{
			name: "ack repeated and positional entries",
			args: []string{"ack", "--lease", "lease-1", "--entry", "01A", "--entry", "01B", "--json", "01C"},
			want: notification.AckRequest{LeaseToken: "lease-1", EntryIDs: []string{"01A", "01B", "01C"}},
			got:  func(f *fakeNotificationsBackend) any { return f.ack },
		},
		{
			name: "open flag after token",
			args: []string{"open", "route-1", "--json"},
			want: notification.OpenRequest{RouteToken: "route-1"},
			got:  func(f *fakeNotificationsBackend) any { return f.open },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeNotificationsBackend{}
			cmd := cli.NewNotificationsCommand(backend)
			stdio, _, errOut := newIO()
			if code := cmd.Run(context.Background(), test.args, stdio); code != 0 {
				t.Fatalf("Run(%v) code = %d, want 0; stderr=%s", test.args, code, errOut.String())
			}
			if got := test.got(backend); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("request = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNotificationsCommand_AllChannelRequestsSetAll(t *testing.T) {
	t.Parallel()
	tests := []struct {
		subcommand string
		gotAll     func(*fakeNotificationsBackend) bool
	}{
		{subcommand: "install", gotAll: func(f *fakeNotificationsBackend) bool { return f.install.All }},
		{subcommand: "test", gotAll: func(f *fakeNotificationsBackend) bool { return f.test.All }},
		{subcommand: "uninstall", gotAll: func(f *fakeNotificationsBackend) bool { return f.uninstall.All }},
	}
	for _, test := range tests {
		t.Run(test.subcommand, func(t *testing.T) {
			t.Parallel()
			backend := &fakeNotificationsBackend{}
			stdio, _, errOut := newIO()
			code := cli.NewNotificationsCommand(backend).Run(
				context.Background(), []string{test.subcommand, "--channel", "all", "--json"}, stdio,
			)
			if code != 0 {
				t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
			}
			if !test.gotAll(backend) {
				t.Fatal("request All = false, want true")
			}
		})
	}
}

func TestNotificationsCommand_ExactRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no subcommand", want: "usage: a2a notifications <install|status|test|uninstall|preference|open|claim|ack> ...\nworkflow: run `a2a docs notifications` for the walkthrough\n"},
		{name: "unknown subcommand", args: []string{"send"}, want: "notifications: unknown subcommand \"send\"\nusage: a2a notifications <install|status|test|uninstall|preference|open|claim|ack> ...\nworkflow: run `a2a docs notifications` for the walkthrough\n"},
		{name: "unknown channel", args: []string{"install", "--channel", "statusline"}, want: "notifications install: --channel must be all, macos, or vscode\n"},
		{name: "status positional", args: []string{"status", "extra"}, want: "notifications status: unexpected positional arguments\n"},
		{name: "preference no choice", args: []string{"preference"}, want: "notifications preference: choose exactly one of --remind-in, --never, or --reset\n"},
		{name: "preference conflicting", args: []string{"preference", "--never", "--reset"}, want: "notifications preference: choose exactly one of --remind-in, --never, or --reset\n"},
		{name: "preference low days", args: []string{"preference", "--remind-in", "0"}, want: "notifications preference: --remind-in must be a whole number from 1 to 365\n"},
		{name: "preference high days", args: []string{"preference", "--remind-in", "366"}, want: "notifications preference: --remind-in must be a whole number from 1 to 365\n"},
		{name: "preference global remind", args: []string{"preference", "--remind-in", "7", "--global"}, want: "notifications preference: --global is valid only with --never or --reset\n"},
		{name: "claim missing json", args: []string{"claim", "--channel", "macos", "--consumer", "c", "--project", "p", "--limit", "1"}, want: "notifications claim: --channel, --consumer, --project, --limit (greater than zero), and --json are required\n"},
		{name: "claim all", args: []string{"claim", "--channel", "all", "--consumer", "c", "--project", "p", "--limit", "1", "--json"}, want: "notifications claim: --channel is required and must be macos or vscode\n"},
		{name: "ack missing entries", args: []string{"ack", "--lease", "l", "--json"}, want: "notifications ack: --lease, one or more --entry or positional entry IDs, and --json are required\n"},
		{name: "open missing token", args: []string{"open", "--json"}, want: "notifications open: exactly one route token is required\n"},
		{name: "open multiple tokens", args: []string{"open", "a", "b"}, want: "notifications open: exactly one route token is required\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeNotificationsBackend{}
			stdio, _, errOut := newIO()
			if code := cli.NewNotificationsCommand(backend).Run(context.Background(), test.args, stdio); code != 2 {
				t.Fatalf("code = %d, want 2", code)
			}
			if got := errOut.String(); got != test.want {
				t.Fatalf("stderr = %q, want %q", got, test.want)
			}
			if backend.called != "" {
				t.Fatalf("backend called on refusal: %s", backend.called)
			}
		})
	}
}

func TestNotificationsCommand_HelpNeverCallsBackend(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--help"},
		{"install", "--help"},
		{"status", "--help"},
		{"test", "--help"},
		{"uninstall", "--help"},
		{"preference", "--help"},
		{"open", "--help"},
		{"claim", "--help"},
		{"ack", "--help"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			stdio, out, errOut := newIO()
			if code := cli.NewNotificationsCommand(nil).Run(context.Background(), args, stdio); code != 0 {
				t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			if out.Len()+errOut.Len() == 0 {
				t.Fatal("help printed no output")
			}
		})
	}
}

func TestNotificationsCommand_JSONArraysAreNonNullAndLeaseCanBeNull(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "operation",
			args: []string{"install", "--json"},
			want: `{"project":null,"results":[],"failed":false}` + "\n",
		},
		{
			name: "status",
			args: []string{"status", "--json"},
			want: `{"available_channels":[],"installed_channels":[],"project":null,"level":{"line":"","new":0,"urgent":0,"stale":0,"update":"","severity":"","quiet":false},"offer":{"state":"","scope":"","remind_after":null},"components":[]}` + "\n",
		},
		{
			name: "claim",
			args: []string{"claim", "--channel", "macos", "--consumer", "c", "--project", "p", "--limit", "1", "--json"},
			want: `{"lease":null,"entries":[]}` + "\n",
		},
		{
			name: "ack",
			args: []string{"ack", "--lease", "l", "--entry", "e", "--json"},
			want: `{"acked":[],"already_acked":[]}` + "\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdio, out, errOut := newIO()
			code := cli.NewNotificationsCommand(&fakeNotificationsBackend{}).Run(
				context.Background(), test.args, stdio,
			)
			if code != 0 {
				t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
			}
			if got := out.String(); got != test.want {
				t.Fatalf("stdout = %q, want %q", got, test.want)
			}
			var decoded any
			if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
		})
	}
}

func TestNotificationsCommand_HumanOutputAndRuntimeError(t *testing.T) {
	t.Parallel()
	t.Run("operation", func(t *testing.T) {
		t.Parallel()
		backend := &fakeNotificationsBackend{operationOut: notification.OperationResult{
			Results: []notification.ChannelResult{
				{Channel: notification.ChannelMacOS, Status: "installed"},
				{Channel: notification.ChannelVSCode, Status: "skipped", Reason: "code not found"},
			},
		}}
		stdio, out, errOut := newIO()
		code := cli.NewNotificationsCommand(backend).Run(context.Background(), []string{"install"}, stdio)
		if code != 0 || errOut.Len() != 0 {
			t.Fatalf("code=%d stderr=%s", code, errOut.String())
		}
		if got, want := out.String(), "macos: installed\nvscode: skipped (code not found)\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
	t.Run("status", func(t *testing.T) {
		t.Parallel()
		backend := &fakeNotificationsBackend{statusOut: notification.Status{
			AvailableChannels: []notification.Channel{notification.ChannelMacOS, notification.ChannelVSCode},
			InstalledChannels: []notification.Channel{notification.ChannelMacOS},
			Offer:             notification.Offer{State: "ask"},
		}}
		stdio, out, _ := newIO()
		code := cli.NewNotificationsCommand(backend).Run(context.Background(), []string{"status"}, stdio)
		if code != 0 {
			t.Fatalf("code = %d, want 0", code)
		}
		if got, want := out.String(), "available: macos, vscode\ninstalled: macos\noffer: ask\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
	t.Run("backend error", func(t *testing.T) {
		t.Parallel()
		backend := &fakeNotificationsBackend{err: errors.New("permission denied")}
		stdio, out, errOut := newIO()
		code := cli.NewNotificationsCommand(backend).Run(context.Background(), []string{"status"}, stdio)
		if code != 1 || out.Len() != 0 {
			t.Fatalf("code=%d stdout=%s", code, out.String())
		}
		if got, want := errOut.String(), "notifications status: permission denied\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	})
	t.Run("structured operation failure", func(t *testing.T) {
		t.Parallel()
		backend := &fakeNotificationsBackend{operationOut: notification.OperationResult{
			Results: []notification.ChannelResult{{
				Channel: notification.ChannelMacOS, Status: "failed", Reason: "unsupported",
			}},
			Failed: true,
		}}
		stdio, out, errOut := newIO()
		code := cli.NewNotificationsCommand(backend).Run(
			context.Background(), []string{"install", "--json"}, stdio,
		)
		if code != 1 || errOut.Len() != 0 {
			t.Fatalf("code=%d stderr=%s", code, errOut.String())
		}
		if got, want := out.String(), "{\"project\":null,\"results\":[{\"channel\":\"macos\",\"status\":\"failed\",\"reason\":\"unsupported\"}],\"failed\":true}\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})
}
