package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/skill"
)

// docsToolFixtureManifest mirrors internal/cli/cmd_docs_test.go's fixture:
// two groups, four sections, one (fixture-missing-page) naming a file the
// fixture tree deliberately omits (§6 edge case). IDs are synthetic
// (fixture-*), not real corpus topic ids, so AC-4's own literal-section-id
// grep under internal/ and cmd/ finds no roster added by this test.
const docsToolFixtureManifest = `{
  "schema": "a2a-docs-manifest/v1",
  "groups": ["Start", "Concepts"],
  "loop_corpus": ["a2ahub/loops.md"],
  "sections": [
    {"id": "fixture-start-a", "group": "Start", "title": "Fixture Start A", "file": "a2ahub/onboarding.md"},
    {"id": "fixture-start-b", "group": "Start", "title": "Fixture Start B", "file": "a2ahub/overview.md"},
    {"id": "fixture-concept-a", "group": "Concepts", "title": "Fixture Concept A", "file": "a2ahub/loops.md"},
    {"id": "fixture-missing-page", "group": "Concepts", "title": "Fixture Missing Page", "file": "a2ahub/missing.md"}
  ]
}`

func docsToolFixtureTree(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		skill.DocsManifestPath: {Data: []byte(docsToolFixtureManifest)},
		"a2ahub/onboarding.md": {Data: []byte("Fixture start A body.\n")},
		"a2ahub/overview.md":   {Data: []byte("Fixture start B body.\n")},
		"a2ahub/loops.md":      {Data: []byte("Fixture concept A body.\n")},
		// a2ahub/missing.md is deliberately absent.
	}
}

func TestDocsHandlerNoTopicListsVocabulary(t *testing.T) {
	t.Parallel()
	handler := newDocsHandler(docsToolFixtureTree(t))

	result, body, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if body != "" {
		t.Fatalf("expected no body block for a listing, got %q", body)
	}
	topics, ok := result.([]DocsTopic)
	if !ok {
		t.Fatalf("result = %#v, want []DocsTopic", result)
	}
	if len(topics) != 4 {
		t.Fatalf("len(topics) = %d, want 4", len(topics))
	}
	if topics[0].ID != "fixture-start-a" || topics[0].Group != "Start" || topics[0].Title != "Fixture Start A" {
		t.Errorf("topics[0] = %+v, want the manifest's first section", topics[0])
	}
}

func TestDocsHandlerKnownTopicReturnsPageAsBody(t *testing.T) {
	t.Parallel()
	handler := newDocsHandler(docsToolFixtureTree(t))

	args, _ := json.Marshal(DocsInput{Topic: "fixture-start-b"})
	result, body, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if body != "Fixture start B body.\n" {
		t.Fatalf("body = %q, want the fixture page verbatim", body)
	}
	topic, ok := result.(DocsTopic)
	if !ok || topic.ID != "fixture-start-b" {
		t.Fatalf("result = %#v, want DocsTopic{ID: fixture-start-b}", result)
	}
}

func TestDocsHandlerUnknownTopicRefusesNamingValidIDs(t *testing.T) {
	t.Parallel()
	handler := newDocsHandler(docsToolFixtureTree(t))

	args, _ := json.Marshal(DocsInput{Topic: "nope"})
	result, body, err := handler(context.Background(), args)
	if err == nil {
		t.Fatalf("handler succeeded with result=%#v body=%q, want a refusal", result, body)
	}
	for _, id := range []string{"fixture-start-a", "fixture-start-b", "fixture-concept-a", "fixture-missing-page"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("refusal %q does not name valid id %q", err, id)
		}
	}
}

func TestDocsHandlerMissingPageRefusesNamingSectionAndAbsentPage(t *testing.T) {
	t.Parallel()
	handler := newDocsHandler(docsToolFixtureTree(t))

	args, _ := json.Marshal(DocsInput{Topic: "fixture-missing-page"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal for a section whose page is missing, got none")
	}
	if !strings.Contains(err.Error(), "fixture-missing-page") {
		t.Errorf("refusal %q does not name the section id", err)
	}
	if !strings.Contains(err.Error(), "a2ahub/missing.md") {
		t.Errorf("refusal %q does not name the absent page path", err)
	}
}

// TestDocsHandlerRejectsUnknownField proves a2a_docs goes through
// decodeStrict like every other tool input site (spec 04 D4).
func TestDocsHandlerRejectsUnknownField(t *testing.T) {
	t.Parallel()
	handler := newDocsHandler(docsToolFixtureTree(t))

	_, _, err := handler(context.Background(), json.RawMessage(`{"topic":"fixture-start-b","bogus":true}`))
	if err == nil {
		t.Fatal("expected a decode refusal for an unknown field, got none")
	}
}

// TestDocsHandlerAddingManifestSectionAddsTopicWithNoCodeChange is AC-5 on
// the MCP surface: a section added to the manifest becomes a topic with no
// code change to this file.
func TestDocsHandlerAddingManifestSectionAddsTopicWithNoCodeChange(t *testing.T) {
	t.Parallel()
	base := docsToolFixtureTree(t)
	extended := fstest.MapFS{}
	for k, v := range base {
		extended[k] = v
	}
	extendedManifest := strings.Replace(
		docsToolFixtureManifest,
		`{"id": "fixture-missing-page", "group": "Concepts", "title": "Fixture Missing Page", "file": "a2ahub/missing.md"}`,
		`{"id": "fixture-missing-page", "group": "Concepts", "title": "Fixture Missing Page", "file": "a2ahub/missing.md"},
                    {"id": "invented-topic", "group": "Concepts", "title": "Invented", "file": "a2ahub/invented.md"}`,
		1,
	)
	if extendedManifest == docsToolFixtureManifest {
		t.Fatal("could not seed the extra section: the anchor string did not match")
	}
	extended[skill.DocsManifestPath] = &fstest.MapFile{Data: []byte(extendedManifest)}
	extended["a2ahub/invented.md"] = &fstest.MapFile{Data: []byte("Invented body.\n")}

	handler := newDocsHandler(extended)
	args, _ := json.Marshal(DocsInput{Topic: "invented-topic"})
	result, body, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if body != "Invented body.\n" {
		t.Fatalf("body = %q, want the invented page verbatim", body)
	}
	topic, ok := result.(DocsTopic)
	if !ok || topic.ID != "invented-topic" {
		t.Fatalf("result = %#v, want DocsTopic{ID: invented-topic}", result)
	}
}
