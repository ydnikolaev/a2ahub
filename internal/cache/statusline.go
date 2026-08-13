package cache

import (
	"context"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/release"
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

// StatuslineResult is the single computed statusline model. Line preserves the
// published default text contract; the structured fields power --json and
// alternate rendering without parsing that text back into facts.
type StatuslineResult struct {
	Line        string `json:"-"`
	New         int    `json:"new"`
	Urgent      int    `json:"urgent"`
	UrgentKind  string `json:"urgent_kind"`
	UrgentID    string `json:"urgent_id"`
	UrgentTitle string `json:"urgent_title"`
	Stale       int    `json:"stale"`
	Update      string `json:"update"`
	Severity    int    `json:"severity"`
	Quiet       bool   `json:"quiet"`
	Exit        int    `json:"-"`
	hasItems    bool
}

// Statusline computes §7.5's contract: cache-read only (no network, no
// live git fetch — every read here is a local working-tree/`.git`
// read), at most one line, zero-noise (empty line + exit 0 when nothing
// actionable, or when no space is connected at all — CC-092), EXCEPT that
// spec 19 T4 amends CC-092: an available update IS actionable content, so
// an otherwise-empty line prints the update segment alone (still exit 0 —
// the notice never inflates severity). When any connected space's mirror
// sync-age exceeds the Store's TTL, the CLI boundary may claim
// ClaimStatuslineRefreshLease and start the canonical `a2a sync` path. This
// method itself stays strictly cache-only and owns no background work.
func (s *Store) Statusline(ctx context.Context) (StatuslineResult, error) {
	n := s.UpdateNotice()

	if len(s.spaceMirrorsSnapshot()) == 0 {
		if n.Grade != release.GradeNone {
			return finishStatusline(StatuslineResult{Update: n.Segment}, SeverityQuiet), nil
		}
		return finishStatusline(StatuslineResult{}, SeverityQuiet), nil
	}

	idx, _, err := s.index(ctx)
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
	for spaceID, artifacts := range idx {
		manifest := s.manifestFor(spaceID)
		for _, fa := range artifacts {
			if reasons, _ := actionableReasons(fa, s.ownSystem, manifest); len(reasons) > 0 {
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
				if ReasonCode(r) == ReasonStaleSLA {
					staleCount++
					break
				}
			}
		}
	}

	if len(actionable) == 0 && staleCount == 0 {
		if n.Grade != release.GradeNone {
			return finishStatusline(StatuslineResult{Update: n.Segment}, SeverityQuiet), nil
		}
		return finishStatusline(StatuslineResult{}, SeverityQuiet), nil
	}

	newCount := 0
	var urgent *actionableEntry
	urgentCount, urgentLabel := 0, ""
	severity := SeverityItemsPending
	for i := range actionable {
		e := &actionable[i]
		if _, seen := prior.Items[e.fa.Env.ID]; !seen {
			newCount++
		}
		label := urgencyLabel(e.fa.Env.Priority, e.fa.Env.Blocking, e.reasons)
		if label != "" {
			severity = SeverityUrgent
			urgentCount++
			if urgent == nil {
				urgent, urgentLabel = e, label
			}
		}
	}

	result := StatuslineResult{
		New: newCount, Urgent: urgentCount, Stale: staleCount,
		Update: n.Segment, hasItems: len(actionable) > 0 || staleCount > 0,
	}
	if urgent != nil {
		// With more than one, the specific label would describe only the
		// named item, so the segment widens to the honest superset.
		if urgentCount > 1 {
			urgentLabel = "urgent"
		}
		result.UrgentKind = urgentLabel
		result.UrgentID = urgent.fa.Env.ID
		result.UrgentTitle = urgent.fa.Env.Title
	}
	return finishStatusline(result, severity), nil
}

// RenderStatusline renders an already-computed result. prefix controls only
// the published leading "a2a: " presentation; update-only and quiet results
// remain byte-compatible with the original contract.
func RenderStatusline(result StatuslineResult, prefix bool) string {
	if !result.hasItems {
		return result.Update
	}
	var b strings.Builder
	if prefix {
		b.WriteString("a2a: ")
	}
	fmt.Fprintf(&b, "%d new", result.New)
	if result.Urgent > 0 {
		fmt.Fprintf(&b, " · %d %s %s %q", result.Urgent, result.UrgentKind, result.UrgentID, result.UrgentTitle)
	}
	if result.Stale > 0 {
		fmt.Fprintf(&b, " · %d stale", result.Stale)
	}
	if result.Update != "" {
		fmt.Fprintf(&b, " · %s", result.Update)
	}
	return b.String()
}

func finishStatusline(result StatuslineResult, severity Severity) StatuslineResult {
	result.Severity = int(severity)
	result.Exit = int(severity)
	result.Quiet = severity == SeverityQuiet
	result.Line = RenderStatusline(result, true)
	return result
}

// SampleStatusline returns a deterministic, dependency-free result for shell
// prompt integration checks. It uses the same model and renderer as live data.
func SampleStatusline() StatuslineResult {
	return finishStatusline(StatuslineResult{
		New: 2, Urgent: 1, UrgentKind: "p1",
		UrgentID: "XQ-sample-20260728-a2a", UrgentTitle: "representative urgent item",
		Stale: 1, hasItems: true,
	}, SeverityUrgent)
}

// urgencyLabel names the SOURCE of an item's urgency, for the status line's
// urgent segment. Three independent things make an item urgent, and the
// segment has to say which one fired.
//
// An earlier version rendered all three as the literal "p1". Observed live on
// 2026-07-24 (P36's matrix): a question carrying `priority: p3, blocking:
// true` was announced as `1 p1`. A status line that states a priority the
// artifact does not carry teaches its reader to stop believing it — and the
// count was hardcoded to 1 besides, hiding every additional urgent item.
//
// Precedence is by specificity, not severity: an explicit p1 is what the
// author actually wrote, so it wins over the derived reasons.
func urgencyLabel(priority string, blocking bool, reasons []string) string {
	switch {
	case priority == "p1":
		return "p1"
	case blocking:
		return "blocking"
	}
	for _, r := range reasons {
		if ReasonCode(r) == ReasonGatePendingOnMe {
			return "gate"
		}
	}
	return ""
}

// SeverityOf grades an already-computed item list on §7.5's scale, so a
// caller that is not the statusline can answer "is there anything for me,
// and how bad is it?" without parsing output.
//
// It exists because an unattended runner had no way to ask. `a2a inbox`'s
// exit codes are 2=usage, 1=a mirror could not be read, 0=everything else —
// so "nothing to do" and "five p1 items" are the same code, and any
// scheduled agent loop has to shell out through `--json | jq length` to find
// out whether to start work at all. The severity vocabulary was already
// specified (§7.5, OP-215) and already implemented for the statusline; only
// its reach was missing.
//
// The grading rule is `urgencyLabel`'s, unchanged and un-forked — same
// function, so the statusline segment and a scheduler's branch can never
// disagree about what "urgent" means.
//
// One ceiling, stated because it is a reasonable thing to expect and it is
// deliberately absent: an item whose `needed_by` has passed grades as
// ItemsPending, not Urgent. §7.5's exit-code contract is quoted in the plan
// as "0 quiet / 10 items pending / 11 p1 or gate pending", and widening
// urgency to cover a missed deadline would change what `a2a statusline`
// returns on a corpus nobody re-verified. That is a protocol change and
// belongs in the surfaced-diff flow, not here.
func SeverityOf(items []Item) Severity {
	if len(items) == 0 {
		return SeverityQuiet
	}
	for _, it := range items {
		if urgencyLabel(it.Priority, it.Blocking, it.Reasons) != "" {
			return SeverityUrgent
		}
	}
	return SeverityItemsPending
}
