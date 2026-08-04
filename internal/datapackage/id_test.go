package datapackage

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// These mirror, byte-for-byte, the $defs.packageId/$defs.reportId patterns
// in schemas/data-package/v1/data-package.schema.json and
// schemas/verification-report/v1/verification-report.schema.json. Kept
// here (not exported from id.go) because their only job is to catch this
// Go code and that JSON silently drifting apart — the two are otherwise
// unconnected at compile time.
var (
	packageIDPattern = regexp.MustCompile(`^DP-[a-z0-9]+-[0-9]{8}-[0-9abcdefghjkmnpqrstvwxyz]{4}$`)
	reportIDPattern  = regexp.MustCompile(`^VR-[a-z0-9]+-[0-9]{8}-[0-9abcdefghjkmnpqrstvwxyz]{4}$`)
)

func TestMintAndParsePackageID(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 4, 15, 4, 5, 0, time.UTC)
	entropy := bytes.NewReader([]byte{0, 0, 0, 0})

	id, err := MintPackageIDAt("axon", at, entropy)
	if err != nil {
		t.Fatalf("MintPackageIDAt: %v", err)
	}
	if want := "DP-axon-20260804-0000"; id != want {
		t.Fatalf("MintPackageIDAt = %q, want %q", id, want)
	}

	parsed, err := ParsePackageID(id)
	if err != nil {
		t.Fatalf("ParsePackageID(%q): %v", id, err)
	}
	if parsed.Prefix != PackagePrefix || parsed.Class != artifact.ClassExchangeBroadcast || parsed.System != "axon" || parsed.Date != "20260804" {
		t.Fatalf("ParsePackageID(%q) = %+v, unexpected shape", id, parsed)
	}
}

func TestMintAndParseReportID(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 4, 15, 4, 5, 0, time.UTC)
	entropy := bytes.NewReader([]byte{0, 0, 0, 0})

	id, err := MintReportIDAt("seomatrix", at, entropy)
	if err != nil {
		t.Fatalf("MintReportIDAt: %v", err)
	}
	if want := "VR-seomatrix-20260804-0000"; id != want {
		t.Fatalf("MintReportIDAt = %q, want %q", id, want)
	}

	parsed, err := ParseReportID(id)
	if err != nil {
		t.Fatalf("ParseReportID(%q): %v", id, err)
	}
	if parsed.Prefix != ReportPrefix || parsed.Class != artifact.ClassExchangeBroadcast || parsed.System != "seomatrix" {
		t.Fatalf("ParseReportID(%q) = %+v, unexpected shape", id, parsed)
	}
}

func TestParsePackageID_RefusesWrongPrefix(t *testing.T) {
	t.Parallel()
	cases := []string{
		"XD-axon-20260804-k3f9", // decision — the exact collision D-1 closes
		"VR-axon-20260804-k3f9", // a report id, not a package id
		"XC-axon-order-api",     // a standing contract id, wrong class entirely
		"not-an-id-at-all",
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if _, err := ParsePackageID(id); !errors.Is(err, artifact.ErrMalformedID) {
				t.Fatalf("ParsePackageID(%q) = %v, want errors.Is ErrMalformedID", id, err)
			}
		})
	}
}

func TestParseReportID_RefusesWrongPrefix(t *testing.T) {
	t.Parallel()
	cases := []string{
		"DP-axon-20260804-k3f9", // a package id, not a report id
		"XD-axon-20260804-k3f9",
		"XC-axon-order-api",
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseReportID(id); !errors.Is(err, artifact.ErrMalformedID) {
				t.Fatalf("ParseReportID(%q) = %v, want errors.Is ErrMalformedID", id, err)
			}
		})
	}
}

func TestMintPackageID_RoundTripsThroughSchemaPattern(t *testing.T) {
	t.Parallel()
	id, err := MintPackageID("axon")
	if err != nil {
		t.Fatalf("MintPackageID: %v", err)
	}
	if !packageIDPattern.MatchString(id) {
		t.Fatalf("MintPackageID produced %q, which does not match the data-package/v1 schema's id pattern", id)
	}
}

func TestMintReportID_RoundTripsThroughSchemaPattern(t *testing.T) {
	t.Parallel()
	id, err := MintReportID("seomatrix")
	if err != nil {
		t.Fatalf("MintReportID: %v", err)
	}
	if !reportIDPattern.MatchString(id) {
		t.Fatalf("MintReportID produced %q, which does not match the verification-report/v1 schema's id pattern", id)
	}
}
