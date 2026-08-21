package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/release"
)

// These are spec 19 T4's assertions, moved down to the ONE function that now
// renders the advisory. They used to live twice, on `a2a inbox` and
// `a2a outbox`, which is how the rule "some verbs tell you, others do not"
// came to exist without anyone deciding it. The contract is unchanged: never a
// byte on stdout, prose on stderr in human mode, the shared UpdateNotice
// object on stderr in --json mode.

func advisoryNotice(t *testing.T, current, latest string) cache.UpdateNotice {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, t.TempDir(), latest, now)
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice(current, cachePath, time.Hour, nil)
	return store.UpdateNotice()
}

func TestWriteUpdateAdvisory_HumanModeProse(t *testing.T) {
	t.Parallel()
	io, out, errOut := newIO()
	cli.WriteUpdateAdvisory(io, advisoryNotice(t, "0.1.2", "0.3.0"), false)

	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty — the advisory must never move a consumer's bytes", out.String())
	}
	if !strings.Contains(errOut.String(), "note: a2a update available: v0.1.2 -> v0.3.0 — run 'a2a update'") {
		t.Fatalf("stderr = %q, want the update-available prose line", errOut.String())
	}
}

func TestWriteUpdateAdvisory_JSONModeObject(t *testing.T) {
	t.Parallel()
	io, out, errOut := newIO()
	notice := advisoryNotice(t, "0.1.2", "0.3.0")
	cli.WriteUpdateAdvisory(io, notice, true)

	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	var got cache.UpdateNotice
	if err := json.Unmarshal([]byte(strings.TrimSpace(errOut.String())), &got); err != nil {
		t.Fatalf("stderr is not one UpdateNotice JSON line: %v (%q)", err, errOut.String())
	}
	if got.Current != "0.1.2" || got.Latest != "0.3.0" || !got.UpdateAvailable {
		t.Fatalf("notice object wrong: %+v", got)
	}
}

// THE PAIRED HALF, and the one that keeps the advisory worth reading: a
// current install must produce NOTHING. A note that fires when there is
// nothing to do is a note people learn to ignore, and then the one that
// matters is ignored with it.
func TestWriteUpdateAdvisory_CurrentInstallSaysNothing(t *testing.T) {
	t.Parallel()
	for _, jsonOut := range []bool{false, true} {
		io, out, errOut := newIO()
		cli.WriteUpdateAdvisory(io, advisoryNotice(t, "0.3.0", "0.3.0"), jsonOut)
		if out.Len() != 0 || errOut.Len() != 0 {
			t.Fatalf("jsonOut=%v: an up-to-date install emitted stdout=%q stderr=%q", jsonOut, out.String(), errOut.String())
		}
	}
}

func TestWriteUpdateAdvisory_DisabledNoticeSaysNothing(t *testing.T) {
	t.Parallel()
	io, out, errOut := newIO()
	cli.WriteUpdateAdvisory(io, cache.UpdateNotice{Grade: release.GradeNone}, false)
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("a disabled notice emitted stdout=%q stderr=%q", out.String(), errOut.String())
	}
}
