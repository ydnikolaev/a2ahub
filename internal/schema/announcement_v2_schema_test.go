package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/ydnikolaev/a2ahub/schemas"
)

func TestAnnouncementV2WorkGoldenFixtures(t *testing.T) {
	t.Parallel()
	sch, baseProps := loadAnnouncementV2Schema(t)
	root := filepath.Join(corpusRoot, "envelope/v2/fixtures")

	wantModes := map[string]bool{
		"planning":     false,
		"implementing": false,
		"testing":      false,
		"reviewing":    false,
		"waiting":      false,
		"paused":       false,
		"finished":     false,
	}
	validPaths, err := filepath.Glob(filepath.Join(root, "valid/XA-*.md"))
	if err != nil {
		t.Fatalf("glob valid announcement/v2 fixtures: %v", err)
	}
	if len(validPaths) < len(wantModes)+1 {
		t.Fatalf("valid announcement/v2 fixture count = %d, want at least %d modes plus one legacy status feed", len(validPaths), len(wantModes)+1)
	}
	statusFeedFound := false
	for _, path := range validPaths {
		path := path
		name := filepath.Base(path)
		if strings.Contains(name, "status-feed") {
			statusFeedFound = true
		}
		for mode := range wantModes {
			if strings.Contains(name, "work-"+mode) {
				wantModes[mode] = true
			}
		}
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			instance := readEnvelopeFixture(t, path)
			if err := sch.Validate(instance); err != nil {
				t.Fatalf("expected valid announcement/v2 fixture: %v", err)
			}
		})
	}
	for mode, found := range wantModes {
		if !found {
			t.Errorf("missing valid work mode fixture %q", mode)
		}
	}
	if !statusFeedFound {
		t.Error("missing ordinary status-feed compatibility fixture")
	}

	invalidPaths, err := filepath.Glob(filepath.Join(root, "invalid/XA-*-work-*.md"))
	if err != nil {
		t.Fatalf("glob invalid announcement/v2 fixtures: %v", err)
	}
	if len(invalidPaths) == 0 {
		t.Fatal("expected invalid announcement/v2 fixtures")
	}
	for _, path := range invalidPaths {
		path := path
		t.Run("invalid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat(path + ".expect.yaml"); err != nil {
				t.Fatalf("invalid fixture has no expectation sidecar: %v", err)
			}
			err := sch.Validate(readEnvelopeFixture(t, path))
			if err == nil {
				t.Fatal("expected announcement/v2 fixture rejection")
			}
			violations := extractFieldViolations(err, baseProps)
			if len(violations) != 1 {
				t.Fatalf("fixture must carry exactly one schema fault, got %+v", violations)
			}
		})
	}
}

func TestAnnouncementV2WorkShapeIsClosed(t *testing.T) {
	t.Parallel()
	doc, err := readJSON("envelope/v2/announcement.schema.json")
	if err != nil {
		t.Fatalf("read announcement/v2 schema: %v", err)
	}
	root := doc.(map[string]any)
	defs := root["$defs"].(map[string]any)
	work := defs["workCheckpoint"].(map[string]any)
	properties := work["properties"].(map[string]any)
	want := map[string]bool{
		"id": true, "semantic_sequence": true, "mode": true,
		"subject_ref": true, "summary": true, "reported_at": true,
		"valid_until": true, "waiting_on": true,
	}
	if len(properties) != len(want) {
		t.Fatalf("work property count = %d, want %d: %v", len(properties), len(want), properties)
	}
	for name := range properties {
		if !want[name] {
			t.Errorf("unexpected durable work property %q", name)
		}
	}
	for _, forbidden := range []string{"operation_key", "operation_id", "artifact_id", "event_id", "pending"} {
		if _, ok := properties[forbidden]; ok {
			t.Errorf("private/local property %q entered durable work schema", forbidden)
		}
	}
}

func TestAnnouncementV2WorkTemplateContract(t *testing.T) {
	t.Parallel()
	raw, err := schemas.FS.ReadFile("templates/v2/announcement.md")
	if err != nil {
		t.Fatalf("read embedded announcement/v2 template: %v", err)
	}
	for _, want := range []string{
		"schema: envelope/v2", "category: status", "ack_requested: false",
		"semantic_sequence:", "mode:", "subject_ref:", "valid_until:",
		"waiting_on:",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("embedded announcement/v2 template does not contain %q", want)
		}
	}
}

func loadAnnouncementV2Schema(t *testing.T) (*jsonschema.Schema, map[string]bool) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	base, err := readJSON("envelope/v2/base.schema.json")
	if err != nil {
		t.Fatalf("read envelope/v2 base: %v", err)
	}
	if err := compiler.AddResource(resourceURLPrefix+"envelope/v2/base.schema.json", base); err != nil {
		t.Fatalf("add envelope/v2 base: %v", err)
	}
	announcement, err := readJSON("envelope/v2/announcement.schema.json")
	if err != nil {
		t.Fatalf("read announcement/v2 schema: %v", err)
	}
	const seed = resourceURLPrefix + "announcement-v2-test"
	if err := compiler.AddResource(seed, announcement); err != nil {
		t.Fatalf("add announcement/v2 schema: %v", err)
	}
	sch, err := compiler.Compile(seed)
	if err != nil {
		t.Fatalf("compile announcement/v2 schema: %v", err)
	}
	return sch, topLevelProperties(base)
}
