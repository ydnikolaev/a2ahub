package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

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
	if got != want {
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
