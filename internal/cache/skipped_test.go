package cache

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestDecodeArtifactFile_ReasonVocabulary is one case per SkipReason*
// vocabulary value, table-driven — including the duplicate mapping-key
// case (`thread:` written twice) that produced the filed 2026-07-26
// defect: an artifact silently invisible to every read verb but `a2a
// show`. Two distinct undecodable-yaml sub-cases are covered deliberately
// (ParseFrontmatter's own syntax probe rejecting it vs. decodeEnvelope's
// typed decode rejecting it) — see SkipReasonUndecodableYAML's doc comment
// for why both collapse into the one reason.
func TestDecodeArtifactFile_ReasonVocabulary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		setup      func(t *testing.T) (dir, path string)
		wantReason string
	}{
		{
			name: "unreadable",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				return dir, filepath.Join(dir, "missing.md")
			},
			wantReason: SkipReasonUnreadable,
		},
		{
			name: "not-frontmatter-shaped",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				path := filepath.Join(dir, "no-frontmatter.md")
				fxWriteFile(t, path, "just some markdown, no frontmatter delimiters at all\n")
				return dir, path
			},
			wantReason: SkipReasonNotFrontmatterShaped,
		},
		{
			name: "undecodable-yaml-duplicate-key",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				path := filepath.Join(dir, "dup-thread.md")
				fxWriteFile(t, path, "---\nid: XW-axon-dup\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbody\n")
				return dir, path
			},
			wantReason: SkipReasonUndecodableYAML,
		},
		{
			name: "undecodable-yaml-type-mismatch",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				path := filepath.Join(dir, "bad-refs-type.md")
				// refs: a scalar string, where envelopeProbe.Refs is
				// []refEntry — ParseFrontmatter's generic map[string]any
				// probe accepts this fine; only decodeEnvelope's typed
				// decode rejects it, exercising the OTHER path into the
				// same undecodable-yaml reason.
				fxWriteFile(t, path, "---\nid: XW-axon-bad\nrefs: not-a-list\n---\nbody\n")
				return dir, path
			},
			wantReason: SkipReasonUndecodableYAML,
		},
		{
			name: "no-id",
			setup: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				path := filepath.Join(dir, "no-id.md")
				fxWriteFile(t, path, "---\nthread: thread:axon:x\n---\nbody\n")
				return dir, path
			},
			wantReason: SkipReasonNoID,
		},
		{
			name: "unrelativizable-path",
			setup: func(t *testing.T) (string, string) {
				// filepath.Rel errors when base and target disagree on
				// absoluteness — no file needs to exist for this stage,
				// since decodeArtifactFile checks Rel before any I/O.
				return "/absolute/base", "relative/child.md"
			},
			wantReason: SkipReasonUnrelativizable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, path := tc.setup(t)
			a, skip := decodeArtifactFile(dir, path)
			if skip == nil {
				t.Fatalf("decodeArtifactFile(%q, %q) = %+v, nil skip; want reason %q", dir, path, a, tc.wantReason)
			}
			if skip.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q (path=%q)", skip.Reason, tc.wantReason, skip.Path)
			}
			if skip.Path == "" {
				t.Fatalf("skip.Path is empty")
			}
		})
	}
}

// writeMalformedArtifact writes and commits a *.md file at spaceRelPath
// whose raw bytes are NOT produced by yaml.Marshal (fixtureSpace's own
// commitArtifact always marshals a well-formed map, so it can never
// reproduce a duplicate-key document) — this is how the tests below get a
// REAL malformed artifact into a REAL fixture space's working tree, the
// same way the filed defect's own doubled `thread:` line reached disk.
func writeMalformedArtifact(t *testing.T, fx *fixtureSpace, spaceRelPath, raw string) {
	t.Helper()
	full := filepath.Join(fx.dir, filepath.FromSlash(spaceRelPath))
	fxMkdirAll(t, filepath.Dir(full))
	fxWriteFile(t, full, raw)
	fxCommitAndPush(t, fx.dir, "fixture: malformed artifact "+spaceRelPath)
}

// TestWalkArtifacts_BestEffort_ReportsSkipAndKeepsTheRest is the defect's
// own regression test at the walk level: a real fixture space carries two
// well-formed artifacts and one artifact with a `thread:` key written
// twice (the exact defect, filed 2026-07-26). The best-effort property
// this package depends on must NOT regress — the two good artifacts are
// still indexed — while the bad one is now reported, not silently dropped.
func TestWalkArtifacts_BestEffort_ReportsSkipAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})

	fx.commitArtifact("axon/exchanges/XW-axon-20260701-good1.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-good1", "type": "work_request",
		"title": "good one", "space": "fixture-space", "from": "axon", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(time.Now()),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-good2.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-good2", "type": "work_request",
		"title": "good two", "space": "fixture-space", "from": "axon", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(time.Now()),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")

	writeMalformedArtifact(t, fx, "axon/exchanges/XW-axon-20260701-bad.md",
		"---\nschema: envelope/v1\nid: XW-axon-20260701-bad\ntype: work_request\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbad body\n")

	artifacts, skips, err := walkArtifacts(fx.dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}

	if len(artifacts) != 2 {
		t.Fatalf("len(artifacts) = %d, want 2 (best-effort: one bad file must not blind the walk to the rest)", len(artifacts))
	}
	ids := map[string]bool{}
	for _, a := range artifacts {
		ids[a.Env.ID] = true
	}
	if !ids["XW-axon-20260701-good1"] || !ids["XW-axon-20260701-good2"] {
		t.Fatalf("expected both good artifacts present, got ids=%v", ids)
	}

	if len(skips) != 1 {
		t.Fatalf("len(skips) = %d, want 1: %+v", len(skips), skips)
	}
	if skips[0].Reason != SkipReasonUndecodableYAML {
		t.Fatalf("skips[0].Reason = %q, want %q", skips[0].Reason, SkipReasonUndecodableYAML)
	}
	if skips[0].Path != "axon/exchanges/XW-axon-20260701-bad.md" {
		t.Fatalf("skips[0].Path = %q, want axon/exchanges/XW-axon-20260701-bad.md", skips[0].Path)
	}
}

// TestWalkArtifacts_SkipsSortedByPath asserts deterministic ordering with
// TWO skipped files whose filesystem discovery order does not match their
// path's sort order — golden CLI/MCP output depends on this.
func TestWalkArtifacts_SkipsSortedByPath(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})

	// "zzz" sorts after "aaa" lexically; write zzz first so a naive
	// discovery-order (rather than sorted) result would fail this check.
	writeMalformedArtifact(t, fx, "axon/exchanges/zzz-bad.md", "no frontmatter here at all\n")
	writeMalformedArtifact(t, fx, "axon/exchanges/aaa-bad.md", "no frontmatter here at all either\n")

	_, skips, err := walkArtifacts(fx.dir)
	if err != nil {
		t.Fatalf("walkArtifacts: %v", err)
	}
	if len(skips) != 2 {
		t.Fatalf("len(skips) = %d, want 2: %+v", len(skips), skips)
	}
	if !sort.SliceIsSorted(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path }) {
		t.Fatalf("skips not sorted by path: %+v", skips)
	}
	if skips[0].Path != "axon/exchanges/aaa-bad.md" || skips[1].Path != "axon/exchanges/zzz-bad.md" {
		t.Fatalf("unexpected skip order: %+v", skips)
	}
}

// TestWalkEvents_ReportsSkip is the events-side "same convention, same
// silence" counterpart: a duplicated `subject:` key in a committed
// event/v1 file is silently skipped from the folded event set the same
// way an artifact would be, and now reported the same way too.
func TestWalkEvents_ReportsSkip(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	path := filepath.Join(fx.dir, "axon", "events", fx.year, "01HFXBAD0000000000000000.yaml")
	fxMkdirAll(t, filepath.Dir(path))
	fxWriteFile(t, path, "schema: event/v1\nevent: 01HFXBAD0000000000000000\nsubject: XW-axon-x\nsubject: XW-axon-y\n")
	fxCommitAndPush(t, fx.dir, "fixture: malformed event")

	events, skips, err := walkEvents(fx.dir)
	if err != nil {
		t.Fatalf("walkEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0", len(events))
	}
	if len(skips) != 1 {
		t.Fatalf("len(skips) = %d, want 1: %+v", len(skips), skips)
	}
	if skips[0].Reason != SkipReasonUndecodableYAML {
		t.Fatalf("skips[0].Reason = %q, want %q", skips[0].Reason, SkipReasonUndecodableYAML)
	}
}

// TestBuildIndex_CarriesSkipsAsSpaceLevelFact is item 3 of the brief: a
// skip cannot be per-item (there is no item), so buildIndex must surface
// it as its own return value rather than folding it into foldedArtifact.
func TestBuildIndex_CarriesSkipsAsSpaceLevelFact(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	fx.commitArtifact("axon/exchanges/XW-axon-good.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-good", "type": "work_request",
		"title": "good", "space": "fixture-space", "from": "axon", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(time.Now()),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	writeMalformedArtifact(t, fx, "axon/exchanges/XW-axon-bad.md",
		"---\nid: XW-axon-bad\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbad\n")

	artifacts, skips, err := buildIndex(context.Background(), "sp1", fx.dir, "acme", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("len(artifacts) = %d, want 1", len(artifacts))
	}
	if len(skips) != 1 {
		t.Fatalf("len(skips) = %d, want 1: %+v", len(skips), skips)
	}
	if skips[0].Path != "axon/exchanges/XW-axon-bad.md" {
		t.Fatalf("skips[0].Path = %q", skips[0].Path)
	}
}

// TestStore_SkippedFiles_SpaceScopedAndAll exercises the two accessors
// stage 2 needs: SkippedFiles for one space (`a2a thread` is space-scoped)
// and AllSkippedFiles across every connected space (`a2a inbox` is not).
func TestStore_SkippedFiles_SpaceScopedAndAll(t *testing.T) {
	t.Parallel()
	dirty := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	writeMalformedArtifact(t, dirty, "axon/exchanges/XW-axon-bad.md",
		"---\nid: XW-axon-bad\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbad\n")

	clean := newFixtureSpace(t, fixtureParticipant{System: "seomatrix"})
	clean.commitArtifact("seomatrix/exchanges/XW-seomatrix-good.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-seomatrix-good", "type": "work_request",
		"title": "good", "space": "fixture-space", "from": "seomatrix", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(time.Now()),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "body")

	store := NewStore("axon", t.TempDir(), []SpaceMirror{
		{SpaceID: "sp-dirty", Dir: dirty.dir, Manifest: mustManifest(t, dirty)},
		{SpaceID: "sp-clean", Dir: clean.dir, Manifest: mustManifest(t, clean)},
	}, time.Now, 0)

	ctx := context.Background()

	got, err := store.SkippedFiles(ctx, "sp-dirty")
	if err != nil {
		t.Fatalf("SkippedFiles(sp-dirty): %v", err)
	}
	if len(got) != 1 || got[0].Reason != SkipReasonUndecodableYAML {
		t.Fatalf("SkippedFiles(sp-dirty) = %+v, want one undecodable-yaml skip", got)
	}

	gotClean, err := store.SkippedFiles(ctx, "sp-clean")
	if err != nil {
		t.Fatalf("SkippedFiles(sp-clean): %v", err)
	}
	if len(gotClean) != 0 {
		t.Fatalf("SkippedFiles(sp-clean) = %+v, want none", gotClean)
	}

	all, err := store.AllSkippedFiles(ctx)
	if err != nil {
		t.Fatalf("AllSkippedFiles: %v", err)
	}
	if len(all["sp-dirty"]) != 1 {
		t.Fatalf("AllSkippedFiles()[sp-dirty] = %+v, want 1 entry", all["sp-dirty"])
	}
	if len(all["sp-clean"]) != 0 {
		t.Fatalf("AllSkippedFiles()[sp-clean] = %+v, want 0 entries", all["sp-clean"])
	}

	// Best-effort property, at the Store level: the clean space's own
	// artifact is still fully indexed despite the OTHER space carrying a
	// malformed file.
	idx, _, err := store.index(ctx)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	found := false
	for _, fa := range idx["sp-clean"] {
		if fa.Env.ID == "XW-seomatrix-good" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sp-clean's own good artifact missing from index: %+v", idx["sp-clean"])
	}
}

// TestSkippedFilesNeedsNoOwnSystemOrCacheDir pins the invariant `a2a doctor`
// leans on. doctorCheckSkippedFiles is deliberately self-contained — a
// diagnostic that depended on the app wiring it exists to diagnose would be
// useless exactly when it is needed — so it resolves the mirrors itself and
// builds a throwaway Store with EMPTY ownSystem and cacheDir, on the stated
// ground that AllSkippedFiles' own call path reads neither.
//
// That ground is a claim about THIS package, so the guard belongs here: if
// index() ever starts reading cacheDir (a cursor file, a memo) or ownSystem,
// this test reds in the package that made the change, naming it, instead of
// the doctor row silently degrading to "no skips" in the field.
func TestSkippedFilesNeedsNoOwnSystemOrCacheDir(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	writeMalformedArtifact(t, fx, "axon/exchanges/XW-axon-bad.md",
		"---\nid: XW-axon-bad\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbad\n")

	store := NewStore("", "", []SpaceMirror{
		{SpaceID: "sp", Dir: fx.dir, Manifest: mustManifest(t, fx)},
	}, time.Now, 0)

	bySpace, err := store.AllSkippedFiles(context.Background())
	if err != nil {
		t.Fatalf("AllSkippedFiles with empty ownSystem/cacheDir: %v", err)
	}
	if len(bySpace["sp"]) != 1 {
		t.Fatalf("AllSkippedFiles = %+v, want the one skip reported even with no ownSystem and no cacheDir", bySpace)
	}
}
