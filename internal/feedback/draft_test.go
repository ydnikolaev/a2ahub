package feedback

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDraft_AutoFillsAndWritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	draftsDir := filepath.Join(dir, ".a2a", "feedback")
	d := NewDrafter(draftsDir, "v0.1.1")
	d.SetClockForTest(func() time.Time { return time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC) })
	d.SetEntropyForTest(bytes.NewReader([]byte{0x1a, 0x2b, 0x3c}))

	path, err := d.Draft("bug", "sync reports clean but mirror is stale")
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if filepath.Dir(path) != draftsDir {
		t.Fatalf("Draft path = %q, want dir %q", path, draftsDir)
	}
	if filepath.Base(path) != "fb-20260723-1a2b3c.yaml" {
		t.Fatalf("Draft path base = %q, want fb-20260723-1a2b3c.yaml", filepath.Base(path))
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read drafted file: %v", err)
	}

	var doc struct {
		Feedback string `yaml:"feedback"`
		ID       string `yaml:"id"`
		Kind     string `yaml:"kind"`
		Title    string `yaml:"title"`
		Context  struct {
			A2AVersion string `yaml:"a2a_version"`
			OSArch     string `yaml:"os_arch"`
			Surface    string `yaml:"surface"`
		} `yaml:"context"`
		Checks map[string]bool `yaml:"checks"`
		Status string          `yaml:"status"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode drafted file: %v", err)
	}
	if doc.Feedback != "v1" {
		t.Errorf("feedback = %q, want v1", doc.Feedback)
	}
	if doc.ID != "fb-20260723-1a2b3c" {
		t.Errorf("id = %q, want fb-20260723-1a2b3c", doc.ID)
	}
	if doc.Kind != "bug" {
		t.Errorf("kind = %q, want bug", doc.Kind)
	}
	if doc.Title != "sync reports clean but mirror is stale" {
		t.Errorf("title = %q", doc.Title)
	}
	if doc.Context.A2AVersion != "v0.1.1" {
		t.Errorf("context.a2a_version = %q, want v0.1.1", doc.Context.A2AVersion)
	}
	if doc.Context.OSArch == "" {
		t.Errorf("context.os_arch is empty, want GOOS/GOARCH")
	}
	for gate, v := range doc.Checks {
		if v {
			t.Errorf("checks.%s = true, want false (agent flips consciously, I5)", gate)
		}
	}
	if len(doc.Checks) != 5 {
		t.Errorf("len(checks) = %d, want 5", len(doc.Checks))
	}
	if doc.Status != "new" {
		t.Errorf("status = %q, want new", doc.Status)
	}
}

func TestDraft_UnknownKindRejected(t *testing.T) {
	t.Parallel()
	d := NewDrafter(t.TempDir(), "v0.1.1")
	if _, err := d.Draft("wontfix", "x"); err == nil {
		t.Fatal("expected an error for an unknown kind, got nil")
	}
}

func TestDraft_NoTitleLeavesPlaceholder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	d := NewDrafter(dir, "v0.1.1")
	path, err := d.Draft("docs", "")
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Title string `yaml:"title"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Title != "" {
		t.Errorf("title = %q, want empty placeholder when --title omitted", doc.Title)
	}
}
