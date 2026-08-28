package notes

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

// Group orders a PendingItem the way a reader can act alone (P13 spec
// "The order, decided rather than inherited" — a reader acts on the first
// thing it can, so every group sorts strictly above every group after it):
// a local change with a runnable command first, real cross-party work
// third, a standing limitation that obliges something last. Lower sorts
// first. This package computes Group; the WITHIN-group tie-break reuses
// cmd_whatsnew.go's existing whatsnewImpactOrder (internal/cli) rather than
// duplicating it here — this package is imported BY internal/cli, so the
// final sort by (Group, impact) happens at that call site, not in Pending.
type Group int

const (
	// GroupLocalWithRun: scope local, carrying a Run command — a command
	// exists, so it is the cheapest true progress.
	GroupLocalWithRun Group = iota
	// GroupLocalOther: scope local with no Run (a Detect, or prose only) —
	// still unilateral: the agent owns its own repository.
	GroupLocalOther
	// GroupSpace: scope space — real, but may need another party's write
	// access or coordination, so it never outranks unilateral work.
	GroupSpace
	// GroupKnownIssue: a Kind == KindKnownIssue change whose OWN scope is
	// not "none" — a limitation that obliges something is an obligation,
	// but it is a known-issue FIRST: kind decides the group, never the
	// scope-derived group a plain change of the same scope would get. The
	// live corpus carries none (both standing issues are scope: none);
	// this branch is exercised only by fixtures, deliberately, so a future
	// standing issue that DOES oblige something is not silently dropped.
	GroupKnownIssue
)

// PendingItem is one obligation `a2a adapt` surfaces: a corpus Change, the
// release version that introduced it, and the Group Pending assigned it.
type PendingItem struct {
	Version string
	Change  Change
	Group   Group
}

// ErrBaselineAheadOfBinary is returned by ValidateBaseline/Pending when the
// repository's recorded adapted_through names a version strictly NEWER
// than the running binary — a downgrade. Adaptation never walks backwards
// (P13 spec §"the config field", AC-10): the caller must refuse and name
// both versions rather than guess which one is wrong.
var ErrBaselineAheadOfBinary = errors.New("notes: adapted_through baseline is newer than the running binary")

// ErrBinaryVersionUnusable is returned by Pending when binaryVersion is not a
// comparable version — a `dev` build, an empty string, anything parseVersion
// refuses.
//
// It exists because the alternative is a SILENT YES, and this repository has
// the receipts. Since() bounds its walk with
// `if err != nil || tooNew { continue }` (notes.go:193): when the upper bound
// cannot be parsed, OlderThan returns an ERROR and the release is skipped —
// every release, for the same reason — and the caller receives an empty slice
// that is indistinguishable from "you are already up to date". Driven through
// the real binary, `a2a adapt` on a `dev` build therefore printed "nothing to
// adapt" and exited 0, which is a claim about the repository that nothing had
// measured.
//
// D9's rule (internal/validate/result.go:72, "UNMEASURED is a SEVERITY, not a
// fourth verdict") decides the shape: adapt does not gain a fourth outcome, it
// REFUSES, and the refusal names both clocks. Since() itself is deliberately
// left alone — `whatsnew` is a browse surface where an empty answer costs
// nothing, while adapt is an OBLIGATION surface where it costs everything.
var ErrBinaryVersionUnusable = errors.New("notes: the running binary's version is not comparable, so the obligation range cannot be computed")

// ValidateBaseline refuses a baseline strictly newer than binaryVersion. An
// empty baseline ("never adapted") always validates — Pending starts that
// walk at the oldest embedded note instead.
func ValidateBaseline(baseline, binaryVersion string) error {
	const op = "ValidateBaseline"
	if baseline == "" {
		return nil
	}
	input := fmt.Sprintf("baseline=%s binary=%s", baseline, binaryVersion)
	ahead, err := version.OlderThan(binaryVersion, baseline)
	if err != nil {
		return &Error{Op: op, Input: input, Err: err}
	}
	if ahead {
		return &Error{Op: op, Input: input, Err: ErrBaselineAheadOfBinary}
	}
	return nil
}

// Projection is a2a adapt's whole computed answer: the flat, filtered,
// group-tagged obligation set plus the two clocks that produced it (P13
// spec §"the two clocks" — Baseline is the REPOSITORY's own clock,
// BinaryVersion is the running binary's, and they are allowed to differ).
type Projection struct {
	// Baseline is the adapted_through this walk started AFTER (exclusive),
	// or "" when the repository has never recorded one.
	Baseline string
	// BinaryVersion bounds the walk (inclusive) — the running binary never
	// shows notes for a version newer than itself.
	BinaryVersion string
	// Releases is the count of releases strictly between Baseline
	// (exclusive) and BinaryVersion (inclusive), counted BEFORE standing
	// known issues are attached — attaching can synthesize a carrier entry
	// for an otherwise-empty range, which must never inflate this count.
	Releases int
	// StartedFromOldest is true when Baseline was "" — the walk started at
	// Oldest instead of at any recorded baseline, and the caller must SAY
	// SO (P13 spec §"the config field": "absent = never adapted... says
	// so").
	StartedFromOldest bool
	// Oldest is the version of the oldest embedded release note, set only
	// when StartedFromOldest.
	Oldest string
	// Items is the filtered, group-tagged obligation set, in encounter
	// order (ascending release order, then each release's own Changes
	// order) — NOT YET sorted by Group/impact; see Group's own doc.
	Items []PendingItem
}

// pendingGroup reports the Group a Change belongs in and whether it
// belongs in the projection at all. This is NOT a classifier (P13 brief:
// "do not build a classifier") — every branch reads a field the schema
// already requires (Kind, Action.Scope, Action.Run); it decides ORDER
// only, never whether the corpus meant something to be actionable. scope:
// "none" (or, for anything that is not a known-issue, any Action.Scope
// value that is not exactly "local" or "space" — including an entirely
// missing action block, whose Scope decodes to "") is a POSITIVE
// exclusion, not a default inclusion: a change that declares nothing is
// never treated as an obligation.
func pendingGroup(ch Change) (Group, bool) {
	if ch.Kind == KindKnownIssue {
		if ch.Action.Scope == "none" {
			return 0, false
		}
		return GroupKnownIssue, true
	}
	switch ch.Action.Scope {
	case "local":
		if len(ch.Action.Run) > 0 {
			return GroupLocalWithRun, true
		}
		return GroupLocalOther, true
	case "space":
		return GroupSpace, true
	default:
		return 0, false
	}
}

// Pending computes the Projection for the range (baseline, binaryVersion]
// over all (Load's own ascending-version postcondition) and currentIssues
// (LoadCurrentKnownIssues) — the EXISTING primitives this phase reuses
// rather than rebuilds (P13 spec §5). baseline == "" means "never
// adapted": the walk starts at the oldest embedded note and
// Projection.StartedFromOldest reports that.
func Pending(all []ReleaseNotes, currentIssues []Change, baseline, binaryVersion string) (Projection, error) {
	// The upper bound is checked BEFORE the baseline, because an unusable one
	// makes every later answer meaningless rather than merely wrong — see
	// ErrBinaryVersionUnusable for the measurement that earned this guard.
	if _, err := version.OlderThan(binaryVersion, "0.0.0"); err != nil {
		return Projection{}, &Error{
			Op:    "Pending",
			Input: fmt.Sprintf("baseline=%s binary=%s", baseline, binaryVersion),
			Err:   ErrBinaryVersionUnusable,
		}
	}
	if err := ValidateBaseline(baseline, binaryVersion); err != nil {
		return Projection{}, err
	}

	proj := Projection{Baseline: baseline, BinaryVersion: binaryVersion}
	if baseline == "" && len(all) > 0 {
		proj.StartedFromOldest = true
		proj.Oldest = all[0].Version
	}

	slice := Since(all, baseline, binaryVersion)
	proj.Releases = len(slice)
	slice = AttachCurrentKnownIssues(slice, all, currentIssues)

	for _, rn := range slice {
		for _, ch := range rn.Changes {
			group, ok := pendingGroup(ch)
			if !ok {
				continue
			}
			proj.Items = append(proj.Items, PendingItem{Version: rn.Version, Change: ch, Group: group})
		}
	}
	return proj, nil
}

// DetectRunner executes one detect: command (a shell one-liner, as
// authored — release notes were never asked to write argv-safe strings)
// and reports whether the obligation it names STILL FIRES: fired == true
// means the command ran and exited non-zero, i.e. the condition described
// is still present. A non-nil err means the command could not be RUN at
// all (e.g. no shell) and must never be read as "still fires" — the two
// are different facts (gate-lib.sh's gate_fail/gate_unmeasured split,
// mirrored here for the identical reason: "I could not measure this" must
// never borrow "I measured it and it's wrong"'s message).
type DetectRunner func(ctx context.Context, command string) (fired bool, err error)

// DefaultDetectRunner runs command through the platform shell.
func DefaultDetectRunner(ctx context.Context, command string) (bool, error) {
	err := exec.CommandContext(ctx, "sh", "-c", command).Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return true, nil
	}
	return false, err
}

// DetectResult names the first PendingItem whose detect: either still
// fires or could not be run.
type DetectResult struct {
	Item    PendingItem
	Command string
	// Fired is true when the command ran and exited non-zero. Mutually
	// exclusive with Err being non-nil — see DetectRunner's own doc.
	Fired bool
	Err   error
}

// CheckDetects runs every Detect command across items, in encounter order,
// stopping at the first one that either still fires or could not be run —
// `a2a adapt --done` refuses NAMING the change (P13 AC-7), so there is no
// reason to keep exec'ing once the answer is already "not clean". Returns
// nil when every detect: across every item ran and exited zero.
func CheckDetects(ctx context.Context, items []PendingItem, run DetectRunner) *DetectResult {
	if run == nil {
		run = DefaultDetectRunner
	}
	for _, item := range items {
		for _, cmd := range item.Change.Action.Detect {
			fired, err := run(ctx, cmd)
			if err != nil {
				return &DetectResult{Item: item, Command: cmd, Err: err}
			}
			if fired {
				return &DetectResult{Item: item, Command: cmd, Fired: true}
			}
		}
	}
	return nil
}
