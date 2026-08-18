// render_rich.go is P4's second encoder over P3's message model: Bot API
// 10.1's sendRichMessage / InputRichMessage / InputRichBlock* family (spec
// 04 §0.5, added 2026-06-11).
//
// The method's parameter list, message_thread_id support and old-client
// behaviour were flagged UNCONFIRMED at spec-writing time (the discovery
// page truncated). This phase verified them against the LIVE
// core.telegram.org/bots/api page on 2026-08-18 (egress was available in
// this run's sandbox) rather than guessing a wire shape:
//
//   - sendRichMessage's own parameter table: business_connection_id
//     (optional), chat_id (required), message_thread_id (optional Integer —
//     "for forum supergroups... only", the identical shape and wording
//     sendMessage's own message_thread_id carries), direct_messages_topic_id,
//     rich_message (required InputRichMessage), disable_notification,
//     protect_content, allow_paid_broadcast, message_effect_id,
//     suggested_post_parameters, reply_parameters, reply_markup.
//   - message_thread_id addresses a forum topic exactly as sendMessage's
//     own parameter does — so a route with `topic` set is NOT refused for
//     renderer:rich (the spec's own conditional stop-condition does not
//     apply): SendRich passes msg.Route.Topic through unchanged, present
//     iff set (AC12), the same as SendHTML.
//   - InputRichMessage: exactly one of `blocks` (Array of InputRichBlock),
//     `html`, or `markdown` — this package always uses `blocks`, never the
//     html/markdown shorthands, so nothing here duplicates render_html.go's
//     own escaping logic.
//   - InputRichBlockSectionHeading{type:"heading", text, size 1-6},
//     InputRichBlockTable{type:"table", cells: [][]RichBlockTableCell},
//     InputRichBlockDetails{type:"details", summary, blocks, is_open},
//     InputRichBlockPreformatted{type:"pre", text, language},
//     InputRichBlockFooter{type:"footer", text} — all confirmed present with
//     exactly these fields.
//
// Full verdict (including the discrepancy against the spec's own literal
// "SectionHeading + Anchor" / "Footer" wording): this phase's own report,
// rich_surface_verdict.
package spacenotify

import "fmt"

// inputRichMessage is Bot API 10.1's InputRichMessage, restricted to the
// `blocks` form (verified live 2026-08-18).
type inputRichMessage struct {
	Blocks []any `json:"blocks"`
}

// The block types below carry ONLY the fields this phase's renderer table
// needs — each verified live against its own #inputrichblock<name> anchor.

type richBlockSectionHeading struct {
	Type string `json:"type"` // always "heading"
	Text string `json:"text"`
	Size int    `json:"size,omitempty"`
}

type richBlockParagraph struct {
	Type string `json:"type"` // always "paragraph"
	Text string `json:"text"`
}

type richBlockTable struct {
	Type  string                 `json:"type"` // always "table"
	Cells [][]richBlockTableCell `json:"cells"`
}

// richBlockTableCell is RichBlockTableCell — the SAME type name the live
// docs use for both InputRichBlockTable's cells and the read-side
// RichBlockTable's cells (there is no separate "Input" cell type).
type richBlockTableCell struct {
	Text     string `json:"text,omitempty"`
	IsHeader bool   `json:"is_header,omitempty"`
}

type richBlockDetails struct {
	Type    string `json:"type"` // always "details"
	Summary string `json:"summary"`
	Blocks  []any  `json:"blocks"`
	IsOpen  bool   `json:"is_open,omitempty"`
}

type richBlockPreformatted struct {
	Type     string `json:"type"` // always "pre"
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
}

type richBlockFooter struct {
	Type string `json:"type"` // always "footer"
	Text string `json:"text"`
}

// richHeadingSize is InputRichBlockSectionHeading's own size field: "1-6, 1
// is the largest, 6 is the smallest" (verified live). 3 renders as an
// h3-equivalent — prominent without competing with Telegram's own message
// chrome, the same weight <b> gives the HTML renderer's own heading line.
const richHeadingSize = 3

// RenderRich encodes msg as Bot API 10.1's InputRichMessage (spec 04 "The
// two renderers" table, Rich column), from the SAME compose() output
// RenderHTML consumes (AC3).
//
// Two deviations from the spec table's literal wording, both forced by
// what the live API actually offers (see this file's own package doc
// comment and rich_surface_verdict):
//
//   - "class + type + title | ... | SectionHeading + Anchor": the live
//     InputRichBlockAnchor is a same-message jump target (<a name=...>),
//     not a hyperlink — it cannot carry the artifact's own link the way the
//     spec's HTML column's "<a href>" does. This renderer uses
//     SectionHeading alone for the heading and a Paragraph for the title;
//     the artifact reference travels in the Footer block instead (below),
//     matching where RenderHTML puts it.
//   - "links | ... | Footer": Links.Artifact is a repo-relative PATH, not
//     an absolute URL (model.go's own doc comment), so it is placed in the
//     Footer as plain text, never as a RichTextUrl entity — the same
//     "never a href built from a non-URL" decision RenderHTML makes.
func RenderRich(msg Message, locale string) inputRichMessage {
	c := compose(msg)
	if c.digest != nil {
		return renderDigestRich(c.digest, locale)
	}

	var blocks []any
	blocks = append(blocks, richBlockSectionHeading{
		Type: "heading",
		Text: fmt.Sprintf("[%s] %s", c.headingClass, c.headingType),
		Size: richHeadingSize,
	})
	if c.title != "" {
		blocks = append(blocks, richBlockParagraph{Type: "paragraph", Text: c.title})
	}

	if len(c.facts) > 0 {
		var rows [][]richBlockTableCell
		for _, f := range c.facts {
			value := f.value
			if value == "" {
				value = label(f.key, locale) // the boolean "overdue" row: the label IS the content
			}
			rows = append(rows, []richBlockTableCell{
				{Text: label(f.key, locale), IsHeader: true},
				{Text: value},
			})
		}
		blocks = append(blocks, richBlockTable{Type: "table", Cells: rows})
	}

	if desc, notes, ok := richDescription(c, locale); ok {
		detailBlocks := []any{richBlockParagraph{Type: "paragraph", Text: desc}}
		for _, n := range notes {
			detailBlocks = append(detailBlocks, richBlockParagraph{Type: "paragraph", Text: n})
		}
		blocks = append(blocks, richBlockDetails{
			Type:    "details",
			Summary: label("description", locale),
			Blocks:  detailBlocks,
		})
	}

	if c.prompt != nil {
		blocks = append(blocks, richBlockPreformatted{Type: "pre", Text: promptText(c.prompt, locale)})
	}

	if c.link != "" {
		blocks = append(blocks, richBlockFooter{Type: "footer", Text: label("link", locale) + ": " + c.link})
	}

	return inputRichMessage{Blocks: blocks}
}

// richDescription mirrors descriptionBlockHTML's redaction handling (same
// TruncationReason facts, resolved through the SAME truncationSentence) but
// carries no render-time budget concern: the spec's Budget rule is scoped
// to "The HTML renderer" by its own text, and Bot API 10.1's own limits for
// rich messages were not part of what this phase's live-doc check
// confirmed — see rich_surface_verdict.
func richDescription(c composed, locale string) (string, []string, bool) {
	redacted := false
	var notes []string
	for _, r := range c.descriptionNotes {
		if r.Code == TruncationDescriptionRedacted {
			redacted = true
			continue
		}
		notes = append(notes, truncationSentence(r, locale))
	}
	if c.description == "" && !redacted {
		return "", nil, false
	}
	text := c.description
	if redacted {
		text = truncationSentence(TruncationReason{Code: TruncationDescriptionRedacted}, locale)
	}
	return text, notes, true
}

func renderDigestRich(d *Digest, locale string) inputRichMessage {
	blocks := []any{
		richBlockSectionHeading{
			Type: "heading",
			Text: fmt.Sprintf("%s — %d", label("digest", locale), d.Count),
			Size: richHeadingSize,
		},
	}
	if len(d.Items) > 0 {
		var rows [][]richBlockTableCell
		for _, item := range d.Items {
			rows = append(rows, []richBlockTableCell{
				{Text: item.Type, IsHeader: true},
				{Text: item.ID},
				{Text: item.Title},
			})
		}
		blocks = append(blocks, richBlockTable{Type: "table", Cells: rows})
	}
	return inputRichMessage{Blocks: blocks}
}
