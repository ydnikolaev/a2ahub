package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
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
	for _, name := range []string{"credentials", "space access", "space identity", "versions", "CI presence", "statusline wiring"} {
		if !strings.Contains(stdout.String(), name+": PASS") {
			t.Errorf("stdout missing %q PASS line; got %q", name, stdout.String())
		}
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
	cmd.cloneOrFetch = func(context.Context, string, string) error { return errors.New("boom") }
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
		cmd.cloneOrFetch = func(context.Context, string, string) error { return nil }
		ok, detail := cmd.doctorCheckSpaceAccess(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail unreachable mirror", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.cloneOrFetch = func(context.Context, string, string) error { return errors.New("connection refused") }
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

func TestDoctorCheckSkillManualCurrent_UpToDate_Clean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.3.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.3.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	if !strings.Contains(detail, "skill manual current (v0.3.0)") {
		t.Fatalf("detail = %q, want the current-manual note", detail)
	}
}

func TestDoctorCheckSkillManualCurrent_UnparseableProvenance_VersionUnknown(t *testing.T) {
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

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	if !strings.Contains(detail, "version unknown") {
		t.Fatalf("detail = %q, want a version-unknown note", detail)
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
	// Only non-comment lines matter here — the file's own explanatory
	// comments legitimately mention the word "latest" to say it must never
	// be used.
	for _, line := range strings.Split(string(raw), "\n") {
		code, _, _ := strings.Cut(line, "#")
		if strings.Contains(code, "\"latest\"") || strings.Contains(code, ": latest") {
			t.Fatalf("workflow line pins to \"latest\", want an explicit version (AC row 7): %q", line)
		}
	}
	content := string(raw)
	if !strings.Contains(content, "A2A_VALIDATOR_VERSION") {
		t.Fatal("workflow must name an explicit pinned-version variable (AC row 7)")
	}
	if !strings.Contains(content, "secrets.A2A_BINARY_FETCH_TOKEN") {
		t.Fatal("workflow must fetch the binary via the read-only token repo secret (§10.5 CI credential row)")
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
