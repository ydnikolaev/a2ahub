package spacenotify

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRenderRich_CarriesEveryFact is the "parity" testing requirement (spec
// 04 §6): both renderers carry the same fact set — a fact present in one
// and absent in the other must fail this test. It checks every fact VALUE
// compose() produces appears as a substring of BOTH renderers' output, so a
// renderer that silently drops a fact (or adds one the other lacks) is
// caught by construction rather than by a hand-maintained list per class.
func TestRenderRich_CarriesEveryFact(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = "a short description"

	html := RenderHTML(msg, "en")
	richPayload, err := json.Marshal(RenderRich(msg, "en"))
	if err != nil {
		t.Fatalf("marshal rich message: %v", err)
	}
	rich := string(richPayload)

	c := compose(msg)
	values := []string{c.title, c.description}
	for _, f := range c.facts {
		if f.value != "" {
			values = append(values, f.value)
		}
	}
	values = append(values, c.prompt.Moves...)
	values = append(values, c.link)

	for _, v := range values {
		if v == "" {
			continue
		}
		if !strings.Contains(html, v) {
			t.Errorf("fact %q present in compose() but missing from RenderHTML:\n%s", v, html)
		}
		if !strings.Contains(rich, v) {
			t.Errorf("fact %q present in compose() but missing from RenderRich:\n%s", v, rich)
		}
	}
}

// TestRenderRich_PromptIsAPreformattedBlock is AC1: the prompt renders
// inside a code block in the rich renderer too — InputRichBlockPreformatted
// (type "pre"), the same semantic RenderHTML's <pre><code> carries.
func TestRenderRich_PromptIsAPreformattedBlock(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	payload, err := json.Marshal(RenderRich(msg, "en"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"type":"pre"`) {
		t.Fatalf("want a preformatted (\"pre\") block for the prompt, got: %s", payload)
	}
}

// TestRenderRich_DescriptionIsADetailsBlock is AC2's rich-side proof: the
// description renders as InputRichBlockDetails (type "details").
func TestRenderRich_DescriptionIsADetailsBlock(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = "expand me"
	payload, err := json.Marshal(RenderRich(msg, "en"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"type":"details"`) {
		t.Fatalf("want a details block for the description, got: %s", payload)
	}
}

// TestRenderRich_NoDescriptionOmitsDetailsBlock is the "a class with no
// description" edge case spec 04 §6 names explicitly.
func TestRenderRich_NoDescriptionOmitsDetailsBlock(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	payload, err := json.Marshal(RenderRich(msg, "en"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(payload), `"type":"details"`) {
		t.Fatalf("want no details block when Description is absent, got: %s", payload)
	}
}

// TestRenderRich_Digest is the "a digest" edge case spec 04 §6 names
// explicitly.
func TestRenderRich_Digest(t *testing.T) {
	t.Parallel()
	msg := Message{
		Route: Route{Chat: "-100", Locale: "en", Renderer: "rich"},
		Class: ClassDigest,
		Digest: &Digest{
			Count: 1,
			Items: []DigestItem{{ID: "XW-9", Type: "question", Title: "A question"}},
		},
	}
	payload, err := json.Marshal(RenderRich(msg, "en"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), "XW-9") {
		t.Fatalf("want the digest item present, got: %s", payload)
	}
}

// TestRenderRich_RedactedDescriptionIsLocalized mirrors
// TestRenderHTML_RedactedDescriptionIsLocalized on the rich side.
func TestRenderRich_RedactedDescriptionIsLocalized(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Description = ""
	msg.Truncated = []TruncationReason{{Code: TruncationDescriptionRedacted}}

	ru, err := json.Marshal(RenderRich(msg, "ru"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(ru), "учётных данных") {
		t.Fatalf("want the Russian redaction sentence, got: %s", ru)
	}
}
