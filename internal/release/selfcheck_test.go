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

// The self-check must read the FIRST LINE of stdout, not the whole of it. A
// binary that prints anything after its stamp was, until 2026-08-22, an
// unupgradeable binary: v0.25.0 gave `a2a version` a three-axis report on
// stdout and every installed a2a refused the upgrade as "unparseable".
func TestParseVersionStamp_ReadsTheFirstLineOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		out  string
		want string
		ok   bool
	}{
		{"bare stamp", "a2a 0.25.2 (abc1234)\n", "0.25.2", true},
		{"stamp with no trailing newline", "a2a 0.25.2 (abc1234)", "0.25.2", true},
		{"stamp followed by a report", "a2a 0.25.2 (abc1234)\n\nbinary    0.25.2\nlatest    0.25.2   up to date\n", "0.25.2", true},
		{"v-prefixed", "v-prefixed is not the stamp shape", "", false},
		{"report with no stamp line", "binary    0.25.2\na2a 0.25.2 (abc1234)\n", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseVersionStamp(tc.out)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: version = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The whole point of reading one line is that a self-check PASSES against a
// binary whose version verb says more than its stamp.
func TestSelfCheckVersion_AcceptsAStampFollowedByAReport(t *testing.T) {
	t.Parallel()
	run := func(context.Context, string, ...string) (string, error) {
		return "a2a 0.25.2 (abc1234)\n\nbinary    0.25.2\nspaces    none connected\n", nil
	}
	if err := SelfCheckVersion(context.Background(), "/tmp/a2a.new", "0.25.2", run); err != nil {
		t.Fatalf("SelfCheckVersion refused a stamp line it can read: %v", err)
	}
}
