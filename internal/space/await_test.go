package space

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

func awaitRequest() AwaitRequest {
	return AwaitRequest{
		Branch: "a2a/axon/ack/XW-axon-1",
		Repo:   host.Repo{Owner: "acme", Name: "space"},
		Refresh: func(context.Context) error {
			return nil
		},
		Clear: func() error {
			return nil
		},
	}
}

func TestAwaiterWaitsForMergeThenRefreshesAndClears(t *testing.T) {
	t.Parallel()

	fake := host.NewFakeHost()
	findCalls := 0
	fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
		findCalls++
		state := "open"
		if findCalls > 1 {
			state = "merged"
		}
		return &host.PRInfo{Number: 7, URL: "https://example.invalid/pr/7", State: state}, nil
	}
	refreshed, cleared := false, false
	req := awaitRequest()
	req.Refresh = func(context.Context) error { refreshed = true; return nil }
	req.Clear = func() error { cleared = true; return nil }

	awaiter := NewAwaiter(fake, testBuild("0.0.1"))
	awaiter.SetWaitForTest(func(context.Context) error { return nil })
	result, err := awaiter.Await(context.Background(), req)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	if result.PRURL != "https://example.invalid/pr/7" || !refreshed || !cleared {
		t.Fatalf("result=%+v refreshed=%v cleared=%v", result, refreshed, cleared)
	}
}

func TestAwaiterRefusesRedClosedMissingAndCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pr   *host.PRInfo
		run  host.CheckStatusResult
		wait func(context.Context) error
		want error
	}{
		{
			name: "red check", pr: &host.PRInfo{Number: 8, State: "open"},
			run:  host.CheckStatusResult{State: "completed", Conclusion: "failure", Name: "a2a-validate / validate"},
			want: ErrAwaitCheckFailed,
		},
		{name: "closed", pr: &host.PRInfo{State: "closed"}, want: ErrAwaitClosed},
		{name: "missing", pr: nil, want: ErrAwaitPRNotFound},
		{
			name: "cancelled wait", pr: &host.PRInfo{Number: 9, State: "open"},
			wait: func(context.Context) error { return context.DeadlineExceeded },
			want: context.DeadlineExceeded,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := host.NewFakeHost()
			fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
				return tc.pr, nil
			}
			fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
				return tc.run, nil
			}
			awaiter := NewAwaiter(fake, testBuild("0.0.1"))
			if tc.wait != nil {
				awaiter.SetWaitForTest(tc.wait)
			}
			_, err := awaiter.Await(context.Background(), awaitRequest())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Await error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestAwait_LocalBuildCheckFailedHint: a failed required check on a local
// build names the pinned-validator cause; a release build keeps today's exact
// text (T6).
func TestAwait_LocalBuildCheckFailedHint(t *testing.T) {
	t.Parallel()

	const sha = "0123456789abcdef0123456789abcdef01234567"
	failed := func(t *testing.T, build version.Build) error {
		t.Helper()
		fake := host.NewFakeHost()
		fake.FindPRFunc = func(context.Context, host.FindPRRequest) (*host.PRInfo, error) {
			return &host.PRInfo{Number: 8, State: "open"}, nil
		}
		fake.CheckStatusFunc = func(context.Context, host.StatusRequest) (host.CheckStatusResult, error) {
			return host.CheckStatusResult{State: "completed", Conclusion: "failure", Name: "a2a-validate / validate"}, nil
		}
		awaiter := NewAwaiter(fake, build)
		awaiter.SetWaitForTest(func(context.Context) error { return nil })
		_, err := awaiter.Await(context.Background(), awaitRequest())
		if !errors.Is(err, ErrAwaitCheckFailed) {
			t.Fatalf("Await error = %v, want ErrAwaitCheckFailed", err)
		}
		return err
	}

	release := failed(t, version.NewBuild("0.27.0", sha, ""))
	const today = `space: required check failed: "a2a-validate / validate" completed with failure`
	if release.Error() != today {
		t.Errorf("release build error = %q, want today's exact text %q", release.Error(), today)
	}

	local := failed(t, version.NewBuild("0.27.0", sha, "local")).Error()
	for _, want := range []string{
		today, "a2a 0.27.0 (local, " + sha[:12] + ")", "pinned released validator",
		"newer than v0.27.0", "cut a release",
	} {
		if !strings.Contains(local, want) {
			t.Errorf("local build error %q lacks %q", local, want)
		}
	}
	if strings.Contains(local, sha) {
		t.Errorf("local build error carries the full sha, want 12 characters: %q", local)
	}
}
