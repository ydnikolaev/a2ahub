package livee2e

import (
	"context"
	"errors"
	"fmt"
)

// ErrSyncVisibilityTimedOut means every bounded refresh succeeded but the
// commit the caller was waiting for still was not visible from the refreshed
// base branch. A successful sync process is not sufficient evidence: the
// remote provider may report a PR merged before its base ref is observable by
// git fetch.
var ErrSyncVisibilityTimedOut = errors.New("livee2e: merged commit did not become visible after sync")

// awaitSyncVisibility refreshes a checkout until visible proves the merged
// commit is actually contained by its base branch. pause is injected so the
// retry state machine is hermetic under make check; the live adapter supplies
// the bounded provider-consistency delay.
func awaitSyncVisibility(
	ctx context.Context,
	attempts int,
	sync func() error,
	visible func() (bool, error),
	pause func(context.Context) error,
) error {
	if attempts < 1 {
		return fmt.Errorf("%w: invalid attempt budget %d", ErrSyncVisibilityTimedOut, attempts)
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := sync(); err != nil {
			return err
		}
		ok, err := visible()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if attempt == attempts {
			break
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w after %d attempts", ErrSyncVisibilityTimedOut, attempts)
}
