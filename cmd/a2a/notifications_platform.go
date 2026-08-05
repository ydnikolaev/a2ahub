package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/notification"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

const vscodeExtensionID = "a2ahub.a2ahub-notifications"

type localComponentManager struct {
	version       string
	executable    string
	componentRoot string
	home          string
	registry      *notification.Registry
	repo          string
	localAssets   bool
	components    map[notification.Channel]release.CohortComponent
	assetPaths    map[notification.Channel]string
	run           func(context.Context, string, ...string) error
}

func newNotificationController(p paths) (*notification.Controller, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	registry := notification.NewRegistry(p.machineConfig, time.Now)
	repo := release.DefaultUpdateRepo
	if machine, loadErr := space.LoadMachineConfig(p.machineConfig); loadErr == nil && machine.Defaults["update_repo"] != "" {
		repo = machine.Defaults["update_repo"]
	}
	_, componentsDirConfigured := os.LookupEnv("A2A_COMPONENTS_DIR")
	localAssets := version == "dev" && componentsDirConfigured
	manager := &localComponentManager{
		version: version, executable: executable, home: home, registry: registry,
		componentRoot: notificationComponentRoot(home, version),
		repo:          repo, localAssets: localAssets,
		components: map[notification.Channel]release.CohortComponent{},
		assetPaths: map[notification.Channel]string{},
	}
	service := &notification.Service{
		Registry: registry,
		Delivery: notification.NewDeliveryStore(filepath.Join(cacheRoot, "a2a", "notifications"), time.Now),
		Probe:    manager,
	}
	service.Sources = func(project notification.Project) (notification.ProjectSource, error) {
		projectPaths := p
		projectPaths.projectRoot = project.Root
		projectPaths.projectConfig = filepath.Join(project.Root, ".a2a", "config.yaml")
		projectPaths.staging = filepath.Join(project.Root, ".a2a", "staging")
		return buildStore(context.Background(), projectPaths)
	}
	return &notification.Controller{
		Service: service, Components: manager, OpenRoute: openNotificationRoute,
	}, nil
}

func notificationComponentRoot(home, productVersion string) string {
	if configured := os.Getenv("A2A_COMPONENTS_DIR"); configured != "" && productVersion == "dev" {
		return filepath.Clean(configured)
	}
	return filepath.Join(home, ".local", "share", "a2a", "components", productVersion)
}

func (m *localComponentManager) Available(ctx context.Context) ([]notification.Channel, error) {
	var out []notification.Channel
	if runtime.GOOS == "darwin" {
		out = append(out, notification.ChannelMacOS)
	}
	if _, err := resolveCodeCLI(ctx, ""); err == nil {
		out = append(out, notification.ChannelVSCode)
	}
	return out, nil
}

func (m *localComponentManager) Installed(_ context.Context) ([]notification.Channel, error) {
	var out []notification.Channel
	if info, err := os.Stat(m.macAppPath()); err == nil && info.IsDir() {
		out = append(out, notification.ChannelMacOS)
	}
	// Ordinary status is an activation-time registry read, not a subprocess
	// probe. The ownership marker is the fast installed fact; explicit
	// `status --probe` and doctor inspect the editor/profile itself.
	machine, err := space.LoadMachineConfig(m.registry.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, component := range machine.Notifications.Components {
		if component.Channel == string(notification.ChannelVSCode) {
			out = appendUniqueNotificationChannel(out, notification.ChannelVSCode)
			break
		}
	}
	return out, nil
}

func appendUniqueNotificationChannel(channels []notification.Channel, channel notification.Channel) []notification.Channel {
	for _, existing := range channels {
		if existing == channel {
			return channels
		}
	}
	return append(channels, channel)
}

func (m *localComponentManager) Health(ctx context.Context) ([]notification.ComponentHealth, error) {
	machine, err := space.LoadMachineConfig(m.registry.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var health []notification.ComponentHealth
	if runtime.GOOS == "darwin" {
		item := notification.ComponentHealth{
			Channel: notification.ChannelMacOS, Available: true,
			Protocol: notification.SchemaVersion,
		}
		binary := filepath.Join(m.macAppPath(), "Contents", "MacOS", "A2ANotifier")
		if info, statErr := os.Stat(binary); statErr == nil && info.Mode().IsRegular() {
			item.Installed = true
			var status struct {
				ProductVersion  string `json:"product_version"`
				ProtocolVersion int    `json:"protocol_version"`
				Permission      string `json:"permission"`
				LoginItem       string `json:"login_item"`
				CLIHandshake    string `json:"cli_handshake"`
			}
			raw, probeErr := runBoundedOutput(ctx, 3*time.Second, binary, "--status", "--config", m.macConfigPath())
			if probeErr != nil {
				item.Handshake = "failed"
				item.Detail = probeErr.Error()
			} else if len(raw) > 0 {
				if json.Unmarshal(raw, &status) == nil {
					item.Version = status.ProductVersion
					item.Protocol = status.ProtocolVersion
					item.Permission = status.Permission
					item.LoginItem = status.LoginItem
					item.Handshake = status.CLIHandshake
				}
			}
		}
		health = append(health, item)
	}

	managedVSCode := make([]space.NotificationComponent, 0)
	for _, component := range machine.Notifications.Components {
		if component.Channel == string(notification.ChannelVSCode) {
			managedVSCode = append(managedVSCode, component)
		}
	}
	if len(managedVSCode) == 0 {
		managedVSCode = append(managedVSCode, space.NotificationComponent{Channel: string(notification.ChannelVSCode)})
	}
	for _, component := range managedVSCode {
		item := notification.ComponentHealth{
			Channel: notification.ChannelVSCode, Profile: component.Profile,
			CodePath: component.CodePath, Version: component.Version,
			Protocol: notification.SchemaVersion,
		}
		code, resolveErr := resolveCodeCLI(ctx, component.CodePath)
		if resolveErr == nil {
			item.Available = true
			item.CodePath = code
			args := []string{"--list-extensions", "--show-versions"}
			if component.Profile != "" {
				args = append(args, "--profile", component.Profile)
			}
			if raw, listErr := runBoundedOutput(ctx, 3*time.Second, code, args...); listErr == nil {
				for _, line := range strings.Split(string(raw), "\n") {
					fields := strings.SplitN(strings.TrimSpace(line), "@", 2)
					if strings.EqualFold(fields[0], vscodeExtensionID) {
						item.Installed = true
						if len(fields) == 2 {
							item.Version = fields[1]
						}
						item.Handshake = notificationComponentHandshake(item.Version, m.version)
						break
					}
				}
			}
		} else {
			item.Detail = resolveErr.Error()
		}
		health = append(health, item)
	}
	sort.Slice(health, func(i, j int) bool {
		left, right := health[i], health[j]
		return string(left.Channel)+"\x00"+left.Profile+"\x00"+left.CodePath <
			string(right.Channel)+"\x00"+right.Profile+"\x00"+right.CodePath
	})
	return health, nil
}

func notificationComponentHandshake(componentVersion, cliVersion string) string {
	if componentVersion == "" {
		return ""
	}
	if strings.TrimPrefix(componentVersion, "v") == strings.TrimPrefix(cliVersion, "v") {
		return "ok"
	}
	return "mismatch"
}

func (m *localComponentManager) Configure(_ context.Context, projects []notification.Project) error {
	macEnrolled := make([]macProjectConfig, 0, len(projects))
	vscodeEnrolled := make([]macProjectConfig, 0, len(projects))
	for _, project := range projects {
		if hasNotificationChannel(project.Channels, notification.ChannelMacOS) {
			macEnrolled = append(macEnrolled, macProjectConfig{ID: project.ID, Root: project.Root})
		}
		if hasNotificationChannel(project.Channels, notification.ChannelVSCode) {
			vscodeEnrolled = append(vscodeEnrolled, macProjectConfig{ID: project.ID, Root: project.Root})
		}
	}
	sort.Slice(macEnrolled, func(i, j int) bool { return macEnrolled[i].ID < macEnrolled[j].ID })
	sort.Slice(vscodeEnrolled, func(i, j int) bool { return vscodeEnrolled[i].ID < vscodeEnrolled[j].ID })
	macConfig := macNotifierConfig{
		SchemaVersion: 1, CLIPath: m.executable, CLIVersion: m.version,
		ConsumerID:          "macos-" + localConsumerHash(m.macConfigPath()),
		PollIntervalSeconds: 60, Projects: macEnrolled,
	}
	if err := writePrivateJSON(m.macConfigPath(), macConfig); err != nil {
		return fmt.Errorf("configure macOS notifier: %w", err)
	}
	vscodeConfig := vscodeNotifierConfig{
		SchemaVersion: 1, CLIPath: m.executable, CLIVersion: m.version,
		ProtocolVersion: notification.SchemaVersion, Projects: vscodeEnrolled,
	}
	if err := writePrivateJSON(m.vscodeConfigPath(), vscodeConfig); err != nil {
		return fmt.Errorf("configure VS Code notifier: %w", err)
	}
	return nil
}

func (m *localComponentManager) Install(ctx context.Context, channel notification.Channel, options notification.ComponentOptions) (notification.ChannelResult, error) {
	if err := m.ensureComponent(ctx, channel); err != nil {
		return notification.ChannelResult{Channel: channel, Status: "failed"}, err
	}
	var (
		result notification.ChannelResult
		err    error
	)
	switch channel {
	case notification.ChannelMacOS:
		result, err = m.installMacOS(ctx)
	case notification.ChannelVSCode:
		result, err = m.installVSCode(ctx, options)
	default:
		return notification.ChannelResult{}, notification.ErrInvalidChannel
	}
	if err != nil || result.Status == "failed" {
		return result, err
	}
	if rememberErr := m.registry.RememberComponent(channel, options.Profile, options.CodePath, strings.TrimPrefix(m.version, "v")); rememberErr != nil {
		return notification.ChannelResult{Channel: channel, Status: "failed"}, fmt.Errorf("record managed component: %w", rememberErr)
	}
	return result, nil
}

func (m *localComponentManager) Test(ctx context.Context, channel notification.Channel, _ notification.Project) (notification.ChannelResult, error) {
	switch channel {
	case notification.ChannelMacOS:
		binary := filepath.Join(m.macAppPath(), "Contents", "MacOS", "A2ANotifier")
		if err := runBounded(ctx, binary, "--test", "--config", m.macConfigPath()); err != nil {
			return notification.ChannelResult{Channel: channel, Status: "failed"}, err
		}
		return notification.ChannelResult{Channel: channel, Status: "delivered"}, nil
	case notification.ChannelVSCode:
		installed, err := m.Installed(ctx)
		if err != nil {
			return notification.ChannelResult{Channel: channel, Status: "failed"}, err
		}
		if !hasNotificationChannel(installed, channel) {
			return notification.ChannelResult{Channel: channel, Status: "failed"}, errors.New("VS Code extension is not installed")
		}
		return notification.ChannelResult{Channel: channel, Status: "ready", Reason: "use A2A: Refresh Notifications in VS Code"}, nil
	default:
		return notification.ChannelResult{}, notification.ErrInvalidChannel
	}
}

func (m *localComponentManager) Uninstall(ctx context.Context, channel notification.Channel, options notification.ComponentOptions, purge bool) (notification.ChannelResult, error) {
	var (
		result notification.ChannelResult
		err    error
	)
	switch channel {
	case notification.ChannelMacOS:
		result, err = m.uninstallMacOS(ctx, purge)
	case notification.ChannelVSCode:
		result, err = m.uninstallVSCode(ctx, options, purge)
	default:
		return notification.ChannelResult{}, notification.ErrInvalidChannel
	}
	if err != nil || result.Status == "failed" {
		return result, err
	}
	if forgetErr := m.registry.ForgetComponent(channel, options.Profile, options.CodePath); forgetErr != nil {
		return notification.ChannelResult{Channel: channel, Status: "failed"}, fmt.Errorf("forget managed component: %w", forgetErr)
	}
	return result, nil
}

func (m *localComponentManager) ensureComponent(ctx context.Context, channel notification.Channel) error {
	if m.localAssets {
		return nil
	}
	if m.version == "dev" {
		return errors.New("development builds require A2A_COMPONENTS_DIR with locally built companion assets")
	}
	kind := release.ComponentVSCode
	if channel == notification.ChannelMacOS {
		kind = release.ComponentMacOS
	}
	source := release.NewGitHubSource(http.DefaultClient, "", m.repo)
	rel, err := source.Latest(ctx)
	if err != nil {
		return fmt.Errorf("resolve companion release: %w", err)
	}
	productVersion := strings.TrimPrefix(m.version, "v")
	if rel.Version != productVersion {
		return fmt.Errorf("installed a2a %s has no current companion cohort (latest is %s); run `a2a update --yes` first", productVersion, rel.Version)
	}
	component, assetPath, err := release.FetchVerifiedComponent(
		ctx, http.DefaultClient, source.Token, rel, kind, notification.SchemaVersion,
		m.componentRoot, release.KeylessVerifier(m.repo),
	)
	if err != nil {
		return fmt.Errorf("prepare %s companion: %w", channel, err)
	}
	m.components[channel] = component
	m.assetPaths[channel] = assetPath
	switch channel {
	case notification.ChannelMacOS:
		return m.extractMacApp(ctx, assetPath, component)
	case notification.ChannelVSCode:
		return verifyVSIX(assetPath, component)
	default:
		return notification.ErrInvalidChannel
	}
}

func (m *localComponentManager) extractMacApp(ctx context.Context, archivePath string, component release.CohortComponent) error {
	stage, err := os.MkdirTemp(m.componentRoot, ".mac-app-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := runBounded(ctx, "/usr/bin/ditto", "-x", "-k", archivePath, stage); err != nil {
		return err
	}
	app := filepath.Join(stage, "A2A Notifier.app")
	if err := verifyMacBundle(ctx, app, component.ProductVersion, component.TeamID); err != nil {
		return err
	}
	target := filepath.Join(m.componentRoot, "A2A Notifier.app")
	if _, err := os.Stat(target); err == nil {
		previous := target + ".previous"
		if err := os.RemoveAll(previous); err != nil {
			return err
		}
		if err := os.Rename(target, previous); err != nil {
			return err
		}
	}
	return os.Rename(app, target)
}

func verifyVSIX(path string, component release.CohortComponent) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return errors.New("VSIX is missing, non-regular, or oversized")
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open VSIX: %w", err)
	}
	defer func() { _ = archive.Close() }()
	for _, file := range archive.File {
		if file.Name != "extension/package.json" {
			continue
		}
		if file.UncompressedSize64 > 1<<20 {
			return errors.New("VSIX package manifest is oversized")
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		raw, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
		_ = reader.Close()
		if readErr != nil {
			return readErr
		}
		var manifest struct {
			Name      string `json:"name"`
			Publisher string `json:"publisher"`
			Version   string `json:"version"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("decode VSIX package manifest: %w", err)
		}
		if manifest.Publisher+"."+manifest.Name != component.ExtensionID ||
			manifest.Publisher != component.Publisher ||
			manifest.Version != component.ProductVersion {
			return errors.New("VSIX publisher, extension ID, or product version mismatch")
		}
		return nil
	}
	return errors.New("VSIX has no extension/package.json")
}

func (m *localComponentManager) installMacOS(ctx context.Context) (notification.ChannelResult, error) {
	if runtime.GOOS != "darwin" {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, errors.New("macOS notifications require macOS 13+")
	}
	source := filepath.Join(m.componentRoot, "A2A Notifier.app")
	expectedTeam := ""
	if component, ok := m.components[notification.ChannelMacOS]; ok {
		expectedTeam = component.TeamID
	}
	if err := verifyMacBundle(ctx, source, m.version, expectedTeam); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	dest := m.macAppPath()
	status := "installed"
	if _, err := os.Stat(dest); err == nil {
		status = "repaired"
	}
	applications := filepath.Dir(dest)
	if err := os.MkdirAll(applications, 0o755); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	stage, err := os.MkdirTemp(applications, ".a2a-notifier-stage-*")
	if err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	stagedApp := filepath.Join(stage, "A2A Notifier.app")
	if err := runBounded(ctx, "/usr/bin/ditto", source, stagedApp); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	backup := dest + ".previous"
	hadPrevious := false
	if _, err := os.Stat(dest); err == nil {
		if err := os.RemoveAll(backup); err != nil {
			return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
		}
		if err := os.Rename(dest, backup); err != nil {
			return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
		}
		hadPrevious = true
	}
	if err := os.Rename(stagedApp, dest); err != nil {
		if rollbackErr := rollbackMacInstall(dest, backup, hadPrevious); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("restore previous macOS notifier: %w", rollbackErr))
		}
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	binary := filepath.Join(dest, "Contents", "MacOS", "A2ANotifier")
	if err := runBounded(ctx, binary, "--install", "--config", m.macConfigPath()); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 4 {
			// Permission denial is not a broken install: keep the verified app
			// in place so System Settings can grant it later.
			return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "approval-required", Reason: err.Error()}, nil
		}
		if reason, preserve := adHocMacApprovalReason(expectedTeam, dest, err); preserve {
			// Gatekeeper may refuse the first launch of an app with no Developer
			// ID. Keep the exact cohort-verified app in place so the user can
			// make the explicit Open Anyway decision, then safely retry.
			return notification.ChannelResult{
				Channel: notification.ChannelMacOS, Status: "approval-required", Reason: reason,
			}, nil
		}
		if rollbackErr := rollbackMacInstall(dest, backup, hadPrevious); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("restore previous macOS notifier: %w", rollbackErr))
		}
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	if status == "installed" {
		if err := runBounded(ctx, binary, "--test", "--config", m.macConfigPath()); err != nil {
			if rollbackErr := rollbackMacInstall(dest, backup, hadPrevious); rollbackErr != nil {
				err = errors.Join(err, fmt.Errorf("restore previous macOS notifier: %w", rollbackErr))
			}
			return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed", Reason: "installed but readiness notification failed"}, err
		}
	}
	reason := ""
	if hadPrevious {
		reason = "previous signed app retained at " + backup
	}
	return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: status, Reason: reason}, nil
}

func adHocMacApprovalReason(expectedTeamID, appPath string, launchErr error) (string, bool) {
	if expectedTeamID != release.MacOSAdHocTeamID || launchErr == nil {
		return "", false
	}
	return fmt.Sprintf(
		"macOS did not launch the ad-hoc app: %v; open %s once, approve it under System Settings > Privacy & Security > Open Anyway, then repeat `a2a notifications install --channel macos`",
		launchErr, appPath,
	), true
}

func rollbackMacInstall(destination, backup string, hadPrevious bool) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	if !hadPrevious {
		return nil
	}
	return os.Rename(backup, destination)
}

func (m *localComponentManager) installVSCode(ctx context.Context, options notification.ComponentOptions) (notification.ChannelResult, error) {
	code, err := resolveCodeCLI(ctx, options.CodePath)
	if err != nil {
		return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "failed"}, err
	}
	vsix := m.assetPaths[notification.ChannelVSCode]
	if vsix == "" {
		vsix = filepath.Join(m.componentRoot, "a2ahub-notifications.vsix")
	}
	if info, err := os.Stat(vsix); err != nil || !info.Mode().IsRegular() {
		return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "failed"}, fmt.Errorf("verified VSIX is missing at %s", vsix)
	}
	args := []string{"--install-extension", vsix, "--force"}
	if options.Profile != "" {
		args = append(args, "--profile", options.Profile)
	}
	if err := m.runCommand(ctx, code, args...); err != nil {
		if previous := m.previousManagedVSIX(options); previous != "" {
			rollbackArgs := []string{"--install-extension", previous, "--force"}
			if options.Profile != "" {
				rollbackArgs = append(rollbackArgs, "--profile", options.Profile)
			}
			if rollbackErr := m.runCommand(ctx, code, rollbackArgs...); rollbackErr == nil {
				return notification.ChannelResult{
					Channel: notification.ChannelVSCode, Status: "failed",
					Reason: "new VSIX failed; previous managed VSIX restored",
				}, err
			}
			return notification.ChannelResult{
				Channel: notification.ChannelVSCode, Status: "failed",
				Reason: "new VSIX and previous managed VSIX restore both failed",
			}, err
		}
		return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "failed"}, err
	}
	return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "installed"}, nil
}

func (m *localComponentManager) runCommand(ctx context.Context, executable string, args ...string) error {
	if m.run != nil {
		return m.run(ctx, executable, args...)
	}
	return runBounded(ctx, executable, args...)
}

func (m *localComponentManager) previousManagedVSIX(options notification.ComponentOptions) string {
	if m.localAssets {
		return ""
	}
	machine, err := space.LoadMachineConfig(m.registry.Path)
	if err != nil {
		return ""
	}
	for _, component := range machine.Notifications.Components {
		if component.Channel != string(notification.ChannelVSCode) ||
			component.Profile != options.Profile ||
			component.CodePath != options.CodePath ||
			component.Version == "" ||
			strings.TrimPrefix(component.Version, "v") == strings.TrimPrefix(m.version, "v") {
			continue
		}
		path := filepath.Join(
			m.home, ".local", "share", "a2a", "components",
			strings.TrimPrefix(component.Version, "v"),
			"a2ahub-notifications.vsix",
		)
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func (m *localComponentManager) uninstallMacOS(ctx context.Context, purge bool) (notification.ChannelResult, error) {
	app := m.macAppPath()
	binary := filepath.Join(app, "Contents", "MacOS", "A2ANotifier")
	if _, err := os.Stat(binary); errors.Is(err, os.ErrNotExist) {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "not-installed"}, nil
	}
	if err := runBounded(ctx, binary, "--unregister", "--config", m.macConfigPath()); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	trash := filepath.Join(m.home, ".Trash")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	target := filepath.Join(trash, fmt.Sprintf("A2A Notifier %d.app", time.Now().Unix()))
	if err := os.Rename(app, target); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "failed"}, err
	}
	if purge {
		_ = os.Remove(m.macConfigPath())
	}
	return notification.ChannelResult{Channel: notification.ChannelMacOS, Status: "uninstalled", Reason: "moved to Trash"}, nil
}

func (m *localComponentManager) uninstallVSCode(ctx context.Context, options notification.ComponentOptions, purge bool) (notification.ChannelResult, error) {
	code, err := resolveCodeCLI(ctx, options.CodePath)
	if err != nil {
		return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "failed"}, err
	}
	args := []string{"--uninstall-extension", vscodeExtensionID}
	if options.Profile != "" {
		args = append(args, "--profile", options.Profile)
	}
	if err := runBounded(ctx, code, args...); err != nil {
		return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "failed"}, err
	}
	if purge {
		_ = os.Remove(m.vscodeConfigPath())
	}
	return notification.ChannelResult{Channel: notification.ChannelVSCode, Status: "uninstalled"}, nil
}

func verifyMacBundle(ctx context.Context, path, expectedVersion, expectedTeamID string) error {
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return fmt.Errorf("verified macOS app is missing at %s", path)
	}
	plist := filepath.Join(path, "Contents", "Info.plist")
	bundleID, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", plist).Output()
	if err != nil || strings.TrimSpace(string(bundleID)) != "io.a2ahub.notifier" {
		return errors.New("macOS app bundle identifier mismatch")
	}
	productVersion, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", plist).Output()
	if err != nil || strings.TrimSpace(string(productVersion)) != strings.TrimPrefix(expectedVersion, "v") {
		return errors.New("macOS app product version mismatch")
	}
	if err := runBounded(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", path); err != nil {
		return fmt.Errorf("macOS app signature verification: %w", err)
	}
	if expectedTeamID != "" {
		detail, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", path).CombinedOutput()
		if err != nil || !strings.Contains(string(detail), "\nTeamIdentifier="+expectedTeamID+"\n") {
			return errors.New("macOS app signing team mismatch")
		}
	}
	return nil
}

func resolveCodeCLI(ctx context.Context, selected string) (string, error) {
	if selected != "" {
		if !filepath.IsAbs(selected) {
			return "", errors.New("--code must be an absolute executable path")
		}
		info, err := os.Stat(selected)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return "", fmt.Errorf("VS Code CLI is not executable: %s", selected)
		}
		return filepath.Clean(selected), nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	path, err := exec.LookPath("code")
	if err != nil {
		return "", errors.New("canonical Visual Studio Code CLI `code` is unavailable; pass --code explicitly")
	}
	select {
	case <-cmdCtx.Done():
		return "", cmdCtx.Err()
	default:
		return filepath.Clean(path), nil
	}
}

func runBounded(ctx context.Context, executable string, args ...string) error {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s timed out", filepath.Base(executable))
		}
		return fmt.Errorf("%s failed: %w", filepath.Base(executable), err)
	}
	return nil
}

func runBoundedOutput(ctx context.Context, timeout time.Duration, executable string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(runCtx, executable, args...).Output()
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return output, fmt.Errorf("%s timed out", filepath.Base(executable))
	}
	if err != nil {
		return output, fmt.Errorf("%s failed: %w", filepath.Base(executable), err)
	}
	return output, nil
}

func openNotificationRoute(ctx context.Context, route notification.Route, project notification.Project) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"html"}
	if route.Kind == "update" {
		args = append(args, "--update")
	} else if route.SpaceID != "" && route.ArtifactID != "" {
		args = append(args, "--focus", route.SpaceID+":"+route.ArtifactID)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = project.Root
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func writePrivateJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > 256*1024 {
		return errors.New("notification component config exceeds 256 KiB")
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func localConsumerHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

func hasNotificationChannel(channels []notification.Channel, want notification.Channel) bool {
	for _, channel := range channels {
		if channel == want {
			return true
		}
	}
	return false
}

func (m *localComponentManager) macAppPath() string {
	return filepath.Join(m.home, "Applications", "A2A Notifier.app")
}

func (m *localComponentManager) macConfigPath() string {
	return filepath.Join(m.home, "Library", "Application Support", "io.a2ahub.notifier", "config.json")
}

func (m *localComponentManager) vscodeConfigPath() string {
	return filepath.Join(m.home, ".config", "a2a", "vscode-notifier.json")
}

type macProjectConfig struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

type macNotifierConfig struct {
	SchemaVersion       int                `json:"schema_version"`
	CLIPath             string             `json:"cli_path"`
	CLIVersion          string             `json:"cli_version"`
	ConsumerID          string             `json:"consumer_id"`
	PollIntervalSeconds int                `json:"poll_interval_seconds"`
	Projects            []macProjectConfig `json:"projects"`
}

type vscodeNotifierConfig struct {
	SchemaVersion   int                `json:"schema_version"`
	CLIPath         string             `json:"cli_path"`
	CLIVersion      string             `json:"cli_version"`
	ProtocolVersion int                `json:"protocol_version"`
	Projects        []macProjectConfig `json:"projects"`
}
