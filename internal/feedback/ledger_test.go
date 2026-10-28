package feedback

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestReadLedger_MissingFile(t *testing.T) {
	t.Parallel()
	items, err := ReadLedger(filepath.Join(t.TempDir(), "ledger.yaml"))
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty list for a missing ledger, got %+v", items)
	}
}

// TestLedgerLockWaitDefaultIsTheProductBudget pins the wait a real
// `a2a feedback submit` gives another holder before it refuses. It exists
// because the test below RAISES that budget for itself, and a knob a test can
// turn is a knob that quietly becomes the product's value unless something
// asserts otherwise.
func TestLedgerLockWaitDefaultIsTheProductBudget(t *testing.T) {
	t.Parallel()

	if defaultLedgerLockWait != 2*time.Second {
		t.Fatalf("defaultLedgerLockWait = %v, want 2s — this is what an operator waits before being told to retry; changing it is a product decision, not a test fix", defaultLedgerLockWait)
	}
}

func TestAppendLedger_ConcurrentWritersLoseNoRows(t *testing.T) {
	// NOT t.Parallel(): this test mutates the package-level ledgerLockWait,
	// and a sibling running concurrently would observe the raised budget.

	// 24 writers serialise through one lock, so the last one polls at least
	// 23 x ledgerLockPoll before its turn plus every holder's own write. The
	// PRODUCT budget of 2s fits that unloaded and does not fit it inside
	// `make check`, where the rest of the suite is saturating the machine —
	// measured 2026-08-10, one red in an otherwise-green ceiling while this
	// same test passed 3/3 in isolation. The subject here is "no row is
	// lost", not "24 writers fit in the operator's patience", so the budget
	// is raised for this test rather than the contention reduced to hide it.
	restore := ledgerLockWait
	ledgerLockWait = 60 * time.Second
	t.Cleanup(func() { ledgerLockWait = restore })

	path := filepath.Join(t.TempDir(), ".a2a", "feedback", "ledger.yaml")
	const writers = 24
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("fb-20260728-%06x", n)
			errs <- AppendLedger(path, LedgerItem{
				ID: id, Kind: "bug", Title: id,
				PRURL: "https://example.invalid/" + id, Filed: "2026-07-28T00:00:00Z",
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent AppendLedger: %v", err)
		}
	}

	items, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != writers {
		t.Fatalf("ledger lost rows: got %d, want %d", len(items), writers)
	}
}

func TestReadLedger_EmptyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ledger.yaml")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	items, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty list for an empty ledger file, got %+v", items)
	}
}

func TestReadLedger_Corrupt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ledger.yaml")
	if err := os.WriteFile(path, []byte("items: [this is: not: valid"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadLedger(path); err == nil {
		t.Fatal("expected a clear error for a corrupt ledger, got nil")
	}
}

func TestAppendLedger_AppendAndIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".a2a", "feedback", "ledger.yaml")

	item1 := LedgerItem{ID: "fb-20260701-aaaaaa", Kind: "bug", Title: "t1", PRURL: "https://example.invalid/pr/1", Filed: "2026-07-23T00:00:00Z"}
	if err := AppendLedger(path, item1); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}
	item2 := LedgerItem{ID: "fb-20260702-bbbbbb", Kind: "docs", Title: "t2", PRURL: "https://example.invalid/pr/2", Filed: "2026-07-23T00:00:01Z"}
	if err := AppendLedger(path, item2); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}

	items, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2: %+v", len(items), items)
	}

	// Idempotent resubmit: appending the same id again must not duplicate.
	dup := LedgerItem{ID: item1.ID, Kind: item1.Kind, Title: "changed title (must not overwrite)", PRURL: item1.PRURL, Filed: item1.Filed}
	if err := AppendLedger(path, dup); err != nil {
		t.Fatalf("AppendLedger (dup): %v", err)
	}
	items, err = ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected AppendLedger to no-op on a duplicate id, len(items) = %d: %+v", len(items), items)
	}

	found, err := FindLedgerItem(path, item2.ID)
	if err != nil {
		t.Fatalf("FindLedgerItem: %v", err)
	}
	if found == nil || found.Title != "t2" {
		t.Fatalf("FindLedgerItem(%s) = %+v, want title t2", item2.ID, found)
	}

	missing, err := FindLedgerItem(path, "fb-nonexistent")
	if err != nil {
		t.Fatalf("FindLedgerItem: %v", err)
	}
	if missing != nil {
		t.Fatalf("FindLedgerItem for an unknown id = %+v, want nil", missing)
	}
}

// TestLedgerInboxKeyOmittedOnHubRows is spec 08 AC5's ledger half: a row the
// hub route writes carries no `inbox:` key, so its bytes are what they were
// before the local inbox existed, and a row filed to the inbox carries it.
func TestLedgerInboxKeyOmittedOnHubRows(t *testing.T) {
	t.Parallel()

	hub, err := yaml.Marshal(ledgerDoc{Items: []LedgerItem{{ID: "fb-20260701-aaaaaa", Kind: "bug", Title: "t1", PRURL: "https://example.invalid/pr/1", Filed: "2026-07-23T00:00:00Z"}}})
	if err != nil {
		t.Fatalf("marshal hub row: %v", err)
	}
	if strings.Contains(string(hub), "inbox") {
		t.Fatalf("a hub-route row must carry no inbox key, got:\n%s", hub)
	}

	filed, err := yaml.Marshal(ledgerDoc{Items: []LedgerItem{{ID: "fb-20260701-aaaaaa", Kind: "bug", Title: "t1", Inbox: "/data/feedback/inbox/fb-20260701-aaaaaa.yaml", Filed: "2026-07-23T00:00:00Z"}}})
	if err != nil {
		t.Fatalf("marshal inbox row: %v", err)
	}
	if !strings.Contains(string(filed), "inbox: /data/feedback/inbox/fb-20260701-aaaaaa.yaml") {
		t.Fatalf("an inbox-filed row must carry its inbox path, got:\n%s", filed)
	}
}

// TestAppendLedger_DeliveredLaterSetsPRURLOnce pins §T1.3's one update: an
// inbox row (empty pr_url) gains the PR URL when the report is delivered, keeps
// its inbox path and filed time, and is a no-op from then on.
func TestAppendLedger_DeliveredLaterSetsPRURLOnce(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".a2a", "feedback", "ledger.yaml")
	inboxRow := LedgerItem{ID: "fb-20260701-aaaaaa", Kind: "bug", Title: "t1", Inbox: "/data/inbox/fb-20260701-aaaaaa.yaml", Filed: "2026-07-23T00:00:00Z"}
	if err := AppendLedger(path, inboxRow); err != nil {
		t.Fatalf("AppendLedger (inbox): %v", err)
	}
	// Filing the same report to the inbox again changes nothing.
	if err := AppendLedger(path, inboxRow); err != nil {
		t.Fatalf("AppendLedger (inbox again): %v", err)
	}

	delivered := LedgerItem{ID: inboxRow.ID, Kind: "bug", Title: "t1", PRURL: "https://example.invalid/pr/1", Filed: "2026-08-01T00:00:00Z"}
	if err := AppendLedger(path, delivered); err != nil {
		t.Fatalf("AppendLedger (delivered): %v", err)
	}
	items, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	want := LedgerItem{ID: inboxRow.ID, Kind: "bug", Title: "t1", PRURL: "https://example.invalid/pr/1", Inbox: inboxRow.Inbox, Filed: inboxRow.Filed}
	if len(items) != 1 || items[0] != want {
		t.Fatalf("ledger = %+v, want exactly %+v", items, want)
	}

	// A different PR URL later must not rewrite a delivered row.
	other := delivered
	other.PRURL = "https://example.invalid/pr/2"
	if err := AppendLedger(path, other); err != nil {
		t.Fatalf("AppendLedger (other): %v", err)
	}
	items, err = ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(items) != 1 || items[0] != want {
		t.Fatalf("a delivered row was rewritten: ledger = %+v, want %+v", items, want)
	}
}
