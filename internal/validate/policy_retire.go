// This file adds ONE named policy-class check to internal/validate (plan
// 08 Placement decisions, binding): the retire-precondition check §5.4/
// D-022 requires before `contract retire` may proceed. It is a pure
// function of caller-resolved facts — this package does no I/O (rails
// "pure core"); internal/cli's `contract retire` verb (spec 08 T1) is the
// one caller for this phase, reading consumes.yaml/satisfied-requirement/
// deprecation-thread facts from the local mirror and handing them in here.
//
// This hook is NOT wired into Engine.ValidateForSubmit's V2 pipeline in
// this phase (engine.go is off-limits to this phase's allowlist) — V3's
// CI wiring of this same hook is P9's job (spec 08 §10/Deferred). This
// phase only defines and unit-tests the check itself, exactly as the plan
// scopes it ("CI wiring of this hook into V3 is P9's, not touched here").
package validate

import (
	"fmt"
	"sort"
	"strings"
)

// RegisteredConsumer is one system whose acknowledgement the retire
// precondition (§5.4/D-022) requires before a contract version may
// retire. "Registered consumer" = any system with a satisfied requirement
// referencing the contract OR a `consumes.yaml` entry naming it (§5.2.3).
type RegisteredConsumer struct {
	// System is the consuming system's id.
	System string
	// Acked reports whether this system has acknowledged the deprecation
	// announcement (a broadcast-ack event on the deprecation thread,
	// §3.4.7) that named this contract version.
	Acked bool
	// Left reports the consumer's manifest membership status as of the
	// retire attempt: `left` systems are excluded from the ack set
	// entirely (§5.4 bullet (a), CC-062) — they never block retire and
	// are never counted as "un-acked".
	Left bool
}

// ObservedConsumer is one system whose OWN committed artifacts show it
// consuming the contract being retired while NEITHER half of the D-022
// declaration union names it (spec 03-observed-consumption.md §T2:
// "declared consumption is an act of commitment, written; observed
// consumption is evidence, computed" — the two are different kinds of
// fact and must not be conflated).
//
// It is derived, never written: internal/cache resolves it from the
// verify-passed deliveries already committed in the space
// (FindObservedConsumers), so no registry entry, no schema field and no
// migration exists for it — README AC3's "where a fact can be derived, it
// is derived", because a second written claim can itself go stale.
//
// DELIBERATELY NOT A CONSUMER. This type is NOT RegisteredConsumer and
// carries no Acked/Left: an observed system's silence is not an
// outstanding acknowledgement, because it never registered and was never
// owed one. Giving it those fields would be the first step to counting it
// in the un-acked set, which §9 refuses in as many words.
//
// The json tags are load-bearing rather than decorative: internal/mcp
// returns this type verbatim on `a2a_contract_retire`'s structured result
// (§8 row 7), where every other key is snake_case — an untagged struct
// would put `System`/`Packages` on an agent-facing wire beside `pr_url`
// and `remaining_action`, which is the kind of inconsistency a caller has
// to special-case forever. Violation (result.go) tags its own fields for
// the same reason.
type ObservedConsumer struct {
	// System is the observed consuming system's id.
	System string `json:"system"`
	// Version is the contract version that system's own deliveries pinned
	// (cache.ObservedConsumer's own field, carried through unchanged) —
	// retire is per-version, so naming the system without it is not
	// actionable (spec 06 US-2). Deliberately NOT `omitempty`: an empty
	// string here is a REACHABLE, meaningful value (a delivery whose pinned
	// reference carried no `@version` at all — cache's own
	// splitPinnedContractID), and on an agent-to-agent wire that value must
	// stay indistinguishable from nothing OTHER than an older binary that
	// never populated this field — omitting it on the empty case would
	// erase exactly that distinction.
	Version string `json:"version"`
	// Packages is the number of DISTINCT data packages that system
	// verify-passed against this contract at this version — a count of
	// things a reader would actually have to look at, never of deliverable
	// occurrences (cache.UnadoptedConsumption's own doc comment).
	Packages int `json:"packages"`
}

// RetirePrecondition is CheckRetirePrecondition's own minimal input — the
// caller (internal/cli) resolves every fact from the local mirror; this
// package never reads a manifest or an event stream itself.
type RetirePrecondition struct {
	// Consumers is every registered consumer of the contract version
	// being retired (§5.2.3/D-022 union: satisfied requirement OR
	// consumes.yaml entry).
	Consumers []RegisteredConsumer
	// SunsetPassed reports whether the deprecation's recorded sunset date
	// has passed as of the retire attempt.
	SunsetPassed bool
	// HasReminder reports whether >=1 `note` reminder event is recorded
	// on the deprecation announcement's thread (§5.4 bullet (b)).
	HasReminder bool
	// ActorIsHuman reports whether the retiring actor's kind is "human"
	// (§5.4 bullet (b): "the retire event arrives via a human-reviewed
	// G2-class PR").
	ActorIsHuman bool
	// Override is whether the caller (`--override`) requested the
	// human-gated override path.
	Override bool

	// Observed is every system the space's own committed record shows
	// consuming this contract WITHOUT declaring it (spec
	// 03-observed-consumption.md, US-1).
	//
	// READ BY NOTHING IN CheckRetirePrecondition, and that is the design,
	// not an oversight (§8 criterion 2, §9 "Why observation does not
	// veto"): a producer cannot distinguish "a pipeline lives on this"
	// from "one experimental delivery was received" — only the consumer
	// can — and a veto on the producer's guess would make any contract
	// ever delivered against immortal. It reaches the DECISION instead of
	// the GATE, through ObservedConsumptionNotice below, so the clean path
	// stops being silently blind while staying exactly as clean.
	//
	// The field lives on this input rather than beside it because the
	// notice is a function of the SAME facts the check reads (the declared
	// consumer count is half of what makes "0 declared, 1 observed"
	// meaningful) — and because a reader of this struct should meet, in
	// one place, both the set that blocks and the set that deliberately
	// does not.
	Observed []ObservedConsumer
}

// CheckRetirePrecondition is the §5.4/D-022 retire-precondition
// policy-class check (CC-081/CC-082/CC-086):
//
//   - No un-acked (non-`left`) registered consumer at all: clean, retire
//     proceeds ungated (violation == nil, overridden == nil).
//   - Un-acked consumers exist and Override is false: blocked (AC-202.2) —
//     a single POL-006 violation, never reaching the funnel.
//   - Un-acked consumers exist and Override is true but the full §5.4
//     precondition set (sunset passed AND a recorded reminder AND a human
//     actor) is not met: still blocked (AC-202.3 first clause) — the same
//     POL-006 violation; override does not relax any single precondition.
//   - Un-acked consumers exist, Override is true, and every precondition
//     is met: retire succeeds; overridden lists the un-acked consumers
//     (sorted, deterministic) for the caller to flag `retired-unacked`
//     and notify (AC-202.3 second clause).
//
// p.Observed is READ BY NOTHING HERE. Observed consumption informs the
// producer's decision and gates nothing (spec 03-observed-consumption.md
// §8 criterion 2 / §9); it reaches the operator through
// ObservedConsumptionNotice, never through this function's refusal set.
// policy_retire_observed_test.go asserts that inertness over every branch
// below rather than trusting this sentence.
func CheckRetirePrecondition(p RetirePrecondition) (violation *Violation, overridden []string) {
	var unacked []string
	seen := map[string]bool{}
	for _, c := range p.Consumers {
		if c.Left {
			continue
		}
		if !c.Acked && !seen[c.System] {
			seen[c.System] = true
			unacked = append(unacked, c.System)
		}
	}
	if len(unacked) == 0 {
		return nil, nil
	}
	sort.Strings(unacked)

	if p.Override && p.SunsetPassed && p.HasReminder && p.ActorIsHuman {
		return nil, unacked
	}

	shown := unacked
	suffix := ""
	if len(shown) > retireNoticeLimit {
		shown = shown[:retireNoticeLimit]
		suffix = fmt.Sprintf(" (+%d more)", len(unacked)-retireNoticeLimit)
	}
	return &Violation{
		Code:  "POL-006",
		Class: ClassPolicy,
		Path:  "",
		Message: "retire refused: registered consumers missing acknowledgement: " +
			strings.Join(shown, ", ") + suffix +
			" (§5.4) — ask them to acknowledge, or resubmit as a human-reviewed override once sunset has passed and a reminder is recorded",
		CCRef:    "CC-081",
		Severity: SeverityReject,
		Subjects: append([]string(nil), unacked...),
	}, nil
}

// retireNoticeLimit bounds how many systems either retire message renders
// inline before collapsing the tail into "(+N more)" — the same cap
// CheckRetirePrecondition's own POL-006 text uses, named once so the two
// cannot drift into rendering the same kind of list two different ways.
const retireNoticeLimit = 8

// ObservedConsumptionNotice renders the one line `contract retire` prints
// on BOTH surfaces when the space's own committed record shows systems
// consuming this contract that never declared it — spec
// 03-observed-consumption.md §8 criteria 1 and 7 (the epic's AC5).
//
// It returns "" when nothing is observed, which is the overwhelmingly
// common case and §8 criterion 4's floor: with no observation there is no
// line, so a plain retire's output is byte-identical to what it was before
// this phase. Nothing here can refuse: the return type is a string, not a
// *Violation, and that is the type system carrying §9's "informs, never
// vetoes" rather than a comment asking for it.
//
// It lives in this package, and not in either surface, for ADR-004's
// reason: `a2a contract retire` (internal/cli) and `a2a_contract_retire`
// (internal/mcp) are two doors onto one rule, and a rule one door can
// express and the other cannot is exactly the B22 shape this epic's AC5
// exists to stop recurring. Both call this; neither formats its own.
//
// The declared count it opens with is deliberately the SAME set
// CheckRetirePrecondition weighs — `left` systems excluded (§5.4 bullet
// (a)), deduplicated by system — because "0 declared consumers, 1
// observed" is the sentence that makes the clean path's blindness visible,
// and it is only true if both halves are counted the same way the gate
// counts them.
func ObservedConsumptionNotice(p RetirePrecondition) string {
	// observedKey is System+Version: a system pinning TWO different
	// versions of the same contract must render as two distinct entries,
	// never collapsed into one (spec 06 §8 criterion 5) — the same shape
	// that would make a naive map[string]int keyed by System alone silently
	// drop one version's own count.
	type observedKey struct{ System, Version string }
	packages := map[observedKey]int{}
	var keys []observedKey
	for _, o := range p.Observed {
		if o.System == "" {
			continue
		}
		key := observedKey{System: o.System, Version: o.Version}
		if _, seen := packages[key]; !seen {
			keys = append(keys, key)
		}
		// A (system, version) pair named twice is one entry: keep the
		// LARGER count rather than summing, since each entry is already a
		// distinct-package count for the same (system, contract, version)
		// triple and summing would double-count a package a caller
		// resolved twice.
		if o.Packages > packages[key] {
			packages[key] = o.Packages
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].System != keys[j].System {
			return keys[i].System < keys[j].System
		}
		return keys[i].Version < keys[j].Version
	})

	declared := map[string]bool{}
	for _, c := range p.Consumers {
		if c.Left {
			continue
		}
		declared[c.System] = true
	}

	shown := keys
	suffix := ""
	if len(shown) > retireNoticeLimit {
		shown = shown[:retireNoticeLimit]
		suffix = fmt.Sprintf(" (+%d more)", len(keys)-retireNoticeLimit)
	}
	rendered := make([]string, 0, len(shown))
	for _, k := range shown {
		unit := "packages"
		if packages[k] == 1 {
			unit = "package"
		}
		versionPart := ""
		if k.Version != "" {
			versionPart = " @ " + k.Version
		}
		rendered = append(rendered, fmt.Sprintf("%s (%d %s%s)", k.System, packages[k], unit, versionPart))
	}

	return fmt.Sprintf(
		"%d declared consumer(s), %d observed and undeclared: %s%s — their own verify-passed deliveries pin this contract while they declare it nowhere. "+
			"Observed consumption never blocks retire (§9); it is named so this decision is not made blind. "+
			"Each of them can exit with `a2a contract adopt` (declare the dependency) or by acknowledging the deprecation — no new verb, either way",
		len(declared), len(keys), strings.Join(rendered, ", "), suffix)
}
