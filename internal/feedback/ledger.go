package feedback

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
func AppendLedger(path string, item LedgerItem) (err error) {
	const op = "AppendLedger"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	lock, err := acquireLedgerLock(path)
	if err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	defer func() {
		if releaseErr := lock.release(); err == nil && releaseErr != nil {
			err = fmt.Errorf("feedback: %s: %w", op, releaseErr)
		}
	}()

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
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("feedback: %s: %w", op, err)
	}
	return nil
}

const (
	ledgerLockWait  = 2 * time.Second
	ledgerLockStale = 2 * time.Minute
	ledgerLockPoll  = 25 * time.Millisecond
)

type ledgerLock struct {
	path  string
	token string
}

// acquireLedgerLock serializes ledger read-modify-write across processes with
// a portable O_EXCL lock file. A crashed holder becomes recoverable after the
// stale bound; a live holder produces a bounded, actionable retry error.
func acquireLedgerLock(ledgerPath string) (*ledgerLock, error) {
	lockPath := ledgerPath + ".lock"
	deadline := time.Now().Add(ledgerLockWait)
	for {
		token, err := ledgerLockToken()
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, writeErr := file.WriteString(token); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(lockPath)
				return nil, writeErr
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(lockPath)
				return nil, closeErr
			}
			return &ledgerLock{path: lockPath, token: token}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > ledgerLockStale {
			_ = os.Remove(lockPath) // reason: recovery is re-raced through O_EXCL; an active replacement wins safely
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("ledger is locked by another feedback submit; retry")
		}
		time.Sleep(ledgerLockPoll)
	}
}

func ledgerLockToken() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (l *ledgerLock) release() error {
	current, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if string(current) != l.token {
		return nil
	}
	return os.Remove(l.path)
}
