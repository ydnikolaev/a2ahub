package livee2e

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMergeVisibilityBudgetCoversObservedGitPropagationLag(t *testing.T) {
	t.Parallel()

	const observedLag = 5 * time.Minute
	waitWindow := time.Duration(mergeVisibilityAttempts-1) * mergeVisibilityPollInterval
	if waitWindow < observedLag {
		t.Fatalf("merge visibility window = %s, want at least observed %s", waitWindow, observedLag)
	}
	if waitWindow != mergeVisibilityCeiling {
		t.Fatalf("merge visibility window = %s, want the full declared ceiling %s", waitWindow, mergeVisibilityCeiling)
	}
}

func TestAwaitSyncVisibilityDoesNotTrustOneSuccessfulRefresh(t *testing.T) {
	t.Parallel()

	syncs := 0
	checks := 0
	pauses := 0
	err := awaitSyncVisibility(
		context.Background(),
		3,
		func() error {
			syncs++
			return nil
		},
		func() (bool, error) {
			checks++
			return checks == 2, nil
		},
		func(context.Context) error {
			pauses++
			return nil
		},
	)
	if err != nil {
		t.Fatalf("awaitSyncVisibility: %v", err)
	}
	if syncs != 2 || checks != 2 || pauses != 1 {
		t.Fatalf("syncs/checks/pauses = %d/%d/%d, want 2/2/1", syncs, checks, pauses)
	}
}

func TestAwaitSyncVisibilityFailsClosedWhenCommitNeverAppears(t *testing.T) {
	t.Parallel()

	err := awaitSyncVisibility(
		context.Background(),
		2,
		func() error { return nil },
		func() (bool, error) { return false, nil },
		func(context.Context) error { return nil },
	)
	if !errors.Is(err, ErrSyncVisibilityTimedOut) {
		t.Fatalf("error = %v, want ErrSyncVisibilityTimedOut", err)
	}
}
