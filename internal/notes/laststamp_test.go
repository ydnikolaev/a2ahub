package notes

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
)

// lastReleasedStamp is the code path `go run internal/notes/lastreleased.go
// <dir>` runs for `make deliver`'s local-build stamp (D-040): load the corpus
// with CorpusPattern, then adoption.LastReleased. The tool itself is a
// `//go:build ignore` main, so this test calls the same two steps in order.
func lastReleasedStamp(fsys fstest.MapFS) (string, error) {
	all, err := adoption.Load(fsys, CorpusPattern)
	if err != nil {
		return "", err
	}
	return adoption.LastReleased(all)
}

func noteFile(version, released string) *fstest.MapFile {
	return &fstest.MapFile{Data: fmt.Appendf(nil,
		"schema: release-notes/v1\nversion: %s\nreleased: %s\nheadline: h\nchanges: []\n", version, released)}
}

// TestLastReleasedStamp restores the cases the deleted lastreleased_test.go
// held against a2a's own copy, now against the unit-backed path the stamp
// tool runs.
func TestLastReleasedStamp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		want    string
		wantErr bool
	}{
		{"highest dated version, not the newest file name", fstest.MapFS{
			"0.9.1.yaml":  noteFile("0.9.1", `"2026-01-01"`),
			"0.27.0.yaml": noteFile("0.27.0", `"2026-10-01"`),
			"0.10.0.yaml": noteFile("0.10.0", `"2026-05-01"`),
		}, "0.27.0", false},
		{"an unreleased note above the newest dated one is skipped", fstest.MapFS{
			"0.28.0.yaml": noteFile("0.28.0", adoption.Unreleased),
			"0.27.0.yaml": noteFile("0.27.0", `"2026-10-01"`),
		}, "0.27.0", false},
		{"none dated is an error", fstest.MapFS{
			"0.28.0.yaml": noteFile("0.28.0", adoption.Unreleased),
		}, "", true},
		{"empty corpus is an error", fstest.MapFS{}, "", true},
		{"an unparseable version is an error, never skipped", fstest.MapFS{
			"0.27.0.yaml": noteFile("0.27.0", `"2026-10-01"`),
			"zebra.yaml":  noteFile("zebra", `"2026-10-02"`),
		}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := lastReleasedStamp(tc.fsys)
			if (err != nil) != tc.wantErr {
				t.Fatalf("stamp error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("stamp = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLastReleasedStampIgnoresInputOrder: adoption.LastReleased is handed an
// unsorted slice directly (Load sorts, so the file-based cases above cannot
// show this), and still picks the highest dated version.
func TestLastReleasedStampIgnoresInputOrder(t *testing.T) {
	t.Parallel()
	got, err := adoption.LastReleased([]adoption.Release{
		{Version: "0.27.0", Released: "2026-10-01"},
		{Version: "0.28.0", Released: adoption.Unreleased},
		{Version: "0.26.0", Released: "2026-09-01"},
	})
	if err != nil || got != "0.27.0" {
		t.Fatalf("LastReleased = %q, %v; want 0.27.0", got, err)
	}
}

// TestLastReleasedStampOverTheEmbeddedCorpus: the real corpus yields a dated
// version, never an authored-ahead one.
func TestLastReleasedStampOverTheEmbeddedCorpus(t *testing.T) {
	t.Parallel()
	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
	if err != nil {
		t.Fatal(err)
	}
	got, err := adoption.LastReleased(all)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := adoption.Exactly(all, got); !ok || r.Released == adoption.Unreleased {
		t.Fatalf("stamp %q is not a dated release in the corpus", got)
	}
}
