package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// AwaitTarget is the one resolved pending write and its owning space.
type AwaitTarget struct {
	SpaceID string
	Await   func(context.Context) (space.AwaitResult, error)
}

// AwaitResolver resolves an artifact id to exactly one recorded pending write.
type AwaitResolver func(context.Context, string) (AwaitTarget, error)

// AwaitCommand owns only transport: argument parsing and result rendering.
// The polling state machine lives in space.Awaiter.
type AwaitCommand struct {
	resolve AwaitResolver
}

// NewAwaitCommand constructs the await transport.
func NewAwaitCommand(resolve AwaitResolver) *AwaitCommand {
	return &AwaitCommand{resolve: resolve}
}

// Name implements Command.
func (c *AwaitCommand) Name() string { return "await" }

// Synopsis implements Command.
func (c *AwaitCommand) Synopsis() string {
	return "wait for a recorded pending write to merge: await <artifact-id> [--timeout <duration>]"
}

// Run implements Command.
func (c *AwaitCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("await", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum wait duration")
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || *timeout <= 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a await <artifact-id> [--timeout <duration>]")
		return 2
	}

	waitCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	target, err := c.resolve(waitCtx, positional[0])
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "await: %v\n", err)
		return 1
	}
	if target.Await == nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "await: resolved pending write has no await capability")
		return 1
	}
	result, err := target.Await(waitCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			_, _ = fmt.Fprintf(stdio.Stderr, "await: timed out after %s\n", timeout.String())
		} else {
			_, _ = fmt.Fprintf(stdio.Stderr, "await: %v\n", err)
		}
		return 1
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "await: merged PR %s; refreshed space %s\n", result.PRURL, target.SpaceID)
	return 0
}

var _ Command = (*AwaitCommand)(nil)
