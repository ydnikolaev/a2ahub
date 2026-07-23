package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/notes"
)

// whatsnewFixtureCorpus is a fixed, already version-ascending 3-entry
// corpus (mirrors notes.Load's own postcondition) covering every impact
// (high/normal/low) and every Action.Scope (space/local/none) the render
// path branches on.
func whatsnewFixtureCorpus() []notes.ReleaseNotes {
	return []notes.ReleaseNotes{
		{
			Schema: "release-notes/v1", Version: "0.2.0", Released: "2026-01-01", Headline: "H2",
			Changes: []notes.Change{
				{ID: "A", Kind: "feat", Impact: "low", Subject: "s2-low", Detail: "detail two",
					Action: notes.Action{Scope: "none", Why: "w2"}},
			},
		},
		{
			Schema: "release-notes/v1", Version: "0.3.0", Released: "2026-02-01", Headline: "H3",
			Changes: []notes.Change{
				{ID: "B", Kind: "fix", Impact: "high", Subject: "s3-high", Detail: "detail three high",
					Action: notes.Action{Scope: "space", Why: "w3", Detect: []string{"a2a outbox --json"}, Run: []string{"a2a lifecycle ack X"}}},
				{ID: "C", Kind: "feat", Impact: "normal", Subject: "s3-normal", Detail: "detail three normal",
					Action: notes.Action{Scope: "none", Why: "w3n"}},
			},
		},
		{
			Schema: "release-notes/v1", Version: "0.4.0", Released: "2026-03-01", Headline: "H4",
			Changes: []notes.Change{
				{ID: "D", Kind: "feat", Impact: "normal", Subject: "s4-local", Detail: "detail four local",
					Action: notes.Action{Scope: "local", Why: "w4"}},
			},
		},
	}
}

func whatsnewTestCommand(binaryVersion string, load func() ([]notes.ReleaseNotes, error)) *WhatsnewCommand {
	c := NewWhatsnewCommand(binaryVersion)
	c.load = load
	return c
}

func fixedWhatsnewLoad() ([]notes.ReleaseNotes, error) {
	return whatsnewFixtureCorpus(), nil
}

func TestWhatsnewSinceRangeBoundedByBinaryVersion(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.4.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--since", "0.2.0"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	text := out.String()
	// newest-first: v0.4.0 must appear before v0.3.0.
	i4 := strings.Index(text, "v0.4.0")
	i3 := strings.Index(text, "v0.3.0")
	if i4 < 0 || i3 < 0 || i4 > i3 {
		t.Fatalf("expected v0.4.0 before v0.3.0 (newest-first), got:\n%s", text)
	}
	if strings.Contains(text, "v0.2.0") {
		t.Fatalf("since=0.2.0 must exclude v0.2.0 itself, got:\n%s", text)
	}
}

func TestWhatsnewNoSinceExactMatch(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.3.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "v0.3.0") || !strings.Contains(text, "H3") {
		t.Fatalf("expected the exact 0.3.0 entry, got:\n%s", text)
	}
	if strings.Contains(text, "v0.2.0") || strings.Contains(text, "v0.4.0") {
		t.Fatalf("expected ONLY the exact match, got:\n%s", text)
	}
	// impact ordering within one release: high before normal.
	iHigh := strings.Index(text, "s3-high")
	iNormal := strings.Index(text, "s3-normal")
	if iHigh < 0 || iNormal < 0 || iHigh > iNormal {
		t.Fatalf("expected high-impact change before normal-impact change, got:\n%s", text)
	}
}

func TestWhatsnewNoSinceDevBuildIsEmpty(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("dev", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no release notes for version dev") {
		t.Fatalf("expected the empty-corpus message, got: %q", out.String())
	}
}

func TestWhatsnewSinceWorksOnDevBuildUnbounded(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("dev", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--since", "0.2.0"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	text := out.String()
	if !strings.Contains(text, "v0.3.0") || !strings.Contains(text, "v0.4.0") {
		t.Fatalf("expected --since to be unbounded above on an unparseable (dev) binary version, got:\n%s", text)
	}
}

func TestWhatsnewJSONShape(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.4.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--since", "0.2.0", "--json"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	var decoded []notes.ReleaseNotes
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("expected valid JSON: %v\noutput: %s", err, out.String())
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 entries, got %d: %#v", len(decoded), decoded)
	}
}

func TestWhatsnewJSONEmptyIsArrayNotNull(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("dev", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--json"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("expected a bare [] for the empty case, got: %q", out.String())
	}
}

func TestWhatsnewUsageErrorOnPositionalArg(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.4.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"unexpected"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage: a2a whatsnew") {
		t.Fatalf("expected a usage message, got: %q", errOut.String())
	}
}

func TestWhatsnewUsageErrorOnBadFlag(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.4.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"--nonsense"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestWhatsnewLoadError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("corpus load boom")
	c := whatsnewTestCommand("0.4.0", func() ([]notes.ReleaseNotes, error) { return nil, wantErr })
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "whatsnew:") || !strings.Contains(errOut.String(), "corpus load boom") {
		t.Fatalf("expected an actionable error message, got: %q", errOut.String())
	}
}

func TestWhatsnewActionScopeRendering(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.3.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	text := out.String()
	// scope: space (s3-high) renders why/detect/run.
	if !strings.Contains(text, "→ why: w3") {
		t.Fatalf("expected the scope:space directive's why line, got:\n%s", text)
	}
	if !strings.Contains(text, "detect: a2a outbox --json") {
		t.Fatalf("expected the scope:space directive's detect line, got:\n%s", text)
	}
	if !strings.Contains(text, "run: a2a lifecycle ack X") {
		t.Fatalf("expected the scope:space directive's run line, got:\n%s", text)
	}
	// scope: none (s3-normal) must NOT render a why line for w3n.
	if strings.Contains(text, "why: w3n") {
		t.Fatalf("scope:none must not render an action directive, got:\n%s", text)
	}
}

func TestWhatsnewLocalScopeAlsoRenders(t *testing.T) {
	t.Parallel()
	c := whatsnewTestCommand("0.4.0", fixedWhatsnewLoad)
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "→ why: w4") {
		t.Fatalf("expected scope:local to also render its why line, got:\n%s", out.String())
	}
}

func TestWhatsnewWrapDetailWrapsLongLines(t *testing.T) {
	t.Parallel()
	long := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty"
	lines := whatsnewWrapDetail(long, "    ", 30)
	if len(lines) < 2 {
		t.Fatalf("expected the long detail to wrap across multiple lines, got %d: %v", len(lines), lines)
	}
	for _, l := range lines {
		if len(l) > 30+len("nineteen") {
			t.Errorf("line exceeds a reasonable bound: %q", l)
		}
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("expected every wrapped line to carry the indent, got %q", l)
		}
	}
}

func TestWhatsnewWrapDetailEmpty(t *testing.T) {
	t.Parallel()
	if lines := whatsnewWrapDetail("", "  ", 76); lines != nil {
		t.Fatalf("expected nil for empty detail, got %v", lines)
	}
}

func TestWhatsnewImpactOrderUnknownImpactSortsLast(t *testing.T) {
	t.Parallel()
	if got := whatsnewImpactOrder("weird"); got != len(whatsnewImpactRank) {
		t.Fatalf("whatsnewImpactOrder(weird) = %d, want %d (sorts after every known impact)", got, len(whatsnewImpactRank))
	}
	if whatsnewImpactOrder("low") >= whatsnewImpactOrder("weird") {
		t.Fatalf("expected an unrecognized impact to sort after low")
	}
}

func TestWhatsnewSynopsisAndName(t *testing.T) {
	t.Parallel()
	c := NewWhatsnewCommand("0.4.0")
	if c.Name() != "whatsnew" {
		t.Fatalf("Name() = %q", c.Name())
	}
	if c.Synopsis() == "" {
		t.Fatal("expected a non-empty synopsis")
	}
}
