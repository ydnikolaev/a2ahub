package livee2e

import (
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// ErrConformanceLocationUnstable marks a conformance violation that cannot be
// acted on: a malformed JSON pointer, or no message at all.
var ErrConformanceLocationUnstable = errors.New("conformance violation carries no stable structured location")

// stableConformanceLocation reports whether one conformance violation carries
// the stable structured location LE-OC-08 requires, and why it does not.
//
// The EMPTY pointer is a location, not a missing one: RFC 6901 spells the
// whole-document pointer as "", which is exactly what a root-level `required`
// or `type` failure legitimately carries. Spec 06 §5.4 makes the conformance
// report expose the instance/schema JSON pointers the approved library
// reports, and spec 07's LE-OC-08 row asks for a *stable* instance/schema
// location — never a non-empty one. This scenario used to demand a non-empty
// schema pointer, which no spec asks for and which the product cannot satisfy
// for a document-level violation without inventing a pointer the library never
// produced; it also contradicted the sibling instance pointer, which the same
// scenario has always accepted as empty.
//
// What must hold, and what this checks, is that a consumer can machine-read
// the location: both pointers are well-formed RFC 6901 (empty, or "/"-rooted
// with every "~" escaped), and the violation says something. Byte-stability
// across repeated checks stays the caller's assertion.
func stableConformanceLocation(violation contract.InstanceViolation) error {
	if err := validJSONPointer(violation.InstancePointer); err != nil {
		return fmt.Errorf("%w: instance pointer: %w", ErrConformanceLocationUnstable, err)
	}
	if err := validJSONPointer(violation.SchemaPointer); err != nil {
		return fmt.Errorf("%w: schema pointer: %w", ErrConformanceLocationUnstable, err)
	}
	if violation.Message == "" {
		return fmt.Errorf("%w: message is empty", ErrConformanceLocationUnstable)
	}
	return nil
}

// validJSONPointer applies RFC 6901 §3: a pointer is either empty (the whole
// document) or a sequence of "/"-prefixed reference tokens in which "~" only
// ever appears as the escape "~0" or "~1".
func validJSONPointer(pointer string) error {
	if pointer == "" {
		return nil
	}
	if pointer[0] != '/' {
		return fmt.Errorf("%q is neither empty nor rooted at %q", pointer, "/")
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || (pointer[index+1] != '0' && pointer[index+1] != '1') {
			return fmt.Errorf("%q has an unescaped %q", pointer, "~")
		}
		index++
	}
	return nil
}
