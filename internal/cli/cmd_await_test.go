package cli_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestAwaitCommandRendersSuccessAndTimeout(t *testing.T) {
	t.Parallel()

	success := cli.NewAwaitCommand(func(context.Context, string) (cli.AwaitTarget, error) {
		return cli.AwaitTarget{
			SpaceID: "space-1",
			Await: func(context.Context) (space.AwaitResult, error) {
				return space.AwaitResult{PRURL: "https://example.invalid/pr/7"}, nil
			},
		}, nil
	})
	io, out, errOut := newIO()
	if code := success.Run(context.Background(), []string{"XW-axon-1", "--timeout", "1s"}, io); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "merged PR https://example.invalid/pr/7") {
		t.Fatalf("stdout=%q", out.String())
	}

	timeout := cli.NewAwaitCommand(func(context.Context, string) (cli.AwaitTarget, error) {
		return cli.AwaitTarget{
			Await: func(context.Context) (space.AwaitResult, error) {
				return space.AwaitResult{}, context.DeadlineExceeded
			},
		}, nil
	})
	timeoutIO, _, timeoutErr := newIO()
	if code := timeout.Run(context.Background(), []string{"XW-axon-1", "--timeout", "1s"}, timeoutIO); code != 1 {
		t.Fatalf("timeout code=%d", code)
	}
	if !strings.Contains(timeoutErr.String(), "timed out after 1s") {
		t.Fatalf("stderr=%q", timeoutErr.String())
	}
}

func TestAwaitCommandMissingMarkerAndUsage(t *testing.T) {
	t.Parallel()

	cmd := cli.NewAwaitCommand(func(context.Context, string) (cli.AwaitTarget, error) {
		return cli.AwaitTarget{}, errors.New("no pending write")
	})
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"XW-axon-1"}, io); code != 1 {
		t.Fatalf("missing code=%d", code)
	}
	if !strings.Contains(errOut.String(), "no pending write") {
		t.Fatalf("stderr=%q", errOut.String())
	}
	badIO, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"XW-axon-1", "--timeout", "0s"}, badIO); code != 2 {
		t.Fatalf("zero timeout code=%d, want 2", code)
	}
}
