package space

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// maxConfigBytes bounds every config/manifest file read (rails: "bounded
// reads everywhere").
const maxConfigBytes = 1 << 20 // 1 MiB

// defaultMirrorSubdir is the project-relative fallback mirror root used
// when neither a space's mirror-location key nor the machine config's
// mirror root resolves one (§7.4: "or `.a2a/cache/mirrors/`, gitignored").
const defaultMirrorSubdir = ".a2a/cache/mirrors"

// ProjectConfig is `.a2a/config.yaml` (§7.4): committed to the project
// repo, the whole project team inherits it. It names this project's own
// system id and every connected space (repo URL + mirror-location key).
// It never carries credentials.
type ProjectConfig struct {
	System string `yaml:"system"`
	Spaces []Ref  `yaml:"spaces"`
}

// Ref is one connected-space entry in ProjectConfig.
type Ref struct {
	ID string `yaml:"id"`
	// RepoURL is the space repo's git remote URL.
	RepoURL string `yaml:"repo_url"`
	// MirrorLocation is a key resolved against the machine config's
	// mirror root (§7.4); empty falls back to the project-relative
	// default (see ResolveMirrorLocation).
	MirrorLocation string `yaml:"mirror_location,omitempty"`
}

// MachineConfig is `~/.config/a2a/config.yaml` (§7.4): NEVER committed.
// Credentials here are REFERENCES only (env:<VAR> or cmd:<argv...>,
// portable superset of "keychain" — plan 05 Placement decision), never
// literal secrets; plus the mirror root directory and personal defaults.
type MachineConfig struct {
	MirrorRoot string `yaml:"mirror_root,omitempty"`
	// Credentials maps a space id to a credential reference string
	// ("env:VAR" or "cmd:argv...", validated at load time by
	// ParseCredentialReference — a malformed value fails the load loudly
	// rather than silently accepting a would-be literal secret).
	Credentials map[string]string `yaml:"credentials,omitempty"`
	Defaults    map[string]string `yaml:"defaults,omitempty"`
	// Notifications is personal, machine-local presentation state. It is
	// intentionally not part of ProjectConfig: one developer enabling the
	// macOS companion must not commit that choice for every clone.
	Notifications NotificationConfig `yaml:"notifications,omitempty"`
}

// NotificationConfig owns machine-global prompt policy and per-project
// enrollment. Component health/permissions are read from the platform and
// delivery cursors live in disposable cache, not here.
type NotificationConfig struct {
	PromptPolicy   NotificationPromptPolicy `yaml:"prompt_policy,omitempty"`
	Projects       []NotificationProject    `yaml:"projects,omitempty"`
	Components     []NotificationComponent  `yaml:"components,omitempty"`
	CoalesceWindow string                   `yaml:"coalesce_window,omitempty"`
	PollInterval   string                   `yaml:"poll_interval,omitempty"`
}

// NotificationComponent records only installs owned by a2a. It is what lets
// self-update repair the same VS Code profile/CLI without taking ownership of
// an extension the user installed by another mechanism.
type NotificationComponent struct {
	Channel  string `yaml:"channel"`
	Profile  string `yaml:"profile,omitempty"`
	CodePath string `yaml:"code_path,omitempty"`
	Version  string `yaml:"version,omitempty"`
}

// NotificationPromptPolicy is the only global setup-offer preference.
type NotificationPromptPolicy struct {
	State string `yaml:"state,omitempty"` // ask | never
}

// NotificationProject is one stable local registration. ID survives an
// explicit root move so consent and delivery state do not reset.
type NotificationProject struct {
	ID       string                    `yaml:"id"`
	Root     string                    `yaml:"root"`
	Channels []string                  `yaml:"channels,omitempty"`
	Prompt   NotificationProjectPrompt `yaml:"prompt,omitempty"`
	LastSeen time.Time                 `yaml:"last_seen,omitempty"`
}

// NotificationProjectPrompt is project-scoped offer state.
type NotificationProjectPrompt struct {
	State       string    `yaml:"state,omitempty"` // ask | snoozed | never
	RemindAfter time.Time `yaml:"remind_after,omitempty"`
}

// LoadProjectConfig reads and parses path as a ProjectConfig (bounded
// read).
func LoadProjectConfig(path string) (ProjectConfig, error) {
	const op = "LoadProjectConfig"
	raw, err := readBounded(path)
	if err != nil {
		return ProjectConfig{}, &Error{Op: op, Input: path, Err: err}
	}
	var cfg ProjectConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return ProjectConfig{}, &Error{Op: op, Input: path, Err: fmt.Errorf("%w: %w", ErrManifestInvalid, err)}
	}
	return cfg, nil
}

// LoadMachineConfig reads and parses path as a MachineConfig (bounded
// read), validating every credential reference's shape at load time (a
// value that isn't a well-formed env:/cmd: reference fails the load —
// this is the structural guard against a literal secret ever landing in
// this file, spec 05 §8 AC row 5).
func LoadMachineConfig(path string) (MachineConfig, error) {
	const op = "LoadMachineConfig"
	raw, err := readBounded(path)
	if err != nil {
		return MachineConfig{}, &Error{Op: op, Input: path, Err: err}
	}
	var cfg MachineConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return MachineConfig{}, &Error{Op: op, Input: path, Err: fmt.Errorf("%w: %w", ErrManifestInvalid, err)}
	}
	for space, ref := range cfg.Credentials {
		if _, err := ParseCredentialReference(ref); err != nil {
			return MachineConfig{}, &Error{Op: op, Input: space, Err: err}
		}
	}
	if err := validateNotificationConfig(cfg.Notifications); err != nil {
		return MachineConfig{}, &Error{Op: op, Input: path, Err: err}
	}
	return cfg, nil
}

func validateNotificationConfig(cfg NotificationConfig) error {
	if cfg.PromptPolicy.State != "" && cfg.PromptPolicy.State != "ask" && cfg.PromptPolicy.State != "never" {
		return fmt.Errorf("%w: notifications.prompt_policy.state must be ask or never", ErrManifestInvalid)
	}
	seen := make(map[string]bool, len(cfg.Projects))
	for _, project := range cfg.Projects {
		if project.ID == "" || !filepath.IsAbs(project.Root) {
			return fmt.Errorf("%w: notification project requires id and absolute root", ErrManifestInvalid)
		}
		if seen[project.ID] {
			return fmt.Errorf("%w: duplicate notification project id %q", ErrManifestInvalid, project.ID)
		}
		seen[project.ID] = true
		switch project.Prompt.State {
		case "", "ask", "snoozed", "never":
		default:
			return fmt.Errorf("%w: notification project %q has invalid prompt state %q", ErrManifestInvalid, project.ID, project.Prompt.State)
		}
		for _, channel := range project.Channels {
			if channel != "macos" && channel != "vscode" {
				return fmt.Errorf("%w: notification project %q has invalid channel %q", ErrManifestInvalid, project.ID, channel)
			}
		}
	}
	seenComponents := make(map[string]bool, len(cfg.Components))
	for _, component := range cfg.Components {
		if component.Channel != "macos" && component.Channel != "vscode" {
			return fmt.Errorf("%w: notification component has invalid channel %q", ErrManifestInvalid, component.Channel)
		}
		if component.Channel == "macos" && (component.Profile != "" || component.CodePath != "") {
			return fmt.Errorf("%w: macos notification component cannot have a VS Code profile or CLI path", ErrManifestInvalid)
		}
		if component.CodePath != "" && !filepath.IsAbs(component.CodePath) {
			return fmt.Errorf("%w: notification component code_path must be absolute", ErrManifestInvalid)
		}
		key := component.Channel + "\x00" + component.Profile + "\x00" + component.CodePath
		if seenComponents[key] {
			return fmt.Errorf("%w: duplicate managed notification component", ErrManifestInvalid)
		}
		seenComponents[key] = true
	}
	for name, raw := range map[string]string{
		"coalesce_window": cfg.CoalesceWindow,
		"poll_interval":   cfg.PollInterval,
	} {
		if raw == "" {
			continue
		}
		if d, err := time.ParseDuration(raw); err != nil || d <= 0 {
			return fmt.Errorf("%w: notifications.%s must be a positive duration", ErrManifestInvalid, name)
		}
	}
	return nil
}

// SaveMachineConfig atomically writes the typed machine config with
// user-only permissions. Mutating commands call this owner instead of
// hand-editing YAML.
func SaveMachineConfig(path string, cfg MachineConfig) error {
	const op = "SaveMachineConfig"
	if err := validateNotificationConfig(cfg.Notifications); err != nil {
		return &Error{Op: op, Input: path, Err: err}
	}
	raw, err := marshalMachineConfigPreservingExisting(path, cfg)
	if err != nil {
		return &Error{Op: op, Input: path, Err: err}
	}
	if len(raw) > maxConfigBytes {
		return &Error{Op: op, Input: path, Err: fmt.Errorf("machine config exceeds %d byte bound", maxConfigBytes)}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &Error{Op: op, Input: path, Err: err}
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return &Error{Op: op, Input: path, Err: err}
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return &Error{Op: op, Input: path, Err: err}
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return &Error{Op: op, Input: path, Err: err}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return &Error{Op: op, Input: path, Err: err}
	}
	if err := tmp.Close(); err != nil {
		return &Error{Op: op, Input: path, Err: err}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return &Error{Op: op, Input: path, Err: err}
	}
	return nil
}

// marshalMachineConfigPreservingExisting updates only the notifications
// mapping when a machine config already exists. That preserves unknown
// forward-compatible keys and operator comments instead of round-tripping
// the whole file through today's typed struct.
func marshalMachineConfigPreservingExisting(path string, cfg MachineConfig) ([]byte, error) {
	existing, err := readBounded(path)
	if err != nil {
		if os.IsNotExist(err) {
			return yaml.Marshal(cfg)
		}
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrManifestInvalid, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: machine config root must be a mapping", ErrManifestInvalid)
	}
	rawNotifications, err := yaml.Marshal(cfg.Notifications)
	if err != nil {
		return nil, err
	}
	var notificationDoc yaml.Node
	if err := yaml.Unmarshal(rawNotifications, &notificationDoc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	value := notificationDoc.Content[0]
	replaced := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "notifications" {
			root.Content[i+1] = value
			replaced = true
			break
		}
	}
	if !replaced {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "notifications"},
			value,
		)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// readBounded reads path with a size cap (rails: bounded reads
// everywhere) — a file at or over the cap is rejected rather than
// silently truncated, since a truncated config/manifest is worse than an
// explicit refusal.
func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only fd, close error is not actionable here

	limited := io.LimitReader(f, maxConfigBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("space: %s exceeds %d byte read bound", path, maxConfigBytes)
	}
	return raw, nil
}

// ResolveMirrorLocation resolves the local directory a space's mirror
// clone lives under (§7.4): ref.MirrorLocation joined onto the machine
// config's mirror root when both are set; otherwise the project-relative
// default `.a2a/cache/mirrors/<space-id>` under projectRoot.
func ResolveMirrorLocation(projectRoot string, ref Ref, machine MachineConfig) string {
	if ref.MirrorLocation != "" && machine.MirrorRoot != "" {
		return filepath.Join(machine.MirrorRoot, ref.MirrorLocation)
	}
	if ref.MirrorLocation != "" {
		// A location key without a configured machine mirror root still
		// resolves project-relatively, keyed by the given location
		// rather than the space id.
		return filepath.Join(projectRoot, defaultMirrorSubdir, ref.MirrorLocation)
	}
	return filepath.Join(projectRoot, defaultMirrorSubdir, ref.ID)
}

// CredentialReference is a parsed machine-config credential reference:
// env:<VAR> or cmd:<argv...> (plan 05 Placement decision — a portable
// superset of "keychain": a keychain lookup is itself a cmd: helper).
type CredentialReference struct {
	Kind string   // "env" | "cmd"
	Env  string   // set when Kind == "env"
	Argv []string // set when Kind == "cmd" (naive whitespace split — v1
	// limitation, no quoting: an argv element containing a space is not
	// representable; flagged as a known gap, not invented scope)
}

// ParseCredentialReference parses s into a CredentialReference, rejecting
// any value that isn't exactly "env:<VAR>" or "cmd:<argv...>" — this is
// the structural guard that keeps a literal secret from ever being
// accepted as a machine-config credential value.
func ParseCredentialReference(s string) (CredentialReference, error) {
	const op = "ParseCredentialReference"
	switch {
	case strings.HasPrefix(s, "env:"):
		env := strings.TrimPrefix(s, "env:")
		if env == "" {
			return CredentialReference{}, &Error{Op: op, Input: s, Err: ErrInvalidCredentialReference}
		}
		return CredentialReference{Kind: "env", Env: env}, nil
	case strings.HasPrefix(s, "cmd:"):
		rest := strings.TrimSpace(strings.TrimPrefix(s, "cmd:"))
		argv := strings.Fields(rest)
		if len(argv) == 0 {
			return CredentialReference{}, &Error{Op: op, Input: s, Err: ErrInvalidCredentialReference}
		}
		return CredentialReference{Kind: "cmd", Argv: argv}, nil
	default:
		return CredentialReference{}, &Error{Op: op, Input: s, Err: ErrInvalidCredentialReference}
	}
}
