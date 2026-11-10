package e2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// largeSidecarBytes is deliberately a literal, and deliberately above both
// 1 MiB and 2 MiB. The boundary tests in internal/contract are written relative
// to contract.MaxFileBytes, so they pass after any change to it and cannot see
// a reader on this path still enforcing a private 1 MiB or 2 MiB of its own.
// Deriving this number from the constant would make this test blind in the same
// way.
const largeSidecarBytes = 2_621_440 // 2.5 MiB

// TestContractLargeSidecarLandsAndReadsBack publishes a contract declaring a
// generated file of largeSidecarBytes under artifacts/, on a real local space
// through the built binary with no network: the preflight, the publish and its
// land check (LocalHost runs the V3 check over the merge candidate before it
// moves main), then every read of the landed version. A real contract could not
// publish a 1.6 MB generated file under the old 1 MiB bound.
func TestContractLargeSidecarLandsAndReadsBack(t *testing.T) {
	t.Parallel()
	g := newLocalGateRig(t, "autonomous")
	id, staging := g.stageAndSubmitContract()

	const relative = "artifacts/generated.json"
	want := largeSidecarJSON(t)
	root := filepath.Join(g.projectDir, filepath.FromSlash(staging))
	mustMkdirAll(t, filepath.Join(root, "artifacts"))
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), want, 0o644); err != nil {
		t.Fatalf("write the sidecar: %v", err)
	}
	// A large fixture as well as a large companion: the land check of the NEXT
	// version re-reads the prior version's fixtures from git, which a companion
	// under artifacts/ never reaches.
	if err := os.WriteFile(filepath.Join(root, "fixtures", "valid", "export.json"), want, 0o644); err != nil {
		t.Fatalf("write the large fixture: %v", err)
	}
	descriptor := filepath.Join(root, "contract.md")
	// Anchored on a whole rendered entry: the scaffold's list indent is the YAML
	// encoder's, and an entry at any other indent is a parse failure that says
	// only "staged descriptor is invalid".
	const firstEntry = "\n    - path: schema/export.schema.json\n"
	declared := strings.Replace(mustReadFile(t, descriptor), firstEntry,
		"\n    - path: "+relative+"\n      role: other\n      normative: true\n      media_type: application/json"+firstEntry, 1)
	if !strings.Contains(declared, "path: "+relative) {
		t.Fatalf("the scaffolded descriptor has no artifacts inventory to extend:\n%s", declared)
	}
	mustWrite(t, descriptor, declared)

	preflight := g.mustFree("contract", "preflight", "--version", "1.0.0", "--staging", staging, "--json", id)
	if !strings.Contains(preflight, `"target_version":"1.0.0"`) {
		t.Fatalf("preflight did not judge 1.0.0: %s", preflight)
	}

	g.mustFree("contract", "publish", "--version", "1.0.0", "--staging", staging, id)
	if got := g.mainHeadMessage(); !strings.Contains(got, "Publish "+id) {
		t.Fatalf("main's tip is not the publish's land merge, so nothing landed:\n%s", got)
	}
	landed := "main:" + g.system + "/provides/export/" + relative
	if got := strings.TrimSpace(gitOutput(t, g.spaceDir, "cat-file", "-s", landed)); got != strconv.Itoa(largeSidecarBytes) {
		t.Fatalf("the blob on the space's main is %s bytes, want %d", got, largeSidecarBytes)
	}

	g.mustFree("sync")
	mustMkdirAll(t, filepath.Join(g.projectDir, "vendor"))
	materialized := g.mustFree("contract", "materialize", "--to", "vendor/export", "--json", id+"@1.0.0")
	if !strings.Contains(materialized, `"outcome":"materialized"`) {
		t.Fatalf("materialize did not report a real write: %s", materialized)
	}
	got, err := os.ReadFile(filepath.Join(g.projectDir, "vendor", "export", filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read the materialized sidecar: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the materialized sidecar differs from what was published: %d bytes, want %d", len(got), len(want))
	}

	checked := g.mustFree("contract", "check", "--suite", "--json", id+"@1.0.0")
	if !strings.Contains(checked, `"passed":true`) {
		t.Fatalf("the contract's self-suite over the landed version failed: %s", checked)
	}

	listed := g.mustFree("contracts", "--json")
	if !strings.Contains(listed, id) || !strings.Contains(listed, "1.0.0") {
		t.Fatalf("the mirror read does not list %s at 1.0.0: %s", id, listed)
	}

	// The second version: its land check reads 1.0.0's fixtures back out of git
	// as the compatibility baseline, and its publish replaces a landed large
	// blob with another one.
	next := bytes.Replace(want, []byte(`"pad":"x`), []byte(`"pad":"y`), 1)
	if bytes.Equal(next, want) {
		t.Fatal("the second version's sidecar is unchanged, so nothing below replaces a landed blob")
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(relative)), next, 0o644); err != nil {
		t.Fatalf("rewrite the sidecar: %v", err)
	}
	g.mustFree("contract", "publish", "--version", "1.1.0", "--staging", staging, id)
	g.mustFree("sync")
	materialized = g.mustFree("contract", "materialize", "--to", "vendor/export-next", "--json", id+"@1.1.0")
	if !strings.Contains(materialized, `"outcome":"materialized"`) {
		t.Fatalf("materialize of 1.1.0 did not report a real write: %s", materialized)
	}
	got, err = os.ReadFile(filepath.Join(g.projectDir, "vendor", "export-next", filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read the 1.1.0 sidecar: %v", err)
	}
	if !bytes.Equal(got, next) {
		t.Fatalf("the 1.1.0 sidecar differs from what was published: %d bytes, want %d", len(got), len(next))
	}
}

// largeSidecarJSON renders a valid JSON document of exactly largeSidecarBytes,
// valid UTF-8 with no NUL, shaped like the generated file that hit the bound.
func largeSidecarJSON(t *testing.T) []byte {
	t.Helper()
	const prefix, suffix = `{"rows":[`, "]}\n"
	var b bytes.Buffer
	b.WriteString(prefix)
	for n := 0; ; n++ {
		row := fmt.Sprintf(`{"n":%d,"label":"row %d"},`, n, n)
		if b.Len()+len(row)+len(`{"pad":""}`)+len(suffix) > largeSidecarBytes {
			break
		}
		b.WriteString(row)
	}
	pad := largeSidecarBytes - b.Len() - len(`{"pad":""}`) - len(suffix)
	b.WriteString(`{"pad":"` + strings.Repeat("x", pad) + `"}`)
	b.WriteString(suffix)
	if b.Len() != largeSidecarBytes {
		t.Fatalf("the sidecar fixture is %d bytes, want %d", b.Len(), largeSidecarBytes)
	}
	return b.Bytes()
}
