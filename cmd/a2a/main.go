// Command a2a is the single a2ahub binary: CLI + (later) local MCP server +
// validator + statusline provider + local HTML generator (§7.1, D-005,
// R-004). This phase stands up only the subcommand-dispatch skeleton and a
// build-time version stamp; no OP-2xx verb has real behavior yet.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
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
	cmd, ok := buildCommands()[args[0]]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	return cmd(args[1:], stdout, stderr)
}

func printUsage(w io.Writer) {
	// Writes to the caller-supplied stderr/stdout are best-effort for a
	// CLI: a broken pipe here is not actionable and not worth propagating
	// through every dispatch-table command's signature.
	_, _ = fmt.Fprintln(w, "usage: a2a <command> [args...]")
	_, _ = fmt.Fprintln(w, "commands:")
	_, _ = fmt.Fprintln(w, "  init        set up project config (.a2a/config.yaml)")
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
	_, _ = fmt.Fprintln(w, "  ack, accept, decline, respond, verify, ... lifecycle verbs")
	_, _ = fmt.Fprintln(w, "  contract    contract lifecycle (new/publish/deprecate/retire/diff/verify-export)")
	_, _ = fmt.Fprintln(w, "  doctor      diagnose config / space / credentials")
	_, _ = fmt.Fprintln(w, "  version     print the binary version stamp")
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
