package gitfixture

import (
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestArgs_PrependsFlagsBeforeSubcommand(t *testing.T) {
	t.Parallel()

	in := []string{"commit", "-m", "msg"}
	inSnapshot := append([]string(nil), in...)

	got := Args(in...)

	want := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "commit", "-m", "msg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args(%v) = %v, want %v", in, got, want)
	}
	if !reflect.DeepEqual(in, inSnapshot) {
		t.Fatalf("Args mutated its input: got %v, want unchanged %v", in, inSnapshot)
	}
}

func TestArgs_EmptyInput(t *testing.T) {
	t.Parallel()

	got := Args()
	want := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %v, want %v", got, want)
	}
}

// resetGitConfigEnv clears every GIT_CONFIG_* var for the duration of the
// test and restores the pre-test state afterwards (whatever it was,
// including entries added by the test itself) — these tests must never
// leak into, or inherit from, whatever the test binary's own TestMain
// already set via HardenEnv.
func resetGitConfigEnv(t *testing.T) {
	t.Helper()
	var original []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_CONFIG_") {
			original = append(original, kv)
		}
	}
	// Every name here comes out of os.Environ(), so it is already a legal
	// variable name — the only thing os.Setenv/os.Unsetenv can reject. The
	// errors are discarded explicitly for that reason. (t.Setenv is not usable
	// here: it can set but not UNSET, and this helper must clear entries the
	// test binary's own TestMain already installed.)
	for _, kv := range original {
		name, _, _ := strings.Cut(kv, "=")
		_ = os.Unsetenv(name)
	}
	t.Cleanup(func() {
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "GIT_CONFIG_") {
				name, _, _ := strings.Cut(kv, "=")
				_ = os.Unsetenv(name)
			}
		}
		for _, kv := range original {
			name, val, _ := strings.Cut(kv, "=")
			_ = os.Setenv(name, val)
		}
	})
}

func snapshotGitConfigEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_CONFIG_") {
			out = append(out, kv)
		}
	}
	sort.Strings(out)
	return out
}

// reason: mutates process env (GIT_CONFIG_*) — not safe to run in parallel
// with other env-mutating tests (rails pre-flight checklist #7, matching
// internal/space/credential_test.go's idiom).
func TestHardenEnv_ComposesOverPresetCount(t *testing.T) {
	resetGitConfigEnv(t)
	// Constant names; see resetGitConfigEnv for why the error is discarded.
	_ = os.Setenv("GIT_CONFIG_COUNT", "1")
	_ = os.Setenv("GIT_CONFIG_KEY_0", "user.name")
	_ = os.Setenv("GIT_CONFIG_VALUE_0", "someone")

	HardenEnv()

	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "3" {
		t.Fatalf("GIT_CONFIG_COUNT = %q, want 3 (1 preset + 2 appended)", got)
	}
	if os.Getenv("GIT_CONFIG_KEY_0") != "user.name" || os.Getenv("GIT_CONFIG_VALUE_0") != "someone" {
		t.Fatalf("HardenEnv clobbered the pre-set entry at index 0: KEY_0=%q VALUE_0=%q",
			os.Getenv("GIT_CONFIG_KEY_0"), os.Getenv("GIT_CONFIG_VALUE_0"))
	}
	if os.Getenv("GIT_CONFIG_KEY_1") != "gc.auto" || os.Getenv("GIT_CONFIG_VALUE_1") != "0" {
		t.Fatalf("GIT_CONFIG_KEY_1/VALUE_1 = %q/%q, want gc.auto/0",
			os.Getenv("GIT_CONFIG_KEY_1"), os.Getenv("GIT_CONFIG_VALUE_1"))
	}
	if os.Getenv("GIT_CONFIG_KEY_2") != "maintenance.auto" || os.Getenv("GIT_CONFIG_VALUE_2") != "false" {
		t.Fatalf("GIT_CONFIG_KEY_2/VALUE_2 = %q/%q, want maintenance.auto/false",
			os.Getenv("GIT_CONFIG_KEY_2"), os.Getenv("GIT_CONFIG_VALUE_2"))
	}
}

// reason: mutates process env (GIT_CONFIG_*) — not safe to run in parallel
// (see TestHardenEnv_ComposesOverPresetCount's reason comment).
func TestHardenEnv_Idempotent(t *testing.T) {
	resetGitConfigEnv(t)

	HardenEnv()
	first := snapshotGitConfigEnv()

	HardenEnv()
	second := snapshotGitConfigEnv()

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second HardenEnv call changed the env:\nbefore=%v\nafter =%v", first, second)
	}
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "2" {
		t.Fatalf("GIT_CONFIG_COUNT after two HardenEnv calls = %q, want 2 (no duplicate entries)", got)
	}
}

// reason: mutates process env (GIT_CONFIG_*) — not safe to run in parallel
// (see TestHardenEnv_ComposesOverPresetCount's reason comment). It is also
// a real subprocess check (git config --get), not a pure unit test.
func TestHardenEnv_EndToEnd_GitConfigGet(t *testing.T) {
	resetGitConfigEnv(t)
	HardenEnv()

	dir := t.TempDir()
	init := exec.Command("git", Args("init", "-q", dir)...)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	get := exec.Command("git", Args("-C", dir, "config", "--get", "gc.auto")...)
	out, err := get.Output()
	if err != nil {
		t.Fatalf("git config --get gc.auto: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != "0" {
		t.Fatalf("git config --get gc.auto = %q, want 0 (HardenEnv should make this true even without Args, but this routes through both)", got)
	}
}
