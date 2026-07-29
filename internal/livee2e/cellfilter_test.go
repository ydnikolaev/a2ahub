package livee2e

import (
	"strings"
	"testing"
)

func TestSelectedCellsNarrowsToExactDeclaredCells(t *testing.T) {
	t.Parallel()

	got, err := selectedCells(
		" contract-publish-deprecate-retire/A/cli ",
		Catalogue(),
	)
	if err != nil {
		t.Fatalf("selectedCells: %v", err)
	}
	want := cellKey{
		scenario: "contract-publish-deprecate-retire",
		system:   SystemA,
		surface:  SurfaceCLI,
	}
	if len(got) != 1 || !got[want] {
		t.Fatalf("selected cells = %v, want only %s", got, renderCellKey(want))
	}
}

func TestSelectedCellsUnsetMeansNoCellNarrowing(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "  ", "\t"} {
		got, err := selectedCells(raw, Catalogue())
		if err != nil {
			t.Fatalf("raw %q: %v", raw, err)
		}
		if got != nil {
			t.Fatalf("raw %q selected %v, want nil (all cells)", raw, got)
		}
	}
}

func TestSelectedCellsRefusesUnknownOrMalformedCell(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"contract-publish-deprecate-retire/A",
		"contract-publish-deprecate-retire/C/cli",
		"contract-publish-deprecate-retire/A/http",
	} {
		if _, err := selectedCells(raw, Catalogue()); err == nil {
			t.Fatalf("raw %q: expected refusal", raw)
		} else if !strings.Contains(err.Error(), raw) {
			t.Fatalf("raw %q: refusal must name the bad cell; got %v", raw, err)
		}
	}
}
