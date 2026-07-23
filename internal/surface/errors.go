package surface

import "errors"

// Sentinel errors, one per failure class (internal/host idiom). Callers use
// errors.Is against these; a typed *Error carries the operation and
// offending input on top.
var (
	// ErrForeignLinkTarget is returned by Link when a surface's
	// <SkillsHome>/a2ahub entry already exists, is not a2ahub-owned (not a
	// symlink into the SSOT tree, not a stub carrying our marker tag), and
	// force is not set. Nothing is written.
	ErrForeignLinkTarget = errors.New("surface: link target is not an a2ahub-managed entry")
)

// Error is the small typed error every exported operation in this package
// returns on failure. It always wraps one of the sentinels above so callers
// can use errors.Is/As; it never panics on bad input.
type Error struct {
	// Op names the failing operation (e.g. "Link").
	Op string
	// Input is the offending input, kept for diagnostics (may be empty).
	Input string
	// Err is the wrapped sentinel (see the vars above).
	Err error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "surface: " + e.Op + ": " + e.Err.Error()
	}
	return "surface: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
