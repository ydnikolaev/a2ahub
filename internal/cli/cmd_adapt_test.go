package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
	"gopkg.in/yaml.v3"
)

// adaptTestCommand builds an AdaptCommand wired to a fixed corpus and a
// fixed adapted_through baseline, bypassing the real embedded corpus and
// the real filesystem (the same DI shape whatsnewTestCommand/
// newTestUpdateCommand already use in this package).
func adaptTestCommand(binaryVersion, baseline string, load func() ([]adoption.Release, error)) *AdaptCommand {
	c := NewAdaptCommand(binaryVersion, "/fake/.a2a/config.yaml")
	c.load = load
	c.loadCurrentIssues = func() ([]adoption.Change, error) { return nil, nil }
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{AdaptedThrough: baseline}, nil
	}
	return c
}

// adaptDatedCorpus is the real corpus without a note authored ahead of its tag.
// Such a note changes with every commit that adds to it, and it would surface
// for the old binary the real-corpus pins name. They judge dated notes only.
func adaptDatedCorpus() ([]adoption.Release, error) {
	all, err := adoption.Load(releasenotes.FS, notes.CorpusPattern)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(rn adoption.Release) bool { return rn.Released == adoption.Unreleased }), nil
}

// adaptFixtureCorpus is a small synthetic corpus covering every group the
// order table names: a local change with a run: (group 1), local prose
// only (group 2), and a space change with a detect: (group 3) — plus one
// scope:none change that must never surface, and one release entirely
// before the baseline this file's tests use.
func adaptFixtureCorpus() []adoption.Release {
	return []adoption.Release{
		{Version: "0.10.0", Changes: []adoption.Change{
			{ID: "OLD", Kind: "feat", Impact: "high", Subject: "before the baseline", Detail: "d",
				Action: adoption.Action{Scope: "local", Why: "w"}},
		}},
		{Version: "0.20.0", Changes: []adoption.Change{
			{ID: "RUNNABLE", Kind: "feat", Impact: "high", Subject: "a command exists", Detail: "d",
				Action: adoption.Action{Scope: "local", Why: "w", Run: []string{"a2a doctor"}}},
		}},
		{Version: "0.21.0", Changes: []adoption.Change{
			{ID: "DETECTED", Kind: "fix", Impact: "normal", Subject: "space obligation with a detect", Detail: "d",
				Action: adoption.Action{Scope: "space", Why: "w", Detect: []string{"check-it"}}},
			{ID: "PROSE", Kind: "feat", Impact: "low", Subject: "prose only", Detail: "d",
				Action: adoption.Action{Scope: "local", Why: "w"}},
			{ID: "IGNORED", Kind: "feat", Impact: "low", Subject: "never obliges anything", Detail: "d",
				Action: adoption.Action{Scope: "none", Why: "w"}},
		}},
	}
}

func fixedAdaptLoad() ([]adoption.Release, error) { return adaptFixtureCorpus(), nil }

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
	c.load = adaptDatedCorpus
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
	c.load = adaptDatedCorpus
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
	c.loadCurrentIssues = func() ([]adoption.Change, error) { return nil, nil }
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

// TestAdaptDoneRecordsATemplateDetectOnTheAgentsWord: a detect: that is a
// template (`a2a submit <id>`) fails in a shell on the redirect and read as
// "still fires", so `--done` could never record past it. A template is
// never run: it is named, and recorded on the agent's word. axon's clock
// (0.13.0) still reaches three such notes.
func TestAdaptDoneRecordsATemplateDetectOnTheAgentsWord(t *testing.T) {
	t.Parallel()
	c, get := adaptDoneTestCommand(t, "0.20.0", "system: sys\nspaces: []\nadapted_through: \"0.10.0\"\n")
	c.load = func() ([]adoption.Release, error) {
		return []adoption.Release{{Version: "0.20.0", Changes: []adoption.Change{
			{ID: "TEMPLATED", Kind: "policy", Impact: "high", Subject: "s", Detail: "d",
				Action: adoption.Action{Scope: "local", Why: "w", Detect: []string{"a2a submit <id>   # names every field"}}},
			{ID: "PROBED", Kind: "fix", Impact: "normal", Subject: "s", Detail: "d",
				Action: adoption.Action{Scope: "local", Why: "w", Detect: []string{"real-probe"}}},
		}}}, nil
	}
	c.detectRun = func(_ context.Context, cmd string) (bool, error) {
		if cmd != "real-probe" {
			t.Errorf("ran %q: a template detect must never be executed", cmd)
		}
		return false, nil
	}
	var out, errOut bytes.Buffer
	if code := c.Run(context.Background(), []string{"--done"}, IO{Stdout: &out, Stderr: &errOut}); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	for _, want := range []string{"1/2 verified", "template detect", "TEMPLATED"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout is missing %q: %s", want, out.String())
		}
	}
	if !strings.Contains(get(), "adapted_through: 0.20.0") {
		t.Fatalf("config was not stamped at the binary's version: %s", get())
	}
}

// TestAdaptDoneRefusesANeverClockedRepository (D-044): with no
// adapted_through the projection reaches back to the oldest note, whose
// obligations describe states the repository never had. --done refuses
// before running any detect:, names the onboarding procedure, and writes
// nothing; the session sets the first clock.
func TestAdaptDoneRefusesANeverClockedRepository(t *testing.T) {
	t.Parallel()
	const initial = "system: sys\nspaces: []\n"
	for _, args := range [][]string{{"--done"}, {"--done", "--json"}} {
		c, get := adaptDoneTestCommand(t, "0.21.0", initial)
		c.detectRun = func(_ context.Context, cmd string) (bool, error) {
			t.Errorf("ran %q: a never-clocked --done must run no detect", cmd)
			return false, nil
		}
		var out, errOut bytes.Buffer
		if code := c.Run(context.Background(), args, IO{Stdout: &out, Stderr: &errOut}); code != 1 {
			t.Fatalf("%v: exit = %d, want 1; stdout=%s stderr=%s", args, code, out.String(), errOut.String())
		}
		for _, want := range []string{"never recorded adapted_through", "D-044", "PROVENANCE", "0.21.0"} {
			if !strings.Contains(out.String()+errOut.String(), want) {
				t.Errorf("%v: output is missing %q: stdout=%s stderr=%s", args, want, out.String(), errOut.String())
			}
		}
		if get() != initial {
			t.Fatalf("%v: a refused --done wrote the config: %s", args, get())
		}
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

// TestAdaptDoneRecordsAuthoredAhead walks one authored-ahead note through a
// local build's whole life: it obliges, --done records it, a change added to
// the same note still obliges, and the release that dates the note clears the
// record without firing the recorded change again.
func TestAdaptDoneRecordsAuthoredAhead(t *testing.T) {
	t.Parallel()
	local := func(id string) adoption.Change {
		return adoption.Change{ID: id, Kind: "feat", Impact: "normal", Subject: id, Detail: "d",
			Action: adoption.Action{Scope: "local", Why: "w"}}
	}
	corpus := []adoption.Release{
		{Version: "0.27.1", Released: "2026-10-30"},
		{Version: "0.27.2", Released: adoption.Unreleased, Changes: []adoption.Change{local("FIRST")}},
	}
	load := func() ([]adoption.Release, error) { return corpus, nil }

	c, get := adaptDoneTestCommand(t, "0.27.1", "system: sys\nspaces: []\nadapted_through: \"0.27.1\"\n")
	c.load = load
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := c.Run(context.Background(), args, IO{Stdout: &out, Stderr: &errOut})
		return code, out.String()
	}

	if code, out := run(); code != 1 || !strings.Contains(out, "FIRST") {
		t.Fatalf("before --done: exit %d, stdout %q; want exit 1 naming FIRST", code, out)
	}
	if code, out := run("--done"); code != 0 {
		t.Fatalf("--done: exit %d, stdout %q", code, out)
	}
	if !strings.Contains(get(), "adapted_through: 0.27.1") || !strings.Contains(get(), "- FIRST") {
		t.Fatalf("config store = %q, want adapted_through kept at the stamp and FIRST recorded", get())
	}
	if code, out := run(); code != 0 || strings.Contains(out, "FIRST") {
		t.Fatalf("after --done: exit %d, stdout %q; want exit 0 with FIRST gone", code, out)
	}

	corpus[1].Changes = append(corpus[1].Changes, local("LATER"))
	if code, out := run(); code != 1 || !strings.Contains(out, "LATER") || strings.Contains(out, "FIRST") {
		t.Fatalf("after the note grew: exit %d, stdout %q; want LATER only", code, out)
	}
	if code, out := run("--done"); code != 0 {
		t.Fatalf("second --done: exit %d, stdout %q", code, out)
	}

	corpus[1].Released = "2026-11-02"
	corpus = append(corpus, adoption.Release{Version: "0.27.3", Released: adoption.Unreleased})
	c.binaryVersion = "0.27.2"
	if code, out := run(); code != 0 || strings.Contains(out, "FIRST") || strings.Contains(out, "LATER") {
		t.Fatalf("after the release: exit %d, stdout %q; want exit 0 with nothing re-fired", code, out)
	}
	if code, out := run("--done"); code != 0 {
		t.Fatalf("--done after the release: exit %d, stdout %q", code, out)
	}
	if got := get(); !strings.Contains(got, "adapted_through: 0.27.2") || strings.Contains(got, "adapted_ahead") {
		t.Fatalf("config store = %q, want adapted_through moved and adapted_ahead gone", got)
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
	c := adaptTestCommand("0.21.0", "0.10.0", func() ([]adoption.Release, error) { return nil, wantErr })
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
	c := adaptTestCommand("0.21.0", "0.10.0", func() ([]adoption.Release, error) {
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

// adaptRequireCommand is an AdaptCommand for --require: a corpus with one
// dated and one authored-ahead note, a clock that still owes a step (so plain
// adapt would exit 1), adapted_ahead naming one id, and writes that fail the
// test, because --require must never write.
func adaptRequireCommand(t *testing.T, binaryVersion string) *AdaptCommand {
	t.Helper()
	c := NewAdaptCommand(binaryVersion, "/fake/.a2a/config.yaml")
	c.load = func() ([]adoption.Release, error) {
		return []adoption.Release{
			{Version: "0.20.0", Released: "2026-01-01", Changes: []adoption.Change{
				{ID: "RN-02000-1", Kind: "feat", Impact: "high", Subject: "owed", Detail: "d",
					Action: adoption.Action{Scope: "local", Why: "w"}},
			}},
			{Version: "0.21.0", Released: adoption.Unreleased, Changes: []adoption.Change{
				{ID: "RN-02100-0", Kind: "feat", Impact: "low", Subject: "ahead", Detail: "d",
					Action: adoption.Action{Scope: "none", Why: "w"}},
			}},
		}, nil
	}
	c.loadCurrentIssues = func() ([]adoption.Change, error) { return nil, nil }
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{AdaptedThrough: "0.10.0", AdaptedAhead: []string{"RN-09900-7"}}, nil
	}
	c.readFile = func(string) ([]byte, error) {
		t.Error("--require read the config file for writing")
		return nil, errors.New("no")
	}
	c.writeFile = func(string, []byte, os.FileMode) error { t.Error("--require wrote a file"); return errors.New("no") }
	c.detectRun = func(context.Context, string) (bool, error) { t.Error("--require ran a detect"); return false, nil }
	c.execPath = func() (string, error) { return "/opt/a2a", nil }
	return c
}

// TestAdaptRequire covers every exit of `a2a adapt --require` (D6). Each
// satisfied row exits 0 although the clock still owes a step, which plain
// adapt would answer with exit 1.
func TestAdaptRequire(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		binary     string
		args       []string
		clock      string // adapted_through; empty means the fixture's 0.10.0
		code       int
		wantStderr []string
	}{
		{name: "this binary's own version", binary: "0.20.0", args: []string{"--require", "0.20.0"}, code: 0},
		{name: "an older version, with a leading v", binary: "0.20.0", args: []string{"--require", "v0.19.3"}, code: 0},
		{name: "the = form", binary: "0.20.0", args: []string{"--require=0.2.0"}, code: 0},
		{name: "a newer version is not satisfied and names the binary", binary: "0.20.0", args: []string{"--require", "99.0.0"}, code: 1,
			wantStderr: []string{"a2a 0.20.0 at /opt/a2a", "older than the required v99.0.0"}},
		{name: "a dev build cannot show it is new enough", binary: "dev", args: []string{"--require", "0.1.0"}, code: 1,
			wantStderr: []string{"a2a dev at /opt/a2a", "not a release build", "v0.1.0"}},
		{name: "a dated change in the corpus", binary: "0.20.0", args: []string{"--require", "RN-02000-1"}, code: 0},
		{name: "a change authored ahead and embedded", binary: "0.20.0", args: []string{"--require", "RN-02100-0"}, code: 0},
		{name: "adapted_ahead naming a change this binary lacks is the stale-binary case", binary: "0.20.0", args: []string{"--require", "RN-09900-7"}, code: 1,
			wantStderr: []string{"a2a 0.20.0 at /opt/a2a", "does not carry change RN-09900-7"}},
		{name: "the version form refuses a binary older than the repository's clock", binary: "0.20.0", clock: "0.25.0", args: []string{"--require", "0.19.0"}, code: 1,
			wantStderr: []string{"adapted through v0.25.0", "newer than this binary (v0.20.0 at /opt/a2a)"}},
		{name: "the change form refuses a binary older than the repository's clock", binary: "0.20.0", clock: "0.25.0", args: []string{"--require", "RN-02000-1"}, code: 1,
			wantStderr: []string{"adapted through v0.25.0", "newer than this binary (v0.20.0 at /opt/a2a)"}},
		{name: "a clock equal to the binary is satisfied", binary: "0.20.0", clock: "0.20.0", args: []string{"--require", "0.20.0"}, code: 0},
		{name: "a clock this binary cannot read fails closed", binary: "0.20.0", clock: "0.20.x", args: []string{"--require", "0.20.0"}, code: 1,
			wantStderr: []string{`adapted_through "0.20.x" is not a version`, "v0.20.0 at /opt/a2a"}},
		{name: "a dev build skips a clock it cannot compare", binary: "dev", clock: "0.20.0", args: []string{"--require", "RN-02000-1"}, code: 0},
		{name: "a dev build still answers the change form from its corpus", binary: "dev", args: []string{"--require", "RN-02000-1"}, code: 0},
		{name: "an unknown change is not satisfied and names what is missing", binary: "0.20.0", args: []string{"--require", "RN-09999-1"}, code: 1,
			wantStderr: []string{"a2a 0.20.0 at /opt/a2a", "does not carry change RN-09999-1"}},
		{name: "a two-part version is malformed", binary: "0.20.0", args: []string{"--require", "1.2"}, code: 2,
			wantStderr: []string{`"1.2" is neither a version`, "usage: a2a adapt"}},
		{name: "free text is malformed", binary: "0.20.0", args: []string{"--require", "latest"}, code: 2,
			wantStderr: []string{`"latest" is neither`}},
		{name: "a standing known-issue id is not a change id", binary: "0.20.0", args: []string{"--require", "KI-MACOS-ADHOC-SIGNING"}, code: 2},
		{name: "an empty value is malformed", binary: "0.20.0", args: []string{"--require="}, code: 2},
		{name: "a missing value is a usage error", binary: "0.20.0", args: []string{"--require"}, code: 2},
		{name: "--done is mutually exclusive", binary: "0.20.0", args: []string{"--require", "0.20.0", "--done"}, code: 2,
			wantStderr: []string{"cannot be combined with --done or --json"}},
		{name: "--json is rejected", binary: "0.20.0", args: []string{"--json", "--require", "0.20.0"}, code: 2,
			wantStderr: []string{"cannot be combined with --done or --json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := adaptRequireCommand(t, tc.binary)
			if tc.clock != "" {
				c.loadProjectConfig = func(string) (space.ProjectConfig, error) {
					return space.ProjectConfig{AdaptedThrough: tc.clock, AdaptedAhead: []string{"RN-09900-7"}}, nil
				}
			}
			var stdout, stderr bytes.Buffer
			code := c.Run(context.Background(), tc.args, IO{Stdout: &stdout, Stderr: &stderr})
			if code != tc.code {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, tc.code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("--require printed on stdout: %q", stdout.String())
			}
			if tc.code == 0 && stderr.Len() != 0 {
				t.Errorf("a satisfied --require printed on stderr: %q", stderr.String())
			}
			if tc.code == 1 && strings.Count(stderr.String(), "\n") != 1 {
				t.Errorf("an unsatisfied --require must print exactly one stderr line, got %q", stderr.String())
			}
			for _, w := range tc.wantStderr {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr %q lacks %q", stderr.String(), w)
				}
			}
		})
	}
}

// TestAdaptRequireVersionNeedsNoProjectConfig: with no .a2a/config.yaml the
// clock check is skipped silently and the version form answers from the
// binary alone, without loading the corpus.
func TestAdaptRequireVersionNeedsNoProjectConfig(t *testing.T) {
	t.Parallel()
	c := adaptRequireCommand(t, "0.20.0")
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, os.ErrNotExist }
	c.load = func() ([]adoption.Release, error) {
		t.Error("the version form loaded the corpus")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	if code := c.Run(context.Background(), []string{"--require", "0.20.0"}, IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("exit = %d, want 0; stderr %q", code, stderr.String())
	}
}

// TestAdaptRequireChangeWithoutProjectConfig: with no config the change form
// still answers from the corpus alone, and a miss is a miss, not an error.
func TestAdaptRequireChangeWithoutProjectConfig(t *testing.T) {
	t.Parallel()
	c := adaptRequireCommand(t, "0.20.0")
	c.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, os.ErrNotExist }
	for id, want := range map[string]int{"RN-02000-1": 0, "RN-09900-7": 1} {
		var stdout, stderr bytes.Buffer
		if code := c.Run(context.Background(), []string{"--require", id}, IO{Stdout: &stdout, Stderr: &stderr}); code != want {
			t.Errorf("%s: exit = %d, want %d; stderr %q", id, code, want, stderr.String())
		}
	}
}

// TestAdaptRequireCorpusLoadFailureRefuses: a corpus that will not load is a
// defect in the binary, named as such, never read as "change missing".
func TestAdaptRequireCorpusLoadFailureRefuses(t *testing.T) {
	t.Parallel()
	c := adaptRequireCommand(t, "0.20.0")
	c.load = func() ([]adoption.Release, error) { return nil, errors.New("boom") }
	var stdout, stderr bytes.Buffer
	if code := c.Run(context.Background(), []string{"--require", "RN-02000-1"}, IO{Stdout: &stdout, Stderr: &stderr}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "boom") || !strings.Contains(stderr.String(), "defect in the a2a binary") {
		t.Fatalf("stderr %q does not name the load failure", stderr.String())
	}
}
