package cli_test

// Shared test-only fixture builders for the P7 read-verb tests
// (cmd_inbox/outbox/show/thread/search/statusline_test.go). Unlike
// internal/cache's own tests, buildIndex needs no real git history —
// it walks plain files on disk — so these helpers write a bare tree
// (no `git init`), which keeps these CLI-layer tests focused on flag
// parsing / exit codes / JSON shape (the condition logic itself is
// internal/cache's own, already covered there).

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

func cliWriteManifest(t *testing.T, dir string, systems ...string) space.Manifest {
	t.Helper()
	type participant struct {
		System  string   `yaml:"system"`
		Org     string   `yaml:"org"`
		Section string   `yaml:"section"`
		Owners  []string `yaml:"owners"`
		Status  string   `yaml:"status"`
		Joined  string   `yaml:"joined"`
	}
	type manifest struct {
		Schema           string        `yaml:"schema"`
		Space            string        `yaml:"space"`
		MinBinaryVersion string        `yaml:"min_binary_version"`
		Participants     []participant `yaml:"participants"`
	}
	m := manifest{Schema: "space/v1", Space: "fixture-space", MinBinaryVersion: "0.0.0"}
	for _, s := range systems {
		m.Participants = append(m.Participants, participant{
			System: s, Org: s + "-org", Section: s, Owners: []string{s + "-human"}, Status: "active", Joined: "2026-01-01",
		})
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("cliWriteManifest: marshal: %v", err)
	}
	parsed, err := space.ParseManifest(raw)
	if err != nil {
		t.Fatalf("cliWriteManifest: parse: %v", err)
	}
	return parsed
}

func cliWriteArtifact(t *testing.T, dir, relPath string, fields map[string]any, body string) {
	t.Helper()
	raw, err := yaml.Marshal(fields)
	if err != nil {
		t.Fatalf("cliWriteArtifact: marshal: %v", err)
	}
	full := "---\n" + string(raw) + "---\n" + body
	path := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("cliWriteArtifact: mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatalf("cliWriteArtifact: write: %v", err)
	}
}

func cliWriteEvent(t *testing.T, dir, system, ulid string, fields map[string]any) {
	t.Helper()
	base := map[string]any{"schema": "event/v1", "event": ulid, "space": "fixture-space"}
	for k, v := range fields {
		base[k] = v
	}
	raw, err := yaml.Marshal(base)
	if err != nil {
		t.Fatalf("cliWriteEvent: marshal: %v", err)
	}
	path := filepath.Join(dir, system, "events", "2026", ulid+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("cliWriteEvent: mkdir: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("cliWriteEvent: write: %v", err)
	}
}

func cliEvt(subject, transition, actorSystem string, at time.Time) map[string]any {
	return map[string]any{
		"subject": subject, "transition": transition,
		"actor": map[string]any{"kind": "agent", "name": actorSystem + "-bot", "system": actorSystem},
		"at":    at.UTC().Format(time.RFC3339),
	}
}

func cliWR(id, title, from string, to []string, priority string, blocking bool) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "work_request", "title": title,
		"space": "fixture-space", "from": from, "to": to,
		"actor":   map[string]any{"kind": "agent", "name": from + "-bot"},
		"created": time.Now().UTC().Format(time.RFC3339), "priority": priority, "blocking": blocking,
		"classification": "internal",
	}
}
