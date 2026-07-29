package livee2e

import (
	"context"
	"fmt"
)

// awaitLookupVisibility retries a provider lookup only while retryable says
// the result is an eventual-visibility miss. A newly-created GitHub pull
// request can be returned by POST /pulls before it appears in LIST /pulls;
// treating the first empty list as decisive turns a healthy write into a
// false live failure.
//
// lookup stores any successful value in its caller's closure. Keeping this
// helper value-agnostic leaves the retry rule available to the untagged,
// hermetic suite even though PullState itself belongs to the livee2e build.
func awaitLookupVisibility(
	ctx context.Context,
	attempts int,
	lookup func() error,
	retryable func(error) bool,
	pause func(context.Context) error,
) error {
	if attempts < 1 {
		return fmt.Errorf("livee2e: invalid lookup visibility attempt budget %d", attempts)
	}
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := lookup()
		if err == nil {
			return nil
		}
		if !retryable(err) {
			return err
		}
		last = err
		if attempt == attempts {
			break
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w after %d visibility attempts", last, attempts)
}
