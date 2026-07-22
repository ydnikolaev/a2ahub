package release

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

// Grade is the T4 advisory severity every surface renders from the SAME
// underlying facts (current/latest/floor) — never re-derived per surface.
type Grade int

const (
	// GradeNone: nothing to advise (latest <= current and not required).
	GradeNone Grade = iota
	// GradeAvailable: a newer release exists (latest > current), the
	// running binary is not below any floor.
	GradeAvailable
	// GradeRequired: current < some space's floor — CC-085 already
	// refuses writes against that space. Stays advisory (exit 0, T4
	// amendment #3) even though it is the stronger grade.
	GradeRequired
)

// grade computes the shared T4 verdict: current/latest/floor unparseable
// inputs degrade to GradeNone (this package is advisory-display-only here;
// Resolve is the fail-closed decision path callers use before an actual
// update or write-refusal act).
func grade(current, latest, floor string) Grade {
	if floor != "" {
		if required, err := version.OlderThan(current, floor); err == nil && required {
			return GradeRequired
		}
	}
	if latest != "" {
		if older, err := version.OlderThan(current, latest); err == nil && older {
			return GradeAvailable
		}
	}
	return GradeNone
}

// FormatNotice renders the T4 full-sentence advisory text every "prose"
// surface (inbox/outbox stderr, doctor) uses verbatim, plus its Grade
// (GradeNone => text is ""). Every notice string lives HERE so all
// surfaces render identically (T4).
func FormatNotice(current, latest, floor, floorSpace string) (string, Grade) {
	g := grade(current, latest, floor)
	switch g {
	case GradeRequired:
		return fmt.Sprintf("update required: %s pins v%s, running v%s — run 'a2a update'", floorSpace, floor, current), g
	case GradeAvailable:
		return fmt.Sprintf("a2a update available: v%s -> v%s — run 'a2a update'", current, latest), g
	default:
		return "", g
	}
}

// FormatSegment renders the T4 short segment form the statusline (§7.5)
// appends as a trailing segment, plus its Grade. Kept alongside
// FormatNotice (same shared grade logic, same package) so every surface's
// wording stays in sync even though the statusline needs a terser shape
// than the full sentence.
func FormatSegment(current, latest, floor, floorSpace string) (string, Grade) {
	g := grade(current, latest, floor)
	switch g {
	case GradeRequired:
		return fmt.Sprintf("UPDATE REQUIRED (%s pins %s)", floorSpace, floor), g
	case GradeAvailable:
		return fmt.Sprintf("update v%s->v%s", current, latest), g
	default:
		return "", g
	}
}
