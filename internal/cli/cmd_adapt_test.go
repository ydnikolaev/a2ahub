package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// adaptTestCommand builds an AdaptCommand wired to a fixed corpus and a
// fixed adapted_through baseline, bypassing the real embedded corpus and
// the real filesystem (the same DI shape whatsnewTestCommand/
// newTestUpdateCommand already use in this package).
func adaptTestCommand(binaryVersion, baseline string, load func() ([]notes.ReleaseNotes, error)) *AdaptCommand {
	c := NewAdaptCommand(binaryVersion, "/fake/.a2a/config.yaml")
	c.load = load
	c.loadCurrentIssues = func() ([]notes.Change, error) { return nil, nil }
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{AdaptedThrough: baseline}, nil
	}
	return c
}

// adaptFixtureCorpus is a small synthetic corpus covering every group the
// order table names: a local change with a run: (group 1), local prose
// only (group 2), and a space change with a detect: (group 3) — plus one
// scope:none change that must never surface, and one release entirely
// before the baseline this file's tests use.
func adaptFixtureCorpus() []notes.ReleaseNotes {
	return []notes.ReleaseNotes{
		{Version: "0.10.0", Changes: []notes.Change{
			{ID: "OLD", Kind: "feat", Impact: "high", Subject: "before the baseline", Detail: "d",
				Action: notes.Action{Scope: "local", Why: "w"}},
		}},
		{Version: "0.20.0", Changes: []notes.Change{
			{ID: "RUNNABLE", Kind: "feat", Impact: "high", Subject: "a command exists", Detail: "d",
				Action: notes.Action{Scope: "local", Why: "w", Run: []string{"a2a doctor"}}},
		}},
		{Version: "0.21.0", Changes: []notes.Change{
			{ID: "DETECTED", Kind: "fix", Impact: "normal", Subject: "space obligation with a detect", Detail: "d",
				Action: notes.Action{Scope: "space", Why: "w", Detect: []string{"check-it"}}},
			{ID: "PROSE", Kind: "feat", Impact: "low", Subject: "prose only", Detail: "d",
				Action: notes.Action{Scope: "local", Why: "w"}},
			{ID: "IGNORED", Kind: "feat", Impact: "low", Subject: "never obliges anything", Detail: "d",
				Action: notes.Action{Scope: "none", Why: "w"}},
		}},
	}
}

func fixedAdaptLoad() ([]notes.ReleaseNotes, error) { return adaptFixtureCorpus(), nil }

// newAdaptFileStore is an in-memory readFile/writeFile pair — the same
// role a temp file would play, without touching disk. get() lets a test
// assert the file was (or was not) mutated.
func newAdaptFileStore(initial string) (
	read func(string) ([]byte, error),
	write func(string, []byte, os.FileMode) error,
	get func() string,
) {
	data := []byte(initial)
	read = func(string) ([]byte, error) {
		if data == nil {
			return nil, os.ErrNotExist
		}
		return append([]byte(nil), data...), nil
	}
	write = func(_ string, d []byte, _ os.FileMode) error {
		data = append([]byte(nil), d...)
		return nil
	}
	get = func() string { return string(data) }
	return read, write, get
}

// --- AC1-3, over the REAL embedded corpus (P13 brief: "prove ACs 1-3
// against the REAL corpus before anything else") -----------------------

func TestAdaptRealCorpus_Baseline0190_Binary0256(t *testing.T) {
	t.Parallel()
	c := NewAdaptCommand("0.25.6", "/fake/.a2a/config.yaml")
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{AdaptedThrough: "0.19.0"}, nil
	}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (obligations remain); stderr=%s", code, errOut.String())
	}
	text := out.String()

	// AC-3: opens with the imperative, closes with the outstanding count.
	if !strings.Contains(text, "44 obligations since v0.19.0 (23 releases)") {
		t.Fatalf("want the header naming 44/0.19.0/23, got:\n%s", text)
	}
	iOpen := strings.Index(text, "ADAPT THIS REPOSITORY TO THE CHANGES BELOW.")
	iClose := strings.Index(text, "44 obligations remain")
	if iOpen < 0 {
		t.Fatalf("want the imperative directive, got:\n%s", text)
	}
	if iClose < 0 {
		t.Fatalf("want the closing outstanding count, got:\n%s", text)
	}
	if iOpen > iClose {
		t.Fatalf("want the imperative before the closing count, got:\n%s", text)
	}

	// AC-1/AC-2: no scope:none change, neither standing known issue.
	if strings.Contains(text, "provider-tier verification deferred") {
		t.Fatalf("the provider-tier known issue is scope:none and must not appear:\n%s", text)
	}
	if strings.Contains(text, "ad-hoc signed") {
		t.Fatalf("the macOS known issue is scope:none and must not appear:\n%s", text)
	}
}

func TestAdaptRealCorpus_UpToDateIsNothingToAdapt(t *testing.T) {
	t.Parallel()
	c := NewAdaptCommand("0.25.6", "/fake/.a2a/config.yaml")
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{AdaptedThrough: "0.25.6"}, nil
	}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (nothing remains); stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "nothing to adapt") {
		t.Fatalf("stdout = %q, want 'nothing to adapt'", out.String())
	}
}

// --- AC-4/AC-5: the two clocks ------------------------------------------

func TestAdaptTwoClocks_WalksFromRepoBaselineNotBinaryHistory(t *testing.T) {
	t.Parallel()
	// Baseline 0.10.0, binary 0.21.0: a one-clock design keyed off "the
	// version the binary replaced" (e.g. 0.20.0, an in-range version this
	// fixture deliberately also carries its own obligation at) would hide
	// RUNNABLE. The repo's own baseline must still surface it.
	c := adaptTestCommand("0.21.0", "0.10.0", fixedAdaptLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("exit = %d, stderr=%s", code, errOut.String())
	}
	text := out.String()
	if strings.Contains(text, "before the baseline") {
		t.Fatalf("OLD (at/before the baseline) must not appear:\n%s", text)
	}
	for _, want := range []string{"a command exists", "prose only", "space obligation with a detect"} {
		if !strings.Contains(text, want) {
			t.Fatalf("want %q in output, got:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "3 obligations since v0.10.0 (2 releases)") {
		t.Fatalf("want the 0.10.0-baselined header (2 releases: 0.20.0, 0.21.0), got:\n%s", text)
	}
}

func TestAdaptNeverAdaptedStartsFromOldestAndSaysSo(t *testing.T) {
	t.Parallel()
	c := adaptTestCommand("0.21.0", "", fixedAdaptLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("exit = %d, stderr=%s", code, errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "never recorded adapted_through") || !strings.Contains(text, "v0.10.0") {
		t.Fatalf("want an explicit never-adapted notice naming the oldest release, got:\n%s", text)
	}
	if !strings.Contains(text, "before the baseline") {
		t.Fatalf("with no baseline, OLD (0.10.0) must be included too, got:\n%s", text)
	}
}

// --- AC-10: a baseline above the binary refuses -------------------------

func TestAdaptBaselineAboveBinaryRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		baseline      string
		binaryVersion string
		wantRefusal   bool
	}{
		{"baseline behind binary proceeds", "0.10.0", "0.21.0", false},
		{"baseline equal to binary proceeds (nothing pending)", "0.21.0", "0.21.0", false},
		{"baseline ahead of binary refuses", "0.25.0", "0.21.0", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := adaptTestCommand(tc.binaryVersion, tc.baseline, fixedAdaptLoad)
			var out, errOut bytes.Buffer
			code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
			if tc.wantRefusal {
				if code == 0 {
					t.Fatalf("exit = 0, want a refusal; stdout=%s", out.String())
				}
				if !strings.Contains(errOut.String(), tc.baseline) || !strings.Contains(errOut.String(), tc.binaryVersion) {
					t.Fatalf("stderr = %q, want it to name both %q and %q", errOut.String(), tc.baseline, tc.binaryVersion)
				}
			} else if code != 0 && code != 1 {
				t.Fatalf("exit = %d, want 0 or 1 (not a refusal); stderr=%s", code, errOut.String())
			}
		})
	}
}

// --- AC-6/7/8: --done -----------------------------------------------------

func adaptDoneTestCommand(t *testing.T, binaryVersion, initialConfig string) (*AdaptCommand, func() string) {
	t.Helper()
	read, write, get := newAdaptFileStore(initialConfig)
	c := NewAdaptCommand(binaryVersion, "/fake/.a2a/config.yaml")
	c.load = fixedAdaptLoad
	c.loadCurrentIssues = func() ([]notes.Change, error) { return nil, nil }
	c.readFile = read
	c.writeFile = write
	c.loadProjectConfig = func(path string) (space.ProjectConfig, error) {
		raw, err := read(path)
		if err != nil {
			return space.ProjectConfig{}, err
		}
		var cfg space.ProjectConfig
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return space.ProjectConfig{}, err
		}
		return cfg, nil
	}
	return c, get
}

func TestAdaptDoneWritesAndNextRunListsNothing(t *testing.T) {
	t.Parallel()
	c, get := adaptDoneTestCommand(t, "0.21.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	c.detectRun = func(context.Context, string) (bool, error) { return false, nil } // every detect runs clean

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "recorded adapted_through=v0.21.0") {
		t.Fatalf("stdout = %q", out.String())
	}
	// verified=1 (DETECTED carries a detect:), total=3 (RUNNABLE, PROSE,
	// DETECTED) — never claim more verification than actually ran (this
	// epic's own C4).
	if !strings.Contains(out.String(), "1/3 verified via detect:") {
		t.Fatalf("stdout = %q, want an honest 1/3 verified count", out.String())
	}
	if !strings.Contains(get(), "adapted_through: 0.21.0") {
		t.Fatalf("config store = %q, want adapted_through updated to 0.21.0", get())
	}
	if !strings.Contains(get(), "system: sys") {
		t.Fatalf("config store = %q, want the pre-existing system: key preserved", get())
	}

	var out2, errOut2 bytes.Buffer
	code2 := c.Run(context.Background(), nil, IO{Stdout: &out2, Stderr: &errOut2})
	if code2 != 0 {
		t.Fatalf("second run exit = %d, want 0; stderr=%s", code2, errOut2.String())
	}
	if !strings.Contains(out2.String(), "nothing to adapt") {
		t.Fatalf("second run stdout = %q, want nothing to adapt", out2.String())
	}
}

func TestAdaptDoneRefusesWhenDetectStillFires(t *testing.T) {
	t.Parallel()
	c, get := adaptDoneTestCommand(t, "0.21.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	before := get()
	c.detectRun = func(_ context.Context, cmd string) (bool, error) {
		return cmd == "check-it", nil // DETECTED's own detect: still fires
	}

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut})
	if code == 0 {
		t.Fatalf("exit = 0, want a refusal; stdout=%s", out.String())
	}
	if !strings.Contains(errOut.String(), "DETECTED") {
		t.Fatalf("stderr = %q, want it to NAME the firing change (DETECTED)", errOut.String())
	}
	if !strings.Contains(errOut.String(), "still fires") {
		t.Fatalf("stderr = %q, want it to say the detect still fires", errOut.String())
	}
	if get() != before {
		t.Fatalf("config store changed on a refusal: got %q, want unchanged %q", get(), before)
	}
}

func TestAdaptDoneRefusesWhenDetectCannotRun(t *testing.T) {
	t.Parallel()
	c, get := adaptDoneTestCommand(t, "0.21.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	before := get()
	wantErr := errors.New("exec: \"sh\": executable file not found in $PATH")
	c.detectRun = func(context.Context, string) (bool, error) { return false, wantErr }

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut})
	if code == 0 {
		t.Fatalf("exit = 0, want a refusal; stdout=%s", out.String())
	}
	if strings.Contains(errOut.String(), "still fires") {
		t.Fatalf("stderr = %q, a run failure must never be reported as 'still fires'", errOut.String())
	}
	if !strings.Contains(errOut.String(), "could not be run") {
		t.Fatalf("stderr = %q, want it to say the detect could not be run", errOut.String())
	}
	if get() != before {
		t.Fatalf("config store changed on a refusal: got %q, want unchanged %q", get(), before)
	}
}

func TestAdaptDoneUnverifiedWhenNoDetectAtAll(t *testing.T) {
	t.Parallel()
	// Baseline 0.10.0, binary 0.20.0: the only pending item is RUNNABLE,
	// which carries no detect: at all.
	c, _ := adaptDoneTestCommand(t, "0.20.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "UNVERIFIED") {
		t.Fatalf("stdout = %q, want the literal word UNVERIFIED (AC-8) — only 4/44 real obligations ever carry a detect, and claiming otherwise is this epic's own C4", out.String())
	}
}

func TestAdaptDoneNothingPending(t *testing.T) {
	t.Parallel()
	c, get := adaptDoneTestCommand(t, "0.10.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "nothing pending") {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(get(), "adapted_through: 0.10.0") {
		t.Fatalf("config store = %q", get())
	}
}

func TestAdaptDoneJSONShape(t *testing.T) {
	t.Parallel()
	c, _ := adaptDoneTestCommand(t, "0.21.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	c.detectRun = func(context.Context, string) (bool, error) { return false, nil }

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done", "--json"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	var result adaptDoneJSON
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("adapt --done --json is not one JSON document: %v (raw: %s)", err, out.String())
	}
	if result.Refused || result.Recorded != "0.21.0" || result.Verified != 1 || result.Total != 3 {
		t.Fatalf("result = %+v, want Recorded=0.21.0 Verified=1 Total=3 Refused=false", result)
	}
}

func TestAdaptDoneJSONRefusalShape(t *testing.T) {
	t.Parallel()
	c, _ := adaptDoneTestCommand(t, "0.21.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	c.detectRun = func(_ context.Context, cmd string) (bool, error) { return cmd == "check-it", nil }

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done", "--json"}, IO{Stdout: &out, Stderr: &errOut})
	if code == 0 {
		t.Fatalf("exit = 0, want a refusal; stdout=%s", out.String())
	}
	var result adaptDoneJSON
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("adapt --done --json is not one JSON document: %v (raw: %s)", err, out.String())
	}
	if !result.Refused || result.ChangeID != "DETECTED" {
		t.Fatalf("result = %+v, want Refused=true ChangeID=DETECTED", result)
	}
}

// --- AC-9 / --json ---------------------------------------------------------

func TestAdaptJSONVerdictMatchesExitCode(t *testing.T) {
	t.Parallel()
	c := adaptTestCommand("0.21.0", "0.10.0", fixedAdaptLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--json"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%s", code, errOut.String())
	}
	var result adaptJSON
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("adapt --json is not one JSON document: %v (raw: %s)", err, out.String())
	}
	if !result.Verdict {
		t.Fatalf("result.Verdict = false, want true (obligations remain, exit=%d)", code)
	}
	if result.Count != 3 {
		t.Fatalf("result.Count = %d, want 3", result.Count)
	}
}

func TestAdaptJSONVerdictFalseWhenNothingPending(t *testing.T) {
	t.Parallel()
	c := adaptTestCommand("0.10.0", "0.10.0", fixedAdaptLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--json"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	var result adaptJSON
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("adapt --json is not one JSON document: %v", err)
	}
	if result.Verdict {
		t.Fatal("result.Verdict = true, want false (nothing pending)")
	}
}

// --- usage / load-failure plumbing -----------------------------------------

func TestAdaptUsageErrorOnPositionalArg(t *testing.T) {
	t.Parallel()
	c := adaptTestCommand("0.21.0", "0.10.0", fixedAdaptLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"extra"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestAdaptUsageErrorOnBadFlag(t *testing.T) {
	t.Parallel()
	c := adaptTestCommand("0.21.0", "0.10.0", fixedAdaptLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--nope"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestAdaptLoadError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("corpus load exploded")
	c := adaptTestCommand("0.21.0", "0.10.0", func() ([]notes.ReleaseNotes, error) { return nil, wantErr })
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), wantErr.Error()) {
		t.Fatalf("stderr = %q, want it to contain %q", errOut.String(), wantErr.Error())
	}
}

// --- AC-11: `a2a update` prints the adapt view -----------------------------
//
// cmd_update_test.go is NOT in this phase's allowlist, so the args-shape
// assertion there (`printPostUpdateDigest` now execs `adapt`, not `whatsnew
// --since <FromVersion>`) is reported to the lead as a one-line diff in
// this phase's deviations rather than edited here. This test instead
// covers the ONE behavioural risk that change introduces and that no
// existing test could catch: unlike `whatsnew` (always exit 0), `adapt`
// exits NON-ZERO exactly when obligations remain (AC-9) — the common case
// right after an update — and the naive "any runner error means skip" rule
// the old `whatsnew` caller relied on would have silently swallowed
// exactly the output this command exists to show.
func TestUpdateCommand_PostSwapDigest_AdaptExitCodeOneStillPrints(t *testing.T) {
	t.Parallel()
	cmd := NewUpdateCommand("0.1.0", "/fake/.a2a/config.yaml", "/fake/machine.yaml", "/fake/root")

	var calls [][]string
	cmd.whatsnewRunner = func(_ context.Context, path string, args ...string) (string, error) {
		calls = append(calls, append([]string{path}, args...))
		// release.DefaultRunner's real shape is exec.Cmd.Output(): stdout
		// is still populated even when the child exits non-zero, wrapped
		// in a *exec.ExitError. Exercise the REAL error type rather than a
		// fake one, since printPostUpdateDigest now branches on it.
		err := exec.Command("sh", "-c", "exit 1").Run()
		return "a2a adapt — 3 obligations since v0.1.0 (1 release).\n", err
	}

	var out bytes.Buffer
	res := release.ApplyResult{FromVersion: "0.1.0", ToVersion: "0.2.0"}
	cmd.printPostUpdateDigest(context.Background(), "/fake/exec/a2a", res, IO{Stdout: &out})

	if len(calls) != 1 || strings.Join(calls[0][1:], " ") != "adapt" {
		t.Fatalf("calls = %v, want exactly one call to `adapt`", calls)
	}
	if !strings.Contains(out.String(), "what changed") || !strings.Contains(out.String(), "3 obligations") {
		t.Fatalf("stdout = %q, want the digest printed despite adapt's exit code 1", out.String())
	}
}

// TestUpdateCommand_PostSwapDigest_UnexpectedExitCodeStillSkips proves the
// other half: an exit code adapt never documents (2 = usage error here) is
// NOT treated as "the child ran cleanly enough to trust its stdout" — the
// same fail-closed posture the pre-existing runner-error test already
// established for "could not run at all".
func TestUpdateCommand_PostSwapDigest_UnexpectedExitCodeStillSkips(t *testing.T) {
	t.Parallel()
	cmd := NewUpdateCommand("0.1.0", "/fake/.a2a/config.yaml", "/fake/machine.yaml", "/fake/root")
	cmd.whatsnewRunner = func(_ context.Context, path string, args ...string) (string, error) {
		err := exec.Command("sh", "-c", "exit 2").Run()
		return "should never be shown", err
	}

	var out bytes.Buffer
	res := release.ApplyResult{FromVersion: "0.1.0", ToVersion: "0.2.0"}
	cmd.printPostUpdateDigest(context.Background(), "/fake/exec/a2a", res, IO{Stdout: &out})

	if strings.Contains(out.String(), "should never be shown") {
		t.Fatalf("stdout = %q, want no digest on an undocumented exit code", out.String())
	}
}

func TestAdaptSynopsisAndName(t *testing.T) {
	t.Parallel()
	c := NewAdaptCommand("0.21.0", "/fake/.a2a/config.yaml")
	if c.Name() != "adapt" {
		t.Errorf("Name() = %q, want adapt", c.Name())
	}
	if c.Synopsis() == "" {
		t.Error("Synopsis() is empty")
	}
}

// --- refusal-ratchet migration (answers-that-hold-2026-08 P4, spec 04):
// this file's own err-to-stderr sites render a three-part Refusal
// (attempted/found/nextStep), never a bare "adapt: <err>" passthrough. -----

func TestAdaptRefusesCorpusLoadFailure(t *testing.T) {
	t.Parallel()
	c := adaptTestCommand("0.21.0", "0.10.0", func() ([]notes.ReleaseNotes, error) {
		return nil, errors.New("corpus.yaml: boom")
	})
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	text := errOut.String()
	if !strings.Contains(text, "embedded release-note corpus") {
		t.Fatalf("expected the refusal to name what was attempted, got: %s", text)
	}
	if !strings.Contains(text, "boom") {
		t.Fatalf("expected the refusal to carry the underlying error, got: %s", text)
	}
	if !strings.Contains(text, "file it against a2ahub") {
		t.Fatalf("expected the refusal to name its next step, got: %s", text)
	}
}

func TestAdaptDoneRefusesUnwritableConfig(t *testing.T) {
	t.Parallel()
	c, _ := adaptDoneTestCommand(t, "0.21.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	c.detectRun = func(context.Context, string) (bool, error) { return false, nil }
	c.writeFile = func(string, []byte, os.FileMode) error { return errors.New("permission denied") }

	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	text := errOut.String()
	if !strings.Contains(text, "record adapted_through") {
		t.Fatalf("expected the refusal to name what was attempted, got: %s", text)
	}
	if !strings.Contains(text, "permission denied") {
		t.Fatalf("expected the refusal to carry the underlying error, got: %s", text)
	}
	if !strings.Contains(text, "a2a init") {
		t.Fatalf("expected the refusal to name its next step, got: %s", text)
	}
}
