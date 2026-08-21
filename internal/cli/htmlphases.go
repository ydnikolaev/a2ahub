package cli

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// htmlSlowRenderThreshold is when a render stops being something a person
// waits through and becomes something they complain about. Below it the
// breakdown is noise; at or above it, the breakdown is the first thing anyone
// would ask for, so it is printed WITHOUT being asked.
const htmlSlowRenderThreshold = 3 * time.Second

// htmlPhases records how long each stage of a dashboard render took.
//
// It exists because the alternative is guessing. `a2a html` was reported as
// taking about a minute; the natural suspect was the release-notes corpus
// carried on the versions page, and measuring it took that suspect off the
// table in one command — 8.7 ms to load all 46 releases, inside an 80 ms
// end-to-end demo render. Without a breakdown the next report would have been
// diagnosed by the next plausible story.
type htmlPhases struct {
	start time.Time
	last  time.Time
	order []string
	spent map[string]time.Duration
}

func newHTMLPhases() *htmlPhases {
	now := time.Now()
	return &htmlPhases{start: now, last: now, spent: map[string]time.Duration{}}
}

// mark closes the phase named by label. Phases are measured as the gap since
// the previous mark, so a caller adds one line per stage and nothing has to
// stay in step with anything else.
func (p *htmlPhases) mark(label string) {
	now := time.Now()
	if _, seen := p.spent[label]; !seen {
		p.order = append(p.order, label)
	}
	p.spent[label] += now.Sub(p.last)
	p.last = now
}

// report writes the breakdown to w when asked, or unasked when the render was
// slow enough to notice. It goes to STDERR: this is a diagnostic about the
// run, not part of the artifact, and `--json`'s stdout must stay parseable.
func (p *htmlPhases) report(w io.Writer, requested bool) {
	total := time.Since(p.start)
	if !requested && total < htmlSlowRenderThreshold {
		return
	}
	if !requested {
		_, _ = fmt.Fprintf(w, "a2a html: this render took %s — the breakdown below is printed unasked because it was slow.\n", total.Round(time.Millisecond))
	}
	labels := append([]string(nil), p.order...)
	sort.SliceStable(labels, func(i, j int) bool { return p.spent[labels[i]] > p.spent[labels[j]] })
	for _, label := range labels {
		_, _ = fmt.Fprintf(w, "  %-10s %8s\n", label, p.spent[label].Round(time.Millisecond))
	}
	_, _ = fmt.Fprintf(w, "  %-10s %8s\n", "TOTAL", total.Round(time.Millisecond))
}
