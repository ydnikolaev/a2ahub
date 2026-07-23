package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeInboxItem(t *testing.T, hubRoot, id, kind, title, status string) {
	t.Helper()
	dir := filepath.Join(hubRoot, "feedback", "inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "feedback: v1\n" +
		"id: " + id + "\n" +
		"kind: " + kind + "\n" +
		"severity: minor\n" +
		"title: \"" + title + "\"\n" +
		"summary: \"summary\"\n" +
		"context:\n  a2a_version: v0.1.1\n  os_arch: darwin/arm64\n  surface: cli\n" +
		"checks:\n  docs_consulted: true\n  grounded_in_real_work: true\n  not_space_specific: true\n  no_sensitive_content: true\n  duplicates_checked: true\n" +
		"status: " + status + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write inbox item: %v", err)
	}
}

func seedBacklog(t *testing.T, hubRoot string, wipLimit int, items ...BacklogItem) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(hubRoot, "feedback"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeBacklogDoc(hubRoot, BacklogDoc{Backlog: "v1", WipLimit: wipLimit, Items: items}); err != nil {
		t.Fatalf("seedBacklog: %v", err)
	}
}

func TestTriage_EmptyInboxIsClean(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	report, err := Triage(hubRoot)
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("expected a clean report for an empty inbox, got %+v", report.Entries)
	}
}

func TestTriage_ListsNewItemsAndSkipsAlreadyTriaged(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)
	writeInboxItem(t, hubRoot, "fb-20260701-aaaaaa", "bug", "sync reports clean but stale", "new")
	writeInboxItem(t, hubRoot, "fb-20260702-bbbbbb", "docs", "already handled", "accepted")

	report, err := Triage(hubRoot)
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("expected exactly 1 status:new entry, got %+v", report.Entries)
	}
	if report.Entries[0].Item.ID != "fb-20260701-aaaaaa" {
		t.Fatalf("entry = %+v, want fb-20260701-aaaaaa", report.Entries[0].Item)
	}
}

func TestTriage_DedupeCandidatesAgainstInboxAndBacklog(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16, BacklogItem{
		ID: "fb-20260601-cccccc", Kind: "bug", Severity: "major",
		Title: "sync reports clean mirror stale issue", Verdict: "accepted",
		Route: "backlog", Refs: nil, Date: "2026-06-01",
	})
	writeInboxItem(t, hubRoot, "fb-20260701-aaaaaa", "bug", "sync reports clean mirror stale", "new")
	writeInboxItem(t, hubRoot, "fb-20260702-dddddd", "bug", "sync reports clean mirror stale too", "new")

	report, err := Triage(hubRoot)
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("expected 2 status:new entries, got %+v", report.Entries)
	}
	for _, e := range report.Entries {
		var sawInbox, sawBacklog bool
		for _, c := range e.Candidates {
			if c.Source == "inbox" {
				sawInbox = true
			}
			if c.Source == "backlog" {
				sawBacklog = true
			}
		}
		if !sawInbox {
			t.Errorf("%s: expected an inbox dedupe candidate, got %+v", e.Item.ID, e.Candidates)
		}
		if !sawBacklog {
			t.Errorf("%s: expected a backlog dedupe candidate, got %+v", e.Item.ID, e.Candidates)
		}
	}
}

func TestApplyVerdicts_MutatesRoutesDigestsAndIsIdempotent(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)
	writeInboxItem(t, hubRoot, "fb-20260701-aaaaaa", "bug", "accepted item", "new")
	writeInboxItem(t, hubRoot, "fb-20260702-bbbbbb", "docs", "duplicate item", "new")
	now := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)

	verdicts := []Verdict{
		{ID: "fb-20260701-aaaaaa", Status: "accepted", Resolution: "backlog fb-20260701-aaaaaa", Route: "backlog", Refs: nil},
		{ID: "fb-20260702-bbbbbb", Status: "duplicate", Resolution: "duplicate of fb-20260601-cccccc"},
	}
	result, err := ApplyVerdicts(hubRoot, verdicts, now)
	if err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("Applied = %+v, want 2", result.Applied)
	}

	items, err := readInboxItems(hubRoot)
	if err != nil {
		t.Fatalf("readInboxItems: %v", err)
	}
	byID := make(map[string]TriageItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	if byID["fb-20260701-aaaaaa"].Status != "accepted" {
		t.Errorf("fb-20260701-aaaaaa status = %q, want accepted", byID["fb-20260701-aaaaaa"].Status)
	}
	if byID["fb-20260702-bbbbbb"].Status != "duplicate" {
		t.Errorf("fb-20260702-bbbbbb status = %q, want duplicate", byID["fb-20260702-bbbbbb"].Status)
	}

	backlog, err := readBacklogDoc(hubRoot)
	if err != nil {
		t.Fatalf("readBacklogDoc: %v", err)
	}
	if len(backlog.Items) != 1 || backlog.Items[0].ID != "fb-20260701-aaaaaa" || backlog.Items[0].Verdict != "accepted" {
		t.Fatalf("backlog.Items = %+v, want one accepted row for fb-20260701-aaaaaa", backlog.Items)
	}

	digest, err := os.ReadFile(filepath.Join(hubRoot, "feedback", "digest.md"))
	if err != nil {
		t.Fatalf("read digest.md: %v", err)
	}
	if !strings.Contains(string(digest), "fb-20260701-aaaaaa") || !strings.Contains(string(digest), "fb-20260702-bbbbbb") {
		t.Fatalf("digest.md = %q, want both ids mentioned", digest)
	}

	// Idempotent: after applying verdicts, both items are no longer
	// status:new, so triage is clean and a re-apply of the SAME verdicts
	// is a no-op (skipped, not re-applied/re-digested).
	report, err := Triage(hubRoot)
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("expected a clean triage after applying verdicts, got %+v", report.Entries)
	}

	digestBefore, err := os.ReadFile(filepath.Join(hubRoot, "feedback", "digest.md"))
	if err != nil {
		t.Fatalf("read digest.md: %v", err)
	}
	result2, err := ApplyVerdicts(hubRoot, verdicts, now)
	if err != nil {
		t.Fatalf("ApplyVerdicts (re-run): %v", err)
	}
	if len(result2.Applied) != 0 || len(result2.Skipped) != 2 {
		t.Fatalf("re-run result = %+v, want 0 applied, 2 skipped (already triaged)", result2)
	}
	digestAfter, err := os.ReadFile(filepath.Join(hubRoot, "feedback", "digest.md"))
	if err != nil {
		t.Fatalf("read digest.md: %v", err)
	}
	if string(digestBefore) != string(digestAfter) {
		t.Fatal("expected the re-run to leave digest.md unchanged (idempotent)")
	}
}

func TestApplyVerdicts_WipLimitHit(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 1, BacklogItem{
		ID: "fb-20260601-cccccc", Kind: "bug", Severity: "major", Title: "already at limit",
		Verdict: "accepted", Route: "backlog", Date: "2026-06-01",
	})
	writeInboxItem(t, hubRoot, "fb-20260701-aaaaaa", "bug", "one more accepted item", "new")

	result, err := ApplyVerdicts(hubRoot, []Verdict{
		{ID: "fb-20260701-aaaaaa", Status: "accepted", Route: "backlog"},
	}, time.Now())
	if err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}
	if len(result.WipLimitHit) != 1 || result.WipLimitHit[0] != "fb-20260701-aaaaaa" {
		t.Fatalf("WipLimitHit = %+v, want [fb-20260701-aaaaaa]", result.WipLimitHit)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %+v, want none (wip-limit brake refuses the mutation too)", result.Applied)
	}

	// The item must stay status:new (untouched) so it can be retried once
	// the backlog has room.
	items, err := readInboxItems(hubRoot)
	if err != nil {
		t.Fatalf("readInboxItems: %v", err)
	}
	if items[0].Status != "new" {
		t.Fatalf("item status = %q, want new (untouched by the wip-limit brake)", items[0].Status)
	}
}

func TestApplyVerdicts_UnknownIDIsSkipped(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	result, err := ApplyVerdicts(hubRoot, []Verdict{{ID: "fb-nonexistent", Status: "accepted"}}, time.Now())
	if err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "fb-nonexistent" {
		t.Fatalf("Skipped = %+v, want [fb-nonexistent]", result.Skipped)
	}
}

func TestParseVerdicts(t *testing.T) {
	t.Parallel()
	raw := []byte("verdicts:\n  - id: fb-20260701-aaaaaa\n    status: accepted\n    route: backlog\n")
	verdicts, err := ParseVerdicts(raw)
	if err != nil {
		t.Fatalf("ParseVerdicts: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].ID != "fb-20260701-aaaaaa" || verdicts[0].Status != "accepted" {
		t.Fatalf("verdicts = %+v", verdicts)
	}
}
