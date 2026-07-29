package e2e

import (
	"bytes"
	"context"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/notification"
)

type notificationBackend struct {
	status notification.Status
}

func (b notificationBackend) Install(context.Context, notification.InstallRequest) (notification.OperationResult, error) {
	return notification.OperationResult{}, nil
}
func (b notificationBackend) Status(context.Context, notification.StatusRequest) (notification.Status, error) {
	return b.status, nil
}
func (b notificationBackend) Test(context.Context, notification.TestRequest) (notification.OperationResult, error) {
	return notification.OperationResult{}, nil
}
func (b notificationBackend) Uninstall(context.Context, notification.UninstallRequest) (notification.OperationResult, error) {
	return notification.OperationResult{}, nil
}
func (b notificationBackend) Preference(context.Context, notification.PreferenceRequest) (notification.PreferenceResult, error) {
	return notification.PreferenceResult{}, nil
}
func (b notificationBackend) Claim(context.Context, notification.ClaimRequest) (notification.ClaimResult, error) {
	return notification.ClaimResult{}, nil
}
func (b notificationBackend) Ack(context.Context, notification.AckRequest) (notification.AckResult, error) {
	return notification.AckResult{}, nil
}
func (b notificationBackend) Open(context.Context, notification.OpenRequest) (notification.OpenResult, error) {
	return notification.OpenResult{}, nil
}

func TestT3Notifications(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	command := cli.NewNotificationsCommand(notificationBackend{status: notification.Status{
		AvailableChannels: []notification.Channel{notification.ChannelVSCode},
		Offer:             notification.Offer{State: "ask", Scope: "project"},
		Level:             notification.Level{Quiet: true, Severity: "quiet"},
	}})
	code := command.Run(context.Background(), []string{"status", "--json"}, cli.IO{
		Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("notifications status: code=%d stderr=%q", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"offer":{"state":"ask"`)) {
		t.Fatalf("notifications status JSON = %s", stdout.String())
	}
}
