package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// TestPending_RealCorpusMeasurement is the P13 "THE MEASUREMENT" tripwire,
// the adapt-projection sibling of TestLoad_CorpusIntegrity: pinned numbers
// over the REAL embedded corpus for a repository baselined at v0.19.0 and a
// v0.25.6 binary (spec 13 §"THE MEASUREMENT"). Bump these at the next
// release that changes the shape of what obliges the reader — that edit IS
// the check.
//
// The spec's own table says 8 of the 44 obligations carry a runnable
// `run:`; a lead re-count during this phase's own brief reported 6. This
// test's own count — driven through the same typed Pending this package
// ships — is 8, agreeing with the spec and not the recount; see this
// phase's reported deviations for the discrepancy.
func TestPending_RealCorpusMeasurement(t *testing.T) {
	t.Parallel()
	all, err := Load(releasenotes.FS)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	issues, err := LoadCurrentKnownIssues(releasenotes.FS)
	if err != nil {
		t.Fatalf("LoadCurrentKnownIssues: %v", err)
	}

	proj, err := Pending(all, issues, "0.19.0", "0.25.6")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	if proj.Releases != 23 {
		t.Errorf("Releases = %d, want 23", proj.Releases)
	}
	if len(proj.Items) != 44 {
		t.Fatalf("len(Items) = %d, want 44: %+v", len(proj.Items), proj.Items)
	}
	if proj.StartedFromOldest {
		t.Error("StartedFromOldest = true, want false (an explicit baseline was given)")
	}

	var local, space, withRun, withDetect int
	for _, item := range proj.Items {
		switch item.Change.Action.Scope {
		case "local":
			local++
		case "space":
			space++
		default:
			t.Errorf("item %s: unexpected scope %q reached the projection", item.Change.ID, item.Change.Action.Scope)
		}
		if len(item.Change.Action.Run) > 0 {
			withRun++
		}
		if len(item.Change.Action.Detect) > 0 {
			withDetect++
		}
	}
	if local != 32 {
		t.Errorf("local obligations = %d, want 32", local)
	}
	if space != 12 {
		t.Errorf("space obligations = %d, want 12", space)
	}
	if withDetect != 4 {
		t.Errorf("obligations carrying a detect: = %d, want 4", withDetect)
	}
	if withRun != 8 {
		t.Errorf("obligations carrying a runnable run: = %d, want 8 (spec's own count; a lead recount during this phase reported 6 — this test's own count, driven through Pending, is 8)", withRun)
	}

	// Both standing known issues are scope: none today and must never
	// reach the projection (AC-2).
	for _, item := range proj.Items {
		if item.Change.Kind == KindKnownIssue {
			t.Errorf("a scope:none known-issue reached the projection: %+v", item.Change)
		}
	}
}

// TestPendingExcludesScopeNoneAndStandingKnownIssues (AC-1, AC-2) proves the
// filter on a small synthetic corpus mixing every Action.Scope value plus a
// standing known issue, independent of whatever the real corpus contains
// today.
func TestPendingExcludesScopeNoneAndStandingKnownIssues(t *testing.T) {
	t.Parallel()
	all := []ReleaseNotes{
		{Version: "1.0.0", Changes: []Change{
			{ID: "C-NONE", Kind: "feat", Impact: "high", Action: Action{Scope: "none"}},
			{ID: "C-EMPTY-ACTION", Kind: "feat", Impact: "high"}, // no action block at all: Scope decodes to ""
			{ID: "C-LOCAL", Kind: "feat", Impact: "normal", Action: Action{Scope: "local"}},
			{ID: "C-SPACE", Kind: "feat", Impact: "low", Action: Action{Scope: "space"}},
		}},
	}
	issues := []Change{
		{ID: "KI-NONE", Kind: KindKnownIssue, Action: Action{Scope: "none"}},
	}

	proj, err := Pending(all, issues, "", "1.0.0")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	got := map[string]bool{}
	for _, item := range proj.Items {
		got[item.Change.ID] = true
	}
	if got["C-NONE"] {
		t.Error("a scope:none change reached the projection")
	}
	if got["C-EMPTY-ACTION"] {
		t.Error("a change with no action block at all reached the projection — scope must be a POSITIVE local/space match, never a default inclusion")
	}
	if got["KI-NONE"] {
		t.Error("the scope:none standing known issue reached the projection")
	}
	if !got["C-LOCAL"] || !got["C-SPACE"] {
		t.Fatalf("expected C-LOCAL and C-SPACE in the projection, got %+v", proj.Items)
	}
}

// TestPendingGroupOrder (the order table, P13 spec) proves every group
// assignment, including the known-issue special case: a known issue whose
// OWN scope is "local" still sorts as GroupKnownIssue, not
// GroupLocalWithRun — kind decides the group before scope does.
func TestPendingGroupOrder(t *testing.T) {
	t.Parallel()
	all := []ReleaseNotes{
		{Version: "1.0.0", Changes: []Change{
			{ID: "LOCAL-RUN", Kind: "feat", Action: Action{Scope: "local", Run: []string{"a2a doctor"}}},
			{ID: "LOCAL-PROSE", Kind: "feat", Action: Action{Scope: "local"}},
			{ID: "LOCAL-DETECT", Kind: "feat", Action: Action{Scope: "local", Detect: []string{"a2a doctor"}}},
			{ID: "SPACE", Kind: "feat", Action: Action{Scope: "space"}},
			// A known issue that obliges something: kind wins, group 4 —
			// even though it ALSO carries a run:, which would otherwise
			// put a plain change in GroupLocalWithRun.
			{ID: "KI-LOCAL-RUN", Kind: KindKnownIssue, Action: Action{Scope: "local", Run: []string{"a2a doctor"}}},
		}},
	}

	proj, err := Pending(all, nil, "", "1.0.0")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	want := map[string]Group{
		"LOCAL-RUN":    GroupLocalWithRun,
		"LOCAL-PROSE":  GroupLocalOther,
		"LOCAL-DETECT": GroupLocalOther,
		"SPACE":        GroupSpace,
		"KI-LOCAL-RUN": GroupKnownIssue,
	}
	if len(proj.Items) != len(want) {
		t.Fatalf("len(Items) = %d, want %d: %+v", len(proj.Items), len(want), proj.Items)
	}
	for _, item := range proj.Items {
		if g, ok := want[item.Change.ID]; !ok || g != item.Group {
			t.Errorf("%s: Group = %v, want %v", item.Change.ID, item.Group, want[item.Change.ID])
		}
	}
}

// TestPendingTwoClocks (AC-4, AC-5) is the fixture the spec calls out by
// name: the walk must start at the REPOSITORY's own baseline
// (adapted_through), never at "the version the binary replaced" — a
// different clock entirely. A repo baselined at 0.10.0 and running a
// 0.30.0 binary must see every obliging change from 0.11.0 through 0.30.0,
// even though a from-the-binary's-previous-version reading (e.g. "0.25.0",
// a version this fixture places INSIDE the range with its own obliging
// change) would have hidden everything at or before it.
func TestPendingTwoClocks(t *testing.T) {
	t.Parallel()
	all := []ReleaseNotes{
		{Version: "0.10.0", Changes: []Change{{ID: "OLD", Kind: "feat", Action: Action{Scope: "local"}}}},
		{Version: "0.20.0", Changes: []Change{{ID: "MID", Kind: "feat", Action: Action{Scope: "local"}}}},
		// A version that could be mistaken for "the binary's previous
		// version" in a one-clock design — it must NOT act as a boundary.
		{Version: "0.25.0", Changes: []Change{{ID: "AT-WRONG-CLOCK", Kind: "feat", Action: Action{Scope: "local"}}}},
		{Version: "0.30.0", Changes: []Change{{ID: "NEW", Kind: "feat", Action: Action{Scope: "local"}}}},
	}

	proj, err := Pending(all, nil, "0.10.0", "0.30.0")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if proj.Releases != 3 {
		t.Errorf("Releases = %d, want 3 (0.20.0, 0.25.0, 0.30.0)", proj.Releases)
	}
	got := map[string]bool{}
	for _, item := range proj.Items {
		got[item.Change.ID] = true
	}
	if got["OLD"] {
		t.Error("OLD (at/before the baseline) must not appear")
	}
	for _, id := range []string{"MID", "AT-WRONG-CLOCK", "NEW"} {
		if !got[id] {
			t.Errorf("%s must appear — the walk starts at the repo baseline, not at any version derived from the binary", id)
		}
	}
}

// TestPendingAbsentBaselineStartsFromOldest (AC-4's "never adapted" edge,
// spec §"the config field": "Absent field = never adapted; the walk then
// starts at the oldest note the binary carries AND SAYS SO").
func TestPendingAbsentBaselineStartsFromOldest(t *testing.T) {
	t.Parallel()
	all := []ReleaseNotes{
		{Version: "0.1.0", Changes: []Change{{ID: "A", Kind: "feat", Action: Action{Scope: "local"}}}},
		{Version: "0.2.0", Changes: []Change{{ID: "B", Kind: "feat", Action: Action{Scope: "local"}}}},
	}
	proj, err := Pending(all, nil, "", "0.2.0")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if !proj.StartedFromOldest {
		t.Error("StartedFromOldest = false, want true")
	}
	if proj.Oldest != "0.1.0" {
		t.Errorf("Oldest = %q, want 0.1.0", proj.Oldest)
	}
	if len(proj.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2 (both releases, starting from the oldest): %+v", len(proj.Items), proj.Items)
	}
}

// TestPendingEmptyRangeIsNothingToAdapt (spec §6 edge case: "a release
// whose every change is none produces nothing to adapt and exits 0" — the
// projection's own half of that: an empty Items with no error).
func TestPendingEmptyRangeIsNothingToAdapt(t *testing.T) {
	t.Parallel()
	all := []ReleaseNotes{
		{Version: "1.0.0", Changes: []Change{{ID: "A", Kind: "feat", Action: Action{Scope: "none"}}}},
	}
	proj, err := Pending(all, nil, "", "1.0.0")
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(proj.Items) != 0 {
		t.Fatalf("len(Items) = %d, want 0: %+v", len(proj.Items), proj.Items)
	}
}

// TestValidateBaselineRefusesDowngrade (AC-10): a baseline above the
// running binary refuses, naming both versions.
func TestValidateBaselineRefusesDowngrade(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		baseline      string
		binaryVersion string
		wantErr       bool
	}{
		{"no baseline never refuses", "", "0.1.0", false},
		{"baseline equal to binary is fine", "0.5.0", "0.5.0", false},
		{"baseline older than binary is fine", "0.5.0", "0.6.0", false},
		{"baseline newer than binary refuses", "0.7.0", "0.6.0", true},
		{"unparseable baseline fails closed", "not-a-version", "0.6.0", true},
		{"unparseable binary fails closed", "0.5.0", "not-a-version", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBaseline(tc.baseline, tc.binaryVersion)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateBaseline(%q, %q) = nil, want an error", tc.baseline, tc.binaryVersion)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateBaseline(%q, %q) = %v, want nil", tc.baseline, tc.binaryVersion, err)
			}
		})
	}

	// Pending surfaces the exact sentinel and names both versions in the
	// wrapped Error's Input.
	_, err := Pending(nil, nil, "0.7.0", "0.6.0")
	if !errors.Is(err, ErrBaselineAheadOfBinary) {
		t.Fatalf("Pending error = %v, want wrapping ErrBaselineAheadOfBinary", err)
	}
	if err == nil || !strings.Contains(err.Error(), "0.7.0") || !strings.Contains(err.Error(), "0.6.0") {
		t.Fatalf("error = %v, want it to name both the baseline and the binary version", err)
	}
}

// TestCheckDetects_AllClean (--done's happy path): every detect: across
// every item runs and exits zero.
func TestCheckDetects_AllClean(t *testing.T) {
	t.Parallel()
	items := []PendingItem{
		{Change: Change{ID: "A", Action: Action{Detect: []string{"one"}}}},
		{Change: Change{ID: "B", Action: Action{Detect: []string{"two", "three"}}}},
	}
	var calls []string
	run := func(_ context.Context, cmd string) (bool, error) {
		calls = append(calls, cmd)
		return false, nil
	}
	if result := CheckDetects(context.Background(), items, run); result != nil {
		t.Fatalf("CheckDetects = %+v, want nil (every detect ran clean)", result)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %v, want 3", calls)
	}
}

// TestCheckDetects_StillFires (AC-7): --done refuses NAMING the change
// whose detect: still fires — the fixture's detect is a failing command
// (fired == true), not a command that could not run at all.
func TestCheckDetects_StillFires(t *testing.T) {
	t.Parallel()
	items := []PendingItem{
		{Change: Change{ID: "A", Action: Action{Detect: []string{"still-broken"}}}},
		{Change: Change{ID: "B", Action: Action{Detect: []string{"never-reached"}}}},
	}
	run := func(_ context.Context, cmd string) (bool, error) {
		return cmd == "still-broken", nil
	}
	result := CheckDetects(context.Background(), items, run)
	if result == nil {
		t.Fatal("CheckDetects = nil, want a firing result")
	}
	if !result.Fired || result.Err != nil {
		t.Fatalf("result = %+v, want Fired=true, Err=nil", result)
	}
	if result.Item.Change.ID != "A" {
		t.Fatalf("result names %q, want A", result.Item.Change.ID)
	}
}

// TestCheckDetects_CannotRunIsNotFired proves the split the doc comment
// promises: a detect: that could not be RUN at all is reported as an
// error, never conflated with "still fires".
func TestCheckDetects_CannotRunIsNotFired(t *testing.T) {
	t.Parallel()
	items := []PendingItem{
		{Change: Change{ID: "A", Action: Action{Detect: []string{"whatever"}}}},
	}
	wantErr := fmt.Errorf("exec: \"sh\": executable file not found in $PATH")
	run := func(_ context.Context, cmd string) (bool, error) {
		return false, wantErr
	}
	result := CheckDetects(context.Background(), items, run)
	if result == nil {
		t.Fatal("CheckDetects = nil, want a result naming the unmeasurable detect")
	}
	if result.Fired {
		t.Fatal("Fired = true, want false — a command that could not run must never read as 'still fires'")
	}
	if !errors.Is(result.Err, wantErr) {
		t.Fatalf("Err = %v, want it to wrap %v", result.Err, wantErr)
	}
}

// TestDefaultDetectRunner exercises the real shell-backed runner, not just
// the fake seam every other test above uses.
func TestDefaultDetectRunner(t *testing.T) {
	t.Parallel()
	fired, err := DefaultDetectRunner(context.Background(), "exit 0")
	if err != nil || fired {
		t.Fatalf("exit 0: fired=%v err=%v, want fired=false err=nil", fired, err)
	}
	fired, err = DefaultDetectRunner(context.Background(), "exit 1")
	if err != nil || !fired {
		t.Fatalf("exit 1: fired=%v err=%v, want fired=true err=nil", fired, err)
	}
}
