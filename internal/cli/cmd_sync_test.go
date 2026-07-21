package cli_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestSyncFetchesAllConnectedMirrors: sync fetches every connected
// space's mirror and calls the cache-refresh seam for each.
func TestSyncFetchesAllConnectedMirrors(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")

	connect := cli.NewConnectCommand(cfgPath, machinePath, dir)
	io1, _, _ := newIO()
	if code := connect.Run(context.Background(), []string{fx.RemoteURL()}, io1); code != 0 {
		t.Fatalf("connect: code = %d", code)
	}

	pending := &recordingPendingMarker{}
	sync := cli.NewSyncCommand(cfgPath, machinePath, dir, pending)
	io2, out, errOut := newIO()
	code := sync.Run(context.Background(), nil, io2)
	if code != 0 {
		t.Fatalf("sync: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(pending.calls) != 1 {
		t.Fatalf("expected exactly one cache-refresh seam call, got %d", len(pending.calls))
	}
}

func TestSyncNoConnectedSpaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	pending := &recordingPendingMarker{}
	sync := cli.NewSyncCommand(cfgPath, machinePath, dir, pending)

	io, out, _ := newIO()
	code := sync.Run(context.Background(), nil, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (no connected spaces is trivially synced)", code)
	}
	if out.Len() == 0 {
		t.Fatal("expected an informational message")
	}
	if len(pending.calls) != 0 {
		t.Fatalf("expected zero seam calls with no connected spaces, got %d", len(pending.calls))
	}
}

func TestSyncRejectsUnexpectedArgs(t *testing.T) {
	t.Parallel()
	sync := cli.NewSyncCommand("cfg.yaml", "machine.yaml", t.TempDir(), cli.NewNoopPendingMarker())
	io, _, _ := newIO()
	code := sync.Run(context.Background(), []string{"unexpected"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error)", code)
	}
}

type recordingPendingMarker struct {
	calls []string
}

func (m *recordingPendingMarker) MarkPending(_ context.Context, spaceID, _ string, _ space.WriteResult) error {
	m.calls = append(m.calls, spaceID)
	return nil
}
