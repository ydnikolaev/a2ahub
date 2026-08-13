package datatransport

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// THE SUITE'S OWN TEETH.
//
// RunConformance is a gate, and a gate nobody has watched red is a gate
// nobody has checked. Every case below hands it a driver broken in exactly
// one way and asserts it reports THAT failure — so the suite cannot rot into
// a function that returns without looking.
//
// recorderT is the conformanceT the suite is written against. Fatalf must
// stop execution the way *testing.T's does, so it panics with a sentinel the
// runner recovers — otherwise a suite that should have stopped would carry
// on into assertions the broken driver was never going to reach.
type recorderT struct{ msg string }

type fatalSentinel struct{}

func (r *recorderT) Helper() {}

func (r *recorderT) Fatalf(format string, args ...any) {
	if r.msg == "" {
		r.msg = fmt.Sprintf(format, args...)
	}
	panic(fatalSentinel{})
}

// runBroken drives the suite against d and returns the first failure it
// reported, or "" if the suite passed.
func runBroken(d Driver) (msg string) {
	rec := &recorderT{}
	defer func() {
		// The recover variable must NOT shadow `rec`: a panicking Fatalf
		// leaves the named result unassigned, so every broken driver would
		// come back as "the suite passed". Watched exactly that way before
		// this line was written — three of six broken drivers reported as
		// passing, for a reason that had nothing to do with the suite.
		if r := recover(); r != nil {
			if _, ok := r.(fatalSentinel); !ok {
				panic(r)
			}
		}
		msg = rec.msg
	}()
	runConformance(rec, d)
	return rec.msg
}

func TestConformanceSuiteRedsOnEachBrokenDriver(t *testing.T) {
	base := NewNullDriver().(*nullDriver)

	cases := []struct {
		name   string
		driver Driver
		want   string
	}{
		{"empty name", &brokenDriver{nullDriver: base, emptyName: true}, "want non-empty"},
		{"unstable name", &brokenDriver{nullDriver: base, unstableName: true}, "not stable"},
		{"accepts an unsafe locator", &brokenDriver{nullDriver: base, acceptsAnything: true}, "accepts unsafe locator"},
		{"puts without refusing an unsafe locator", &brokenDriver{nullDriver: base, putAcceptsUnsafe: true}, "want a refusal"},
		{"corrupts bytes on the way back", &brokenDriver{nullDriver: base, truncateOnGet: true}, "want"},
		{"appends instead of replacing", &brokenDriver{nullDriver: base, mergeOnPut: true}, "want 1 entries"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runBroken(tc.driver)
			if got == "" {
				t.Fatalf("the suite PASSED a driver broken as %q — it is not checking that rule", tc.name)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("suite red for %q = %q, want it to name %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestConformanceSuitePassesACorrectDriver is the other direction: the cases
// above prove the suite can fail, this proves it does not fail everything.
func TestConformanceSuitePassesACorrectDriver(t *testing.T) {
	if got := runBroken(NewNullDriver().(*nullDriver)); got != "" {
		t.Fatalf("the suite reported %q against a correct driver, want no failure", got)
	}
}

// brokenDriver wraps the null driver and breaks exactly one rule per flag.
type brokenDriver struct {
	*nullDriver
	emptyName        bool
	unstableName     bool
	acceptsAnything  bool
	putAcceptsUnsafe bool
	truncateOnGet    bool
	mergeOnPut       bool

	nameCalls int
}

func (b *brokenDriver) Name() string {
	b.nameCalls++
	switch {
	case b.emptyName:
		return ""
	case b.unstableName && b.nameCalls > 1:
		return b.nullDriver.Name() + "-drifted"
	}
	return b.nullDriver.Name()
}

func (b *brokenDriver) AcceptsLocator(locator string) bool {
	if b.acceptsAnything {
		return true
	}
	return b.nullDriver.AcceptsLocator(locator)
}

func (b *brokenDriver) Put(ctx context.Context, locator string, files map[string][]byte) error {
	if b.putAcceptsUnsafe && !b.nullDriver.AcceptsLocator(locator) {
		return nil
	}
	if b.mergeOnPut {
		existing, err := b.nullDriver.Get(ctx, locator)
		if err == nil {
			merged := make(map[string][]byte, len(existing)+len(files))
			for k, v := range existing {
				merged[k] = v
			}
			for k, v := range files {
				merged[k] = v
			}
			files = merged
		}
	}
	return b.nullDriver.Put(ctx, locator, files)
}

func (b *brokenDriver) Get(ctx context.Context, locator string) (map[string][]byte, error) {
	got, err := b.nullDriver.Get(ctx, locator)
	if err != nil || !b.truncateOnGet {
		return got, err
	}
	for k, v := range got {
		if len(v) > 0 {
			got[k] = v[:len(v)-1]
		}
	}
	return got, nil
}
