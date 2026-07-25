// Package installsh_test drives scripts/install.sh's setup_shell function in
// isolation, without ever performing a real install: sourcing the script
// with A2A_INSTALL_LIB=1 defines every function (curl_json, find_asset_id,
// download_asset, resolve_install_dir, install_binary, rc_needs_line,
// append_block, print_block, setup_shell) and returns before install_binary
// or setup_shell is ever invoked — see scripts/install.sh's §9 guard.
//
// Every case below builds cmd.Env from scratch (never os.Environ(), never
// t.Setenv): a leaked ZDOTDIR, XDG_DATA_HOME, or A2A_NO_MODIFY_PATH from the
// host running `go test` would write outside the sandbox or silently change
// which code path runs. Building Env from scratch is also what keeps every
// non-mutating subtest t.Parallel()-safe.
package installsh_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves the product repo root from this source file's own
// location (scripts/installsh/install_test.go -> ../.. is the repo root),
// the same idiom internal/e2e/main_test.go:66 uses — never the test's cwd.
func repoRoot(t *testing.T) string {
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

// installScriptPath returns the absolute path to scripts/install.sh.
func installScriptPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "scripts", "install.sh")
}

// a2aStub is a POSIX-sh executable that answers `a2a completion <shell>`
// with a marker string unique to that shell, so a test can prove the
// generated completion file actually came from the (stubbed) binary rather
// than existing-but-empty because the exec silently failed
// (`"$a2a_bin" completion zsh > file 2>/dev/null || return 0` creates an
// empty file even when $a2a_bin can't be found or errors).
const a2aStub = `#!/bin/sh
case "$1" in
  completion)
    case "$2" in
      zsh) printf '#compdef a2a\n# MARKER-ZSH\n' ;;
      bash) printf '# MARKER-BASH\n' ;;
      fish) printf '# MARKER-FISH\n' ;;
      *) exit 1 ;;
    esac
    ;;
  *) exit 1 ;;
esac
`

// sandbox is one case's isolated filesystem: a temp HOME and a temp
// A2A_INSTALL_DIR pre-loaded with the stub a2a binary at exactly
// ${installDir}/a2a — setup_shell calls "$a2a_bin" by absolute path, so
// being on $PATH is not enough (advisor note: install_dir/a2a, not just
// somewhere on PATH).
type sandbox struct {
	home       string
	installDir string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	home := t.TempDir()
	installDir := t.TempDir()
	stubPath := filepath.Join(installDir, "a2a")
	if err := os.WriteFile(stubPath, []byte(a2aStub), 0o755); err != nil {
		t.Fatalf("write a2a stub: %v", err)
	}
	return sandbox{home: home, installDir: installDir}
}

// runResult captures one setup_shell invocation.
type runResult struct {
	stdout string
	stderr string
	err    error
}

// runSetupShell sources install.sh in A2A_INSTALL_LIB mode and calls
// setup_shell exactly once, against an env map built entirely from scratch
// (no inherited variables). This is the idiom the brief specifies:
//
//	sh -c 'set -eu; A2A_INSTALL_LIB=1 . "$1"; setup_shell' sh <install.sh>
func runSetupShell(t *testing.T, env map[string]string) runResult {
	t.Helper()
	return run(t, env, `set -eu; A2A_INSTALL_LIB=1 . "$1"; setup_shell`)
}

// run is the shared low-level driver: it execs `sh -c script sh
// <install.sh>` with cmd.Env built solely from the supplied map.
func run(t *testing.T, env map[string]string, script string) runResult {
	t.Helper()
	cmd := exec.Command("sh", "-c", script, "sh", installScriptPath(t))
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	cmd.Env = envList

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return runResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// baseEnv is the minimal env every case needs: PATH (basename/mkdir/grep/sed
// live in /usr/bin:/bin on darwin — the boxes this suite runs on), HOME,
// SHELL, and A2A_INSTALL_DIR pinned to the sandbox so resolve_install_dir
// never touches /usr/local/bin or the real $HOME.
func baseEnv(sb sandbox, shell string, installDirOnPath bool) map[string]string {
	path := "/usr/bin:/bin"
	if installDirOnPath {
		path = sb.installDir + ":" + path
	}
	return map[string]string{
		"PATH":            path,
		"HOME":            sb.home,
		"SHELL":           shell,
		"A2A_INSTALL_DIR": sb.installDir,
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func requireExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected %s to NOT exist, but it does", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}

// TestSetupShell_Zsh covers AC-1002.2's zsh branch: the completion drop-in
// (proven via the stub's marker, not mere existence), the guarded rc block,
// and the zsh "fpath hint" the AC calls out by name. It also exercises "HOME
// with no rc file at all" (the .zshrc below does not exist before the run)
// and the compinit-not-yet-present half of that branch's real logic.
func TestSetupShell_Zsh(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	rcFile := filepath.Join(sb.home, ".zshrc")
	requireAbsent(t, rcFile) // no rc file at all going in — must not crash setup_shell

	env := baseEnv(sb, "/bin/zsh", false)
	res := runSetupShell(t, env)
	if res.err != nil {
		t.Fatalf("setup_shell failed: %v\nstderr:\n%s", res.err, res.stderr)
	}

	compFile := filepath.Join(sb.home, ".zsh", "completions", "_a2a")
	requireExists(t, compFile)
	if got := mustReadFile(t, compFile); !strings.Contains(got, "MARKER-ZSH") {
		t.Fatalf("completion file missing zsh marker, got:\n%s", got)
	}

	requireExists(t, rcFile)
	rc := mustReadFile(t, rcFile)
	if !strings.Contains(rc, "# >>> a2a install >>>") {
		t.Fatalf(".zshrc missing the guarded block, got:\n%s", rc)
	}
	compDir := filepath.Join(sb.home, ".zsh", "completions")
	if !strings.Contains(rc, "fpath=("+compDir+" $fpath)") {
		t.Fatalf(".zshrc missing the fpath hint naming %s, got:\n%s", compDir, rc)
	}
	if !strings.Contains(rc, "autoload -Uz compinit && compinit") {
		t.Fatalf(".zshrc should carry compinit when the rc had none, got:\n%s", rc)
	}
}

// TestSetupShell_Zsh_CompinitAlreadyPresent covers the other half of the
// "only add compinit when the rc does not already run it" branch:
// rc_needs_line must see the pre-existing "compinit" line and skip adding a
// second one, while the a2a block (fpath hint included) still gets appended
// because the marker itself was never in the seeded rc.
func TestSetupShell_Zsh_CompinitAlreadyPresent(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	rcFile := filepath.Join(sb.home, ".zshrc")
	seed := "# managed by oh-my-zsh\nautoload -Uz compinit && compinit\n"
	if err := os.WriteFile(rcFile, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed .zshrc: %v", err)
	}

	env := baseEnv(sb, "/bin/zsh", false)
	res := runSetupShell(t, env)
	if res.err != nil {
		t.Fatalf("setup_shell failed: %v\nstderr:\n%s", res.err, res.stderr)
	}

	rc := mustReadFile(t, rcFile)
	// "autoload -Uz compinit && compinit" itself contains the substring
	// "compinit" twice, so count occurrences of the whole compinit LINE
	// rather than the substring — this is what actually proves setup_shell
	// did not append a second compinit invocation on top of the seeded one.
	if got := strings.Count(rc, "autoload -Uz compinit && compinit"); got != 1 {
		t.Fatalf("expected exactly one compinit invocation line (the seeded one, none appended), got %d:\n%s", got, rc)
	}
	if !strings.Contains(rc, "# >>> a2a install >>>") {
		t.Fatalf("expected the a2a block to still be appended (marker was absent), got:\n%s", rc)
	}
	compDir := filepath.Join(sb.home, ".zsh", "completions")
	if !strings.Contains(rc, "fpath=("+compDir+" $fpath)") {
		t.Fatalf("expected the fpath hint even when compinit was pre-seeded, got:\n%s", rc)
	}
}

// TestSetupShell_Bash_InstallDirOffPath covers AC-1002.2's bash branch when
// $install_dir is NOT already on $PATH: the drop-in lands under
// ${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions/a2a and
// the `export PATH=...` block is written.
func TestSetupShell_Bash_InstallDirOffPath(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	env := baseEnv(sb, "/bin/bash", false)
	res := runSetupShell(t, env)
	if res.err != nil {
		t.Fatalf("setup_shell failed: %v\nstderr:\n%s", res.err, res.stderr)
	}

	compFile := filepath.Join(sb.home, ".local", "share", "bash-completion", "completions", "a2a")
	requireExists(t, compFile)
	if got := mustReadFile(t, compFile); !strings.Contains(got, "MARKER-BASH") {
		t.Fatalf("completion file missing bash marker, got:\n%s", got)
	}

	rcFile := filepath.Join(sb.home, ".bashrc")
	requireExists(t, rcFile)
	rc := mustReadFile(t, rcFile)
	if !strings.Contains(rc, "export PATH=\""+sb.installDir+":$PATH\"") {
		t.Fatalf(".bashrc missing the PATH export block, got:\n%s", rc)
	}
}

// TestSetupShell_Bash_InstallDirOnPath covers the same branch when
// $install_dir IS already on $PATH: the completion drop-in is still
// written (that half of the branch runs unconditionally, before the
// need_path check), but no rc block is written at all — .bashrc is never
// even created.
func TestSetupShell_Bash_InstallDirOnPath(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	env := baseEnv(sb, "/bin/bash", true)
	res := runSetupShell(t, env)
	if res.err != nil {
		t.Fatalf("setup_shell failed: %v\nstderr:\n%s", res.err, res.stderr)
	}

	compFile := filepath.Join(sb.home, ".local", "share", "bash-completion", "completions", "a2a")
	requireExists(t, compFile)
	if got := mustReadFile(t, compFile); !strings.Contains(got, "MARKER-BASH") {
		t.Fatalf("completion file missing bash marker, got:\n%s", got)
	}

	rcFile := filepath.Join(sb.home, ".bashrc")
	requireAbsent(t, rcFile)
}

// TestSetupShell_Fish covers AC-1002.2's fish branch: the completions file
// under ~/.config/fish/completions/a2a.fish, carrying the fish marker.
func TestSetupShell_Fish(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	env := baseEnv(sb, "/usr/local/bin/fish", false)
	res := runSetupShell(t, env)
	if res.err != nil {
		t.Fatalf("setup_shell failed: %v\nstderr:\n%s", res.err, res.stderr)
	}

	compFile := filepath.Join(sb.home, ".config", "fish", "completions", "a2a.fish")
	requireExists(t, compFile)
	if got := mustReadFile(t, compFile); !strings.Contains(got, "MARKER-FISH") {
		t.Fatalf("completion file missing fish marker, got:\n%s", got)
	}

	rcFile := filepath.Join(sb.home, ".config", "fish", "config.fish")
	requireExists(t, rcFile)
	rc := mustReadFile(t, rcFile)
	if !strings.Contains(rc, "fish_add_path "+sb.installDir) {
		t.Fatalf("config.fish missing fish_add_path line, got:\n%s", rc)
	}
}

// TestSetupShell_UnknownShell covers the `*)` branch: an unrecognized
// $SHELL must not be a silent no-op (it logs the completion advice line)
// and must not fail (exit 0), and it must not create any drop-in anywhere.
func TestSetupShell_UnknownShell(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	env := baseEnv(sb, "/bin/tcsh", false)
	res := runSetupShell(t, env)
	if res.err != nil {
		t.Fatalf("setup_shell should exit 0 for an unknown shell: %v\nstderr:\n%s", res.err, res.stderr)
	}

	requireAbsent(t, filepath.Join(sb.home, ".zsh"))
	requireAbsent(t, filepath.Join(sb.home, ".config", "fish"))
	requireAbsent(t, filepath.Join(sb.home, ".local", "share", "bash-completion"))
	requireAbsent(t, filepath.Join(sb.home, ".zshrc"))
	requireAbsent(t, filepath.Join(sb.home, ".bashrc"))

	if !strings.Contains(res.stderr, "run 'a2a completion <bash|zsh|fish>' to generate it") {
		t.Fatalf("expected the completion advice line on stderr, got:\n%s", res.stderr)
	}
}

// TestSetupShell_Idempotent covers AC-1002.3: running setup_shell twice
// against the same HOME must leave exactly one guarded block, and the
// second run must log that it left the file untouched.
func TestSetupShell_Idempotent(t *testing.T) {
	t.Parallel()
	sb := newSandbox(t)
	env := baseEnv(sb, "/bin/zsh", false)

	first := runSetupShell(t, env)
	if first.err != nil {
		t.Fatalf("first setup_shell run failed: %v\nstderr:\n%s", first.err, first.stderr)
	}
	second := runSetupShell(t, env)
	if second.err != nil {
		t.Fatalf("second setup_shell run failed: %v\nstderr:\n%s", second.err, second.stderr)
	}

	rcFile := filepath.Join(sb.home, ".zshrc")
	rc := mustReadFile(t, rcFile)
	if got := strings.Count(rc, "# >>> a2a install >>>"); got != 1 {
		t.Fatalf("expected exactly one guarded block after two runs, got %d:\n%s", got, rc)
	}
	if !strings.Contains(second.stderr, "already carries the a2a block — left untouched") {
		t.Fatalf("expected the second run to log the untouched notice, got:\n%s", second.stderr)
	}
}

// TestInstallLibMode_NoNetworkNoInstall covers AC-1002.1: sourcing with
// A2A_INSTALL_LIB=1 sets the A2A_INSTALL_LIB_LOADED sentinel, defines
// setup_shell, and performs no network call. The no-network claim is proven,
// not assumed: a `curl` stub sits earlier on $PATH than any real curl, and
// it writes a tripwire file before failing — if install.sh ever executed
// curl_json/download_asset (it must not, in lib mode) the tripwire would
// exist.
func TestInstallLibMode_NoNetworkNoInstall(t *testing.T) {
	t.Parallel()
	stubDir := t.TempDir()
	tripwireDir := t.TempDir()
	tripwire := filepath.Join(tripwireDir, "curl-was-called")

	curlStub := "#!/bin/sh\ntouch '" + tripwire + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(stubDir, "curl"), []byte(curlStub), 0o755); err != nil {
		t.Fatalf("write curl stub: %v", err)
	}

	env := map[string]string{
		"PATH": stubDir + ":/usr/bin:/bin",
		"HOME": t.TempDir(),
	}
	res := run(t, env, `set -eu; A2A_INSTALL_LIB=1 . "$1"; printf 'SENTINEL=%s\n' "${A2A_INSTALL_LIB_LOADED:-}"; command -v setup_shell >/dev/null 2>&1 && printf 'SETUP_SHELL_DEFINED=1\n'`)
	if res.err != nil {
		t.Fatalf("sourcing install.sh in lib mode failed: %v\nstderr:\n%s\nstdout:\n%s", res.err, res.stderr, res.stdout)
	}
	if !strings.Contains(res.stdout, "SENTINEL=1") {
		t.Fatalf("expected A2A_INSTALL_LIB_LOADED=1 sentinel, got stdout:\n%s", res.stdout)
	}
	if !strings.Contains(res.stdout, "SETUP_SHELL_DEFINED=1") {
		t.Fatalf("expected setup_shell to be a defined command, got stdout:\n%s", res.stdout)
	}

	requireAbsent(t, tripwire)
}

// TestScriptParsesUnderPOSIXShells is a cheap syntax gate: install.sh must
// stay parseable POSIX sh (`sh -n`) — no bashisms — since the installer runs
// under whatever /bin/sh the target box ships.
func TestScriptParsesUnderPOSIXShells(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sh", "-n", installScriptPath(t))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh -n scripts/install.sh: %v\n%s", err, out)
	}
}
