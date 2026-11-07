package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// fb-20261106-96837b: a note is transition-free, and the owed party writing
// one instead of the move it owes is the pattern that went unnoticed for a
// week. `a2a note` says so at the moment it happens — on stderr, beside the
// verb's other advisories, so the stdout success line stays one line.
func TestNoteHintsTheOwedTransitionToTheOwedParty(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-n001"
	seedAcceptedQuestion(t, mirrorDir, id, "beta")

	owed := cli.NewNoteCommand(&fakeLifecycleFunnel{}, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	if code := owed.Run(context.Background(), []string{"--note", "still working on it", id}, io); code != 0 {
		t.Fatalf("owed note code = %d, stderr=%s", code, errOut.String())
	}
	want := "note recorded; you owe respond on " + id + " — a note does not move it\n"
	if !strings.Contains(errOut.String(), want) {
		t.Fatalf("owed party's stderr = %q, want it to contain %q", errOut.String(), want)
	}
	if strings.Contains(out.String(), "you owe") {
		t.Fatalf("hint leaked onto stdout: %q", out.String())
	}

	// The asker is waiting, not owing: a note from them is just a note.
	asker := cli.NewNoteCommand(&fakeLifecycleFunnel{}, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	askerIO, _, askerErr := newIO()
	if code := asker.Run(context.Background(), []string{"--note", "any update?", id}, askerIO); code != 0 {
		t.Fatalf("asker note code = %d, stderr=%s", code, askerErr.String())
	}
	if strings.Contains(askerErr.String(), "you owe") {
		t.Fatalf("the waiting party was told it owes a move: %q", askerErr.String())
	}
}
