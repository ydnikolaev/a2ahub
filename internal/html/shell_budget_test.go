package html_test

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/html"
	"github.com/ydnikolaev/a2ahub/internal/localserver"
)

// TestShellBudgetMeasurement records the independent shell, DOCS, DATA, and
// one fully-populated verdict contributions against the production admission
// ceiling. DemoData is the repository's canonical dense fixture; measuring the
// injected document (rather than source-file sizes) includes JSON indentation,
// escaping, and the token stylesheet splice actually retained by the server.
func TestShellBudgetMeasurement(t *testing.T) {
	t.Parallel()

	tmpl := html.DefaultTemplate()
	docs, err := html.Docs()
	if err != nil {
		t.Fatalf("Docs: %v", err)
	}
	demo, err := html.DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	if len(demo.Inbox) == 0 {
		t.Fatal("DemoData inbox is empty; no item exists for the per-verdict measurement")
	}

	renderLength := func(name string, data html.Data, sections []html.DocSection) int {
		t.Helper()
		out, renderErr := html.Render(tmpl, data, sections)
		if renderErr != nil {
			t.Fatalf("Render %s: %v", name, renderErr)
		}
		return len(out)
	}

	floor := renderLength("floor", html.Data{}, nil)
	docsContribution := renderLength("DOCS", html.Data{}, docs) - floor
	dataContribution := renderLength("DemoData", demo, nil) - floor
	full := renderLength("DemoData+DOCS", demo, docs)
	if want := floor + docsContribution + dataContribution; full != want {
		t.Fatalf("independent render contributions do not add up: full=%d, want floor+DOCS+DATA=%d", full, want)
	}
	if docsContribution <= 0 || dataContribution <= 0 {
		t.Fatalf("non-positive contribution: DOCS=%d DATA=%d", docsContribution, dataContribution)
	}

	// Zero the target first so the delta stays the cost of exactly the six P1
	// html.Item verdict fields even if the dense fixture later authors them.
	withoutVerdict := demo
	withoutVerdict.Inbox = append([]html.Item(nil), demo.Inbox...)
	withoutVerdict.Inbox[0].WaitingOn = nil
	withoutVerdict.Inbox[0].ExpectedTransition = ""
	withoutVerdict.Inbox[0].Why = ""
	withoutVerdict.Inbox[0].HumanGate = ""
	withoutVerdict.Inbox[0].OperationalItems = nil
	withoutVerdict.Inbox[0].ReasonSentence = html.LocalizedText{}

	withVerdict := withoutVerdict
	withVerdict.Inbox = append([]html.Item(nil), withoutVerdict.Inbox...)
	withVerdict.Inbox[0].WaitingOn = []string{"atlas", "checkout"}
	withVerdict.Inbox[0].ExpectedTransition = "approve"
	withVerdict.Inbox[0].Why = "domain 3.4.4 quorum gate: two required approvers still owe an approval"
	withVerdict.Inbox[0].HumanGate = "G3"
	withVerdict.Inbox[0].OperationalItems = []cache.OperationalItem{
		{Name: "endpoint", State: cache.OperationalStateReady},
		{Name: "credential-channel", State: cache.OperationalStateAbsent},
		{Name: "registration", State: cache.OperationalStateUndeclared},
	}
	withVerdict.Inbox[0].ReasonSentence = html.LocalizedText{
		RU: "Ход approve ждут от atlas и checkout; он требует участия человека через G3.",
		EN: "atlas and checkout are expected to perform approve; it requires a human through G3.",
	}
	withoutVerdictBytes := renderLength("DemoData without one verdict", withoutVerdict, nil)
	withVerdictBytes := renderLength("DemoData with one verdict", withVerdict, nil)
	perItemVerdictDelta := withVerdictBytes - withoutVerdictBytes
	if perItemVerdictDelta <= 0 {
		t.Fatalf("per-item verdict delta=%d, want a positive byte contribution", perItemVerdictDelta)
	}

	ceiling := localserver.DefaultMaxShellBytes
	headroom := ceiling - full
	if headroom <= 0 {
		t.Fatalf("dense DemoData+DOCS shell=%d reaches production ceiling=%d", full, ceiling)
	}
	t.Logf("shell_budget floor=%d DOCS=%d DATA=%d per_item_verdict_delta=%d full=%d ceiling=%d headroom=%d fixture=DemoData item=inbox[0]",
		floor, docsContribution, dataContribution, perItemVerdictDelta, full, ceiling, headroom)
}
