package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/skill"
)

// --- doctorCheckSkillManualCurrent, the TREE walk (judge-the-thing-2026-08 P5) ---
//
// Spec 05 §8's fourteen criteria. #10 and #11 (through the built binary) live
// in internal/e2e/testdata/t3/doctor.txtar; every other criterion is here.

// mustInstallSkillTree installs the REAL embedded skill.Files tree into
// target, stamped with version — the round-trip baseline every test in this
// file starts from and then mutates on disk (the fixture must outlive P4's
// fix, spec 05 §6: "seeded directly on disk, never by running an old
// binary").
func mustInstallSkillTree(t *testing.T, target, version string) {
	t.Helper()
	if _, err := installSkillTree(skill.Files, target, version, false); err != nil {
		t.Fatalf("installSkillTree: %v", err)
	}
}

func newSkillTreeDoctorCommand(t *testing.T, root, binaryVersion string) *DoctorCommand {
	t.Helper()
	cmd := NewDoctorCommand(host.NewFakeHost(), binaryVersion, "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	cmd.SkillFiles = skill.Files
	return cmd
}

// TestDoctorCheckSkillManualCurrent_RoundTrip_CleanZeroDifferences is §8 #1:
// a tree written by installSkillTree from THIS binary's own embed, stamped
// with THIS binary's version, is a clean PASS with zero differences. This
// single assertion kills the prefix-strip, exclusion and extras classes at
// once, and proves the check and the installer agree without sharing code
// (spec 05 §5: NOT extracted into one function).
func TestDoctorCheckSkillManualCurrent_RoundTrip_CleanZeroDifferences(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	mustInstallSkillTree(t, target, "0.23.0")

	cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	if !strings.Contains(detail, "skill manual current (v0.23.0, ") ||
		!strings.Contains(detail, "files verified against this binary's own tree") {
		t.Fatalf("detail = %q, want the tree-verified current-manual note", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_LiveDefectReplayed is §8 #2: the exact
// state the report's own evidence.actual showed — a missing reference file
// plus two files behind — replayed by seeding the corruption directly on
// disk (never by running an old binary: once P4 lands, the bug that used to
// PRODUCE this state cannot reproduce it, and this fixture must outlive that
// bug).
func TestDoctorCheckSkillManualCurrent_LiveDefectReplayed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	mustInstallSkillTree(t, target, "0.23.0")

	if err := os.Remove(filepath.Join(target, "reference", "notify.md")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"troubleshooting.md", "notifications.md"} {
		if err := os.WriteFile(filepath.Join(target, rel), []byte("stale content, behind the embed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if ok {
		t.Fatalf("want fail, got pass: %s", detail)
	}
	if !strings.Contains(detail, "1 missing, 2 modified, 0 unexpected") {
		t.Errorf("detail = %q, want the stable class-count shape", detail)
	}
	for _, path := range []string{"reference/notify.md", "troubleshooting.md", "notifications.md"} {
		if !strings.Contains(detail, path) {
			t.Errorf("detail = %q, missing path %q", detail, path)
		}
	}
	if !strings.Contains(detail, "run 'a2a skill install'") {
		t.Errorf("detail = %q, want the remedy command", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_ModifiedAlone is §8 #3: a same-name,
// different-bytes file is caught independently of any missing file.
func TestDoctorCheckSkillManualCurrent_ModifiedAlone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	mustInstallSkillTree(t, target, "0.23.0")

	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	flipped := append([]byte(nil), data...)
	flipped[0] ^= 0xFF
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), flipped, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if ok {
		t.Fatalf("want fail, got pass: %s", detail)
	}
	if !strings.Contains(detail, "0 missing, 1 modified, 0 unexpected") {
		t.Errorf("detail = %q, want exactly 1 modified and nothing else", detail)
	}
	if !strings.Contains(detail, "SKILL.md") {
		t.Errorf("detail = %q, missing the modified path", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_UnexpectedFile is §8 #4: a disk file
// absent from the embed is `unexpected`, and the root PROVENANCE.md is the
// ONLY exemption — `reference/PROVENANCE.md` is NOT exempt (the anti-
// widening tooth: without it, a base-name match would pass the first half
// and quietly ignore any file so named, spec 05 "The exclusion rule").
func TestDoctorCheckSkillManualCurrent_UnexpectedFile(t *testing.T) {
	t.Parallel()

	t.Run("root PROVENANCE.md is exempt", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, skillDefaultDir)
		mustInstallSkillTree(t, target, "0.23.0")
		// installSkillTree already wrote the root PROVENANCE.md — a round
		// trip with nothing else changed proves it is not counted, since a
		// clean PASS (asserted elsewhere) would otherwise be impossible.
		cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
		ok, detail := cmd.doctorCheckSkillManualCurrent()
		if !ok {
			t.Fatalf("want pass (root PROVENANCE.md must not count as unexpected), got fail: %s", detail)
		}
	})

	t.Run("reference/PROVENANCE.md is NOT exempt", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, skillDefaultDir)
		mustInstallSkillTree(t, target, "0.23.0")
		if err := os.WriteFile(filepath.Join(target, "reference", "PROVENANCE.md"), []byte("not exempt\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
		ok, detail := cmd.doctorCheckSkillManualCurrent()
		if ok {
			t.Fatalf("want fail (reference/PROVENANCE.md is not the exempted path), got pass: %s", detail)
		}
		if !strings.Contains(detail, "0 missing, 0 modified, 1 unexpected") {
			t.Errorf("detail = %q, want exactly 1 unexpected", detail)
		}
		if !strings.Contains(detail, "reference/PROVENANCE.md") {
			t.Errorf("detail = %q, missing the unexpected path", detail)
		}
	})
}

// TestDoctorCheckSkillManualCurrent_HonestLag_NoteUnchangedAndWalkNeverRuns
// is §8 #5: an honestly stale install stays PASS with today's exact note,
// no matter how far its tree has drifted — the walk is not even decisive
// here, so it must not run at all. A sentinel fs.FS proves that: if the
// walk ran, the test would fail from inside Open, not from a wrong string.
func TestDoctorCheckSkillManualCurrent_HonestLag_NoteUnchangedAndWalkNeverRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.22.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.23.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	cmd.SkillFiles = doctorSentinelFS{t: t}

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass (never a FAIL on honest lag), got fail: %s", detail)
	}
	want := " · skill manual is v0.22.0, binary is v0.23.0 — run 'a2a skill install'"
	if detail != want {
		t.Fatalf("detail = %q, want the UNCHANGED pre-phase note %q", detail, want)
	}
}

// TestDoctorCheckSkillManualCurrent_StampNewerThanBinary is §8 #6: a stamp
// NEWER than the binary reads as its own state, not as "skill manual
// current" — the behaviour-changing branch spec 05 §0 calls out by name (row
// 6): today's code collapses "equal" and "newer" and reads a future
// release's manual as current.
func TestDoctorCheckSkillManualCurrent_StampNewerThanBinary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.24.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.23.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	cmd.SkillFiles = doctorSentinelFS{t: t}

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass, got fail: %s", detail)
	}
	if strings.Contains(detail, "skill manual current") {
		t.Errorf("detail = %q, must NOT read as \"skill manual current\" — a future release's manual is not current", detail)
	}
	if !strings.Contains(detail, "v0.24.0") || !strings.Contains(detail, "v0.23.0") {
		t.Errorf("detail = %q, want both versions named", detail)
	}
	if !strings.Contains(detail, "newer") {
		t.Errorf("detail = %q, want it to say the manual is newer", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_Dev is §8 #7: a `dev` build never FAILs
// this row at any tree state; when the tree differs it says so and names
// files.
func TestDoctorCheckSkillManualCurrent_Dev(t *testing.T) {
	t.Parallel()

	t.Run("matching tree keeps today's wording", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, skillDefaultDir)
		mustInstallSkillTree(t, target, "dev")

		cmd := newSkillTreeDoctorCommand(t, root, "dev")
		ok, detail := cmd.doctorCheckSkillManualCurrent()
		if !ok {
			t.Fatalf("dev never FAILs; got fail: %s", detail)
		}
		if !strings.Contains(detail, "development build") {
			t.Errorf("detail = %q, want the unchanged dev wording", detail)
		}
		if strings.Contains(detail, "differs") {
			t.Errorf("detail = %q, must not claim drift when the tree matches", detail)
		}
	})

	t.Run("differing tree names files, still PASS", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, skillDefaultDir)
		mustInstallSkillTree(t, target, "dev")
		if err := os.Remove(filepath.Join(target, "reference", "notify.md")); err != nil {
			t.Fatal(err)
		}

		cmd := newSkillTreeDoctorCommand(t, root, "dev")
		ok, detail := cmd.doctorCheckSkillManualCurrent()
		if !ok {
			t.Fatalf("dev never FAILs, even with a differing tree; got fail: %s", detail)
		}
		if !strings.Contains(detail, "development build") || !strings.Contains(detail, "differs") {
			t.Errorf("detail = %q, want it to name the drift", detail)
		}
		if !strings.Contains(detail, "reference/notify.md") {
			t.Errorf("detail = %q, want the differing file named", detail)
		}
		if !strings.Contains(detail, "a2a skill install") {
			t.Errorf("detail = %q, want the dev-tree-differs remedy", detail)
		}
	})
}

// TestDoctorCheckSkillManualCurrent_HandEditedOwnedTreeFails is §8 #8's first
// half: an OWNED tree with a hand-edited file FAILs, and the remedy line
// states that `a2a skill install` overwrites local edits. The second half —
// a non-owned directory is never judged and never walked — is
// TestDoctorCheckSkillManualCurrent_ForeignInstall_NotJudged, which already
// uses the same doctorSentinelFS tooth.
func TestDoctorCheckSkillManualCurrent_HandEditedOwnedTreeFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	mustInstallSkillTree(t, target, "0.23.0")
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("hand-edited, ignoring the do-not-edit notice\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if ok {
		t.Fatalf("a hand-edited OWNED tree must FAIL, got pass: %s", detail)
	}
	if !strings.Contains(detail, "SKILL.md") {
		t.Errorf("detail = %q, want the edited file named", detail)
	}
	if !strings.Contains(strings.ToLower(detail), "overwrites local edits") {
		t.Errorf("detail = %q, want the remedy to say the reinstall overwrites local edits", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_OutputStability is §8 #9: with 12
// differing files the line names 5 sorted paths plus "(+7 more)", and two
// consecutive runs are byte-identical (map iteration order would otherwise
// flap).
func TestDoctorCheckSkillManualCurrent_OutputStability(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	mustInstallSkillTree(t, target, "0.23.0")

	var rels []string
	err := filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(target, p)
		if relErr != nil || rel == skillProvenanceFile {
			return relErr
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rels)
	if len(rels) < 12 {
		t.Fatalf("embedded tree only carries %d files, need at least 12 for this fixture", len(rels))
	}
	for _, rel := range rels[:12] {
		if err := os.WriteFile(filepath.Join(target, rel), []byte("modified for output-stability test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
	ok1, detail1 := cmd.doctorCheckSkillManualCurrent()
	ok2, detail2 := cmd.doctorCheckSkillManualCurrent()
	if ok1 || ok2 {
		t.Fatalf("want fail on both runs, got ok1=%v ok2=%v", ok1, ok2)
	}
	if detail1 != detail2 {
		t.Fatalf("two consecutive runs differ:\n1: %q\n2: %q", detail1, detail2)
	}
	if !strings.Contains(detail1, "12 modified") {
		t.Errorf("detail = %q, want 12 modified", detail1)
	}
	if !strings.Contains(detail1, "(+7 more)") {
		t.Errorf("detail = %q, want exactly 5 named and \"(+7 more)\"", detail1)
	}
	named := strings.Count(detail1, "modified: ")
	if named != 5 {
		t.Errorf("detail = %q names %d paths, want exactly 5", detail1, named)
	}
}

// TestDoctorCheckSkillManualCurrent_Undecidable is §6's "undecidable" row: a
// disk file that cannot be read is an advisory PASS naming the path — never
// a FAIL, never a raw error string. chmod 000 is skipped when running as
// root (root ignores the permission bit, which would silently weaken this
// assertion rather than test it).
func TestDoctorCheckSkillManualCurrent_Undecidable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 is not meaningful on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions; chmod 000 would not exercise the undecidable path")
	}
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	mustInstallSkillTree(t, target, "0.23.0")

	locked := filepath.Join(target, "SKILL.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	cmd := newSkillTreeDoctorCommand(t, root, "0.23.0")
	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("an unreadable file is undecidable, never a FAIL; got fail: %s", detail)
	}
	if !strings.Contains(detail, "SKILL.md") {
		t.Errorf("detail = %q, want the unreadable path named", detail)
	}
	if strings.Contains(detail, "permission denied") {
		t.Errorf("detail = %q, must report a CLASS, never the raw OS error string", detail)
	}
}

// TestDoctorCheckSkillManualCurrent_Unwired is §6's "unwired" row: with
// SkillFiles == nil, an otherwise-decisive equality degrades to an advisory
// PASS naming the exact reason — never a silent upgrade to the tree-verified
// note (§8 #10's own point; the built-binary proof that a shipped binary
// never actually takes this branch lives in doctor.txtar).
func TestDoctorCheckSkillManualCurrent_Unwired(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, skillDefaultDir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.23.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewDoctorCommand(host.NewFakeHost(), "0.23.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", root)
	cmd.cachePath = func() (string, error) { return "/unused/does-not-exist/update-check.json", nil }
	// cmd.SkillFiles left nil deliberately.

	ok, detail := cmd.doctorCheckSkillManualCurrent()
	if !ok {
		t.Fatalf("want pass (unwired is undecidable, never a FAIL), got fail: %s", detail)
	}
	if !strings.Contains(detail, "skill tree unverified") || !strings.Contains(detail, "this build wires no embedded skill tree") {
		t.Fatalf("detail = %q, want the exact unwired advisory", detail)
	}
	if strings.Contains(detail, "files verified") {
		t.Errorf("detail = %q, must NOT carry the tree-verified note when the tree was never read", detail)
	}
}

// TestSkillProvenanceOutputUnchanged is §8 #12's first half (epic AC3): this
// phase introduces NO second claim. skillProvenance must emit its pre-phase
// string byte-for-byte — a digest or manifest entry added here would be
// exactly the "simplification" spec 05 §5 refuses on the record.
func TestSkillProvenanceOutputUnchanged(t *testing.T) {
	t.Parallel()
	got := skillProvenance("0.23.0")
	want := "<!-- a2ahub-skill-install -->\n" +
		"# a2ahub skill — installed artifact\n\n" +
		"Written by `a2a skill install` / `a2a init` (a2a 0.23.0).\n" +
		"Source: github.com/ydnikolaev/a2ahub — skill/a2ahub/.\n\n" +
		"**Do not hand-edit** — refresh with `a2a skill install`.\n\n" +
		"a2ahub is the protocol for typed cross-system artifact exchange. Start at " +
		"[SKILL.md](SKILL.md); the `a2a` binary is the source of truth for command " +
		"syntax and validation (this tree defers to it, never restates it).\n"
	if got != want {
		t.Fatalf("skillProvenance(\"0.23.0\") changed:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestDocsManifestCarriesNoDigestKey is §8 #12's second half (epic AC3): the
// docs manifest the install already writes carries no digest/hash/checksum/
// sha/size key at any depth — the reporter's other refused proposal (spec 05
// §5: it lives INSIDE the tree under comparison, so a stale copy of it would
// be evidence about itself).
func TestDocsManifestCarriesNoDigestKey(t *testing.T) {
	t.Parallel()
	data, err := skill.Files.ReadFile("a2ahub/docs-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	forbidden := regexp.MustCompile(`(?i)digest|hash|checksum|sha|size`)
	var offenders []string
	var scan func(v any, path string)
	scan = func(v any, path string) {
		switch t := v.(type) {
		case map[string]any:
			for k, vv := range t {
				childPath := path + "." + k
				if forbidden.MatchString(k) {
					offenders = append(offenders, childPath)
				}
				scan(vv, childPath)
			}
		case []any:
			for _, vv := range t {
				scan(vv, path+"[]")
			}
		}
	}
	scan(doc, "$")
	if len(offenders) > 0 {
		t.Fatalf("docs-manifest.json carries a digest-shaped key at %v — spec 05 §5 refuses this on the record: "+
			"a manifest INSIDE the compared tree is evidence about itself", offenders)
	}
}

// TestDoctorSkillManualVersionRegexpRoundTripsSkillProvenance is §8 #14: the
// version string `a2a skill install` stamps and the one this check compares
// against are the SAME shape, so rows 3/4 are reachable in a shipped binary
// (spec 05 "The equality rows 3 and 4 stand on" — the standing dependency
// between cmd/a2a/wire.go's two call sites of the package-level `version`).
func TestDoctorSkillManualVersionRegexpRoundTripsSkillProvenance(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"0.23.0", "1.0.0", "0.0.1"} {
		provenance := skillProvenance(v)
		match := doctorSkillManualVersionPattern.FindStringSubmatch(provenance)
		if match == nil {
			t.Fatalf("skillProvenance(%q): stamp regexp found no match in %q", v, provenance)
		}
		if match[1] != v {
			t.Errorf("skillProvenance(%q): stamp regexp captured %q, want exactly %q", v, match[1], v)
		}
	}
}
