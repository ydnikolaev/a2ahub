package livee2e

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// stageRecorder prints the logic tier's wall-clock timeline, one line per
// edge: `logic-stage: <stage> <start|end> +<seconds since start>`.
//
// The logic is untagged so it is unit-tested by a plain `make test`; the
// tagged TestLogicMatrix only calls it. verify.sh's logic_stage_rows turns the
// lines into stages.jsonl rows. A parallel subtest's reported duration excludes
// its pause, so stages are stamped from the wall clock here and never from
// test durations.
//
// Methods on a nil *stageRecorder are no-ops: the live tier shares
// driveFamilies with the logic tier and must print nothing.
type stageRecorder struct {
	start time.Time
	now   func() time.Time

	mu sync.Mutex
	w  io.Writer
}

// newStageRecorder builds a recorder measuring from start, reading time from
// now and writing whole lines to w.
func newStageRecorder(start time.Time, now func() time.Time, w io.Writer) *stageRecorder {
	return &stageRecorder{start: start, now: now, w: w}
}

// Mark writes one stage line. edge is "start" or "end".
func (r *stageRecorder) Mark(stage, edge string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	offset := r.now().Sub(r.start).Seconds()
	// reason: a timeline line is best-effort telemetry; a failed write must
	// never fail the tier it is measuring.
	_, _ = fmt.Fprintf(r.w, "logic-stage: %s %s +%.1f\n", stage, edge, offset)
}

// spawnCounter counts short-lived `a2a` spawns per verb (the first argument)
// and their wall cost. The zero value is ready to use.
type spawnCounter struct {
	mu    sync.Mutex
	verbs map[string]*spawnStat
}

type spawnStat struct {
	n     int
	total time.Duration
}

// Add records one spawn of args that took d.
func (c *spawnCounter) Add(args []string, d time.Duration) {
	verb := "<none>"
	if len(args) > 0 {
		verb = args[0]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.verbs == nil {
		c.verbs = map[string]*spawnStat{}
	}
	s := c.verbs[verb]
	if s == nil {
		s = &spawnStat{}
		c.verbs[verb] = s
	}
	s.n++
	s.total += d
}

// Reset empties the counter.
func (c *spawnCounter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verbs = nil
}

// Lines renders one `logic-spawns: <verb> n=<count> total=<s> mean=<ms>` line
// per verb, sorted by verb.
func (c *spawnCounter) Lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	verbs := make([]string, 0, len(c.verbs))
	for v := range c.verbs {
		verbs = append(verbs, v)
	}
	sort.Strings(verbs)
	out := make([]string, 0, len(verbs))
	for _, v := range verbs {
		s := c.verbs[v]
		mean := float64(s.total.Milliseconds()) / float64(s.n)
		out = append(out, fmt.Sprintf("logic-spawns: %s n=%d total=%.1f mean=%.0f", v, s.n, s.total.Seconds(), mean))
	}
	return out
}
