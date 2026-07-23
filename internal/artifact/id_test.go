package artifact

import (
	"errors"
	"testing"
	"time"
)

func TestMintStandingID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		prefix, system string
		slug           string
		want           string
		wantErr        error
	}{
		{name: "simple", prefix: "XC", system: "axon", slug: "ingest", want: "XC-axon-ingest"},
		{name: "slug with hyphens", prefix: "XR", system: "axon", slug: "country-vocabulary", want: "XR-axon-country-vocabulary"},
		{name: "empty slug rejected", prefix: "XC", system: "axon", slug: "", wantErr: ErrEmptyField},
		{name: "empty system rejected", prefix: "XC", system: "", slug: "ingest", wantErr: ErrEmptyField},
		{name: "empty prefix rejected", prefix: "", system: "axon", slug: "ingest", wantErr: ErrEmptyField},
		{name: "hyphenated system rejected", prefix: "XC", system: "my-system", slug: "ingest", wantErr: ErrMalformedID},
		{name: "lowercase prefix rejected", prefix: "xc", system: "axon", slug: "ingest", wantErr: ErrMalformedID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MintStandingID(tc.prefix, tc.system, tc.slug)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("MintStandingID(%q,%q,%q) error = %v, want errors.Is %v", tc.prefix, tc.system, tc.slug, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("MintStandingID(%q,%q,%q) unexpected error: %v", tc.prefix, tc.system, tc.slug, err)
			}
			if got != tc.want {
				t.Fatalf("MintStandingID(%q,%q,%q) = %q, want %q", tc.prefix, tc.system, tc.slug, got, tc.want)
			}

			// Round-trip through ParseID.
			parsed, err := ParseID(got)
			if err != nil {
				t.Fatalf("ParseID(%q) unexpected error: %v", got, err)
			}
			if parsed.Class != ClassStanding || parsed.Prefix != tc.prefix || parsed.System != tc.system || parsed.Slug != tc.slug {
				t.Fatalf("ParseID(%q) = %+v, want prefix=%q system=%q slug=%q class=standing", got, parsed, tc.prefix, tc.system, tc.slug)
			}
		})
	}
}

func TestMintExchangeIDAt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC)
	entropy := deterministicEntropy{seed: []byte{0, 0, 0, 0}} // -> "0000" in the alphabet

	got, err := MintExchangeIDAt("XQ", "axon", at, entropy)
	if err != nil {
		t.Fatalf("MintExchangeIDAt unexpected error: %v", err)
	}
	want := "XQ-axon-20260721-0000"
	if got != want {
		t.Fatalf("MintExchangeIDAt = %q, want %q", got, want)
	}

	parsed, err := ParseID(got)
	if err != nil {
		t.Fatalf("ParseID(%q) unexpected error: %v", got, err)
	}
	if parsed.Class != ClassExchangeBroadcast || parsed.Prefix != "XQ" || parsed.System != "axon" || parsed.Date != "20260721" || parsed.Rand != "0000" {
		t.Fatalf("ParseID(%q) = %+v, want exchange-broadcast XQ/axon/20260721/0000", got, parsed)
	}
}

func TestMintExchangeIDAt_rejectsBadInputs(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	entropy := deterministicEntropy{seed: []byte{1, 2, 3, 4}}

	cases := []struct {
		name           string
		prefix, system string
		wantErr        error
	}{
		{name: "empty prefix", prefix: "", system: "axon", wantErr: ErrEmptyField},
		{name: "empty system", prefix: "XQ", system: "", wantErr: ErrEmptyField},
		{name: "hyphenated system", prefix: "XQ", system: "my-system", wantErr: ErrMalformedID},
		{name: "lowercase prefix", prefix: "xq", system: "axon", wantErr: ErrMalformedID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := MintExchangeIDAt(tc.prefix, tc.system, at, entropy)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("MintExchangeIDAt(%q,%q) error = %v, want errors.Is %v", tc.prefix, tc.system, err, tc.wantErr)
			}
		})
	}
}

func TestMintExchangeID_smokeNoTrivialCollision(t *testing.T) {
	// reason: this is a tight-loop smoke test of the real crypto/rand
	// path, not a formal collision proof (§6) — not parallel-safe to
	// subtest split since it deliberately shares state minimally; kept
	// single-goroutine for a simple, fast assertion.
	t.Parallel()

	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		id, err := MintExchangeID("XQ", "axon")
		if err != nil {
			t.Fatalf("MintExchangeID unexpected error: %v", err)
		}
		if seen[id] {
			t.Fatalf("MintExchangeID produced a collision on iteration %d: %q", i, id)
		}
		seen[id] = true
	}
}

func TestParseID_malformed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty string", in: ""},
		{name: "missing system", in: "XC-slug"},
		{name: "malformed prefix (lowercase)", in: "xc-axon-ingest"},
		{name: "malformed prefix (digits)", in: "X1-axon-ingest"},
		{name: "empty slug/rest", in: "XC-axon-"},
		{name: "wrong date length", in: "XQ-axon-2026072-k3f9"},
		{name: "non-base32 suffix (uppercase)", in: "XQ-axon-20260721-K3F9"},
		{name: "non-base32 suffix (invalid char i)", in: "XQ-axon-20260721-i3f9"},
		{name: "non-base32 suffix (too short)", in: "XQ-axon-20260721-k3f"},
		// Security: a standing slug must not carry a path traversal — the
		// slug flows into layout.ProvidesContract/Exchange (path.Join), so an
		// escaping slug would be a local file-read oracle (D-014).
		{name: "slug path traversal (dotdot)", in: "XC-axon-../../../../etc/passwd"},
		{name: "slug with slash", in: "XC-axon-provides/secret"},
		{name: "slug with dot segment", in: "XC-axon-a.b"},
		{name: "slug uppercase", in: "XC-axon-Ingest"},
		{name: "slug leading hyphen", in: "XC-axon--ingest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseID(tc.in)
			if !errors.Is(err, ErrMalformedID) {
				t.Fatalf("ParseID(%q) error = %v, want errors.Is ErrMalformedID", tc.in, err)
			}
		})
	}
}

func TestParseID_valid(t *testing.T) {
	t.Parallel()

	standing, err := ParseID("XC-axon-ingest")
	if err != nil {
		t.Fatalf("ParseID(standing) unexpected error: %v", err)
	}
	if standing.Class != ClassStanding || standing.Prefix != "XC" || standing.System != "axon" || standing.Slug != "ingest" {
		t.Fatalf("ParseID(standing) = %+v, unexpected", standing)
	}

	exchange, err := ParseID("XQ-axon-20260721-k3f9")
	if err != nil {
		t.Fatalf("ParseID(exchange) unexpected error: %v", err)
	}
	if exchange.Class != ClassExchangeBroadcast || exchange.Prefix != "XQ" || exchange.System != "axon" || exchange.Date != "20260721" || exchange.Rand != "k3f9" {
		t.Fatalf("ParseID(exchange) = %+v, unexpected", exchange)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	id, err := ParseID("XC-axon-ingest")
	if err != nil {
		t.Fatalf("ParseID unexpected error: %v", err)
	}

	t.Run("both guards pass", func(t *testing.T) {
		t.Parallel()
		if err := Validate(id, "axon/provides/ingest/XC-axon-ingest.md"); err != nil {
			t.Fatalf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("filename stem mismatch only", func(t *testing.T) {
		t.Parallel()
		err := Validate(id, "axon/provides/ingest/XC-axon-wrong.md")
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("Validate() error = %v, want errors.Is ErrIDMismatch", err)
		}
		if errors.Is(err, ErrSectionMismatch) {
			t.Fatalf("Validate() unexpectedly also reports ErrSectionMismatch: %v", err)
		}
	})

	t.Run("owning section mismatch only", func(t *testing.T) {
		t.Parallel()
		err := Validate(id, "seomatrix/provides/ingest/XC-axon-ingest.md")
		if !errors.Is(err, ErrSectionMismatch) {
			t.Fatalf("Validate() error = %v, want errors.Is ErrSectionMismatch", err)
		}
		if errors.Is(err, ErrIDMismatch) {
			t.Fatalf("Validate() unexpectedly also reports ErrIDMismatch: %v", err)
		}
	})

	t.Run("both guards fail", func(t *testing.T) {
		t.Parallel()
		err := Validate(id, "seomatrix/provides/ingest/XC-axon-wrong.md")
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("Validate() error = %v, want errors.Is ErrIDMismatch", err)
		}
		if !errors.Is(err, ErrSectionMismatch) {
			t.Fatalf("Validate() error = %v, want errors.Is ErrSectionMismatch", err)
		}
	})
}

func TestValidateAtProvidesContract(t *testing.T) {
	t.Parallel()

	id, err := ParseID("XC-axon-ingest")
	if err != nil {
		t.Fatalf("ParseID unexpected error: %v", err)
	}

	t.Run("the fixed contract.md filename passes", func(t *testing.T) {
		t.Parallel()
		// The whole point: the stem is the literal "contract", never the
		// id — under the default placement this path is unconditionally
		// ErrIDMismatch (which made every contract unpublishable).
		if err := ValidateAt(id, "axon/provides/ingest/contract.md", PlacementProvidesContract); err != nil {
			t.Fatalf("ValidateAt() unexpected error: %v", err)
		}
		if err := Validate(id, "axon/provides/ingest/contract.md"); !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("default placement should still red the fixed filename, got %v", err)
		}
	})

	t.Run("wrong slug directory is still an id mismatch", func(t *testing.T) {
		t.Parallel()
		err := ValidateAt(id, "axon/provides/other-feed/contract.md", PlacementProvidesContract)
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("ValidateAt() error = %v, want errors.Is ErrIDMismatch", err)
		}
	})

	t.Run("wrong filename under the right slug is an id mismatch", func(t *testing.T) {
		t.Parallel()
		err := ValidateAt(id, "axon/provides/ingest/descriptor.md", PlacementProvidesContract)
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("ValidateAt() error = %v, want errors.Is ErrIDMismatch", err)
		}
	})

	t.Run("foreign section reports both guards", func(t *testing.T) {
		t.Parallel()
		err := ValidateAt(id, "seomatrix/provides/ingest/contract.md", PlacementProvidesContract)
		if !errors.Is(err, ErrSectionMismatch) {
			t.Fatalf("ValidateAt() error = %v, want errors.Is ErrSectionMismatch", err)
		}
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("a path outside the id's own section cannot identify it; got %v", err)
		}
	})

	t.Run("an exchange-class id can never satisfy this placement", func(t *testing.T) {
		t.Parallel()
		exchangeID, perr := ParseID("XQ-axon-20260721-k3f9")
		if perr != nil {
			t.Fatalf("ParseID unexpected error: %v", perr)
		}
		err := ValidateAt(exchangeID, "axon/provides/k3f9/contract.md", PlacementProvidesContract)
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("ValidateAt() error = %v, want errors.Is ErrIDMismatch", err)
		}
	})
}

func TestValidateAtSpaceLevel(t *testing.T) {
	t.Parallel()

	id, err := ParseID("XD-axon-20260802-r1w9")
	if err != nil {
		t.Fatalf("ParseID unexpected error: %v", err)
	}

	t.Run("decisions/<id>.md passes", func(t *testing.T) {
		t.Parallel()
		if err := ValidateAt(id, "decisions/XD-axon-20260802-r1w9.md", PlacementSpaceLevel); err != nil {
			t.Fatalf("ValidateAt() unexpected error: %v", err)
		}
		if err := Validate(id, "decisions/XD-axon-20260802-r1w9.md"); !errors.Is(err, ErrSectionMismatch) {
			t.Fatalf("default placement should still red the space-level dir, got %v", err)
		}
	})

	t.Run("the stem guard still applies", func(t *testing.T) {
		t.Parallel()
		err := ValidateAt(id, "decisions/XD-axon-20260802-wrong.md", PlacementSpaceLevel)
		if !errors.Is(err, ErrIDMismatch) {
			t.Fatalf("ValidateAt() error = %v, want errors.Is ErrIDMismatch", err)
		}
		if errors.Is(err, ErrSectionMismatch) {
			t.Fatalf("space-level placement must never report a section mismatch: %v", err)
		}
	})
}

// deterministicEntropy is a fixed io.Reader for testable mint calls —
// every Read fills from seed cyclically.
type deterministicEntropy struct{ seed []byte }

func (d deterministicEntropy) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = d.seed[i%len(d.seed)]
	}
	return len(p), nil
}
