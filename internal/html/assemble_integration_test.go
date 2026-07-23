package html

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// TestAssemble_NodesAndContractEdges drives Assemble over a hand-built Store
// (a temp mirror with a manifest + a consumes.yaml) — deterministic, no
// network — and asserts the nodes, per-space health, and the derived
// contract-dependency edge.
func TestAssemble_NodesAndContractEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	consumes := "schema: consumes/v1\nsystem: axon\ndependencies:\n" +
		"  - contract: XC-seomatrix-feed-v1\n    major: 1\n    since: 2026-07-01\n"
	if err := os.WriteFile(filepath.Join(dir, "axon", "consumes.yaml"), []byte(consumes), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := space.Manifest{Space: "getvisa", Participants: []space.Participant{
		{System: "axon", Section: "axon/", Org: "r22d222", Status: "active", Owners: []string{"ydnikolaev"}},
		{System: "seomatrix", Section: "seomatrix/", Org: "r22d222", Status: "active", Owners: []string{"xpressmike"}},
	}}
	store := cache.NewStore("axon", t.TempDir(),
		[]cache.SpaceMirror{{SpaceID: "getvisa", Dir: dir, RepoURL: "https://github.com/r22d222/a2a", Manifest: manifest}},
		time.Now, 0)

	data, err := Assemble(context.Background(), store, "", time.Now())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(data.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2: %+v", len(data.Nodes), data.Nodes)
	}
	var axon *Node
	for i := range data.Nodes {
		if data.Nodes[i].System == "axon" {
			axon = &data.Nodes[i]
		}
	}
	if axon == nil || !axon.Self || axon.Org != "r22d222" {
		t.Fatalf("axon self node wrong: %+v", axon)
	}

	if len(data.Spaces) != 1 || data.Spaces[0].ParticipantCount != 2 || !data.Spaces[0].Readable {
		t.Fatalf("space health: %+v", data.Spaces)
	}

	if len(data.ContractEdges) != 1 {
		t.Fatalf("contract edges = %d, want 1: %+v", len(data.ContractEdges), data.ContractEdges)
	}
	e := data.ContractEdges[0]
	if e.From != "axon" || e.To != "seomatrix" || e.Contract != "XC-seomatrix-feed-v1" ||
		e.PinnedMajor != 1 || e.Drift != "dangling" || e.Space != "getvisa" {
		t.Fatalf("contract edge wrong: %+v", e)
	}

	if data.Self != "axon" {
		t.Fatalf("self = %q, want axon", data.Self)
	}
}
