package version

import "testing"

func TestBuildStamp(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name        string
		ver, commit string
		channelVar  string
		wantChannel string
		wantStamp   string
	}{
		{"release byte-identical to the historical stamp", "0.27.0", sha, "", "release", "a2a 0.27.0 (" + sha + ")"},
		{"local", "0.27.0", sha, "local", "local", "a2a 0.27.0 (local, " + sha + ")"},
		{"unknown channel value is release", "0.27.0", sha, "beta", "release", "a2a 0.27.0 (" + sha + ")"},
		{"dev version wins over local", "dev", sha, "local", "dev", "a2a dev (" + sha + ")"},
		{"dev plain", "dev", "", "", "dev", "a2a dev (unknown)"},
		{"empty commit is unknown", "0.27.0", "", "", "release", "a2a 0.27.0 (unknown)"},
		{"local with empty commit", "0.27.0", "", "local", "local", "a2a 0.27.0 (local, unknown)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := NewBuild(tc.ver, tc.commit, tc.channelVar)
			if b.Channel != tc.wantChannel {
				t.Errorf("Channel = %q, want %q", b.Channel, tc.wantChannel)
			}
			if got := b.Stamp(); got != tc.wantStamp {
				t.Errorf("Stamp() = %q, want %q", got, tc.wantStamp)
			}
		})
	}
}

func TestIsDevBuild(t *testing.T) {
	t.Parallel()
	if !IsDevBuild("dev") || IsDevBuild("0.27.0") || IsDevBuild("") {
		t.Fatal("IsDevBuild must be true only for the literal dev")
	}
}
