package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func testAdaptCorpus() []notes.ReleaseNotes {
	return []notes.ReleaseNotes{
		{Version: "0.1.0", Released: "2026-01-01", Headline: "H1", Changes: []notes.Change{
			{ID: "A", Kind: "feat", Impact: "low", Subject: "local one", Detail: "d1",
				Action: notes.Action{Scope: "local", Why: "w1", Run: []string{"echo hi"}}},
		}},
		{Version: "0.2.0", Released: "2026-02-01", Headline: "H2", Changes: []notes.Change{
			{ID: "B", Kind: "fix", Impact: "high", Subject: "space one", Detail: "d2",
				Action: notes.Action{Scope: "space", Why: "w2"}},
			{ID: "C", Kind: "fix", Impact: "normal", Subject: "not obliging", Detail: "d3",
				Action: notes.Action{Scope: "none", Why: "w3"}},
		}},
	}
}

func noAdaptCurrentIssues() ([]notes.Change, error) { return nil, nil }

func fakeAdaptProjectConfig(baseline string) func(string) (space.ProjectConfig, error) {
	return func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{AdaptedThrough: baseline}, nil
	}
}

func TestAdaptHandlerReturnsPendingProjectionSortedByGroup(t *testing.T) {
	t.Parallel()
	handler := newAdaptHandler(
		func() ([]notes.ReleaseNotes, error) { return testAdaptCorpus(), nil },
		noAdaptCurrentIssues,
		fakeAdaptProjectConfig(""),
		"0.2.0",
		"unused-path",
	)

	result, body, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if body != "" {
		t.Fatalf("expected no body block, got %q", body)
	}
	out, ok := result.(AdaptOutput)
	if !ok {
		t.Fatalf("expected AdaptOutput, got %#v", result)
	}
	if !out.StartedFromOldest || out.Oldest != "0.1.0" {
		t.Fatalf("expected StartedFromOldest from v0.1.0, got %#v", out)
	}
	if out.Count != 2 || len(out.Pending) != 2 {
		t.Fatalf("expected 2 obligations (scope:none excluded), got %#v", out)
	}
	// GroupLocalWithRun (0) sorts before GroupSpace (2).
	if out.Pending[0].Change.ID != "A" || out.Pending[1].Change.ID != "B" {
		t.Fatalf("expected [A, B] group order, got [%s, %s]", out.Pending[0].Change.ID, out.Pending[1].Change.ID)
	}
	if out.Pending[0].Change.Action.Scope != "local" || out.Pending[1].Change.Action.Scope != "space" {
		t.Fatalf("expected each item's own scope preserved, got %#v", out.Pending)
	}
}

func TestAdaptHandlerBaselineAheadOfBinaryRefusesNamingBothClocks(t *testing.T) {
	t.Parallel()
	handler := newAdaptHandler(
		func() ([]notes.ReleaseNotes, error) { return testAdaptCorpus(), nil },
		noAdaptCurrentIssues,
		fakeAdaptProjectConfig("0.5.0"),
		"0.2.0",
		"unused-path",
	)

	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "0.5.0") || !strings.Contains(err.Error(), "0.2.0") {
		t.Fatalf("expected the error to name both the baseline and the binary version, got %q", err)
	}
}

func TestAdaptHandlerUnusableBinaryVersionRefusesRatherThanEmptyList(t *testing.T) {
	t.Parallel()
	handler := newAdaptHandler(
		func() ([]notes.ReleaseNotes, error) { return testAdaptCorpus(), nil },
		noAdaptCurrentIssues,
		fakeAdaptProjectConfig(""),
		"dev",
		"unused-path",
	)

	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected a refusal naming the unusable binary version, not a silent empty list")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Fatalf("expected the error to name the unusable binary version, got %q", err)
	}
}

func TestAdaptHandlerLoadErrorPropagates(t *testing.T) {
	t.Parallel()
	wantErr := "corpus load boom"
	handler := newAdaptHandler(
		func() ([]notes.ReleaseNotes, error) { return nil, &testAdaptLoadError{msg: wantErr} },
		noAdaptCurrentIssues,
		fakeAdaptProjectConfig(""),
		"0.2.0",
		"unused-path",
	)

	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("expected the load error to propagate, got %v", err)
	}
}

type testAdaptLoadError struct{ msg string }

func (e *testAdaptLoadError) Error() string { return e.msg }

func TestAdaptHandlerRejectsUnknownField(t *testing.T) {
	t.Parallel()
	handler := newAdaptHandler(
		func() ([]notes.ReleaseNotes, error) { return testAdaptCorpus(), nil },
		noAdaptCurrentIssues,
		fakeAdaptProjectConfig(""),
		"0.2.0",
		"unused-path",
	)

	_, _, err := handler(context.Background(), json.RawMessage(`{"done":true}`))
	if err == nil {
		t.Fatal("expected the closed schema to refuse an unknown field (e.g. the CLI's --done, which is not exposed here)")
	}
}

func TestAdaptToolInputSchemaIsClosedAndExposesNoRootSelector(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	registerAdaptTool(registry, AdaptDeps{BinaryVersion: "0.2.0", ProjectConfigPath: "/some/project/.a2a/config.yaml"})
	names := registry.ToolNames()
	if len(names) != 1 || names[0] != "a2a_adapt" {
		t.Fatalf("expected exactly one registered tool named a2a_adapt, got %v", names)
	}

	spec, ok := registry.Get("a2a_adapt")
	if !ok {
		t.Fatal("a2a_adapt not found in registry")
	}
	var schema struct {
		Type                 string         `json:"type"`
		AdditionalProperties bool           `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("a2a_adapt schema must be closed")
	}
	for _, forbidden := range []string{"root", "project_root", "cwd", "path", "done"} {
		if _, ok := schema.Properties[forbidden]; ok {
			t.Fatalf("schema exposes forbidden/unavailable selector %q", forbidden)
		}
	}
}
