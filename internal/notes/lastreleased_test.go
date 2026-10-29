package notes

import "testing"

func TestLastReleased(t *testing.T) {
	t.Parallel()

	mk := func(v, released string) ReleaseNotes { return ReleaseNotes{Version: v, Released: released} }
	tests := []struct {
		name    string
		all     []ReleaseNotes
		want    string
		wantErr bool
	}{
		{"highest dated version, not the newest file name", []ReleaseNotes{
			mk("0.9.1", "2026-01-01"), mk("0.27.0", "2026-10-01"), mk("0.10.0", "2026-05-01"),
		}, "0.27.0", false},
		{"an unreleased note above the newest dated one is skipped", []ReleaseNotes{
			mk("0.28.0", UnreleasedSentinel), mk("0.27.0", "2026-10-01"),
		}, "0.27.0", false},
		{"order does not matter", []ReleaseNotes{
			mk("0.27.0", "2026-10-01"), mk("0.28.0", UnreleasedSentinel), mk("0.26.0", "2026-09-01"),
		}, "0.27.0", false},
		{"none dated is an error", []ReleaseNotes{mk("0.28.0", UnreleasedSentinel)}, "", true},
		{"empty corpus is an error", nil, "", true},
		{"an unparseable version is an error, never skipped", []ReleaseNotes{
			mk("0.27.0", "2026-10-01"), mk("zebra", "2026-10-02"),
		}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := LastReleased(tc.all)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LastReleased error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("LastReleased = %q, want %q", got, tc.want)
			}
		})
	}
}
