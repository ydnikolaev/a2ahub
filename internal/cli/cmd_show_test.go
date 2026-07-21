package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestShowCommand_DigestMismatchWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	cliWriteArtifact(t, dir, "axon/requires/XR-axon-target.md", map[string]any{
		"schema": "envelope/v1", "id": "XR-axon-target", "type": "requirement", "title": "target",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": base.Format(time.RFC3339),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "target body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000010", cliEvt("XR-axon-target", "publish", "axon", base))

	referring := cliWR("XW-axon-20260701-ref", "referring", "axon", []string{"seomatrix"}, "p2", false)
	referring["refs"] = []map[string]any{{"ref": "XR-axon-target#sha256:deadbeef"}}
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-ref.md", referring, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000011", cliEvt("XW-axon-20260701-ref", "submit", "axon", base.Add(time.Hour)))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(2 * time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--json", "XW-axon-20260701-ref"}, io)
	if code != 0 {
		t.Fatalf("code = %d (must be 0 — V5 warning is never a hard error), stdout=%s", code, out.String())
	}
	var decoded struct {
		Warnings []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	found := false
	for _, w := range decoded.Warnings {
		if w.Code == "REF-004" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a REF-004 warning, got %+v", decoded.Warnings)
	}
}

func TestShowCommand_RefNotFoundError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon")
	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewShowCommand(store)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"XW-axon-nope"}, io)
	if code == 0 {
		t.Fatalf("code = 0, want non-zero for ref-not-found; stderr=%s", errOut.String())
	}
	if errOut.Len() == 0 {
		t.Fatal("expected an actionable error message on stderr")
	}
}

func TestShowCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewShowCommand(store)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), nil, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
