package livee2e

import (
	"context"
	"errors"
	"testing"
)

func TestAwaitLookupVisibilityRetriesAnInitiallyMissingPull(t *testing.T) {
	t.Parallel()

	errNotVisible := errors.New("pull not visible yet")
	lookups := 0
	pauses := 0
	err := awaitLookupVisibility(
		context.Background(),
		3,
		func() error {
			lookups++
			if lookups == 1 {
				return errNotVisible
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, errNotVisible) },
		func(context.Context) error {
			pauses++
			return nil
		},
	)
	if err != nil {
		t.Fatalf("awaitLookupVisibility: %v", err)
	}
	if lookups != 2 || pauses != 1 {
		t.Fatalf("lookups/pauses = %d/%d, want 2/1", lookups, pauses)
	}
}

func TestAwaitLookupVisibilityDoesNotRetryADecisiveFailure(t *testing.T) {
	t.Parallel()

	errFatal := errors.New("provider refused lookup")
	lookups := 0
	err := awaitLookupVisibility(
		context.Background(),
		3,
		func() error {
			lookups++
			return errFatal
		},
		func(error) bool { return false },
		func(context.Context) error {
			t.Fatal("pause called for a decisive failure")
			return nil
		},
	)
	if !errors.Is(err, errFatal) {
		t.Fatalf("error = %v, want errFatal", err)
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestAwaitLookupVisibilityFailsClosedAtItsBudget(t *testing.T) {
	t.Parallel()

	errNotVisible := errors.New("pull not visible yet")
	lookups := 0
	err := awaitLookupVisibility(
		context.Background(),
		2,
		func() error {
			lookups++
			return errNotVisible
		},
		func(err error) bool { return errors.Is(err, errNotVisible) },
		func(context.Context) error { return nil },
	)
	if !errors.Is(err, errNotVisible) {
		t.Fatalf("error = %v, want the visibility miss preserved", err)
	}
	if lookups != 2 {
		t.Fatalf("lookups = %d, want the exact budget 2", lookups)
	}
}
