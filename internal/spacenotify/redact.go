package spacenotify

import (
	"strings"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/sensitive"
)

// maxDescriptionRunes bounds Message.Description to what is useful on a
// phone notification — well past a preview line, short of forwarding an
// entire long document into a chat message. Chosen here (spacenotify owns
// the message model) rather than reusing internal/cache's own
// maximumOperational* constants, which bound unrelated operational-wait
// text and are not exported.
const maxDescriptionRunes = 600

// redactedMarker replaces the WHOLE description when a credential shape is
// found — the same whole-value-replacement convention
// internal/cache/operational.go's safeOperationalText already uses (never
// a partial in-place substitution that could leave a shape half-visible).
const redactedMarker = "[redacted: a credential shape was present]"

// boundAndRedact bounds and redacts an artifact's own markdown body into
// Message.Description, returning the bounded/redacted text and the
// Truncated facts describing what was dropped and why (spec 03's own
// `truncated` field, AC3).
//
// Order matches internal/cache's own established precedent: bound FIRST,
// then test the bounded text for a credential shape. internal/sensitive
// is the ONLY redaction authority consulted (spec 03 §5: "internal/
// sensitive for redaction") — this package invents no second heuristic.
func boundAndRedact(body []byte) (string, []string) {
	var truncated []string

	text := strings.TrimSpace(string(body))
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	if n := utf8.RuneCountInString(text); n > maxDescriptionRunes {
		text = string([]rune(text)[:maxDescriptionRunes])
		truncated = append(truncated, "description truncated to 600 characters")
	}
	if sensitive.ContainsContent(text) {
		text = redactedMarker
		truncated = append(truncated, "description redacted: a credential shape was present")
	}
	return text, truncated
}
