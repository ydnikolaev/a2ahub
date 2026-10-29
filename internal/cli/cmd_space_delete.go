// `a2a space delete --local` (fast-loop-2026-10 spec 03 §T4). This file's only
// package-level symbols are file-private, uniquely-named helpers (spaceDelete*
// prefix) plus the one consumer-side seam the verb needs; the verb itself is a
// SpaceCommand method dispatched from cmd_space.go's switch.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// spaceDisconnector is `space delete`'s consumer-side seam over the caller's
// project connection (rails ISP/DI): *DisconnectCommand satisfies it, so the
// removal that `a2a disconnect` runs is the one `delete --approve` runs, not a
// copy of it.
type spaceDisconnector interface {
	// ConnectedRef is this project's configured ref for target, changing nothing.
	ConnectedRef(target string) (space.Ref, bool)
	// RemoveConnected drops target's config entry, mirror and cache; found is
	// false when target is not connected.
	RemoveConnected(ctx context.Context, target string, stdio IO) (ref space.Ref, found bool, code int)
}

const spaceDeleteUsage = "usage: a2a space delete --local <space-id> [--dir <path>] [--approve]"

// runDelete implements `a2a space delete --local <space-id> [--dir <path>]
// [--approve]`. Exit codes: 2 = usage error; 1 = refused (including the
// no---approve answer, which changes nothing) or a failed removal; 0 = deleted,
// or already deleted.
func (c *SpaceCommand) runDelete(ctx context.Context, args []string, stdio IO) int {
	fset := flag.NewFlagSet("space delete", flag.ContinueOnError)
	fset.SetOutput(stdio.Stderr)
	localFlag := fset.Bool("local", false, "delete a local space (the only kind this verb deletes)")
	dirFlag := fset.String("dir", "", "the local space's bare repository (default: this project's connection to <space-id>, else <data-root>/spaces/<space-id>.git)")
	approve := fset.Bool("approve", false, "remove the repository (without it nothing is changed and the facts are listed)")

	// The space id may sit anywhere among the flags (`--local <id> --approve`
	// is the documented shape), and flag.Parse stops at the first positional,
	// so parse again after taking one.
	var spaceID string
	rest := args
	for {
		if err := fset.Parse(rest); err != nil {
			return 2
		}
		if fset.NArg() == 0 {
			break
		}
		if spaceID != "" {
			_, _ = fmt.Fprintln(stdio.Stderr, spaceDeleteUsage)
			return 2
		}
		spaceID, rest = fset.Arg(0), fset.Args()[1:]
	}
	if spaceID == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, spaceDeleteUsage)
		return 2
	}
	if !*localFlag {
		_, _ = fmt.Fprintln(stdio.Stderr, "space delete: only a local space can be deleted — a GitHub space is deleted on GitHub; pass --local")
		_, _ = fmt.Fprintln(stdio.Stderr, spaceDeleteUsage)
		return 2
	}
	if c.Open == nil || c.Disconnect == nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "space delete: not wired (no host opener or project connection) — this build cannot verify a local space or disconnect it")
		return 1
	}
	attempted := "space delete --local " + spaceID

	connected, hasRef := c.Disconnect.ConnectedRef(spaceID)
	target := *dirFlag
	switch {
	case target != "":
	case hasRef:
		if !spaceDeleteIsPath(connected.RepoURL) {
			return spaceRefuse(stdio,
				attempted+": find the local space",
				fmt.Sprintf("this project's %q is connected to %s, which is not a space on this machine", spaceID, connected.RepoURL),
				"a GitHub space is deleted on GitHub; to stop using it here run `a2a disconnect "+spaceID+"` (or pass --dir <path> for a local space of the same id)")
		}
		target = connected.RepoURL
	default:
		if c.DataRoot == "" {
			_, _ = fmt.Fprintln(stdio.Stderr, "space delete: not wired (no data root) — pass --dir <path>")
			return 1
		}
		target = filepath.Join(c.DataRoot, "spaces", spaceID+".git")
	}
	abs, err := initCanonicalPath(target)
	if err != nil {
		return spaceRefuse(stdio,
			attempted+": resolve the repository path",
			fmt.Sprintf("cannot resolve %s: %v", target, err),
			"pass --dir with the path `a2a space init --local` printed, then re-run `a2a "+attempted+" --dir <path>`")
	}
	// The caller's connection is disconnected only when it is a connection to
	// THIS repository: a --dir that deletes one space must not drop another
	// space's entry that happens to share the id.
	ownRef := hasRef && initLocalLocator(connected.RepoURL) == abs

	if _, statErr := os.Stat(abs); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			return spaceRefuse(stdio,
				attempted+": inspect "+abs,
				statErr.Error(),
				"make "+abs+" readable, or pass the right --dir, then re-run `a2a "+attempted+"`")
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "space delete: space %q already deleted (no repository at %s)\n", spaceID, abs)
		return c.spaceDeleteDisconnect(ctx, spaceID, ownRef, stdio)
	}

	facts, code := c.spaceDeleteVerify(ctx, spaceID, abs, stdio)
	if code != 0 {
		return code
	}

	if !*approve {
		found := fmt.Sprintf("the bare repository %s holds %s on main", abs, spaceDeleteCommitsText(facts.commits))
		next := "to delete it run `a2a " + attempted + " --approve`"
		if len(facts.others) > 0 {
			found += "; other participants that are not marked as left: " + strings.Join(facts.others, ", ")
			next += ", then run `a2a disconnect " + spaceID + "` in the repository of each of them"
		}
		return spaceRefuse(stdio, attempted+": remove the local space (nothing was changed)", found, next)
	}

	if err := os.RemoveAll(abs); err != nil {
		return spaceRefuse(stdio,
			attempted+": remove "+abs,
			err.Error(),
			"fix the cause named above (is the directory writable?), then re-run `a2a "+attempted+" --approve` — it is safe to repeat")
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "space delete: removed the local space %q at %s (%s on main)\n", spaceID, abs, spaceDeleteCommitsText(facts.commits))
	if code := c.spaceDeleteDisconnect(ctx, spaceID, ownRef, stdio); code != 0 {
		return code
	}
	for _, system := range facts.others {
		_, _ = fmt.Fprintf(stdio.Stdout, "space delete: next: in %s's repository run `a2a disconnect %s`\n", system, spaceID)
	}
	return 0
}

// spaceDeleteDisconnect runs the caller's disconnect for the space when its
// project holds a connection to the repository just removed (or already
// absent), through the same removal `a2a disconnect` runs.
func (c *SpaceCommand) spaceDeleteDisconnect(ctx context.Context, spaceID string, ownRef bool, stdio IO) int {
	if !ownRef {
		return 0
	}
	_, found, code := c.Disconnect.RemoveConnected(ctx, spaceID, stdio)
	switch {
	case code != 0:
		return code
	case found:
		_, _ = fmt.Fprintf(stdio.Stdout, "space delete: disconnected %q from this project (config entry + mirror)\n", spaceID)
	}
	return 0
}

// spaceDeleteFacts is what the operator is shown before anything is removed.
type spaceDeleteFacts struct {
	commits int
	others  []string // systems other than the caller's whose status is not left
}

// spaceDeleteVerify proves the directory is a local space a2a made, for spaceID,
// before it can be removed, and reads the facts. Each failure is a three-part
// refusal (code 1). The order is the spec's: the repository, the marker, the
// manifest.
func (c *SpaceCommand) spaceDeleteVerify(ctx context.Context, spaceID, abs string, stdio IO) (spaceDeleteFacts, int) {
	attempted := "space delete --local " + spaceID
	state, err := space.ReadLocalSpaceRepo(ctx, abs)
	if err != nil {
		return spaceDeleteFacts{}, spaceRefuse(stdio,
			attempted+": inspect "+abs, err.Error(),
			"pass the path `a2a space init --local` printed with --dir, then re-run `a2a "+attempted+"`")
	}
	if !state.IsBareRepo {
		return spaceDeleteFacts{}, spaceRefuse(stdio,
			attempted+": verify "+abs,
			abs+" is not a bare git repository",
			"nothing was touched; pass the local space's own path with --dir (`a2a space init --local` printed it)")
	}
	marked := false
	if h, _, openErr := c.Open(abs); openErr == nil && h != nil {
		marked = h.Capabilities().OnThisMachine
	}
	if !marked {
		return spaceDeleteFacts{}, spaceRefuse(stdio,
			attempted+": verify "+abs,
			abs+" is a bare repository that a2a did not create as a local space (it has no a2a.host=local marker)",
			"nothing was touched; a2a deletes only a space `a2a space init --local` made — remove "+abs+" yourself if you are sure it is unwanted")
	}
	switch {
	case state.SpaceID == "":
		return spaceDeleteFacts{}, spaceRefuse(stdio,
			attempted+": verify "+abs,
			abs+" has no main branch with a readable space.yaml",
			"nothing was touched; pass the local space's own path with --dir")
	case state.SpaceID != spaceID:
		return spaceDeleteFacts{}, spaceRefuse(stdio,
			attempted+": verify "+abs,
			fmt.Sprintf("the space.yaml on main of %s names the space %q, not %q", abs, state.SpaceID, spaceID),
			"nothing was touched; run `a2a space delete --local "+state.SpaceID+"` for that space, or pass the right --dir")
	}

	facts := spaceDeleteFacts{commits: state.Commits}
	for _, p := range state.Manifest.Participants {
		if p.System == c.OwnSystem || p.Status == fold.MembershipLeft {
			continue
		}
		facts.others = append(facts.others, p.System)
	}
	sort.Strings(facts.others)
	return facts, 0
}

// spaceDeleteCommitsText is "1 commit" or "N commits".
func spaceDeleteCommitsText(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return strconv.Itoa(n) + " commits"
}

// spaceDeleteIsPath reports whether a configured locator names a directory on
// this machine rather than a URL or an scp-style address.
func spaceDeleteIsPath(locator string) bool {
	return filepath.IsAbs(locator) && !strings.Contains(locator, "://")
}
