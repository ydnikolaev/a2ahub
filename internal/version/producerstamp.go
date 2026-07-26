package version

import "fmt"

// ProducerStampFloor is the release that introduced `produced_by` on event/v1 —
// the tool and version that wrote an event.
//
// It lives HERE, in the one package both internal/space (which writes the
// stamp) and internal/validate (which requires it) already depend on for
// version comparison. Neither of those imports the other, so a constant in
// either would have needed a copy in the other, and two copies of the release
// that switches a rule on is the shape that has already cost this repo an
// inert write floor and a tier that was right about something the product was
// wrong about.
const ProducerStampFloor = "0.10.0"

// ProducerStampActive reports whether a space whose manifest names spaceFloor
// as its min_binary_version is in the regime where events carry `produced_by` —
// which is the same question as "must they", because a space that stamps is a
// space that can require it.
//
// # Why the SPACE FLOOR decides this, and not the binary's own version
//
// event/v1 is `additionalProperties: false`, and a space's CI validator is
// pinned BY THE SPACE (`.github/workflows/a2a-validate.yml` names
// `a2a-validate-reusable.yml@vX.Y.Z`). So a space still pinned to a release
// predating the field would REJECT every stamped event: a freshly updated
// binary writing to a space nobody had migrated would have all of its writes
// refused over an unknown field — a worse failure than the stale-binary one the
// stamp exists to catch.
//
// Keying on the floor makes the order self-enforcing instead of a runbook step
// somebody has to get right. `a2a space update` re-pins the validator AND
// raises the floor in ONE pull request, and that pull request carries no events,
// so the OLD validator accepts it. The field therefore cannot appear in a space
// before the validator that understands it. No migration window, nothing to
// coordinate between two machines, and no release that has to ship before
// another one.
//
// An EMPTY floor means the space names none — true of every space written
// before the field existed — and is not the stamping regime.
//
// Fails CLOSED on an unreadable floor by returning the error rather than a
// bool: a floor nobody can parse is a space whose intent is unknown, and
// answering either way would decide it silently.
func ProducerStampActive(spaceFloor string) (bool, error) {
	if spaceFloor == "" {
		return false, nil
	}
	older, err := OlderThan(spaceFloor, ProducerStampFloor)
	if err != nil {
		return false, fmt.Errorf("version: producer stamp: unreadable min_binary_version %q: %w", spaceFloor, err)
	}
	return !older, nil
}
