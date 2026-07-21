package artifact

import (
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func TestMintULIDAt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	entropy := deterministicEntropy{seed: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}

	id, err := MintULIDAt(at, entropy)
	if err != nil {
		t.Fatalf("MintULIDAt unexpected error: %v", err)
	}
	if id.Time() != ulid.Timestamp(at) {
		t.Fatalf("MintULIDAt time component = %d, want %d", id.Time(), ulid.Timestamp(at))
	}

	// Deterministic inputs mint deterministically.
	id2, err := MintULIDAt(at, entropy)
	if err != nil {
		t.Fatalf("MintULIDAt unexpected error: %v", err)
	}
	if id.String() != id2.String() {
		t.Fatalf("MintULIDAt not deterministic for identical inputs: %q != %q", id, id2)
	}

	// Round-trip through ParseULID.
	parsed, err := ParseULID(id.String())
	if err != nil {
		t.Fatalf("ParseULID(%q) unexpected error: %v", id, err)
	}
	if parsed != id {
		t.Fatalf("ParseULID(%q) = %v, want %v", id, parsed, id)
	}
}

func TestMintULID_smoke(t *testing.T) {
	t.Parallel()

	id, err := MintULID()
	if err != nil {
		t.Fatalf("MintULID unexpected error: %v", err)
	}
	if id.String() == "" {
		t.Fatalf("MintULID produced an empty string")
	}
}

func TestParseULID_malformed(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"not-a-ulid",
		"01ARZ3NDEKTSV4RRFFQ69G5FA",   // one char short
		"01ARZ3NDEKTSV4RRFFQ69G5FAVX", // one char long
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			_, err := ParseULID(in)
			if !errors.Is(err, ErrMalformedULID) {
				t.Fatalf("ParseULID(%q) error = %v, want errors.Is ErrMalformedULID", in, err)
			}
		})
	}
}
