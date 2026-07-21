package artifact

import (
	"crypto/rand"
	"io"
	"time"

	"github.com/oklog/ulid/v2"
)

// MintULID mints a lifecycle-event ULID (§5.2.2, ADR-002) using the
// current time and a crypto-random entropy source. A ULID is an
// intra-commit tiebreak only — never one of the two §3.3 artifact-ID
// classes (standing, exchange/broadcast).
func MintULID() (ulid.ULID, error) {
	return MintULIDAt(time.Now(), rand.Reader)
}

// MintULIDAt is the testable variant of MintULID: the caller supplies the
// timestamp and entropy source.
func MintULIDAt(t time.Time, entropy io.Reader) (ulid.ULID, error) {
	const op = "MintULID"
	id, err := ulid.New(ulid.Timestamp(t), entropy)
	if err != nil {
		return ulid.ULID{}, &Error{Op: op, Err: err}
	}
	return id, nil
}

// ParseULID parses and validates a ULID string via
// github.com/oklog/ulid/v2; malformed input is a typed error wrapping
// ErrMalformedULID, never a panic.
func ParseULID(s string) (ulid.ULID, error) {
	const op = "ParseULID"
	id, err := ulid.ParseStrict(s)
	if err != nil {
		return ulid.ULID{}, &Error{Op: op, Input: s, Err: ErrMalformedULID}
	}
	return id, nil
}
