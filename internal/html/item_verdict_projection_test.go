package html

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// TestToItemCarriesCompleteVerdict pins the value-level half of the Item
// projection. The reflection gate proves only that destination field names
// exist; this test proves toItem assigns the already-computed verdict and does
// not recover a human gate from a coincidentally matching reason string.
func TestToItemCarriesCompleteVerdict(t *testing.T) {
	t.Parallel()

	operational := []cache.OperationalItem{{Name: "endpoint", State: cache.OperationalStateReady}}
	source := cache.Item{
		ID: "XD-atlas-20260813-review", Type: "decision", State: "proposed",
		Reasons: []string{"gate-pending-on-me"}, WaitingOn: []string{"atlas"},
		ExpectedTransition: "approve", Why: "specs/03-domain.md: a diagnostic rule citation",
		HumanGate: "G3", RuleIdentity: "decision/proposed", OperationalItems: operational,
		Overdue: true, ActivationOwed: true,
	}

	got := toItem(source, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), "atlas", openItemIndex{})
	if !reflect.DeepEqual(got.WaitingOn, source.WaitingOn) {
		t.Errorf("WaitingOn = %v, want %v", got.WaitingOn, source.WaitingOn)
	}
	if got.ExpectedTransition != source.ExpectedTransition {
		t.Errorf("ExpectedTransition = %q, want %q", got.ExpectedTransition, source.ExpectedTransition)
	}
	if got.Why != source.Why {
		t.Errorf("Why = %q, want %q", got.Why, source.Why)
	}
	if got.HumanGate != source.HumanGate {
		t.Errorf("HumanGate = %q, want %q", got.HumanGate, source.HumanGate)
	}
	if !reflect.DeepEqual(got.OperationalItems, operational) {
		t.Errorf("OperationalItems = %v, want %v", got.OperationalItems, operational)
	}
	if !got.Overdue || !got.ActivationOwed {
		t.Errorf("attention semantics were not carried mechanically: Overdue=%v ActivationOwed=%v", got.Overdue, got.ActivationOwed)
	}
	if !got.GatePending {
		t.Error("GatePending = false, want true when the typed verdict carries HumanGate")
	}
	if got.ReasonSentence.RU == "" || got.ReasonSentence.EN == "" {
		t.Fatalf("ReasonSentence = %+v, want both locales", got.ReasonSentence)
	}
	if strings.Contains(got.ReasonSentence.RU, "specs/") || strings.Contains(got.ReasonSentence.EN, "specs/") {
		t.Fatalf("ReasonSentence leaked diagnostic Why: %+v", got.ReasonSentence)
	}
}

// TestDashboardConsumersDoNotClassifyRawAttentionReasons guards both authored
// and generated production consumers. Reasons remain diagnostic wire data; a
// browser branching on their raw codes would recreate server policy in the
// presentation layer. The vocabulary is derived from cache.ReasonCodes rather
// than copied into this test.
func TestDashboardConsumersDoNotClassifyRawAttentionReasons(t *testing.T) {
	t.Parallel()

	paths := []string{
		filepath.Join("..", "..", "web", "design-source", "14-local-dashboard-v4.dc.html"),
		"template.html",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			file, err := os.Open(path)
			if err != nil {
				t.Fatalf("open production consumer: %v", err)
			}
			t.Cleanup(func() {
				if closeErr := file.Close(); closeErr != nil {
					t.Errorf("close production consumer: %v", closeErr)
				}
			})

			lineNumber := 0
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
			for scanner.Scan() {
				lineNumber++
				line := scanner.Text()
				if !strings.Contains(line, ".reasons") {
					continue
				}
				for _, code := range cache.ReasonCodes() {
					if strings.Contains(line, `"`+string(code)+`"`) || strings.Contains(line, `'`+string(code)+`'`) {
						t.Errorf("%s:%d classifies raw attention reason %q; consume server-carried semantic fields instead: %s",
							path, lineNumber, code, strings.TrimSpace(line))
					}
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan production consumer: %v", err)
			}
		})
	}
}

// TestAssembleConsumesOneDashboardSnapshot keeps the production assembler on
// the resistance-tested cache API. Reintroducing the older independent reads
// would evaluate pendency again while projecting threads and prompts even if
// DashboardSnapshot itself remained perfectly single-pass.
func TestAssembleConsumesOneDashboardSnapshot(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("assemble.go")
	if err != nil {
		t.Fatalf("read assemble.go: %v", err)
	}
	source := string(raw)
	if got := strings.Count(source, "store.DashboardSnapshot(ctx)"); got != 1 {
		t.Fatalf("DashboardSnapshot production calls = %d, want exactly 1", got)
	}
	for _, forbidden := range []string{
		"store.OpenInboxSnapshot(ctx)",
		"store.OutboxSnapshot(ctx)",
		"store.ExchangeArchiveSnapshot(ctx)",
		"store.ThreadView(ctx",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("assemble.go still independently consumes %s; use the carried DashboardSnapshot verdict", forbidden)
		}
	}
}

func TestToItemGatePendingUsesHumanGateNotReasons(t *testing.T) {
	t.Parallel()

	got := toItem(cache.Item{
		ID: "XD-atlas-20260813-review", Type: "decision", State: "proposed",
		Reasons: []string{"gate-pending-on-me"},
	}, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), "atlas", openItemIndex{})
	if got.GatePending {
		t.Fatal("GatePending = true from Reasons alone; it must use the typed HumanGate verdict field")
	}
}

func TestToItemCarriesNobodyOwesVerdict(t *testing.T) {
	t.Parallel()

	source := cache.Item{
		ID: "XQ-atlas-20260813-declined", Type: "question", State: "declined",
		Reasons: []string{string(cache.ReasonDeclined)},
		Why:     "settled; the remedy is a new question",
		// No thread, owners, expected transition, or gate is the deliberate
		// third-shape edge: "nobody owes" remains a complete verdict because
		// its diagnostic reason and typed row identity are still present.
		RuleIdentity: "question/declined",
	}
	got := toItem(source, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), "atlas", openItemIndex{})
	if len(got.WaitingOn) != 0 || got.ExpectedTransition != "" || got.HumanGate != "" {
		t.Errorf("nobody-owes verdict grew an owner/move/gate: WaitingOn=%v ExpectedTransition=%q HumanGate=%q",
			got.WaitingOn, got.ExpectedTransition, got.HumanGate)
	}
	if got.Why != source.Why {
		t.Errorf("Why = %q, want complete nobody-owes diagnostic %q", got.Why, source.Why)
	}
	if got.GatePending {
		t.Error("GatePending = true for nobody-owes verdict")
	}
	if got.ReasonSentence.RU == "" || got.ReasonSentence.EN == "" {
		t.Fatalf("ReasonSentence = %+v, want explicit declined sentence in both locales", got.ReasonSentence)
	}
	if strings.Contains(got.ReasonSentence.RU, source.Why) || strings.Contains(got.ReasonSentence.EN, source.Why) {
		t.Fatalf("ReasonSentence leaked nobody-owes diagnostic Why: %+v", got.ReasonSentence)
	}
}
