package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// updateRepoRoot resolves the product repo root from this source file's own
// location (internal/cli/cmd_update_test.go -> ../.. is the repo root) —
// the same runtime.Caller idiom internal/e2e/main_test.go's repoRoot uses,
// copied rather than imported (internal/e2e is not a dependency of
// internal/cli).
func updateRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved root %s has no go.mod: %v", root, err)
	}
	return root
}

// updateChildBinOnce/updateChildBinPath/updateChildBinErr build the real
// `a2a` binary EXACTLY ONCE for this test binary's whole run (spec 04 §11's
// LEAD DECISION: "Build ONCE per test binary, not per case"), novel for
// package cli — internal/e2e already owns this idiom (main_test.go:108-118)
// for testscript-driven black-box tests; this package's seams
// (postSwapRunner, resolveExec, source, runner) are unexported, so only a
// package-cli test can wire them, and observing that the refreshed tree came
// from the CHILD's own embed (criterion 1) requires a real child carrying a
// real embed. No -ldflags, so the child stamps the package default "dev"
// (cmd/a2a/main.go's own literal) — criterion 1 uses exactly that to prove
// provenance: a PROVENANCE.md stamped "dev" while res.ToVersion is "0.3.0"
// could only have been written by the child.
var (
	updateChildBinOnce sync.Once
	updateChildBinPath string
	updateChildBinErr  error
)

// updateChildBinary returns the path to the once-built child `a2a` binary,
// failing the calling test (not the whole run) if the build itself failed.
func updateChildBinary(t *testing.T) string {
	t.Helper()
	root := updateRepoRoot(t)
	updateChildBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "a2a-cli-update-bin-*")
		if err != nil {
			updateChildBinErr = err
			return
		}
		bin := filepath.Join(dir, "a2a")
		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/a2a")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			updateChildBinErr = fmt.Errorf("go build ./cmd/a2a: %w\n%s", buildErr, out)
			return
		}
		updateChildBinPath = bin
	})
	if updateChildBinErr != nil {
		t.Fatalf("build child a2a binary: %v", updateChildBinErr)
	}
	return updateChildBinPath
}

// assertSkillTreeMatchesRepo asserts that every file under the repo's own
// skill/a2ahub/ embed corpus is byte-identical to the file the refresh wrote
// at the same relative path under target — the criterion-1 provenance-of-
// bytes assertion (spec 04 §8 row 1): the installed tree must come from
// WHICHEVER binary ran `skill install`, not the parent process, and the only
// binary that could have produced a match here is the child.
func assertSkillTreeMatchesRepo(t *testing.T, repoRoot, target string) {
	t.Helper()
	repoTree := os.DirFS(filepath.Join(repoRoot, "skill", "a2ahub"))
	err := fs.WalkDir(repoTree, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		want, readErr := fs.ReadFile(repoTree, p)
		if readErr != nil {
			return readErr
		}
		got, readErr := os.ReadFile(filepath.Join(target, filepath.FromSlash(p)))
		if readErr != nil {
			t.Errorf("read installed %s: %v", p, readErr)
			return nil
		}
		if !bytes.Equal(got, want) {
			t.Errorf("installed %s does not match the repo's own skill/a2ahub embed", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo skill tree: %v", err)
	}
}

// fakeUpdateSource is a release.Source test double: a fixed Release/error and
// a call counter, so tests can assert `--check` and the consent gate never
// reach the network beyond this one resolve call.
type fakeUpdateSource struct {
	rel   release.Release
	err   error
	calls int32
}

func (f *fakeUpdateSource) Latest(_ context.Context) (release.Release, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.rel, f.err
}
func (f *fakeUpdateSource) Name() string { return "fake" }

// newUpdateReleaseFixture wires an httptest server serving the platform
// asset + a correct SHA256SUMS for it, and returns the Release pointing at
// that server plus hit counters so tests can assert "no download happened"
// (§6 "--check performs zero downloads").
func newUpdateReleaseFixture(t *testing.T, targetVersion string) (release.Release, *int32, *int32) {
	t.Helper()
	platform := fmt.Sprintf("a2a-%s-%s", runtime.GOOS, runtime.GOARCH)
	assetBytes := []byte("NEW-BINARY-CONTENTS-v" + targetVersion)
	sum := sha256.Sum256(assetBytes)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), platform)

	var assetHits, sumsHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/asset":
			atomic.AddInt32(&assetHits, 1)
			_, _ = w.Write(assetBytes)
		case "/sums":
			atomic.AddInt32(&sumsHits, 1)
			_, _ = w.Write([]byte(sums))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	rel := release.Release{
		Tag:     "v" + targetVersion,
		Version: targetVersion,
		Commit:  "abc1234",
		Assets: []release.Asset{
			{Name: platform, BrowserDownloadURL: srv.URL + "/asset"},
			{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/sums"},
		},
	}
	return rel, &assetHits, &sumsHits
}

// newUpdateExecFixture seeds a fake "running binary" in its own temp
// directory (never the test binary itself) so Swap's real rename exercises
// a throwaway file.
func newUpdateExecFixture(t *testing.T) (execPath string, origBytes []byte) {
	t.Helper()
	dir := t.TempDir()
	execPath = filepath.Join(dir, "a2a")
	origBytes = []byte("OLD-BINARY-CONTENTS-v0.1.0")
	if err := os.WriteFile(execPath, origBytes, 0o755); err != nil {
		t.Fatalf("seed exec: %v", err)
	}
	return execPath, origBytes
}

func assertUpdateExecUnchanged(t *testing.T, execPath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read exec: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("running binary was modified: got %q want %q", got, want)
	}
}

// updateMatchingRunner echoes cmd/a2a's versionStamp() contract
// ("a2a <version> (<sha>)") so release.SelfCheckVersion's post-download
// self-check passes.
func updateMatchingRunner(version string) release.Runner {
	return func(_ context.Context, _ string, _ ...string) (string, error) {
		return fmt.Sprintf("a2a %s (abc1234)\n", version), nil
	}
}

// newTestUpdateCommand builds an UpdateCommand with every network/exec/FS
// seam defaulted to an inert value; individual tests override what they
// need. Project/machine config paths point at files that do not exist,
// which is a tolerant no-op per the constructor's real defaults (same
// convention SyncCommand uses) — zero connected spaces, zero floor
// constraint, unless a test overrides loadProjectConfig itself.
func newTestUpdateCommand(t *testing.T, binaryVersion string) *UpdateCommand {
	t.Helper()
	dir := t.TempDir()
	cmd := NewUpdateCommand(binaryVersion,
		filepath.Join(dir, "config.yaml"),
		filepath.Join(dir, "machine.yaml"),
		dir,
	)
	// The real ResolveToken() reads process env directly (not seamed, per
	// this phase's brief: Apply's Token field is wired verbatim from
	// release.ResolveToken()). Clear it so tests never accidentally pick up
	// a developer machine's GH_TOKEN and flip to the tokened fetch path,
	// which would break the httptest fixture's tokenless BrowserDownloadURL
	// wiring.
	t.Setenv("A2A_UPDATE_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	return cmd
}

func newUpdateIO(stdin string) (IO, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return IO{Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr}, &stdout, &stderr
}

func TestUpdateCommand_UpToDate_NoDownload(t *testing.T) {
	rel, assetHits, sumsHits := newUpdateReleaseFixture(t, "0.1.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	src := &fakeUpdateSource{rel: rel}
	cmd.source = func(string) release.Source { return src }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }

	stdio, stdout, _ := newUpdateIO("")
	code := cmd.Run(context.Background(), nil, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "already up to date") {
		t.Fatalf("stdout = %q, want an up-to-date message", stdout.String())
	}
	if got := atomic.LoadInt32(assetHits); got != 0 {
		t.Fatalf("asset downloaded (%d hits) when up to date", got)
	}
	if got := atomic.LoadInt32(sumsHits); got != 0 {
		t.Fatalf("SHA256SUMS downloaded (%d hits) when up to date", got)
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestUpdateCommand_Check_UpdateAvailable_NoDownload(t *testing.T) {
	rel, assetHits, sumsHits := newUpdateReleaseFixture(t, "0.3.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	src := &fakeUpdateSource{rel: rel}
	cmd.source = func(string) release.Source { return src }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }

	stdio, stdout, _ := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--check"}, stdio)

	if code != 10 {
		t.Fatalf("exit code = %d, want 10", code)
	}
	if !strings.Contains(stdout.String(), "update available") {
		t.Fatalf("stdout = %q, want an update-available message", stdout.String())
	}
	if atomic.LoadInt32(&src.calls) != 1 {
		t.Fatalf("source.Latest called %d times, want 1", src.calls)
	}
	if got := atomic.LoadInt32(assetHits); got != 0 {
		t.Fatalf("--check downloaded the asset (%d hits)", got)
	}
	if got := atomic.LoadInt32(sumsHits); got != 0 {
		t.Fatalf("--check downloaded SHA256SUMS (%d hits)", got)
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestUpdateCommand_CheckJSON_Shape(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }

	stdio, stdout, _ := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--check", "--json"}, stdio)

	if code != 10 {
		t.Fatalf("exit code = %d, want 10", code)
	}
	var got updateJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode --json output: %v (raw: %s)", err, stdout.String())
	}
	want := updateJSON{
		Current:         "0.1.0",
		Latest:          "0.3.0",
		UpdateAvailable: true,
		Floor:           "",
		FloorSpace:      "",
		Required:        false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--json = %+v, want %+v", got, want)
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestUpdateCommand_NonTTY_WithoutYes_Refuses(t *testing.T) {
	rel, assetHits, sumsHits := newUpdateReleaseFixture(t, "0.3.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.isTTY = func() bool { return false }

	stdio, _, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), nil, stdio)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Fatalf("stderr = %q, want a --yes hint", stderr.String())
	}
	if got := atomic.LoadInt32(assetHits); got != 0 {
		t.Fatalf("asset downloaded (%d hits) without consent", got)
	}
	if got := atomic.LoadInt32(sumsHits); got != 0 {
		t.Fatalf("SHA256SUMS downloaded (%d hits) without consent", got)
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestUpdateCommand_AllowUnsigned_HappySwap(t *testing.T) {
	rel, assetHits, sumsHits := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated v0.1.0 -> v0.3.0 (abc1234)") {
		t.Fatalf("stdout = %q, want the version-delta report line", stdout.String())
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read swapped exec: %v", err)
	}
	if string(got) != "NEW-BINARY-CONTENTS-v0.3.0" {
		t.Fatalf("exec content = %q, want the swapped asset content", got)
	}
	if atomic.LoadInt32(assetHits) != 1 {
		t.Fatalf("asset hits = %d, want exactly 1", *assetHits)
	}
	if atomic.LoadInt32(sumsHits) != 1 {
		t.Fatalf("SHA256SUMS hits = %d, want exactly 1", *sumsHits)
	}
}

// --- post-swap digest + skill refresh (P31 wave 5) -------------------------

// updateFakeWhatsnewRunner returns out/err for every invocation, recording
// the args it was called with so tests can assert the digest execs the
// NEW (just-swapped) binary's own `whatsnew --since <FromVersion>`.
func updateFakeWhatsnewRunner(out string, err error, calls *[][]string) release.Runner {
	return func(_ context.Context, path string, args ...string) (string, error) {
		*calls = append(*calls, append([]string{path}, args...))
		return out, err
	}
}

func TestUpdateCommand_PostSwapDigest_PrintsUnderHeader(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")

	var calls [][]string
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("v0.3.0 (2026-01-01) — headline\n  [high] a change\n", nil, &calls)

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "what changed") || !strings.Contains(stdout.String(), "a change") {
		t.Fatalf("stdout = %q, want the digest under a header", stdout.String())
	}
	if len(calls) != 1 {
		t.Fatalf("whatsnewRunner calls = %d, want 1", len(calls))
	}
	if calls[0][0] != execPath {
		t.Fatalf("whatsnewRunner path = %q, want the swapped exec path %q", calls[0][0], execPath)
	}
	if want := []string{"whatsnew", "--since", "0.1.0"}; strings.Join(calls[0][1:], " ") != strings.Join(want, " ") {
		t.Fatalf("whatsnewRunner args = %v, want %v", calls[0][1:], want)
	}
}

func TestUpdateCommand_PostSwapDigest_RunnerError_StillExitsZeroNoExtraOutput(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")

	var calls [][]string
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("exec: no such file"), &calls)

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (a digest failure is never fatal); stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "what changed") {
		t.Fatalf("stdout = %q, want no digest header on a runner error", stdout.String())
	}
	if !strings.Contains(stdout.String(), "updated v0.1.0 -> v0.3.0") {
		t.Fatalf("stdout = %q, want the version-delta line still present", stdout.String())
	}
}

func TestUpdateCommand_PostSwapDigest_EmptyOutput_Skipped(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")

	var calls [][]string
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("   \n", nil, &calls)

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "what changed") {
		t.Fatalf("stdout = %q, want no digest header on whitespace-only output", stdout.String())
	}
}

func TestUpdateCommand_PostSwapDigest_JSONMode_NoHumanDigest(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")

	var calls [][]string
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("v0.3.0 — headline\n  [high] a change\n", nil, &calls)

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned", "--json"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "what changed") {
		t.Fatalf("stdout = %q, want no human digest in --json mode", stdout.String())
	}
	var result updateJSON
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("full update --json is not one JSON document: %v (raw: %s)", err, stdout.String())
	}
	if result.Current != "0.1.0" || result.Latest != "0.3.0" {
		t.Fatalf("full update --json = %+v", result)
	}
	if len(calls) != 0 {
		t.Fatalf("whatsnewRunner calls = %d, want 0 (never invoked in --json mode)", len(calls))
	}
}

// updateFakeSeamCall records one postSwapRunner invocation.
type updateFakeSeamCall struct {
	executable, dir string
	args            []string
}

// updateFakeSeam returns a postSwapRunner fake that records every call and
// dispatches its return value on args[0]: "skill" calls return skillErr,
// every other call (the notification-component repair) returns nil. One
// fake serves both callers per spec 04 §T1 "Consequence for fakes".
func updateFakeSeam(calls *[]updateFakeSeamCall, skillErr error) func(context.Context, string, string, ...string) error {
	return func(_ context.Context, executable, dir string, args ...string) error {
		*calls = append(*calls, updateFakeSeamCall{executable: executable, dir: dir, args: args})
		if len(args) > 0 && args[0] == "skill" {
			return skillErr
		}
		return nil
	}
}

func TestUpdateCommand_SkillRefresh_ManagedInstall_Refreshed(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("no digest"), &[][]string{})

	// Seed a PRE-EXISTING managed skill install (provenance-marked, older
	// version) under the command's own projectRoot (newTestUpdateCommand's
	// 4th NewUpdateCommand arg).
	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, nil)

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "refreshed skill install") {
		t.Fatalf("stdout = %q, want a refreshed-skill confirmation", stdout.String())
	}
	if len(calls) != 1 {
		t.Fatalf("postSwapRunner calls = %d, want exactly 1 (%v)", len(calls), calls)
	}
	if calls[0].executable != execPath || calls[0].dir != cmd.projectRoot {
		t.Fatalf("call = %+v, want executable=%q dir=%q", calls[0], execPath, cmd.projectRoot)
	}
	if want := []string{"skill", "install"}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Fatalf("call args = %v, want %v (no flags)", calls[0].args, want)
	}
}

func TestUpdateCommand_SkillRefresh_CustomConfiguredInstall(t *testing.T) {
	// reason: newTestUpdateCommand uses t.Setenv for update-source isolation.
	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{SkillDir: "agent/manual"}, nil
	}
	target := filepath.Join(cmd.projectRoot, "agent", "manual")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, nil)

	var stdout, stderr bytes.Buffer
	cmd.refreshInstalledSkill(context.Background(), "the-swapped-exec-path", release.ApplyResult{ToVersion: "0.3.0"}, IO{Stdout: &stdout, Stderr: &stderr}, false)

	if !strings.Contains(stdout.String(), "agent/manual -> v0.3.0") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(calls) != 1 {
		t.Fatalf("postSwapRunner calls = %d, want exactly 1 (%v)", len(calls), calls)
	}
	// The exec's WORKING DIRECTORY is projectRoot — NOT the custom target
	// (spec 04 §5 "Two resolutions of skill_dir": the CHILD resolves the
	// target itself, from its own cwd's .a2a/config.yaml).
	if calls[0].executable != "the-swapped-exec-path" || calls[0].dir != cmd.projectRoot {
		t.Fatalf("call = %+v, want executable=%q dir=%q", calls[0], "the-swapped-exec-path", cmd.projectRoot)
	}
	if want := []string{"skill", "install"}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Fatalf("call args = %v, want %v (no --dir, no --force)", calls[0].args, want)
	}
	if _, err := os.Stat(filepath.Join(cmd.projectRoot, skillDefaultDir)); !os.IsNotExist(err) {
		t.Fatalf("refresh unexpectedly touched default target: %v", err)
	}
}

func TestUpdateCommand_SkillRefresh_NoInstall_SkippedSilently(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("no digest"), &[][]string{})
	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, nil)
	// No pre-existing install at cmd.projectRoot/.a2ahub/skill — never created.

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "refreshed skill install") {
		t.Fatalf("stdout = %q, want no refresh message when there is nothing to refresh", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(cmd.projectRoot, skillDefaultDir)); !os.IsNotExist(err) {
		t.Fatalf("skill refresh created an install where none existed (err=%v)", err)
	}
	// The stronger claim criterion 5 asks for: the seam was NEVER invoked at
	// all for the skill refresh (a component-repair call, if any, is fine —
	// this fixture configures none, so any call at all is unexpected here).
	if len(calls) != 0 {
		t.Fatalf("postSwapRunner calls = %d, want 0 (%v)", len(calls), calls)
	}
}

func TestUpdateCommand_SkillRefresh_ForeignTarget_SkippedNotClobbered(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("no digest"), &[][]string{})
	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, nil)

	// A target that exists but carries NO a2ahub provenance marker — someone
	// else's content. The update must never touch it.
	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "unrelated.txt"), []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "refreshed skill install") {
		t.Fatalf("stdout = %q, want no refresh message for a foreign target", stdout.String())
	}
	if _, err := os.ReadFile(filepath.Join(skillTarget, "unrelated.txt")); err != nil {
		t.Fatalf("foreign target content was removed: %v", err)
	}
	// The stronger claim criterion 6 asks for: the seam was NEVER invoked.
	if len(calls) != 0 {
		t.Fatalf("postSwapRunner calls = %d, want 0 (%v)", len(calls), calls)
	}
}

// fakeSig is a signature-layer stub for the composite verifier: it lets a
// test drive the CLI's signature gating without a real .cosign.bundle.
type fakeSig struct{ err error }

func (f fakeSig) Verify(context.Context, string, release.Release) error { return f.err }

// TestUpdateCommand_VerifiedSignatureSwapsWithoutFlag: a signature that
// VERIFIES lets the update proceed with NO --allow-unsigned — the end-state
// the keyless verifier delivers (checksum runs first via the composite).
func TestUpdateCommand_VerifiedSignatureSwapsWithoutFlag(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")
	cmd.verifier = func(string) release.Verifier { return release.NewCompositeVerifier(fakeSig{nil}) }

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes"}, stdio) // NO --allow-unsigned
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (verified signature needs no flag); stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated v0.1.0 -> v0.3.0") {
		t.Fatalf("stdout = %q, want the version-delta line", stdout.String())
	}
}

// TestUpdateCommand_InvalidSignatureNotGateable: a present-but-INVALID
// signature is a hard stop — --allow-unsigned does NOT override it, the binary
// is untouched. Pins the fail-closed invariant end-to-end through the CLI.
func TestUpdateCommand_InvalidSignatureNotGateable(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.verifier = func(string) release.Verifier {
		return release.NewCompositeVerifier(fakeSig{release.ErrSignatureInvalid})
	}

	stdio, _, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned"}, stdio)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (invalid signature never gateable); stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "never overridable") {
		t.Fatalf("stderr = %q, want the non-overridable signature message", stderr.String())
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestUpdateCommand_BelowFloor(t *testing.T) {
	rel, assetHits, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa"}}}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }
	cmd.readFile = func(string) ([]byte, error) { return []byte("min_binary_version: \"0.5.0\"\n"), nil }

	stdio, _, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), nil, stdio)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "getvisa") || !strings.Contains(stderr.String(), "0.5.0") {
		t.Fatalf("stderr = %q, want it to name the pinning space and its floor", stderr.String())
	}
	if got := atomic.LoadInt32(assetHits); got != 0 {
		t.Fatalf("asset downloaded (%d hits) despite BelowFloor", got)
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestUpdateCommand_UsageError(t *testing.T) {
	cmd := newTestUpdateCommand(t, "0.1.0")

	stdio, _, _ := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"unexpected-positional-arg"}, stdio)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 for an unexpected positional arg", code)
	}

	stdio2, _, _ := newUpdateIO("")
	code2 := cmd.Run(context.Background(), []string{"--not-a-real-flag"}, stdio2)
	if code2 != 2 {
		t.Fatalf("exit code = %d, want 2 for an unknown flag", code2)
	}
}

// TestUpdateCommand_ComputeFloor_MaxAcrossSpaces exercises the floor
// computation directly: the MAX min_binary_version across multiple
// connected spaces wins, and an unreadable/unparseable manifest is skipped
// rather than failing the whole computation (best-effort per this phase's
// brief).
func TestUpdateCommand_ComputeFloor_MaxAcrossSpaces(t *testing.T) {
	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.resolveMirror = func(_ string, ref space.Ref, _ space.MachineConfig) string { return ref.ID }
	cmd.readFile = func(path string) ([]byte, error) {
		switch path {
		case filepath.Join("low", "space.yaml"):
			return []byte("min_binary_version: \"0.2.0\"\n"), nil
		case filepath.Join("high", "space.yaml"):
			return []byte("min_binary_version: \"0.9.0\"\n"), nil
		case filepath.Join("broken", "space.yaml"):
			return nil, fmt.Errorf("boom: unreadable")
		case filepath.Join("malformed", "space.yaml"):
			return []byte("not: [valid"), nil
		default:
			return nil, fmt.Errorf("unexpected path %q", path)
		}
	}
	cfg := space.ProjectConfig{Spaces: []space.Ref{
		{ID: "low"}, {ID: "high"}, {ID: "broken"}, {ID: "malformed"},
	}}
	floor, floorSpace := cmd.computeFloor(cfg, space.MachineConfig{})
	if floor != "0.9.0" || floorSpace != "high" {
		t.Fatalf("computeFloor = (%q, %q), want (0.9.0, high)", floor, floorSpace)
	}
}

func TestUpdateCommand_NameAndSynopsis(t *testing.T) {
	cmd := newTestUpdateCommand(t, "0.1.0")
	if cmd.Name() != "update" {
		t.Fatalf("Name() = %q, want %q", cmd.Name(), "update")
	}
	if cmd.Synopsis() == "" {
		t.Fatal("Synopsis() is empty")
	}
}

func TestUpdateCommandRefreshesOnlyManagedEnabledNotificationComponents(t *testing.T) {
	// reason: newTestUpdateCommand owns process environment through t.Setenv.
	cmd := newTestUpdateCommand(t, "0.1.0")
	var calls [][]string
	cmd.postSwapRunner = func(_ context.Context, executable, dir string, args ...string) error {
		calls = append(calls, append([]string{executable, dir}, args...))
		return nil
	}
	machine := space.MachineConfig{Notifications: space.NotificationConfig{
		Projects: []space.NotificationProject{
			{Root: "/projects/one", Channels: []string{"vscode"}},
		},
		Components: []space.NotificationComponent{
			{Channel: "macos", Version: "0.1.0"},
			{Channel: "vscode", Profile: "Work", CodePath: "/opt/code", Version: "0.1.0"},
		},
	}}
	stdio, _, _ := newUpdateIO("")

	results := cmd.refreshManagedNotificationComponents(context.Background(), "/opt/a2a", machine, stdio)

	want := []string{
		"/opt/a2a", "/projects/one",
		"notifications", "install", "--channel", "vscode",
		"--project", "/projects/one", "--json",
		"--profile", "Work", "--code", "/opt/code",
	}
	if len(calls) != 1 || strings.Join(calls[0], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("companion calls = %v, want [%v]", calls, want)
	}
	if len(results) != 1 || results[0].Status != "updated" || results[0].Profile != "Work" {
		t.Fatalf("component results = %+v", results)
	}
}

// TestUpdateCommand_SharedSeam_DispatchesBothCallersWithoutConflating is
// spec 04 §6's "shared fake" row / §T1 "Consequence for fakes": the
// notification-component repair and the skill refresh reach the SAME
// postSwapRunner seam through one full `a2a update` run, and a fake
// dispatching on args[0] sees exactly two kinds of call — one "skill" call
// for the manual refresh, one "notifications" call for the component
// repair — and does not conflate them (the skill call must never surface as
// a component result, and vice versa).
func TestUpdateCommand_SharedSeam_DispatchesBothCallersWithoutConflating(t *testing.T) {
	rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, _ := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.runner = updateMatchingRunner("0.3.0")
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("no digest"), &[][]string{})
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) {
		return space.MachineConfig{Notifications: space.NotificationConfig{
			Projects:   []space.NotificationProject{{Root: "/projects/one", Channels: []string{"vscode"}}},
			Components: []space.NotificationComponent{{Channel: "vscode", Version: "0.1.0"}},
		}}, nil
	}

	// A pre-existing managed skill install so the refresh actually execs.
	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, nil)

	stdio, stdout, stderr := newUpdateIO("")
	code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned", "--json"}, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if len(calls) != 2 {
		t.Fatalf("postSwapRunner calls = %d, want exactly 2 (%v)", len(calls), calls)
	}
	var notificationsCalls, skillCalls int
	for _, c := range calls {
		if len(c.args) == 0 {
			t.Fatalf("call with no args: %+v", c)
		}
		switch c.args[0] {
		case "notifications":
			notificationsCalls++
		case "skill":
			skillCalls++
		default:
			t.Fatalf("unexpected call kind %q: %+v", c.args[0], c)
		}
	}
	if notificationsCalls != 1 || skillCalls != 1 {
		t.Fatalf("notifications calls = %d, skill calls = %d, want 1 each (%v)", notificationsCalls, skillCalls, calls)
	}

	var result updateJSON
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("--json stdout is not one JSON document: %v (raw: %s)", err, stdout.String())
	}
	if len(result.Components) != 1 || result.Components[0].Channel != "vscode" || result.Components[0].Status != "updated" {
		t.Fatalf("jsonResult.Components = %+v, want exactly the ONE notification component (the skill call must not be conflated into it)", result.Components)
	}
}

// TestUpdateCommand_TTY_Confirm_Cancelled exercises the isTTY==true /
// confirm==false branch (T1 `--yes` row: "a false answer => 'update:
// cancelled', exit 0"), distinct from the non-TTY refusal path above.
func TestUpdateCommand_TTY_Confirm_Cancelled(t *testing.T) {
	rel, assetHits, _ := newUpdateReleaseFixture(t, "0.3.0")
	execPath, orig := newUpdateExecFixture(t)

	cmd := newTestUpdateCommand(t, "0.1.0")
	cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
	cmd.resolveExec = func() (string, error) { return execPath, nil }
	cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
	cmd.isTTY = func() bool { return true }
	cmd.confirm = func(prompt string, _ IO) bool {
		if !strings.Contains(prompt, "0.1.0") || !strings.Contains(prompt, "0.3.0") {
			t.Fatalf("confirm prompt = %q, want it to show the version delta", prompt)
		}
		return false
	}

	stdio, stdout, _ := newUpdateIO("")
	code := cmd.Run(context.Background(), nil, stdio)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (cancelled)", code)
	}
	if !strings.Contains(stdout.String(), "cancelled") {
		t.Fatalf("stdout = %q, want a cancelled message", stdout.String())
	}
	if got := atomic.LoadInt32(assetHits); got != 0 {
		t.Fatalf("asset downloaded (%d hits) after a cancelled confirm", got)
	}
	assertUpdateExecUnchanged(t, execPath, orig)
}

func TestDefaultUpdateResolveExec(t *testing.T) {
	p, err := defaultUpdateResolveExec()
	if err != nil {
		t.Fatalf("defaultUpdateResolveExec: %v", err)
	}
	if p == "" {
		t.Fatal("defaultUpdateResolveExec returned an empty path")
	}
}

func TestDefaultUpdateIsTTY(t *testing.T) {
	// Not asserting a specific value (test runners are rarely a real TTY) —
	// just that the seam runs without panicking and returns a bool.
	_ = defaultUpdateIsTTY()
}

func TestDefaultUpdateConfirm(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"Y\n", true},
		{"n\n", false},
		{"\n", false},
		{"", false},
	}
	for _, tc := range cases {
		stdio := IO{Stdin: strings.NewReader(tc.input), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
		got := defaultUpdateConfirm("proceed? [y/N] ", stdio)
		if got != tc.want {
			t.Errorf("defaultUpdateConfirm(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestUpdateSkillRefreshWritesTheChildBinarysOwnTree is spec 04 §8 criterion
// 1: after a refresh the installed tree must be byte-identical to the NEW
// binary's own embedded skill/a2ahub corpus, and PROVENANCE must carry the
// NEW binary's own stamp — neither supplied by the updating (parent)
// process. res.ToVersion is deliberately set to a value ("0.3.0") that
// differs from the child's real build stamp ("dev", the default when built
// without -ldflags) so a PROVENANCE stamp of "dev" can only have been
// written by the CHILD.
func TestUpdateSkillRefreshWritesTheChildBinarysOwnTree(t *testing.T) {
	root := updateRepoRoot(t)
	execPath := updateChildBinary(t)
	cmd := newTestUpdateCommand(t, "0.1.0")

	// Seed a PRE-EXISTING managed skill install (provenance-marked, stale
	// content) under the command's own projectRoot, exactly like the other
	// SkillRefresh tests — the ownership gate must see an owned target.
	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	// The DEFAULT seam (unmodified from NewUpdateCommand: the real os/exec
	// closure) execs the real built binary. res.ToVersion is deliberately
	// wrong ("0.3.0") so a correct "dev" stamp on disk could only have come
	// from the CHILD, never from this parent process.
	stdio, stdout, _ := newUpdateIO("")
	cmd.refreshInstalledSkill(context.Background(), execPath, release.ApplyResult{ToVersion: "0.3.0"}, stdio, false)

	assertSkillTreeMatchesRepo(t, root, skillTarget)

	prov, err := os.ReadFile(filepath.Join(skillTarget, skillProvenanceFile))
	if err != nil {
		t.Fatalf("read PROVENANCE.md: %v", err)
	}
	if !strings.Contains(string(prov), "a2a dev") {
		t.Fatalf("PROVENANCE.md = %q, want the CHILD's own dev stamp (built without -ldflags), not res.ToVersion", prov)
	}
	if strings.Contains(string(prov), "0.3.0") {
		t.Fatalf("PROVENANCE.md = %q, want NO trace of the parent-supplied ToVersion (0.3.0)", prov)
	}
	if !strings.Contains(stdout.String(), "refreshed skill install") {
		t.Fatalf("stdout = %q, want a refreshed-skill confirmation", stdout.String())
	}
	// §9's own claim, proven here (not by the fake-seam tests, which never
	// spawn a child at all): the seam wires neither Stdout nor Stderr, so
	// os/exec connects both to the null device BY CONSTRUCTION. This is the
	// one test with a REAL child, so it is the one place the child's own
	// three human lines ("a2a skill: installed N files to …", "entry
	// point: …") could actually leak into this process's stdout — and don't.
	if strings.Contains(stdout.String(), "entry point") || strings.Contains(stdout.String(), "files to") {
		t.Fatalf("stdout = %q, want none of the child's own `skill install` output — the seam wires no Stdout, so os/exec connects it to the null device", stdout.String())
	}
}

// TestUpdateSkillRefreshExecsTheSwappedBinary is spec 04 §8 criterion 4: the
// refresh execs the swapped binary with argv exactly "skill install", no
// flags, in projectRoot — and writes no file into the target itself (the
// fake seam never touches disk, so any change to skillTarget could only
// have come from this process, which criterion 1 already forbids).
func TestUpdateSkillRefreshExecsTheSwappedBinary(t *testing.T) {
	cmd := newTestUpdateCommand(t, "0.1.0")
	execPath, _ := newUpdateExecFixture(t)

	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, nil)

	stdio, _, _ := newUpdateIO("")
	cmd.refreshInstalledSkill(context.Background(), execPath, release.ApplyResult{ToVersion: "0.3.0"}, stdio, false)

	if len(calls) != 1 {
		t.Fatalf("postSwapRunner calls = %d, want exactly 1 (%v)", len(calls), calls)
	}
	if calls[0].executable != execPath || calls[0].dir != cmd.projectRoot {
		t.Fatalf("call = %+v, want executable=%q dir=%q", calls[0], execPath, cmd.projectRoot)
	}
	if want := []string{"skill", "install"}; strings.Join(calls[0].args, " ") != strings.Join(want, " ") {
		t.Fatalf("call args = %v, want %v (no --force, no --dir)", calls[0].args, want)
	}
	got, err := os.ReadFile(filepath.Join(skillTarget, "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if string(got) != "stale content" {
		t.Fatalf("SKILL.md = %q, want it UNCHANGED — the fake seam never touched disk, so the refresh itself must write no file into the target", got)
	}
}

// TestUpdateSkillRefresh_SeamError_NoConfirmationLine is spec 04 §8
// criterion 7: a failing refresh leaves a2a update's exit code at 0, prints
// NO confirmation line, and warns on stderr — in both human and --json mode.
func TestUpdateSkillRefresh_SeamError_NoConfirmationLine(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
		execPath, _ := newUpdateExecFixture(t)

		cmd := newTestUpdateCommand(t, "0.1.0")
		cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
		cmd.resolveExec = func() (string, error) { return execPath, nil }
		cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
		cmd.runner = updateMatchingRunner("0.3.0")
		cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("no digest"), &[][]string{})

		skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
		if err := os.MkdirAll(skillTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
			t.Fatal(err)
		}
		var calls []updateFakeSeamCall
		cmd.postSwapRunner = updateFakeSeam(&calls, fmt.Errorf("exec: skill install: exit status 1"))

		args := []string{"--yes", "--allow-unsigned"}
		if jsonMode {
			args = append(args, "--json")
		}
		stdio, stdout, stderr := newUpdateIO("")
		code := cmd.Run(context.Background(), args, stdio)

		if code != 0 {
			t.Fatalf("json=%v: exit code = %d, want 0 (the update itself already succeeded)", jsonMode, code)
		}
		if strings.Contains(stdout.String(), "refreshed skill install") {
			t.Fatalf("json=%v: stdout = %q, want NO confirmation line when the refresh seam errors", jsonMode, stdout.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("json=%v: stderr is empty, want a warning naming the failure", jsonMode)
		}
	}
}

// TestUpdateSkillRefresh_FailureWarningNamesRemedy is spec 04 §8 criterion
// 8: the failure warning names `a2a skill install` AND `--force`, and says
// the target was verified a2ahub-owned before the refresh — so an
// interrupted child that removed PROVENANCE.md does not leave the operator
// at a refusal with no way out (cmd_skill.go:294/303's remove-first,
// stamp-last order).
func TestUpdateSkillRefresh_FailureWarningNamesRemedy(t *testing.T) {
	cmd := newTestUpdateCommand(t, "0.1.0")
	execPath, _ := newUpdateExecFixture(t)

	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&calls, fmt.Errorf("exec: skill install: exit status 1"))

	stdio, _, stderr := newUpdateIO("")
	cmd.refreshInstalledSkill(context.Background(), execPath, release.ApplyResult{ToVersion: "0.3.0"}, stdio, false)

	if !strings.Contains(stderr.String(), "a2a skill install") || !strings.Contains(stderr.String(), "--force") {
		t.Fatalf("stderr = %q, want it to name both `a2a skill install` and `--force`", stderr.String())
	}
	if !strings.Contains(stderr.String(), "owned") {
		t.Fatalf("stderr = %q, want it to say the target was verified a2ahub-owned before the refresh", stderr.String())
	}
}

// TestUpdateCommand_SkillRefresh_JSONCleanOnSuccessAndFailure is spec 04 §8
// criterion 9: `a2a update --json` stdout parses as exactly one JSON
// document whether the refresh succeeds or fails, and the refresh
// contributes nothing to it — the child's own strings never appear because
// the seam sets neither Stdout nor Stderr (os/exec connects both to the null
// device by construction, not by a suppression rule this phase must
// preserve).
func TestUpdateCommand_SkillRefresh_JSONCleanOnSuccessAndFailure(t *testing.T) {
	for _, seamErr := range []error{nil, fmt.Errorf("exec: skill install: exit status 1")} {
		rel, _, _ := newUpdateReleaseFixture(t, "0.3.0")
		execPath, _ := newUpdateExecFixture(t)

		cmd := newTestUpdateCommand(t, "0.1.0")
		cmd.source = func(string) release.Source { return &fakeUpdateSource{rel: rel} }
		cmd.resolveExec = func() (string, error) { return execPath, nil }
		cmd.cachePath = func() (string, error) { return filepath.Join(t.TempDir(), "cache.json"), nil }
		cmd.runner = updateMatchingRunner("0.3.0")
		cmd.whatsnewRunner = updateFakeWhatsnewRunner("", fmt.Errorf("no digest"), &[][]string{})

		skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
		if err := os.MkdirAll(skillTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
			t.Fatal(err)
		}
		var calls []updateFakeSeamCall
		cmd.postSwapRunner = updateFakeSeam(&calls, seamErr)

		stdio, stdout, _ := newUpdateIO("")
		code := cmd.Run(context.Background(), []string{"--yes", "--allow-unsigned", "--json"}, stdio)

		if code != 0 {
			t.Fatalf("seamErr=%v: exit code = %d, want 0", seamErr, code)
		}
		var result updateJSON
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("seamErr=%v: --json stdout is not one JSON document: %v (raw: %s)", seamErr, err, stdout.String())
		}
		if strings.Contains(stdout.String(), "installed") || strings.Contains(stdout.String(), "entry point") {
			t.Fatalf("seamErr=%v: stdout = %q, want none of the child's own `skill install` output", seamErr, stdout.String())
		}
	}
}

// TestUpdateSkillRefresh_DevFromVersion_StillRefreshes is spec 04 §8
// criterion 10: a FromVersion that is not a release version still
// refreshes — the digest's version precondition (printPostUpdateDigest
// returns early when FromVersion does not parse) is NOT inherited by the
// refresh. Driven directly against printPostUpdateDigest/refreshInstalledSkill
// rather than through the full cmd.Run pipeline: release.Resolve itself
// refuses an unparseable Current version before the swap step is ever
// reached (release/resolve.go — version.OlderThan("dev", latest) errors), so
// a dev-build `a2a update` never gets far enough to exercise this row
// end-to-end. Calling both post-swap steps directly with
// res.FromVersion = "dev" still proves the exact claim the criterion makes:
// the refresh's own precondition set is disjoint from the digest's
// (recorded as a deviation in spec 04 §11).
func TestUpdateSkillRefresh_DevFromVersion_StillRefreshes(t *testing.T) {
	cmd := newTestUpdateCommand(t, "0.1.0")
	execPath, _ := newUpdateExecFixture(t)

	skillTarget := filepath.Join(cmd.projectRoot, skillDefaultDir)
	if err := os.MkdirAll(skillTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, "SKILL.md"), []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillTarget, skillProvenanceFile), []byte(skillProvenance("0.1.0")), 0o644); err != nil {
		t.Fatal(err)
	}

	res := release.ApplyResult{FromVersion: "dev", ToVersion: "0.3.0"}
	var whatsnewCalls [][]string
	cmd.whatsnewRunner = updateFakeWhatsnewRunner("v0.3.0 — headline\n", nil, &whatsnewCalls)
	var skillCalls []updateFakeSeamCall
	cmd.postSwapRunner = updateFakeSeam(&skillCalls, nil)

	stdio, _, _ := newUpdateIO("")
	cmd.printPostUpdateDigest(context.Background(), execPath, res, stdio)
	cmd.refreshInstalledSkill(context.Background(), execPath, res, stdio, false)

	if len(whatsnewCalls) != 0 {
		t.Fatalf("whatsnewRunner calls = %d, want 0 (FromVersion %q does not parse — the digest is skipped)", len(whatsnewCalls), res.FromVersion)
	}
	if len(skillCalls) != 1 {
		t.Fatalf("postSwapRunner (skill) calls = %d, want exactly 1 — the refresh has no version precondition", len(skillCalls))
	}
}
