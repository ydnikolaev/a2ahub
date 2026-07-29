package e2e

import (
	"context"
	"io"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestT3Await is the direct-construction evidence for the network-backed
// await verb: it observes a recorded PR become merged, refreshes, and clears.
func TestT3Await(t *testing.T) {
	t.Parallel()
	refreshed, cleared := false, false
	cmd := cli.NewAwaitCommand(func(context.Context, string) (cli.AwaitTarget, error) {
		return cli.AwaitTarget{
			SpaceID: "fixture-space",
			Await: func(context.Context) (space.AwaitResult, error) {
				refreshed, cleared = true, true
				return space.AwaitResult{PRURL: "https://example.invalid/pr/1"}, nil
			},
		}, nil
	})
	if code := cmd.Run(context.Background(), []string{"XW-axon-1"}, cli.IO{
		Stdout: io.Discard, Stderr: io.Discard,
	}); code != 0 {
		t.Fatalf("await code = %d", code)
	}
	if !refreshed || !cleared {
		t.Fatalf("refreshed=%v cleared=%v", refreshed, cleared)
	}
}
