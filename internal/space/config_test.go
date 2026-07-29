package space

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestMirrorLocationResolution is spec 05 §8 AC row 7: mirror clone
// location resolves per the project config's per-space mirror-location
// key.
func TestMirrorLocationResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		projectRoot string
		ref         Ref
		machine     MachineConfig
		want        string
	}{
		{
			name:        "location key + machine mirror root",
			projectRoot: "/proj",
			ref:         Ref{ID: "getvisa", MirrorLocation: "getvisa-mirror"},
			machine:     MachineConfig{MirrorRoot: "/home/u/spaces"},
			want:        filepath.Join("/home/u/spaces", "getvisa-mirror"),
		},
		{
			name:        "location key, no machine mirror root: project-relative default keyed by location",
			projectRoot: "/proj",
			ref:         Ref{ID: "getvisa", MirrorLocation: "getvisa-mirror"},
			machine:     MachineConfig{},
			want:        filepath.Join("/proj", defaultMirrorSubdir, "getvisa-mirror"),
		},
		{
			name:        "no location key: project-relative default keyed by space id",
			projectRoot: "/proj",
			ref:         Ref{ID: "getvisa"},
			machine:     MachineConfig{MirrorRoot: "/home/u/spaces"},
			want:        filepath.Join("/proj", defaultMirrorSubdir, "getvisa"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveMirrorLocation(tc.projectRoot, tc.ref, tc.machine)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseCredentialReference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    CredentialReference
		wantErr bool
	}{
		{name: "env", in: "env:A2A_GITHUB_TOKEN", want: CredentialReference{Kind: "env", Env: "A2A_GITHUB_TOKEN"}},
		{name: "cmd", in: "cmd:op read op://vault/item/token", want: CredentialReference{Kind: "cmd", Argv: []string{"op", "read", "op://vault/item/token"}}},
		{name: "empty env name", in: "env:", wantErr: true},
		{name: "empty cmd argv", in: "cmd:", wantErr: true},
		{name: "literal-looking value rejected", in: "ghp_abcdef1234567890", wantErr: true},
		{name: "empty string rejected", in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCredentialReference(tc.in)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidCredentialReference) {
					t.Fatalf("ParseCredentialReference(%q) error = %v, want ErrInvalidCredentialReference", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCredentialReference(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseCredentialReference(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCredentialNeverInConfig is spec 05 §8 AC row 5: credentials are
// resolved from env/keychain at call time and never appear, literally, in
// either config file on disk — round-trip serialize/deserialize asserts
// no secret-shaped field ever serializes.
func TestCredentialNeverInConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	machinePath := filepath.Join(dir, "config.yaml")
	projectPath := filepath.Join(dir, "project.yaml")

	writeYAML(t, projectPath, ProjectConfig{
		System: "axon",
		Spaces: []Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git", MirrorLocation: "getvisa-mirror"}},
	})
	writeYAML(t, machinePath, MachineConfig{
		MirrorRoot:  "/home/u/spaces",
		Credentials: map[string]string{"getvisa": "env:A2A_GITHUB_TOKEN_GETVISA"},
	})

	proj, err := LoadProjectConfig(projectPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	machine, err := LoadMachineConfig(machinePath)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if proj.System != "axon" || machine.MirrorRoot != "/home/u/spaces" {
		t.Fatalf("round-trip mismatch: proj=%+v machine=%+v", proj, machine)
	}

	raw, err := os.ReadFile(machinePath)
	if err != nil {
		t.Fatalf("read machine config: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "env:A2A_GITHUB_TOKEN_GETVISA") {
		t.Fatal("expected the credential REFERENCE string to be present in the file")
	}
	// No config value may be anything other than an env:/cmd: reference —
	// LoadMachineConfig itself enforces this at load time (see
	// TestLoadMachineConfigRejectsLiteralSecret); this is the structural
	// half of the AC (the type has no room for a raw token field at all).
	for _, ref := range machine.Credentials {
		if _, err := ParseCredentialReference(ref); err != nil {
			t.Fatalf("stored credential value %q is not a valid reference: %v", ref, err)
		}
	}
}

func TestLoadMachineConfigRejectsLiteralSecret(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("credentials:\n  getvisa: ghp_thisIsALiteralToken\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadMachineConfig(path)
	if !errors.Is(err, ErrInvalidCredentialReference) {
		t.Fatalf("LoadMachineConfig error = %v, want ErrInvalidCredentialReference", err)
	}
}

func TestNotificationConfigRoundTripAndValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	want := MachineConfig{
		MirrorRoot: "/tmp/mirrors",
		Notifications: NotificationConfig{
			PromptPolicy: NotificationPromptPolicy{State: "ask"},
			Projects: []NotificationProject{{
				ID: "01JPROJECT", Root: filepath.Join(dir, "project"),
				Channels: []string{"macos", "vscode"},
				Prompt:   NotificationProjectPrompt{State: "snoozed", RemindAfter: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)},
			}},
			CoalesceWindow: "10s", PollInterval: "1m",
		},
	}
	if err := SaveMachineConfig(path, want); err != nil {
		t.Fatalf("SaveMachineConfig: %v", err)
	}
	got, err := LoadMachineConfig(path)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("mode = %o, want 600", gotMode)
	}
}

func TestNotificationConfigRefusesInvalidPersonalState(t *testing.T) {
	t.Parallel()

	cases := []MachineConfig{
		{Notifications: NotificationConfig{PromptPolicy: NotificationPromptPolicy{State: "sometimes"}}},
		{Notifications: NotificationConfig{Projects: []NotificationProject{{ID: "p", Root: "relative"}}}},
		{Notifications: NotificationConfig{Projects: []NotificationProject{{ID: "p", Root: "/one"}, {ID: "p", Root: "/two"}}}},
		{Notifications: NotificationConfig{Projects: []NotificationProject{{ID: "p", Root: "/one", Channels: []string{"pager"}}}}},
		{Notifications: NotificationConfig{PollInterval: "never"}},
	}
	for i, cfg := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			t.Parallel()
			if err := SaveMachineConfig(filepath.Join(t.TempDir(), "config.yaml"), cfg); !errors.Is(err, ErrManifestInvalid) {
				t.Fatalf("SaveMachineConfig error = %v, want ErrManifestInvalid", err)
			}
		})
	}
}

func TestSaveMachineConfigPreservesUnknownKeysAndComments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	existing := "# operator comment\nmirror_root: /tmp/mirrors\nfuture_key: keep-me # inline\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadMachineConfig(path)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	cfg.Notifications.PromptPolicy.State = "never"
	if err := SaveMachineConfig(path, cfg); err != nil {
		t.Fatalf("SaveMachineConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(raw)
	for _, want := range []string{"# operator comment", "future_key: keep-me", "# inline", "state: never"} {
		if !strings.Contains(body, want) {
			t.Fatalf("saved config lost %q:\n%s", want, body)
		}
	}
}

func writeYAML(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
