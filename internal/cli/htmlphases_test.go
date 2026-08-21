package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A fast render must stay quiet unless asked. A diagnostic that prints on
// every run is one people stop reading, and then the slow run's breakdown goes
// unread with the rest.
func TestHTMLPhases_QuietWhenFastAndUnasked(t *testing.T) {
	t.Parallel()
	p := newHTMLPhases()
	p.mark("assemble")
	var buf bytes.Buffer
	p.report(&buf, false)
	if buf.Len() != 0 {
		t.Fatalf("a fast render printed a breakdown nobody asked for: %q", buf.String())
	}
}

func TestHTMLPhases_PrintsWhenAsked(t *testing.T) {
	t.Parallel()
	p := newHTMLPhases()
	p.mark("assemble")
	p.mark("render")
	var buf bytes.Buffer
	p.report(&buf, true)
	got := buf.String()
	for _, want := range []string{"assemble", "render", "TOTAL"} {
		if !strings.Contains(got, want) {
			t.Fatalf("--timing output is missing %q:\n%s", want, got)
		}
	}
}

// THE POINT OF THE WHOLE FILE: a render slow enough to notice explains itself
// without anyone remembering a flag. `a2a html` was reported at about a minute
// and diagnosed by guessing, because there was nothing to read.
func TestHTMLPhases_SlowRenderExplainsItselfUnasked(t *testing.T) {
	t.Parallel()
	p := newHTMLPhases()
	p.start = p.start.Add(-2 * htmlSlowRenderThreshold)
	p.last = p.start
	p.mark("assemble")

	var buf bytes.Buffer
	p.report(&buf, false)
	got := buf.String()
	if !strings.Contains(got, "printed unasked because it was slow") {
		t.Fatalf("a slow render must explain itself without --timing:\n%s", got)
	}
	if !strings.Contains(got, "assemble") {
		t.Fatalf("the unasked breakdown must still name the phases:\n%s", got)
	}
}

// The phases are ordered by cost, because the first line is the answer to
// "what is slow" and a reader should not have to scan for it.
func TestHTMLPhases_SlowestFirst(t *testing.T) {
	t.Parallel()
	p := newHTMLPhases()
	p.spent = map[string]time.Duration{"assemble": time.Millisecond, "render": time.Second}
	p.order = []string{"assemble", "render"}

	var buf bytes.Buffer
	p.report(&buf, true)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "render") {
		t.Fatalf("the most expensive phase must come first:\n%s", buf.String())
	}
}
