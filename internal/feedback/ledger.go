package feedback

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LedgerItem is one `.a2a/feedback/ledger.yaml` row (§T1: consumer-local,
// gitignored with the rest of `.a2a/` private state).
type LedgerItem struct {
	ID    string `yaml:"id"`
	Kind  string `yaml:"kind"`
	Title string `yaml:"title"`
	PRURL string `yaml:"pr_url"`
	Filed string `yaml:"filed"` // RFC3339
}

type ledgerDoc struct {
	Items []LedgerItem `yaml:"items"`
}

// ReadLedger reads path's ledger. A missing file returns an empty slice,
// nil error (§6: "empty, missing file" is not a failure). A malformed
// ledger returns a clear wrapped error — never a silent empty list, which
// would make a corrupt ledger indistinguishable from "nothing filed yet".
func ReadLedger(path string) ([]LedgerItem, error) {
	const op = "ReadLedger"
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("feedback: %s: %w", op, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil
	}
	var doc ledgerDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("feedback: %s: %s: corrupt ledger: %w", op, path, err)
	}
	return doc.Items, nil
}

// FindLedgerItem returns path's ledger row for id, or nil if absent.
func FindLedgerItem(path, id string) (*LedgerItem, error) {
	items, err := ReadLedger(path)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ID == id {
			found := it
			return &found, nil
		}
	}
	return nil, nil
}

// AppendLedger appends item to path's ledger (read-modify-write). It is
// idempotent per id: if a row with item.ID already exists, AppendLedger
// is a no-op (submit.go's own idempotent-resubmit contract relies on
// this).
func AppendLedger(path string, item LedgerItem) error {
	const op = "AppendLedger"
	items, err := ReadLedger(path)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.ID == item.ID {
			return nil
		}
	}
	items = append(items, item)
	out, merr := yaml.Marshal(ledgerDoc{Items: items})
	if merr != nil {
		return fmt.Errorf("feedback: %s: %w", op, merr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	return nil
}
