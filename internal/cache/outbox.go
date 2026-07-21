package cache

import (
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// ownedByMe is `a2a outbox`'s base scope (OP-208: "own open items").
func ownedByMe(fa foldedArtifact, me string) bool { return fa.Env.From == me }

// attentionReasons evaluates every one of OP-208's 4 normative
// `--attention` conditions (quoted verbatim in spec 07 §T1) against fa,
// given prior (the read cursor snapshot as of the last `a2a inbox` run —
// OP-208's own "changed since read cursor" has no cursor of its own; it
// reads the SAME one `a2a inbox` advances), now, and sla (space.yaml's
// staleness SLA, default 7 days).
func attentionReasons(fa foldedArtifact, prior cursorSnapshot, now time.Time, sla time.Duration) []string {
	var reasons []string
	state := fa.Result.State

	// 1: {folded state changed since read cursor}. An item entirely
	// absent from the prior snapshot (never seen by an inbox read) also
	// counts as changed — there is no earlier baseline to call
	// "unchanged".
	if prev, ok := prior.Items[fa.Env.ID]; !ok || prev != string(state) {
		reasons = append(reasons, "state-changed-since-cursor")
	}

	// 2: {declined}.
	if state == fold.StateDeclined {
		reasons = append(reasons, "declined")
	}

	// 3: {disputed}.
	for _, rs := range fa.Result.Responses {
		if rs == fold.StateDisputed {
			reasons = append(reasons, "disputed")
			break
		}
	}

	// 4: {stale: no event for the SLA, or needed_by passed}.
	if !fa.LatestEventAt.IsZero() && now.Sub(fa.LatestEventAt) > sla {
		reasons = append(reasons, "stale-sla")
	}
	if fa.Env.NeededBy != "" {
		if nb, err := time.Parse("2006-01-02", fa.Env.NeededBy); err == nil && now.After(nb) {
			reasons = append(reasons, "needed-by-passed")
		}
	}

	return reasons
}
