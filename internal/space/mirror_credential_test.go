package space

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// gitShimToken is the credential the argv tests authenticate with. It is
// deliberately not a plausible real token shape: if it ever escapes into a
// file, the assertion that finds it should be unambiguous about what leaked.
const gitShimToken = "TEST-MIRROR-CREDENTIAL-DO-NOT-SHIP"

// installGitArgvShim puts a fake `git` first on PATH which appends every
// invocation's argv to a log and exits 0, and returns the log's path.
//
// The shim is how these tests observe ORDER, which is the property that
// matters and the one no amount of "does the fetch work" testing can see: a
// credential placed after the subcommand still authenticates the call, still
// passes every functional test, and still writes the token into the new
// repository's config. Only the argv shows the difference.
func installGitArgvShim(t *testing.T) string {
	t.Helper()

	shimDir := t.TempDir()
	logPath := filepath.Join(shimDir, "argv.log")
	script := "#!/bin/sh\n" +
		"{ for a in \"$@\"; do printf '%s\\n' \"$a\"; done; printf -- '---\\n'; } >> \"" + logPath + "\"\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// readShimInvocations splits the shim's log into one argv slice per call.
func readShimInvocations(t *testing.T, logPath string) [][]string {
	t.Helper()

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}
	var calls [][]string
	var current []string
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "---" {
			calls = append(calls, current)
			current = nil
			continue
		}
		current = append(current, line)
	}
	return calls
}

// findInvocation returns the first recorded argv containing verb.
func findInvocation(t *testing.T, calls [][]string, verb string) []string {
	t.Helper()

	for _, argv := range calls {
		if slices.Contains(argv, verb) {
			return argv
		}
	}
	t.Fatalf("no git invocation carried %q; recorded: %v", verb, calls)
	return nil
}

// assertCredentialPrecedesSubcommand is the assertion this whole file exists
// for. See runNetworkGit's doc comment for what going the other way costs.
func assertCredentialPrecedesSubcommand(t *testing.T, argv []string, verb string) {
	t.Helper()

	headerIdx, verbIdx := -1, -1
	for i, arg := range argv {
		if headerIdx < 0 && strings.HasPrefix(arg, "http.extraheader=") {
			headerIdx = i
		}
		if verbIdx < 0 && arg == verb {
			verbIdx = i
		}
	}
	if headerIdx < 0 {
		t.Fatalf("git %s ran with no credential header: %q", verb, argv)
	}
	if verbIdx < 0 {
		t.Fatalf("git argv does not contain %q: %q", verb, argv)
	}
	if headerIdx > verbIdx {
		t.Fatalf("credential header at %d comes AFTER the %q subcommand at %d — git persists a -c "+
			"given after the subcommand into the repository's own config, leaking the token into a "+
			"machine-shared mirror: %q", headerIdx, verb, verbIdx, argv)
	}
	if argv[headerIdx-1] != "-c" {
		t.Fatalf("credential header at %d is not preceded by -c: %q", headerIdx, argv)
	}
}

func TestCloneOrFetchPutsTheCredentialBeforeTheCloneSubcommand(t *testing.T) {
	logPath := installGitArgvShim(t)
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, "https://example.invalid/o/r", host.Credential{Token: gitShimToken}); err != nil {
		t.Fatalf("CloneOrFetch: %v", err)
	}
	assertCredentialPrecedesSubcommand(t, findInvocation(t, readShimInvocations(t, logPath), "clone"), "clone")
}

func TestCloneOrFetchPutsTheCredentialBeforeTheFetchSubcommand(t *testing.T) {
	// The fetch branch needs a real repository to fetch INTO, so the real git
	// does the first clone and the shim only observes the refresh. This is
	// also the branch a fresh `a2a connect` does NOT take — the clone above is
	// — which is why both are pinned separately.
	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{}); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	logPath := installGitArgvShim(t)
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{Token: gitShimToken}); err != nil {
		t.Fatalf("CloneOrFetch (fetch path): %v", err)
	}
	assertCredentialPrecedesSubcommand(t, findInvocation(t, readShimInvocations(t, logPath), "fetch"), "fetch")
}

func TestCloneOrFetchTokenlessCredentialAddsNoAuthArgs(t *testing.T) {
	logPath := installGitArgvShim(t)
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, "https://example.invalid/o/r", host.Credential{}); err != nil {
		t.Fatalf("CloneOrFetch: %v", err)
	}
	argv := findInvocation(t, readShimInvocations(t, logPath), "clone")
	if argv[0] != "clone" {
		t.Fatalf("a tokenless clone must run a bare `git clone`, got %q — a public space is read "+
			"with no credential configured at all and nothing may be injected on its behalf", argv)
	}
}

// TestCloneOrFetchLeavesNoCredentialInTheMirrorConfig runs the REAL git over a
// real repository, because the leak this guards against is created by git
// itself, not by this package: the difference between the safe and unsafe argv
// only shows up in what git decides to persist.
//
// A mirror is machine-level shared state — mirror_root puts every project's
// clone of a space in one directory — so a token written into its config is
// readable by every project on the machine and outlives the process that put
// it there.
func TestCloneOrFetchLeavesNoCredentialInTheMirrorConfig(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{Token: gitShimToken}); err != nil {
		t.Fatalf("CloneOrFetch: %v", err)
	}
	// Fetch as well: the refresh path writes to the same config.
	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL(), host.Credential{Token: gitShimToken}); err != nil {
		t.Fatalf("CloneOrFetch (fetch path): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dest, ".git", "config"))
	if err != nil {
		t.Fatalf("read mirror config: %v", err)
	}
	config := string(raw)
	if strings.Contains(config, gitShimToken) {
		t.Fatalf("the credential was persisted into the mirror's own git config:\n%s", config)
	}
	if strings.Contains(strings.ToLower(config), "extraheader") {
		t.Fatalf("the per-invocation auth override was persisted into the mirror's git config:\n%s", config)
	}
}

// TestNetworkGitVerbsRouteThroughRunNetworkGit is the gate that keeps this
// repair from being undone by the next person to add a git call here.
//
// The defect it guards was not a wrong line of code — it was a MISSING
// parameter that nothing asked for, in a function whose local tests all
// passed because they run against local bare-repo fixtures where
// authentication is meaningless. No functional test in this package can fail
// on it. So the invariant is stated structurally instead: in this package, a
// git subcommand that contacts a remote appears only inside runNetworkGit,
// which is the one function that takes a credential.
//
// It reads the package's own source rather than a hand-kept list of call
// sites, so a new file is covered the day it is written.
func TestNetworkGitVerbsRouteThroughRunNetworkGit(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		checked++

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee, ok := call.Fun.(*ast.Ident)
			if !ok || callee.Name == "runNetworkGit" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil || !slices.Contains(networkGitVerbs, value) {
					continue
				}
				t.Errorf("%s: %s(..., %q, ...) runs a git verb that contacts a remote outside "+
					"runNetworkGit — route it through runNetworkGit so it carries a credential, or "+
					"the call silently works on public repositories and 404s on private ones",
					fset.Position(lit.Pos()), callee.Name, value)
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("the gate parsed no source files — it would pass vacuously")
	}
}
