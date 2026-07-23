package feedback

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatus_EmptyLedger(t *testing.T) {
	t.Parallel()
	rows, err := Status(filepath.Join(t.TempDir(), "ledger.yaml"), func(string) ([]byte, error) {
		t.Fatal("hub reader should not be called for an empty ledger")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows for an empty ledger, got %+v", rows)
	}
}

func TestStatus_ResolvesHubState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ledger.yaml")
	if err := AppendLedger(path, LedgerItem{ID: "fb-20260701-aaaaaa", Kind: "bug", Title: "t1", PRURL: "https://example.invalid/pr/1", Filed: "2026-07-23T00:00:00Z"}); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}

	reader := func(id string) ([]byte, error) {
		if id != "fb-20260701-aaaaaa" {
			t.Fatalf("unexpected id %q", id)
		}
		return []byte("status: accepted\nresolution: backlog fb-20260701-aaaaaa\n"), nil
	}
	rows, err := Status(path, reader)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].HubStatus != "accepted" || rows[0].Resolution != "backlog fb-20260701-aaaaaa" {
		t.Fatalf("rows[0] = %+v, want hub status accepted with resolution", rows[0])
	}
}

func TestStatus_UnreachableHubDegradesToUnknown(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ledger.yaml")
	if err := AppendLedger(path, LedgerItem{ID: "fb-20260701-aaaaaa", Kind: "bug", Title: "t1", PRURL: "https://example.invalid/pr/1", Filed: "2026-07-23T00:00:00Z"}); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}

	reader := func(string) ([]byte, error) { return nil, errors.New("network unreachable") }
	rows, err := Status(path, reader)
	if err != nil {
		t.Fatalf("Status: %v (network errors must degrade to unknown, exit 0)", err)
	}
	if len(rows) != 1 || rows[0].HubStatus != "unknown" {
		t.Fatalf("rows = %+v, want a single row with HubStatus unknown", rows)
	}
}

func TestDefaultHubReader_HappyAndNotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fb-20260701-1a2b3c") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("status: accepted\nresolution: backlog fb-20260701-1a2b3c\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	reader := DefaultHubReader(srv.Client(), srv.URL)

	raw, err := reader("fb-20260701-1a2b3c")
	if err != nil {
		t.Fatalf("DefaultHubReader: %v", err)
	}
	if !strings.Contains(string(raw), "status: accepted") {
		t.Fatalf("raw = %q, want status: accepted", raw)
	}

	if _, err := reader("fb-nonexistent"); err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
}
