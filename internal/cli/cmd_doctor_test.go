package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/notification"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/skill"
	"github.com/ydnikolaev/a2ahub/testkit/fakegithub"
	"gopkg.in/yaml.v3"
)

// spaceTemplateRoot is the repo-relative path from this package's test
// working directory (go test's cwd is the package dir) to the space
// scaffold this phase ships (space-template/** is pure data — no imports —
// so these tests read it straight off disk rather than embedding it).
const spaceTemplateRoot = "../../space-template"

func newTestDoctorCommand() *DoctorCommand {
	cmd := NewDoctorCommand(host.NewFakeHost(), "0.1.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", "/unused/project")
	// Hermetic: NewDoctorCommand's real default (release.CachePath) points at
	// this machine's actual os.UserCacheDir() update-check.json — tests must
	// never read that real file. Point at a guaranteed-absent path (spec 19
	// T3: absent cache == "no notice", never an error) unless a test
	// overrides cmd.cachePath itself to exercise the advisory.
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	return cmd
}

func TestDoctorNameAndSynopsis(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	if got := cmd.Name(); got != "doctor" {
		t.Fatalf("Name() = %q, want \"doctor\"", got)
	}
	if cmd.Synopsis() == "" {
		t.Fatal("Synopsis() must not be empty")
	}
}

func TestDoctorNotificationComponentsUsesCanonicalStatusHealth(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.projectRoot = "/project"
	cmd.NotificationStatus = func(_ context.Context, root string) (notification.Status, error) {
		if root != "/project" {
			t.Fatalf("status root = %q", root)
		}
		return notification.Status{
			Project: &notification.Project{
				ID: "project", Root: root, Channels: []notification.Channel{notification.ChannelMacOS},
			},
			Components: []notification.ComponentHealth{{
				Channel: notification.ChannelMacOS, Installed: true,
				Permission: "authorized", LoginItem: "enabled", Handshake: "ok",
			}},
		}, nil
	}
	if ok, detail := cmd.doctorCheckNotificationComponents(context.Background()); !ok || detail != "" {
		t.Fatalf("healthy notifications = ok:%t detail:%q", ok, detail)
	}
	cmd.NotificationStatus = func(context.Context, string) (notification.Status, error) {
		return notification.Status{
			Project: &notification.Project{Channels: []notification.Channel{notification.ChannelVSCode}},
			Components: []notification.ComponentHealth{{
				Channel: notification.ChannelVSCode, Installed: true, Handshake: "mismatch", Profile: "Work",
			}},
		}, nil
	}
	if ok, detail := cmd.doctorCheckNotificationComponents(context.Background()); ok || !strings.Contains(detail, "mismatch") {
		t.Fatalf("mismatched notifications = ok:%t detail:%q", ok, detail)
	}
	cmd.NotificationStatus = func(context.Context, string) (notification.Status, error) {
		return notification.Status{
			Project: &notification.Project{Channels: []notification.Channel{notification.ChannelMacOS}},
			Components: []notification.ComponentHealth{{
				Channel: notification.ChannelMacOS, Installed: true,
				Handshake: "failed", Detail: "status probe timed out",
			}},
		}, nil
	}
	if ok, detail := cmd.doctorCheckNotificationComponents(context.Background()); ok || !strings.Contains(detail, "status probe timed out") {
		t.Fatalf("failed probe notifications = ok:%t detail:%q", ok, detail)
	}
}

func TestDoctorCheckStatuslineWiringRealLookup(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	// Exercises the constructor's real (non-overridden) lookupGit seam —
	// this dev/CI environment is expected to have git on PATH.
	if err := cmd.lookupGit(); err != nil {
		t.Skipf("git not on PATH in this environment: %v", err)
	}
	ok, detail := cmd.doctorCheckStatuslineWiring()
	if !ok {
		t.Fatalf("want pass with real git on PATH, got fail: %s", detail)
	}
}

// --- Run-level tests (flag handling, exit codes, aggregate report shape) ---

func TestDoctorRunRejectsSpaceFlagExplicitly(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), []string{"--space"}, IO{Stdout: &stdout, Stderr: &stderr})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(stderr.String(), "not available") {
		t.Fatalf("stderr = %q, want an explicit \"not available\" message", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no check output once --space is rejected", stdout.String())
	}
}

func TestDoctorRunAllPassOnZeroConnectedSpaces(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	for _, name := range []string{"credentials", "space access", "space identity", "participant avatars", "versions", "CI presence", "auto-merge enabled", "stuck green PRs", "statusline wiring"} {
		if !strings.Contains(stdout.String(), name+": PASS") {
			t.Errorf("stdout missing %q PASS line; got %q", name, stdout.String())
		}
	}
}

func TestDoctorParticipantAvatarsAdvisesOnlyForMissingActiveOwners(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mirror := filepath.Join(root, "mirror")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema: manifest/v1
space: getvisa
min_binary_version: 0.18.1
participants:
  - system: axon
    org: r22d222
    section: axon
    owners: [ydnikolaev]
    status: active
    joined: 2026-08-01
  - system: seomatrix
    org: r22d222
    section: seomatrix
    owners: [xpressmike, YDNIKOLAEV]
    status: active
    joined: 2026-08-01
  - system: legacy
    org: r22d222
    section: legacy
    owners: [left-owner]
    status: left
    joined: 2026-07-01
`
	if err := os.WriteFile(filepath.Join(mirror, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newTestDoctorCommand()
	cmd.projectRoot = root
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cmd.ParticipantAvatarStatus = func(login string) (bool, bool) {
		return strings.EqualFold(login, "ydnikolaev"), true
	}
	ok, detail := cmd.doctorCheckParticipantAvatars(space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}, space.MachineConfig{})
	if !ok {
		t.Fatalf("missing avatars are advisory, got FAIL: %s", detail)
	}
	for _, want := range []string{"getvisa: xpressmike", "a2a sync", "initials remain available"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail=%q, want %q", detail, want)
		}
	}
	for _, unwanted := range []string{"ydnikolaev", "left-owner"} {
		if strings.Contains(detail, unwanted) {
			t.Errorf("detail=%q unexpectedly contains %q", detail, unwanted)
		}
	}
}

func TestDoctorParticipantAvatarsDoesNotPrescribeSyncForUnsupportedOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := "schema: manifest/v1\nspace: demo\nparticipants:\n  - system: app\n    owners: [foo_bar]\n    status: active\n"
	if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return dir }
	cmd.ParticipantAvatarStatus = func(string) (bool, bool) { return false, false }
	ok, detail := cmd.doctorCheckParticipantAvatars(space.ProjectConfig{Spaces: []space.Ref{{ID: "demo"}}}, space.MachineConfig{})
	if !ok || !strings.Contains(detail, "correct them in space.yaml") || strings.Contains(detail, "a2a sync") {
		t.Fatalf("unsupported owner advisory = ok:%t detail:%q", ok, detail)
	}
}

func TestDoctorRunNonZeroExitAndActionableMessageOnFailure(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/getvisa.git"}}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return errors.New("boom") }
	cmd.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "space access: FAIL: getvisa: boom") {
		t.Fatalf("stdout missing actionable per-check message; got %q", stdout.String())
	}
}

// TestDoctorRunSurfacesUpdateAdvisoryOnPass is spec 19 AC #7: `a2a doctor`
// must actually REPORT "update available" as advisory prose — not merely
// compute it internally — while the versions check still PASSES (exit 0).
func TestDoctorRunSurfacesUpdateAdvisoryOnPass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "update-check.json")
	if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: time.Now(), Latest: "0.3.0", Source: "github"}); err != nil {
		t.Fatalf("release.WriteCheck: %v", err)
	}

	cmd := newTestDoctorCommand()
	cmd.binaryVersion = "0.1.2"
	cmd.cachePath = func() (string, error) { return cachePath, nil }
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (advisory alone must not fail the check); stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "versions: PASS") {
		t.Fatalf("stdout = %q, want the versions check to still report PASS", stdout.String())
	}
	if !strings.Contains(stdout.String(), "update available: v0.1.2 -> v0.3.0 — run a2a update") {
		t.Fatalf("stdout = %q, want the update-available advisory actually reported, not just computed", stdout.String())
	}
}

func TestDoctorRunCannotLoadProjectConfigIsRuntimeFailure(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, errors.New("no such file") }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (runtime failure, distinct from usage's 2)", code)
	}
	if !strings.Contains(stderr.String(), "no such file") {
		t.Fatalf("stderr = %q, want the underlying error surfaced", stderr.String())
	}
}

func TestDoctorVersionsTreatsLiteralDevAsUnreleasedBuild(t *testing.T) {
	t.Parallel()
	cmd := NewDoctorCommand(nil, "dev", "", "", t.TempDir())
	ok, detail := cmd.doctorCheckVersions(space.ProjectConfig{
		Spaces: []space.Ref{{ID: "space"}},
	}, space.MachineConfig{})
	if !ok {
		t.Fatal("literal dev must be an advisory/skip, not a broken released version")
	}
	for _, want := range []string{"unreleased local build", "skipped"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail=%q, want %q", detail, want)
		}
	}
}

// --- Per-check unit tests: each of OP-218's five basic checks independently
// drivable to both PASS and FAIL (spec 09 §6 "Basic doctor" testing row). ---

func TestDoctorCheckCredentials(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}
	machine := space.MachineConfig{}

	t.Run("pass", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{Token: "tok"}, nil
		}
		ok, detail := cmd.doctorCheckCredentials(context.Background(), cfg, machine)
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail unresolved", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{}, errors.New("credential unresolved")
		}
		ok, detail := cmd.doctorCheckCredentials(context.Background(), cfg, machine)
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "getvisa") || !strings.Contains(detail, "credential unresolved") {
			t.Fatalf("detail = %q, want it to name the space and the error", detail)
		}
	})

	// The write path resolves A2A_TOKEN_<SPACE_ID> FIRST and the machine-
	// config reference second; doctor must ask the same question, or it
	// reds a token that `a2a submit` would happily use (and greens a
	// reference submit would reject).
	t.Run("passes the same explicit override env var a write does", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		var sawEnvVar string
		cmd.resolveCredential = func(_ context.Context, envVar string, _ space.CredentialReference) (host.Credential, error) {
			sawEnvVar = envVar
			return host.Credential{Token: "tok"}, nil
		}
		if ok, detail := cmd.doctorCheckCredentials(context.Background(), cfg, machine); !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
		if sawEnvVar != "A2A_TOKEN_GETVISA" {
			t.Fatalf("explicit env var = %q, want A2A_TOKEN_GETVISA (the same one submit reads)", sawEnvVar)
		}
	})

	t.Run("no connected spaces vacuously passes", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		ok, _ := cmd.doctorCheckCredentials(context.Background(), space.ProjectConfig{}, machine)
		if !ok {
			t.Fatal("want pass with zero connected spaces")
		}
	})
}

// TestDoctorCheckSpaceIdentity: `a2a init -space <url>` guesses the space
// id from the repo URL, so a repo whose basename is not its space id leaves
// a config naming a space that does not exist. Doctor used to report a
// healthy setup while every write failed — this check is the guard.
func TestDoctorCheckSpaceIdentity(t *testing.T) {
	t.Parallel()
	manifest := "schema: space/v1\nspace: getvisa\nmin_binary_version: 0.0.0\nparticipants: []\n"

	t.Run("matching id passes", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(string) ([]byte, error) { return []byte(manifest), nil }
		ok, detail := cmd.doctorCheckSpaceIdentity(space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/o/a2a.git"}}}, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("url-derived id that the manifest disagrees with fails, naming the fix", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(string) ([]byte, error) { return []byte(manifest), nil }
		ok, detail := cmd.doctorCheckSpaceIdentity(space.ProjectConfig{Spaces: []space.Ref{{ID: "a2a", RepoURL: "https://example.invalid/o/a2a.git"}}}, space.MachineConfig{})
		if ok {
			t.Fatal("want fail: the configured id is not the id the space declares")
		}
		for _, want := range []string{"a2a", "getvisa", "a2a connect"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("detail = %q, want it to name %q", detail, want)
			}
		}
	})

	t.Run("an unreachable mirror is left to the space-access check", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(string) ([]byte, error) { return nil, errors.New("no such file") }
		ok, _ := cmd.doctorCheckSpaceIdentity(space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}, space.MachineConfig{})
		if !ok {
			t.Fatal("want pass: a missing mirror is another check's failure, not a double-report")
		}
	})
}

func TestDoctorCheckSpaceAccess(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/getvisa.git"}}}

	t.Run("pass", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }
		ok, detail := cmd.doctorCheckSpaceAccess(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail unreachable mirror", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return errors.New("connection refused") }
		ok, detail := cmd.doctorCheckSpaceAccess(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "connection refused") {
			t.Fatalf("detail = %q, want the underlying fetch error", detail)
		}
	})
}

func TestDoctorCheckVersions(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}

	t.Run("pass when binary meets the pin", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.binaryVersion = "1.0.0"
		cmd.readFile = func(string) ([]byte, error) {
			return []byte("schema: space/v1\nspace: getvisa\nmin_binary_version: 0.5.0\nparticipants: []\n"), nil
		}
		ok, detail := cmd.doctorCheckVersions(cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail when binary is stale", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.binaryVersion = "0.1.0"
		cmd.readFile = func(string) ([]byte, error) {
			return []byte("schema: space/v1\nspace: getvisa\nmin_binary_version: 9.9.9\nparticipants: []\n"), nil
		}
		ok, detail := cmd.doctorCheckVersions(cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "9.9.9") {
			t.Fatalf("detail = %q, want the min_binary_version pin named", detail)
		}
	})

	t.Run("fail when space.yaml is unreadable (mirror missing)", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
		ok, detail := cmd.doctorCheckVersions(cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "cannot read space.yaml") {
			t.Fatalf("detail = %q, want an actionable read-failure message", detail)
		}
	})
}

// --- spec 19 T4 doctor row: the "versions" check's update-available
// advisory (cache-read only) — separate from the min_binary_version floor
// comparison above, which keeps its own FAIL semantics unchanged. ---

func TestDoctorCheckVersions_UpdateAdvisory(t *testing.T) {
	t.Parallel()

	t.Run("newer cached release appends the advisory, check still passes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cachePath := filepath.Join(dir, "update-check.json")
		if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: time.Now(), Latest: "0.3.0", Source: "github"}); err != nil {
			t.Fatalf("release.WriteCheck: %v", err)
		}

		cmd := newTestDoctorCommand()
		cmd.binaryVersion = "0.1.2"
		cmd.cachePath = func() (string, error) { return cachePath, nil }
		// No connected spaces: the floor comparison is vacuous (ok=true),
		// isolating the advisory half.
		ok, detail := cmd.doctorCheckVersions(space.ProjectConfig{}, space.MachineConfig{})
		if !ok {
			t.Fatalf("want the check to still PASS on an advisory alone, got fail: %s", detail)
		}
		if !strings.Contains(detail, "update available") {
			t.Fatalf("detail = %q, want it to contain \"update available\"", detail)
		}
		if !strings.Contains(detail, "v0.1.2") || !strings.Contains(detail, "v0.3.0") {
			t.Fatalf("detail = %q, want the current/latest versions named", detail)
		}
	})

	t.Run("a floor violation still FAILs even with a newer cached release", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cachePath := filepath.Join(dir, "update-check.json")
		if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: time.Now(), Latest: "0.3.0", Source: "github"}); err != nil {
			t.Fatalf("release.WriteCheck: %v", err)
		}

		cmd := newTestDoctorCommand()
		cmd.binaryVersion = "0.1.0"
		cmd.cachePath = func() (string, error) { return cachePath, nil }
		cmd.readFile = func(string) ([]byte, error) {
			return []byte("schema: space/v1\nspace: getvisa\nmin_binary_version: 9.9.9\nparticipants: []\n"), nil
		}
		cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}
		ok, detail := cmd.doctorCheckVersions(cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail (floor violation), got pass")
		}
		if !strings.Contains(detail, "9.9.9") {
			t.Fatalf("detail = %q, want the min_binary_version pin still named", detail)
		}
	})

	t.Run("empty cache emits no advisory", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.binaryVersion = "0.1.2"
		cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "absent.json"), nil }
		ok, detail := cmd.doctorCheckVersions(space.ProjectConfig{}, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
		if strings.Contains(detail, "update available") {
			t.Fatalf("detail = %q, want no advisory on an absent cache", detail)
		}
	})

	t.Run("up to date emits no advisory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cachePath := filepath.Join(dir, "update-check.json")
		if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: time.Now(), Latest: "0.1.2", Source: "github"}); err != nil {
			t.Fatalf("release.WriteCheck: %v", err)
		}
		cmd := newTestDoctorCommand()
		cmd.binaryVersion = "0.1.2"
		cmd.cachePath = func() (string, error) { return cachePath, nil }
		ok, detail := cmd.doctorCheckVersions(space.ProjectConfig{}, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
		if strings.Contains(detail, "update available") {
			t.Fatalf("detail = %q, want no advisory when already up to date", detail)
		}
	})
}

func TestDoctorCheckCIPresence(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}

	t.Run("pass when the workflow file exists", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(path string) ([]byte, error) {
			if strings.HasSuffix(path, ".github/workflows/a2a-validate.yml") {
				return []byte("name: a2a-validate\n"), nil
			}
			return nil, os.ErrNotExist
		}
		ok, detail := cmd.doctorCheckCIPresence(cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail when the workflow file is missing", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
		ok, detail := cmd.doctorCheckCIPresence(cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "a2a-validate.yml") {
			t.Fatalf("detail = %q, want the missing path named", detail)
		}
	})
}

// TestDoctorCheckAutoMerge is WAVE M2 / spec 45 AC-1050.5: three outcomes,
// driven against fakegithub over real HTTP (the on/off cases) and an inline
// httptest handler for the read-failure case — fakegithub's GET
// /repos/{owner}/{name} always answers 200, so a 403 needs a handler of its
// own (see this phase's reported deviation).
func TestDoctorCheckAutoMerge(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	machine := space.MachineConfig{}
	withToken := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	t.Run("pass when auto-merge is on", func(t *testing.T) {
		t.Parallel()
		gh := fakegithub.New(t, t.TempDir())
		gh.AllowAutoMerge = true

		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(nil, gh.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckAutoMerge(context.Background(), cfg, machine)
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail naming the setting and the fix when auto-merge is off", func(t *testing.T) {
		t.Parallel()
		gh := fakegithub.New(t, t.TempDir())
		gh.AllowAutoMerge = false

		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(nil, gh.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckAutoMerge(context.Background(), cfg, machine)
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "getvisa") {
			t.Fatalf("detail = %q, want the space named", detail)
		}
		if !strings.Contains(detail, "Allow auto-merge") {
			t.Fatalf("detail = %q, want the setting and how to turn it on named", detail)
		}
		if !strings.Contains(detail, "stalls behind a PR nothing will merge") {
			t.Fatalf("detail = %q, want the why-it-matters explanation", detail)
		}
	})

	// AC-1050.5's third outcome, as CORRECTED lead-side 2026-07-25: an
	// unanswerable read is an ADVISORY, not a failure. It must never be a
	// silent PASS (that reproduces the original defect with a green check next
	// to it) and must never be a FAIL either — a fine-grained token without
	// "Repository metadata: read" is a legitimate, common, working setup, and a
	// gate that reds on a working setup is a gate people stop reading. Same
	// resolution this repo already reached for doctorWorkflowScopeNote.
	t.Run("an unanswerable read is an advisory PASS, never silent, never a FAIL", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
		}))
		defer srv.Close()

		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(nil, srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckAutoMerge(context.Background(), cfg, machine)
		if !ok {
			t.Fatalf("want an advisory PASS, got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "unverified") {
			t.Fatalf("detail = %q, want the note to say the setting is UNVERIFIED — a silent PASS here is the false-green this row exists to prevent", detail)
		}
		if !strings.Contains(detail, "getvisa") {
			t.Fatalf("detail = %q, want the space named so the reader knows WHICH space is unverified", detail)
		}
		// The note must be stable output: doctor's line is asserted verbatim by
		// the e2e doctor script, and an interpolated multi-line API body both
		// destabilises it and buries the actionable sentence.
		if strings.Contains(detail, "\n") || strings.Contains(detail, "Forbidden") {
			t.Fatalf("detail = %q must not interpolate the raw API error", detail)
		}
	})

	t.Run("an unwired repo-settings reader is the same advisory, not a failure", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand() // host.NewFakeHost() implements no AutoMergeAllowed
		ok, detail := cmd.doctorCheckAutoMerge(context.Background(), cfg, machine)
		if !ok {
			t.Fatalf("want an advisory PASS, got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "unverified") {
			t.Fatalf("detail = %q, want the unverified note", detail)
		}
	})

	// The half that MUST stay a failure even when a sibling space is
	// unreadable: a genuinely-off setting is actionable, so it wins over the
	// advisory rather than being softened into it.
	t.Run("a genuinely-off setting still FAILs even alongside an unverifiable space", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/repos/acme/getvisa") {
				_, _ = w.Write([]byte(`{"allow_auto_merge": false}`))
				return
			}
			http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
		}))
		defer srv.Close()

		twoSpaces := space.ProjectConfig{System: cfg.System, Spaces: append(
			append([]space.Ref{}, cfg.Spaces...),
			space.Ref{ID: "other", RepoURL: "https://github.com/acme/other"},
		)}

		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(nil, srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckAutoMerge(context.Background(), twoSpaces, machine)
		if ok {
			t.Fatalf("want FAIL — one space really has auto-merge off; got pass with %q", detail)
		}
		if !strings.Contains(detail, "Allow auto-merge") {
			t.Fatalf("detail = %q, want the actionable half, not the advisory", detail)
		}
	})

	t.Run("no connected spaces vacuously passes", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		ok, _ := cmd.doctorCheckAutoMerge(context.Background(), space.ProjectConfig{}, machine)
		if !ok {
			t.Fatal("want pass with zero connected spaces")
		}
	})
}

func TestDoctorCheckStuckGreenPRs(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git",
	}}}
	resolve := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	t.Run("fails with exact remedy for green unarmed PR", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.ListOpenPRsFunc = func(context.Context, host.ListOpenPRsRequest) ([]host.OpenPRSummary, error) {
			return []host.OpenPRSummary{{Number: 42, URL: "https://github.com/acme/getvisa/pull/42"}}, nil
		}
		fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "completed", Conclusion: "success"}, nil
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve

		ok, detail := cmd.doctorCheckStuckGreenPRs(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatal("green unarmed PR passed")
		}
		if !strings.Contains(detail, "pull/42") ||
			!strings.Contains(detail, "`gh pr merge 42 --auto --repo acme/getvisa`") ||
			!strings.Contains(detail, "doctor will never merge") {
			t.Fatalf("detail is not actionable/read-only: %q", detail)
		}
	})

	t.Run("armed or red PRs do not fail", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.ListOpenPRsFunc = func(context.Context, host.ListOpenPRsRequest) ([]host.OpenPRSummary, error) {
			return []host.OpenPRSummary{
				{Number: 1, AutoMergeArmed: true},
				{Number: 2, AutoMergeArmed: false},
			}, nil
		}
		fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "completed", Conclusion: "failure"}, nil
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		if ok, detail := cmd.doctorCheckStuckGreenPRs(context.Background(), cfg, space.MachineConfig{}); !ok {
			t.Fatalf("non-stuck PRs failed: %s", detail)
		}
	})

	t.Run("unreadable state is an advisory", func(t *testing.T) {
		t.Parallel()
		fake := host.NewFakeHost()
		fake.ListOpenPRsFunc = func(context.Context, host.ListOpenPRsRequest) ([]host.OpenPRSummary, error) {
			return nil, errors.New("forbidden")
		}
		cmd := newTestDoctorCommand()
		cmd.h = fake
		cmd.resolveCredential = resolve
		ok, detail := cmd.doctorCheckStuckGreenPRs(context.Background(), cfg, space.MachineConfig{})
		if !ok || !strings.Contains(detail, "unverified") || strings.Contains(detail, "forbidden") {
			t.Fatalf("unreadable state = (%v, %q), want stable advisory", ok, detail)
		}
	})
}

// --- doctorCheckScaffoldingCurrent ("space scaffolding current" row) -----

// doctorScaffoldingTemplateFS is a small stand-in embedded template — the
// same six-path shape spaceUpdateTemplateFS (cmd_space_test.go) uses for
// `space update`'s own tests, kept separate because that helper lives in
// the external cli_test package and this file needs its own.
func doctorScaffoldingTemplateFS() fstest.MapFS {
	return fstest.MapFS{
		"space.yaml": &fstest.MapFile{Data: []byte(
			"schema: manifest/v1\n" +
				"space: replace-with-space-id\n" +
				"min_binary_version: 0.1.0\n" +
				"participants: []\n",
		)},
		"CODEOWNERS": &fstest.MapFile{Data: []byte("/space.yaml @REPLACE_WITH_ORG/space-owners\n")},
		"README.md":  &fstest.MapFile{Data: []byte("# space template\n")},
		".github/workflows/a2a-validate.yml": &fstest.MapFile{Data: []byte(
			"jobs:\n  a2a-validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.1.0\n",
		)},
		".github/dependabot.yml": &fstest.MapFile{Data: []byte("version: 2\n")},
		"BRANCH-PROTECTION.md":   &fstest.MapFile{Data: []byte("# branch protection checklist\n")},
	}
}

// doctorScaffoldingMirrorReadFile matches by path SUFFIX (the mirror
// directory prefix doctor's real resolveMirror computes is irrelevant to
// these tests) — a path with no matching suffix reports os.ErrNotExist,
// exactly what a file absent from the mirror looks like to spaceComputeUpdatePlanFor.
func doctorScaffoldingMirrorReadFile(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		for rel, content := range files {
			if strings.HasSuffix(filepath.ToSlash(path), rel) {
				return []byte(content), nil
			}
		}
		return nil, os.ErrNotExist
	}
}

func TestDoctorCheckScaffoldingCurrent_NoConnectedSpacesVacuouslyPasses(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.TemplateFiles = doctorScaffoldingTemplateFS()
	ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), space.ProjectConfig{}, space.MachineConfig{})
	if !ok || detail != "" {
		t.Fatalf("want pass with no note, got ok=%v detail=%q", ok, detail)
	}
}

func TestDoctorCheckScaffoldingCurrent_TemplateNotWiredIsAdvisoryNotFailure(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	cmd := newTestDoctorCommand() // cmd.TemplateFiles left nil, "not wired"
	ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
	if !ok {
		t.Fatalf("want an advisory PASS, got FAIL: %s", detail)
	}
	if !strings.Contains(detail, "unverified") {
		t.Fatalf("detail = %q, want an explicit unverified note", detail)
	}
}

func TestDoctorCheckScaffoldingCurrent_DevBinaryIsUndecidableNotFailure(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	cmd := newTestDoctorCommand()
	cmd.TemplateFiles = doctorScaffoldingTemplateFS()
	cmd.binaryVersion = "dev"
	ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
	if !ok {
		t.Fatalf("want an advisory PASS, got FAIL: %s", detail)
	}
	if !strings.Contains(detail, "could not be checked") {
		t.Fatalf("detail = %q, want a could-not-check note (a dev build cannot be compared as a release version)", detail)
	}
}

func TestDoctorCheckScaffoldingCurrent_UnreadableMirrorIsUndecidableNotFailure(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	cmd := newTestDoctorCommand()
	cmd.TemplateFiles = doctorScaffoldingTemplateFS()
	cmd.binaryVersion = "0.5.0"
	// A read failure OTHER than "not exist" (e.g. a permission error) makes
	// spaceComputeUpdatePlanFor itself return an error — this must render as
	// "could not be checked", never a FAIL.
	cmd.readFile = func(string) ([]byte, error) { return nil, errors.New("permission denied") }
	ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
	if !ok {
		t.Fatalf("want an advisory PASS, got FAIL: %s", detail)
	}
	if !strings.Contains(detail, "could not be checked") {
		t.Fatalf("detail = %q, want a could-not-check note", detail)
	}
}

func TestDoctorCheckScaffoldingCurrent_InSyncPassesWithNoNote(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	cmd := newTestDoctorCommand()
	cmd.TemplateFiles = doctorScaffoldingTemplateFS()
	cmd.binaryVersion = "0.5.0"
	cmd.readFile = doctorScaffoldingMirrorReadFile(map[string]string{
		"space.yaml":                         "schema: manifest/v1\nspace: getvisa\nmin_binary_version: 0.1.0\nparticipants: []\n",
		"CODEOWNERS":                         "/space.yaml @REPLACE_WITH_ORG/space-owners\n",
		".github/workflows/a2a-validate.yml": "jobs:\n  a2a-validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.5.0\n",
		".github/dependabot.yml":             "version: 2\n",
		"BRANCH-PROTECTION.md":               "# branch protection checklist\n",
	})
	ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
	if !ok || detail != "" {
		t.Fatalf("want pass with no note for an already-current space, got ok=%v detail=%q", ok, detail)
	}
}

// TestDoctorCheckScaffoldingCurrent_BehindNamesWhoCanFixByPermission is this
// phase's core acceptance: a behind space is NEVER a failure, and the note's
// wording is resolved from what the credential can actually DO (push/admin
// on GET /repos/{owner}/{repo}'s `permissions` object), never a config
// field — the three outcomes spec'd in the brief.
func TestDoctorCheckScaffoldingCurrent_BehindNamesWhoCanFixByPermission(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	withToken := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}
	// A mirror with only space.yaml present (pinned BELOW the template's
	// floor) and every other managed file missing — guaranteed BEHIND.
	behindReadFile := doctorScaffoldingMirrorReadFile(map[string]string{
		"space.yaml": "schema: manifest/v1\nspace: getvisa\nmin_binary_version: 0.0.1\nparticipants: []\n",
	})

	t.Run("push access -> names the drift and says run the command", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"permissions":{"admin":false,"push":true,"pull":true}}`))
		}))
		defer srv.Close()

		cmd := newTestDoctorCommand()
		cmd.TemplateFiles = doctorScaffoldingTemplateFS()
		cmd.binaryVersion = "0.5.0"
		cmd.readFile = behindReadFile
		cmd.h = host.NewGitHubHost(nil, srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want an advisory PASS (never FAIL), got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "getvisa") {
			t.Fatalf("detail = %q, want the space named", detail)
		}
		if !strings.Contains(detail, "behind the current template") {
			t.Fatalf("detail = %q, want the drift named", detail)
		}
		if !strings.Contains(detail, "run `a2a space update`") {
			t.Fatalf("detail = %q, want it to say to run the command (push access)", detail)
		}
		if strings.Contains(detail, "ask the space admin") {
			t.Fatalf("detail = %q, must not tell a pushable credential to ask someone else", detail)
		}
	})

	t.Run("read-only access -> names the drift and says ask the space admin", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"permissions":{"admin":false,"push":false,"pull":true}}`))
		}))
		defer srv.Close()

		cmd := newTestDoctorCommand()
		cmd.TemplateFiles = doctorScaffoldingTemplateFS()
		cmd.binaryVersion = "0.5.0"
		cmd.readFile = behindReadFile
		cmd.h = host.NewGitHubHost(nil, srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want an advisory PASS (never FAIL), got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "ask the space admin to run `a2a space update`") {
			t.Fatalf("detail = %q, want a ONE-SENTENCE ask-the-admin note a participant can relay verbatim", detail)
		}
		if strings.Contains(detail, "who can fix it could not be determined") {
			t.Fatalf("detail = %q, a KNOWN read-only permission must not render as unknown", detail)
		}
	})

	t.Run("permission unknown (read fails) -> neutral note, prescribes nothing", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
		}))
		defer srv.Close()

		cmd := newTestDoctorCommand()
		cmd.TemplateFiles = doctorScaffoldingTemplateFS()
		cmd.binaryVersion = "0.5.0"
		cmd.readFile = behindReadFile
		cmd.h = host.NewGitHubHost(nil, srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want an advisory PASS (never FAIL), got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "could not be determined") {
			t.Fatalf("detail = %q, want the neutral could-not-determine note", detail)
		}
		if strings.Contains(detail, "run `a2a space update`") || strings.Contains(detail, "ask the space admin") {
			t.Fatalf("detail = %q, an UNKNOWN permission must not prescribe who acts", detail)
		}
		// Stable output: no multi-line API body interpolated (the same
		// discipline doctorCheckAutoMerge's note already follows).
		if strings.Contains(detail, "\n") || strings.Contains(detail, "Forbidden") {
			t.Fatalf("detail = %q must not interpolate the raw API error", detail)
		}
	})

	// FakeHost implements no RepoPermissions — the same "unwired reader"
	// shape doctorCheckAutoMerge's own test asserts for AutoMergeAllowed —
	// so this is doctor's DEFAULT behavior against a behind space with no
	// GitHub host wired at all: neutral, never a guess.
	t.Run("unwired repo-permissions reader is the same neutral note", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand() // host.NewFakeHost() implements no RepoPermissions
		cmd.TemplateFiles = doctorScaffoldingTemplateFS()
		cmd.binaryVersion = "0.5.0"
		cmd.readFile = behindReadFile
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckScaffoldingCurrent(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want an advisory PASS (never FAIL), got FAIL: %s", detail)
		}
		if !strings.Contains(detail, "could not be determined") {
			t.Fatalf("detail = %q, want the neutral could-not-determine note", detail)
		}
	})
}

func TestDoctorCheckStatuslineWiring(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.lookupGit = func() error { return nil }
	if ok, detail := cmd.doctorCheckStatuslineWiring(); !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}

	cmd2 := newTestDoctorCommand()
	cmd2.lookupGit = func() error { return errors.New("not found") }
	ok, detail := cmd2.doctorCheckStatuslineWiring()
	if ok {
		t.Fatal("want fail, got pass")
	}
	if !strings.Contains(detail, "not found") {
		t.Fatalf("detail = %q, want the underlying lookup error", detail)
	}
}

// --- doctorCheckSkillDiscoverable (P32, AC-918.2) --------------------------

func TestDoctorCheckSkillDiscoverable_NotInstalled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cmd := NewDoctorCommand(host.NewFakeHost(), "0.1.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillDiscoverable()
	if !ok {
		t.Fatalf("want pass (not installed is not this check's concern), got fail: %s", detail)
	}
	if !strings.Contains(detail, "no a2ahub skill installed") {
		t.Fatalf("detail = %q, want a not-installed note", detail)
	}
}

func TestDoctorCheckSkillDiscoverable_InstalledButUnlinked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".a2ahub", "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".a2ahub", "skill", "SKILL.md"), []byte("---\nname: a2ahub\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A detected surface (.claude/ present) with NO a2ahub link under it.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.1.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillDiscoverable()
	if !ok {
		t.Fatalf("want pass (advisory-on-PASS, matching doctorCheckVersions), got fail: %s", detail)
	}
	if !strings.Contains(detail, "ADVISORY") || !strings.Contains(detail, "a2a skill link") {
		t.Fatalf("detail = %q, want the installed-but-unlinked advisory naming the fix", detail)
	}
}

func TestDoctorCheckSkillDiscoverable_InstalledAndLinked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".a2ahub", "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".a2ahub", "skill", "SKILL.md"), []byte("---\nname: a2ahub\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, ".claude", "skills", "a2ahub")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.1.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillDiscoverable()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	if !strings.Contains(detail, "linked (1 surface") {
		t.Fatalf("detail = %q, want the linked-surface count", detail)
	}
}

// TestDoctorRunRendersSkillDiscoverableWithSeparator guards the PASS-line
// rendering convention (Run's "%s: PASS%s\n" has no space before detail):
// every returned detail must lead with " · " itself, or the line mashes
// together like doctorCheckVersions's own advisory does when this is
// missed.
func TestDoctorRunRendersSkillDiscoverableWithSeparator(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "skill discoverable: PASS · ") {
		t.Fatalf("stdout = %q, want a properly separated PASS line", stdout.String())
	}
}

func TestDoctorRunUsesCustomSkillDirectoryForBothChecks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "agent", "manual")
	skillMD := []byte("---\nname: a2ahub\n---\n")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), skillMD, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.3.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/project.yaml", "/unused/machine.yaml", root)
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{SkillDir: "agent/manual"}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	cmd.lookupGit = func() error { return nil }
	// A minimal embed matching the disk fixture byte-for-byte, so the walk
	// this phase adds is clean rather than degrading the row to the
	// "wires no embedded skill tree" advisory this test does not exercise.
	cmd.SkillFiles = fstest.MapFS{"a2ahub/SKILL.md": {Data: skillMD}}

	var stdout, stderr bytes.Buffer
	if code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("Run code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"skill discoverable: PASS · skill installed",
		"skill manual current: PASS · skill manual current (v0.3.0, 1 files verified against this binary's own tree)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

// --- doctorCheckSkillManualCurrent (P31 wave 5) -----------------------------

func TestDoctorCheckSkillManualCurrent_NoInstall(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass (no install is not this check's concern), got fail: %s", detail)
	}
	if !strings.Contains(detail, "no skill installed") {
		t.Fatalf("detail = %q, want a no-install note", detail)
	}
}

func TestDoctorCheckSkillManualCurrent_OlderManual_Advisory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass (advisory-on-PASS, never a hard FAIL), got fail: %s", detail)
	}
	if !strings.Contains(detail, "v0.1.0") || !strings.Contains(detail, "v0.3.0") || !strings.Contains(detail, "a2a skill install") {
		t.Fatalf("detail = %q, want the stale-manual advisory naming both versions and the fix", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_UpToDate_Clean is judge-the-thing-2026-08
// P5 §8 #1's round-trip: a tree written by installSkillTree from THIS
// binary's own embed, stamped with THIS binary's version, must be a clean
// PASS carrying the tree-verified note — never a bare stamp-only PASS, which
// is exactly the wording a forgotten cmd/a2a wiring line degrades to (§8
// #10).
func TestDoctorCheckSkillManualCurrent_UpToDate_Clean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if _, err := installSkillTree(skill.Files, target, "0.3.0", false); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	cmd.SkillFiles = skill.Files

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	if !strings.Contains(detail, "skill manual current (v0.3.0, ") ||
		!strings.Contains(detail, "files verified against this binary's own tree") {
		t.Fatalf("detail = %q, want the tree-verified current-manual note", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_UnparseableProvenance_VersionUnknown
// covers the OWNED case: the a2ahub provenance marker is present (so
// skillTargetState reports owned=true and this check has standing to judge
// the tree), but the version stamp itself is garbled. Before P5 the fixture
// here carried no marker at all, which — now that ownership gates the whole
// check (spec 05 "The other side of owned is row 2") — would hit the FOREIGN
// branch (TestDoctorCheckSkillManualCurrent_ForeignInstall_NotJudged) instead
// of this one. The marker is added so this test still exercises what its
// name promises.
func TestDoctorCheckSkillManualCurrent_UnparseableProvenance_VersionUnknown(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	garbled := skillProvenanceTag + "\nnot a real provenance file\n"
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(garbled), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	// This used to assert the literal "version unknown", which is the phrase the
	// row was changed to stop saying: it answered three different situations
	// identically and named neither a cause nor an action. A test that pins an
	// uninformative message keeps it, so the assertion is now about what the note
	// must CONVEY.
	for _, want := range []struct{ substr, why string }{
		{"no version stamp", "name the CAUSE — the file carries nothing to read"},
		{"a2a skill install", "name the action, which here genuinely does rewrite the file"},
	} {
		if !strings.Contains(detail, want.substr) {
			t.Errorf("detail = %q, missing %q — %s", detail, want.substr, want.why)
		}
	}
}

// doctorSentinelFS fails the test the moment ANYTHING is read from it — the
// tooth for judge-the-thing-2026-08 P5 §8 #8's second half: a NON-owned
// directory must never be walked at all, not merely "walked and ignored".
type doctorSentinelFS struct {
	t *testing.T
}

func (s doctorSentinelFS) Open(name string) (fs.File, error) {
	s.t.Helper()
	s.t.Fatalf("doctorSentinelFS.Open(%q): a non-owned skill directory must never be read from the embedded tree", name)
	return nil, fs.ErrNotExist
}

// TestDoctorCheckSkillManualCurrent_ForeignInstall_NotJudged is spec 05 §0 row
// 2: a non-empty directory WITHOUT the a2ahub provenance marker is someone
// else's, and doctor has no standing to judge it — not "unparseable", not
// "version unknown", simply not our tree to read. Before P5, this exact
// fixture (a PROVENANCE.md with no marker) hit the "no version stamp" branch
// and reported a verdict about a tree this tool never installed.
func TestDoctorCheckSkillManualCurrent_ForeignInstall_NotJudged(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte("not a real provenance file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	cmd.SkillFiles = doctorSentinelFS{t: t}

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass (not our tree to judge), got fail: %s", detail)
	}
	if !strings.Contains(detail, "not judged") {
		t.Fatalf("detail = %q, want it to say the directory is not judged", detail)
	}
}

// TestDoctorSkillManualNamesADevBuildRatherThanSayingVersionUnknown is the case
// that actually fires in normal development, every single time: a dev build
// stamps `(a2a dev)`, the version pattern requires a leading digit, and the row
// reported "version unknown" with nothing to say why.
//
// It must NOT name a remedy here. `a2a skill install` from a dev build re-stamps
// `dev` and changes nothing — which is the louder form of the defect this whole
// line of fixes is about: a message that sends the reader somewhere that cannot
// help.
func TestDoctorSkillManualNamesADevBuildRatherThanSayingVersionUnknown(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// skillProvenance("dev") itself, not a hand-typed excerpt — the marker
	// tag it carries is what makes this an OWNED install now that ownership
	// gates the whole check (spec 05 "The other side of owned is row 2").
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("dev")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "dev", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("this row never FAILs; got fail: %s", detail)
	}
	if !strings.Contains(detail, "development build") {
		t.Errorf("detail = %q, want it to name the cause — a dev build stamps `a2a dev`, which is "+
			"expected when running from source and is not a problem to fix", detail)
	}
	if strings.Contains(detail, "a2a skill install") {
		t.Errorf("detail = %q, must NOT name a remedy: re-installing from a dev build re-stamps `dev` "+
			"and changes nothing, which is exactly the dead-remedy defect this row was fixed for", detail)
	}
}

// TestDoctorRunRendersSkillManualCurrentWithSeparator guards the same
// PASS-line separator convention TestDoctorRunRendersSkillDiscoverableWithSeparator
// pins for its sibling check.
func TestDoctorRunRendersSkillManualCurrentWithSeparator(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "skill manual current: PASS · ") {
		t.Fatalf("stdout = %q, want a properly separated PASS line", stdout.String())
	}
}

// --- doctorCheckAutomationCoverage --------------------------------------
//
// onboarding.md §8.6 recommends two automations doctor structurally cannot
// observe (a harness session-start hook, and a2a-poll.yml in the
// PARTICIPANT's own repo — D-021, "advisory, never invasive"). This row
// must say so plainly, must never claim a verdict it did not earn (no PASS
// wording implying a check happened, no FAIL at all), and must always
// appear.

func TestDoctorCheckAutomationCoverage_AlwaysPassWithAdvisory(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	ok, detail := cmd.doctorCheckAutomationCoverage()
	if !ok {
		t.Fatalf("automation coverage must never FAIL (doctor cannot observe either automation, so it has no basis to fail); got fail: %s", detail)
	}
	for _, want := range []string{
		"session-start hook",
		"a2a-poll.yml",
		"PARTICIPANT",
		"cannot see",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %q, want it to mention %q", detail, want)
		}
	}
}

// TestDoctorRunRendersAutomationCoverageWithSeparator guards the same
// PASS-line separator convention TestDoctorRunRendersSkillDiscoverableWithSeparator
// pins for its siblings, and asserts the row is present and never a FAIL at
// the Run level (zero connected spaces, so nothing else could make Run
// non-zero either).
func TestDoctorRunRendersAutomationCoverageWithSeparator(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "automation coverage: PASS · ") {
		t.Fatalf("stdout = %q, want a properly separated PASS line for automation coverage", stdout.String())
	}
	if strings.Contains(stdout.String(), "automation coverage: FAIL") {
		t.Fatalf("stdout = %q, automation coverage must never FAIL — it observes nothing to fail on", stdout.String())
	}
}

// --- doctorVersionOlder: the file-private version comparator this phase's
// plan Placement decision explicitly sanctions (internal/space's own
// versionOlderThan is unexported to that package). ---

func TestDoctorVersionOlder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		binary, min string
		wantOlder   bool
		wantErr     bool
	}{
		{binary: "1.0.0", min: "0.5.0", wantOlder: false},
		{binary: "0.1.0", min: "0.2.0", wantOlder: true},
		{binary: "0.1.0", min: "0.1.0", wantOlder: false},
		{binary: "v0.1.0", min: "v0.1.0", wantOlder: false},
		{binary: "not-a-version", min: "0.1.0", wantErr: true},
		{binary: "0.1.0", min: "", wantErr: true},
	}
	for _, tc := range cases {
		older, err := doctorVersionOlder(tc.binary, tc.min)
		if tc.wantErr {
			if err == nil {
				t.Errorf("doctorVersionOlder(%q, %q): want error, got nil", tc.binary, tc.min)
			}
			continue
		}
		if err != nil {
			t.Errorf("doctorVersionOlder(%q, %q): unexpected error %v", tc.binary, tc.min, err)
		}
		if older != tc.wantOlder {
			t.Errorf("doctorVersionOlder(%q, %q) = %v, want %v", tc.binary, tc.min, older, tc.wantOlder)
		}
	}
}

// --- space-template/space.yaml: schema-valid instance proof (AC-101.1
// green-on-empty). space-template/** is pure data (no imports); this test
// reads it off disk and validates it against the P2 manifest schema exactly
// the way any real consumer would. ---

func TestDoctorSpaceTemplateManifestValidatesWithZeroParticipants(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(spaceTemplateRoot + "/space.yaml")
	if err != nil {
		t.Fatalf("read space-template/space.yaml: %v", err)
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("schema.DecodeYAMLInstance: %v", err)
	}

	var doc struct {
		Schema       string `yaml:"schema"`
		Participants []any  `yaml:"participants"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if len(doc.Participants) != 0 {
		t.Fatalf("space-template/space.yaml must ship with zero participants (AC-101.1 green-on-empty), got %d", len(doc.Participants))
	}

	violations, err := corpus.ValidateManifest(doc.Schema, instance)
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("space-template/space.yaml must validate clean against schemas/manifest/v1/space.schema.json, got violations: %+v", violations)
	}
}

// --- Workflow/CODEOWNERS artifact assertions (spec 09 §8 AC rows 6-8, 10 —
// template-artifact-checkable without a live GitHub repo). ---

func TestDoctorWorkflowCheckNameByteEquality(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(spaceTemplateRoot + "/.github/workflows/a2a-validate.yml")
	if err != nil {
		t.Fatalf("read a2a-validate.yml: %v", err)
	}

	var wf struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
			If   string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	prJob, ok := wf.Jobs["a2a-validate"]
	if !ok {
		t.Fatal("workflow has no `a2a-validate` job")
	}
	if prJob.Name != "a2a-validate" {
		t.Fatalf("a2a-validate job's name = %q, want byte-identical \"a2a-validate\" (AC row 6)", prJob.Name)
	}
	if !strings.Contains(prJob.If, "pull_request") {
		t.Fatalf("a2a-validate job's `if` = %q, want it scoped to pull_request (blocking gate)", prJob.If)
	}

	for id, job := range wf.Jobs {
		if id == "a2a-validate" {
			continue
		}
		if job.Name == "a2a-validate" {
			t.Fatalf("job %q also emits the name %q — collides with the required-check context (AC row 10)", id, job.Name)
		}
		if strings.Contains(job.If, "pull_request") {
			t.Fatalf("job %q also runs on pull_request — only one job may emit the a2a-validate check", id)
		}
	}
}

func TestDoctorWorkflowPushJobNeverRequiredCheck(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(spaceTemplateRoot + "/.github/workflows/a2a-validate.yml")
	if err != nil {
		t.Fatalf("read a2a-validate.yml: %v", err)
	}

	var wf struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
			If   string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	found := false
	for id, job := range wf.Jobs {
		if strings.Contains(job.If, "push") {
			found = true
			if job.Name == "a2a-validate" {
				t.Fatalf("push-triggered job %q must never carry the a2a-validate check name (AC row 10)", id)
			}
		}
	}
	if !found {
		t.Fatal("workflow has no push-triggered job (flag-only post-merge audit, §5.5 V3 row)")
	}
}

func TestDoctorWorkflowPinnedNotLatest(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(spaceTemplateRoot + "/.github/workflows/a2a-validate.yml")
	if err != nil {
		t.Fatalf("read a2a-validate.yml: %v", err)
	}
	// P33: the space is a CALLER — it carries no validation logic and no
	// version/fetch of its own. Every `uses:` must pin a2ahub's reusable
	// workflow to an immutable RELEASE TAG `@vMAJOR.MINOR.PATCH` (never
	// @main/@latest/a branch; tags are pushed-once/immutable in a2ahub's
	// publish model — AC-933.1 as amended). Only non-comment lines matter —
	// the file's own comments legitimately say the word "latest" to forbid it.
	usesRefs := 0
	for _, line := range strings.Split(string(raw), "\n") {
		code, _, _ := strings.Cut(line, "#")
		if strings.Contains(code, "\"latest\"") || strings.Contains(code, ": latest") {
			t.Fatalf("workflow pins to \"latest\", want an immutable SHA (AC-933.1): %q", line)
		}
		// Token checks scan CODE only — comments legitimately name the dead
		// secret / var to say they are gone (as does this file's header).
		if strings.Contains(code, "A2A_BINARY_FETCH_TOKEN") {
			t.Fatalf("P33 killed the fetch-token secret — the space must not USE A2A_BINARY_FETCH_TOKEN: %q", line)
		}
		if strings.Contains(code, "A2A_VALIDATOR_VERSION") {
			t.Fatalf("P33: the a2a version lives in the reusable-workflow ref, not an A2A_VALIDATOR_VERSION env: %q", line)
		}
		trimmed := strings.TrimSpace(code)
		if !strings.HasPrefix(trimmed, "uses:") {
			continue
		}
		usesRefs++
		if !strings.Contains(trimmed, "a2a-validate-reusable.yml") {
			t.Fatalf("caller `uses:` must reference a2ahub's reusable validation workflow, got %q", trimmed)
		}
		at := strings.Index(trimmed, "@")
		if at < 0 {
			t.Fatalf("caller `uses:` must be pinned to a release tag (never unpinned/@main), got %q", trimmed)
		}
		ref := strings.TrimSpace(trimmed[at+1:])
		if ref == "main" || ref == "master" || ref == "latest" {
			t.Fatalf("caller `uses:` pinned to a moving ref %q, want an immutable release tag @vMAJOR.MINOR.PATCH (AC-933.1)", ref)
		}
		// Immutable release tag: v<major>.<minor>.<patch>, each part numeric.
		validTag := strings.HasPrefix(ref, "v")
		if validTag {
			parts := strings.Split(strings.TrimPrefix(ref, "v"), ".")
			validTag = len(parts) == 3
			for _, p := range parts {
				if p == "" {
					validTag = false
				}
				for _, r := range p {
					if r < '0' || r > '9' {
						validTag = false
					}
				}
			}
		}
		if !validTag {
			t.Fatalf("caller `uses:` ref %q is not an immutable release tag @vMAJOR.MINOR.PATCH (AC-933.1)", ref)
		}
	}
	if usesRefs == 0 {
		t.Fatal("caller has no `uses:` reference to the reusable validation workflow (P33)")
	}
}

func TestDoctorCodeownersGatedPathsOnly(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(spaceTemplateRoot + "/CODEOWNERS")
	if err != nil {
		t.Fatalf("read CODEOWNERS: %v", err)
	}

	var gatedPaths []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		gatedPaths = append(gatedPaths, fields[0])
	}

	want := map[string]bool{"/space.yaml": true, "/decisions/**": true}
	if len(gatedPaths) != len(want) {
		t.Fatalf("CODEOWNERS gated paths = %v, want exactly %v (AC row 8: no /<system>/provides/** pre-seeding)", gatedPaths, want)
	}
	for _, p := range gatedPaths {
		if !want[p] {
			t.Fatalf("CODEOWNERS has unexpected gated path %q (only /space.yaml and /decisions/** belong at template time)", p)
		}
		if strings.Contains(p, "provides") {
			t.Fatalf("CODEOWNERS must not pre-seed a /<system>/provides/** entry, found %q", p)
		}
	}
}

// TestDoctorCheckCodeownersResolvable is the hermetic half of the check that a
// live walk on 2026-07-26 turned from a hand-verification into a gate.
//
// The class it catches: GitHub IGNORES a CODEOWNERS owner it cannot resolve
// rather than rejecting it, so a file naming a team nobody created looks like
// it gates `/space.yaml` and gates nothing — and code-owner review is the
// whole mechanism behind G4. This shipped TWICE as a documentation problem
// (first a placeholder whose team name survived the natural edit, then one
// whose "replace both halves" instruction still yields `@org/login`, a team
// reference) before anybody thought to ask GitHub, which answers with line
// numbers.
//
// Three outcomes, matching doctorCheckAutoMerge's shape rather than inventing
// a second convention, and the third is the one worth stating: a read the
// credential cannot make is an ADVISORY, never a FAIL. A fine-grained token
// without "Repository metadata: read" is a legitimate working setup, and a
// gate that reds on a working setup is a gate people stop reading.
func TestDoctorCheckCodeownersResolvable(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
	machine := space.MachineConfig{}
	withToken := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}
	// The handler shape mirrors what real GitHub returned when driven by hand
	// against a fresh space — including that `errors` is an ARRAY on the happy
	// path too, not an absent key.
	serve := func(t *testing.T, status int, body string) *httptest.Server {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	t.Run("pass when every owner resolves", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, 200, `{"errors":[]}`)
		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(srv.Client(), srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, machine)
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
		if detail != "" {
			t.Errorf("detail = %q, want empty on a clean pass", detail)
		}
	})

	t.Run("fail quoting GitHub's own line and suggestion", func(t *testing.T) {
		t.Parallel()
		// Verbatim from a real response (2026-07-26), including the wording that
		// names all THREE conditions an owner must satisfy — the reason the
		// suggestion is quoted rather than paraphrased.
		srv := serve(t, 200, `{"errors":[
			{"line":36,"kind":"Unknown owner","message":"Unknown owner on line 36",
			 "suggestion":"make sure the team @acme/space-admins exists, is publicly visible, and has write access to the repository"},
			{"line":37,"kind":"Unknown owner","message":"Unknown owner on line 37",
			 "suggestion":"make sure the team @acme/space-admins exists, is publicly visible, and has write access to the repository"}
		]}`)
		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(srv.Client(), srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, machine)
		if ok {
			t.Fatal("want fail, got pass — an unresolvable owner means the trust root is ungated")
		}
		for _, want := range []struct{ substr, why string }{
			{"getvisa", "name the space"},
			{"line 36", "name the line, which is the only part a reader can act on directly"},
			{"line 37", "name EVERY bad line, not just the first"},
			{"publicly visible", "quote GitHub's own three conditions rather than paraphrasing one of them"},
			{"gates nothing it appears to gate", "say what is actually at stake"},
		} {
			if !strings.Contains(detail, want.substr) {
				t.Errorf("detail is missing %q — %s\ngot: %s", want.substr, want.why, detail)
			}
		}
		// Driven live, one paragraph of explanation per bad line buried the part
		// that differs under the part that does not.
		if n := strings.Count(detail, "gates nothing it appears to gate"); n != 1 {
			t.Errorf("the explanation appears %d times for one space — say it once and list the lines, "+
				"or the line numbers drown in repetition", n)
		}
	})

	t.Run("a read the credential cannot make is an advisory, never a fail", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, 403, `{"message":"Resource not accessible by personal access token"}`)
		cmd := newTestDoctorCommand()
		cmd.h = host.NewGitHubHost(srv.Client(), srv.URL)
		cmd.resolveCredential = withToken

		ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, machine)
		if !ok {
			t.Fatal("want PASS-with-advisory: a fine-grained token without \"Repository metadata: read\" " +
				"is a legitimate working setup, and a gate that reds on one is a gate people stop reading")
		}
		if detail == "" {
			t.Fatal("want an advisory note — a silent PASS here reproduces the original defect with a " +
				"green check beside it")
		}
		if !strings.Contains(detail, "unverified") {
			t.Errorf("detail = %q, want it to say the answer is UNKNOWN rather than good", detail)
		}
	})

	t.Run("no connected spaces is not a finding", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), space.ProjectConfig{}, machine)
		if !ok || detail != "" {
			t.Fatalf("want a silent pass with no spaces; got ok=%v detail=%q", ok, detail)
		}
	})

	t.Run("a host wiring no reader says so rather than passing silently", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand() // host.NewFakeHost() implements no CodeownersErrors
		ok, detail := cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, machine)
		if !ok {
			t.Fatalf("an unwired reader is not a failure; got fail: %s", detail)
		}
		if !strings.Contains(detail, "unverified") {
			t.Errorf("detail = %q, want it to name that nothing was checked", detail)
		}
	})
}

// TestDoctorSkillAdvisoryDistinguishesNoSurfaceFromUnlinked is the regression
// for an advisory that named a remedy which cannot work.
//
// Two states were collapsed into one message: a surface EXISTS and is not linked
// (where `a2a skill link` is exactly right), and NO surface exists at all (where
// `a2a skill link` answers "no known agent surface detected — nothing to link"
// and the advisory then repeats verbatim on the next doctor).
//
// Found on 2026-07-26 by following the advice and watching nothing change. Loud,
// specific and unactionable is the same family as the validation gate that named
// the wrong author: it sends the reader somewhere that cannot help, and their
// next move is to stop trusting the check.
func TestDoctorSkillAdvisoryDistinguishesNoSurfaceFromUnlinked(t *testing.T) {
	t.Parallel()

	installSkill := func(t *testing.T, root string) {
		t.Helper()
		dir := filepath.Join(root, ".a2ahub", "skill")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# skill\n"), 0o644); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}

	t.Run("no agent surface at all: says there is nothing to link", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		installSkill(t, root)

		cmd := newTestDoctorCommand()
		cmd.projectRoot = root
		ok, detail := cmd.doctorCheckSkillDiscoverable()
		if !ok {
			t.Fatalf("this row never FAILs; got fail: %s", detail)
		}
		if strings.Contains(detail, "a2a skill link") {
			t.Errorf("the advisory still tells a surface-less project to run `a2a skill link`, which "+
				"answers \"nothing to link\" and changes nothing:\n%s", detail)
		}
		if !strings.Contains(detail, "nothing to link") {
			t.Errorf("detail = %q, want it to say plainly that there is nothing to link here", detail)
		}
	})

	t.Run("a surface exists but is unlinked: names it and the remedy", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		installSkill(t, root)
		// A surface is DETECTED by its own directory existing.
		if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
			t.Fatalf("mkdir .claude: %v", err)
		}

		cmd := newTestDoctorCommand()
		cmd.projectRoot = root
		ok, detail := cmd.doctorCheckSkillDiscoverable()
		if !ok {
			t.Fatalf("this row never FAILs; got fail: %s", detail)
		}
		if !strings.Contains(detail, "a2a skill link") {
			t.Errorf("detail = %q, want the remedy — HERE it actually works", detail)
		}
		if !strings.Contains(detail, "claude") {
			t.Errorf("detail = %q, want the detected surface named, so the reader knows what will be "+
				"linked", detail)
		}
	})
}

// --- doctorCheckSkippedFiles ("skipped mirror files" row) ------------------
//
// Stage 2 of the defect filed 2026-07-26: the read model's best-effort walk
// (internal/cache/skipped.go) already reports which mirror files it could
// not decode; this row surfaces that fact for the operator who runs `a2a
// doctor` without ever hitting a skip through a read verb. Table-driven,
// t.Parallel() throughout (this phase's own testing convention).

// doctorSkippedFilesSpaceYAML is a minimal, always-parseable space.yaml —
// participants are irrelevant to skip detection (buildIndex only consults
// the manifest for FOLD, not for the decode stage skipped.go reports on),
// so an empty list is deliberate, mirroring doctorCheckVersions' own
// fixture shape (TestDoctorCheckVersions, this file).
const doctorSkippedFilesSpaceYAML = "schema: space/v1\nspace: getvisa\nmin_binary_version: 0.0.0\nparticipants: []\n"

func writeDoctorSkippedFilesSpaceYAML(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(doctorSkippedFilesSpaceYAML), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
}

func TestDoctorCheckSkippedFiles(t *testing.T) {
	t.Parallel()

	t.Run("zero connected spaces passes with no advisory", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		ok, detail := cmd.doctorCheckSkippedFiles(context.Background(), space.ProjectConfig{}, space.MachineConfig{})
		if !ok || detail != "" {
			t.Fatalf("got ok=%v detail=%q, want pass with no advisory", ok, detail)
		}
	})

	t.Run("clean space passes with no advisory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeDoctorSkippedFilesSpaceYAML(t, dir)
		// One well-formed artifact — the best-effort property this row
		// depends on (a bad file elsewhere must not blind it) has nothing to
		// prove here yet; this case is the "nothing skipped" baseline.
		goodPath := filepath.Join(dir, "axon", "exchanges", "XW-axon-good.md")
		if err := os.MkdirAll(filepath.Dir(goodPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goodPath, []byte("---\nid: XW-axon-good\ntitle: t\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := newTestDoctorCommand()
		cmd.readFile = os.ReadFile
		cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return dir }
		cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}

		ok, detail := cmd.doctorCheckSkippedFiles(context.Background(), cfg, space.MachineConfig{})
		if !ok || detail != "" {
			t.Fatalf("got ok=%v detail=%q, want pass with no advisory (a gate that fires on a clean space is a gate people silence)", ok, detail)
		}
	})

	t.Run("skipped file: PASS with an advisory naming the path and reason", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeDoctorSkippedFilesSpaceYAML(t, dir)
		badPath := filepath.Join(dir, "axon", "exchanges", "XW-axon-bad.md")
		if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
			t.Fatal(err)
		}
		// The exact defect shape: a `thread:` key written twice — well-formed
		// frontmatter delimiters, undecodable YAML underneath (skipped_test.go's
		// own fixture, internal/cache).
		if err := os.WriteFile(badPath, []byte("---\nid: XW-axon-bad\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbad\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := newTestDoctorCommand()
		cmd.readFile = os.ReadFile
		cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return dir }
		cfg := space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}

		ok, detail := cmd.doctorCheckSkippedFiles(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("this row never FAILs on an undecodable file — see its own doc comment (counterparty diff-authz); got fail: %s", detail)
		}
		if !strings.Contains(detail, "axon/exchanges/XW-axon-bad.md") {
			t.Fatalf("detail = %q, want the skipped path named", detail)
		}
		if !strings.Contains(detail, "undecodable-yaml") {
			t.Fatalf("detail = %q, want the skip reason named", detail)
		}
		if !strings.Contains(detail, "getvisa") {
			t.Fatalf("detail = %q, want the owning space named", detail)
		}
	})

	t.Run("Run wires the row into the aggregate report without failing doctor", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeDoctorSkippedFilesSpaceYAML(t, dir)
		badPath := filepath.Join(dir, "axon", "exchanges", "XW-axon-bad2.md")
		if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(badPath, []byte("just some markdown, no frontmatter delimiters at all\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// The rest of the fleet must be green too, so this test isolates
		// THIS row's own contribution to the exit code rather than proving
		// nothing about it (a red run for an unrelated reason would still
		// satisfy "stdout contains the PASS line" vacuously).
		if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "a2a-validate.yml"), []byte("name: a2a-validate\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := newTestDoctorCommand()
		cmd.readFile = os.ReadFile
		cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return dir }
		cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{Token: "test-token"}, nil
		}
		cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
			return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/getvisa.git"}}}, nil
		}
		cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
		cmd.lookupGit = func() error { return nil }
		cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }

		var stdout, stderr bytes.Buffer
		code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (an advisory-only row must not fail doctor); stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "skipped mirror files: PASS") {
			t.Fatalf("stdout missing the %q PASS line; got %q", "skipped mirror files", stdout.String())
		}
		if !strings.Contains(stdout.String(), "XW-axon-bad2.md") {
			t.Fatalf("stdout = %q, want the skipped path named in the aggregate report", stdout.String())
		}
	})
}

// TestDoctorCheckDefaultBranchHealthyFAILIncludesRunURL is this phase's own
// acceptance: `default branch healthy`'s FAIL message used to name the
// space, the branch, and the selected check run's NAME and CONCLUSION —
// good evidence, but not a link the operator can click, on the one row
// whose entire job is to send them to a failing run
// (docs/backlog.md § "CheckStatusResult carries no run URL"). Once
// host.CheckStatusResult carries a URL, the FAIL line must carry it too.
func TestDoctorCheckDefaultBranchHealthyFAILIncludesRunURL(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git",
	}}}
	const runURL = "https://github.com/acme/getvisa/runs/999888777"

	fake := host.NewFakeHost()
	fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "completed", Conclusion: "failure", Name: "a2a-validate", URL: runURL}, nil
	}
	cmd := newTestDoctorCommand()
	cmd.h = fake
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
	if ok {
		t.Fatalf("want FAIL, got PASS: %s", detail)
	}
	if !strings.Contains(detail, runURL) {
		t.Fatalf("detail = %q, want the failing check run's URL (%s) so the operator has something to click", detail, runURL)
	}
	// The pre-existing evidence (space, conclusion) must still be present —
	// the URL is an addition, not a replacement.
	if !strings.Contains(detail, "getvisa") || !strings.Contains(detail, "failure") {
		t.Fatalf("detail = %q, want the space and conclusion still named alongside the URL", detail)
	}
}

// TestDoctorCheckDefaultBranchHealthyFAILDegradesWithoutRunURL proves the
// row degrades the same way it already did before URL existed: when GitHub
// reports no html_url for the selected run, the FAIL message stays the
// useful sentence it always was — never an empty link, never a bare
// "unknown" in its place.
func TestDoctorCheckDefaultBranchHealthyFAILDegradesWithoutRunURL(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git",
	}}}

	fake := host.NewFakeHost()
	fake.RefCheckStatusFunc = func(context.Context, host.RefStatusRequest) (host.CheckStatusResult, error) {
		return host.CheckStatusResult{State: "completed", Conclusion: "failure", Name: "a2a-validate"}, nil // URL deliberately absent
	}
	cmd := newTestDoctorCommand()
	cmd.h = fake
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
	if ok {
		t.Fatalf("want FAIL, got PASS: %s", detail)
	}
	if !strings.Contains(detail, "getvisa") || !strings.Contains(detail, "failure") {
		t.Fatalf("detail = %q, want the space and conclusion named even with no run URL", detail)
	}
	if strings.Contains(detail, " — ") {
		t.Fatalf("detail = %q, want no dangling link separator when URL is absent", detail)
	}
}

// doctorMirrorOnBranch builds a real local git mirror (bare origin + clone)
// whose refs/remotes/origin/HEAD resolves to branch — the fixture
// doctorCheckDefaultBranchHealthy's own base-branch derivation
// (no-silent-yes-2026-08 P2b, space.ResolveBaseBranch) needs, which none of
// this file's other "mirror" fixtures provide (they are plain directories
// seeded with hand-written files, no .git at all).
func doctorMirrorOnBranch(t *testing.T, branch string) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	contractGitRun(t, "", "init", "--bare", "-q", "-b", branch, origin)

	seed := t.TempDir()
	contractGitRun(t, "", "init", "-q", "-b", branch, seed)
	if err := os.WriteFile(filepath.Join(seed, "f.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	contractGitRun(t, seed, "add", "-A")
	contractGitRun(t, seed, "commit", "-q", "-m", "seed")
	contractGitRun(t, seed, "remote", "add", "origin", origin)
	contractGitRun(t, seed, "push", "-q", "origin", branch)

	clone := filepath.Join(t.TempDir(), "clone")
	contractGitRun(t, "", "clone", "-q", origin, clone)
	return clone
}

// TestDoctorCheckDefaultBranchHealthyDerivesNonMainBranchAndProbesIt is this
// phase's own acceptance: a space whose remote default is "master" makes
// this check PROBE "master" — never the "main" it always checked before —
// and NAME "master" in the discoverability detail (epic AC-2).
func TestDoctorCheckDefaultBranchHealthyDerivesNonMainBranchAndProbesIt(t *testing.T) {
	t.Parallel()
	mirror := doctorMirrorOnBranch(t, "master")
	cfg := space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git",
	}}}

	var gotRef string
	fake := host.NewFakeHost()
	fake.RefCheckStatusFunc = func(_ context.Context, req host.RefStatusRequest) (host.CheckStatusResult, error) {
		gotRef = req.Ref
		return host.CheckStatusResult{State: "completed", Conclusion: "success", Name: "a2a-validate"}, nil
	}
	cmd := newTestDoctorCommand()
	cmd.h = fake
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
	if !ok {
		t.Fatalf("want PASS, got FAIL: %s", detail)
	}
	if gotRef != "master" {
		t.Fatalf("RefCheckStatus was asked about Ref=%q, want the DERIVED %q, never the old hardcoded \"main\"", gotRef, "master")
	}
	if !strings.Contains(detail, "getvisa: master") {
		t.Fatalf("detail = %q, want it to name the derived base branch (AC-2 discoverability: \"a2a doctor\" names the branch it will push to)", detail)
	}
}

// TestDoctorCheckDefaultBranchHealthyNamesBranchEvenWithNoRefStatusReader is
// the trap this phase's own brief named explicitly: the discoverability
// instrument (AC-2) must survive the "this build wires no ref status
// reader" early return, which used to skip ALL per-space work — a build
// with no reader is exactly the build most likely to be a fresh join, where
// naming the push target matters most.
func TestDoctorCheckDefaultBranchHealthyNamesBranchEvenWithNoRefStatusReader(t *testing.T) {
	t.Parallel()
	mirror := doctorMirrorOnBranch(t, "master")
	cfg := space.ProjectConfig{Spaces: []space.Ref{{
		ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git",
	}}}

	cmd := newTestDoctorCommand()
	cmd.h = doctorHostWithoutRefReader{Host: host.NewFakeHost()}
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }

	ok, detail := cmd.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
	if !ok {
		t.Fatalf("want advisory PASS, got FAIL: %s", detail)
	}
	if !strings.Contains(detail, "getvisa: master") {
		t.Fatalf("detail = %q, want the derived base branch named even when this build wires no ref status reader", detail)
	}
}

// --- doctorUnadoptedConsumptionRows (defects-fix-2026-08 P6) ---
//
// These write the same on-disk shape internal/cache's own registered_
// consumers_test.go fixtures use (a handoff .md, its lifecycle events, a
// data package manifest.json), because doctorUnadoptedConsumptionRows is a
// thin per-space wrapper over cache.FindUnadoptedConsumption — the
// detection logic itself is exercised there; these tests are about the
// row/advisory SHAPE this package produces from that fact.

func docWriteFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("docWriteFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("docWriteFile: write: %v", err)
	}
}

// docWriteAcceptedDelivery commits a verify-passed (or not, per accepted)
// handoff from "seomatrix" to "axon" whose one "data" deliverable resolves
// to a data-package/v1 manifest pinning contractRef.
func docWriteAcceptedDelivery(t *testing.T, mirror, handoffSuffix, packageSuffix, contractRef string, accepted bool) {
	t.Helper()
	handoffID := "XH-seomatrix-20260817-" + handoffSuffix
	packageID := "DP-seomatrix-20260818-" + packageSuffix

	docWriteFile(t, mirror, "seomatrix/"+handoffID+".md",
		"---\nschema: envelope/v1\nid: "+handoffID+"\ntype: handoff\ntitle: t\nspace: getvisa\n"+
			"from: seomatrix\nto: [axon]\ncreated: 2026-08-17T10:00:00Z\n"+
			"deliverables:\n  - name: dataset\n    ref: "+packageID+"\n    kind: data\n"+
			"verification: manual\nacceptance_criteria: [\"done\"]\nlimitations: []\n"+
			"fulfills: [\"XW-seomatrix-20260817-zzzz\"]\n---\nBody.\n")

	steps := []struct{ transition, system string }{{"submit", "seomatrix"}, {"acknowledge", "axon"}}
	if accepted {
		steps = append(steps, struct{ transition, system string }{"verify-pass", "axon"})
	}
	for i, s := range steps {
		ulid := fmt.Sprintf("01HFXH%s%011d", handoffSuffix, i+1)
		docWriteFile(t, mirror, fmt.Sprintf("seomatrix/events/2026/%s.yaml", ulid),
			fmt.Sprintf("schema: event/v1\nevent: %s\nspace: getvisa\n"+
				"subject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\n"+
				"at: 2026-08-17T%02d:00:00Z\n", ulid, handoffID, s.transition, s.system, 10+i))
	}

	docWriteFile(t, mirror, "seomatrix/data/"+packageID+"/manifest.json",
		fmt.Sprintf(`{"schema":"data-package/v1","id":%q,"contract":%q}`, packageID, contractRef))
}

func TestDoctorUnadoptedConsumptionRows_NamesContractWithCountAndAdoptCommand(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorUnadoptedConsumptionRows(cfg, space.MachineConfig{})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.SpaceID != "getvisa" {
		t.Fatalf("SpaceID = %q, want getvisa", row.SpaceID)
	}
	if row.Status != doctorVisibilityWARN {
		t.Fatalf("Status = %q, want WARN — consuming without adopting must never fail doctor by itself", row.Status)
	}
	for _, want := range []string{"1 verify-passed delivery conforms to", "XC-seomatrix-regime-corpus", "a2a contract adopt XC-seomatrix-regime-corpus"} {
		if !strings.Contains(row.Detail, want) {
			t.Fatalf("Detail = %q, want it to contain %q", row.Detail, want)
		}
	}
}

// TestDoctorUnadoptedConsumptionRows_PluralCountAgreesGrammatically is the
// count!=1 branch TestDoctorUnadoptedConsumptionRows_NamesContractWithCountAndAdoptCommand's
// own count==1 fixture cannot exercise: the noun (delivery/deliveries) AND
// its verb (conforms/conform) must both flip together, or "2 verify-passed
// deliveries conforms to" ships instead of "2 verify-passed deliveries
// conform to".
func TestDoctorUnadoptedConsumptionRows_PluralCountAgreesGrammatically(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)
	docWriteAcceptedDelivery(t, mirror, "bbbb", "jgf1", "XC-seomatrix-regime-corpus@1.0.0#bbb222", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorUnadoptedConsumptionRows(cfg, space.MachineConfig{})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly 1: %+v", len(rows), rows)
	}
	if !strings.Contains(rows[0].Detail, "2 verify-passed deliveries conform to XC-seomatrix-regime-corpus") {
		t.Fatalf("Detail = %q, want the plural noun AND verb to agree", rows[0].Detail)
	}
}

func TestDoctorUnadoptedConsumptionRows_SilentWhenNoUnadoptedDelivery(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorUnadoptedConsumptionRows(cfg, space.MachineConfig{})
	if len(rows) != 0 {
		t.Fatalf("got %+v, want no rows — nothing was verify-passed against an unadopted contract", rows)
	}
}

func TestDoctorUnadoptedConsumptionRows_MalformedRegistryReportsUnverifiedNotWarn(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	docWriteFile(t, mirror, "axon/consumes.yaml", "consumes: []\n") // placeholder shape, refused
	docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", true)

	cmd := newTestDoctorCommand()
	cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
	cfg := space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "getvisa"}}}

	rows := cmd.doctorUnadoptedConsumptionRows(cfg, space.MachineConfig{})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want exactly 1 (the unverified row, no false contract list): %+v", len(rows), rows)
	}
	if rows[0].Status != doctorVisibilityUNVERIFIED {
		t.Fatalf("Status = %q, want UNVERIFIED — an unreadable registry must never round down to a (possibly false) WARN advisory", rows[0].Status)
	}
	if !strings.Contains(rows[0].Detail, "consumes.yaml") {
		t.Fatalf("Detail = %q, want it to name the registry path", rows[0].Detail)
	}
}

func TestDoctorUnadoptedConsumptionRows_NoOwnSystemConfiguredReturnsNil(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	rows := cmd.doctorUnadoptedConsumptionRows(space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}, space.MachineConfig{})
	if rows != nil {
		t.Fatalf("got %+v, want nil — no configured system id means nothing can be resolved as \"mine\"", rows)
	}
}

// TestDoctorRunUnadoptedConsumptionWarnDoesNotChangeExitCode is spec 06
// AC2's own claim, tested literally: the SAME connected space, byte-
// identical except for one extra committed event (the verify-pass that
// flips the new advisory on), must produce the SAME exit code either way —
// "the check is a WARN and does not by itself change `a2a doctor`'s exit
// code" (spec 06 §T1). This does not require the rest of doctor to be
// green; it only requires the WARN row's presence to make no difference to
// whatever exit code every OTHER check already produces.
func TestDoctorRunUnadoptedConsumptionWarnDoesNotChangeExitCode(t *testing.T) {
	t.Parallel()

	buildMirror := func(t *testing.T, accepted bool) string {
		root := t.TempDir()
		mirror := filepath.Join(root, "mirror")
		manifest := "schema: manifest/v1\nspace: getvisa\nmin_binary_version: 0.0.0\n" +
			"participants:\n" +
			"  - system: axon\n    org: o\n    section: axon\n    owners: [me]\n    status: active\n    joined: 2026-08-01\n" +
			"  - system: seomatrix\n    org: o\n    section: seomatrix\n    owners: [me]\n    status: active\n    joined: 2026-08-01\n"
		docWriteFile(t, mirror, "space.yaml", manifest)
		docWriteFile(t, mirror, ".github/workflows/a2a-validate.yml", "name: a2a-validate\n")
		docWriteFile(t, mirror, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")
		docWriteAcceptedDelivery(t, mirror, "aaaa", "p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111", accepted)
		return mirror
	}

	runOnce := func(t *testing.T, mirror string) (int, string) {
		t.Helper()
		cmd := newTestDoctorCommand()
		cmd.resolveMirror = func(string, space.Ref, space.MachineConfig) string { return mirror }
		cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
			return space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://example.invalid/getvisa.git"}}}, nil
		}
		cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
		cmd.lookupGit = func() error { return nil }
		cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }
		cmd.resolveCredential = func(context.Context, string, space.CredentialReference) (host.Credential, error) {
			return host.Credential{Token: "tok"}, nil
		}
		var stdout, stderr bytes.Buffer
		code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
		return code, stdout.String()
	}

	baselineCode, baselineOut := runOnce(t, buildMirror(t, false))
	if strings.Contains(baselineOut, "consumed contract [") {
		t.Fatalf("baseline (not yet verify-passed) unexpectedly already carries a consumed-contract row: %s", baselineOut)
	}
	if baselineCode != 0 {
		t.Fatalf("baseline exit code = %d, want 0 (every OTHER check must already be green here, or the mutation this test exists to catch — wiring the new WARN row into allOK — cannot be observed): stdout=%q", baselineCode, baselineOut)
	}

	warnCode, warnOut := runOnce(t, buildMirror(t, true))
	if !strings.Contains(warnOut, "consumed contract [getvisa]: WARN:") {
		t.Fatalf("stdout = %q, want a WARN row naming the unadopted contract once the handoff is verify-passed", warnOut)
	}
	if !strings.Contains(warnOut, "run `a2a contract adopt XC-seomatrix-regime-corpus`") {
		t.Fatalf("stdout = %q, want the exact `a2a contract adopt <id>` invocation", warnOut)
	}
	if warnCode != baselineCode {
		t.Fatalf("exit code changed from %d (no WARN) to %d (WARN present) — a WARN-only advisory must never change `a2a doctor`'s exit code", baselineCode, warnCode)
	}
}

// TestDoctorNamesTheUnmeasuredReleaseAxis: when NO release check has ever run
// on this machine, every surface is silent — this row, the per-verb advisory,
// the MCP note. Silence then reads as "you are current", which is a claim
// nobody made. `doctor` is what an agent runs when told to check its setup, so
// it is the place that must say "nobody has looked".
func TestDoctorNamesTheUnmeasuredReleaseAxis(t *testing.T) {
	t.Parallel()
	cmd := newTestDoctorCommand()
	cmd.binaryVersion = "0.24.0"
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	if code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("exit code = %d, want 0 — an unmeasured axis is not a failure", code)
	}
	if !strings.Contains(stdout.String(), "no release check has ever run on this machine") {
		t.Fatalf("doctor must name the unmeasured release axis; stdout=%q", stdout.String())
	}
}

// The paired half: with a check on disk, doctor must NOT claim nobody looked.
// Without this, the test above passes for a row that prints the sentence
// unconditionally.
func TestDoctorDoesNotClaimUnmeasuredWhenAcheckExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "update-check.json")
	if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: time.Now(), Latest: "0.24.0", Source: "github"}); err != nil {
		t.Fatalf("release.WriteCheck: %v", err)
	}
	cmd := newTestDoctorCommand()
	cmd.binaryVersion = "0.24.0"
	cmd.cachePath = func() (string, error) { return cachePath, nil }
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	if code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if strings.Contains(stdout.String(), "no release check has ever run") {
		t.Fatalf("a machine WITH a check must not be told nobody looked; stdout=%q", stdout.String())
	}
}
