package lane

import (
	"errors"
	"fmt"
)

// blockErr formats a grammar-level parse failure at a 1-based line number.
func blockErr(line int, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}

// joinErrors aggregates every parse failure found in one scan so a single
// Load call reports every malformed declaration, not just the first —
// the same reason Reconcile and Coverage return a slice of Refusal rather
// than stopping at the first mismatch.
func joinErrors(errs []error) error {
	return errors.Join(errs...)
}
