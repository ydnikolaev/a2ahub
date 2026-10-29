// `a2a wait` (fast-loop spec 02). This file's only package-level symbols are
// WaitCommand, WaitSource, NewWaitCommand and wait*-prefixed file-private
// helpers, so it cannot collide with a sibling verb file in this package.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// WaitSource is the one capability the transport needs: block until an event
// that concerns this system lands, or the context ends (consumer-side ISP).
type WaitSource interface {
	Wait(ctx context.Context, req cache.WaitRequest) (cache.WaitResult, error)
}

// Exit codes (declared in schemas/verdict-exit-codes.yaml, row `wait`):
// 0 events printed; 124 timed out with every space observed (the coreutils
// `timeout` convention, so a shell loop reads it as "nothing happened"); 3 timed
// out and at least one space's latest observation failed, so silence proves
// nothing; 1 an error before the first observation; 2 usage.
const (
	waitExitTimeout    = 124
	waitExitUnmeasured = 3
)

const waitUsage = "usage: a2a wait [--space <id>] [--mine | --subject <id> | --thread <id>] [--timeout <duration>] [--json]"

// WaitCommand owns only transport: flags in, one fact-only line per event out.
// Selection, the cursor and the watch loop live in internal/cache.
type WaitCommand struct {
	src WaitSource
}

// NewWaitCommand constructs the wait transport. src must not be nil
// (rails anti-pattern #10).
func NewWaitCommand(src WaitSource) *WaitCommand { return &WaitCommand{src: src} }

// Name implements Command.
func (c *WaitCommand) Name() string { return "wait" }

// Synopsis implements Command.
func (c *WaitCommand) Synopsis() string {
	return "block until another system's move concerning you lands: wait [--space <id>] [--mine | --subject <id> | --thread <id>] [--timeout <duration>] [--json]"
}

type waitJSON struct {
	Events     []cache.WaitEvent `json:"events"`
	TimedOut   bool              `json:"timed_out"`
	Unmeasured []string          `json:"unmeasured"`
}

// Run implements Command.
func (c *WaitCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	spaceID := fs.String("space", "", "restrict the wait to one connected space")
	fs.Bool("mine", false, "events that concern this system (the default)")
	subject := fs.String("subject", "", "events on this artifact id")
	thread := fs.String("thread", "", "events on this thread id")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum wait duration (must be positive)")
	jsonOut := fs.Bool("json", false, "JSON output (guaranteed shape)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	selectors := 0
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "mine", "subject", "thread":
			selectors++
		}
	})
	sel := cache.WaitSelector{Kind: cache.WaitMine}
	switch {
	case *subject != "":
		sel = cache.WaitSelector{Kind: cache.WaitSubject, ID: *subject}
	case *thread != "":
		sel = cache.WaitSelector{Kind: cache.WaitThread, ID: *thread}
	}
	emptyValue := (waitFlagSet(fs, "subject") && *subject == "") || (waitFlagSet(fs, "thread") && *thread == "")
	if fs.NArg() != 0 || selectors > 1 || emptyValue || *timeout <= 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, waitUsage)
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-watch"))
		return 2
	}

	req := cache.WaitRequest{
		Selector: sel,
		// every diagnostic from the source already names itself ("wait: space ...")
		Notify: func(line string) { _, _ = fmt.Fprintln(stdio.Stderr, waitScrub(line)) },
	}
	if *spaceID != "" {
		req.Spaces = []string{*spaceID}
	}

	waitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	res, err := c.src.Wait(waitCtx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "wait: %v\n", waitScrub(err.Error()))
		return 1
	}

	if *jsonOut {
		out := waitJSON{Events: res.Events, TimedOut: res.TimedOut && len(res.Events) == 0, Unmeasured: res.Unmeasured}
		if out.Events == nil {
			out.Events = []cache.WaitEvent{}
		}
		if out.Unmeasured == nil {
			out.Unmeasured = []string{}
		}
		enc := json.NewEncoder(stdio.Stdout)
		if err := enc.Encode(out); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "wait: write output: %v\n", err)
			return 1
		}
	} else {
		for _, e := range res.Events {
			_, _ = fmt.Fprintln(stdio.Stdout, strings.Join([]string{
				waitScrub(e.Space), waitScrub(e.Event), waitScrub(e.Actor),
				waitScrub(e.Transition), waitScrub(e.Subject), waitScrub(e.Why),
			}, "\t"))
		}
	}

	if len(res.Events) > 0 {
		return 0
	}
	if len(res.Unmeasured) > 0 {
		_, _ = fmt.Fprintf(stdio.Stderr, "wait: timed out and could not observe: %s\n", waitScrub(strings.Join(res.Unmeasured, ", ")))
		return waitExitUnmeasured
	}
	if res.TimedOut {
		return waitExitTimeout
	}
	// Neither an event nor a timeout: the source ended the wait without a
	// verdict. That is a defect in the source, never a clean silence.
	_, _ = fmt.Fprintln(stdio.Stderr, "wait: ended without events or a timeout")
	return 1
}

func waitFlagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// waitScrub keeps a field on one line: a tab, newline or other control
// character in an identifier read from a file must not forge a second line or
// column (D-014).
func waitScrub(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

var _ Command = (*WaitCommand)(nil)
