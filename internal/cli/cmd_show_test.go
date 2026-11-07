package cli_test

import (
	"context"
	"encoding/json"
	"strings"
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
	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, time.Now, 0)
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

// TestShowCommand_FlagAfterPositional is Wave K's exact live-run 6 repro:
// `a2a show <id> --json` used to exit 2 with "usage: a2a show <ref>",
// because Go's flag package stops parsing at the first non-flag token, so
// `--json` was counted as a SECOND positional and `fs.NArg() != 1`.
//
// TEETH: reverting ShowCommand.Run's parseArgsAnyOrder call (cmd_show.go)
// back to a bare `fs.Parse(args)` reds this with exactly that usage error.
func TestShowCommand_FlagAfterPositional(t *testing.T) {
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
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000012", cliEvt("XR-axon-target", "publish", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"XR-axon-target", "--json"}, io)
	if code != 0 {
		t.Fatalf("show <id> --json: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out.String())
	}
	if decoded.ID != "XR-axon-target" {
		t.Fatalf("got id %q, want XR-axon-target", decoded.ID)
	}
}

func TestShowCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	cmd := cli.NewShowCommand(store)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), nil, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

// TestShowCommand_PrintsAcceptanceCriteriaAndExpectedResponse: both the human
// and --json outputs carry the artifact's acceptance_criteria[] and
// expected_response (fb-20261107-37ce55); an artifact without them is unchanged.
func TestShowCommand_PrintsAcceptanceCriteriaAndExpectedResponse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	wr := cliWR("XW-axon-20260701-crit", "with criteria", "axon", []string{"seomatrix"}, "p2", false)
	wr["schema"] = "envelope/v2"
	wr["category"] = "data"
	wr["acceptance_criteria"] = []map[string]any{{"id": "c1", "text": "first thing"}, {"id": "c2", "text": "second thing"}}
	wr["expected_response"] = map[string]any{"shape": "verdict"}
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-crit.md", wr, "the body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000020", cliEvt("XW-axon-20260701-crit", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewShowCommand(store)

	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"XW-axon-20260701-crit"}, io); code != 0 {
		t.Fatalf("code = %d, out=%s", code, out.String())
	}
	for _, want := range []string{"Acceptance criteria:\n  c1: first thing\n  c2: second thing\n", "Expected response: shape=verdict\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("human output lacks %q:\n%s", want, out.String())
		}
	}

	io, out, _ = newIO()
	if code := cmd.Run(context.Background(), []string{"--json", "XW-axon-20260701-crit"}, io); code != 0 {
		t.Fatalf("json code = %d", code)
	}
	var decoded struct {
		Criteria []struct{ ID, Text string } `json:"acceptance_criteria"`
		Expected struct{ Shape string }      `json:"expected_response"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(decoded.Criteria) != 2 || decoded.Criteria[0].ID != "c1" || decoded.Criteria[1].Text != "second thing" || decoded.Expected.Shape != "verdict" {
		t.Fatalf("json = %+v", decoded)
	}
}
