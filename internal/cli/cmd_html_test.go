package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserCommand_PerOS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos     string
		wantBin  string
		wantArgN int // index of the path arg
	}{
		{"darwin", "open", 1},
		{"windows", "rundll32", 2},
		{"linux", "xdg-open", 1},
		{"freebsd", "xdg-open", 1}, // default branch
	}
	for _, tt := range tests {
		cmd := browserCommand(tt.goos, "/tmp/x.html")
		if !strings.HasSuffix(cmd.Path, tt.wantBin) && cmd.Args[0] != tt.wantBin {
			t.Errorf("%s: launcher = %q, want %q", tt.goos, cmd.Args[0], tt.wantBin)
		}
		if got := cmd.Args[len(cmd.Args)-1]; got != "/tmp/x.html" {
			t.Errorf("%s: path arg = %q, want the file path", tt.goos, got)
		}
		if tt.goos == "windows" && cmd.Args[1] != "url.dll,FileProtocolHandler" {
			t.Errorf("windows: missing FileProtocolHandler arg: %v", cmd.Args)
		}
	}
}

// TestHtmlCommand_DemoNoOpen exercises Run end-to-end on the embedded demo with
// --no-open: it must render a self-contained file and exit 0 WITHOUT launching a
// browser (no store needed — --demo uses the embedded fixture).
func TestHtmlCommand_DemoNoOpen(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "dash.html")
	var stdout, stderr bytes.Buffer
	code := NewHtmlCommand(nil).Run(context.Background(),
		[]string{"--demo", "--no-open", "--out", out},
		IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	page := string(b)
	if !strings.Contains(page, "const DATA = ") || !strings.Contains(page, "const DOCS = ") {
		t.Error("rendered page missing DATA/DOCS globals")
	}
	if strings.Contains(page, "A2A_DATA_START") {
		t.Error("marker leaked — template not injected")
	}
	// --no-open must not announce a browser launch.
	if strings.Contains(stdout.String(), "browser") {
		t.Errorf("--no-open should not open a browser, stdout=%q", stdout.String())
	}
}
