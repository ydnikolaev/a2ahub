package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var expectPlanArgument = regexp.MustCompile(`--expect-plan (sha256:[0-9a-f]+)`)

// TestPublishDroppingACompanionNamesTheDigestToConfirm walks the refusal the way
// its reader will: a version that drops a companion is refused without a digest,
// the refusal names the exact one, preflight shows the delete, and re-running
// with that digest lands the version.
func TestPublishDroppingACompanionNamesTheDigestToConfirm(t *testing.T) {
	t.Parallel()
	g := newLocalGateRig(t, "autonomous")
	id, staging := g.stageAndSubmitContract()

	const relative = "artifacts/notes.json"
	root := filepath.Join(g.projectDir, filepath.FromSlash(staging))
	mustMkdirAll(t, filepath.Join(root, "artifacts"))
	companion := filepath.Join(root, filepath.FromSlash(relative))
	mustWrite(t, companion, "{\"note\":\"first\"}\n")
	descriptor := filepath.Join(root, "contract.md")
	const firstEntry = "\n    - path: schema/export.schema.json\n"
	entry := "\n    - path: " + relative + "\n      role: other\n      normative: true\n      media_type: application/json"
	bare := mustReadFile(t, descriptor)
	declared := strings.Replace(bare, firstEntry, entry+firstEntry, 1)
	if declared == bare {
		t.Fatalf("the scaffolded descriptor has no artifacts inventory to extend:\n%s", bare)
	}
	mustWrite(t, descriptor, declared)
	g.mustFree("contract", "publish", "--version", "1.0.0", "--staging", staging, id)

	if err := os.Remove(companion); err != nil {
		t.Fatalf("drop the companion: %v", err)
	}
	mustWrite(t, descriptor, bare)
	publish := []string{"contract", "publish", "--version", "1.1.0", "--staging", staging}

	stdout, stderr, code := g.free(append(append([]string(nil), publish...), id)...)
	if code == 0 {
		t.Fatalf("a publish that deletes a sidecar landed with no digest: %s", stdout)
	}
	if !strings.Contains(stderr, "space: plan-changed") || !strings.Contains(stderr, "deletes 1 sidecar file(s)") {
		t.Fatalf("the refusal does not say what the plan deletes:\n%s", stderr)
	}
	match := expectPlanArgument.FindStringSubmatch(stderr)
	if match == nil {
		t.Fatalf("the refusal names no --expect-plan digest:\n%s", stderr)
	}

	preflight := g.mustFree("contract", "preflight", "--version", "1.1.0", "--staging", staging, "--json", id)
	if !strings.Contains(preflight, relative) || !strings.Contains(preflight, match[1]) {
		t.Fatalf("preflight does not show the delete and the digest the refusal named (%s): %s", match[1], preflight)
	}

	g.mustFree(append(append([]string(nil), publish...), "--expect-plan", match[1], id)...)
	if got := g.mainHeadMessage(); !strings.Contains(got, "Publish "+id) {
		t.Fatalf("main's tip is not the publish's land merge, so nothing landed:\n%s", got)
	}
}
