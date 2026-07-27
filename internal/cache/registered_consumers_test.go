package cache

// registered_consumers_test.go exercises FindRegisteredConsumers/
// FindRegisteredConsumersForMajor directly against a plain on-disk mirror
// layout (no git commit needed — unlike buildIndex's own tests, this scan
// never resolves anything through git history, only filepath.Glob +
// direct reads).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// rcWriteFile writes content at mirrorDir/relPath, creating parent
// directories as needed.
func rcWriteFile(t *testing.T, mirrorDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(mirrorDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("rcWriteFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("rcWriteFile: write: %v", err)
	}
}

func rcWriteConsumesYAML(t *testing.T, mirrorDir, system, contractID string, major int) {
	t.Helper()
	content := "schema: consumes/v1\nsystem: " + system + "\ndependencies:\n" +
		"  - contract: " + contractID + "\n    major: " + strconv.Itoa(major) + "\n    since: \"2026-01-01\"\n"
	rcWriteFile(t, mirrorDir, system+"/consumes.yaml", content)
}

// TestFindRegisteredConsumers_ConsumesYAML is the ordinary D-022 case: a
// system with a real consumes/v1 registry naming the contract is a
// registered consumer, unscoped.
func TestFindRegisteredConsumers_ConsumesYAML(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteConsumesYAML(t, mirrorDir, "beta", "XC-axon-widget", 1)

	got, err := FindRegisteredConsumers(mirrorDir, "XC-axon-widget")
	if err != nil {
		t.Fatalf("FindRegisteredConsumers: %v", err)
	}
	if !got["beta"] || len(got) != 1 {
		t.Fatalf("got %v, want exactly {beta: true}", got)
	}
}

// TestFindRegisteredConsumers_NoMatches is the empty-mirror case: no
// consumes.yaml, no requirements naming the contract -> empty map, no
// error.
func TestFindRegisteredConsumers_NoMatches(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	got, err := FindRegisteredConsumers(mirrorDir, "XC-axon-widget")
	if err != nil {
		t.Fatalf("FindRegisteredConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

// TestFindRegisteredConsumersForMajor_FiltersConsumesYAMLByMajor is Edge
// 1's core ask (04-per-version-lifecycle.md §4, AC-9): a consumes.yaml
// dependency counts only when its own `major` matches the major being
// retired — "beta" pinned to major 1 and "gamma" pinned to major 2 must
// resolve to DIFFERENT consumer sets for the same contract id. TEETH:
// reverting the `if major >= 0 && d.Major != major { continue }` guard
// (i.e. dropping the major filter entirely) makes ForMajor(..., 1) return
// {beta, gamma} instead of {beta} alone — this assertion catches that.
func TestFindRegisteredConsumersForMajor_FiltersConsumesYAMLByMajor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteConsumesYAML(t, mirrorDir, "beta", "XC-axon-widget", 1)
	rcWriteConsumesYAML(t, mirrorDir, "gamma", "XC-axon-widget", 2)

	forMajor1, err := FindRegisteredConsumersForMajor(mirrorDir, "XC-axon-widget", 1)
	if err != nil {
		t.Fatalf("FindRegisteredConsumersForMajor(1): %v", err)
	}
	if !forMajor1["beta"] || forMajor1["gamma"] || len(forMajor1) != 1 {
		t.Fatalf("major 1: got %v, want exactly {beta: true}", forMajor1)
	}

	forMajor2, err := FindRegisteredConsumersForMajor(mirrorDir, "XC-axon-widget", 2)
	if err != nil {
		t.Fatalf("FindRegisteredConsumersForMajor(2): %v", err)
	}
	if !forMajor2["gamma"] || forMajor2["beta"] || len(forMajor2) != 1 {
		t.Fatalf("major 2: got %v, want exactly {gamma: true}", forMajor2)
	}

	unscoped, err := FindRegisteredConsumers(mirrorDir, "XC-axon-widget")
	if err != nil {
		t.Fatalf("FindRegisteredConsumers: %v", err)
	}
	if !unscoped["beta"] || !unscoped["gamma"] || len(unscoped) != 2 {
		t.Fatalf("unscoped: got %v, want both {beta, gamma}", unscoped)
	}
}

// TestFindRegisteredConsumersForMajor_UnrelatedContractExcluded proves the
// major filter does not accidentally widen the CONTRACT match itself: a
// dependency on a different contract id, even at the SAME major, must
// never appear.
func TestFindRegisteredConsumersForMajor_UnrelatedContractExcluded(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteConsumesYAML(t, mirrorDir, "beta", "XC-axon-other", 1)

	got, err := FindRegisteredConsumersForMajor(mirrorDir, "XC-axon-widget", 1)
	if err != nil {
		t.Fatalf("FindRegisteredConsumersForMajor: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty (different contract id)", got)
	}
}

// TestFindRegisteredConsumers_RequirementNotSatisfiedDoesNotCount: a
// requirement naming the contract as its target_contract, folded only
// through create+publish (never acknowledged/satisfied), must not appear
// in the registered-consumer set — the D-022 union's requirement half only
// ever counts a SATISFIED requirement.
func TestFindRegisteredConsumers_RequirementNotSatisfiedDoesNotCount(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "beta/requires/XR-beta-abc.md",
		"---\nschema: envelope/v1\nid: XR-beta-abc\ntype: requirement\ntitle: t\nspace: fixture-space\n"+
			"from: beta\nto: [axon]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\n"+
			"category: new-capability\npriority: p3\nblocking: true\nclassification: internal\n"+
			"target_contract: XC-axon-widget\nacceptance_criteria: [\"ac1\"]\n---\nbody\n")
	rcWriteFile(t, mirrorDir, "beta/events/2026/01HFXR0000000000000000001.yaml",
		"schema: event/v1\nevent: 01HFXR0000000000000000001\nspace: fixture-space\n"+
			"subject: XR-beta-abc\ntransition: create\nactor: {kind: agent, name: bot, system: beta}\n"+
			"at: 2026-07-21T10:00:00Z\n")
	rcWriteFile(t, mirrorDir, "beta/events/2026/01HFXR0000000000000000002.yaml",
		"schema: event/v1\nevent: 01HFXR0000000000000000002\nspace: fixture-space\n"+
			"subject: XR-beta-abc\ntransition: publish\nactor: {kind: agent, name: bot, system: beta}\n"+
			"at: 2026-07-21T11:00:00Z\n")

	got, err := FindRegisteredConsumers(mirrorDir, "XC-axon-widget")
	if err != nil {
		t.Fatalf("FindRegisteredConsumers: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty (requirement is published, not satisfied)", got)
	}
}

// TestFindRegisteredConsumers_SatisfiedRequirementCounts is the D-022
// union's OTHER half, and it is here because until this wave it could
// never fire.
//
// Both former per-surface copies built the fold envelope from {ID, Kind,
// From} and omitted `To`. A requirement reaches `satisfied` only via
// published --acknowledge(RoleTarget)--> acknowledged --satisfy-->
// satisfied, and RoleTarget resolves to env.To0() — so with To empty the
// acknowledge was flagged unauthorized, never applied, and the fold
// stopped at `published`. Every satisfied requirement in the space read
// as unsatisfied, so a consumer who registered by filing one never
// blocked `contract retire`: a gate written to fail closed failing OPEN
// down one of its two branches, invisibly, because the branch never
// evaluated true rather than because it evaluated wrongly.
//
// TEETH: drop `To: probe.To` from the fold.Envelope in
// registered_consumers.go and this test reds with an empty consumer set,
// while every other test in this file stays green — which is precisely
// how the defect survived two implementations.
func TestFindRegisteredConsumers_SatisfiedRequirementCounts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "beta/requires/XR-beta-abc.md",
		"---\nschema: envelope/v1\nid: XR-beta-abc\ntype: requirement\ntitle: t\nspace: fixture-space\n"+
			"from: beta\nto: [axon]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\n"+
			"category: new-capability\npriority: p3\nblocking: true\nclassification: internal\n"+
			"target_contract: XC-axon-widget\nacceptance_criteria: [\"ac1\"]\n---\nbody\n")
	for i, ev := range []struct{ transition, system, at string }{
		{"create", "beta", "2026-07-21T10:00:00Z"},
		{"publish", "beta", "2026-07-21T11:00:00Z"},
		// The acknowledge comes from the TARGET (axon) — the step that
		// silently failed authorization when To was dropped.
		{"acknowledge", "axon", "2026-07-21T12:00:00Z"},
		{"satisfy", "beta", "2026-07-21T13:00:00Z"},
	} {
		rcWriteFile(t, mirrorDir, fmt.Sprintf("beta/events/2026/01HFXR000000000000000000%d.yaml", i+1),
			fmt.Sprintf("schema: event/v1\nevent: 01HFXR000000000000000000%d\nspace: fixture-space\n"+
				"subject: XR-beta-abc\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\n"+
				"at: %s\n", i+1, ev.transition, ev.system, ev.at))
	}

	got, err := FindRegisteredConsumers(mirrorDir, "XC-axon-widget")
	if err != nil {
		t.Fatalf("FindRegisteredConsumers: %v", err)
	}
	if !got["beta"] {
		t.Fatalf("got %v, want beta — a SATISFIED requirement is half of the D-022 union, and a "+
			"consumer registered that way must block retire", got)
	}

	// Edge 1's conservative carve-out: a satisfied requirement carries no
	// version, so it counts against EVERY major. That rule is only worth
	// anything now that the branch can fire at all.
	for _, major := range []int{1, 2, 7} {
		scoped, serr := FindRegisteredConsumersForMajor(mirrorDir, "XC-axon-widget", major)
		if serr != nil {
			t.Fatalf("FindRegisteredConsumersForMajor(%d): %v", major, serr)
		}
		if !scoped["beta"] {
			t.Fatalf("major %d: got %v, want beta — an unversioned registration must not be dropped "+
				"from a gate that exists to protect it", major, scoped)
		}
	}
}

// TestFindRegisteredConsumers_MalformedConsumesFailsClosed: an unreadable
// consumes/v1 registry must error, never round down to "consumes
// nothing" — the retire gate's own reason for this rule (see
// parseConsumesStrict's doc comment).
func TestFindRegisteredConsumers_MalformedConsumesFailsClosed(t *testing.T) {
	t.Parallel()

	for name, registry := range map[string]string{
		"placeholder shape": "consumes: []\n",
		"missing header":    "dependencies:\n  - contract: XC-axon-widget\n    major: 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			rcWriteFile(t, mirrorDir, "beta/consumes.yaml", registry)

			_, err := FindRegisteredConsumers(mirrorDir, "XC-axon-widget")
			if err == nil {
				t.Fatal("expected an error: the registry could not be read as consumes/v1")
			}
		})
	}
}
