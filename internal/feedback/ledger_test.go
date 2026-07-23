package feedback

import (
	"os"
	"path/filepath"
	"testing"
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
