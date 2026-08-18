// render_html.go is one of P4's two encoders over P3's message model (spec
// 04-render-and-transport.md "The two renderers"). It also owns compose(),
// the ONE composition entry point render_rich.go shares — AC3's "no second
// composition path" — and this phase's own small, closed bilingual
// vocabulary.
//
// On that vocabulary: spec 04's own text says "renderers add no strings of
// their own" and points at internal/viewvocab. internal/viewvocab.go is
// off this phase's allowlist, and — having read its full table — it carries
// no family for a delivery-time structural label ("From"/"Priority") or a
// drop notice ("description truncated to N characters"): those are not
// domain STATUS values the dashboard shows, they are this message model's
// OWN vocabulary. So the closed table lives here instead, in-package,
// reused verbatim by render_rich.go (same package, no import) rather than
// duplicated per renderer. Promoting it to internal/viewvocab, if a THIRD
// caller ever needs these exact words, is the lead's call.
package spacenotify

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/agentprompt"
)

// telegramTextBudget is sendMessage's own documented ceiling: "text is
// 1-4096 characters after entities parsing" (spec 04 §0.5, verified against
// the live Bot API docs 2026-08-18).
const telegramTextBudget = 4096

// composed is the ONE ordered, purely-factual representation of a Message
// both renderers translate into their own wire format (AC3: "Both
// renderers are driven from the same model with no second composition
// path"). Nothing here is presentational — no HTML markup, no Telegram
// rich-block types, no bold/anchor styling: that translation is each
// renderer's own job, over the SAME facts in the SAME order.
type composed struct {
	// digest is set only when the source Message's Class == ClassDigest; in
	// that case every other field below is unused (mirrors Message's own
	// "Digest carries no per-artifact facts" rule).
	digest *Digest

	headingClass string // Message.Class — a protocol identifier, not a sentence; not localised (see label's own doc comment)
	headingType  string // Artifact.Type
	title        string // Artifact.Title
	link         string // Links.Artifact — repo-relative (never a full URL; see render_html.go's own note on why this is never rendered as <a href>)

	// facts are Parties/Turn/Urgency, flattened into rows in the model's
	// own field order. All of them are the "optional fact table rows" the
	// spec's Budget rule may shed under length pressure — nothing here is
	// exempt except Description, Prompt, and Link, each handled separately.
	facts []factRow

	description string // "" when absent OR redacted (TruncationDescriptionRedacted covers "redacted")
	// descriptionNotes carries the model's own structured drop facts,
	// unresolved — each renderer turns a TruncationReason into words in the
	// route's own locale via truncationSentence, so the words are composed
	// exactly once per (code, locale) pair rather than once per renderer.
	descriptionNotes []TruncationReason

	prompt *agentprompt.AgentPrompt
}

// factRow is one (localisable key, already-formatted value) pair. value is
// "" only for factOverdue's boolean row, whose whole content IS its label.
type factRow struct {
	key   string
	value string
}

// compose turns msg into the shared composed representation. It is the
// ONLY function in this package that reads Message's Parties/Turn/Urgency
// fields into a fact list — RenderHTML and RenderRich both call it and nothing
// else builds one (AC3).
func compose(msg Message) composed {
	if msg.Class == ClassDigest {
		return composed{digest: msg.Digest}
	}

	c := composed{
		headingClass:     msg.Class,
		prompt:           msg.Prompt,
		description:      msg.Description,
		descriptionNotes: msg.Truncated,
	}
	if msg.Artifact != nil {
		c.headingType = msg.Artifact.Type
		c.title = msg.Artifact.Title
	}
	if msg.Links != nil {
		c.link = msg.Links.Artifact
	}
	if msg.Parties != nil {
		c.facts = append(c.facts, factRow{"from", msg.Parties.From})
		if len(msg.Parties.To) > 0 {
			c.facts = append(c.facts, factRow{"to", strings.Join(msg.Parties.To, ", ")})
		}
		if msg.Parties.For != "" {
			c.facts = append(c.facts, factRow{"for", msg.Parties.For})
		}
	}
	if msg.Turn != nil {
		if len(msg.Turn.Owners) > 0 {
			c.facts = append(c.facts, factRow{"owners", strings.Join(msg.Turn.Owners, ", ")})
		}
		if msg.Turn.Expected != "" {
			c.facts = append(c.facts, factRow{"expected", msg.Turn.Expected})
		}
		if msg.Turn.Why != "" {
			c.facts = append(c.facts, factRow{"why", msg.Turn.Why})
		}
		if msg.Turn.HumanGate != "" {
			c.facts = append(c.facts, factRow{"human_gate", msg.Turn.HumanGate})
		}
	}
	if msg.Urgency != nil {
		if msg.Urgency.Priority != "" {
			c.facts = append(c.facts, factRow{"priority", msg.Urgency.Priority})
		}
		if msg.Urgency.Deadline != "" {
			c.facts = append(c.facts, factRow{"deadline", msg.Urgency.Deadline})
		}
		if msg.Urgency.Overdue {
			c.facts = append(c.facts, factRow{"overdue", ""})
		}
	}
	return c
}

// vocabulary is this phase's closed bilingual label table — see this
// file's own package doc comment for why it lives here rather than in
// internal/viewvocab. Index 0 is English, index 1 is Russian.
var vocabulary = map[string][2]string{
	"from":        {"From", "От"},
	"to":          {"To", "Кому"},
	"for":         {"For", "Для"},
	"owners":      {"Owners", "Ответственные"},
	"expected":    {"Expected", "Ожидается"},
	"why":         {"Why", "Причина"},
	"human_gate":  {"Human gate", "Требуется согласование"},
	"priority":    {"Priority", "Приоритет"},
	"deadline":    {"Deadline", "Срок"},
	"overdue":     {"Overdue", "Просрочено"},
	"link":        {"Link", "Ссылка"},
	"description": {"Description", "Описание"},
	"moves":       {"Moves", "Доступные действия"},
	"ask_first":   {"Ask first", "Сначала спросить"},
	"doc":         {"Doc", "Документ"},
	"loop":        {"Loop", "Цикл"},
	"drafts":      {"Drafts", "Черновики"},
	"digest":      {"Digest", "Сводка"},
}

// label resolves key to its word in locale ("ru" for Russian, anything else
// for English — Route.Locale's own default, spec 03). key values that are
// PROTOCOL data (Message.Class, Artifact.Type, Urgency.Priority's own
// "p1"/"p2" vocabulary) are deliberately NOT run through this table: they
// are enum identifiers carried verbatim off the wire, the same way
// `render`'s own JSON output never translates them — only the STRUCTURAL
// prose this phase adds (labels, drop notices) is bilingual.
func label(key, locale string) string {
	pair, ok := vocabulary[key]
	if !ok {
		return key
	}
	if locale == "ru" {
		return pair[1]
	}
	return pair[0]
}

// truncationSentence turns one structured TruncationReason into the
// reader's-locale sentence — the ONE place either renderer turns a
// TruncationCode into words (TASK1's repair: "sentences are P4's job").
func truncationSentence(r TruncationReason, locale string) string {
	ru := locale == "ru"
	switch r.Code {
	case TruncationDescriptionBounded:
		if ru {
			return fmt.Sprintf("описание сокращено до %d символов", r.Bound)
		}
		return fmt.Sprintf("description truncated to %d characters", r.Bound)
	case TruncationDescriptionRedacted:
		if ru {
			return "описание скрыто: обнаружен признак учётных данных"
		}
		return "description redacted: a credential shape was present"
	default:
		// Closed-set switch, open-set defensive default: an unrecognised
		// code never fabricates a specific claim.
		if ru {
			return "содержимое изменено"
		}
		return "content was modified"
	}
}

// droppedForLengthSentence is the render-time (not model-time) notice the
// HTML renderer's own Budget rule adds when it drops the description to
// fit 4096 characters — a fact ABOUT THE RENDERING, never carried in
// Message.Truncated (which only ever describes what boundAndRedact did
// upstream, at model-build time).
func droppedForLengthSentence(locale string) string {
	if locale == "ru" {
		return "описание опущено: сообщение превышает лимит Telegram в 4096 символов"
	}
	return "description omitted: the message exceeds Telegram's 4096-character limit"
}

// promptText renders p as locale-labelled plain text lines — shared by both
// renderers (HTML wraps it in <pre><code>, Rich puts it in an
// InputRichBlockPreformatted; the text itself is composed exactly once).
func promptText(p *agentprompt.AgentPrompt, locale string) string {
	lines := []string{label("moves", locale) + ": " + strings.Join(p.Moves, ", ")}
	if len(p.AskFirst) > 0 {
		lines = append(lines, label("ask_first", locale)+": "+strings.Join(p.AskFirst, ", "))
	}
	if p.Doc != "" {
		lines = append(lines, label("doc", locale)+": "+p.Doc)
	}
	if p.Loop != "" {
		lines = append(lines, label("loop", locale)+": "+p.Loop)
	}
	if len(p.Drafts) > 0 {
		lines = append(lines, label("drafts", locale)+": "+strings.Join(p.Drafts, ", "))
	}
	return strings.Join(lines, "\n")
}

// htmlEscaper escapes exactly the three characters Telegram's HTML parse
// mode requires (spec 04 §0.5: "HTML style escapes only <, >, &") — never
// html.EscapeString, which also escapes quotes Telegram does not ask for.
// & is replaced first by construction (strings.Replacer scans the input
// once rather than chaining separate .Replace calls, so an escaped "&amp;"
// can never be re-escaped into "&amp;amp;").
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }

// RenderHTML encodes msg as sendMessage's own HTML parse-mode text (spec 04
// "The two renderers" table, HTML column): a bold class/type + title line,
// one `<b>key</b> value` fact row per fact, the description in an
// expandable blockquote, the agent prompt in <pre><code>, and the artifact
// link on a trailing line. Every interpolated value passes through
// escapeHTML (AC9).
//
// Budget: fits telegramTextBudget by dropping, in order, the description
// (replaced with a localised "omitted for length" notice) then the fact
// table's rows from the end — never the prompt block, never the link (spec
// 04's own Budget rule).
//
// Links.Artifact is never rendered as `<a href>`: model.go's own doc
// comment says this verb "never learns the repo's own hosting URL", so the
// value is a repo-relative PATH, not an absolute URL — Telegram's HTML
// parse mode requires href to be a real URL, and an `<a href="relative/
// path">` would either be dropped or reject the whole send. Both renderers
// show it as inline code instead (deviation from the spec's literal
// "<a href>"/"Footer" wording, documented in this phase's own report).
func RenderHTML(msg Message, locale string) string {
	c := compose(msg)
	if c.digest != nil {
		return renderDigestHTML(c.digest, locale)
	}

	dropDescription := false
	factLimit := len(c.facts)
	for {
		body := assembleHTML(c, locale, dropDescription, factLimit)
		if utf8.RuneCountInString(body) <= telegramTextBudget {
			return body
		}
		if !dropDescription {
			dropDescription = true
			continue
		}
		if factLimit > 0 {
			factLimit--
			continue
		}
		// Nothing left to drop but the prompt/link, which the spec's Budget
		// rule protects unconditionally — return over-budget rather than
		// silently dropping either.
		return body
	}
}

func assembleHTML(c composed, locale string, dropDescription bool, factLimit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>[%s] %s</b>\n", escapeHTML(c.headingClass), escapeHTML(c.headingType))
	fmt.Fprintf(&b, "%s\n", escapeHTML(c.title))

	for _, f := range c.facts[:factLimit] {
		l := label(f.key, locale)
		if f.value == "" {
			fmt.Fprintf(&b, "<b>%s</b>\n", escapeHTML(l))
			continue
		}
		fmt.Fprintf(&b, "<b>%s</b> %s\n", escapeHTML(l), escapeHTML(f.value))
	}

	if block, ok := descriptionBlockHTML(c, locale, dropDescription); ok {
		b.WriteString(block)
		b.WriteString("\n")
	}

	if c.prompt != nil {
		b.WriteString("<pre><code>")
		b.WriteString(escapeHTML(promptText(c.prompt, locale)))
		b.WriteString("</code></pre>\n")
	}

	if c.link != "" {
		fmt.Fprintf(&b, "%s <code>%s</code>", escapeHTML(label("link", locale)), escapeHTML(c.link))
	}

	return strings.TrimRight(b.String(), "\n")
}

func descriptionBlockHTML(c composed, locale string, dropped bool) (string, bool) {
	redacted := false
	var notes []TruncationReason
	for _, r := range c.descriptionNotes {
		if r.Code == TruncationDescriptionRedacted {
			redacted = true
			continue
		}
		notes = append(notes, r)
	}
	if c.description == "" && !redacted {
		return "", false // no description was ever present — AC2 only renders this block "when present"
	}
	if dropped {
		return fmt.Sprintf("<i>%s</i>", escapeHTML(droppedForLengthSentence(locale))), true
	}

	text := c.description
	if redacted {
		text = truncationSentence(TruncationReason{Code: TruncationDescriptionRedacted}, locale)
	}
	block := fmt.Sprintf("<blockquote expandable>%s</blockquote>", escapeHTML(text))
	for _, r := range notes {
		block += "\n<i>" + escapeHTML(truncationSentence(r, locale)) + "</i>"
	}
	return block, true
}

func renderDigestHTML(d *Digest, locale string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%s</b> — %d\n", escapeHTML(label("digest", locale)), d.Count)
	for _, item := range d.Items {
		fmt.Fprintf(&b, "• <code>%s</code> %s %s\n", escapeHTML(item.ID), escapeHTML(item.Type), escapeHTML(item.Title))
	}
	return strings.TrimRight(b.String(), "\n")
}
