package cache

// ReasonCode is one machine-readable explanation attached to an inbox,
// outbox, overdue, statusline, or notification item. Human words do not live
// here; presentation composes them at its own boundary.
type ReasonCode string

// The complete current reason-code vocabulary. Production producers and
// consumers refer to these names; ReasonCodes is the enumerable projection
// used by tests now and the binary catalogue in the next phase.
const (
	ReasonAddressedNoAck               ReasonCode = "addressed-no-ack"
	ReasonRespondedAwaitingVerifyClose ReasonCode = "responded-awaiting-verify-close"
	ReasonDisputedTowardMe             ReasonCode = "disputed-toward-me"
	ReasonP1OrBlockingOpen             ReasonCode = "p1-or-blocking-open"
	ReasonGatePendingOnMe              ReasonCode = "gate-pending-on-me"
	ReasonActivationOwed               ReasonCode = "activation-owed"
	ReasonStateChangedSinceCursor      ReasonCode = "state-changed-since-cursor"
	ReasonDeclined                     ReasonCode = "declined"
	ReasonDisputed                     ReasonCode = "disputed"
	ReasonStaleSLA                     ReasonCode = "stale-sla"
	ReasonNeededByPassed               ReasonCode = "needed-by-passed"
	ReasonOverdueOnMe                  ReasonCode = "overdue-on-me"
)

// ReasonCodes returns every declared reason exactly once, in stable normative
// order. The returned slice is fresh so callers cannot mutate the vocabulary.
func ReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonAddressedNoAck,
		ReasonRespondedAwaitingVerifyClose,
		ReasonDisputedTowardMe,
		ReasonP1OrBlockingOpen,
		ReasonGatePendingOnMe,
		ReasonActivationOwed,
		ReasonStateChangedSinceCursor,
		ReasonDeclined,
		ReasonDisputed,
		ReasonStaleSLA,
		ReasonNeededByPassed,
		ReasonOverdueOnMe,
	}
}

// HasReason reports whether raw, the established []string wire shape, carries
// code. Keeping the conversion here lets consumers stay typed without changing
// that additive compatibility boundary.
func HasReason(raw []string, code ReasonCode) bool {
	for _, reason := range raw {
		if ReasonCode(reason) == code {
			return true
		}
	}
	return false
}
