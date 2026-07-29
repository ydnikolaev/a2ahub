package schema

import (
	"fmt"
	"testing"
)

// TestActorNameMinLength_EventSchema is the fail-closed stopgap for the
// anonymous-actor gap (HIGH finding): an empty actor.name must be REJECTED
// by event/v1 — `required` alone only guarantees the field is present, not
// non-empty, so `name: ""` used to validate silently.
func TestActorNameMinLength_EventSchema(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	const eventDoc = `
schema: event/v1
event: 01J40A7M9P1S3V5W7Y9A1C3E5G
space: getvisa
subject: XR-axon-country-vocabulary
transition: satisfy
actor: {kind: agent, name: %s, system: axon}
at: 2026-08-05T17:45:00Z
`

	t.Run("empty name is invalid", func(t *testing.T) {
		t.Parallel()
		instance := toInstance(t, fmt.Sprintf(eventDoc, `""`))
		violations, err := c.ValidateEvent("v1", instance)
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if len(violations) == 0 {
			t.Fatal("expected a violation for actor.name: \"\", got none")
		}
		found := false
		for _, v := range violations {
			if v.Path == "actor.name" && v.Keyword == "minLength" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a MinLength violation at path 'actor.name', got %+v", violations)
		}
	})

	t.Run("non-empty name is valid", func(t *testing.T) {
		t.Parallel()
		instance := toInstance(t, fmt.Sprintf(eventDoc, "claude-code"))
		violations, err := c.ValidateEvent("v1", instance)
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if len(violations) != 0 {
			t.Fatalf("expected a valid actor.name to produce zero violations, got %+v", violations)
		}
	})
}

// TestActorNameMinLength_EnvelopeSchema is the same fail-closed guard on
// the envelope/v1 base schema's actor.name (shared by every artifact type
// via allOf + base $ref).
func TestActorNameMinLength_EnvelopeSchema(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	const workRequestDoc = `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: A valid work request
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260721-k3f9
actor: {kind: agent, name: %s}
created: "2026-07-31T08:40:00Z"
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without normalization."
acceptance_criteria:
  - "Every code exists in the registry."
classification: internal
`

	t.Run("empty name is invalid", func(t *testing.T) {
		t.Parallel()
		instance := toInstance(t, fmt.Sprintf(workRequestDoc, `""`))
		violations, err := c.ValidateEnvelope("work_request", "v1", instance)
		if err != nil {
			t.Fatalf("ValidateEnvelope: %v", err)
		}
		found := false
		for _, v := range violations {
			if v.Path == "actor.name" && v.Keyword == "minLength" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a MinLength violation at path 'actor.name', got %+v", violations)
		}
	})

	t.Run("non-empty name is valid", func(t *testing.T) {
		t.Parallel()
		instance := toInstance(t, fmt.Sprintf(workRequestDoc, "codex"))
		violations, err := c.ValidateEnvelope("work_request", "v1", instance)
		if err != nil {
			t.Fatalf("ValidateEnvelope: %v", err)
		}
		if len(violations) != 0 {
			t.Fatalf("expected a valid actor.name to produce zero violations, got %+v", violations)
		}
	})
}
