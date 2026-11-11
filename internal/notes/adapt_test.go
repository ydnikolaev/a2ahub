package notes

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
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
// test's own count — driven through adoption.Pending with a2a's own
// Policy — is 8, agreeing with the spec and not the recount; see this
// phase's reported deviations for the discrepancy.
func TestPending_RealCorpusMeasurement(t *testing.T) {
	t.Parallel()
	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	issues, err := adoption.LoadStanding(releasenotes.FS, StandingPath)
	if err != nil {
		t.Fatalf("LoadStanding: %v", err)
	}

	// A note authored ahead changes with every commit that adds to it, and it
	// would surface here for this old binary. The pin judges dated notes only.
	all = slices.DeleteFunc(all, func(rn adoption.Release) bool { return rn.Released == adoption.Unreleased })

	proj, err := adoption.Pending(all, issues, adoption.Clock{Through: "0.19.0"}, "0.25.6", Policy())
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
	// 4 at the measurement; the 2026-11-08 detect retrofit added a probe to
	// RN-01906-3 and RN-01908-2 and removed RN-02506-5's non-probe.
	if withDetect != 5 {
		t.Errorf("obligations carrying a detect: = %d, want 5", withDetect)
	}
	if withRun != 8 {
		t.Errorf("obligations carrying a runnable run: = %d, want 8 (spec's own count; a lead recount during this phase reported 6 — this test's own count, driven through Pending, is 8)", withRun)
	}

	// Both standing known issues are scope: none today and must never
	// reach the projection (AC-2).
	for _, item := range proj.Items {
		if item.Change.Kind == adoption.KindKnownIssue {
			t.Errorf("a scope:none known-issue reached the projection: %+v", item.Change)
		}
	}
}

// legacyTemplateDetects are the released changes whose detect: is a
// template. They shipped in binaries already, so they stay as written; the
// list may only shrink. A note authored now writes a runnable probe or,
// where none exists, an exemption row in
// scripts/lib/release-note-detect-budget.txt.
var legacyTemplateDetects = []string{
	"RN-0700-6", "RN-0900-8", "RN-01100-1", "RN-01900-8", "RN-02600-3", "RN-02600-5",
}

// TestNoNewTemplateDetects is the forward gate: no change outside
// legacyTemplateDetects carries a template detect:, and every listed id
// still does (a fixed one is deleted from the list in the same commit).
func TestNoNewTemplateDetects(t *testing.T) {
	t.Parallel()
	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	found := map[string]bool{}
	for _, rn := range all {
		for _, ch := range rn.Changes {
			for _, cmd := range ch.Action.Detect {
				if !adoption.IsTemplateDetect(cmd) {
					continue
				}
				found[ch.ID] = true
				if !slices.Contains(legacyTemplateDetects, ch.ID) {
					t.Errorf("%s (%s): detect %q is a template, not a probe: `a2a adapt --done` can never run it. Write a command that exits 0 once the step is done.", ch.ID, rn.Version, cmd)
				}
			}
		}
	}
	for _, id := range legacyTemplateDetects {
		if !found[id] {
			t.Errorf("%s no longer carries a template detect: delete it from legacyTemplateDetects", id)
		}
	}
}

// mutatingDetect matches a detect that invokes an a2a verb which writes
// (to the repository, the mirror, the space, or the read cursor: `inbox`
// advances it) or which never exits on its own (`mcp` serves stdio until
// its client hangs up). `a2a adapt --done` runs every detect in the reader's tree,
// so such a command would perform the step it claims to probe, or a
// different one, and report the obligation as met. A read-only form
// (`a2a space update --check`) is a different command: invokesWriter
// exempts it.
var mutatingDetect = regexp.MustCompile(`(^|[;&|(!]\s*|\s)a2a\s+(init|new|submit|respond|update|sync|connect|inbox|close|verify|dispute|land|mcp|html|notifications|skill\s+(install|link)|contract\s+(publish|adopt|retire|deprecate|new)|space\s+update)($|[\s;&|)])`)

// TestNoDetectInvokesAWriter is the forward gate for the side-effect class:
// no runnable detect: in the embedded corpus may call a writing verb. A
// template is exempt because it is never run (TestNoNewTemplateDetects
// guards that class).
func TestNoDetectInvokesAWriter(t *testing.T) {
	t.Parallel()
	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, rn := range all {
		for _, ch := range rn.Changes {
			for _, cmd := range ch.Action.Detect {
				if adoption.IsTemplateDetect(cmd) || !invokesWriter(cmd) {
					continue
				}
				t.Errorf("%s (%s): detect %q invokes an a2a verb that writes or never exits: `a2a adapt --done` would run it in the reader's tree. Write a read-only probe, or remove the detect and add an exemption row to scripts/lib/release-note-detect-budget.txt.", ch.ID, rn.Version, cmd)
			}
		}
	}
}

func invokesWriter(cmd string) bool {
	return mutatingDetect.MatchString(strings.ReplaceAll(cmd, "space update --check", "space check"))
}

func TestInvokesWriter(t *testing.T) {
	t.Parallel()
	for cmd, want := range map[string]bool{
		"a2a init":                       true,
		"a2a inbox --json | grep -q x":   true,
		"a2a sync && a2a inbox":          true,
		"a2a skill install":              true,
		"a2a contract publish x":         true,
		"a2a space update":               true,
		"a2a validate --all":             false,
		"a2a doctor":                     false,
		"! a2a --version | grep -q 0.26": false,
		"grep -q 'a2a sync' Makefile":    false,
		"a2a show RN-1":                  false,
	} {
		if got := invokesWriter(cmd); got != want {
			t.Errorf("invokesWriter(%q) = %v, want %v", cmd, got, want)
		}
	}
}
