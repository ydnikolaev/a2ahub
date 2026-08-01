package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestTroubleshootingEnumeratesEveryDoctorCheck(t *testing.T) {
	t.Parallel()
	cmd := NewDoctorCommand(host.NewFakeHost(), "0.16.0", "/unused/project.yaml", "/unused/machine.yaml", t.TempDir())
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) { return space.ProjectConfig{}, nil }
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.cachePath = func() (string, error) { return "/unused/no-update-cache.json", nil }
	cmd.lookupGit = func() error { return nil }

	var stdout, stderr bytes.Buffer
	if code := cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr}); code != 0 {
		t.Fatalf("doctor code=%d stderr=%s", code, stderr.String())
	}
	raw, err := os.ReadFile("../../skill/a2ahub/troubleshooting.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	var names []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		name, _, found := strings.Cut(line, ": PASS")
		if !found {
			continue
		}
		names = append(names, name)
		if !strings.Contains(doc, "| **"+name+"** |") {
			t.Errorf("troubleshooting omits doctor check %q", name)
		}
	}
	if len(names) != 16 {
		t.Fatalf("doctor emitted %d checks (%v), want 16; update the documented count and this tripwire together", len(names), names)
	}
	if !strings.Contains(doc, "## The sixteen checks") {
		t.Fatal("troubleshooting doctor-count heading does not match the executable list")
	}
}
