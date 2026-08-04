package livee2e

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSSEStreamKeepsBufferedEventsAcrossBlocks(t *testing.T) {
	t.Parallel()
	// The exact LE-OC-03 shape: the caller was busy landing pull requests, so
	// by the time it reads, a dozen keepalives and the revision event are all
	// sitting in one buffer. A reader rebuilt per block dropped everything the
	// previous one had already pulled off the socket, losing the very event
	// the scenario exists to observe.
	raw := strings.Repeat(": keepalive\n\n", 12) +
		"event: revision\nid: sha256:two\ndata: {\"revision\":\"sha256:two\"}\n\n"
	observed, err := newSSEStream(strings.NewReader(raw)).readUntilRevision()
	if err != nil {
		t.Fatalf("readUntilRevision() error = %v, observed = %q", err, observed)
	}
	if !strings.Contains(observed, "event: revision") {
		t.Fatalf("revision event was dropped: %q", observed)
	}
	if got := strings.Count(observed, ": keepalive"); got != 12 {
		t.Fatalf("kept %d of 12 leading keepalives: %q", got, observed)
	}
}

func TestSSEStreamRefusesProjectionRowsAndSurfacesTruncation(t *testing.T) {
	t.Parallel()
	rows := "event: revision\nid: sha256:two\ndata: {\"timeline\":[]}\n\n"
	observed, err := newSSEStream(strings.NewReader(rows)).readUntilRevision()
	if !errors.Is(err, ErrSSEExposedSnapshotRows) {
		t.Fatalf("projection rows error = %v, observed = %q", err, observed)
	}
	truncated := ": keepalive\n\n: keepal"
	observed, err = newSSEStream(strings.NewReader(truncated)).readUntilRevision()
	if !errors.Is(err, io.EOF) || !strings.HasPrefix(observed, ": keepalive") {
		t.Fatalf("truncated stream error = %v, observed = %q", err, observed)
	}
}

func TestSSEStreamBoundsTheWholeStream(t *testing.T) {
	t.Parallel()
	// The bound is the stream's, not one block's: an endless keepalive feed
	// must terminate the read rather than grow without limit.
	endless := strings.NewReader(strings.Repeat(": keepalive\n\n", maxSSEStreamBytes))
	observed, err := newSSEStream(endless).readUntilRevision()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("unbounded stream error = %v", err)
	}
	if len(observed) > maxSSEStreamBytes {
		t.Fatalf("observed %d bytes, want at most the %d-byte stream bound", len(observed), maxSSEStreamBytes)
	}
}
