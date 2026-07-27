package cache

import (
	"context"
	"testing"
	"time"
)

func TestProtocolFlags_ProjectsCommittedFoldViolation(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t,
		fixtureParticipant{System: "axon"},
		fixtureParticipant{System: "seomatrix"},
		fixtureParticipant{System: "outsider"},
	)
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	const subject = "XW-axon-20260701-work"
	fx.commitArtifact("axon/exchanges/"+subject+".md",
		wr(subject, "Need a crawl result", "axon", []string{"seomatrix"}, "p2", false),
		"Please return the crawl result.")
	submit := evt(subject, "submit", "axon", base)
	fx.commitEvent("axon", fxULID(930), submit)
	unauthorized := evt(subject, "acknowledge", "outsider", base.Add(time.Hour))
	fx.commitEvent("outsider", fxULID(931), unauthorized)

	store := NewStore("axon", t.TempDir(),
		[]SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		func() time.Time { return base.Add(2 * time.Hour) }, 0)
	got, err := store.ProtocolFlags(context.Background())
	if err != nil {
		t.Fatalf("ProtocolFlags: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("flags = %+v, want one committed violation", got)
	}
	flag := got[0]
	if flag.Space != "sp1" || flag.System != "outsider" || flag.Artifact != subject ||
		flag.Code != "unauthorized-actor" || flag.EventULID != fxULID(931) ||
		flag.Severity != "attention" {
		t.Fatalf("flag projection wrong: %+v", flag)
	}
}
