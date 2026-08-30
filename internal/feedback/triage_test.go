package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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

// TestTriage_EmptyInboxIsClean keeps passing under the AC3 freshness
// parameter because hubRoot (t.TempDir()) is not a git work tree in
// production terms — the CLI-level resolver would report
// FreshnessNotApplicable for it, so the test states that assumption
// explicitly rather than leaning on a zero value (own-loop 09 §8 AC3,
// §11 wave D2: "TestTriage_EmptyInboxIsClean keeps passing because its
// hubRoot is not a work tree").
func TestTriage_EmptyInboxIsClean(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	report, err := Triage(hubRoot, Freshness{Status: FreshnessNotApplicable})
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

	report, err := Triage(hubRoot, Freshness{Status: FreshnessNotApplicable})
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

	report, err := Triage(hubRoot, Freshness{Status: FreshnessNotApplicable})
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

// TestTriage_RefusedFreshnessRefusesBeforeListing is AC3's own text made a
// unit test: a refused freshness verdict must make Triage refuse — naming
// the reason and the hub of record — BEFORE it reads a single
// feedback/inbox/*.yaml file, never manufacturing a "clean" (or any other)
// report (own-loop 09 §8 AC3, §11 wave D2). The inbox here is seeded with a
// status:new item specifically so a report that silently came back clean
// (rather than erroring) would be caught: an empty TriageReport plus a nil
// error would look identical to "genuinely nothing to review."
func TestTriage_RefusedFreshnessRefusesBeforeListing(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)
	writeInboxItem(t, hubRoot, "fb-20260701-aaaaaa", "bug", "must not be listed on a refused verdict", "new")

	freshness := Freshness{
		Status:      FreshnessRefused,
		Reason:      "hub's default branch carries fb-20260808-999999, which this tree lacks",
		HubOfRecord: "https://github.com/ydnikolaev/a2ahub",
	}
	report, err := Triage(hubRoot, freshness)
	if err == nil {
		t.Fatalf("Triage returned nil error for a refused freshness verdict, report = %+v", report)
	}
	if !strings.Contains(err.Error(), freshness.Reason) {
		t.Errorf("error = %q, want it to name the reason %q", err.Error(), freshness.Reason)
	}
	if !strings.Contains(err.Error(), freshness.HubOfRecord) {
		t.Errorf("error = %q, want it to name the hub of record %q", err.Error(), freshness.HubOfRecord)
	}
	if len(report.Entries) != 0 {
		t.Fatalf("report.Entries = %+v, want empty on a refused verdict (never a listing)", report.Entries)
	}
}

// TestTriage_RefusedFreshnessWithNoReasonNamesUnresolved is the zero-value
// case: a Freshness nobody resolved (Reason and HubOfRecord both blank,
// Status defaulting to FreshnessRefused because that is iota 0) must still
// refuse, and its error must say so rather than printing a blank reason —
// proving the fail-closed zero value actually reads as a real refusal, not
// a silently empty one (§11 wave D2: "a Freshness value nobody resolved...
// must fail CLOSED").
func TestTriage_RefusedFreshnessWithNoReasonNamesUnresolved(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	report, err := Triage(hubRoot, Freshness{})
	if err == nil {
		t.Fatalf("Triage returned nil error for a zero-value (unresolved) freshness verdict, report = %+v", report)
	}
	if !strings.Contains(err.Error(), "never resolved") {
		t.Errorf("error = %q, want it to name the verdict as never resolved", err.Error())
	}
}

// TestTriage_CurrentFreshnessAllowsClean proves the OTHER admitted status
// (FreshnessCurrent, not just FreshnessNotApplicable) reaches the ordinary
// listing path rather than refusing.
func TestTriage_CurrentFreshnessAllowsClean(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	report, err := Triage(hubRoot, Freshness{Status: FreshnessCurrent})
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("expected a clean report for an empty inbox under FreshnessCurrent, got %+v", report.Entries)
	}
}

// TestApplyVerdicts_RejectsInvalidStatusBeforeMutating is the go-auditor P25
// IN MED regression: a verdict whose status is outside the closed enum must
// fail the WHOLE apply with an error, and must NOT rewrite any inbox file
// (fail-closed, atomic — no partial corruption).
func TestApplyVerdicts_RejectsInvalidStatusBeforeMutating(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)
	writeInboxItem(t, hubRoot, "fb-20260701-aaaaaa", "bug", "still new after a bad apply", "new")
	now := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)

	// one good verdict + one typo'd status — the whole apply must be refused.
	verdicts := []Verdict{
		{ID: "fb-20260701-aaaaaa", Status: "accepted", Route: "backlog"},
		{ID: "fb-20260702-bbbbbb", Status: "accepetd"}, // typo
	}
	if _, err := ApplyVerdicts(hubRoot, verdicts, now); err == nil {
		t.Fatal("ApplyVerdicts accepted an invalid status, want error")
	}

	// the good item must be untouched (still `new`), proving no partial write.
	items, err := readInboxItems(hubRoot)
	if err != nil {
		t.Fatalf("readInboxItems: %v", err)
	}
	for _, it := range items {
		if it.ID == "fb-20260701-aaaaaa" && it.Status != "new" {
			t.Errorf("fb-20260701-aaaaaa status = %q, want new (no partial mutation on a rejected apply)", it.Status)
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
	// is a no-op (refused, not re-applied/re-digested).
	report, err := Triage(hubRoot, Freshness{Status: FreshnessNotApplicable})
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
	if len(result2.Applied) != 0 || len(result2.Refused) != 2 {
		t.Fatalf("re-run result = %+v, want 0 applied, 2 refused (already triaged)", result2)
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

func TestApplyVerdicts_UnknownIDIsUnknownID(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	result, err := ApplyVerdicts(hubRoot, []Verdict{{ID: "fb-nonexistent", Status: "accepted"}}, time.Now())
	if err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}
	if len(result.UnknownID) != 1 || result.UnknownID[0] != "fb-nonexistent" {
		t.Fatalf("UnknownID = %+v, want [fb-nonexistent]", result.UnknownID)
	}
	if len(result.Refused) != 0 {
		t.Fatalf("Refused = %+v, want none (an unknown id is never a refused-not-new record)", result.Refused)
	}
}

// TestApplyVerdicts_RefusedAndUnknownIDAreDistinct is the split's own
// regression test: an already-triaged id and an unknown id in the SAME
// apply must land in different ApplyResult fields, never merged into one
// bucket (P4, computed-not-listed-2026-08 spec 04 §8 row 3).
func TestApplyVerdicts_RefusedAndUnknownIDAreDistinct(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	writeInboxItem(t, hubRoot, "fb-20260801-aaaaaa", "bug", "already triaged", "accepted")

	result, err := ApplyVerdicts(hubRoot, []Verdict{
		{ID: "fb-20260801-aaaaaa", Status: "rejected"},
		{ID: "fb-typo-does-not-exist", Status: "rejected"},
	}, time.Now())
	if err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}
	if len(result.Refused) != 1 || result.Refused[0] != "fb-20260801-aaaaaa" {
		t.Fatalf("Refused = %+v, want [fb-20260801-aaaaaa]", result.Refused)
	}
	if len(result.UnknownID) != 1 || result.UnknownID[0] != "fb-typo-does-not-exist" {
		t.Fatalf("UnknownID = %+v, want [fb-typo-does-not-exist]", result.UnknownID)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Applied = %+v, want none", result.Applied)
	}
}

// TestApplyStatusResolution_RequiresExactlyOneStatusLine is a direct-call
// unit test (applyStatusResolution is unexported but in-package) for the
// brief's rule 1: "Exactly one such line must exist; zero or more than one
// is an error naming the path (fail before writing anything)." Neither shape
// is reachable end-to-end through ApplyVerdicts — a missing status: key
// leaves TriageItem.Status == "" (readInboxItems), which fails the
// `item.Status != "new"` guard and is skipped before the writer ever runs;
// a duplicate status: key is rejected earlier still, by the probe's own
// yaml.Unmarshal. So this is the only path that actually exercises the guard.
func TestApplyStatusResolution_RequiresExactlyOneStatusLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{name: "zero status lines", raw: "id: fb-20260801-eeeeee\ntitle: \"no status key\"\n"},
		{name: "two status lines", raw: "id: fb-20260801-eeeeee\nstatus: new\nstatus: accepted\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := applyStatusResolution([]byte(tc.raw), "rejected", "")
			if err == nil {
				t.Fatalf("applyStatusResolution(%q) = %q, nil, want an error naming the status-line count", tc.raw, out)
			}
			if out != nil {
				t.Fatalf("applyStatusResolution returned non-nil bytes on error: %q", out)
			}
		})
	}
}

// TestApplyVerdicts_RefusesAnUnrepresentableResolution is the fail-closed
// case for the round-trip guard in applyStatusResolution: a resolution
// ending in a newline cannot survive a literal `|-` block (which strips a
// trailing newline), so ApplyVerdicts must refuse rather than silently write
// a resolution that does not match the verdict, and the file on disk must
// stay byte-unchanged (fail BEFORE writing anything).
func TestApplyVerdicts_RefusesAnUnrepresentableResolution(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)
	writeInboxItem(t, hubRoot, "fb-20260801-ffffff", "bug", "unrepresentable resolution", "new")
	path := filepath.Join(hubRoot, "feedback", "inbox", "fb-20260801-ffffff.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	verdicts := []Verdict{{ID: "fb-20260801-ffffff", Status: "rejected", Resolution: "trailing newline\n"}}
	if _, err := ApplyVerdicts(hubRoot, verdicts, now); err == nil {
		t.Fatal("ApplyVerdicts accepted a resolution a literal block cannot round-trip, want an error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("file changed despite the refusal:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// testStripStatusAndResolution removes the top-level `status:` line and the
// top-level `resolution:` section (its key line plus every following blank
// or whitespace-indented continuation line) from raw. It is written
// independently of applyStatusResolution's own section logic in triage.go —
// sharing the helper would let a bug common to both sides hide from the
// byte-preservation assertion below.
func testStripStatusAndResolution(raw string) string {
	lines := strings.Split(raw, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		ln := lines[i]
		if strings.HasPrefix(ln, "status:") {
			i++
			continue
		}
		if strings.HasPrefix(ln, "resolution:") {
			i++
			for i < len(lines) {
				c := lines[i]
				if c == "" || strings.HasPrefix(c, " ") || strings.HasPrefix(c, "\t") {
					i++
					continue
				}
				break
			}
			continue
		}
		out = append(out, ln)
		i++
	}
	return strings.Join(out, "\n")
}

// TestApplyVerdicts_PreservesBytesOutsideStatusAndResolution is the
// load-bearing regression for own-loop 09 §11 (2026-08-09, wave C): a
// reporter's submitted record must survive triage intact except for the two
// fields the verdict owns. The fixture uses submission-order (non-alphabetic)
// keys, a folded `summary: >-` and a literal `proposal: |-` with an internal
// blank line — exactly the reshaping a yaml.Marshal round-trip used to
// introduce (alphabetical key reorder, re-quoting, re-folding).
func TestApplyVerdicts_PreservesBytesOutsideStatusAndResolution(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	dir := filepath.Join(hubRoot, "feedback", "inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := "title: \"submission key order, not alphabetical\"\n" +
		"kind: bug\n" +
		"id: fb-20260801-aaaaaa\n" +
		"feedback: v1\n" +
		"summary: >-\n" +
		"    a folded summary line that keeps\n" +
		"    flowing across several continuation\n" +
		"    lines of folded text\n" +
		"severity: minor\n" +
		"proposal: |-\n" +
		"    First line of the proposal.\n" +
		"\n" +
		"    Second paragraph after a deliberate blank line.\n" +
		"context:\n" +
		"    surface: cli\n" +
		"status: new\n"
	path := filepath.Join(dir, "fb-20260801-aaaaaa.yaml")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write inbox item: %v", err)
	}

	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	verdicts := []Verdict{{ID: "fb-20260801-aaaaaa", Status: "rejected", Resolution: "not applicable to this space"}}
	if _, err := ApplyVerdicts(hubRoot, verdicts, now); err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}

	gotRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read applied file: %v", err)
	}

	wantStripped := testStripStatusAndResolution(original)
	gotStripped := testStripStatusAndResolution(string(gotRaw))
	if gotStripped != wantStripped {
		t.Fatalf("bytes outside status/resolution changed:\n--- want ---\n%s\n--- got ---\n%s", wantStripped, gotStripped)
	}

	// Submission key order (title: first, not alphabetical) must have
	// survived too — a yaml.Marshal round-trip would put "checks"/"context"
	// first.
	if !strings.HasPrefix(string(gotRaw), "title:") {
		t.Fatalf("expected submission key order (title: first) to survive, got:\n%s", gotRaw)
	}
}

// TestApplyVerdicts_ResolutionValueRoundTrips covers renderResolutionSection's
// two emitted shapes plus the whitespace-run case that must fall back from
// folded to literal (a folded scalar unfolds every line break — and would
// unfold a run of spaces just as wrongly — to a single space).
func TestApplyVerdicts_ResolutionValueRoundTrips(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		resolution string
	}{
		{
			name:       "long unbreakable token stays on its own line, unbroken",
			resolution: "Fixed under P9; see docs/features/archive/operational-confidence-2026-08/README.md for the write-up.",
		},
		{
			name:       "newline forces a literal block",
			resolution: "Fixed in v0.15.0.\n\nSee the commit for detail.",
		},
		{
			name:       "a whitespace run cannot be folded, falls back to literal",
			resolution: "duplicate of  fb-20260601-cccccc",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hubRoot := t.TempDir()
			seedBacklog(t, hubRoot, 16)
			writeInboxItem(t, hubRoot, "fb-20260801-bbbbbb", "bug", "roundtrip case", "new")
			now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
			verdicts := []Verdict{{ID: "fb-20260801-bbbbbb", Status: "rejected", Resolution: tc.resolution}}
			if _, err := ApplyVerdicts(hubRoot, verdicts, now); err != nil {
				t.Fatalf("ApplyVerdicts: %v", err)
			}

			raw, err := os.ReadFile(filepath.Join(hubRoot, "feedback", "inbox", "fb-20260801-bbbbbb.yaml"))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("yaml.Unmarshal: %v\nraw:\n%s", err, raw)
			}
			got, _ := doc["resolution"].(string)
			if got != tc.resolution {
				t.Fatalf("resolution = %q, want %q\nraw:\n%s", got, tc.resolution, raw)
			}
		})
	}
}

// TestApplyVerdicts_ResolutionInsertionAndReplacement covers both shapes of
// § own-loop 09 rule 2: a record with no resolution: key gains one
// immediately before status:, and a record that already carries a
// multi-line resolution has the WHOLE section replaced with no orphan
// continuation lines left behind.
func TestApplyVerdicts_ResolutionInsertionAndReplacement(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	seedBacklog(t, hubRoot, 16)

	// Case 1: no resolution: key -> one is inserted immediately before status:.
	writeInboxItem(t, hubRoot, "fb-20260801-cccccc", "bug", "gains a resolution", "new")

	// Case 2: an existing multi-line resolution -> fully replaced.
	dir := filepath.Join(hubRoot, "feedback", "inbox")
	existing := "feedback: v1\n" +
		"id: fb-20260801-dddddd\n" +
		"kind: bug\n" +
		"severity: minor\n" +
		"title: \"already has a resolution\"\n" +
		"summary: \"summary\"\n" +
		"resolution: |-\n" +
		"    stale line one\n" +
		"    stale line two\n" +
		"status: new\n"
	if err := os.WriteFile(filepath.Join(dir, "fb-20260801-dddddd.yaml"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	verdicts := []Verdict{
		{ID: "fb-20260801-cccccc", Status: "accepted", Resolution: "routed to backlog", Route: "backlog"},
		{ID: "fb-20260801-dddddd", Status: "rejected", Resolution: "fresh resolution text replaces the stale one"},
	}
	if _, err := ApplyVerdicts(hubRoot, verdicts, now); err != nil {
		t.Fatalf("ApplyVerdicts: %v", err)
	}

	// Case 1: resolution: inserted strictly before status:, nothing orphaned
	// after status:.
	raw1, err := os.ReadFile(filepath.Join(dir, "fb-20260801-cccccc.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines1 := strings.Split(string(raw1), "\n")
	statusLine1, resolutionLine1 := -1, -1
	for i, ln := range lines1 {
		if strings.HasPrefix(ln, "status:") {
			statusLine1 = i
		}
		if strings.HasPrefix(ln, "resolution:") {
			resolutionLine1 = i
		}
	}
	if resolutionLine1 == -1 || statusLine1 == -1 || resolutionLine1 >= statusLine1 {
		t.Fatalf("expected resolution: inserted before status:, got resolutionLine=%d statusLine=%d, file:\n%s", resolutionLine1, statusLine1, raw1)
	}
	for _, ln := range lines1[statusLine1+1:] {
		if ln != "" {
			t.Fatalf("unexpected content after status: line: %q, full file:\n%s", ln, raw1)
		}
	}

	// Case 2: stale continuation lines are gone, no orphans, replacement
	// value round-trips.
	raw2, err := os.ReadFile(filepath.Join(dir, "fb-20260801-dddddd.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw2), "stale line") {
		t.Fatalf("expected the stale resolution section to be fully replaced, got:\n%s", raw2)
	}
	var doc2 map[string]any
	if err := yaml.Unmarshal(raw2, &doc2); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if got, _ := doc2["resolution"].(string); got != "fresh resolution text replaces the stale one" {
		t.Fatalf("resolution = %q, want replacement text", got)
	}
	if got, _ := doc2["status"].(string); got != "rejected" {
		t.Fatalf("status = %q, want rejected", got)
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
