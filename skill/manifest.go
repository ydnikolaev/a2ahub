package skill

import (
	"encoding/json"
	"fmt"
	"io/fs"
)

// manifest.go owns the typed decode of docs-manifest.json — the curation SSOT
// that says which embedded Markdown pages are documentation, in which order,
// under which navigation group. This package holds it because this package
// holds the embed the manifest describes.
//
// It lives here rather than in a consumer because there are now two: the
// dashboard's Documentation tab (internal/html) renders every section, and the
// CLI's reader verb resolves a topic argument against the same list. A second
// decode would be a second answer to "which sections exist" — the defect class
// answers-that-hold-2026-08 exists to remove (C3: two verbs deciding one
// predicate). ADR-019 is the rule: what both surfaces need moves DOWN.

// DocsManifestPath is the manifest's path inside a skill tree, relative to the
// tree root — the same path whether the tree is the embedded Files or a
// fixture built for a test.
const DocsManifestPath = "a2ahub/docs-manifest.json"

// DocsManifestSchema is the only schema identifier LoadDocsManifest accepts.
// A manifest carrying anything else is refused rather than decoded on a guess.
const DocsManifestSchema = "a2a-docs-manifest/v1"

// DocSectionEntry is one curated documentation section: its stable id (also
// the dashboard's deep-link anchor and the CLI reader's topic argument), its
// navigation group, its display title, and the page's path inside the tree.
// Slice order is the in-group section order.
type DocSectionEntry struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
	File  string `json:"file"`
}

// DocsManifest is a full decode of docs-manifest.json. Every key the file
// carries is decoded, including LoopCorpus, which no Go caller reads today —
// scripts/check-loop-coverage.sh does. A shared type that silently drops a key
// its artifact carries is how the next drift starts, so this one drops none.
type DocsManifest struct {
	Schema     string            `json:"schema"`
	Groups     []string          `json:"groups"`
	LoopCorpus []string          `json:"loop_corpus"`
	Sections   []DocSectionEntry `json:"sections"`
}

// LoadDocsManifest reads and validates the docs manifest from any skill tree.
// It takes an fs.FS rather than reading Files directly so a caller can prove a
// behaviour against a fixture tree — that a section added to the manifest
// becomes reachable with no code change is a property of this decode, and a
// hardcoded embed would make it unprovable.
//
// It returns an error rather than panicking: a CLI verb must refuse and say
// why. EmbeddedDocsManifest is the panicking convenience for package-level
// initialization, where there is no caller to hand an error to.
func LoadDocsManifest(tree fs.FS) (DocsManifest, error) {
	b, err := fs.ReadFile(tree, DocsManifestPath)
	if err != nil {
		return DocsManifest{}, fmt.Errorf("skill: read %s: %w", DocsManifestPath, err)
	}
	var manifest DocsManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return DocsManifest{}, fmt.Errorf("skill: decode %s: %w", DocsManifestPath, err)
	}
	if manifest.Schema != DocsManifestSchema {
		return DocsManifest{}, fmt.Errorf(
			"skill: %s declares schema %q, want %q — refusing to read a manifest whose contract this binary does not know",
			DocsManifestPath, manifest.Schema, DocsManifestSchema,
		)
	}
	if len(manifest.Groups) == 0 || len(manifest.Sections) == 0 {
		return DocsManifest{}, fmt.Errorf(
			"skill: %s carries %d groups and %d sections — a manifest declaring no documentation is a broken tree, not an empty one",
			DocsManifestPath, len(manifest.Groups), len(manifest.Sections),
		)
	}
	return manifest, nil
}

// EmbeddedDocsManifest returns the manifest of the tree compiled into this
// binary. It panics on a bad manifest because the input is embedded at build
// time: there is no runtime state that could produce one, and no caller who
// could act on the error.
func EmbeddedDocsManifest() DocsManifest {
	manifest, err := LoadDocsManifest(Files)
	if err != nil {
		panic(err.Error())
	}
	return manifest
}
