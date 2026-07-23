package space

import (
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// TestLayoutPaths is spec 05 §8 AC row 6: the layout builder's paths match
// the §4.2 tree exactly for all 8 artifact-type locations plus
// consumes.yaml, decisions/, vendored/ (golden path table).
func TestLayoutPaths(t *testing.T) {
	t.Parallel()

	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"provides contract (XC)", l.ProvidesContract("ingest"), "axon/provides/ingest/contract.md"},
		{"provides schema dir", l.ProvidesSchemaDir("ingest"), "axon/provides/ingest/schema"},
		{"provides fixtures valid", l.ProvidesFixturesValidDir("ingest"), "axon/provides/ingest/fixtures/valid"},
		{"provides fixtures invalid", l.ProvidesFixturesInvalidDir("ingest"), "axon/provides/ingest/fixtures/invalid"},
		{"requires (XR)", l.Requires("XR-axon-country-vocabulary"), "axon/requires/XR-axon-country-vocabulary.md"},
		{"consumes.yaml", l.ConsumesYAML(), "axon/consumes.yaml"},
		{"exchange question (XQ)", l.Exchange("XQ-axon-20260721-k3f9"), "axon/exchanges/XQ-axon-20260721-k3f9.md"},
		{"exchange work_request (XW)", l.Exchange("XW-axon-20260721-a1b2"), "axon/exchanges/XW-axon-20260721-a1b2.md"},
		{"exchange handoff (XH)", l.Exchange("XH-axon-20260721-c3d4"), "axon/exchanges/XH-axon-20260721-c3d4.md"},
		{"broadcast announcement (XA)", l.Exchange("XA-axon-20260721-e5f6"), "axon/exchanges/XA-axon-20260721-e5f6.md"},
		{"response (XS)", l.Exchange("XS-axon-20260721-g7h8"), "axon/exchanges/XS-axon-20260721-g7h8.md"},
		{"event file", l.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS"), "axon/events/2026/01J8QYK2Z3ABCDEFGHJKMNPQRS.yaml"},
		{"docs dir", l.DocsDir(), "axon/docs"},
		{"decision (XD, space-level)", Decision("XD-space-20260721-i9j0"), "decisions/XD-space-20260721-i9j0.md"},
		{"vendored dir (space-level)", VendoredDir("acme-corp"), "vendored/acme-corp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestLayoutPathsPassTheirIDPlacementGuard is the standing gate over the
// defect fb-20260723-9ae145 reported: the path a write COMMITS an artifact
// to (this Layout) and the placement guard V2/V3 then RUN over that path
// (internal/artifact) must agree for every artifact type, or the type is
// silently unpublishable — the contract family was, for the whole of
// v0.2.0, because provides/<slug>/contract.md can never satisfy the
// default "stem == id" rule. Any new layout constructor gets a row here.
func TestLayoutPathsPassTheirIDPlacementGuard(t *testing.T) {
	t.Parallel()

	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	cases := []struct {
		name      string
		id        string
		path      string
		placement artifact.Placement
	}{
		{"contract (XC)", "XC-axon-ingest", l.ProvidesContract("ingest"), artifact.PlacementProvidesContract},
		{"requirement (XR)", "XR-axon-country-vocabulary", l.Requires("XR-axon-country-vocabulary"), artifact.PlacementSectionFile},
		{"question (XQ)", "XQ-axon-20260721-k3f9", l.Exchange("XQ-axon-20260721-k3f9"), artifact.PlacementSectionFile},
		{"work_request (XW)", "XW-axon-20260721-a1b2", l.Exchange("XW-axon-20260721-a1b2"), artifact.PlacementSectionFile},
		{"handoff (XH)", "XH-axon-20260721-c3d4", l.Exchange("XH-axon-20260721-c3d4"), artifact.PlacementSectionFile},
		{"announcement (XA)", "XA-axon-20260721-e5f6", l.Exchange("XA-axon-20260721-e5f6"), artifact.PlacementSectionFile},
		{"response (XS)", "XS-axon-20260721-g7h8", l.Exchange("XS-axon-20260721-g7h8"), artifact.PlacementSectionFile},
		{"decision (XD, space-level)", "XD-axon-20260721-j9k0", Decision("XD-axon-20260721-j9k0"), artifact.PlacementSpaceLevel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, perr := artifact.ParseID(tc.id)
			if perr != nil {
				t.Fatalf("ParseID(%q): %v", tc.id, perr)
			}
			if err := artifact.ValidateAt(id, tc.path, tc.placement); err != nil {
				t.Fatalf("the layout's own committed path %q reds its id-placement guard: %v", tc.path, err)
			}
		})
	}
}

func TestNewLayoutRejectsInvalidSystemID(t *testing.T) {
	t.Parallel()

	cases := []string{"", "Axon", "axon-mirror", "axon_mirror", "über"}
	for _, sys := range cases {
		t.Run(sys, func(t *testing.T) {
			t.Parallel()
			_, err := NewLayout(sys)
			if !errors.Is(err, ErrInvalidSystemID) {
				t.Fatalf("NewLayout(%q) error = %v, want ErrInvalidSystemID", sys, err)
			}
		})
	}
}

func TestNewLayoutAcceptsValidSystemID(t *testing.T) {
	t.Parallel()

	for _, sys := range []string{"axon", "seomatrix", "sys2"} {
		t.Run(sys, func(t *testing.T) {
			t.Parallel()
			l, err := NewLayout(sys)
			if err != nil {
				t.Fatalf("NewLayout(%q): %v", sys, err)
			}
			if l.System != sys {
				t.Fatalf("Layout.System = %q, want %q", l.System, sys)
			}
		})
	}
}
