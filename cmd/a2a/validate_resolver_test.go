package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// This file used to carry TestWireGoWriteConstructionSitesUseTheCriteriaWrapper,
// an AST scan proving wire.go's two write paths called the (now-retired)
// mirrorResolverWithCriteria wrapper rather than a bare cli.NewMirrorResolver.
// That per-call-site review is exactly what the 2026-08-09 readiness audit
// (row 50) removed the NEED for: AcceptanceCriteriaCount now lives on
// cli.MirrorResolver itself, guarded by the compile-time
// `var _ validate.ParentCriteriaCounter = (*cli.MirrorResolver)(nil)`
// assertion in internal/cli/adapters.go (and mirrored in
// internal/cli/adapters_test.go). Every current and future
// cli.NewMirrorResolver call site — in this package or any other — inherits
// the capability from the type, so a hardcoded per-file call-site count here
// would only reintroduce the review the fix was designed to make
// unnecessary, and would falsely red on a legitimate refactor that changed
// how many times a file calls the constructor without weakening anything.
//
// What still needs a behavioural proof — because a compile-time interface
// assertion proves the METHOD exists, not that the ENGINE actually reaches
// it for wire.go's real construction shape — is
// TestMirrorResolverFiresREF018InProductionShape, below.

// TestMirrorResolverFiresREF018InProductionShape is the production-shape
// proof this wave's brief asks for: not internal/validate's own
// criteriaResolver fake, but the REAL resolver wire.go now constructs
// (cli.NewMirrorResolver — the plain constructor, since AcceptanceCriteriaCount
// is now a property of the type itself, not a wrapper this package must
// still apply), reading a REAL on-disk parent artifact through the REAL
// validate.Engine, over the SAME real corpus newEngine() loads.
func TestMirrorResolverFiresREF018InProductionShape(t *testing.T) {
	t.Parallel()
	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}

	mirrorDir := t.TempDir()
	writeParentArtifactForREF018Test(t, mirrorDir, "XW-axon-20260808-p9d3", "acceptance_criteria: [\"a\"]")
	manifest := space.Manifest{Participants: []space.Participant{{System: "seomatrix", Status: "active"}}}
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)

	raw := []byte("---\n" + responseV2Header("XS-axon-20260808-p9d3", "XW-axon-20260808-p9d3", 5) + "---\nBody.\n")

	result, err := engine.ValidateForSubmit(
		validate.Draft{Path: "axon/exchanges/XS-axon-20260808-p9d3.md", Raw: raw},
		nil,
		validate.LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected refusal for an out-of-range unmet index against the real mirror resolver, got Valid=true")
	}
	if !hasViolationCode(result.Violations, "REF-018") {
		t.Fatalf("expected REF-018 among violations, got %+v", result.Violations)
	}

	// The in-range half of the same production path: index 0 fits the
	// parent's single declared criterion, so nothing fires.
	inRange := []byte("---\n" + responseV2Header("XS-axon-20260808-p9d4", "XW-axon-20260808-p9d3", 0) + "---\nBody.\n")
	result, err = engine.ValidateForSubmit(
		validate.Draft{Path: "axon/exchanges/XS-axon-20260808-p9d4.md", Raw: inRange},
		nil,
		validate.LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if hasViolationCode(result.Violations, "REF-018") {
		t.Fatalf("expected no REF-018 for an in-range unmet index, got %+v", result.Violations)
	}
}

// writeParentArtifactForREF018Test seeds a committed envelope/v1 work
// artifact directly under mirrorDir/id.md with an explicit
// `acceptance_criteria` YAML fragment — internal/cli's own
// TestMirrorResolverAcceptanceCriteriaCount (adapters_test.go) already
// covers AcceptanceCriteriaCount's unit behaviour (found/unknown/absent);
// this helper only needs the "found, with a declared count" shape to drive
// the real engine end to end.
func writeParentArtifactForREF018Test(t *testing.T, mirrorDir, id, criteriaYAML string) {
	t.Helper()
	dir := filepath.Join(mirrorDir, "axon", "exchanges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\nschema: envelope/v1\nid: " + id + "\ntype: work_request\ntitle: t\nspace: getvisa\nfrom: axon\nto: [seomatrix]\nthread: thread:axon-1\nactor: {kind: agent, name: codex}\ncreated: \"2026-08-08T08:40:00Z\"\npriority: p3\nblocking: true\nclassification: internal\ncategory: feature\nproposed_change: x\n" +
		criteriaYAML + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// responseV2Header builds a schema-valid envelope/v2 response frontmatter
// body (mirrors internal/validate/incompleteness_test.go's own
// responseV2Base, an unexported test-only const this package cannot
// import) naming id/parent/the single unmet index this test varies.
func responseV2Header(id, parent string, unmetIndex int) string {
	return "schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: response\n" +
		"title: A valid v2 response\n" +
		"space: getvisa\n" +
		"from: axon\n" +
		"to: [seomatrix]\n" +
		"thread: thread:axon-20260808-k3f9\n" +
		"actor: {kind: agent, name: codex}\n" +
		"created: \"2026-08-08T08:40:00Z\"\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"parent: " + parent + "\n" +
		"result: partial\n" +
		"unmet: [" + strconv.Itoa(unmetIndex) + "]\n" +
		"blocked_by:\n" +
		"  reason_code: out-of-scope\n" +
		"  owner: seomatrix\n" +
		"  needs: bytes\n"
}

func hasViolationCode(vs []validate.Violation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}
