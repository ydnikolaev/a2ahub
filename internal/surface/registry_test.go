package surface

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegistryRowsAreProvenanced is the AC-918.1 gate: every Registry() row
// must carry both SourceURL and VerifiedOn, or the gate fails loudly. A
// future row that omits either provenance field must break this test, not
// slip through silently.
func TestRegistryRowsAreProvenanced(t *testing.T) {
	t.Parallel()
	for _, s := range Registry() {
		if s.SourceURL == "" {
			t.Errorf("surface %q: SourceURL is empty (every row must carry the doc it was verified against)", s.ID)
		}
		if s.VerifiedOn == "" {
			t.Errorf("surface %q: VerifiedOn is empty (every row must carry the date it was verified)", s.ID)
		}
	}
}

func TestRegistryDeterministicOrder(t *testing.T) {
	t.Parallel()
	rows := Registry()
	if len(rows) != 2 {
		t.Fatalf("Registry() = %d rows, want 2", len(rows))
	}
	if rows[0].ID != "claude" || rows[1].ID != "codex" {
		t.Fatalf("Registry() order = [%s, %s], want [claude, codex]", rows[0].ID, rows[1].ID)
	}
}

func TestRegistryClaudeRow(t *testing.T) {
	t.Parallel()
	s, ok := ByID("claude")
	if !ok {
		t.Fatal("ByID(\"claude\") not found")
	}
	if s.SkillsHome != ".claude/skills" {
		t.Errorf("SkillsHome = %q, want %q", s.SkillsHome, ".claude/skills")
	}
	if s.ContextFile != "CLAUDE.md" {
		t.Errorf("ContextFile = %q, want %q", s.ContextFile, "CLAUDE.md")
	}
	if s.ReadsAgentsMD {
		t.Error("ReadsAgentsMD = true, want false — Claude Code reads CLAUDE.md, not AGENTS.md")
	}
}

func TestRegistryCodexRow(t *testing.T) {
	t.Parallel()
	s, ok := ByID("codex")
	if !ok {
		t.Fatal("ByID(\"codex\") not found")
	}
	if s.SkillsHome != ".codex/skills" {
		t.Errorf("SkillsHome = %q, want %q", s.SkillsHome, ".codex/skills")
	}
	if s.ContextFile != "AGENTS.md" {
		t.Errorf("ContextFile = %q, want %q", s.ContextFile, "AGENTS.md")
	}
	if !s.ReadsAgentsMD {
		t.Error("ReadsAgentsMD = false, want true — Codex reads AGENTS.md")
	}
}

func TestByIDUnknown(t *testing.T) {
	t.Parallel()
	_, ok := ByID("does-not-exist")
	if ok {
		t.Error("ByID(unknown) ok = true, want false")
	}
}

func TestMarkerDir(t *testing.T) {
	t.Parallel()
	claude, _ := ByID("claude")
	if got := claude.MarkerDir(); got != ".claude" {
		t.Errorf("claude.MarkerDir() = %q, want %q", got, ".claude")
	}
	codex, _ := ByID("codex")
	if got := codex.MarkerDir(); got != ".codex" {
		t.Errorf("codex.MarkerDir() = %q, want %q", got, ".codex")
	}
}

// --- detect.go ---

func TestDetectNeitherPresent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got := Detect(root)
	if len(got) != 0 {
		t.Errorf("Detect(empty root) = %v, want empty", got)
	}
}

func TestDetectClaudeOnly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".claude"))
	got := Detect(root)
	if len(got) != 1 || got[0].ID != "claude" {
		t.Fatalf("Detect(.claude only) = %v, want [claude]", got)
	}
}

func TestDetectBothPresentOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".codex"))
	mustMkdirAll(t, filepath.Join(root, ".claude"))
	got := Detect(root)
	if len(got) != 2 || got[0].ID != "claude" || got[1].ID != "codex" {
		t.Fatalf("Detect(both) = %v, want [claude, codex] (deterministic order)", got)
	}
}

func TestDetectMissingRoot(t *testing.T) {
	t.Parallel()
	got := Detect(filepath.Join(t.TempDir(), "does-not-exist"))
	if len(got) != 0 {
		t.Errorf("Detect(missing root) = %v, want empty (best-effort, no error)", got)
	}
}

func TestDetectMarkerIsAFileNotADir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// A ".claude" that is a plain file (not a directory) must not count as
	// detected — Detect requires the marker to be a directory.
	if err := os.WriteFile(filepath.Join(root, ".claude"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Detect(root)
	if len(got) != 0 {
		t.Errorf("Detect(.claude as file) = %v, want empty", got)
	}
}

// --- errors.go ---

func TestErrorStringWithInput(t *testing.T) {
	t.Parallel()
	err := &Error{Op: "Link", Input: "/some/target", Err: ErrForeignLinkTarget}
	want := "surface: Link: /some/target: " + ErrForeignLinkTarget.Error()
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, ErrForeignLinkTarget) {
		t.Error("errors.Is(err, ErrForeignLinkTarget) = false, want true")
	}
}

func TestErrorStringWithoutInput(t *testing.T) {
	t.Parallel()
	err := &Error{Op: "Link", Err: ErrForeignLinkTarget}
	want := "surface: Link: " + ErrForeignLinkTarget.Error()
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// --- link.go ---

const ssotRel = ".a2ahub/skill"

// setupSSOT creates a fake installed SSOT tree at <root>/.a2ahub/skill with
// a SKILL.md, so tests can assert a symlink resolves into it.
func setupSSOT(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ssotRel)
	mustMkdirAll(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: a2ahub\ndescription: test\n---\n# a2ahub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLinkFreshCreatesSymlinkResolvingToSSOT(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := ByID("claude")

	result, err := Link(root, claude, ssotRel, false)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Mode != LinkSymlink {
		t.Fatalf("result.Mode = %q, want %q", result.Mode, LinkSymlink)
	}
	wantPath := filepath.Join(".claude/skills", "a2ahub")
	if result.Path != wantPath {
		t.Errorf("result.Path = %q, want %q (repo-relative)", result.Path, wantPath)
	}
	if result.Surface.ID != "claude" {
		t.Errorf("result.Surface.ID = %q, want %q", result.Surface.ID, "claude")
	}

	target := filepath.Join(root, claude.SkillsHome, "a2ahub")

	// The link itself must be RELATIVE (brief step e) — an absolute symlink
	// breaks the moment the repo is checked out at a different path.
	// EvalSymlinks alone would pass identically for an absolute link, so
	// pin the raw link text via Readlink.
	dest, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("Readlink(%s) error = %v", target, err)
	}
	if filepath.IsAbs(dest) {
		t.Errorf("symlink dest %q is absolute, want relative (portability)", dest)
	}
	if !strings.HasSuffix(dest, ssotRel) {
		t.Errorf("symlink dest %q does not end in %q", dest, ssotRel)
	}

	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", target, err)
	}
	wantResolved, err := filepath.EvalSymlinks(filepath.Join(root, ssotRel))
	if err != nil {
		t.Fatalf("EvalSymlinks(ssot) error = %v", err)
	}
	if resolved != wantResolved {
		t.Errorf("resolved symlink = %s, want %s", resolved, wantResolved)
	}
	// Entry point resolves to the real SKILL.md content.
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatalf("read linked SKILL.md: %v", err)
	}
	if !strings.Contains(string(data), "name: a2ahub") {
		t.Errorf("linked SKILL.md content = %q, want it to contain the SSOT frontmatter", data)
	}
}

func TestLinkRefreshIsIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := ByID("claude")

	if _, err := Link(root, claude, ssotRel, false); err != nil {
		t.Fatalf("first Link() error = %v", err)
	}
	result, err := Link(root, claude, ssotRel, false)
	if err != nil {
		t.Fatalf("second Link() (refresh) error = %v", err)
	}
	if result.Mode != LinkSymlink {
		t.Fatalf("refresh result.Mode = %q, want %q", result.Mode, LinkSymlink)
	}

	target := filepath.Join(root, claude.SkillsHome, "a2ahub")
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", target, err)
	}
	wantResolved, err := filepath.EvalSymlinks(filepath.Join(root, ssotRel))
	if err != nil {
		t.Fatalf("EvalSymlinks(ssot) error = %v", err)
	}
	if resolved != wantResolved {
		t.Errorf("resolved symlink after refresh = %s, want %s", resolved, wantResolved)
	}

	// The SSOT tree itself must survive the refresh — RemoveAll(target) on a
	// symlink removes the link, never the tree it points to.
	if _, err := os.Stat(filepath.Join(root, ssotRel, "SKILL.md")); err != nil {
		t.Errorf("SSOT SKILL.md missing after refresh: %v", err)
	}
}

func TestLinkForeignTargetRefusedWithoutForce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := ByID("claude")

	target := filepath.Join(root, claude.SkillsHome, "a2ahub")
	mustMkdirAll(t, target)
	foreignFile := filepath.Join(target, "SKILL.md")
	foreignContent := "---\nname: someone-else\ndescription: not ours\n---\n"
	if err := os.WriteFile(foreignFile, []byte(foreignContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Link(root, claude, ssotRel, false)
	if !errors.Is(err, ErrForeignLinkTarget) {
		t.Fatalf("Link() error = %v, want errors.Is(err, ErrForeignLinkTarget)", err)
	}

	// Nothing written: target unchanged.
	data, readErr := os.ReadFile(foreignFile)
	if readErr != nil {
		t.Fatalf("foreign target file missing after refusal: %v", readErr)
	}
	if string(data) != foreignContent {
		t.Errorf("foreign target content changed: got %q, want %q", data, foreignContent)
	}

	// force=true replaces it.
	result, err := Link(root, claude, ssotRel, true)
	if err != nil {
		t.Fatalf("Link(force=true) error = %v", err)
	}
	if result.Mode != LinkSymlink {
		t.Fatalf("Link(force=true) result.Mode = %q, want %q", result.Mode, LinkSymlink)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(target) after force: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("target after force is not a symlink (mode=%v)", info.Mode())
	}
}

// TestLinkSymlinkFallbackToStub is intentionally NOT t.Parallel(): it
// mutates the package-level `symlink` var. Keeping it sequential means its
// mutate/restore window runs entirely inside testing's sequential phase,
// before any t.Parallel() test resumes — so it never races a concurrent
// reader of the same var under `-race`.
func TestLinkSymlinkFallbackToStub(t *testing.T) {
	root := t.TempDir()
	setupSSOT(t, root)
	claude, _ := ByID("claude")

	orig := symlink
	symlink = func(string, string) error { return errors.New("simulated: symlinks unavailable") }
	t.Cleanup(func() { symlink = orig })

	result, err := Link(root, claude, ssotRel, false)
	if err != nil {
		t.Fatalf("Link() error = %v", err)
	}
	if result.Mode != LinkStub {
		t.Fatalf("result.Mode = %q, want %q", result.Mode, LinkStub)
	}

	target := filepath.Join(root, claude.SkillsHome, "a2ahub")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(target) = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("stub target is not a directory (mode=%v)", info.Mode())
	}

	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatalf("read stub SKILL.md: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		t.Errorf("stub SKILL.md does not start with frontmatter: %q", content)
	}
	if !strings.Contains(content, "name: a2ahub") {
		t.Errorf("stub SKILL.md missing name: a2ahub frontmatter field: %q", content)
	}
	if !strings.Contains(content, "description:") {
		t.Errorf("stub SKILL.md missing description frontmatter field: %q", content)
	}
	if !strings.Contains(content, linkMarkerTag) {
		t.Errorf("stub SKILL.md missing marker tag %q: %q", linkMarkerTag, content)
	}
	if !strings.Contains(content, ssotRel) {
		t.Errorf("stub SKILL.md does not name the real tree (%q): %q", ssotRel, content)
	}

	// A subsequent Link (still with the broken symlink var) recognizes its
	// own stub as owned and refreshes it without needing --force.
	result2, err := Link(root, claude, ssotRel, false)
	if err != nil {
		t.Fatalf("second Link() (stub refresh) error = %v", err)
	}
	if result2.Mode != LinkStub {
		t.Fatalf("second Link() result.Mode = %q, want %q", result2.Mode, LinkStub)
	}
}
