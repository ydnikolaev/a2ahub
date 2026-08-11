package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// fakeFetchOps is the test double for cli.FetchOperations.
type fakeFetchOps struct {
	result  FetchResultOverride
	err     error
	calls   int
	lastReq cli.FetchRequest
}

// FetchResultOverride lets a test control the exact result FetchOperations
// returns without importing internal/space for a WriteResult-shaped value
// this command never touches.
type FetchResultOverride struct {
	Outcome string
}

func (f *fakeFetchOps) Fetch(_ context.Context, req cli.FetchRequest) (cli.FetchResult, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return cli.FetchResult{}, f.err
	}
	return cli.FetchResult{Ref: req.Ref, Destination: req.Destination, Outcome: f.result.Outcome}, nil
}

func TestFetchSuccessPrintsOutcomeRefAndDestination(t *testing.T) {
	t.Parallel()
	ops := &fakeFetchOps{result: FetchResultOverride{Outcome: "installed"}}
	cmd := cli.NewFetchCommand(ops)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"BL-axon-20260811-ab12", "--to", "/tmp/dest"}, io)
	if code != 0 {
		t.Fatalf("fetch: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if ops.calls != 1 {
		t.Fatalf("Fetch calls = %d, want 1", ops.calls)
	}
	if ops.lastReq.Ref != "BL-axon-20260811-ab12" || ops.lastReq.Destination != "/tmp/dest" {
		t.Fatalf("Fetch request = %+v, unexpected", ops.lastReq)
	}
	if !strings.Contains(out.String(), "installed") || !strings.Contains(out.String(), "BL-axon-20260811-ab12") || !strings.Contains(out.String(), "/tmp/dest") {
		t.Fatalf("stdout must name the outcome, ref and destination, got %q", out.String())
	}
}

func TestFetchJSONOutput(t *testing.T) {
	t.Parallel()
	ops := &fakeFetchOps{result: FetchResultOverride{Outcome: "already-present"}}
	cmd := cli.NewFetchCommand(ops)
	io, out, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"BL-axon-20260811-cd34", "--to", "/tmp/dest2", "--json"}, io)
	if code != 0 {
		t.Fatalf("fetch --json: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var result cli.FetchResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode --json output: %v (stdout=%s)", err, out.String())
	}
	if result.Ref != "BL-axon-20260811-cd34" || result.Destination != "/tmp/dest2" || result.Outcome != "already-present" {
		t.Fatalf("result = %+v, unexpected", result)
	}
}

func TestFetchRefusesNonBlobRef(t *testing.T) {
	t.Parallel()
	ops := &fakeFetchOps{}
	cmd := cli.NewFetchCommand(ops)
	io, _, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"DP-axon-20260811-ab12", "--to", "/tmp/dest"}, io)
	if code != 2 {
		t.Fatalf("want usage exit 2 for a non-BL- ref, got %d; stderr=%s", code, errOut.String())
	}
	if ops.calls != 0 {
		t.Fatalf("Fetch must not be called for a usage-invalid ref, got %d calls", ops.calls)
	}
}

func TestFetchMissingDestinationIsUsageError(t *testing.T) {
	t.Parallel()
	ops := &fakeFetchOps{}
	cmd := cli.NewFetchCommand(ops)
	io, _, _ := newIO()

	code := cmd.Run(context.Background(), []string{"BL-axon-20260811-ab12"}, io)
	if code != 2 {
		t.Fatalf("want usage exit 2 for a missing --to, got %d", code)
	}
	if ops.calls != 0 {
		t.Fatalf("Fetch must not be called on a usage refusal, got %d calls", ops.calls)
	}
}

func TestFetchRefusalSurfacesUnderlyingError(t *testing.T) {
	t.Parallel()
	ops := &fakeFetchOps{err: fmt.Errorf("simulated digest mismatch")}
	cmd := cli.NewFetchCommand(ops)
	io, _, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"BL-axon-20260811-ab12", "--to", "/tmp/dest"}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit when the space refuses, got 0")
	}
	if !strings.Contains(errOut.String(), "simulated digest mismatch") {
		t.Fatalf("refusal must carry the underlying error, got stderr=%q", errOut.String())
	}
}

func TestFetchServiceUnavailableWhenOpsNil(t *testing.T) {
	t.Parallel()
	cmd := cli.NewFetchCommand(nil)
	io, _, errOut := newIO()

	code := cmd.Run(context.Background(), []string{"BL-axon-20260811-ab12", "--to", "/tmp/dest"}, io)
	if code == 0 {
		t.Fatalf("want non-zero exit for a nil ops, got 0")
	}
	if !strings.Contains(errOut.String(), "not configured") {
		t.Fatalf("refusal must say the service is not configured, got stderr=%q", errOut.String())
	}
}
