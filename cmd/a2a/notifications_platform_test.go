package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/notification"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestVerifyVSIXPinsPublisherExtensionAndVersion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "extension.vsix")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	manifest, err := archive.Create("extension/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Write([]byte(`{"name":"a2ahub-notifications","publisher":"a2ahub","version":"1.2.3"}`)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	component := release.CohortComponent{
		Publisher: "a2ahub", ExtensionID: "a2ahub.a2ahub-notifications",
		ProductVersion: "1.2.3",
	}
	if err := verifyVSIX(path, component); err != nil {
		t.Fatalf("verifyVSIX valid: %v", err)
	}
	component.ProductVersion = "1.2.4"
	if err := verifyVSIX(path, component); err == nil {
		t.Fatal("verifyVSIX accepted a mismatched product version")
	}
}

func TestNotificationComponentHandshakeRequiresExactCohortVersion(t *testing.T) {
	t.Parallel()
	if got := notificationComponentHandshake("1.2.3", "v1.2.3"); got != "ok" {
		t.Fatalf("matching handshake = %q", got)
	}
	if got := notificationComponentHandshake("1.2.2", "1.2.3"); got != "mismatch" {
		t.Fatalf("mismatched handshake = %q", got)
	}
	if got := notificationComponentHandshake("", "1.2.3"); got != "" {
		t.Fatalf("unknown handshake = %q", got)
	}
}

func TestPreviousManagedVSIXUsesExactOwnedProfileAndCLI(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	config := filepath.Join(home, "config.yaml")
	if err := space.SaveMachineConfig(config, space.MachineConfig{
		Notifications: space.NotificationConfig{
			Components: []space.NotificationComponent{{
				Channel: "vscode", Profile: "Work", CodePath: "/opt/code", Version: "1.2.2",
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(home, ".local", "share", "a2a", "components", "1.2.2", "a2ahub-notifications.vsix")
	if err := os.MkdirAll(filepath.Dir(previous), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &localComponentManager{
		version: "1.2.3", home: home, registry: notification.NewRegistry(config, nil),
	}

	got := manager.previousManagedVSIX(notification.ComponentOptions{Profile: "Work", CodePath: "/opt/code"})
	if got != previous {
		t.Fatalf("previousManagedVSIX = %q, want %q", got, previous)
	}
	if got := manager.previousManagedVSIX(notification.ComponentOptions{Profile: "Other", CodePath: "/opt/code"}); got != "" {
		t.Fatalf("foreign profile resolved rollback asset %q", got)
	}
}

func TestReleaseBuildIgnoresLocalComponentOverride(t *testing.T) {
	t.Setenv("A2A_COMPONENTS_DIR", filepath.Join(t.TempDir(), "unsigned"))
	home := t.TempDir()
	got := notificationComponentRoot(home, "1.2.3")
	want := filepath.Join(home, ".local", "share", "a2a", "components", "1.2.3")
	if got != want {
		t.Fatalf("release component root = %q, want signed cohort root %q", got, want)
	}
	if got := notificationComponentRoot(home, "dev"); got != os.Getenv("A2A_COMPONENTS_DIR") {
		t.Fatalf("dev component root = %q, want explicit local override", got)
	}
}

func TestConfigureWritesEveryEnrolledVSCodeProject(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	manager := &localComponentManager{
		version: "1.2.3", executable: "/usr/local/bin/a2a", home: home,
	}
	projects := []notification.Project{
		{ID: "b", Root: "/repo/b", Channels: []notification.Channel{notification.ChannelVSCode}},
		{ID: "a", Root: "/repo/a", Channels: []notification.Channel{notification.ChannelMacOS, notification.ChannelVSCode}},
		{ID: "c", Root: "/repo/c", Channels: []notification.Channel{notification.ChannelMacOS}},
	}
	if err := manager.Configure(context.Background(), projects); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	raw, err := os.ReadFile(manager.vscodeConfigPath())
	if err != nil {
		t.Fatalf("read VS Code config: %v", err)
	}
	var got vscodeNotifierConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode VS Code config: %v", err)
	}
	want := []macProjectConfig{{ID: "a", Root: "/repo/a"}, {ID: "b", Root: "/repo/b"}}
	if !reflect.DeepEqual(got.Projects, want) {
		t.Fatalf("VS Code projects = %+v, want %+v", got.Projects, want)
	}
}

func TestVSCodeInstallFailureRestoresExactOwnedPreviousVSIX(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codePath := filepath.Join(t.TempDir(), "code")
	if err := os.WriteFile(codePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "config.yaml")
	if err := space.SaveMachineConfig(config, space.MachineConfig{
		Notifications: space.NotificationConfig{
			Components: []space.NotificationComponent{{
				Channel: "vscode", Profile: "Work", CodePath: codePath, Version: "1.2.2",
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(home, ".local", "share", "a2a", "components", "1.2.2", "a2ahub-notifications.vsix")
	current := filepath.Join(t.TempDir(), "a2ahub-notifications.vsix")
	for path, contents := range map[string]string{previous: "old", current: "new"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var installed []string
	manager := &localComponentManager{
		version: "1.2.3", home: home, registry: notification.NewRegistry(config, nil),
		assetPaths: map[notification.Channel]string{notification.ChannelVSCode: current},
		run: func(_ context.Context, _ string, args ...string) error {
			installed = append(installed, args[1])
			if args[1] == current {
				return errors.New("injected new VSIX failure")
			}
			return nil
		},
	}
	result, err := manager.installVSCode(context.Background(), notification.ComponentOptions{
		Profile: "Work", CodePath: codePath,
	})
	if err == nil || result.Status != "failed" || len(installed) != 2 ||
		installed[0] != current || installed[1] != previous {
		t.Fatalf("result=%+v err=%v installs=%v", result, err, installed)
	}
	if result.Reason != "new VSIX failed; previous managed VSIX restored" {
		t.Fatalf("rollback reason = %q", result.Reason)
	}
}

func TestRollbackMacInstallRestoresPreviousAndReportsMissingBackup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	destination := filepath.Join(root, "A2A Notifier.app")
	backup := destination + ".previous"
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "sentinel"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rollbackMacInstall(destination, backup, true); err != nil {
		t.Fatalf("rollbackMacInstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "sentinel")); err != nil {
		t.Fatalf("previous app was not restored: %v", err)
	}
	if err := rollbackMacInstall(destination, backup, true); err == nil {
		t.Fatal("missing backup restore was silently accepted")
	}
}

func TestAdHocMacLaunchFailureKeepsAppForExplicitApproval(t *testing.T) {
	t.Parallel()
	app := "/Users/test/Applications/A2A Notifier.app"
	reason, preserve := adHocMacApprovalReason(release.MacOSAdHocTeamID, app, errors.New("signal: killed"))
	if !preserve {
		t.Fatal("ad-hoc launch failure would roll back the app before the user can approve it")
	}
	for _, want := range []string{app, "Privacy & Security > Open Anyway", "a2a notifications install --channel macos"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("approval reason %q does not contain %q", reason, want)
		}
	}
	if reason, preserve := adHocMacApprovalReason("A2ADEVTEAM", app, errors.New("crash")); preserve || reason != "" {
		t.Fatalf("Developer ID failure was misclassified as Gatekeeper approval: preserve=%v reason=%q", preserve, reason)
	}
}
