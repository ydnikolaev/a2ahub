package html

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// TestAssemble_NodesAndContractEdges drives Assemble over a hand-built Store
// (a temp mirror with a manifest + a consumes.yaml) — deterministic, no
// network — and asserts the nodes, per-space health, and the derived
// contract-dependency edge.
func TestAssemble_NodesAndContractEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	consumes := "schema: consumes/v1\nsystem: axon\ndependencies:\n" +
		"  - contract: XC-seomatrix-feed-v1\n    major: 1\n    since: 2026-07-01\n"
	if err := os.WriteFile(filepath.Join(dir, "axon", "consumes.yaml"), []byte(consumes), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := space.Manifest{Space: "getvisa", Participants: []space.Participant{
		{System: "axon", Section: "axon/", Org: "r22d222", Status: "active", Owners: []string{"ydnikolaev"}},
		{System: "seomatrix", Section: "seomatrix/", Org: "r22d222", Status: "active", Owners: []string{"xpressmike"}},
	}, Schema: "space/v1", MinBinaryVersion: "0.12.0"}
	workflowDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := "jobs:\n  validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.13.0\n"
	if err := os.WriteFile(filepath.Join(workflowDir, "a2a-validate.yml"), []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	store := cache.NewStore("axon", t.TempDir(),
		[]cache.SpaceMirror{{SpaceID: "getvisa", Dir: dir, RepoURL: "https://github.com/r22d222/a2a", Manifest: manifest}},
		time.Now, 0)

	data, err := Assemble(context.Background(), store, "", time.Now())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(data.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2: %+v", len(data.Nodes), data.Nodes)
	}
	var axon *Node
	for i := range data.Nodes {
		if data.Nodes[i].System == "axon" {
			axon = &data.Nodes[i]
		}
	}
	if axon == nil || !axon.Self || axon.Org != "r22d222" {
		t.Fatalf("axon self node wrong: %+v", axon)
	}

	if len(data.Spaces) != 1 || data.Spaces[0].ParticipantCount != 2 || !data.Spaces[0].Readable {
		t.Fatalf("space health: %+v", data.Spaces)
	}
	if data.Spaces[0].SchemaVersion != "space/v1" ||
		data.Spaces[0].MinBinaryVersion != "0.12.0" ||
		data.Spaces[0].WorkflowVersion != "0.13.0" {
		t.Fatalf("space compatibility axes not assembled: %+v", data.Spaces[0])
	}
	if len(data.ReleaseNotes) == 0 {
		t.Fatal("embedded release-note corpus must be available to the HTML data model")
	}

	if len(data.ContractEdges) != 1 {
		t.Fatalf("contract edges = %d, want 1: %+v", len(data.ContractEdges), data.ContractEdges)
	}
	e := data.ContractEdges[0]
	if e.From != "axon" || e.To != "seomatrix" || e.Contract != "XC-seomatrix-feed-v1" ||
		e.PinnedMajor != 1 || e.Drift != "dangling" || e.Space != "getvisa" {
		t.Fatalf("contract edge wrong: %+v", e)
	}

	if data.Self != "axon" {
		t.Fatalf("self = %q, want axon", data.Self)
	}
}

func TestDependencyFacts_UsesPinnedMajorLifecycle(t *testing.T) {
	t.Parallel()
	ci := cache.ContractInfo{
		Version: "2.3.0", State: "published",
		Versions: []cache.ContractVersion{
			{Version: "1.4.1", State: "deprecated", Sunset: "2026-10-01", Successor: "XC-axon-ingest@2.0.0"},
			{Version: "2.3.0", State: "published"},
		},
	}

	version, state, sunset, successor, majors, drift := dependencyFacts(ci, 1)
	if version != "1.4.1" || state != "deprecated" || sunset != "2026-10-01" ||
		successor != "XC-axon-ingest@2.0.0" || drift != "deprecated" ||
		len(majors) != 2 || majors[0] != 1 || majors[1] != 2 {
		t.Fatalf("pinned deprecated line wrong: version=%q state=%q sunset=%q successor=%q majors=%v drift=%q",
			version, state, sunset, successor, majors, drift)
	}

	_, state, _, _, _, drift = dependencyFacts(ci, 3)
	if state != "missing" || drift != "missing" {
		t.Fatalf("unpublished registered major must be explicit, got state=%q drift=%q", state, drift)
	}

	zero := cache.ContractInfo{
		Version: "1.0.0", State: "published",
		Versions: []cache.ContractVersion{
			{Version: "not-semver", State: "published"},
			{Version: "0.9.0", State: "deprecated"},
			{Version: "1.0.0", State: "published"},
		},
	}
	version, state, _, _, majors, drift = dependencyFacts(zero, 0)
	if version != "0.9.0" || state != "deprecated" || drift != "deprecated" ||
		len(majors) != 2 || majors[0] != 0 || majors[1] != 1 {
		t.Fatalf("major zero must remain a valid registration: version=%q state=%q majors=%v drift=%q",
			version, state, majors, drift)
	}
}

func TestSpaceWorkflowVersion_MixedRefsAreExplicit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	workflowDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "jobs:\n" +
		"  first:\n    uses: org/repo/.github/workflows/a2a-validate-reusable.yml@v0.12.0\n" +
		"  second:\n    uses: org/repo/.github/workflows/a2a-validate-reusable.yml@v0.13.0\n"
	if err := os.WriteFile(filepath.Join(workflowDir, "a2a-validate.yml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	version, ref := spaceWorkflowVersion(dir)
	if version != "mixed" ||
		ref != "org/repo/.github/workflows/a2a-validate-reusable.yml@v0.12.0, org/repo/.github/workflows/a2a-validate-reusable.yml@v0.13.0" {
		t.Fatalf("mixed workflow refs not explicit: version=%q ref=%q", version, ref)
	}
}

// writeArtifact writes a *.md artifact at dir/relPath with the given
// envelope/v1 fields (a plain map — this test's own minimal envelope
// authoring, mirroring internal/cache's own fixtureSpace.commitArtifact
// shape but WITHOUT git: buildIndex's own commitOrder degrades to
// OrderKnown=false / Order="declared" when no commit history is readable,
// which is exactly what this test wants — member order falls back to each
// envelope's own `created` field).
func writeArtifact(t *testing.T, dir, relPath string, fields map[string]any, body string) {
	t.Helper()
	raw, err := yaml.Marshal(fields)
	if err != nil {
		t.Fatalf("writeArtifact: marshal envelope: %v", err)
	}
	full := "---\n" + string(raw) + "---\n" + body
	fullPath := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAssemble_Threads is spec 46 §T6.1's own acceptance shape: a
// two-member thread (a work_request + its response, linked by the
// response's own `parent` field) appears in Data.Threads with both members
// and the `parent` DocLink between them, in transcript order; a
// one-member thread does NOT appear at all (§T3's presentation rule).
func TestAssemble_Threads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const hostileTitle = `<script>alert(1)</script> question`
	const threadA = "thread:axon-20260701-tt01"
	const parentID = "XW-axon-20260701-q1"
	const responseID = "XS-seomatrix-20260701-r1"
	writeArtifact(t, dir, "axon/exchanges/"+parentID+".md", map[string]any{
		"schema": "envelope/v1", "id": parentID, "type": "work_request", "title": hostileTitle,
		"space": "getvisa", "from": "axon", "to": []string{"seomatrix"}, "thread": threadA,
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": "2026-07-01T10:00:00Z", "priority": "p2", "blocking": false, "classification": "internal",
	}, "question body")
	writeArtifact(t, dir, "seomatrix/exchanges/"+responseID+".md", map[string]any{
		"schema": "envelope/v1", "id": responseID, "type": "response", "title": "response",
		"space": "getvisa", "from": "seomatrix", "to": []string{"axon"}, "parent": parentID,
		"result": "answered", "thread": threadA,
		"actor":   map[string]any{"kind": "agent", "name": "seomatrix-bot"},
		"created": "2026-07-01T10:05:00Z", "priority": "p2", "blocking": false, "classification": "internal",
	}, "response body")

	const threadB = "thread:axon-20260702-tt02"
	const soloID = "XW-axon-20260702-solo"
	writeArtifact(t, dir, "axon/exchanges/"+soloID+".md", map[string]any{
		"schema": "envelope/v1", "id": soloID, "type": "work_request", "title": "solo, no reply yet",
		"space": "getvisa", "from": "axon", "to": []string{"seomatrix"}, "thread": threadB,
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": "2026-07-02T09:00:00Z", "priority": "p2", "blocking": false, "classification": "internal",
	}, "solo body")

	manifest := space.Manifest{Space: "getvisa", Participants: []space.Participant{
		{System: "axon", Section: "axon/", Org: "r22d222", Status: "active", Owners: []string{"ydnikolaev"}},
		{System: "seomatrix", Section: "seomatrix/", Org: "r22d222", Status: "active", Owners: []string{"xpressmike"}},
	}}
	store := cache.NewStore("axon", t.TempDir(),
		[]cache.SpaceMirror{{SpaceID: "getvisa", Dir: dir, RepoURL: "https://github.com/r22d222/a2a", Manifest: manifest}},
		time.Now, 0)

	data, err := Assemble(context.Background(), store, "", time.Now())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(data.Threads) != 1 {
		t.Fatalf("Threads = %d, want 1 (the solo thread must not be listed): %+v", len(data.Threads), data.Threads)
	}
	th := data.Threads[0]
	if th.ID != threadA || th.Space != "getvisa" || th.MemberCount != 2 {
		t.Fatalf("thread wrong: %+v", th)
	}
	if th.Opener.ID != parentID {
		t.Fatalf("opener = %+v, want the earlier-created work_request %s", th.Opener, parentID)
	}
	// The hostile title reaches the model VERBATIM here (Go struct field);
	// Render()'s own json.MarshalIndent escaping — asserted generically in
	// html_test.go's TestRender_SanitizesScriptBreakout — is what keeps it
	// from executing once injected into the page.
	if th.Opener.Title != hostileTitle {
		t.Fatalf("opener title = %q, want %q verbatim on the model", th.Opener.Title, hostileTitle)
	}
	if len(th.Members) != 2 {
		t.Fatalf("members = %d, want 2: %+v", len(th.Members), th.Members)
	}
	if th.Members[0].ID != parentID || th.Members[1].ID != responseID {
		t.Fatalf("members not in transcript order: %+v", th.Members)
	}
	// Whose move it is must never name self: YourMove already says that, and
	// repeating it turns "you and seomatrix" into "you, you and seomatrix".
	for _, who := range th.WaitingOthers {
		if who == "axon" {
			t.Fatalf("WaitingOthers names self: %v", th.WaitingOthers)
		}
	}
	if len(th.Links) != 1 {
		t.Fatalf("links = %d, want 1 (the response's own parent link): %+v", len(th.Links), th.Links)
	}
	link := th.Links[0]
	if link.From != responseID || link.To != parentID || link.Kind != "parent" {
		t.Fatalf("link wrong: %+v", link)
	}

	if len(data.ArtifactDetails) != 3 {
		t.Fatalf("ArtifactDetails = %d, want the 3 visible inbox/outbox records: %+v", len(data.ArtifactDetails), data.ArtifactDetails)
	}
	byDetailID := map[string]ArtifactDetail{}
	for _, detail := range data.ArtifactDetails {
		byDetailID[detail.ID] = detail
	}
	responseDetail, ok := byDetailID[responseID]
	if !ok {
		t.Fatalf("response detail %s missing: %+v", responseID, data.ArtifactDetails)
	}
	if responseDetail.Space != "getvisa" || responseDetail.Body != "response body" ||
		responseDetail.Envelope["parent"] != parentID || responseDetail.SourceClass != "canonical" {
		t.Fatalf("response detail is not the canonical show projection: %+v", responseDetail)
	}
	parentDetail, ok := byDetailID[parentID]
	if !ok || parentDetail.Title != hostileTitle || parentDetail.Envelope["title"] != hostileTitle {
		t.Fatalf("hostile title was not preserved as untrusted detail data: %+v", parentDetail)
	}
	for _, fact := range data.Unavailable {
		if fact.ID == "artifact-detail-bodies" {
			t.Fatalf("fulfilled artifact detail capability still reported unavailable: %+v", fact)
		}
	}
}
