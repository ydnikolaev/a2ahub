package release

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

// TestInfoFor is T5's table: a release build keeps Info's answer byte for
// byte, a local build never gets an update advisory but does get a required
// notice that names `make deliver`.
func TestInfoFor(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	release := version.NewBuild("0.27.0", sha, "")
	local := version.NewBuild("0.27.0", sha, "local")
	dev := version.NewBuild("dev", sha, "")

	tests := []struct {
		name                  string
		build                 version.Build
		latest, floor, space  string
		wantAvail, wantReq    bool
		wantGrade             Grade
		sentenceHas           string
		sentenceLacks, segHas string
	}{
		{name: "release, latest newer", build: release, latest: "0.28.0", wantAvail: true, wantGrade: GradeAvailable, sentenceHas: "run 'a2a update'"},
		{name: "local, latest newer: no advisory", build: local, latest: "0.28.0", wantGrade: GradeNone},
		{name: "release, floor above", build: release, latest: "0.28.0", floor: "0.28.0", space: "axon", wantAvail: true, wantReq: true, wantGrade: GradeRequired, sentenceHas: "run 'a2a update'"},
		{name: "local, floor above", build: local, latest: "0.28.0", floor: "0.28.0", space: "axon", wantReq: true, wantGrade: GradeRequired,
			sentenceHas: "axon pins v0.28.0; this local build is v0.27.0 — run `make deliver` in the a2ahub checkout", sentenceLacks: "a2a update", segHas: "UPDATE REQUIRED"},
		{name: "local, floor met", build: local, latest: "0.27.0", floor: "0.27.0", space: "axon", wantGrade: GradeNone},
		{name: "release, latest empty", build: release, wantGrade: GradeNone},
		{name: "local, latest empty and floor empty", build: local, wantGrade: GradeNone},
		{name: "dev behaves as a release", build: dev, latest: "0.28.0", wantGrade: GradeNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := InfoFor(tc.build, tc.latest, tc.floor, tc.space)
			if got.UpdateAvailable != tc.wantAvail || got.Required != tc.wantReq || got.Grade != tc.wantGrade {
				t.Fatalf("InfoFor = avail %v required %v grade %v, want %v %v %v", got.UpdateAvailable, got.Required, got.Grade, tc.wantAvail, tc.wantReq, tc.wantGrade)
			}
			if tc.sentenceHas != "" && !strings.Contains(got.Sentence, tc.sentenceHas) {
				t.Errorf("Sentence = %q, want it to contain %q", got.Sentence, tc.sentenceHas)
			}
			if tc.sentenceLacks != "" && strings.Contains(got.Sentence, tc.sentenceLacks) {
				t.Errorf("Sentence = %q, must not contain %q", got.Sentence, tc.sentenceLacks)
			}
			if tc.segHas != "" && !strings.Contains(got.Segment, tc.segHas) {
				t.Errorf("Segment = %q, want it to contain %q", got.Segment, tc.segHas)
			}
			if tc.wantGrade == GradeNone && (got.Sentence != "" || got.Segment != "") {
				t.Errorf("a GradeNone answer must render nothing, got sentence %q segment %q", got.Sentence, got.Segment)
			}
		})
	}
}

// TestInfoForReleaseIsInfo: for every non-local build InfoFor IS Info, which is
// what keeps a release's bytes and the value-for-value parity with
// `a2a update --json`.
func TestInfoForReleaseIsInfo(t *testing.T) {
	t.Parallel()
	for _, b := range []version.Build{version.NewBuild("0.27.0", "abc", ""), version.NewBuild("dev", "", "")} {
		if got, want := InfoFor(b, "0.28.0", "0.28.0", "axon"), Info(b.Version, "0.28.0", "0.28.0", "axon"); got != want {
			t.Fatalf("InfoFor(%v) = %+v, want Info's %+v", b, got, want)
		}
	}
}
