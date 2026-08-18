package spacenotify

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
)

func TestClassify_HumanGateWinsOverP1(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{
		Priority: "p1", Addressees: []string{"seomatrix"},
		Verdict: pendency.Verdict{HumanGate: "G3"},
	}
	if got := classify(fa); got != ClassHumanGate {
		t.Fatalf("classify = %q, want %q (an artifact that is both p1 and gate-pending gets ONE class, deterministically)", got, ClassHumanGate)
	}
}

func TestClassify_BlockingRequiresAnAddressee(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Priority: "p1"} // no Addressees, no Broadcast
	if got := classify(fa); got != ClassPublished {
		t.Fatalf("classify = %q, want %q (p1 with no receiving participant is not inbound)", got, ClassPublished)
	}
}

func TestClassify_BlockingFlag(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Blocking: true, Addressees: []string{"seomatrix"}}
	if got := classify(fa); got != ClassBlocking {
		t.Fatalf("classify = %q, want %q", got, ClassBlocking)
	}
}

func TestClassify_BroadcastCountsAsInbound(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Priority: "p1", Broadcast: true}
	if got := classify(fa); got != ClassBlocking {
		t.Fatalf("classify = %q, want %q", got, ClassBlocking)
	}
}

func TestClassify_Published(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Priority: "p3", Addressees: []string{"seomatrix"}}
	if got := classify(fa); got != ClassPublished {
		t.Fatalf("classify = %q, want %q", got, ClassPublished)
	}
}
