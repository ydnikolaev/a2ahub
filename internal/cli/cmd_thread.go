// OP-210 `a2a thread` (spec 07 T1; spec 46 §T3/§T4). This file's only
// package-level symbols are ThreadCommand + NewThreadCommand plus its own
// uniquely-named, file-private helpers (thread* prefix) — no shared
// helper, no package var, per this phase's plan Placement decision.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// ThreadCommand implements `a2a thread <thread-id | artifact-id> [--space
// <id>]` (OP-210, spec 46 §T3/§T4): one causally-ordered transcript of
// every artifact AND event on a thread, in ONE space, plus an "open
// items" whose-move-is-it block — built over cache.Store.ThreadView, not
// the old flat cache.Store.Thread reader.
type ThreadCommand struct {
	store *cache.Store
}

// NewThreadCommand constructs the thread command. store must not be nil
// (rails anti-pattern #10).
func NewThreadCommand(store *cache.Store) *ThreadCommand {
	return &ThreadCommand{store: store}
}

// Name implements cli.Command.
func (c *ThreadCommand) Name() string { return "thread" }

// Synopsis implements cli.Command.
func (c *ThreadCommand) Synopsis() string {
	return "read one thread's ordered transcript and open items: thread <thread-id|artifact-id> [--space <id>] (OP-210)"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = the ref resolved
// to no thread in any searched space (cache.ErrThreadNotFound / a wrapped
// cache.ErrNotFound), the thread is ambiguous across connected spaces
// (*cache.ThreadAmbiguityError), or a connected space's mirror could not
// be read; 0 = success.
func (c *ThreadCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("thread", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output (the full ThreadResult, as-is)")
	spaceFlag := fs.String("space", "", "restrict resolution/rendering to one connected space id")
	// Wave K fix (live run 6, "thirteen verbs refuse a flag written after
	// their positional argument"): parseArgsAnyOrder (cli.go), not a bare
	// fs.Parse(args) — both `thread <id> --json` and `thread --json <id>`
	// must work.
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a thread <thread-id|artifact-id> [--space <id>] [--json]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("work-loops"))
		return 2
	}

	result, err := c.store.ThreadView(ctx, positional[0], *spaceFlag)
	if err != nil {
		var amb *cache.ThreadAmbiguityError
		if errors.As(err, &amb) {
			threadPrintAmbiguity(stdio.Stderr, amb)
			return 1
		}
		_, _ = fmt.Fprintf(stdio.Stderr, "thread: %v\n", err)
		return 1
	}

	// Defect fix (filed 2026-07-26): a malformed mirror file used to drop
	// out of the index without a word — see skipadvisory.go's own doc
	// comment. thread is scoped to ONE resolved space (result.Space), unlike
	// inbox/outbox/search above — advising about a different connected
	// space's skips here would be noise about a space this render never
	// touches.
	if skipped, skErr := c.store.SkippedFiles(ctx, result.Space); skErr == nil {
		skipAdvisory(stdio, skipped, *jsonOut)
	} else {
		// computed-not-listed-2026-08 P6 AC-8/§8 row 8 — see cmd_inbox.go's
		// own copy of this comment for the defect this closes.
		_, _ = fmt.Fprintf(stdio.Stderr, "thread: could not determine which files, if any, were skipped: %v\n", skErr)
	}

	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "thread: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}

	threadPrintText(stdio.Stdout, result)
	return 0
}

// threadPrintAmbiguity prints the T4-mandated RECOVERY block: each
// connected space the thread has members in, its member count and
// opener, then the literal rerun command for that space — built from
// amb.Thread (the RESOLVED thread id), never the caller's own ref. A
// member-artifact-id ref only resolves inside the space it lives in
// (ThreadView's resolveLoop honors --space during resolution too), so a
// rerun command built from the artifact id would fail in every space but
// the one it came from; amb.Thread is a `thread:...` value valid in all
// of them (§T4: "a weak agent must be able to copy one line and
// proceed").
func threadPrintAmbiguity(w io.Writer, amb *cache.ThreadAmbiguityError) {
	_, _ = fmt.Fprintf(w, "thread: %q is ambiguous — present in %d connected spaces:\n", amb.Thread, len(amb.Spaces))
	for _, sh := range amb.Spaces {
		_, _ = fmt.Fprintf(w, "  %s\t%d members\topener: %s (%s)\n", sh.Space, sh.MemberCount, sh.Opener.ID, sh.Opener.Title)
	}
	_, _ = fmt.Fprintln(w, "rerun with one of:")
	for _, sh := range amb.Spaces {
		_, _ = fmt.Fprintf(w, "  a2a thread %s --space %s\n", amb.Thread, sh.Space)
	}
}

// threadPrintText renders ThreadResult as tab-separated, greppable text:
// one line per transcript entry (seq, kind, actor/from, transition-or-
// type, id, and for artifacts the title), an OPEN block mirroring
// OpenItems (id, state, waiting-on, legal next moves, a YOUR MOVE marker
// when it's this system's move), an order-degradation notice when the
// space's history was unreadable (Order == cache.ThreadOrderDeclared —
// the reader fell back to declared timestamps, a weaker guarantee a
// consumer must know it holds, §T3), and a trailing
// "!! N unresolved / N flags" line when either is non-empty.
func threadPrintText(w io.Writer, result cache.ThreadResult) {
	_, _ = fmt.Fprintf(w, "thread\t%s\tspace\t%s\torder\t%s\n", result.Thread, result.Space, result.Order)
	if result.Order == cache.ThreadOrderDeclared {
		_, _ = fmt.Fprintln(w, "note: this space's commit history was unreadable — order degraded to DECLARED (envelope/event timestamps), not the committed sequence")
	}
	if result.SyncStale {
		_, _ = fmt.Fprintln(w, "note: this space's mirror is stale")
	}

	for _, te := range result.Transcript {
		switch te.Kind {
		case "artifact":
			a := te.Artifact
			_, _ = fmt.Fprintf(w, "%d\tartifact\t%s\t%s\t%s\t%s\n", te.Seq, a.From, a.Type, a.ID, a.Title)
		case "event":
			e := te.Event
			_, _ = fmt.Fprintf(w, "%d\tevent\t%s\t%s\t%s\n", te.Seq, e.Actor.System, e.Transition, e.Subject)
		}
	}

	_, _ = fmt.Fprintln(w, "OPEN")
	for _, oi := range result.OpenItems {
		var moves []string
		for _, na := range oi.NextActions {
			moves = append(moves, threadInvocation(oi.Type, na.Transition)+":"+strings.Join(na.By, ","))
		}
		marker := ""
		if oi.YourMove {
			marker = "<< YOUR MOVE"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\twaiting_on=%s\tnext=%s\t%s\n",
			oi.ID, oi.State, strings.Join(oi.WaitingOn, ","), strings.Join(moves, ";"), marker)
	}

	if len(result.Unresolved) > 0 || len(result.Flags) > 0 {
		_, _ = fmt.Fprintf(w, "!! %d unresolved / %d flags\n", len(result.Unresolved), len(result.Flags))
	}
}

// threadInvocation translates protocol transition vocabulary into an actual
// CLI invocation. JSON deliberately keeps the protocol word; only the text
// surface promises a copyable `next=` command.
func threadInvocation(artifactType, transition string) string {
	if transition == "acknowledge" {
		return "ack"
	}
	if artifactType == "contract" {
		switch transition {
		case "publish", "deprecate", "retire":
			return "contract " + transition
		}
	}
	return transition
}

var _ Command = (*ThreadCommand)(nil)
