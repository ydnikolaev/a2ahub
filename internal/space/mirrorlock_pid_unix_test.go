//go:build darwin || linux

package space

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// deadPID returns the pid of a process that has started and been reaped, so
// it is not alive on this host.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run throwaway process: %v", err)
	}
	return cmd.ProcessState.Pid()
}

// A holder that died is reclaimed at once, even though its timestamp is fresh.
func TestAcquireMirrorLockReclaimsADeadHoldersFreshLock(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	writeMirrorLockFile(t, gitDir, mirrorLockPayload{PID: deadPID(t), AcquiredAt: time.Now()})

	start := time.Now()
	lock, err := AcquireMirrorLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireMirrorLock over a dead holder's fresh lock: %v", err)
	}
	defer func() { _ = lock.Release() }()
	if elapsed := time.Since(start); elapsed >= mirrorLockWaitBudget {
		t.Fatalf("reclaim took %s, want immediate", elapsed)
	}
}

// A live holder keeps the age rule: a fresh lock is never stolen, and the
// refusal names the holder.
func TestAcquireMirrorLockRefusalNamesALiveHolder(t *testing.T) {
	t.Parallel()

	dir, gitDir := seedMirrorForLockTest(t)
	path := writeMirrorLockFile(t, gitDir, mirrorLockPayload{PID: os.Getpid(), AcquiredAt: time.Now().Add(-30 * time.Second)})
	t.Cleanup(func() { _ = os.Remove(path) })

	_, err := acquireMirrorLockWithin(context.Background(), dir, 50*time.Millisecond)
	if !errors.Is(err, ErrMirrorLocked) {
		t.Fatalf("error = %v, want ErrMirrorLocked", err)
	}
	var locked *MirrorLockedError
	if !errors.As(err, &locked) {
		t.Fatalf("error %T is not a *MirrorLockedError", err)
	}
	if locked.PID != os.Getpid() || locked.Holder != "alive" || locked.Held < 30*time.Second {
		t.Errorf("holder = pid %d %s held %s, want this pid, alive, >= 30s", locked.PID, locked.Holder, locked.Held)
	}
	for _, want := range []string{"pid ", "(alive)", "reclaimed automatically after 2m0s", "at once when the holder is dead"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func TestMirrorLockHolderState(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		pid  int
		want mirrorLockHolder
	}{
		"self":     {os.Getpid(), mirrorLockHolderAlive},
		"dead":     {deadPID(t), mirrorLockHolderDead},
		"zero":     {0, mirrorLockHolderUnknown},
		"negative": {-1, mirrorLockHolderUnknown},
	} {
		if got := mirrorLockHolderState(tc.pid); got != tc.want {
			t.Errorf("%s: state(%d) = %s, want %s", name, tc.pid, got, tc.want)
		}
	}
}
