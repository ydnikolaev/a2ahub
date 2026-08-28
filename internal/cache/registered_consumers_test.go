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
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
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
// --- FindUnadoptedConsumption fixtures ---
//
// These exercise FindRegisteredConsumers' own OPPOSITE direction (spec 06
// §5): a verify-passed handoff's "data" deliverable resolved against its
// data-package/v1 manifest, against ownSystem's own consumes.yaml — never
// git-committed, same plain on-disk layout as the fixtures above, plus a
// data package's own <system>/data/<DP-id>/manifest.json
// (packageresolver.go's own path shape).

// rcWriteHandoff writes a handoff artifact naming exactly one "data"
// deliverable (packageRef) alongside a "code" deliverable (deliverables[]
// minItems:1, but never all-data in the real schema either — this mirrors
// delivery_test.go's own handoffRaw fixture, which is why the "code" kind
// is never counted is worth exercising once, here, rather than assumed).
func rcWriteHandoff(t *testing.T, mirrorDir, id, from, to, packageRef string) {
	t.Helper()
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: handoff\ntitle: t\nspace: fixture-space\n" +
		"from: " + from + "\nto: [" + to + "]\ncreated: 2026-08-17T10:00:00Z\n" +
		"deliverables:\n" +
		"  - name: dataset\n    ref: " + packageRef + "\n    kind: data\n" +
		"  - name: notes\n    ref: docs/notes.md\n    kind: doc\n" +
		"verification: manual\nacceptance_criteria: [\"done\"]\nlimitations: []\n" +
		"fulfills: [\"XW-" + from + "-20260817-zzzz\"]\n---\nBody.\n"
	rcWriteFile(t, mirrorDir, from+"/"+id+".md", content)
}

// rcWriteHandoffLifecycle commits submit (owner=from) + acknowledge
// (target=to) events for id, and a verify-pass (target=to) event too when
// accepted — the ONE handoffRows row (§3.4.5) that folds to
// fold.StateAccepted. Events are committed under from's own tree, mirroring
// this file's existing requirement fixtures (a subject's events live beside
// its owning artifact's own system section, regardless of which system's
// actor produced a given event).
func rcWriteHandoffLifecycle(t *testing.T, mirrorDir, id, from, to string, accepted bool) {
	t.Helper()
	type step struct{ transition, system string }
	steps := []step{{"submit", from}, {"acknowledge", to}}
	if accepted {
		steps = append(steps, step{"verify-pass", to})
	}
	// suffix must vary per handoff id — a shared "01HFXH...i" filename
	// across two DIFFERENT handoffs from the SAME producer would collide on
	// disk (rcWriteFile overwrites), silently dropping one handoff's own
	// events under the other's file.
	suffix := id
	if at := strings.LastIndex(id, "-"); at >= 0 {
		suffix = id[at+1:]
	}
	for i, s := range steps {
		ulid := fmt.Sprintf("01HFXH%s%011d", suffix, i+1)
		rcWriteFile(t, mirrorDir, fmt.Sprintf("%s/events/2026/%s.yaml", from, ulid),
			fmt.Sprintf("schema: event/v1\nevent: %s\nspace: fixture-space\n"+
				"subject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\n"+
				"at: 2026-08-17T%02d:00:00Z\n", ulid, id, s.transition, s.system, 10+i))
	}
}

// rcWriteDataPackageManifest writes a minimal data-package/v1 manifest.json
// at packageresolver.go's own path shape ("<system>/data/<DP-id>/manifest.json",
// system parsed out of packageID itself) pinning contractRef
// ("<XC-id>@<version>#<digest>", data-package.schema.json's
// pinnedContractRef) as its own `contract` field. Every other manifest
// field is omitted: ResolvePackage only json.Unmarshals this document, it
// never schema-validates it (this corpus never calls AssertFormat), so a
// minimal document with schema/id/contract decodes exactly like a real one
// for this reader's purposes.
func rcWriteDataPackageManifest(t *testing.T, mirrorDir, packageID, contractRef string) {
	t.Helper()
	parsed, err := datapackage.ParsePackageID(packageID)
	if err != nil {
		t.Fatalf("rcWriteDataPackageManifest: ParsePackageID(%q): %v", packageID, err)
	}
	content := fmt.Sprintf(`{"schema":"data-package/v1","id":%q,"contract":%q}`, packageID, contractRef)
	rcWriteFile(t, mirrorDir, parsed.System+"/data/"+packageID+"/manifest.json", content)
}

// TestFindUnadoptedConsumption_NamesUnadoptedContractWithDistinctPackageCount
// is the live case's core shape (docs/inbox/defects/07): axon's own
// consumes.yaml is a real, valid consumes/v1 registry with `dependencies:
// []` (NOT a malformed placeholder — the live case parses cleanly and
// still carries zero declared dependencies), while two DISTINCT
// verify-passed deliveries pin the SAME unadopted contract. A third handoff
// re-delivers one of the SAME two packages again (a resubmission) — this
// must not inflate the count past 2 distinct packages.
func TestFindUnadoptedConsumption_NamesUnadoptedContractWithDistinctPackageCount(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-jgf1", "XC-seomatrix-regime-corpus@1.0.0#bbb222")

	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", "DP-seomatrix-20260818-jgf1")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", true)

	// Resubmission: a THIRD accepted handoff re-carries the FIRST package's
	// own ref. Distinct-package count must stay 2, not 3.
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-cccc", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-cccc", "seomatrix", "axon", true)

	got, skip, err := FindUnadoptedConsumption(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("FindUnadoptedConsumption: %v", err)
	}
	if skip != nil {
		t.Fatalf("want no skip (a valid `dependencies: []` registry is the ordinary live case), got %+v", skip)
	}
	if len(got) != 1 {
		t.Fatalf("got %d contracts, want exactly 1: %+v", len(got), got)
	}
	if got[0].ContractID != "XC-seomatrix-regime-corpus" {
		t.Fatalf("got contract %q, want XC-seomatrix-regime-corpus", got[0].ContractID)
	}
	if got[0].Count != 2 {
		t.Fatalf("got Count=%d, want 2 DISTINCT packages (a resubmission of an already-counted package must not inflate the count)", got[0].Count)
	}
}

// TestFindUnadoptedConsumption_RegisteredContractIsSilent is the AC's own
// "a delivery conforming to a contract I DO consume -> silent" edge: one
// contract IS in axon's own consumes.yaml (adopted) and must never appear,
// while a second, unadopted contract in the SAME mirror still does.
func TestFindUnadoptedConsumption_RegisteredContractIsSilent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteConsumesYAML(t, mirrorDir, "axon", "XC-seomatrix-regime-corpus", 1)

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	// The second, unadopted contract in the same mirror — a real DP- id
	// (this phase resolves the handoff-deliverables -> data-package/v1
	// channel only; see this phase's own reported deviation on the live
	// case's XS- response-attachment example, a different channel).
	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-n2yp", "XC-seomatrix-c4-export@1.0.0#ccc333")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", "DP-seomatrix-20260818-n2yp")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", true)

	got, skip, err := FindUnadoptedConsumption(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("FindUnadoptedConsumption: %v", err)
	}
	if skip != nil {
		t.Fatalf("want no skip, got %+v", skip)
	}
	if len(got) != 1 || got[0].ContractID != "XC-seomatrix-c4-export" {
		t.Fatalf("got %+v, want exactly {XC-seomatrix-c4-export} — the registered contract must stay silent", got)
	}
}

// TestFindUnadoptedConsumption_SelfPublishedNeverReported is the AC's own
// "a contract I publish myself is never reported as unadopted" edge: a
// delivery pins a contract whose own id embeds ownSystem as producer
// (ClassStanding's <PREFIX>-<system>-<slug>), even though axon's own
// consumes.yaml never lists it.
func TestFindUnadoptedConsumption_SelfPublishedNeverReported(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-axon-getvisa-ingest@1.0.0#ddd444")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	got, skip, err := FindUnadoptedConsumption(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("FindUnadoptedConsumption: %v", err)
	}
	if skip != nil {
		t.Fatalf("want no skip, got %+v", skip)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty — axon publishes XC-axon-getvisa-ingest itself, never its own \"unadopted\" consumer", got)
	}
}

// TestFindUnadoptedConsumption_NotYetVerifiedNotCounted: a handoff that is
// only submitted+acknowledged, never verify-passed, must not count — the
// fold never reaches StateAccepted.
func TestFindUnadoptedConsumption_NotYetVerifiedNotCounted(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", false)

	got, skip, err := FindUnadoptedConsumption(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("FindUnadoptedConsumption: %v", err)
	}
	if skip != nil {
		t.Fatalf("want no skip, got %+v", skip)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty — the handoff was never verify-passed", got)
	}
}

// TestFindUnadoptedConsumption_MalformedRegistryDegradesToSkipNeverFalseAdvisory
// proves the asymmetry this function's own doc comment claims: an
// unreadable/placeholder consumes.yaml must degrade to a reported SKIP,
// never silently to "empty declared set" (which would falsely re-report an
// already-registered contract as unadopted).
func TestFindUnadoptedConsumption_MalformedRegistryDegradesToSkipNeverFalseAdvisory(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "consumes: []\n") // placeholder shape, refused by parseConsumesStrict

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	got, skip, err := FindUnadoptedConsumption(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("FindUnadoptedConsumption: %v", err)
	}
	if skip == nil {
		t.Fatal("want a non-nil skip: the registry could not be read as consumes/v1")
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty rows alongside the skip — never both a skip AND a (possibly false) contract list", got)
	}
}

// TestFindUnadoptedConsumption_EmptyOwnSystemReturnsNil: no configured
// identity means nothing can be resolved as "mine" — the same degrade
// myDependencyContracts documents for an empty ownSystem.
func TestFindUnadoptedConsumption_EmptyOwnSystemReturnsNil(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	got, skip, err := FindUnadoptedConsumption(mirrorDir, "")
	if err != nil || skip != nil || got != nil {
		t.Fatalf("got (%v, %v, %v), want (nil, nil, nil)", got, skip, err)
	}
}

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

// --- Phase 06 (answers-that-hold-2026-08): the version is carried, not
// dropped ---
//
// TestFindUnadoptedConsumption_DifferentVersionsReportedDistinctly is §8
// criterion 5's read half (spec 06): two verify-passed deliveries pinning
// the SAME contract id at TWO DIFFERENT versions must surface as two
// DISTINCT UnadoptedConsumption entries, never one row whose Count silently
// sums across versions — the naive `map[string]int` keyed by bare contract
// id this criterion warns against.
//
// TEETH: key packagesByContract by ContractID alone (drop Version from the
// grouping key) and this reds with a single collapsed entry.
func TestFindUnadoptedConsumption_DifferentVersionsReportedDistinctly(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-n2yp", "XC-seomatrix-regime-corpus@2.0.0#bbb222")

	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", "DP-seomatrix-20260818-n2yp")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", true)

	got, skip, err := FindUnadoptedConsumption(mirrorDir, "axon")
	if err != nil {
		t.Fatalf("FindUnadoptedConsumption: %v", err)
	}
	if skip != nil {
		t.Fatalf("want no skip, got %+v", skip)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want exactly 2 (one per version), never collapsed: %+v", len(got), got)
	}
	if got[0].ContractID != "XC-seomatrix-regime-corpus" || got[0].Version != "1.0.0" || got[0].Count != 1 {
		t.Fatalf("got[0] = %+v, want {XC-seomatrix-regime-corpus 1.0.0 1}", got[0])
	}
	if got[1].ContractID != "XC-seomatrix-regime-corpus" || got[1].Version != "2.0.0" || got[1].Count != 1 {
		t.Fatalf("got[1] = %+v, want {XC-seomatrix-regime-corpus 2.0.0 1}", got[1])
	}
}

// TestFindObservedConsumers_DifferentVersionsReportedDistinctly is §8
// criterion 5's producer-side half: the SAME system pinning two different
// versions of one contract must appear as two distinct ObservedConsumer
// entries, one per version, never collapsed into one.
func TestFindObservedConsumers_DifferentVersionsReportedDistinctly(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "schema: consumes/v1\nsystem: axon\ndependencies: []\n")

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-n2yp", "XC-seomatrix-regime-corpus@2.0.0#bbb222")

	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", "DP-seomatrix-20260818-n2yp")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-bbbb", "seomatrix", "axon", true)

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err != nil {
		t.Fatalf("FindObservedConsumers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want exactly 2 (one per version), never collapsed: %+v", len(got), got)
	}
	for _, want := range []ObservedConsumer{
		{System: "axon", Version: "1.0.0", Packages: 1},
		{System: "axon", Version: "2.0.0", Packages: 1},
	} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("got %+v, want to contain %+v", got, want)
		}
	}
}

// TestFindObservedConsumers_UnreadableRegistryFailsClosedRatherThanFalseReport
// is §8 criterion 8's boundary on THIS function: FindObservedConsumers opens
// with FindRegisteredConsumers (the DECLARED half), which fails closed on
// ANY unparseable consumes.yaml in the mirror (documented at that
// function's own call site above: "the declared half above still fails
// closed... because that set is the one the gate itself reads"). This
// function must propagate that error rather than swallowing it into an
// empty, falsely-clean observed set — the caller (contractObservedConsumers,
// and this phase's doctorObservedProducerRows) is the layer that degrades
// an error to "nothing observed" (FAILS OPEN, by contract, never by
// silently miscounting here).
func TestFindObservedConsumers_UnreadableRegistryFailsClosedRatherThanFalseReport(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	rcWriteFile(t, mirrorDir, "axon/consumes.yaml", "consumes: []\n") // placeholder shape, refused by parseConsumesStrict

	rcWriteDataPackageManifest(t, mirrorDir, "DP-seomatrix-20260818-p3my", "XC-seomatrix-regime-corpus@1.0.0#aaa111")
	rcWriteHandoff(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", "DP-seomatrix-20260818-p3my")
	rcWriteHandoffLifecycle(t, mirrorDir, "XH-seomatrix-20260817-aaaa", "seomatrix", "axon", true)

	got, err := FindObservedConsumers(mirrorDir, "XC-seomatrix-regime-corpus", ocManifest("axon", "seomatrix"))
	if err == nil {
		t.Fatalf("got (%+v, nil), want a non-nil error — axon's own registry cannot be parsed, and the declared half fails closed rather than silently reporting an empty (falsely clean) observed set", got)
	}
}
