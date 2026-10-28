package feedback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// inboxRig is one Submitter wired for the local-inbox route only: its funnel is
// nil, so the test panics if the inbox route ever reaches one, because that
// route reaches no host (spec 08 §T1.2). Nothing here imports internal/space or
// internal/host: this package has no depguard rule, so every internal import in
// a test file counts against the cross-layer test-import ceiling.
type inboxRig struct {
	sub        *Submitter
	inboxDir   string
	ledgerPath string
}

func newInboxRig(t *testing.T) inboxRig {
	t.Helper()
	root := t.TempDir()
	rig := inboxRig{
		inboxDir:   filepath.Join(root, "data", "feedback", "inbox"),
		ledgerPath: filepath.Join(root, "project", ".a2a", "feedback", "ledger.yaml"),
	}
	rig.sub = NewSubmitter(nil, rig.ledgerPath, filepath.Join(root, "project"), "test-repo", SubmitConfig{
		RemoteURL:  "https://github.com/ydnikolaev/a2ahub",
		BaseBranch: "feedback-hub",
		InboxDir:   rig.inboxDir,
	})
	rig.sub.SetClockForTest(func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) })
	return rig
}

func writeInboxDraft(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fb-20260723-abc123.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	return path
}

// leftovers lists the entries of dir that are not final inbox reports.
func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read inbox dir: %v", err)
	}
	var stray []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".incoming-") {
			stray = append(stray, e.Name())
		}
	}
	return stray
}

// TestFileToInboxFilesTheNormalizedBytes pins §T1.2 steps 1-9: the filed file
// is the normalized draft (no comments), mode 0600 in a 0700 directory, with
// no credential present on a GitHub target, and the ledger row carries the
// inbox path and no PR.
func TestFileToInboxFilesTheNormalizedBytes(t *testing.T) {
	t.Parallel()
	rig := newInboxRig(t)
	draft := "# a comment the record must not carry\n" + validFeedbackYAML
	path := writeInboxDraft(t, draft)

	result, err := rig.sub.FileToInbox(context.Background(), path)
	if err != nil {
		t.Fatalf("FileToInbox: %v", err)
	}
	wantPath := filepath.Join(rig.inboxDir, "fb-20260723-abc123.yaml")
	if result.Inbox != wantPath || result.ID != "fb-20260723-abc123" {
		t.Fatalf("result = %+v, want inbox %q for the draft's id", result, wantPath)
	}
	if result.InboxExisting {
		t.Fatal("a first filing reported itself as already filed")
	}
	if string(result.Stage) != "none" || string(result.RemainingAction) != "none" || !result.LedgerRecorded {
		t.Fatalf("result = %+v, want stage none, remaining action none, ledger recorded", result)
	}
	if result.PRURL != "" || result.Branch != "" {
		t.Fatalf("result = %+v, want no PR and no branch: nothing reached the hub", result)
	}

	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read filed report: %v", err)
	}
	if string(got) != validFeedbackYAML {
		t.Fatalf("filed bytes differ from the normalized draft:\ngot:\n%s\nwant:\n%s", got, validFeedbackYAML)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("filed report mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(rig.inboxDir)
	if err != nil {
		t.Fatalf("stat inbox dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("inbox dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(rig.inboxDir)
	if err != nil {
		t.Fatalf("read inbox dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox holds %d entries, want exactly the one report: %v", len(entries), entries)
	}

	item, err := FindLedgerItem(rig.ledgerPath, "fb-20260723-abc123")
	if err != nil || item == nil {
		t.Fatalf("FindLedgerItem = %v, %v, want a row", item, err)
	}
	want := LedgerItem{ID: "fb-20260723-abc123", Kind: "bug", Title: "a2a sync reports clean but the mirror is stale", Inbox: wantPath, Filed: "2026-10-28T12:00:00Z"}
	if *item != want {
		t.Fatalf("ledger row = %+v, want %+v", *item, want)
	}
}

// TestFileToInboxRefusesWithoutADataRoot is §T1.2 step 4's second half: an
// empty InboxDir refuses, names the data root and writes nothing.
func TestFileToInboxRefusesWithoutADataRoot(t *testing.T) {
	t.Parallel()
	rig := newInboxRig(t)
	rig.sub.cfg.InboxDir = ""
	path := writeInboxDraft(t, validFeedbackYAML)

	result, err := rig.sub.FileToInbox(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "feedback: inbox: no data root") {
		t.Fatalf("FileToInbox error = %v, want it to open with %q", err, "feedback: inbox: no data root")
	}
	if result.ErrorCode != ErrorCodeInboxWrite {
		t.Fatalf("ErrorCode = %q, want %q", result.ErrorCode, ErrorCodeInboxWrite)
	}
	if verr := rig.sub.ValidateInboxPath(path); verr == nil || !strings.Contains(verr.Error(), "no data root") {
		t.Fatalf("ValidateInboxPath error = %v, want the same refusal before any batch writes", verr)
	}
	if _, serr := os.Stat(rig.ledgerPath); !os.IsNotExist(serr) {
		t.Fatalf("a ledger row was written for a refused filing (stat err = %v)", serr)
	}
}

// TestValidateInboxPathHasNoCredentialGuard separates the two routes: the
// same GitHub target with no token is refused by the hub pre-check and passes
// the inbox pre-check; a red report is refused by both.
func TestValidateInboxPathHasNoCredentialGuard(t *testing.T) {
	t.Parallel()
	rig := newInboxRig(t)
	good := writeInboxDraft(t, validFeedbackYAML)

	if err := rig.sub.ValidatePath(good); err == nil || !strings.Contains(err.Error(), "GitHub credential is required") {
		t.Fatalf("ValidatePath error = %v, want the credential refusal (hub route unchanged)", err)
	}
	if err := rig.sub.ValidateInboxPath(good); err != nil {
		t.Fatalf("ValidateInboxPath: %v, want nil with no credential", err)
	}

	red := writeInboxDraft(t, strings.Replace(validFeedbackYAML, "no_sensitive_content: true", "no_sensitive_content: false", 1))
	var refused *ValidationRefusedError
	if err := rig.sub.ValidateInboxPath(red); !errors.As(err, &refused) {
		t.Fatalf("ValidateInboxPath(red) error = %v, want *ValidationRefusedError", err)
	}
	result, err := rig.sub.FileToInbox(context.Background(), red)
	if !errors.As(err, &refused) {
		t.Fatalf("FileToInbox(red) error = %v, want *ValidationRefusedError", err)
	}
	if result.Inbox != "" {
		t.Fatalf("a red report was filed: %+v", result)
	}
	if got := leftovers(t, rig.inboxDir); len(got) != 0 {
		t.Fatalf("leftovers after a refusal: %v", got)
	}
	if _, serr := os.Stat(rig.inboxDir); !os.IsNotExist(serr) {
		t.Fatalf("the inbox directory was created for a refused report (stat err = %v)", serr)
	}
}

// TestFileToInboxIdempotentAndConflict is spec 08 AC6: filing the same draft
// twice is a no-op that says so, one inbox entry and one ledger row remain,
// and an edited draft under the filed id is a conflict that leaves the filed
// bytes untouched and names both paths.
func TestFileToInboxIdempotentAndConflict(t *testing.T) {
	t.Parallel()
	rig := newInboxRig(t)
	path := writeInboxDraft(t, validFeedbackYAML)

	first, err := rig.sub.FileToInbox(context.Background(), path)
	if err != nil || first.InboxExisting {
		t.Fatalf("first FileToInbox = %+v, %v, want a fresh filing", first, err)
	}
	second, err := rig.sub.FileToInbox(context.Background(), path)
	if err != nil {
		t.Fatalf("second FileToInbox: %v", err)
	}
	if !second.InboxExisting || second.Inbox != first.Inbox {
		t.Fatalf("second FileToInbox = %+v, want already filed at %q", second, first.Inbox)
	}
	if !strings.Contains(second.Note, "already in the local inbox") {
		t.Fatalf("Note = %q, want it to say the report is already in the local inbox", second.Note)
	}
	entries, err := os.ReadDir(rig.inboxDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("inbox entries = %v, %v, want exactly one", entries, err)
	}
	items, err := ReadLedger(rig.ledgerPath)
	if err != nil || len(items) != 1 {
		t.Fatalf("ledger = %+v, %v, want exactly one row", items, err)
	}

	edited := strings.Replace(validFeedbackYAML, "mirror HEAD stays put", "mirror HEAD moved backwards", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("edit draft: %v", err)
	}
	conflict, err := rig.sub.FileToInbox(context.Background(), path)
	if err == nil {
		t.Fatalf("FileToInbox of an edited draft = %+v, want a conflict error", conflict)
	}
	if conflict.ErrorCode != ErrorCodeInboxConflict {
		t.Fatalf("ErrorCode = %q, want %q", conflict.ErrorCode, ErrorCodeInboxConflict)
	}
	for _, want := range []string{first.Inbox, path, "a2a feedback new"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("conflict error %q must name %q", err.Error(), want)
		}
	}
	got, err := os.ReadFile(first.Inbox)
	if err != nil {
		t.Fatalf("read filed report: %v", err)
	}
	if string(got) != validFeedbackYAML {
		t.Fatalf("a conflict rewrote the filed report:\n%s", got)
	}
	if got := leftovers(t, rig.inboxDir); len(got) != 0 {
		t.Fatalf("leftovers after a conflict: %v", got)
	}
}

// TestFileToInboxConcurrent is spec 08 AC7: sixteen writers on one id leave
// exactly one report, every call is filed or already filed, and nothing is
// left behind; sixteen writers on sixteen ids leave sixteen reports. The
// ledger is stubbed to a counter so the 2s product lock budget is not what
// this test measures; the ledger's own concurrency is pinned in ledger_test.go.
func TestFileToInboxConcurrent(t *testing.T) {
	t.Parallel()
	const writers = 16

	run := func(t *testing.T, bodyFor func(i int) (id, body string)) (inboxDir string, filed, existing int) {
		t.Helper()
		rig := newInboxRig(t)
		var mu sync.Mutex
		rig.sub.SetAppendLedgerForTest(func(string, LedgerItem) error { return nil })
		paths := make([]string, writers)
		for i := range paths {
			_, body := bodyFor(i)
			paths[i] = filepath.Join(t.TempDir(), fmt.Sprintf("draft-%d.yaml", i))
			if err := os.WriteFile(paths[i], []byte(body), 0o644); err != nil {
				t.Fatalf("write draft: %v", err)
			}
		}
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, err := rig.sub.FileToInbox(context.Background(), paths[i])
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					t.Errorf("writer %d: %v", i, err)
					return
				}
				if result.InboxExisting {
					existing++
				} else {
					filed++
				}
			}()
		}
		wg.Wait()
		return rig.inboxDir, filed, existing
	}

	t.Run("one id", func(t *testing.T) {
		t.Parallel()
		dir, filed, existing := run(t, func(int) (string, string) { return "fb-20260723-abc123", validFeedbackYAML })
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read inbox: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "fb-20260723-abc123.yaml" {
			t.Fatalf("inbox entries = %v, want exactly fb-20260723-abc123.yaml", entries)
		}
		if filed != 1 || existing != writers-1 {
			t.Fatalf("filed = %d, existing = %d, want exactly one filing and %d already-filed", filed, existing, writers-1)
		}
	})

	t.Run("sixteen ids", func(t *testing.T) {
		t.Parallel()
		dir, filed, existing := run(t, func(i int) (string, string) {
			id := fmt.Sprintf("fb-20260723-%06x", i+1)
			return id, strings.Replace(validFeedbackYAML, "fb-20260723-abc123", id, 1)
		})
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read inbox: %v", err)
		}
		if len(entries) != writers || filed != writers || existing != 0 {
			t.Fatalf("entries = %d, filed = %d, existing = %d, want %d, %d, 0", len(entries), filed, existing, writers, writers)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".incoming-") {
				t.Fatalf("temp file %s left behind", e.Name())
			}
		}
	})
}

// TestFileToInboxFailureLeavesNothing is spec 08 AC8: a link failure after the
// temp write returns an inbox-write error and leaves no final file, no temp
// file and no ledger row.
func TestFileToInboxFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	rig := newInboxRig(t)
	rig.sub.link = func(oldname, newname string) error {
		if _, err := os.Stat(oldname); err != nil {
			t.Errorf("the temp file must exist when link runs: %v", err)
		}
		return errors.New("injected: hard links are not supported here")
	}
	path := writeInboxDraft(t, validFeedbackYAML)

	result, err := rig.sub.FileToInbox(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "injected: hard links are not supported here") {
		t.Fatalf("FileToInbox error = %v, want the link failure", err)
	}
	if result.ErrorCode != ErrorCodeInboxWrite {
		t.Fatalf("ErrorCode = %q, want %q", result.ErrorCode, ErrorCodeInboxWrite)
	}
	entries, rerr := os.ReadDir(rig.inboxDir)
	if rerr != nil {
		t.Fatalf("read inbox dir: %v", rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("inbox holds %v after a failed link, want nothing (no final file, no temp file)", entries)
	}
	if _, serr := os.Stat(rig.ledgerPath); !os.IsNotExist(serr) {
		t.Fatalf("a ledger row was written after a failed filing (stat err = %v)", serr)
	}
}

// TestFileToInboxLedgerFailureKeepsTheFiling: the file is filed and the ledger
// is not; the error is a *LedgerRecordError carrying the local-ledger code, and
// a re-run heals the ledger from the already-filed report.
func TestFileToInboxLedgerFailureKeepsTheFiling(t *testing.T) {
	t.Parallel()
	rig := newInboxRig(t)
	path := writeInboxDraft(t, validFeedbackYAML)

	rig.sub.SetAppendLedgerForTest(func(string, LedgerItem) error { return errors.New("disk full") })
	result, err := rig.sub.FileToInbox(context.Background(), path)
	var ledgerErr *LedgerRecordError
	if !errors.As(err, &ledgerErr) || result.ErrorCode != ErrorCodeLocalLedger {
		t.Fatalf("FileToInbox = %+v, %v, want a *LedgerRecordError with code %q", result, err, ErrorCodeLocalLedger)
	}
	if result.Inbox == "" {
		t.Fatalf("the filed report's path must still be reported: %+v", result)
	}

	rig.sub.SetAppendLedgerForTest(AppendLedger)
	healed, err := rig.sub.FileToInbox(context.Background(), path)
	if err != nil || !healed.InboxExisting || !healed.LedgerRecorded {
		t.Fatalf("re-run = %+v, %v, want already filed with the ledger recorded", healed, err)
	}
	if item, ferr := FindLedgerItem(rig.ledgerPath, "fb-20260723-abc123"); ferr != nil || item == nil || item.Inbox != result.Inbox {
		t.Fatalf("ledger row = %v, %v, want the inbox row", item, ferr)
	}
}
