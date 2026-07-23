package surface

import (
	"os"
	"path/filepath"
	"strings"
)

// LinkMode records how a discovery entry was installed for a surface.
type LinkMode string

const (
	// LinkSymlink means the entry is a relative symlink to the SSOT tree
	// (the preferred form — Claude Code documents symlinked skill
	// directories explicitly).
	LinkSymlink LinkMode = "symlink"
	// LinkStub means the entry is a thin stub SKILL.md pointing back at the
	// SSOT tree — the fallback used where symlinks are unavailable (e.g.
	// Windows without Developer Mode). The runtime fallback IS the
	// cross-platform mechanism; there is no build-tag branch for it.
	LinkStub LinkMode = "stub"
)

// LinkResult describes one installed discovery entry.
type LinkResult struct {
	// Surface is the row the entry was installed for.
	Surface Surface
	// Path is the repo-relative a2ahub entry, e.g. ".claude/skills/a2ahub".
	Path string
	// Mode is how the entry was installed.
	Mode LinkMode
}

// linkMarkerTag is the machine-recognizable marker line a stub SKILL.md
// carries (checked for presence anywhere in the file, not as a leading
// prefix — see fileHasMarkerTag). Distinct from cmd_skill.go's
// skillProvenanceTag: that tag marks an *installed SSOT tree*; this one
// marks a *link* (a pointer back to the SSOT tree), so a re-link recognizes
// its own stub without confusing the two kinds of a2ahub-owned target.
const linkMarkerTag = "<!-- a2ahub-skill-link -->"

// symlink is a package-level indirection over os.Symlink so a test can force
// the fallback path deterministically (simulating a platform/permission
// error, e.g. Windows without Developer Mode) without needing an actual
// symlink-hostile filesystem.
var symlink = os.Symlink

// Link installs a discovery entry for surface s under root:
// <root>/<s.SkillsHome>/a2ahub, pointing at the installed SSOT tree at
// <root>/<ssotRel> (normally ".a2ahub/skill"). It first tries a relative
// symlink; if that fails (platform/permission error) it falls back to a
// stub SKILL.md carrying valid Agent-Skill frontmatter and a pointer to the
// SSOT tree's own SKILL.md.
//
// Ownership probe (mirrors cmd_skill.go's skillTargetState/errSkillForeignTarget
// gate): an existing target is "ours" when it is a symlink whose raw link
// text ends in ssotRel, or a directory/file carrying linkMarkerTag. A
// foreign target (present, not ours) is refused with ErrForeignLinkTarget
// and nothing is written, unless force is set. An absent target is always
// free to create. An authorized target (ours, or force over foreign) is
// removed first so the result mirrors — no orphaned stale entry.
func Link(root string, s Surface, ssotRel string, force bool) (LinkResult, error) {
	target := filepath.Join(root, s.SkillsHome, "a2ahub")
	relPath := filepath.Join(s.SkillsHome, "a2ahub")

	exists, owned, err := linkTargetState(target, ssotRel)
	if err != nil {
		return LinkResult{}, &Error{Op: "Link", Input: target, Err: err}
	}
	if exists && !owned && !force {
		return LinkResult{}, &Error{Op: "Link", Input: target, Err: ErrForeignLinkTarget}
	}
	if exists {
		if rmErr := os.RemoveAll(target); rmErr != nil {
			return LinkResult{}, &Error{Op: "Link", Input: target, Err: rmErr}
		}
	}

	skillsHomeAbs := filepath.Join(root, s.SkillsHome)
	if mkErr := os.MkdirAll(skillsHomeAbs, 0o755); mkErr != nil {
		return LinkResult{}, &Error{Op: "Link", Input: skillsHomeAbs, Err: mkErr}
	}

	ssotAbs := filepath.Join(root, ssotRel)
	if linkDest, relErr := filepath.Rel(filepath.Dir(target), ssotAbs); relErr == nil {
		if symErr := symlink(linkDest, target); symErr == nil {
			return LinkResult{Surface: s, Path: relPath, Mode: LinkSymlink}, nil
		}
	}

	// Symlink unavailable (or its relative path could not be computed) —
	// stub fallback. Only this branch creates target as a directory; the
	// symlink branch above must never MkdirAll(target) first (that would
	// make os.Symlink fail with "file exists").
	if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
		return LinkResult{}, &Error{Op: "Link", Input: target, Err: mkErr}
	}
	stub := linkStubContent(s, ssotRel)
	if wErr := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(stub), 0o644); wErr != nil {
		return LinkResult{}, &Error{Op: "Link", Input: target, Err: wErr}
	}
	return LinkResult{Surface: s, Path: relPath, Mode: LinkStub}, nil
}

// linkStubContent renders the fallback stub SKILL.md: valid Agent-Skill
// frontmatter (name + description, so a surface's own skill-loading UI shows
// something sane) followed by the marker tag and a pointer to the real tree.
// The path in the description/prose is repo-root-relative (e.g.
// ".a2ahub/skill/SKILL.md") rather than a relative markdown link, since the
// correct "../.." depth from <SkillsHome>/a2ahub/SKILL.md back to repo root
// is surface-specific and this stub must stay simple prose, not a second
// source of navigation truth.
func linkStubContent(s Surface, ssotRel string) string {
	ssotSkillMD := filepath.ToSlash(filepath.Join(ssotRel, "SKILL.md"))
	return "---\n" +
		"name: a2ahub\n" +
		"description: The a2ahub expert skill — pointer only; the real content lives " +
		"at " + ssotSkillMD + ".\n" +
		"---\n\n" +
		linkMarkerTag + "\n" +
		"# a2ahub (link)\n\n" +
		"This is a pointer, not the skill content — a symlink from " + s.SkillsHome +
		"/a2ahub could not be created on this filesystem. Read `" + ssotSkillMD + "` " +
		"(repo root) for the operating manual; refresh with `a2a skill install` (or " +
		"`a2a skill link` to retry the symlink).\n"
}

// linkTargetState inspects an existing (or absent) link target. exists is
// true when target is present at all, of any type. owned is true when
// target is a2ahub-owned: a symlink whose raw link text ends in ssotRel, or
// a directory/file carrying linkMarkerTag. An absent target is (false,
// false, nil) — free to create without force. err surfaces only a genuine
// unexpected filesystem failure (never "target absent" or "marker
// unreadable" — those degrade to unowned, matching skillTargetState's
// idiom in cmd_skill.go).
func linkTargetState(target, ssotRel string) (exists, owned bool, err error) {
	info, statErr := os.Lstat(target)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, false, nil
		}
		return false, false, statErr
	}

	if info.Mode()&os.ModeSymlink != 0 {
		dest, rlErr := os.Readlink(target)
		if rlErr != nil {
			return true, false, nil //nolint:nilerr // unreadable symlink target => treat as foreign, not a hard error
		}
		return true, strings.HasSuffix(dest, ssotRel), nil
	}

	if info.IsDir() {
		return true, dirHasMarkerTag(target), nil
	}
	return true, fileHasMarkerTag(target), nil
}

// dirHasMarkerTag reports whether target/SKILL.md carries linkMarkerTag
// (a prior stub of ours). An absent/unreadable SKILL.md means "not ours".
func dirHasMarkerTag(target string) bool {
	return fileHasMarkerTag(filepath.Join(target, "SKILL.md"))
}

// fileHasMarkerTag reports whether the file at path contains linkMarkerTag
// on its own line. A stub SKILL.md carries valid Agent-Skill frontmatter
// first (the format requires "---" to lead the file), so the marker tag
// cannot be the file's literal first line the way skillProvenanceTag is in
// cmd_skill.go's PROVENANCE.md — it is checked for presence, not prefix. An
// absent/unreadable file means "not ours".
func fileHasMarkerTag(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), linkMarkerTag)
}
