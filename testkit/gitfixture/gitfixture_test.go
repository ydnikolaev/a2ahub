package gitfixture

import (
	"os"
	"os/exec"
	"path/filepath"
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

	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "5" {
		t.Fatalf("GIT_CONFIG_COUNT = %q, want 5 (1 preset + 4 appended)", got)
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
	if os.Getenv("GIT_CONFIG_KEY_3") != "user.name" || os.Getenv("GIT_CONFIG_VALUE_3") != "a2a-fixture" {
		t.Fatalf("GIT_CONFIG_KEY_3/VALUE_3 = %q/%q, want user.name/a2a-fixture",
			os.Getenv("GIT_CONFIG_KEY_3"), os.Getenv("GIT_CONFIG_VALUE_3"))
	}
	if os.Getenv("GIT_CONFIG_KEY_4") != "user.email" || os.Getenv("GIT_CONFIG_VALUE_4") != "fixture@a2ahub.invalid" {
		t.Fatalf("GIT_CONFIG_KEY_4/VALUE_4 = %q/%q, want user.email/fixture@a2ahub.invalid",
			os.Getenv("GIT_CONFIG_KEY_4"), os.Getenv("GIT_CONFIG_VALUE_4"))
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
	if got := os.Getenv("GIT_CONFIG_COUNT"); got != "4" {
		t.Fatalf("GIT_CONFIG_COUNT after two HardenEnv calls = %q, want 4 (no duplicate entries)", got)
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

	file := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(file, []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-C", dir, "add", "fixture.txt"}, {"-C", dir, "commit", "-q", "-m", "fixture identity"}} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	show := exec.Command("git", "-C", dir, "show", "-s", "--format=%an <%ae>|%cn <%ce>")
	identity, err := show.Output()
	if err != nil {
		t.Fatalf("git show identity: %v", err)
	}
	if got, want := strings.TrimSpace(string(identity)), "a2a-fixture <fixture@a2ahub.invalid>|a2a-fixture <fixture@a2ahub.invalid>"; got != want {
		t.Fatalf("commit identity = %q, want %q", got, want)
	}
}

// TestHardenRepo_StopsDetachedMaintenanceAfterPush is the guard for the gap
// that gc.auto=0 and maintenance.auto=false did NOT close, and that red the
// public repo's `check` on 2026-08-12 with
// "unlinkat …/origin.git: directory not empty".
//
// It is an isolating PAIR — an unhardened control and a hardened repo,
// pushing the same commit — and it has to be. A one-sided version of this
// test is what produced the wrong fix first: a push into an already
// up-to-date repo never runs receive-pack at all, so "zero maintenance
// spawns" was measured from a push that did nothing. The control is what
// makes the hardened side mean something.
//
// It asserts the MECHANISM rather than a flag list, also deliberately: the
// two flags in gitCoreFlags were present the whole time, and a test that
// only compared Args's contents to an expected slice went on passing while
// receive-pack forked `git maintenance run --auto --quiet --detach` after
// every push.
//
// reason: mutates process env (GIT_CONFIG_*) — not safe to run in parallel
// (see TestHardenEnv_ComposesOverPresetCount's reason comment).
func TestHardenRepo_StopsDetachedMaintenanceAfterPush(t *testing.T) {
	resetGitConfigEnv(t)
	HardenEnv()

	root := t.TempDir()
	work := filepath.Join(root, "work")
	control := filepath.Join(root, "control.git")
	hardened := filepath.Join(root, "hardened.git")

	for _, args := range [][]string{
		{"init", "--bare", "-q", "-b", "main", control},
		{"init", "--bare", "-q", "-b", "main", hardened},
		{"init", "-q", "-b", "main", work},
	} {
		if out, err := exec.Command("git", Args(args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	HardenRepo(t, hardened)

	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", work, "add", "-A"},
		{"-C", work, "commit", "-q", "-m", "seed"},
		{"-C", work, "remote", "add", "control", control},
		{"-C", work, "remote", "add", "hardened", hardened},
	} {
		if out, err := exec.Command("git", Args(args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// The control proves receive-pack actually ran and that this git build
	// does spawn detached maintenance by default. Without it, the hardened
	// assertion below is satisfied just as well by a push that did nothing.
	if got := maintenanceLines(pushTrace(t, work, "control")); got == "" {
		t.Fatal("control push spawned NO background maintenance — this test can no longer " +
			"detect the regression it exists for (git default changed, or the push was a no-op)")
	}
	if got := maintenanceLines(pushTrace(t, work, "hardened")); got != "" {
		t.Fatalf("push into a HardenRepo'd repo still spawned background maintenance:\n%s", got)
	}
}

// pushTrace pushes work's main to the named remote with GIT_TRACE on and
// returns everything git said.
func pushTrace(t *testing.T, work, remote string) string {
	t.Helper()
	push := exec.Command("git", Args("-C", work, "push", "-q", remote, "main")...)
	push.Env = append(os.Environ(), "GIT_TRACE=1")
	out, err := push.CombinedOutput()
	if err != nil {
		t.Fatalf("git push %s: %v\n%s", remote, err, out)
	}
	return string(out)
}

// maintenanceLines extracts just the offending trace lines, so a failure
// names the evidence instead of dumping the whole GIT_TRACE.
//
// TWO VOCABULARIES, because git changed how it says this and the trace string
// is the only evidence there is. Measured 2026-08-20 by pushing into a fresh
// bare repo on each:
//
//	git 2.55 (darwin):  run_command: git maintenance run --auto --quiet --detach
//	git 2.43 (ubuntu):  run_command: git gc --auto --quiet
//
// Matching only the first meant this detector saw nothing on the platform CI
// runs on. The control assertion above caught that — but had the control not
// existed, the hardened assertion would have passed VACUOUSLY there: it looks
// for a string git never emits, so a regression in HardenRepo would have been
// invisible on Linux while the test reported green.
//
// receive.autogc, which HardenRepoDir sets, governs both shapes, so the
// guarantee under test is one thing; only its spelling in the trace moved.
func maintenanceLines(trace string) string {
	var hits []string
	for _, line := range strings.Split(trace, "\n") {
		if strings.Contains(line, "maintenance run") || strings.Contains(line, "gc --auto") {
			hits = append(hits, line)
		}
	}
	return strings.Join(hits, "\n")
}
