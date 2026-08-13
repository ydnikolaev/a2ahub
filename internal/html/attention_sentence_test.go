package html

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

type attentionSentenceGolden struct {
	Reason cache.ReasonCode `json:"reason"`
	RU     string           `json:"ru"`
	EN     string           `json:"en"`
}

func TestAttentionSentencesMatchGolden(t *testing.T) {
	t.Parallel()

	item := cache.Item{
		ID: "</script><untrusted-id>", Title: "specs/should-stay-data.md",
		WaitingOn: []string{"atlas", "checkout"}, ExpectedTransition: "respond",
		NeededBy: "2026-08-20", HumanGate: "G3",
		RuleIdentity: "question/submitted",
		Why:          "P5 AC1 (specs/05-declared-nature.md): diagnostic only",
	}
	actual := make([]attentionSentenceGolden, 0, len(cache.ReasonCodes())+1)
	for _, reason := range cache.ReasonCodes() {
		sentence := composeAttentionSentence(reason, item)
		actual = append(actual, attentionSentenceGolden{Reason: reason, RU: sentence.RU, EN: sentence.EN})
	}
	unknownRule := item
	unknownRule.RuleIdentity = "future/unknown"
	unknown := composeAttentionSentence(cache.ReasonAddressedNoAck, unknownRule)
	actual = append(actual, attentionSentenceGolden{Reason: unknownAttentionReason, RU: unknown.RU, EN: unknown.EN})

	encoded, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		t.Fatalf("marshal actual sentences: %v", err)
	}
	want, err := os.ReadFile("testdata/attention_sentences.golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), encoded) {
		t.Fatalf("attention sentence golden mismatch\nwant:\n%s\ngot:\n%s", want, encoded)
	}

	for _, row := range actual {
		if row.RU == "" || row.EN == "" {
			t.Errorf("%s has an empty locale: %+v", row.Reason, row)
		}
		for _, forbidden := range []string{"specs/", "P5 AC1", item.ID, item.Title, "future/unknown"} {
			if strings.Contains(row.RU, forbidden) || strings.Contains(row.EN, forbidden) {
				t.Errorf("%s sentence leaked diagnostic/untrusted/raw-code data %q: %+v", row.Reason, forbidden, row)
			}
		}
	}
}

func TestAttentionSentenceUsesNormativeFirstReasonAndExplicitUnknown(t *testing.T) {
	t.Parallel()

	item := cache.Item{
		Reasons:      []string{string(cache.ReasonDeclined), string(cache.ReasonStaleSLA)},
		RuleIdentity: "question/declined",
	}
	if got, want := attentionSentence(item), composeAttentionSentence(cache.ReasonDeclined, item); got != want {
		t.Fatalf("attentionSentence = %+v, want first normative reason %+v", got, want)
	}

	item.Reasons = []string{"not-in-the-vocabulary"}
	got := attentionSentence(item)
	want := composeAttentionSentence(unknownAttentionReason, item)
	if got != want {
		t.Fatalf("unknown reason = %+v, want explicit fallback %+v", got, want)
	}
	if strings.Contains(got.RU, "not-in-the-vocabulary") || strings.Contains(got.EN, "not-in-the-vocabulary") {
		t.Fatalf("unknown reason fallback leaked raw code: %+v", got)
	}

	item.Reasons = nil
	if got := attentionSentence(item); got != (LocalizedText{}) {
		t.Fatalf("no reasons = %+v, want zero LocalizedText so reasonSentence is omitted", got)
	}

	item.Reasons = []string{string(cache.ReasonDeclined)}
	item.Why = "specs/03-domain.md: should remain technical detail"
	item.RuleIdentity = "future/unknown"
	got = attentionSentence(item)
	knownRule := item
	knownRule.RuleIdentity = "question/declined"
	want = composeAttentionSentence(unknownAttentionReason, knownRule)
	if got != want {
		t.Fatalf("unknown rule identity = %+v, want explicit fallback %+v", got, want)
	}
	if strings.Contains(got.RU, item.Why) || strings.Contains(got.EN, item.Why) {
		t.Fatalf("unknown rule identity fallback leaked diagnostic Why: %+v", got)
	}
}
