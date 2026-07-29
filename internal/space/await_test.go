package space

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
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

	awaiter := NewAwaiter(fake)
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
			awaiter := NewAwaiter(fake)
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
