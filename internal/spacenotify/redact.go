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

// boundAndRedact bounds and redacts an artifact's own markdown body into
// Message.Description, returning the bounded/redacted text and the
// structured Truncated facts describing what was dropped and why (spec 03's
// own `truncated` field, AC3) — never an English sentence baked into a fact
// field (04-render-and-transport.md TASK1's repair: "Fields are facts;
// sentences are P4's job", model.go's own first line). A `ru` route and an
// `en` route read the IDENTICAL TruncationReason values from this
// function; only the renderer, later, turns a code into words in the
// route's own locale.
//
// Order matches internal/cache's own established precedent: bound FIRST,
// then test the bounded text for a credential shape. internal/sensitive
// is the ONLY redaction authority consulted (spec 03 §5: "internal/
// sensitive for redaction") — this package invents no second heuristic.
//
// On redaction, Description becomes "" rather than a fixed marker string —
// a marker IS a sentence, in one language, which is exactly what this
// repair removes. The renderer composes the reader's-locale sentence from
// TruncationDescriptionRedacted alone.
func boundAndRedact(body []byte) (string, []TruncationReason) {
	var truncated []TruncationReason

	text := strings.TrimSpace(string(body))
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	if n := utf8.RuneCountInString(text); n > maxDescriptionRunes {
		text = string([]rune(text)[:maxDescriptionRunes])
		truncated = append(truncated, TruncationReason{Code: TruncationDescriptionBounded, Bound: maxDescriptionRunes})
	}
	if sensitive.ContainsContent(text) {
		text = ""
		truncated = append(truncated, TruncationReason{Code: TruncationDescriptionRedacted})
	}
	return text, truncated
}
