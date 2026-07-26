package artifact

import (
	"crypto/rand"
	"io"
	"strings"
	"time"
)

// ThreadID is a parsed §3.8 thread ID: `thread:<system>-<YYYYMMDD>-<rand4>`.
// It shares the rand4 alphabet and the date+rand4 shape with the §3.3
// exchange/broadcast artifact IDs (id.go) — this is the ONE home for both
// grammars, so Mint/Parse can never drift apart across the two ID kinds.
type ThreadID struct {
	Raw    string
	System string
	Date   string // YYYYMMDD
	Rand   string // 4-char lowercase base32 (base32Alphabet)
}

// MintThreadID mints a §3.8 thread ID using the current UTC date and a
// crypto-random 4-char base32 suffix (no central counter — federation-safe,
// mirroring MintExchangeID).
func MintThreadID(system string) (string, error) {
	return MintThreadIDAt(system, time.Now(), rand.Reader)
}

// MintThreadIDAt is the testable variant of MintThreadID: the caller
// supplies the timestamp (converted to UTC) and the entropy source —
// mirroring MintExchangeIDAt's injected-clock + injected-entropy discipline.
func MintThreadIDAt(system string, at time.Time, entropy io.Reader) (string, error) {
	const op = "MintThreadID"
	if system == "" {
		return "", &Error{Op: op, Err: ErrEmptyField}
	}
	if !systemShape.MatchString(system) {
		return "", &Error{Op: op, Input: system, Err: ErrMalformedID}
	}
	date := at.UTC().Format("20060102")
	suffix, err := randomBase32(entropy, 4)
	if err != nil {
		return "", &Error{Op: op, Err: err}
	}
	return "thread:" + system + "-" + date + "-" + suffix, nil
}

// threadPrefix is the literal §3.8 thread-ID prefix.
const threadPrefix = "thread:"

// ParseThreadID parses a thread ID string into its system, date and rand4
// components. It rejects malformed strings with a typed error wrapping
// ErrMalformedID; it never panics.
//
// The `<system>` token this returns is exactly what the foreign-mint rule
// (§T1 "you may only mint under your own name") compares against an
// introducing artifact's `from`: a never-before-seen thread value is legal
// only if ParseThreadID succeeds AND its System equals that `from`.
func ParseThreadID(s string) (ThreadID, error) {
	const op = "ParseThreadID"
	if !strings.HasPrefix(s, threadPrefix) {
		return ThreadID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	rest := strings.TrimPrefix(s, threadPrefix)
	if rest == "" {
		return ThreadID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	// <system> is hyphen-free (systemShape, shared with §3.3), so splitting
	// on the FIRST hyphen unambiguously separates it from the trailing
	// <YYYYMMDD>-<rand4> pair — the same separation strategy ParseID uses
	// for prefix/system/rest.
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) != 2 {
		return ThreadID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	system, dateRand := parts[0], parts[1]
	if system == "" || !systemShape.MatchString(system) {
		return ThreadID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	// exchangeRestShape is the SAME <YYYYMMDD>-<rand4> shape ParseID
	// enforces for §3.3 exchange/broadcast ids — reused rather than
	// redeclared, so the date+rand4 grammar has one regex, not two.
	m := exchangeRestShape.FindStringSubmatch(dateRand)
	if m == nil {
		return ThreadID{}, &Error{Op: op, Input: s, Err: ErrMalformedID}
	}
	return ThreadID{
		Raw:    s,
		System: system,
		Date:   m[1],
		Rand:   m[2],
	}, nil
}
