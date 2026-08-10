package cache

import "testing"

// TestDecodeEvent_VerdictsDecoded is C7's entry-hop guard for `verdicts[]`
// (event/v2/event.schema.json, P6 wave C, threat-model.md T5): decodeEvent
// must carry the field through, the same way it already carries
// reason_code, refs and produced_by — a field this package never decodes
// can never reach `a2a thread`/`a2a show`/the dashboard/MCP, regardless of
// what the schema or internal/validate do with it.
func TestDecodeEvent_VerdictsDecoded(t *testing.T) {
	t.Parallel()

	raw := []byte(`schema: event/v2
event: 01K1A2B3C4D5E6F7G8H9J0K1M9
space: getvisa
subject: XW-axon-20260810-verify
transition: close
actor:
  kind: agent
  name: verifier
  system: axon
at: 2026-08-10T12:00:00Z
verdicts:
  - index: 0
    verdict: met
    cause_owner: axon
  - index: 1
    verdict: not_warranted
    cause_owner: seomatrix
`)

	probe, err := decodeEvent(raw)
	if err != nil {
		t.Fatalf("decodeEvent: %v", err)
	}
	if len(probe.Verdicts) != 2 {
		t.Fatalf("Verdicts = %+v, want 2 entries", probe.Verdicts)
	}
	if probe.Verdicts[0].Index != 0 || probe.Verdicts[0].Verdict != "met" || probe.Verdicts[0].CauseOwner != "axon" {
		t.Errorf("Verdicts[0] = %+v, want {0 met axon}", probe.Verdicts[0])
	}
	if probe.Verdicts[1].Index != 1 || probe.Verdicts[1].Verdict != "not_warranted" || probe.Verdicts[1].CauseOwner != "seomatrix" {
		t.Errorf("Verdicts[1] = %+v, want {1 not_warranted seomatrix}", probe.Verdicts[1])
	}
}

// TestDecodeEvent_AbsentVerdictsIsEmpty pins the other direction: an
// ordinary event/v1 document (no `verdicts:` at all — every event this
// package has decoded until now) must decode to an empty slice, not error
// and not panic on a nil field.
func TestDecodeEvent_AbsentVerdictsIsEmpty(t *testing.T) {
	t.Parallel()

	raw := []byte(`schema: event/v1
event: 01K1A2B3C4D5E6F7G8H9J0K1MA
space: getvisa
subject: XQ-axon-20260810-note
transition: note
actor:
  kind: agent
  name: someone
  system: axon
at: 2026-08-10T12:00:00Z
note: plain note, no verdicts
`)

	probe, err := decodeEvent(raw)
	if err != nil {
		t.Fatalf("decodeEvent: %v", err)
	}
	if len(probe.Verdicts) != 0 {
		t.Fatalf("Verdicts = %+v, want empty for a document with no verdicts[] field", probe.Verdicts)
	}
	if probe.Note != "plain note, no verdicts" {
		t.Errorf("Note = %q, unaffected field regressed", probe.Note)
	}
}
