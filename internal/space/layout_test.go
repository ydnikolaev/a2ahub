package space

import (
	"errors"
	"testing"
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

func TestNewLayoutRejectsInvalidSystemID(t *testing.T) {
	t.Parallel()

	cases := []string{"", "Axon", "axon-mirror", "axon_mirror", "über"}
	for _, sys := range cases {
		sys := sys
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
		sys := sys
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
