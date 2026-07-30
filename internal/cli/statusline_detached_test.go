package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStartDetachedSyncSurvivesParentExit(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-complete")
	release := filepath.Join(dir, "release-child")

	parent := exec.Command(executable, "__a2a_detached_parent")
	parent.Env = append(os.Environ(),
		"A2A_TEST_DETACHED_MARKER="+marker,
		"A2A_TEST_DETACHED_RELEASE="+release,
	)
	if out, err := parent.CombinedOutput(); err != nil {
		t.Fatalf("detached parent: %v\n%s", err, out)
	}

	// The parent has exited and the child is still blocked on this signal.
	if err := os.WriteFile(release, []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if got, err := os.ReadFile(marker); err == nil {
			if string(got) != "child survived parent exit\n" {
				t.Fatalf("marker = %q", got)
			}
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("detached sync child did not run after its parent exited")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
