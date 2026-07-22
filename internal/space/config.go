package space

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	return cfg, nil
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
