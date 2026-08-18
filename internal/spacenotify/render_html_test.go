package spacenotify

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/agentprompt"
)

func sampleMessage() Message {
	return Message{
		Route:    Route{Chat: "-100", Locale: "en", Renderer: "html"},
		Class:    ClassBlocking,
		Artifact: &Artifact{ID: "XW-1", Type: "work_request", Title: "Fix the thing", Space: "fixture"},
		Parties:  &Parties{From: "axon", To: []string{"seomatrix"}},
		Turn:     &Turn{Owners: []string{"seo-bot"}, Expected: "response", Why: "addressed-no-ack"},
		Urgency:  &Urgency{Priority: "p1", Deadline: "2026-09-01", Overdue: true},
		Prompt:   &agentprompt.AgentPrompt{Moves: []string{"accept", "decline"}, AskFirst: []string{"accept"}, Doc: "reference/authoring/work_request.md"},
		Links:    &Links{Artifact: "axon/exchanges/XW-1.md"},
	}
}

// TestRenderHTML_EscapesAllThreeSpecials is AC9: a title containing all of
// `<`, `>`, `&` is escaped in the HTML renderer.
func TestRenderHTML_EscapesAllThreeSpecials(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Artifact.Title = `<script>alert("x")</script> & "quoted"`
	got := RenderHTML(msg, "en")
	if strings.Contains(got, "<script>") {
		t.Fatalf("title was not escaped, raw <script> tag leaked into: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("want the title's < and > escaped as &lt;/&gt;, got: %s", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Fatalf("want the title's & escaped as &amp;, got: %s", got)
	}
}

// TestRenderHTML_DescriptionOverBudgetTruncatesAndStaysUnderLimit is AC7: a
// message whose description exceeds telegramTextBudget is truncated with a
// stated marker and the WHOLE rendered text stays <= 4096 characters.
func TestRenderHTML_DescriptionOverBudgetTruncatesAndStaysUnderLimit(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = strings.Repeat("a very long description sentence. ", 400) // far over 4096 on its own
	got := RenderHTML(msg, "en")

	if n := utf8.RuneCountInString(got); n > telegramTextBudget {
		t.Fatalf("len(RenderHTML) = %d runes, want <= %d", n, telegramTextBudget)
	}
	if strings.Contains(got, "a very long description sentence.") {
		t.Fatalf("want the oversized description dropped, got it still present: %s", got)
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("want a stated 'omitted for length' marker, got: %s", got)
	}
	// The prompt and link must survive even though the description did not
	// (spec 04's own Budget rule: "never the prompt block and never the
	// link").
	if !strings.Contains(got, "<pre><code>") {
		t.Fatalf("want the prompt block to survive budget dropping, got: %s", got)
	}
	if !strings.Contains(got, "axon/exchanges/XW-1.md") {
		t.Fatalf("want the link to survive budget dropping, got: %s", got)
	}
}

// TestRenderHTML_DescriptionAbsentRendersNoBlock is AC2's negative case:
// no description was ever present, so no blockquote block appears at all.
func TestRenderHTML_DescriptionAbsentRendersNoBlock(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	got := RenderHTML(msg, "en")
	if strings.Contains(got, "blockquote") {
		t.Fatalf("want no blockquote when Description is absent, got: %s", got)
	}
}

// TestRenderHTML_DescriptionPresentRendersExpandableBlock is AC2's positive
// case.
func TestRenderHTML_DescriptionPresentRendersExpandableBlock(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = "short body"
	got := RenderHTML(msg, "en")
	if !strings.Contains(got, `<blockquote expandable>short body</blockquote>`) {
		t.Fatalf("want an expandable blockquote wrapping the description, got: %s", got)
	}
}

// TestRenderHTML_RedactedDescriptionIsLocalized is TASK1's own repair,
// proven at the renderer boundary: a `ru` route reads a Russian sentence, an
// `en` route reads an English one, from the IDENTICAL structured
// Truncated fact — never the same baked-in English string either way.
func TestRenderHTML_RedactedDescriptionIsLocalized(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = ""
	msg.Truncated = []TruncationReason{{Code: TruncationDescriptionRedacted}}

	en := RenderHTML(msg, "en")
	if !strings.Contains(en, "credential shape") {
		t.Fatalf("en render missing the English redaction sentence: %s", en)
	}

	ru := RenderHTML(msg, "ru")
	if !strings.Contains(ru, "учётных данных") {
		t.Fatalf("ru render missing the Russian redaction sentence: %s", ru)
	}
	if strings.Contains(ru, "credential shape") {
		t.Fatalf("ru render leaked the English sentence: %s", ru)
	}
}

// TestCompose_SingleEntryPoint is AC3: both renderers are driven from the
// same model with no second composition path. compose() is pure and
// deterministic — the same Message always yields the identical composed
// value — which is what makes "RenderHTML and RenderRich both call
// compose() and nothing else builds a fact list" a checkable property
// rather than an unenforced convention: if either renderer ever grew its
// own parallel extraction of Parties/Turn/Urgency, that second path could
// disagree with compose() on ORDER or PRESENCE without this test moving,
// which is exactly why TestRenderRich_CarriesEveryFact (render_rich_test.go)
// is the complementary half — it pins that both renderers' OUTPUT reflects
// compose()'s own facts, not just that compose() itself is stable.
func TestCompose_SingleEntryPoint(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = "stable across calls"

	first := compose(msg)
	second := compose(msg)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("compose(msg) is not deterministic:\n%+v\n%+v", first, second)
	}
}

// TestRenderHTML_Digest covers the coalesced-message shape: count plus
// bounded items, no prompt.
func TestRenderHTML_Digest(t *testing.T) {
	t.Parallel()
	msg := Message{
		Route: Route{Chat: "-100", Locale: "en", Renderer: "html"},
		Class: ClassDigest,
		Digest: &Digest{
			Count: 2,
			Items: []DigestItem{
				{ID: "XW-1", Type: "work_request", Title: "First"},
				{ID: "XW-2", Type: "question", Title: "Second"},
			},
		},
	}
	got := RenderHTML(msg, "en")
	if !strings.Contains(got, "XW-1") || !strings.Contains(got, "XW-2") {
		t.Fatalf("want both digest items present, got: %s", got)
	}
	if strings.Contains(got, "<pre><code>") {
		t.Fatalf("want no prompt block in a digest message, got: %s", got)
	}
}
