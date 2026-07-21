package cache

import (
	"context"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// Severity is `a2a statusline`'s exit-code contract (§7.5, quoted
// verbatim): "Exit code communicates severity (0 quiet / 10 items
// pending / 11 p1 or gate pending) so harnesses can style without
// parsing."
type Severity int

// Severity constants map to §7.5's exit codes.
const (
	SeverityQuiet        Severity = 0
	SeverityItemsPending Severity = 10
	SeverityUrgent       Severity = 11
)

// StatuslineResult is `a2a statusline`'s render output: at most one
// line, and the exit code the caller returns from Run.
type StatuslineResult struct {
	Line string
	Exit int
}

// Statusline computes §7.5's contract: cache-read only (no network, no
// live git fetch — every read here is a local working-tree/`.git`
// read), at most one line, zero-noise (empty line + exit 0 when nothing
// actionable, or when no space is connected at all — CC-092). When any
// connected space's mirror sync-age exceeds the Store's TTL, it ALSO
// spawns exactly one detached, recover-guarded background refresh (git
// fetch, v1-min's git-fallback path, D-030 — never a hub client symbol)
// whose result lands in the mirror for the NEXT render; this call never
// waits on that goroutine, so the <100ms render budget is unaffected by
// however long the refresh itself takes.
func (s *Store) Statusline(ctx context.Context) (StatuslineResult, error) {
	if len(s.spaces) == 0 {
		return StatuslineResult{Exit: int(SeverityQuiet)}, nil
	}

	idx, err := s.index(ctx)
	if err != nil {
		return StatuslineResult{}, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return StatuslineResult{}, err
	}

	type actionableEntry struct {
		fa      foldedArtifact
		reasons []string
	}
	var actionable []actionableEntry
	for _, artifacts := range idx {
		for _, fa := range artifacts {
			if reasons := actionableReasons(fa, s.ownSystem); len(reasons) > 0 {
				actionable = append(actionable, actionableEntry{fa: fa, reasons: reasons})
			}
		}
	}

	staleCount := 0
	for spaceID, artifacts := range idx {
		sla := s.slaFor(spaceID)
		for _, fa := range artifacts {
			if !ownedByMe(fa, s.ownSystem) {
				continue
			}
			for _, r := range attentionReasons(fa, prior, s.now(), sla) {
				if r == "stale-sla" {
					staleCount++
					break
				}
			}
		}
	}

	s.triggerRefreshIfStale(ctx, idx)

	if len(actionable) == 0 && staleCount == 0 {
		return StatuslineResult{Exit: int(SeverityQuiet)}, nil
	}

	newCount := 0
	var urgent *actionableEntry
	severity := SeverityItemsPending
	for i := range actionable {
		e := &actionable[i]
		if _, seen := prior.Items[e.fa.Env.ID]; !seen {
			newCount++
		}
		urgentNow := e.fa.Env.Priority == "p1" || e.fa.Env.Blocking
		for _, r := range e.reasons {
			if r == "gate-pending-on-me" {
				urgentNow = true
			}
		}
		if urgentNow {
			severity = SeverityUrgent
			if urgent == nil {
				urgent = e
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "a2a: %d new", newCount)
	if urgent != nil {
		fmt.Fprintf(&b, " · 1 p1 %s %q", urgent.fa.Env.ID, urgent.fa.Env.Title)
	}
	if staleCount > 0 {
		fmt.Fprintf(&b, " · %d stale", staleCount)
	}

	return StatuslineResult{Line: b.String(), Exit: int(severity)}, nil
}

// triggerRefreshIfStale spawns exactly ONE detached, recover-guarded
// goroutine that fetches every connected space whose mirror sync-age
// exceeds the TTL — "owned goroutine + recover per rails" (this call
// never breaks the caller's prompt, and never waits for the goroutine).
//
// The goroutine deliberately does NOT inherit the caller's ctx: a
// "detached" background refresh is meant to keep running after
// Statusline itself has already returned to a prompt that may cancel
// its own request-scoped context immediately afterward — inheriting it
// would silently truncate the very refresh this exists to perform.
// context.Background() is the correct escape hatch here (recover still
// bounds worst-case runaway behavior).
func (s *Store) triggerRefreshIfStale(_ context.Context, idx map[string][]foldedArtifact) {
	_ = idx
	var stale []SpaceMirror
	for _, sm := range s.spaces {
		age, synced := mirrorSyncAge(s.now(), sm.Dir)
		if !synced || age > s.ttl {
			stale = append(stale, sm)
		}
	}
	if len(stale) == 0 {
		return
	}
	go func() {
		defer func() { _ = recover() }() // rails: the refresh goroutine must never panic into the caller's prompt
		for _, sm := range stale {
			_ = space.CloneOrFetch(context.Background(), sm.Dir, sm.RepoURL)
		}
	}()
}
