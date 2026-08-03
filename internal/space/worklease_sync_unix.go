//go:build darwin || linux

package space

import "fmt"

func (s *WorkLeaseStore) syncDirectory() error {
	dir, err := s.root.Open(".")
	if err != nil {
		return fmt.Errorf("space: open work lease directory for sync: %w", err)
	}
	defer func() {
		_ = dir.Close() // reason: preserve the primary open/sync result
	}()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("space: sync work lease directory: %w", err)
	}
	return nil
}
