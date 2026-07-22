package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// dispatchTestRegistry builds a full grouped registry with a REAL empty
// store (nil would panic in the read handlers, which deref store before any
// required-field guard) plus zero-value write deps (every write handler
// early-returns on a missing required field before touching its deps).
func dispatchTestRegistry(t *testing.T) *Registry {
	t.Helper()
	mirrorDir := t.TempDir()
	store := cache.NewStore("beta", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	write := testWriteDeps(mirrorDir, &fakeFunnel{})
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	newDeps := testNewDeps(t.TempDir())
	return BuildRegistry(store, write, mirrorDir, legality, newDeps)
}

// callTool invokes a grouped tool with the given discriminator injected
// alongside no other fields, returning only the handler error.
func callTool(t *testing.T, r *Registry, tool, discKey, discVal string) error {
	t.Helper()
	spec, ok := r.Get(tool)
	if !ok {
		t.Fatalf("tool %q not registered", tool)
	}
	args := json.RawMessage(`{"` + discKey + `":"` + discVal + `"}`)
	_, _, err := spec.Handler(context.Background(), args)
	return err
}

// TestDispatchEnumValuesReachAHandler proves every enum value on every
// grouped tool dispatches to a real per-verb handler (not the "unknown"
// branch, not a panic). A valid discriminator with no other fields still
// surfaces the sub-handler's OWN required-field error (or, for the read
// list-views, an empty-list success) — what matters is it is NOT the
// dispatch layer's "unknown" error.
func TestDispatchEnumValuesReachAHandler(t *testing.T) {
	t.Parallel()
	r := dispatchTestRegistry(t)

	cases := []struct {
		tool    string
		discKey string
		enum    []string
	}{
		{"a2a_read", "view", ReadViews},
		{"a2a_lifecycle", "action", LifecycleActions},
		{"a2a_exchange", "action", ExchangeActions},
		{"a2a_contract", "action", ContractActions},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			for _, val := range tc.enum {
				err := callTool(t, r, tc.tool, tc.discKey, val)
				if err != nil && strings.Contains(err.Error(), "unknown "+tc.discKey) {
					t.Errorf("%s %s=%q reached the unknown-discriminator branch (no handler wired): %v", tc.tool, tc.discKey, val, err)
				}
			}
		})
	}
}

// TestDispatchLifecycleRequiredFieldGuardsFireUnderGroupedTool is spec 15
// §6 rows 2/3: an action valid + ids present but its OWN required field
// absent still trips the per-action guard when driven THROUGH the grouped
// a2a_lifecycle tool (the guard fires before any disk access, so a
// zero-value-deps registry is enough).
//
// Note: spec §6 lists "decline→reason+reason_code", but newLifecycleHandler
// enforces only RequireReason (not RequireReasonCode) in its body — a
// pre-existing P14 handler quirk, out of P15 scope (handler body is
// off-limits). So this asserts decline→reason only.
func TestDispatchLifecycleRequiredFieldGuardsFireUnderGroupedTool(t *testing.T) {
	t.Parallel()
	r := dispatchTestRegistry(t)
	spec, ok := r.Get("a2a_lifecycle")
	if !ok {
		t.Fatal("a2a_lifecycle not registered")
	}
	cases := []struct {
		action string
		input  LifecycleInput
		field  string
	}{
		{"decline", LifecycleInput{IDs: []string{"X"}}, "reason"},
		{"block", LifecycleInput{IDs: []string{"X"}}, "refs"},
		{"supersede", LifecycleInput{IDs: []string{"X"}}, "refs"},
		{"satisfy", LifecycleInput{IDs: []string{"X"}}, "refs"},
		{"verify-fail", LifecycleInput{IDs: []string{"X"}}, "findings"},
		{"reject", LifecycleInput{IDs: []string{"X"}}, "reason"},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(tc.input)
		if err != nil {
			t.Fatalf("%s: marshal input: %v", tc.action, err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: unmarshal input: %v", tc.action, err)
		}
		m["action"] = tc.action
		args, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("%s: marshal args: %v", tc.action, err)
		}
		_, _, herr := spec.Handler(context.Background(), args)
		if herr == nil {
			t.Errorf("%s: expected a required-field error for a missing %s, got nil", tc.action, tc.field)
			continue
		}
		if !strings.Contains(herr.Error(), tc.field) {
			t.Errorf("%s: expected the error to name %q, got: %v", tc.action, tc.field, herr)
		}
	}
}

// TestDispatchUnknownDiscriminatorIsWellFormedError proves a made-up
// action/view returns a normal error (surfaced as an isError tool result),
// never a panic and never a JSON-RPC protocol error (spec 15 §6).
func TestDispatchUnknownDiscriminatorIsWellFormedError(t *testing.T) {
	t.Parallel()
	r := dispatchTestRegistry(t)

	cases := []struct {
		tool    string
		discKey string
	}{
		{"a2a_read", "view"},
		{"a2a_lifecycle", "action"},
		{"a2a_exchange", "action"},
		{"a2a_contract", "action"},
	}
	for _, tc := range cases {
		err := callTool(t, r, tc.tool, tc.discKey, "made-up-verb")
		if err == nil {
			t.Errorf("%s: expected an error for an unknown %s, got nil", tc.tool, tc.discKey)
			continue
		}
		if !strings.Contains(err.Error(), "unknown "+tc.discKey) {
			t.Errorf("%s: expected an 'unknown %s' error, got: %v", tc.tool, tc.discKey, err)
		}
	}
}

// TestDispatchMissingDiscriminatorIsWellFormedError proves an absent
// discriminator returns a normal error, not a panic (spec 15 §6).
func TestDispatchMissingDiscriminatorIsWellFormedError(t *testing.T) {
	t.Parallel()
	r := dispatchTestRegistry(t)
	for _, tool := range []string{"a2a_read", "a2a_lifecycle", "a2a_exchange", "a2a_contract"} {
		spec, ok := r.Get(tool)
		if !ok {
			t.Fatalf("tool %q not registered", tool)
		}
		if _, _, err := spec.Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s: expected an error for a missing discriminator, got nil", tool)
		}
	}
}

// TestGroupedSchemasListFullEnum proves each grouped tool's embedded input
// schema advertises its FULL action/view enum (built from the exported
// slices — the single source both this schema and the parity test read).
func TestGroupedSchemasListFullEnum(t *testing.T) {
	t.Parallel()
	r := dispatchTestRegistry(t)

	type schemaShape struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	cases := []struct {
		tool    string
		discKey string
		enum    []string
	}{
		{"a2a_read", "view", ReadViews},
		{"a2a_lifecycle", "action", LifecycleActions},
		{"a2a_exchange", "action", ExchangeActions},
		{"a2a_contract", "action", ContractActions},
	}
	for _, tc := range cases {
		spec, ok := r.Get(tc.tool)
		if !ok {
			t.Fatalf("tool %q not registered", tc.tool)
		}
		var shape schemaShape
		if err := json.Unmarshal(spec.InputSchema, &shape); err != nil {
			t.Fatalf("%s: decode schema: %v", tc.tool, err)
		}
		if len(shape.Required) != 1 || shape.Required[0] != tc.discKey {
			t.Errorf("%s: required must be [%q], got %v", tc.tool, tc.discKey, shape.Required)
		}
		got := shape.Properties[tc.discKey].Enum
		if len(got) != len(tc.enum) {
			t.Fatalf("%s: schema enum length mismatch: want %v, got %v", tc.tool, tc.enum, got)
		}
		for i, e := range tc.enum {
			if got[i] != e {
				t.Errorf("%s: schema enum[%d] = %q, want %q (must equal the exported slice)", tc.tool, i, got[i], e)
			}
		}
	}
}

// TestReadDispatchAppendsUpdateAdvisoryWithoutTouchingStructuredContent is
// spec 19 T4 AMENDED / §11 wave-12c: an a2a_read response's body carries the
// shared update advisory OUT-OF-BAND when the store's UpdateNotice grades
// GradeAvailable, while StructuredContent (result) stays byte-identical to
// the unwrapped per-verb handler's own result — proving withUpdateNotice
// never mutates result, only body. A GradeNone store (EnableUpdateNotice
// never called — the existing parity/equivalence tests' own construction)
// leaves body byte-unchanged too.
//
// The "want" (unwrapped) and "got" (dispatched) calls each use their OWN
// Store instance (same mirror/manifest/clock, distinct cacheDir): Store.Inbox
// advances the on-disk read cursor as a side effect (an item's New field
// flips false on a second call against the SAME store), which would corrupt
// a byte-for-byte comparison if both calls shared one store.
func TestReadDispatchAppendsUpdateAdvisoryWithoutTouchingStructuredContent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260722-upd1"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"}, {System: "beta", Status: "active"},
	}}
	sm := cache.SpaceMirror{SpaceID: "fixture-space", Dir: mirrorDir, Manifest: manifest}
	newStore := func(t *testing.T) *cache.Store {
		t.Helper()
		return cache.NewStore("beta", t.TempDir(), []cache.SpaceMirror{sm}, func() time.Time { return now }, 0)
	}

	t.Run("grade_available", func(t *testing.T) {
		t.Parallel()
		wantResult, wantBody, err := newInboxHandler(newStore(t))(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("inner inbox handler: %v", err)
		}

		enabledStore := newStore(t)
		cachePath := filepath.Join(t.TempDir(), "update-check.json")
		if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: now, Latest: "0.3.0", Source: "test"}); err != nil {
			t.Fatalf("seed update-check cache: %v", err)
		}
		enabledStore.EnableUpdateNotice("0.1.0", cachePath, 6*time.Hour, nil)
		dispatch := newReadDispatch(enabledStore)
		gotResult, gotBody, err := dispatch(context.Background(), json.RawMessage(`{"view":"inbox"}`))
		if err != nil {
			t.Fatalf("a2a_read view=inbox: %v", err)
		}

		wantJSON, err := json.Marshal(wantResult)
		if err != nil {
			t.Fatalf("marshal inner result: %v", err)
		}
		gotJSON, err := json.Marshal(gotResult)
		if err != nil {
			t.Fatalf("marshal dispatched result: %v", err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("StructuredContent (result) diverged:\nwant %s\ngot  %s", wantJSON, gotJSON)
		}

		notice := release.Info("0.1.0", "0.3.0", "", "")
		if notice.Grade != release.GradeAvailable {
			t.Fatalf("test setup: expected GradeAvailable, got %v", notice.Grade)
		}
		if !strings.Contains(gotBody, notice.Sentence) {
			t.Fatalf("expected the dispatched body to contain the advisory sentence %q, got %q", notice.Sentence, gotBody)
		}
		if gotBody == wantBody {
			t.Fatalf("expected the dispatched body to differ from the inner body (advisory appended)")
		}
	})

	t.Run("grade_none", func(t *testing.T) {
		t.Parallel()
		wantResult, wantBody, err := newInboxHandler(newStore(t))(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("inner inbox handler: %v", err)
		}
		// EnableUpdateNotice deliberately NOT called on this second store:
		// GradeNone, mirroring how the existing mcp parity/equivalence tests
		// build their stores.
		dispatch := newReadDispatch(newStore(t))
		gotResult, gotBody, err := dispatch(context.Background(), json.RawMessage(`{"view":"inbox"}`))
		if err != nil {
			t.Fatalf("a2a_read view=inbox: %v", err)
		}
		if gotBody != wantBody {
			t.Fatalf("GradeNone: expected body unchanged, want %q got %q", wantBody, gotBody)
		}
		wantJSON, _ := json.Marshal(wantResult)
		gotJSON, _ := json.Marshal(gotResult)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("GradeNone: StructuredContent diverged:\nwant %s\ngot  %s", wantJSON, gotJSON)
		}
	})

	// view=show is the ONE read view whose result carries a rich structured
	// payload (showOutput) AND whose body is already non-empty (the
	// artifact's verbatim markdown) — the view that actually exercises the
	// "body != "" so join with a newline" branch AND proves result byte-
	// identity holds even when result is not a bare list.
	t.Run("grade_available_show", func(t *testing.T) {
		t.Parallel()
		showArgs, err := json.Marshal(ShowInput{Ref: id})
		if err != nil {
			t.Fatalf("marshal ShowInput: %v", err)
		}

		wantResult, wantBody, err := newShowHandler(newStore(t))(context.Background(), showArgs)
		if err != nil {
			t.Fatalf("inner show handler: %v", err)
		}
		if wantBody == "" {
			t.Fatalf("test setup: expected a non-empty inner body for view=show")
		}

		enabledStore := newStore(t)
		cachePath := filepath.Join(t.TempDir(), "update-check.json")
		if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: now, Latest: "0.3.0", Source: "test"}); err != nil {
			t.Fatalf("seed update-check cache: %v", err)
		}
		enabledStore.EnableUpdateNotice("0.1.0", cachePath, 6*time.Hour, nil)
		dispatch := newReadDispatch(enabledStore)

		showViewArgs, err := json.Marshal(map[string]string{"view": "show", "ref": id})
		if err != nil {
			t.Fatalf("marshal show view args: %v", err)
		}
		gotResult, gotBody, err := dispatch(context.Background(), showViewArgs)
		if err != nil {
			t.Fatalf("a2a_read view=show: %v", err)
		}

		wantJSON, err := json.Marshal(wantResult)
		if err != nil {
			t.Fatalf("marshal inner result: %v", err)
		}
		gotJSON, err := json.Marshal(gotResult)
		if err != nil {
			t.Fatalf("marshal dispatched result: %v", err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("StructuredContent (result) diverged:\nwant %s\ngot  %s", wantJSON, gotJSON)
		}

		notice := release.Info("0.1.0", "0.3.0", "", "")
		wantJoined := wantBody + "\nnote: " + notice.Sentence
		if gotBody != wantJoined {
			t.Fatalf("expected the body to be the inner body + newline + advisory:\nwant %q\ngot  %q", wantJoined, gotBody)
		}
	})
}

// TestGroupedToolsListWeightDropsFromP14Baseline gates spec 15 §8 AC #1's
// "weight drops materially": the grouped tools/list payload must be well
// under the P14 baseline of 8481 bytes / ~2120 tokens. The measured size is
// logged for the phase report.
func TestGroupedToolsListWeightDropsFromP14Baseline(t *testing.T) {
	t.Parallel()
	r := dispatchTestRegistry(t)

	descs := make([]toolDescriptor, 0, len(r.List()))
	for _, spec := range r.List() {
		descs = append(descs, toolDescriptor{Name: spec.Name, Description: spec.Description, InputSchema: spec.InputSchema})
	}
	payload, err := json.Marshal(toolsListResult{Tools: descs})
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	const p14Baseline = 8481
	t.Logf("grouped tools/list weight: %d bytes (~%d tokens); P14 baseline: %d bytes (~2120 tokens)", len(payload), len(payload)/4, p14Baseline)
	if len(payload) >= p14Baseline {
		t.Fatalf("grouped tools/list weight %d bytes did not drop below the P14 baseline %d", len(payload), p14Baseline)
	}
}
