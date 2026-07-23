package surface

import "path/filepath"

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
// deterministic order (claude, then codex). Adding a provider means adding a
// row here, verified against that provider's own current docs — see
// spec 32 §4 for the deliberately-out-of-scope list (Cursor, Gemini CLI,
// Copilot/VS Code, opencode, Amp).
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
