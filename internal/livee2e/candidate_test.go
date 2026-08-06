package livee2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

type candidateSourceFixture struct {
	want     CandidateExpectation
	commands map[string]string
	files    map[string][]byte
}

func newCandidateSourceFixture(t *testing.T) candidateSourceFixture {
	t.Helper()
	root := t.TempDir()
	verificationRoot := t.TempDir()
	sha := strings.Repeat("a", 40)
	tree := strings.Repeat("b", 40)
	checkLog := filepath.Join(t.TempDir(), "candidate-check.log")
	want := CandidateExpectation{
		Root: root, SHA: sha, Tree: tree, Tag: "v0.19.0",
		Floor: requiredCandidateFloor, CheckLog: checkLog,
	}
	workflow := []byte("jobs:\n  validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.19.0\n  audit:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.19.0\n")
	return candidateSourceFixture{
		want: want,
		commands: map[string]string{
			"git rev-parse --show-toplevel":                   root,
			"git rev-parse HEAD":                              sha,
			"git rev-parse HEAD^{tree}":                       tree,
			"git rev-parse --abbrev-ref HEAD":                 "HEAD",
			"git diff --cached --name-only --no-ext-diff":     "",
			"git diff --name-only --no-ext-diff":              "",
			"git status --porcelain=v1 --untracked-files=all": "",
		},
		files: map[string][]byte{
			filepath.Join(root, "go.mod"):                                                     []byte("module github.com/ydnikolaev/a2ahub\n\ngo 1.26\n"),
			filepath.Join(root, "space-template", "space.yaml"):                               []byte("schema: space/v1\nmin_binary_version: 0.19.0\n"),
			filepath.Join(root, "space-template", ".github", "workflows", "a2a-validate.yml"): workflow,
			checkLog: candidateCheckLog(verificationRoot, sha, tree, want.Tag, "0"),
		},
	}
}

// candidateCheckLog builds a fixture retained-check-log transcript. The
// LOGIC_TIER_ROWS_SHA256 marker is always the CORRECT digest for the real,
// current Catalogue() — computed the same way verifyCandidateCheckLog
// (evidence.go) recomputes it at validation time — so every existing caller
// of this five-parameter helper (including operational_runtime_test.go,
// which this wave does not touch) keeps producing an honest, currently-valid
// transcript with no signature change here. A test that wants to prove the
// MISMATCH path mutates the returned bytes with bytes.Replace, matching the
// existing precedent for every other marker (see e.g. "wrong check sha").
func candidateCheckLog(root, sha, tree, tag, exit string) []byte {
	return []byte("CHECKOUT_ROOT=" + root + "\n" +
		"CANDIDATE_SHA=" + sha + "\n" +
		"CANDIDATE_TREE=" + tree + "\n" +
		"CANDIDATE_TAG=" + tag + "\n" +
		"CHECKOUT_DETACHED=true\n" +
		"INDEX_CLEAN=true\n" +
		"WORKTREE_CLEAN=true\n" +
		"UNTRACKED_CLEAN=true\n" +
		"WEB_DEPS_READY=true\n" +
		"LOGIC_TIER_ROWS_SHA256=" + logicTierRowsDigest(logicTierRowKeys(Catalogue())) + "\n" +
		"EXIT=" + exit + "\n")
}

// privateReleaseControlRail names the file whose presence means "this checkout
// is the private source", for the pairing above.
//
// It must be a TRACKED private-only path. It used to be AGENTS.md, and AGENTS.md
// is in .gitignore — it exists in a working copy and in no checkout of either
// repository. So the pairing could never hold on a clean clone: the private
// repo's CI found the runbook present and the rail absent and concluded, in its
// own words, that a public projection had retained a private runbook. Confident,
// specific, and about the opposite of what was true. It was red for forty
// consecutive runs while passing on the one machine where the untracked file
// happened to sit on disk.
//
// docs/runbooks/release.md is the honest marker: tracked in the private source,
// stripped from every public projection alongside the rest of docs/, and the
// document that actually governs release control. A test that decides WHICH TREE
// it is in must ask git-visible state, never the ambient contents of somebody's
// working directory.
func privateReleaseControlRail(repoRoot string) string {
	return filepath.Join(repoRoot, "docs", "runbooks", "release.md")
}

func TestCandidateRunbookEmitsParserMarkersWhenReleaseControlIsPresent(t *testing.T) {
	t.Parallel()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..")
	runbook := filepath.Join(repoRoot, "docs", "runbooks", "live-e2e", "candidate.sh")
	raw, err := os.ReadFile(runbook)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read candidate runbook: %v", err)
		}
		// The runbook and the rail below are both private release-control
		// surfaces removed from every public projection. A missing runbook stays
		// a hard failure in the private source; their deliberate JOINT absence is
		// the public shape.
		if _, railErr := os.Stat(privateReleaseControlRail(repoRoot)); railErr == nil {
			t.Fatalf("private release-control rail exists but candidate runbook is missing: %v", err)
		} else if !errors.Is(railErr, os.ErrNotExist) {
			t.Fatalf("stat private release-control rail: %v", railErr)
		}
		return
	}
	if _, err := os.Stat(privateReleaseControlRail(repoRoot)); err != nil {
		// A public projection must not accidentally retain a private runbook.
		t.Fatalf("candidate runbook exists without private release-control rail: %v", err)
	}
	// Every assertion below reads the script's CODE, never its prose. A
	// comment that happens to spell `make check` — and the --check-only flag's
	// own doc comment does, because explaining the flag requires naming what
	// it runs — otherwise lands earlier in the file than the real invocation
	// and inverts the ordering check below. That is a guard failing on an
	// explanation of the thing it guards, and the only lesson it can teach is
	// to stop explaining. Stripping is deliberately crude (a `#` opening a
	// trimmed line): this file has no trailing-comment convention, and a
	// cleverer parser here would be its own thing to get wrong.
	text := shellCodeOnly(string(raw))
	if strings.Count(text, `echo "CHECKOUT_ROOT=$VERIFY_ROOT"`) != 1 {
		t.Fatalf("candidate runbook must emit exactly one CHECKOUT_ROOT marker")
	}
	if strings.Contains(text, `echo "CHECKOUT=$VERIFY_ROOT"`) {
		t.Fatalf("candidate runbook emits obsolete CHECKOUT marker")
	}
	if strings.Count(text, "npm --prefix web ci --ignore-scripts") != 1 ||
		strings.Count(text, `echo "WEB_DEPS_READY=true"`) != 1 {
		t.Fatalf("candidate runbook must prepare the locked web toolchain and retain its marker")
	}
	install := strings.Index(text, "npm --prefix web ci --ignore-scripts")
	marker := strings.Index(text, `echo "WEB_DEPS_READY=true"`)
	check := strings.Index(text, "make check")
	if !strings.Contains(text, "set +e\n  (\n    set -e") || install < 0 || marker < install || check < marker {
		t.Fatalf("candidate runbook must fail fast in npm ci before marking dependencies ready and running make check")
	}

	detached := filepath.Join(repoRoot, "docs", "runbooks", "live-e2e", "detached.sh")
	detachedRaw, err := os.ReadFile(detached)
	if err != nil {
		t.Fatalf("read detached runbook: %v", err)
	}
	if strings.Count(shellCodeOnly(string(detachedRaw)), `"WEB_DEPS_READY=true"`) != 1 {
		t.Fatalf("detached runbook must require exactly one successful web-dependency marker")
	}
}

func (f candidateSourceFixture) runner(_ context.Context, _ string, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	out, ok := f.commands[key]
	if !ok {
		return "", errors.New("unexpected command: " + key)
	}
	return out, nil
}

func (f candidateSourceFixture) readFile(path string) ([]byte, error) {
	raw, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return raw, nil
}

func identityPath(path string) (string, error) { return path, nil }

func TestAttestCandidateSourceAcceptsExactCleanExecutionCheckout(t *testing.T) {
	t.Parallel()
	f := newCandidateSourceFixture(t)
	got, err := attestCandidateSourceWith(t.Context(), f.want, f.runner, f.readFile, identityPath)
	if err != nil {
		t.Fatalf("attestCandidateSourceWith: %v", err)
	}
	if got.SourceRoot != f.want.Root || got.SourceSHA != f.want.SHA || got.SourceTree != f.want.Tree ||
		!got.SourceDetached || !got.IndexClean || !got.WorktreeClean || !got.UntrackedClean ||
		got.IntendedTag != f.want.Tag || got.TemplateFloor != f.want.Floor || got.CheckLog != f.want.CheckLog {
		t.Fatalf("attestation = %+v, want expectation %+v", got, f.want)
	}
}

func TestAttestCandidateSourceRefusesEveryCandidateMismatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*candidateSourceFixture)
		want   string
	}{
		{"ambient subdirectory", func(f *candidateSourceFixture) {
			f.commands["git rev-parse --show-toplevel"] = filepath.Dir(f.want.Root)
		}, "not the git module root"},
		{"wrong module", func(f *candidateSourceFixture) {
			f.files[filepath.Join(f.want.Root, "go.mod")] = []byte("module example.invalid/other\n")
		}, "does not declare module"},
		{"head mismatch", func(f *candidateSourceFixture) { f.commands["git rev-parse HEAD"] = strings.Repeat("c", 40) }, "HEAD="},
		{"tree mismatch", func(f *candidateSourceFixture) { f.commands["git rev-parse HEAD^{tree}"] = strings.Repeat("c", 40) }, "HEAD tree="},
		{"attached checkout", func(f *candidateSourceFixture) { f.commands["git rev-parse --abbrev-ref HEAD"] = "main" }, "want detached HEAD"},
		{"staged change", func(f *candidateSourceFixture) {
			f.commands["git diff --cached --name-only --no-ext-diff"] = "staged.go"
		}, "index is dirty"},
		{"worktree change", func(f *candidateSourceFixture) { f.commands["git diff --name-only --no-ext-diff"] = "changed.go" }, "worktree is dirty"},
		{"untracked change", func(f *candidateSourceFixture) {
			f.commands["git status --porcelain=v1 --untracked-files=all"] = "?? stray"
		}, "including untracked"},
		{"verification checkout reused for execution", func(f *candidateSourceFixture) {
			raw := string(f.files[f.want.CheckLog])
			firstEnd := strings.IndexByte(raw, '\n')
			f.files[f.want.CheckLog] = []byte("CHECKOUT_ROOT=" + f.want.Root + raw[firstEnd:])
		}, "roots are identical"},
		{"wrong floor", func(f *candidateSourceFixture) {
			f.files[filepath.Join(f.want.Root, "space-template", "space.yaml")] = []byte("min_binary_version: 0.18.2\n")
		}, "embedded min_binary_version"},
		{"duplicate floor", func(f *candidateSourceFixture) {
			f.files[filepath.Join(f.want.Root, "space-template", "space.yaml")] = []byte("min_binary_version: 0.19.0\nmin_binary_version: 0.19.0\n")
		}, "cardinality=2"},
		{"stale workflow ref", func(f *candidateSourceFixture) {
			path := filepath.Join(f.want.Root, "space-template", ".github", "workflows", "a2a-validate.yml")
			f.files[path] = []byte(strings.Replace(string(f.files[path]), "@v0.19.0", "@v0.18.2", 1))
		}, "found v0.18.2"},
		{"missing workflow ref", func(f *candidateSourceFixture) {
			path := filepath.Join(f.want.Root, "space-template", ".github", "workflows", "a2a-validate.yml")
			f.files[path] = []byte("jobs: {}\n")
		}, "cardinality=0"},
		{"extra workflow ref", func(f *candidateSourceFixture) {
			path := filepath.Join(f.want.Root, "space-template", ".github", "workflows", "a2a-validate.yml")
			f.files[path] = append(f.files[path], []byte("  extra:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@main\n")...)
		}, "cardinality=3"},
		{"wrong check sha", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = candidateCheckLog(t.TempDir(), "nope", f.want.Tree, f.want.Tag, "0")
		}, "CANDIDATE_SHA="},
		{"wrong check tree", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = candidateCheckLog(t.TempDir(), f.want.SHA, "nope", f.want.Tag, "0")
		}, "CANDIDATE_TREE="},
		{"wrong check tag", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, "v0.18.2", "0")
		}, "CANDIDATE_TAG="},
		{"verification checkout attached", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "CHECKOUT_DETACHED=true", "CHECKOUT_DETACHED=false", 1))
		}, "CHECKOUT_DETACHED"},
		{"verification index dirty", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "INDEX_CLEAN=true", "INDEX_CLEAN=false", 1))
		}, "INDEX_CLEAN"},
		{"verification worktree dirty", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "WORKTREE_CLEAN=true", "WORKTREE_CLEAN=false", 1))
		}, "WORKTREE_CLEAN"},
		{"verification untracked dirty", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "UNTRACKED_CLEAN=true", "UNTRACKED_CLEAN=false", 1))
		}, "UNTRACKED_CLEAN"},
		{"web dependencies not ready", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "WEB_DEPS_READY=true", "WEB_DEPS_READY=false", 1))
		}, "WEB_DEPS_READY"},
		{"missing web dependencies marker", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "WEB_DEPS_READY=true\n", "", 1))
		}, "exactly one WEB_DEPS_READY marker"},
		{"ambiguous web dependencies marker", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = append(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0"), []byte("WEB_DEPS_READY=true\n")...)
		}, "exactly one WEB_DEPS_READY marker"},
		{"missing logic tier rows marker", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "LOGIC_TIER_ROWS_SHA256="+logicTierRowsDigest(logicTierRowKeys(Catalogue()))+"\n", "", 1))
		}, "exactly one LOGIC_TIER_ROWS_SHA256 marker"},
		{"ambiguous logic tier rows marker", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = append(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0"), []byte("LOGIC_TIER_ROWS_SHA256="+logicTierRowsDigest(logicTierRowKeys(Catalogue()))+"\n")...)
		}, "exactly one LOGIC_TIER_ROWS_SHA256 marker"},
		{"malformed logic tier rows marker", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = []byte(strings.Replace(string(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0")), "LOGIC_TIER_ROWS_SHA256="+logicTierRowsDigest(logicTierRowKeys(Catalogue())), "LOGIC_TIER_ROWS_SHA256=not-a-digest", 1))
		}, "must be a full lowercase sha256 digest"},
		{"failed check", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "1")
		}, "retained check log EXIT"},
		{"ambiguous check exit", func(f *candidateSourceFixture) {
			f.files[f.want.CheckLog] = append(candidateCheckLog(t.TempDir(), f.want.SHA, f.want.Tree, f.want.Tag, "0"), []byte("EXIT=0\n")...)
		}, "exactly one EXIT marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newCandidateSourceFixture(t)
			tc.mutate(&f)
			_, err := attestCandidateSourceWith(t.Context(), f.want, f.runner, f.readFile, identityPath)
			if !errors.Is(err, ErrCandidateSource) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want candidate source refusal containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAttestCandidateSourceRefusesRelativeRootAndUnreadableEvidence(t *testing.T) {
	t.Parallel()
	f := newCandidateSourceFixture(t)
	f.want.Root = "relative"
	if _, err := attestCandidateSourceWith(t.Context(), f.want, f.runner, f.readFile, identityPath); !errors.Is(err, ErrCandidateSource) {
		t.Fatalf("want relative-root refusal, got %v", err)
	}

	f = newCandidateSourceFixture(t)
	delete(f.files, f.want.CheckLog)
	if _, err := attestCandidateSourceWith(t.Context(), f.want, f.runner, f.readFile, identityPath); !errors.Is(err, ErrCandidateSource) || !strings.Contains(err.Error(), "read retained") {
		t.Fatalf("want unreadable-check-log refusal, got %v", err)
	}
}

func TestAttestCandidateBinaryRecordsDigestRevisionAndStamp(t *testing.T) {
	t.Parallel()
	bin := filepath.Join(t.TempDir(), "a2a")
	if err := os.WriteFile(bin, []byte("candidate-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := CandidateExpectation{Root: t.TempDir(), SHA: strings.Repeat("a", 40), Floor: requiredCandidateFloor}
	source := CandidateAttestation{SourceSHA: want.SHA}
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: want.SHA}, {Key: "vcs.modified", Value: "false"}}}
	run := func(_ context.Context, _, _ string, _ ...string) (string, error) {
		return "a2a 0.19.0 (" + want.SHA + ")", nil
	}
	got, err := attestCandidateBinaryWith(t.Context(), bin, want, source, run, func(string) (*debug.BuildInfo, error) { return info, nil })
	if err != nil {
		t.Fatalf("attestCandidateBinaryWith: %v", err)
	}
	if len(got.BinarySHA256) != 64 || got.BuildRevision != want.SHA || got.BuildModified || got.BinaryStamp == "" {
		t.Fatalf("incomplete binary attestation: %+v", got)
	}
}

func TestAttestCandidateBinaryRefusesEveryStampAndBuildInfoMismatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		settings []debug.BuildSetting
		stamp    string
		infoErr  error
		wantErr  string
	}{
		{"missing revision", []debug.BuildSetting{{Key: "vcs.modified", Value: "false"}}, "", nil, "vcs.revision"},
		{"wrong revision", []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("b", 40)}, {Key: "vcs.modified", Value: "false"}}, "", nil, "vcs.revision"},
		{"missing modified", []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("a", 40)}}, "", nil, "vcs.modified"},
		{"modified source", []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "true"}}, "", nil, "vcs.modified"},
		{"stamp mismatch", []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "false"}}, "a2a 0.19.0 (wrong)", nil, "binary stamp"},
		{"unreadable build info", nil, "", errors.New("not a Go executable"), "read Go build info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := filepath.Join(t.TempDir(), "a2a")
			if err := os.WriteFile(bin, []byte("candidate-binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			want := CandidateExpectation{Root: t.TempDir(), SHA: strings.Repeat("a", 40), Floor: requiredCandidateFloor}
			stamp := tc.stamp
			if stamp == "" {
				stamp = "a2a 0.19.0 (" + want.SHA + ")"
			}
			run := func(_ context.Context, _, _ string, _ ...string) (string, error) { return stamp, nil }
			reader := func(string) (*debug.BuildInfo, error) {
				if tc.infoErr != nil {
					return nil, tc.infoErr
				}
				return &debug.BuildInfo{Settings: tc.settings}, nil
			}
			_, err := attestCandidateBinaryWith(t.Context(), bin, want, CandidateAttestation{}, run, reader)
			if !errors.Is(err, ErrCandidateBinary) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want binary refusal containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// shellCodeOnly drops whole-line shell comments so a runbook assertion reads
// what the script DOES, not what it says about itself. See its call sites for
// the failure it exists to prevent.
func shellCodeOnly(script string) string {
	lines := strings.Split(script, "\n")
	out := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
