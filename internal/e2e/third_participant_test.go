package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestThirdParticipantJoinsAndExchanges makes "two systems now, more later"
// a checked claim instead of an assumption.
//
// Everything else in this package starts from a space whose participants are
// all present from the first commit. That is not how a space grows: two
// people start, and a third joins a space that already has history. The
// joining flow is entirely MANUAL — there is no `a2a space add-participant`
// verb — so what a real admin does is edit `space.yaml`, scaffold the new
// system's section, add its CODEOWNERS entry, and open a pull request. If
// any part of that leaves the space in a state the newcomer's first write
// cannot pass, nobody finds out until a third person actually joins.
//
// The test therefore does the join the way the runbook describes it, and
// then asserts the only thing that matters: the newcomer can write, and an
// EXISTING participant can read what they wrote.
func TestThirdParticipantJoinsAndExchanges(t *testing.T) {
	t.Parallel()

	// A space that already has two participants and history — the "we have
	// been using this for a while" starting point.
	fx := spacefixture.New(t, "axon", "beta")
	origin := fx.RemoteURL()
	axonMirror := fx.Clone("axon")

	seedTwoParticipantManifest(t, origin, "fixture-space", axonMirror)

	// --- the join, exactly as an admin performs it ----------------------
	admin := t.TempDir()
	gitRun(t, "", "clone", origin, admin)

	// 1. the manifest gains the newcomer
	writeMirrorFile(t, admin, "space.yaml", properManifestYAML("fixture-space", "axon", "beta", "gamma"))

	// 2. the section scaffold. The template ships none of this, and no
	//    published document enumerated it until 2026-07-26 — which is
	//    exactly why it is written out here: if the list is wrong, the
	//    newcomer's first write fails and the space looks broken.
	for _, dir := range []string{"provides", "requires", "exchanges", "events", "docs"} {
		writeMirrorFile(t, admin, filepath.Join("gamma", dir, ".gitkeep"), "")
	}
	writeMirrorFile(t, admin, filepath.Join("gamma", "consumes.yaml"),
		"schema: consumes/v1\nsystem: gamma\ndependencies: []\n")

	// 3. the CODEOWNERS entry that gates the newcomer's own provides
	writeMirrorFile(t, admin, "CODEOWNERS", "/space.yaml @org/space-admins\n/gamma/provides/** @org/gamma\n")

	gitRun(t, admin, "add", "-A")
	gitRun(t, admin, "commit", "-m", "space: gamma joins")
	gitRun(t, admin, "push", "origin", "main")

	// --- the newcomer's FIRST write -------------------------------------
	// A newcomer has no fixture clone, because a newcomer is new — they
	// clone the space for the first time, after the join landed. Using
	// fx.Clone here would have handed gamma a checkout that predates its own
	// membership, which is the one state a real joiner never has.
	gammaMirror := filepath.Join(t.TempDir(), "gamma-mirror")
	gitRun(t, "", "clone", origin, gammaMirror)

	manifest := readManifestFromMirror(t, gammaMirror)
	if len(manifest.Participants) != 3 {
		t.Fatalf("the joined manifest parses to %d participants, want 3 — a newcomer whose own entry "+
			"does not parse is invisible to every membership check", len(manifest.Participants))
	}

	// The write goes through the REAL funnel — the section guard, the branch
	// naming and the commit are the parts a newcomer's first write can trip
	// on, and they all live there. Driving the submit VERB on top would add
	// staging/validator plumbing this test does not question.
	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")

	id := "XQ-gamma-20260726-join"
	layout, err := space.NewLayout("gamma")
	if err != nil {
		t.Fatalf("NewLayout(gamma): %v", err)
	}
	req := space.SubmitRequest{
		RepoDir: gammaMirror, System: "gamma", Verb: "submit", ArtifactID: id,
		Files: []space.FileWrite{
			{Path: layout.Exchange(id), Content: []byte(newcomerQuestion(id))},
			{Path: layout.EventFile("2026", "01J8QYK2Z3ABCDEFGHJKMNPQSJ"), Content: []byte("event: submit\n")},
		},
		CommitMessage: "a2a(question): " + id,
		RemoteURL:     origin,
		Repo:          host.Repo{Owner: "acme", Name: "fixture-space"},
		BaseBranch:    "main",
	}
	if _, err := funnel.Submit(context.Background(), req); err != nil {
		t.Fatalf("the newcomer's first write was refused: %v\n\n"+
			"A third participant who cannot write is the whole of what \"more systems later\" promises", err)
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one PR from the newcomer's first write, got %d", len(fakeHost.Opens))
	}

	// --- an EXISTING participant reads it -------------------------------
	mergeBranchToMain(t, gammaMirror, lastOpenedBranch(fakeHost))
	fetchMain(t, axonMirror)

	got := filepath.Join(axonMirror, "gamma", "exchanges", id+".md")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("axon cannot see the newcomer's artifact at %s: %v — a write nobody can read is not "+
			"an exchange", got, err)
	}
}

// seedTwoParticipantManifest pushes a LIST-shaped two-participant manifest,
// so the space starts in the state a real one is in before anybody joins.
// spacefixture seeds a MAP-shaped space.yaml that does not parse as a
// space.Manifest at all (see helpers_test.go's properManifestYAML note).
func seedTwoParticipantManifest(t *testing.T, originDir, spaceID string, existingClones ...string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "clone", originDir, dir)
	writeMirrorFile(t, dir, "space.yaml", properManifestYAML(spaceID, "axon", "beta"))
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "space: the two founding participants")
	gitRun(t, dir, "push", "origin", "main")
	for _, clone := range existingClones {
		fetchMain(t, clone)
	}
}

// readManifestFromMirror parses the space manifest a mirror currently holds,
// through the SAME parser the product uses — a hand-rolled read here could
// accept a shape the product rejects, and this test's whole subject is
// whether the joined manifest is one the product accepts.
func readManifestFromMirror(t *testing.T, mirrorDir string) space.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		t.Fatalf("read space.yaml: %v", err)
	}
	m, err := space.ParseManifest(raw)
	if err != nil {
		t.Fatalf("the joined space.yaml does not parse: %v\n%s", err, raw)
	}
	return m
}

// newcomerQuestion renders the newcomer's first artifact. Written out
// rather than reusing the axon-hardcoded helper, because the AUTHOR being
// the new system is the entire point.
func newcomerQuestion(id string) string {
	return strings.Join([]string{
		"---",
		"schema: envelope/v1",
		"id: " + id,
		"type: question",
		"title: does the newcomer's first write land",
		"space: fixture-space",
		"from: gamma",
		"to: [axon]",
		"actor: {kind: agent, name: bot, system: gamma}",
		"created: 2026-07-26T00:00:00Z",
		"priority: p3",
		"blocking: false",
		"classification: internal",
		"---",
		"body",
		"",
	}, "\n")
}
