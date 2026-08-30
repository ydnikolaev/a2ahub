package surface

import (
	"path/filepath"
	"strings"
)

// Surface is one verified provider fact row: where that provider's runtime
// discovers a skill (SkillsHome) and where it reads always-on context
// (ContextFile), plus the provenance of the row itself (SourceURL,
// VerifiedOn) so it can be re-verified instead of recalled.
type Surface struct {
	// ID is the surface's stable identifier ("claude", "codex").
	ID string
	// SkillsHome is the repo-relative directory this surface's runtime scans
	// for skills, e.g. ".claude/skills".
	SkillsHome string
	// ContextFile is the repo-relative file this surface reads as always-on
	// context, e.g. "CLAUDE.md" or "AGENTS.md".
	ContextFile string
	// ReadsAgentsMD is true when this surface's runtime reads AGENTS.md as
	// always-on context (Codex does; Claude Code does not — it reads
	// CLAUDE.md instead, per code.claude.com/docs/en/memory).
	ReadsAgentsMD bool
	// SourceURL is the doc page this row was read from.
	SourceURL string
	// VerifiedOn is the date (YYYY-MM-DD) this row was last verified against
	// SourceURL.
	VerifiedOn string
}

// Registry returns every known provider-surface row, in a fixed,
// deterministic order (claude, then codex, then dsh). Adding a provider
// means adding a row here, verified against that provider's own current
// docs — see spec 32 §4 for the deliberately-out-of-scope list (Cursor,
// Gemini CLI, Copilot/VS Code, opencode, Amp).
func Registry() []Surface {
	return []Surface{
		{
			ID:            "claude",
			SkillsHome:    ".claude/skills",
			ContextFile:   "CLAUDE.md",
			ReadsAgentsMD: false,
			SourceURL:     "https://code.claude.com/docs/en/skills",
			VerifiedOn:    "2026-07-23",
		},
		{
			ID:            "codex",
			SkillsHome:    ".codex/skills",
			ContextFile:   "AGENTS.md",
			ReadsAgentsMD: true,
			SourceURL:     "https://developers.openai.com/codex/skills",
			VerifiedOn:    "2026-07-23",
		},
		{
			ID:            "dsh",
			SkillsHome:    ".dsh/skills",
			ContextFile:   "AGENTS.md",
			ReadsAgentsMD: true,
			SourceURL:     "https://github.com/deepseek-ai/deepseek-harness/blob/0a53fb55bea101816fa226bb964ae2bed71c343b/packages/skill/skill-filesystem/src/index.ts",
			VerifiedOn:    "2026-08-30",
		},
	}
}

// MarkerDir is the directory whose presence under a repo root means this
// surface is in use (".claude", ".codex") — the parent of SkillsHome.
func (s Surface) MarkerDir() string {
	return filepath.Dir(s.SkillsHome)
}

// ByID returns the registry row for id, and whether it was found.
func ByID(id string) (Surface, bool) {
	for _, s := range Registry() {
		if s.ID == id {
			return s, true
		}
	}
	return Surface{}, false
}

// KnownIDs renders every registry row's ID as one human-readable list
// ("claude, codex, dsh") — the string a message naming the surfaces a2a knows
// must DERIVE rather than restate.
//
// This exists because three messages restated it and went stale the moment a
// third row landed: `a2a skill link` offered "(known: claude, codex)" while
// ByID("dsh") already succeeded, and doctor told a dsh-only project it showed
// no agent surface. A hardcoded list beside a registry survives exactly the
// drift it is there to describe, so the list is computed and the next row
// propagates without anyone remembering.
func KnownIDs() string {
	return joinRows(func(s Surface) string { return s.ID }, ", ")
}

// KnownMarkerDirs renders every row's MarkerDir as a reader-facing alternation
// (".claude/ or .codex/ or .dsh/") for the messages that tell someone what a2a
// looked for and did not find. Same reason as KnownIDs: derived, never typed.
func KnownMarkerDirs() string {
	return joinRows(func(s Surface) string { return s.MarkerDir() + "/" }, " or ")
}

func joinRows(field func(Surface) string, sep string) string {
	rows := Registry()
	parts := make([]string, 0, len(rows))
	for _, s := range rows {
		parts = append(parts, field(s))
	}
	return strings.Join(parts, sep)
}
