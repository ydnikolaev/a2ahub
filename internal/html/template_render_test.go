package html

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTemplateUsesManifestCardRoutes(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../web/design-source/cards.manifest.json")
	if err != nil {
		t.Fatalf("read card manifest: %v", err)
	}
	var manifest struct {
		Cards []struct {
			Kind      string `json:"kind"`
			Component string `json:"component"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode card manifest: %v", err)
	}
	want := map[string]string{
		"item":                "ArtifactDetail",
		"thread":              "ThreadsView",
		"contract":            "ContractsView",
		"cver":                "ContractsView",
		"work-report":         "ExchangeView",
		"space":               "SpacesView",
		"operational-process": "Overview",
	}
	if len(manifest.Cards) != len(want) {
		t.Fatalf("card manifest has %d rows, want exactly %d canonical families", len(manifest.Cards), len(want))
	}
	for _, card := range manifest.Cards {
		if component, ok := want[card.Kind]; !ok || component != card.Component {
			t.Errorf("unexpected card route %q -> %q", card.Kind, card.Component)
		}
		delete(want, card.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("card manifest is missing canonical routes: %v", want)
	}

	root := dashboardDesignSource(t, "14-local-dashboard-v4.dc.html")
	modal := dashboardDesignSource(t, "Modal.dc.html")
	for _, token := range []string{
		`const A2A_CARD_MANIFEST = /*A2A_CARD_MANIFEST*/null;`,
		`const previouslyResolved = selection ? this.resolveCard(previousCardData, selection) : null;`,
		`const newlyResolved = selection ? this.resolveCard(candidate, selection) : null;`,
		`openCard(kind, id, accent)`,
		`cardManifest:A2A_CARD_MANIFEST`,
	} {
		if !strings.Contains(root, token) {
			t.Errorf("dashboard root is missing manifest-card contract %q", token)
		}
	}
	for _, component := range []string{"ArtifactDetail", "ThreadsView", "ContractsView", "ExchangeView", "SpacesView", "Overview"} {
		if !strings.Contains(modal, `cardRow.component === "`+component+`"`) {
			t.Errorf("canonical Modal cannot dispatch manifest component %q", component)
		}
	}
}

// TestTemplateRendersCanonicalCardFacts keeps the reachability tooth on the
// current card families. It names the carried fact at the point where the
// component paints it; a bare model-field occurrence is not enough.
func TestTemplateRendersCanonicalCardFacts(t *testing.T) {
	t.Parallel()

	contracts := map[string][]string{
		"ArtifactDetail.dc.html": {
			`data-artifact-footer`,
			`syncStaleTechnical:"sync_stale=" + String(d.syncStale === true)`,
			`data-event-consistency`,
			`{{ e.consistencyText }}`,
			`const operational = item.operationalItems || artifact.operational_items || [];`,
			`<sc-for list="{{ attachments }}" as="att"`,
		},
		"ThreadsView.dc.html": {
			`data-thread-open-items`,
			`{{ o.reasonSentence }}`,
			`{{ o.technicalExpectedTransition }}`,
			`technicalExpectedTransition:"expected_transition=" + String(o.expected_transition || "")`,
			`<sc-for list="{{ o.operationalItems }}" as="op"`,
			`const transitionResult = resolveVocabulary(d, "transition", String(ev.transition || ""), ru);`,
		},
		"ContractsView.dc.html": {
			`const buildContractCard = (contract) =>`,
			`const buildVersionCard = (contract, version) =>`,
			`const detail = version.detail || null;`,
			`const documents = detail ? (detail.documents || []).map((document, index, rows) =>`,
			`document.shownBytes + " bytes of " + document.totalBytes`,
			`data-contract-documents-omitted`,
		},
	}

	shipped := dashboardTemplateCorpus(t)
	for component, required := range contracts {
		t.Run(component, func(t *testing.T) {
			t.Parallel()
			source := dashboardDesignSource(t, component)
			for _, token := range required {
				if !strings.Contains(source, token) {
					t.Errorf("%s does not render canonical fact %q", component, token)
				}
				if !strings.Contains(shipped, token) {
					t.Errorf("shipped dashboard does not carry %s fact %q", component, token)
				}
			}
		})
	}
}

// TestTemplateUsesCarriedTransitionVocabulary replaces the retired
// TRANSITION_PAST literal table with the one server-carried resolver path.
// Every event-bearing card family must hand the raw transition to that path.
func TestTemplateUsesCarriedTransitionVocabulary(t *testing.T) {
	t.Parallel()

	contracts := map[string]string{
		"ExchangeView.dc.html":  `vocabulary(d, "transition", transition, ru)`,
		"ThreadsView.dc.html":   `resolveVocabulary(d, "transition", String(ev.transition || ""), ru)`,
		"Overview.dc.html":      `this.vocabulary(data, "transition", latest.transition, locale)`,
		"ContractsView.dc.html": `const transitionResult = resolve("transition", event.transition);`,
	}
	shipped := dashboardTemplateCorpus(t)
	for component, resolverCall := range contracts {
		source := dashboardDesignSource(t, component)
		if !strings.Contains(source, resolverCall) {
			t.Errorf("%s does not resolve carried transition vocabulary through %q", component, resolverCall)
		}
		if !strings.Contains(shipped, resolverCall) {
			t.Errorf("shipped dashboard does not carry %s transition resolver call %q", component, resolverCall)
		}
		for _, retired := range []string{"TRANSITION_PAST", "EVENT_SIGNAL"} {
			if strings.Contains(source, retired) {
				t.Errorf("%s still carries retired local transition table %q", component, retired)
			}
		}
	}
}

func dashboardDesignSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../web/design-source/" + name)
	if err != nil {
		t.Fatalf("read dashboard component %s: %v", name, err)
	}
	return string(raw)
}
