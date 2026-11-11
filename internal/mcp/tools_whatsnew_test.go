package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ydnikolaev/go-foundation/units/adoption"
)

func testWhatsnewCorpus() []adoption.Release {
	return []adoption.Release{
		{Version: "0.2.0", Released: "2026-01-01", Headline: "H2", Changes: []adoption.Change{
			{ID: "A", Kind: "feat", Impact: "low", Subject: "s2", Detail: "d2", Action: adoption.Action{Scope: "none", Why: "w2"}},
		}},
		{Version: "0.3.0", Released: "2026-02-01", Headline: "H3", Changes: []adoption.Change{
			{ID: "B", Kind: "fix", Impact: "high", Subject: "s3", Detail: "d3", Action: adoption.Action{Scope: "space", Why: "w3"}},
		}},
	}
}

func noCurrentIssues() ([]adoption.Change, error) { return nil, nil }

func TestWhatsnewHandlerSinceFiltersUnboundedAbove(t *testing.T) {
	t.Parallel()
	handler := newWhatsnewHandler(func() ([]adoption.Release, error) { return testWhatsnewCorpus(), nil }, noCurrentIssues)

	args, _ := json.Marshal(WhatsnewInput{Since: "0.2.0"})
	result, body, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	if body != "" {
		t.Fatalf("expected no body block, got %q", body)
	}
	slice, ok := result.([]adoption.Release)
	if !ok || len(slice) != 1 || slice[0].Version != "0.3.0" {
		t.Fatalf("expected [0.3.0], got %#v", result)
	}
}

func TestWhatsnewHandlerNoSinceReturnsNewestOnly(t *testing.T) {
	t.Parallel()
	handler := newWhatsnewHandler(func() ([]adoption.Release, error) { return testWhatsnewCorpus(), nil }, noCurrentIssues)

	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	slice, ok := result.([]adoption.Release)
	if !ok || len(slice) != 1 || slice[0].Version != "0.3.0" {
		t.Fatalf("expected the single newest entry [0.3.0], got %#v", result)
	}
}

func TestWhatsnewHandlerCurrentIssuesSurviveEmptySinceRange(t *testing.T) {
	t.Parallel()
	handler := newWhatsnewHandler(
		func() ([]adoption.Release, error) { return testWhatsnewCorpus(), nil },
		func() ([]adoption.Change, error) {
			return []adoption.Change{{
				ID: "KI-MACOS-ADHOC-SIGNING", Kind: adoption.KindKnownIssue,
				Impact: "normal", Subject: "macOS may ask once", Detail: "ad-hoc signed",
				Action: adoption.Action{Scope: "none", Why: "approve only the verified release"},
			}}, nil
		},
	)

	args, _ := json.Marshal(WhatsnewInput{Since: "0.3.0"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	slice, ok := result.([]adoption.Release)
	if !ok || len(slice) != 1 || slice[0].Version != "0.3.0" ||
		len(slice[0].Changes) != 1 || slice[0].Changes[0].ID != "KI-MACOS-ADHOC-SIGNING" {
		t.Fatalf("standing issue disappeared from MCP empty range: %#v", result)
	}
}

func TestWhatsnewHandlerNoArgsAtAll(t *testing.T) {
	t.Parallel()
	handler := newWhatsnewHandler(func() ([]adoption.Release, error) { return testWhatsnewCorpus(), nil }, noCurrentIssues)

	result, _, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	slice, ok := result.([]adoption.Release)
	if !ok || len(slice) != 1 || slice[0].Version != "0.3.0" {
		t.Fatalf("expected the single newest entry [0.3.0] with nil args, got %#v", result)
	}
}

func TestWhatsnewHandlerEmptyCorpusNoSince(t *testing.T) {
	t.Parallel()
	handler := newWhatsnewHandler(func() ([]adoption.Release, error) { return nil, nil }, noCurrentIssues)

	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	slice, ok := result.([]adoption.Release)
	if !ok || len(slice) != 0 {
		t.Fatalf("expected an empty (non-nil) slice, got %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("expected a bare [] for the empty case, got %q", encoded)
	}
}

func TestWhatsnewHandlerLoadError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("corpus load boom")
	handler := newWhatsnewHandler(func() ([]adoption.Release, error) { return nil, wantErr }, noCurrentIssues)

	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestWhatsnewHandlerInvalidJSONInput(t *testing.T) {
	t.Parallel()
	handler := newWhatsnewHandler(func() ([]adoption.Release, error) { return testWhatsnewCorpus(), nil }, noCurrentIssues)

	_, _, err := handler(context.Background(), json.RawMessage(`{not json`))
	if err == nil {
		t.Fatal("expected an invalid-input error")
	}
}
