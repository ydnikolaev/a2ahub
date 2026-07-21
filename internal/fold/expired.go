package fold

import "time"

// ExpiredOverlay computes the §3.4.7 `expired` display overlay: never a
// state, never an event, absent from the transition enum. validUntil is
// the announcement envelope's optional `valid_until`; reference is the
// caller-supplied "now" (fold has no clock — this keeps the package pure,
// no time.Now() inside it). A zero validUntil means "no expiry" (never
// expired).
//
// Spec §T1 lists the folded state as a third input alongside valid_until
// and the reference instant; this implementation omits it because the
// overlay's value never depends on it (expiry is purely a valid_until-vs-
// reference computation) — a caller that wants to suppress the overlay
// for e.g. an already-superseded announcement can do so trivially on its
// own side using the state it already has. Recorded as a deviation.
func ExpiredOverlay(validUntil, reference time.Time) bool {
	if validUntil.IsZero() {
		return false
	}
	return reference.After(validUntil)
}
