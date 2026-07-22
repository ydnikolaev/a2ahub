package release

import (
	"context"
	"errors"
	"testing"
)

func fakeRunner(output string, err error) Runner {
	return func(_ context.Context, _ string, _ ...string) (string, error) {
		return output, err
	}
}

func TestSelfCheckVersion_MatchPasses(t *testing.T) {
	t.Parallel()
	run := fakeRunner("a2a 0.3.0 (abc123)\n", nil)
	if err := SelfCheckVersion(context.Background(), "/tmp/a2a.new", "0.3.0", run); err != nil {
		t.Fatalf("SelfCheckVersion: %v", err)
	}
}

func TestSelfCheckVersion_VPrefixTolerated(t *testing.T) {
	t.Parallel()
	run := fakeRunner("a2a 0.3.0 (abc123)\n", nil)
	if err := SelfCheckVersion(context.Background(), "/tmp/a2a.new", "v0.3.0", run); err != nil {
		t.Fatalf("SelfCheckVersion: %v", err)
	}
}

func TestSelfCheckVersion_MismatchFails(t *testing.T) {
	t.Parallel()
	run := fakeRunner("a2a 0.2.9 (abc123)\n", nil)
	err := SelfCheckVersion(context.Background(), "/tmp/a2a.new", "0.3.0", run)
	if !errors.Is(err, ErrSelfCheckFailed) {
		t.Fatalf("SelfCheckVersion() error = %v, want ErrSelfCheckFailed", err)
	}
}

func TestSelfCheckVersion_UnparseableOutputFails(t *testing.T) {
	t.Parallel()
	run := fakeRunner("garbage output\n", nil)
	err := SelfCheckVersion(context.Background(), "/tmp/a2a.new", "0.3.0", run)
	if !errors.Is(err, ErrSelfCheckFailed) {
		t.Fatalf("SelfCheckVersion() error = %v, want ErrSelfCheckFailed", err)
	}
}

func TestSelfCheckVersion_RunFailureFails(t *testing.T) {
	t.Parallel()
	run := fakeRunner("", errors.New("exec: no such file"))
	err := SelfCheckVersion(context.Background(), "/tmp/a2a.new", "0.3.0", run)
	if !errors.Is(err, ErrSelfCheckFailed) {
		t.Fatalf("SelfCheckVersion() error = %v, want ErrSelfCheckFailed", err)
	}
}

func TestSelfCheckVersion_NilRunnerUsesDefault(t *testing.T) {
	t.Parallel()
	// A nonexistent binary path with the default (os/exec) runner must
	// fail closed (ErrSelfCheckFailed), never panic.
	err := SelfCheckVersion(context.Background(), "/nonexistent/a2a-binary-does-not-exist", "0.3.0", nil)
	if !errors.Is(err, ErrSelfCheckFailed) {
		t.Fatalf("SelfCheckVersion() error = %v, want ErrSelfCheckFailed", err)
	}
}
