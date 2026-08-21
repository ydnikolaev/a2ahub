// Command a2a is the single a2ahub binary: CLI + (later) local MCP server +
// validator + statusline provider + local HTML generator (§7.1, D-005,
// R-004). This phase stands up only the subcommand-dispatch skeleton and a
// build-time version stamp; no OP-2xx verb has real behavior yet.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// version and commit are the build-time stamp (§7.3, D-013 — the
// min_binary_version pin checks compare against this). Set via:
//
//	-ldflags "-X main.version=vX.Y.Z -X main.commit=<sha>"
//
// Both carry non-empty defaults so `a2a version` never prints an empty
// stamp under a plain `go run`/`go build` with no ldflags.
var (
	version = "dev"
	commit  = ""
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// command is one dispatch-table entry. args excludes the command name.
type command func(args []string, stdout, stderr io.Writer) int

// run implements the full CLI surface (T1): no subcommand -> usage to
// stderr, exit 2; a registered subcommand -> that command's exit code; an
// unrecognized subcommand -> "unknown command "<x>"" to stderr, exit 2.
// The OP-2xx verbs are built lazily (wire.go) so a bare `a2a version` never
// requires a config file on disk.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	// `--version` / `-v` are the shape every CLI a consumer has ever used
	// answers, and answering "unknown command" to them is a needless
	// papercut — the more so for the one question this binary exists to make
	// easy to ask. They are ALIASES, not a second implementation: the same
	// dispatch entry runs, so the two spellings cannot drift.
	name := args[0]
	if name == "--version" || name == "-v" {
		name = "version"
		args = append([]string{"version"}, args[1:]...)
	}
	cmd, ok := buildCommands()[name]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", name)
		return 2
	}
	// `a2a <verb> --help` is answered WITHOUT resolving the verb's
	// dependencies: help must never require a project config, a mirror, or a
	// credential — see help.go.
	if helpRequested(args[1:]) {
		return runVerbHelp(name, args[1:], stdout)
	}
	code := cmd(args[1:], stdout, stderr)
	writeUpdateAdvisory(name, args[1:], stderr)
	return code
}

// advisorySilentVerbs are the commands that must NOT get the generic advisory
// appended, each for its own reason rather than by taste:
//
//	version     IS the report — a note repeating it would be noise
//	update      IS the fix, and prints its own richer before/after
//	doctor      already has a `versions` check that reports the same fact as a
//	            finding, with the exact "v0.1.2 -> v0.3.0" prose (spec 19 AC#7)
//	statusline  is one embedded line by contract; it already folds the notice
//	            in, and a second line would break every prompt embedding it
//	completion  emits a shell script to stdout that a shell will `eval`
var advisorySilentVerbs = map[string]bool{
	"version": true, "update": true, "doctor": true, "statusline": true, "completion": true,
}

// writeUpdateAdvisory renders the update notice ONCE, after any command, for
// every verb that does not already own the subject.
//
// This is the fix for the shape that let an agent write from a stale binary:
// the advisory used to live in three hand-placed copies, so whether you were
// told depended on which verb you happened to run. Placing it here means a new
// verb cannot be added without it.
//
// It builds the store best-effort and stays silent when there is no project —
// no project means no configured cache to read, and inventing a warning there
// would be a claim about a machine we know nothing about.
//
// The `--json` scan is deliberate and bounded: the flag is spelled the same by
// every verb in this CLI that has one, and a false positive can only change
// the ADVISORY's format on stderr — never the command's behaviour, never its
// exit code, and never a byte of stdout.
func writeUpdateAdvisory(name string, args []string, stderr io.Writer) {
	render, jsonOut := advisoryMode(name, args)
	if !render {
		return
	}
	p, err := resolvePaths()
	if err != nil {
		return
	}
	store, err := buildStore(context.Background(), p)
	if err != nil {
		return
	}
	cli.WriteUpdateAdvisory(cli.IO{Stderr: stderr}, store.UpdateNotice(), jsonOut)
}

// advisoryMode is the whole DECISION, separated from the store read so it can
// be tested without a project on disk. It answers two questions: does this verb
// get the advisory at all, and in which shape.
func advisoryMode(name string, args []string) (render, jsonOut bool) {
	if advisorySilentVerbs[name] {
		return false, false
	}
	for _, a := range args {
		if a == "--json" || a == "-json" || a == "--json=true" || a == "-json=true" {
			return true, true
		}
	}
	return true, false
}

func printUsage(w io.Writer) {
	// Writes to the caller-supplied stderr/stdout are best-effort for a
	// CLI: a broken pipe here is not actionable and not worth propagating
	// through every dispatch-table command's signature.
	_, _ = fmt.Fprintln(w, "usage: a2a <command> [args...]")
	_, _ = fmt.Fprintln(w, "commands:")
	_, _ = fmt.Fprintln(w, "  init        set up project: config + skill install + AGENTS.md pointer")
	_, _ = fmt.Fprintln(w, "  connect     register + mirror-clone a space")
	_, _ = fmt.Fprintln(w, "  disconnect  remove a connected space")
	_, _ = fmt.Fprintln(w, "  new         draft an artifact from a template")
	_, _ = fmt.Fprintln(w, "  template    list / show canonical templates")
	_, _ = fmt.Fprintln(w, "  validate    validate a draft (V1/V2)")
	_, _ = fmt.Fprintln(w, "  submit      validate + open a PR for a draft")
	_, _ = fmt.Fprintln(w, "  sync        fetch all connected spaces")
	_, _ = fmt.Fprintln(w, "  inbox       computed inbox across connected spaces")
	_, _ = fmt.Fprintln(w, "  outbox      your own open items")
	_, _ = fmt.Fprintln(w, "  show        an artifact + folded state + events")
	_, _ = fmt.Fprintln(w, "  thread      a conversation view")
	_, _ = fmt.Fprintln(w, "  search      search the local cache")
	_, _ = fmt.Fprintln(w, "  contracts   list known contracts")
	_, _ = fmt.Fprintln(w, "  statusline  one-line status (for embedding)")
	_, _ = fmt.Fprintln(w, "  html        render a self-contained local dashboard (alias: dashboard)")
	_, _ = fmt.Fprintln(w, "  serve       serve the operational dashboard and snapshot API on loopback")
	_, _ = fmt.Fprintln(w, "  ack, accept, decline, respond, verify, ... lifecycle verbs")
	_, _ = fmt.Fprintln(w, "  contract    contract lifecycle (new/publish/deprecate/retire/diff/verify-export)")
	_, _ = fmt.Fprintln(w, "  work        report semantic work and renew/inspect the local lease")
	_, _ = fmt.Fprintln(w, "  feedback    file feedback on a2a itself (new/validate/submit/status/triage)")
	_, _ = fmt.Fprintln(w, "  doctor      diagnose config / space / credentials")
	_, _ = fmt.Fprintln(w, "  update      self-update to the latest release (--check/--yes/--allow-unsigned)")
	_, _ = fmt.Fprintln(w, "  skill       install the a2ahub expert-skill tree into this repo")
	_, _ = fmt.Fprintln(w, "  completion  print a shell completion script (bash|zsh|fish)")
	_, _ = fmt.Fprintln(w, "  notifications install and control macOS / VS Code notification surfaces")
	_, _ = fmt.Fprintln(w, "  version     binary, release and per-space versions (alias: --version, -v)")
	_, _ = fmt.Fprintln(w, "docs: https://a2ahub.dev/")
}

func runVersion(_ []string, stdout, _ io.Writer) int {
	_, _ = fmt.Fprintln(stdout, versionStamp())
	return 0
}

// versionStamp returns the one-line "a2a <version> (<commit>)" stamp.
// commit prefers the ldflags-injected value; absent that, it falls back to
// the VCS revision Go's toolchain embeds automatically (framework-first —
// no shelling out to `git`), and finally "unknown" if neither is present.
func versionStamp() string {
	sha := commit
	if sha == "" {
		sha = vcsRevision()
	}
	if sha == "" {
		sha = "unknown"
	}
	return fmt.Sprintf("a2a %s (%s)", version, sha)
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}
