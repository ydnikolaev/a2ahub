package release

import (
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

// Grade is the T4 advisory severity every surface renders from the SAME
// underlying facts (current/latest/floor) — never re-derived per surface.
type Grade int

const (
	// GradeNone means nothing to advise (latest <= current and not required).
	GradeNone Grade = iota
	// GradeAvailable means a newer release exists (latest > current) and the
	// running binary is not below any floor.
	GradeAvailable
	// GradeRequired means current < some space's floor — CC-085 already
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

// NoticeInfo is the full structured update fact — the single home for the
// update_available / required booleans AND the rendered advisory text, so
// every surface's JSON object (a2a update --json, inbox --json, MCP a2a_read)
// and advisory line agree value-for-value. update_available and required are
// ORTHOGONAL pure version comparisons; Grade/Segment/Sentence are the display
// rendering (Grade picks the single strongest message to show).
type NoticeInfo struct {
	Current         string
	Latest          string
	Floor           string
	FloorSpace      string
	UpdateAvailable bool // a newer release exists: current strictly older than latest
	Required        bool // running binary below a space floor: current strictly older than floor
	Grade           Grade
	Segment         string
	Sentence        string
}

// Info computes the shared T4 notice facts (the SSOT the CLI --json object,
// the cache UpdateNotice, and the MCP a2a_read update field all derive from,
// so update_available/required never diverge across surfaces). Unparseable
// inputs degrade each boolean to false (advisory-display-only; Resolve is the
// fail-closed decision path for the actual update/write act).
func Info(current, latest, floor, floorSpace string) NoticeInfo {
	avail := false
	if latest != "" {
		if older, err := version.OlderThan(current, latest); err == nil && older {
			avail = true
		}
	}
	required := false
	if floor != "" {
		if older, err := version.OlderThan(current, floor); err == nil && older {
			required = true
		}
	}
	seg, g := FormatSegment(current, latest, floor, floorSpace)
	sen, _ := FormatNotice(current, latest, floor, floorSpace)
	return NoticeInfo{
		Current:         current,
		Latest:          latest,
		Floor:           floor,
		FloorSpace:      floorSpace,
		UpdateAvailable: avail,
		Required:        required,
		Grade:           g,
		Segment:         seg,
		Sentence:        sen,
	}
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

// InfoFor is Info for a specific build (D-040 point 5). A release or dev build
// gets exactly Info's answer. A LOCAL build is a gated build of private main:
// private main already holds every public release's notes, so "a newer release
// exists" is never advice to it, and the next delivery stamps the new version.
// It therefore never reports update_available and renders no advisory. A floor
// above its stamp is still required (the space refuses its writes), but the
// remedy is a delivery, not `a2a update`: a dated note for the floor's version
// must already be in main, so the delivered build is merely stale.
func InfoFor(b version.Build, latest, floor, floorSpace string) NoticeInfo {
	info := Info(b.Version, latest, floor, floorSpace)
	if !b.IsLocal() {
		return info
	}
	info.UpdateAvailable = false
	if !info.Required {
		info.Grade, info.Segment, info.Sentence = GradeNone, "", ""
		return info
	}
	info.Grade = GradeRequired
	info.Segment = fmt.Sprintf("UPDATE REQUIRED (%s pins %s; run make deliver)", floorSpace, floor)
	info.Sentence = fmt.Sprintf("update required: %s pins v%s; this local build is v%s — run `make deliver` in the a2ahub checkout", floorSpace, floor, b.Version)
	return info
}
