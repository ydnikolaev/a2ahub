package mcp

// a2a_docs (P9, answers-that-hold-2026-08 spec 09
// docs/features/active/answers-that-hold-2026-08/specs/09-a-doc-page-is-
// reachable.md; MCP twin of internal/cli's cmd_docs.go): reads the embedded
// a2ahub documentation corpus via skill.LoadDocsManifest — the manifest IS
// the topic vocabulary, read at RUNTIME, never copied here (ADR-019 / class
// C3, this phase's own reason for existing). Registered in
// registerSpaceFree (tools.go): it needs nothing but embedded bytes, no
// local cache, no connected space — the same shape a2a_whatsnew and
// a2a_adapt already use.
//
// Q-B (spec 09 §T1) is answered before this file was written: a paired
// tool, not an mcpExcludedVerbs row. Every existing mcpExcludedVerbs row is
// a host-machine act an MCP client has no business performing; reading a
// doc page is not one. This server has no resource surface at all, so "read
// it as a resource instead" is false here. And US-1's reasoning — "I cannot
// open a file I have no path for" — applies identically to an MCP agent,
// which has strictly LESS filesystem reach than a CLI caller.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/skill"
)

// registerDocsTool registers a2a_docs onto r, over the real embedded skill
// tree (skill.Files). Called by registerSpaceFree (tools.go) — kept as its
// own function, mirroring registerAdaptTool's own precedent in this
// package, so tools.go's registerSpaceFree body stays a one-line call
// rather than growing a second file's ToolSpec/rawSchema literal inline.
func registerDocsTool(r *Registry) {
	r.Register(ToolSpec{
		Name:        "a2a_docs",
		Description: "embedded a2ahub documentation: omit topic to list every known topic (id/group/title), or pass one to read its page",
		InputSchema: rawSchema(map[string]propSpec{
			"topic": {"string", "a documentation topic id from the vocabulary this tool lists when topic is omitted"},
		}),
		Handler: newDocsHandler(skill.Files),
	})
}

// DocsInput is a2a_docs's structured input: an empty/omitted Topic lists
// the vocabulary; a topic id returns that section's page as the response
// body.
type DocsInput struct {
	Topic string `json:"topic,omitempty"`
}

// DocsTopic is one listed vocabulary entry (Topic omitted) or the read
// section's own metadata (Topic set, alongside the page in the handler's
// body return) — a2a_docs' StructuredContent shape either way.
type DocsTopic struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
}

// newDocsHandler builds a2a_docs's handler. tree defaults to skill.Files in
// production (tools.go's registration); tests substitute a fixture fs.FS —
// skill.LoadDocsManifest's own fixture-FS shape (skill/manifest_test.go) —
// so AC-5 (a manifest addition becomes a topic with no code change) is
// provable on this surface too.
func newDocsHandler(tree fs.FS) HandlerFunc {
	return func(_ context.Context, args json.RawMessage) (any, string, error) {
		var in DocsInput
		if err := decodeStrict(args, &in, "a2a_docs", 0); err != nil {
			return nil, "", err
		}

		manifest, err := skill.LoadDocsManifest(tree)
		if err != nil {
			return nil, "", fmt.Errorf("a2a_docs: %w", err)
		}

		if in.Topic == "" {
			topics := make([]DocsTopic, 0, len(manifest.Sections))
			for _, s := range manifest.Sections {
				topics = append(topics, DocsTopic{ID: s.ID, Group: s.Group, Title: s.Title})
			}
			return topics, "", nil
		}

		for _, s := range manifest.Sections {
			if s.ID != in.Topic {
				continue
			}
			body, readErr := fs.ReadFile(tree, s.File)
			if readErr != nil {
				return nil, "", fmt.Errorf(
					"a2a_docs: topic %q: the manifest names %s for this section, which is missing from this build's embedded tree",
					in.Topic, s.File,
				)
			}
			return DocsTopic{ID: s.ID, Group: s.Group, Title: s.Title}, string(body), nil
		}

		return nil, "", fmt.Errorf("a2a_docs: unknown topic %q — known topics: %s", in.Topic, docsHandlerTopicIDs(manifest))
	}
}

// docsHandlerTopicIDs returns every manifest section id, sorted and
// comma-joined, for an unknown-topic refusal to name.
func docsHandlerTopicIDs(manifest skill.DocsManifest) string {
	ids := make([]string, 0, len(manifest.Sections))
	for _, s := range manifest.Sections {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}
