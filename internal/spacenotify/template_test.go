package spacenotify

import (
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// TestSpaceTemplate_DocumentedExampleSurfacesThe20260827Traffic is AC-18:
// "the scaffolded template's route would have surfaced the 2026-08-27
// traffic" — read space-template/space.yaml against the selector.
//
// space-template/space.yaml itself ships `notification_routes: []`
// (US-101/AC-101.1's own "adopting this template costs nothing" —
// verbatim in a2a-notify.yml's own header, and no chat id EXISTS at
// template time to populate a live route with). What this phase can and
// does prove is that the DOCUMENTED example route living in that file's
// own comment — the fix an operator is told to apply to avoid this
// spec's own incident — actually selects what it claims to: a contract
// publish and an announcement addressed to a participant, and NOT an
// ordinary requirement note (the exact "note" AC-5's own traffic
// description names).
//
// This does not just eyeball the prose: it PARSES the commented YAML
// example out of the real file and runs it through selectorMatches, the
// same function Render itself calls — so a future edit that silently
// breaks the example (a typo, a dropped key) reds this test, not just a
// human reviewer.
func TestSpaceTemplate_DocumentedExampleSurfacesThe20260827Traffic(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../space-template/space.yaml")
	if err != nil {
		t.Fatalf("read space-template/space.yaml: %v", err)
	}

	route, sel := extractTemplateExampleRoute(t, raw)

	contractPublish := cache.NotifyArtifact{Kind: "contract", State: "published"}
	announcement := cache.NotifyArtifact{Kind: "announcement", Addressees: []string{"seomatrix"}}
	ordinaryNote := cache.NotifyArtifact{Kind: "requirement", State: "published"}

	if !selectorMatches(contractPublish, route, sel) {
		t.Fatalf("the template's documented example does not select a contract publish — it would repeat the 2026-08-27 incident")
	}
	if !selectorMatches(announcement, route, sel) {
		t.Fatalf("the template's documented example does not select an announcement")
	}
	if selectorMatches(ordinaryNote, route, sel) {
		t.Fatalf("the template's documented example selects an ORDINARY note it should not (AC-5: without also selecting every note)")
	}

	// The example must also declare `published` in events — human-gate/
	// blocking alone (notify setup's own default) is EXACTLY the
	// incident's own route shape.
	if !contains(route.Events, ClassPublished) {
		t.Fatalf("the template's documented example events = %v, want it to include %q (the incident's own missing class)", route.Events, ClassPublished)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// extractTemplateExampleRoute pulls the commented `- channel: telegram ...`
// YAML block out of space-template/space.yaml's own `notification_routes:`
// comment and decodes it exactly the way this package decodes a real
// route: the typed space.NotificationRoute half via yaml.v3, and the P11
// selector half via the SAME notifyRouteSelectorProbe this package's own
// selector.go uses for a real manifest's Raw bytes — one decode path,
// never a second parser invented just for this test.
func extractTemplateExampleRoute(t *testing.T, raw []byte) (space.NotificationRoute, Selector) {
	t.Helper()
	lines := strings.Split(string(raw), "\n")
	var block []string
	collecting := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		switch {
		case strings.HasPrefix(trimmed, "- channel:"):
			collecting = true
			block = append(block, trimmed)
		case collecting && (strings.HasPrefix(trimmed, "chat:") || strings.HasPrefix(trimmed, "events:") || strings.HasPrefix(trimmed, "kind:")):
			block = append(block, "  "+trimmed)
		case collecting:
			collecting = false
		}
	}
	if len(block) == 0 {
		t.Fatalf("space-template/space.yaml carries no commented example route under notification_routes — AC-18's own documentation is missing")
	}
	yamlDoc := "notification_routes:\n"
	for _, l := range block {
		yamlDoc += "  " + l + "\n"
	}

	var manifest space.Manifest
	if err := yaml.Unmarshal([]byte(yamlDoc), &manifest); err != nil {
		t.Fatalf("the template's commented example does not parse as a notification route: %v\n%s", err, yamlDoc)
	}
	if len(manifest.NotificationRoutes) != 1 {
		t.Fatalf("parsed %d routes from the template's example, want 1\n%s", len(manifest.NotificationRoutes), yamlDoc)
	}

	var probe notifyManifestSelectorProbe
	if err := yaml.Unmarshal([]byte(yamlDoc), &probe); err != nil {
		t.Fatalf("could not decode the example's own selector keys: %v\n%s", err, yamlDoc)
	}
	sel := Selector{
		Kind:      toStringSlice(probe.NotificationRoutes[0].Kind),
		State:     toStringSlice(probe.NotificationRoutes[0].State),
		Direction: probe.NotificationRoutes[0].Direction,
		Urgency:   toStringSlice(probe.NotificationRoutes[0].Urgency),
	}
	return manifest.NotificationRoutes[0], sel
}
