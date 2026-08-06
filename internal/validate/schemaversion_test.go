package validate

import (
	"strings"
	"testing"
)

// TestUnreadableOrUnacceptedSchemaMessage pins the two distinct causes to two
// distinct remedies. They shared one sentence about a "one-cycle overlap
// window" that named neither the accepted value nor what to do, and that had
// zero hits in the shipped documentation.
func TestUnreadableOrUnacceptedSchemaMessage(t *testing.T) {
	t.Parallel()

	t.Run("a malformed value is a typo to fix, not a version problem", func(t *testing.T) {
		t.Parallel()
		// "space/1" — the version suffix itself does not parse. ("spce/v1"
		// would NOT reach here: ParseVersion reads only the trailing v<N> and
		// ignores the family prefix, so a misspelled family is caught later by
		// the schema's own `const`, not by POL-005.)
		got := unreadableOrUnacceptedSchemaMessage("space.yaml", "space", "space/1")
		for _, want := range []string{"space/1", "space/v1", "Set it to"} {
			if !strings.Contains(got, want) {
				t.Fatalf("message does not say %q: %s", want, got)
			}
		}
		if strings.Contains(got, "a2a update") {
			t.Fatalf("a malformed value must not be blamed on a stale binary: %s", got)
		}
	})

	t.Run("an unaccepted version names the upgrade", func(t *testing.T) {
		t.Parallel()
		got := unreadableOrUnacceptedSchemaMessage("space.yaml", "space", "space/v9")
		for _, want := range []string{"space/v9", "space/v1", "a2a update"} {
			if !strings.Contains(got, want) {
				t.Fatalf("message does not say %q: %s", want, got)
			}
		}
	})

	t.Run("every family renders an accepted value", func(t *testing.T) {
		t.Parallel()
		for _, family := range []string{"envelope", "event", "space", "consumes"} {
			got := unreadableOrUnacceptedSchemaMessage("this artifact", family, "bogus")
			if !strings.Contains(got, family+"/v") {
				t.Fatalf("family %q renders no accepted value: %s", family, got)
			}
		}
	})

	// The refusal must never describe a window the checks do not enforce, so
	// the rendered value is read from the same source the predicates use.
	t.Run("the named value is one the checks actually accept", func(t *testing.T) {
		t.Parallel()
		got := unreadableOrUnacceptedSchemaMessage("this artifact", "envelope", "bogus")
		if !strings.Contains(got, "envelope/v2") {
			t.Fatalf("envelope's newest accepted version is not named: %s", got)
		}
	})
}
