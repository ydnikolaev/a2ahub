package release

import (
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                                       string
		current, latest                            string
		floor, floorSpace                          string
		wantUpToDate, wantBelowFloor, wantRequired bool
		wantTarget                                 string
	}{
		{
			name:    "latest newer than current, no floor",
			current: "0.1.0", latest: "0.2.0",
			wantUpToDate: false, wantTarget: "0.2.0",
		},
		{
			name:    "up to date (equal)",
			current: "0.2.0", latest: "0.2.0",
			wantUpToDate: true, wantTarget: "0.2.0",
		},
		{
			name:    "up to date (current newer)",
			current: "0.3.0", latest: "0.2.0",
			wantUpToDate: true, wantTarget: "0.3.0",
		},
		{
			name:    "latest below floor names the space",
			current: "0.1.0", latest: "0.2.0", floor: "0.4.0", floorSpace: "getvisa",
			wantUpToDate: false, wantTarget: "0.2.0", wantBelowFloor: true, wantRequired: true,
		},
		{
			name:    "current below floor but latest satisfies it",
			current: "0.1.0", latest: "0.5.0", floor: "0.3.0", floorSpace: "getvisa",
			wantUpToDate: false, wantTarget: "0.5.0", wantBelowFloor: false, wantRequired: true,
		},
		{
			name:    "current already satisfies floor",
			current: "0.5.0", latest: "0.5.0", floor: "0.3.0", floorSpace: "getvisa",
			wantUpToDate: true, wantTarget: "0.5.0", wantBelowFloor: false, wantRequired: false,
		},
		{
			name:    "empty floor means no constraint",
			current: "0.1.0", latest: "0.2.0",
			wantUpToDate: false, wantTarget: "0.2.0", wantBelowFloor: false, wantRequired: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := Resolve(tc.current, tc.latest, Release{Commit: "sha1"}, tc.floor, tc.floorSpace)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if d.UpToDate != tc.wantUpToDate {
				t.Errorf("UpToDate = %v, want %v", d.UpToDate, tc.wantUpToDate)
			}
			if d.Target != tc.wantTarget {
				t.Errorf("Target = %q, want %q", d.Target, tc.wantTarget)
			}
			if d.BelowFloor != tc.wantBelowFloor {
				t.Errorf("BelowFloor = %v, want %v", d.BelowFloor, tc.wantBelowFloor)
			}
			if d.Required != tc.wantRequired {
				t.Errorf("Required = %v, want %v", d.Required, tc.wantRequired)
			}
			if d.Commit != "sha1" {
				t.Errorf("Commit = %q, want %q (from latestRel)", d.Commit, "sha1")
			}
		})
	}
}

func TestResolve_UnparseableVersionFailsClosed(t *testing.T) {
	t.Parallel()
	_, err := Resolve("not-a-version", "0.2.0", Release{}, "", "")
	if !errors.Is(err, version.ErrInvalidVersion) {
		t.Fatalf("Resolve() error = %v, want version.ErrInvalidVersion", err)
	}
}
