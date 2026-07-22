package release

import (
	"strings"
	"testing"
)

func TestFormatNotice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                               string
		current, latest, floor, floorSpace string
		wantGrade                          Grade
		wantContains                       string
	}{
		{name: "none: up to date", current: "0.3.0", latest: "0.3.0", wantGrade: GradeNone},
		{name: "available", current: "0.1.2", latest: "0.3.0", wantGrade: GradeAvailable, wantContains: "a2a update available: v0.1.2 -> v0.3.0"},
		{name: "required", current: "0.1.0", latest: "0.1.0", floor: "0.4.0", floorSpace: "getvisa", wantGrade: GradeRequired, wantContains: "getvisa"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text, grade := FormatNotice(tc.current, tc.latest, tc.floor, tc.floorSpace)
			if grade != tc.wantGrade {
				t.Fatalf("grade = %v, want %v", grade, tc.wantGrade)
			}
			if tc.wantGrade == GradeNone {
				if text != "" {
					t.Fatalf("text = %q, want empty for GradeNone", text)
				}
				return
			}
			if !strings.Contains(text, tc.wantContains) {
				t.Fatalf("text = %q, want to contain %q", text, tc.wantContains)
			}
			if !strings.Contains(text, "a2a update") {
				t.Fatalf("text = %q, want to name the remedy 'a2a update'", text)
			}
		})
	}
}

func TestFormatSegment(t *testing.T) {
	t.Parallel()

	text, grade := FormatSegment("0.1.2", "0.3.0", "", "")
	if grade != GradeAvailable {
		t.Fatalf("grade = %v, want GradeAvailable", grade)
	}
	if !strings.Contains(text, "0.1.2") || !strings.Contains(text, "0.3.0") {
		t.Fatalf("text = %q, want both versions present", text)
	}

	text, grade = FormatSegment("0.3.0", "0.3.0", "", "")
	if grade != GradeNone || text != "" {
		t.Fatalf("FormatSegment up-to-date = (%q, %v), want (\"\", GradeNone)", text, grade)
	}

	text, grade = FormatSegment("0.1.0", "0.1.0", "0.4.0", "getvisa")
	if grade != GradeRequired {
		t.Fatalf("grade = %v, want GradeRequired", grade)
	}
	if !strings.Contains(text, "getvisa") {
		t.Fatalf("text = %q, want to name the pinning space", text)
	}
}

// TestInfo covers the SSOT booleans (update_available / required) both
// surfaces (a2a update --json, cache.UpdateNotice) derive from — especially
// the below-floor region where the two must NOT diverge.
func TestInfo(t *testing.T) {
	cases := []struct {
		name                    string
		current, latest, floor  string
		wantAvail, wantRequired bool
		wantGrade               Grade
	}{
		{"available-only", "0.1.0", "0.3.0", "", true, false, GradeAvailable},
		{"required-and-newer", "0.1.0", "0.6.0", "0.4.0", true, true, GradeRequired},
		{"required-newer-but-below-floor", "0.1.0", "0.3.0", "0.4.0", true, true, GradeRequired},
		{"below-floor-no-newer", "0.3.0", "0.3.0", "0.4.0", false, true, GradeRequired},
		{"up-to-date-no-floor", "0.3.0", "0.3.0", "", false, false, GradeNone},
		{"unparseable-current-degrades", "dev", "0.3.0", "0.4.0", false, false, GradeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Info(tc.current, tc.latest, tc.floor, "sp")
			if got.UpdateAvailable != tc.wantAvail {
				t.Errorf("UpdateAvailable = %v, want %v", got.UpdateAvailable, tc.wantAvail)
			}
			if got.Required != tc.wantRequired {
				t.Errorf("Required = %v, want %v", got.Required, tc.wantRequired)
			}
			if got.Grade != tc.wantGrade {
				t.Errorf("Grade = %v, want %v", got.Grade, tc.wantGrade)
			}
		})
	}
}
