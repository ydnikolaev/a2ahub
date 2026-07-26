package artifact

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

// threadPattern is a hand-transcribed copy of the regex spec P46 §T2 says a
// later wave will put in base.schema.json's `thread` field.
//
// Be precise about what this buys: it proves the SHIPPED CODE matches the
// SPEC'S PROSE. It does NOT link this package to the schema — today
// base.schema.json:54 carries no pattern at all, and when the tightening
// wave hand-types one, a typo in its character class would leave both
// literals green and silently disagreeing. The mechanical link (a test that
// reads the pattern OUT of the schema corpus rather than restating it) is
// that wave's job and is recorded in the phase plan; until it exists, this
// is one honest half of the guard, not the whole one.
var threadPattern = regexp.MustCompile(`^thread:[a-z0-9]+-[0-9]{8}-[0-9abcdefghjkmnpqrstvwxyz]{4}$`)

func TestMintThreadIDAt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC)
	entropy := deterministicEntropy{seed: []byte{0, 0, 0, 0}} // -> "0000" in the alphabet

	got, err := MintThreadIDAt("axon", at, entropy)
	if err != nil {
		t.Fatalf("MintThreadIDAt unexpected error: %v", err)
	}
	want := "thread:axon-20260721-0000"
	if got != want {
		t.Fatalf("MintThreadIDAt = %q, want %q", got, want)
	}
	if !threadPattern.MatchString(got) {
		t.Fatalf("MintThreadIDAt = %q does not match the schema pattern %s", got, threadPattern)
	}

	parsed, err := ParseThreadID(got)
	if err != nil {
		t.Fatalf("ParseThreadID(%q) unexpected error: %v", got, err)
	}
	if parsed.Raw != got || parsed.System != "axon" || parsed.Date != "20260721" || parsed.Rand != "0000" {
		t.Fatalf("ParseThreadID(%q) = %+v, want raw=%q system=axon date=20260721 rand=0000", got, parsed, got)
	}
}

// TestMintThreadIDAt_isDeterministic asserts the same clock+entropy pair
// always mints the same value — the property MintExchangeIDAt's tests rely
// on and thread minting must share, since the CLI/e2e tiers depend on
// reproducible fixtures.
func TestMintThreadIDAt_isDeterministic(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	seed := []byte{9, 8, 7, 6}

	got1, err := MintThreadIDAt("seomatrix", at, deterministicEntropy{seed: seed})
	if err != nil {
		t.Fatalf("MintThreadIDAt unexpected error: %v", err)
	}
	got2, err := MintThreadIDAt("seomatrix", at, deterministicEntropy{seed: seed})
	if err != nil {
		t.Fatalf("MintThreadIDAt unexpected error: %v", err)
	}
	if got1 != got2 {
		t.Fatalf("MintThreadIDAt is not deterministic: %q != %q", got1, got2)
	}
}

// TestMintThreadIDAt_dateIsUTC asserts the date component is formatted in
// UTC, not the input timestamp's own zone — the same guarantee
// MintExchangeIDAt gives.
func TestMintThreadIDAt_dateIsUTC(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("test", -7*3600) // UTC-7
	// 2026-07-21 23:30 -07:00 == 2026-07-22 06:30 UTC.
	at := time.Date(2026, 7, 21, 23, 30, 0, 0, loc)
	entropy := deterministicEntropy{seed: []byte{0, 0, 0, 0}}

	got, err := MintThreadIDAt("axon", at, entropy)
	if err != nil {
		t.Fatalf("MintThreadIDAt unexpected error: %v", err)
	}
	want := "thread:axon-20260722-0000"
	if got != want {
		t.Fatalf("MintThreadIDAt = %q, want %q (date must be UTC)", got, want)
	}
}

func TestMintThreadIDAt_rejectsBadInputs(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	entropy := deterministicEntropy{seed: []byte{1, 2, 3, 4}}

	cases := []struct {
		name    string
		system  string
		wantErr error
	}{
		{name: "empty system", system: "", wantErr: ErrEmptyField},
		{name: "hyphenated system", system: "my-system", wantErr: ErrMalformedID},
		{name: "uppercase system", system: "Axon", wantErr: ErrMalformedID},
		{name: "system with space", system: "ax on", wantErr: ErrMalformedID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := MintThreadIDAt(tc.system, at, entropy)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("MintThreadIDAt(%q) error = %v, want errors.Is %v", tc.system, err, tc.wantErr)
			}
		})
	}
}

// TestParseThreadID_liveFixture pins the two real fixture values in the
// repo — schemas/envelope/v1/fixtures/valid/XR-axon-country-vocabulary.md:23
// and its paired response — so a change to this parser cannot silently stop
// accepting live data.
func TestParseThreadID_liveFixture(t *testing.T) {
	t.Parallel()

	const fixture = "thread:axon-20260729-c7q2"
	got, err := ParseThreadID(fixture)
	if err != nil {
		t.Fatalf("ParseThreadID(%q) unexpected error: %v", fixture, err)
	}
	want := ThreadID{Raw: fixture, System: "axon", Date: "20260729", Rand: "c7q2"}
	if got != want {
		t.Fatalf("ParseThreadID(%q) = %+v, want %+v", fixture, got, want)
	}
	if !threadPattern.MatchString(fixture) {
		t.Fatalf("literal fixture %q does not match the schema pattern %s", fixture, threadPattern)
	}
}

func TestParseThreadID_rejectsMalformed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty string", in: ""},
		{name: "no thread prefix", in: "axon-20260721-c7q2"},
		{name: "wrong prefix casing", in: "Thread:axon-20260721-c7q2"},
		{name: "bare artifact id, not a thread id", in: "XQ-axon-20260721-c7q2"},
		{name: "empty system", in: "thread:-20260721-c7q2"},
		{name: "system with hyphen", in: "thread:my-system-20260721-c7q2"},
		{name: "uppercase system", in: "thread:Axon-20260721-c7q2"},
		{name: "date too short", in: "thread:axon-2026721-c7q2"},
		{name: "date too long", in: "thread:axon-202607211-c7q2"},
		{name: "date non-numeric", in: "thread:axon-2026072x-c7q2"},
		{name: "rand4 too short", in: "thread:axon-20260721-c7q"},
		{name: "rand4 too long", in: "thread:axon-20260721-c7q22"},
		{name: "rand4 outside alphabet (uppercase)", in: "thread:axon-20260721-C7Q2"},
		{name: "rand4 outside alphabet (excluded letter i)", in: "thread:axon-20260721-i7q2"},
		{name: "rand4 outside alphabet (excluded letter l)", in: "thread:axon-20260721-l7q2"},
		{name: "rand4 outside alphabet (excluded letter o)", in: "thread:axon-20260721-o7q2"},
		{name: "rand4 outside alphabet (excluded letter u)", in: "thread:axon-20260721-u7q2"},
		{name: "no date/rand at all", in: "thread:axon"},
		{name: "missing rand4", in: "thread:axon-20260721"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseThreadID(tc.in)
			if !errors.Is(err, ErrMalformedID) {
				t.Fatalf("ParseThreadID(%q) error = %v, want errors.Is ErrMalformedID", tc.in, err)
			}
		})
	}
}

// TestMintThreadIDAt_roundTripsAndMatchesSchemaPattern asserts the
// mint->parse round trip AND the literal schema pattern agreement across a
// spread of systems, so the two never drift apart by hope.
func TestMintThreadIDAt_roundTripsAndMatchesSchemaPattern(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	systems := []string{"axon", "seomatrix", "getvisa", "a1"}

	for _, system := range systems {
		t.Run(system, func(t *testing.T) {
			t.Parallel()
			entropy := deterministicEntropy{seed: []byte{7, 2, 8, 3}}
			got, err := MintThreadIDAt(system, at, entropy)
			if err != nil {
				t.Fatalf("MintThreadIDAt(%q) unexpected error: %v", system, err)
			}
			if !threadPattern.MatchString(got) {
				t.Fatalf("MintThreadIDAt(%q) = %q does not match schema pattern %s", system, got, threadPattern)
			}
			parsed, err := ParseThreadID(got)
			if err != nil {
				t.Fatalf("ParseThreadID(%q) unexpected error: %v", got, err)
			}
			if parsed.System != system {
				t.Fatalf("ParseThreadID(%q).System = %q, want %q", got, parsed.System, system)
			}
		})
	}
}
