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

	"github.com/ydnikolaev/a2ahub/internal/version"
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
func CheckRetirePrecondition(p RetirePrecondition) (violation *Violation, overridden []string) {
	var unacked []string
	for _, c := range p.Consumers {
		if c.Left {
			continue
		}
		if !c.Acked {
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

	return &Violation{
		Code:     "POL-006",
		Class:    ClassPolicy,
		Path:     "",
		Message:  "retire refused: registered consumers have not acknowledged the deprecation (§5.4) — retire un-acked, or resubmit as a human-reviewed override once sunset has passed and a reminder is recorded",
		CCRef:    "CC-081",
		Severity: SeverityReject,
	}, nil
}

// ContractVersionEvent is the minimal projection of a committed contract
// lifecycle event that CheckRetireVersionScope needs: which contract, which
// transition, which version. Both surfaces decode events into their own doc
// type (internal/cli's lifecycleEventDoc, internal/mcp's eventDoc) and map
// into this — the mapping is per-surface, the RULE is not.
type ContractVersionEvent struct {
	Subject    string
	Transition string
	Version    string
}

// CheckRetireVersionScope is POL-011: refuse to retire one version of a
// contract while another version of it is still published.
//
// It exists because internal/fold's contract table is SUBJECT-scoped. Run
// §5.4's cycle as publish 2.0 -> deprecate 1.0 -> retire 1.0 and every step
// is legal, the retire commits, and the subject lands in a state with no
// outgoing transitions — so every later publish or deprecate on that
// contract is refused forever, while 2.0 is live and being consumed. The
// other event order refuses harmlessly. This precondition makes both orders
// behave the same: loudly unavailable, rather than destroying the contract
// when the order happens to be the second one.
//
// It lives HERE, and not in the two verb files that call it, for the reason
// the wave that added it was already fixing elsewhere: a rule implemented
// once per surface is a rule that drifts. `CheckRetirePrecondition` above is
// the shape this follows, and the error registry's own closure test is what
// makes a policy code with no core home visible — it failed on POL-011 the
// moment the guard shipped from the verb layer, which is the gate working.
//
// The refusal names every still-published version and says the per-version
// lifecycle is pending, because a reader who learns the step is pending
// stops hunting for their own mistake.
func CheckRetireVersionScope(events []ContractVersionEvent, contractID, retiredVersion string) *Violation {
	// Keyed on the CANONICAL spelling, never the raw string. `publish`
	// records its own parsed value while `deprecate`/`retire` record the
	// operator's `--version` verbatim, so "1.0.0" and "01.0.0" can name one
	// version across two events. Keying raw made this guard invent a phantom
	// still-published version — the contract's own — and then assert the
	// refusal was "not a mistake in your command". An unparseable spelling
	// keys as itself: it can only ever match another identical unparseable
	// spelling, which is the conservative reading.
	canon := func(v string) string {
		if c, err := version.Canonical(v); err == nil {
			return c
		}
		return v
	}
	published := map[string]bool{}
	for _, ev := range events {
		if ev.Subject != contractID || ev.Version == "" {
			continue
		}
		switch ev.Transition {
		case "publish":
			published[canon(ev.Version)] = true
		case "deprecate", "retire":
			published[canon(ev.Version)] = false
		}
	}
	delete(published, canon(retiredVersion))

	still := make([]string, 0, len(published))
	for v, isPublished := range published {
		if isPublished {
			still = append(still, v)
		}
	}
	if len(still) == 0 {
		return nil
	}
	sort.Slice(still, func(i, j int) bool {
		older, err := version.OlderThan(still[i], still[j])
		if err != nil {
			return still[i] < still[j] // unparseable: falls back to byte order, which affects only the ORDER the message lists, never which versions it flags
		}
		return older
	})

	list := strings.Join(still, ", ")
	return &Violation{
		Code:     "POL-011",
		Severity: SeverityReject,
		Path:     contractID,
		Message: fmt.Sprintf(
			"version(s) %s are also published — retiring %s@%s today would mark the WHOLE contract retired, "+
				"because the lifecycle is recorded per contract rather than per version. That per-version "+
				"lifecycle is pending; this refusal is the step being unavailable, not a mistake in your command",
			list, contractID, retiredVersion),
	}
}
