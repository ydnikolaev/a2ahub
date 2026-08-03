package cli

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/localserver"
)

type fakeServeLauncher struct {
	options ServeOptions
	err     error
	calls   int
}

func (launcher *fakeServeLauncher) Serve(_ context.Context, options ServeOptions) error {
	launcher.calls++
	launcher.options = options
	return launcher.err
}

func TestServeCommandDefaultsAreLoopbackAndOffline(t *testing.T) {
	t.Parallel()
	launcher := &fakeServeLauncher{}
	var stdout, stderr bytes.Buffer
	code := NewServeCommand(launcher).Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 || launcher.calls != 1 {
		t.Fatalf("Run = %d, calls = %d, stderr = %q", code, launcher.calls, stderr.String())
	}
	if launcher.options.Config.Listen != localserver.DefaultListen ||
		launcher.options.Config.Refresh != localserver.DefaultRefresh ||
		launcher.options.Config.SyncEvery != 0 || launcher.options.Open {
		t.Fatalf("options = %#v", launcher.options)
	}
}

func TestServeCommandPassesExplicitOptions(t *testing.T) {
	t.Parallel()
	launcher := &fakeServeLauncher{}
	code := NewServeCommand(launcher).Run(context.Background(), []string{
		"--listen", "[::1]:9876", "--refresh", "750ms", "--sync-every", "15s", "--open=true",
	}, IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if code != 0 {
		t.Fatalf("Run = %d", code)
	}
	if launcher.options.Config.Listen != "[::1]:9876" || launcher.options.Config.Refresh != 750*time.Millisecond ||
		launcher.options.Config.SyncEvery != 15*time.Second || !launcher.options.Open {
		t.Fatalf("options = %#v", launcher.options)
	}
}

func TestServeCommandRejectsUnsafeOrOutOfRangeBeforeLauncher(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		{"--listen", "0.0.0.0:8765"},
		{"--listen", "192.0.2.1:8765"},
		{"--refresh", "249ms"},
		{"--refresh", "61s"},
		{"--sync-every", "14s"},
		{"--sync-every", "25h"},
	}
	for _, args := range tests {
		args := args
		t.Run(args[0]+"="+args[1], func(t *testing.T) {
			t.Parallel()
			launcher := &fakeServeLauncher{}
			if code := NewServeCommand(launcher).Run(context.Background(), args, IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}); code != 2 {
				t.Fatalf("Run(%v) = %d, want usage error", args, code)
			}
			if launcher.calls != 0 {
				t.Fatalf("launcher called for invalid options %v", args)
			}
		})
	}
}

func TestServeCommandMapsLauncherFailure(t *testing.T) {
	t.Parallel()
	launcher := &fakeServeLauncher{err: errors.New("bind failed")}
	var stderr bytes.Buffer
	if code := NewServeCommand(launcher).Run(context.Background(), nil, IO{Stdout: &bytes.Buffer{}, Stderr: &stderr}); code != 1 {
		t.Fatalf("Run = %d", code)
	}
	if launcher.calls != 1 || !bytes.Contains(stderr.Bytes(), []byte("bind failed")) {
		t.Fatalf("calls = %d, stderr = %q", launcher.calls, stderr.String())
	}
}
