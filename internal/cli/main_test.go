package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// TestMain hardens this package's own git spawns (contract_git.go,
// cmd_contract.go, cmd_validate_ci.go all invoke git under test) against
// git's gc --auto grandchild racing a t.TempDir() cleanup's RemoveAll —
// see testkit/gitfixture/gitfixture.go's package doc for the flake this
// prevents. internal/cli's tests also live in package cli_test (same
// directory, same test binary) — Go allows exactly one TestMain per test
// binary regardless of which of the two packages declares it.
func TestMain(m *testing.M) {
	if marker := os.Getenv("A2A_TEST_DETACHED_MARKER"); marker != "" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "__a2a_detached_parent":
			if err := StartDetachedSync(); err != nil {
				os.Exit(3)
			}
			os.Exit(0)
		case "sync":
			if len(os.Args) < 3 || os.Args[2] != "--statusline-refresh" {
				os.Exit(8)
			}
			release := os.Getenv("A2A_TEST_DETACHED_RELEASE")
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(release); err == nil {
					break
				} else if !errors.Is(err, os.ErrNotExist) {
					os.Exit(4)
				}
				if time.Now().After(deadline) {
					os.Exit(5)
				}
				// This explicit release file proves the child remains runnable
				// after its one-shot parent exits; it is synchronization, not
				// sleep-and-hope.
				time.Sleep(5 * time.Millisecond)
			}
			if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
				os.Exit(6)
			}
			markerTemp := marker + ".tmp"
			if err := os.WriteFile(markerTemp, []byte("child survived parent exit\n"), 0o600); err != nil {
				os.Exit(7)
			}
			// Publish the probe atomically. A direct WriteFile briefly exposes a
			// zero-length marker and lets the parent mistake an in-progress write
			// for a failed detached child under the race-enabled full suite.
			if err := os.Rename(markerTemp, marker); err != nil {
				os.Exit(9)
			}
			os.Exit(0)
		}
	}
	os.Exit(gitfixture.RunTests(m))
}
