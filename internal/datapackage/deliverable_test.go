package datapackage

import "testing"

// Moved from internal/cache/delivery_test.go (agent-exchange-2026-08 wave
// 23, part 2) along with DecodeDeliverables itself — deliverable.go's own
// doc comment explains why. handoffRaw is duplicated here rather than
// shared: this package cannot import internal/cache (the dependency runs
// the other way) and a fixture constant carries no behaviour to drift.
const handoffRaw = `---
schema: envelope/v1
id: XH-alpha-20260101-aaaa
type: handoff
thread: thread:t1
from: alpha
to: [beta]
title: Delivery
created: 2026-01-01T00:00:00Z
deliverables:
  - name: sample dataset
    ref: DP-alpha-20260101-aaaa
    kind: data
  - name: code artifact
    ref: some/code/path
    kind: code
verification: manual
acceptance_criteria: ["done"]
limitations: []
fulfills: ["XW-alpha-20251231-zzzz"]
---
Body.
`

func TestDecodeDeliverables_FiltersByFrontmatter(t *testing.T) {
	t.Parallel()
	got, err := DecodeDeliverables([]byte(handoffRaw))
	if err != nil {
		t.Fatalf("DecodeDeliverables: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 deliverables, got %d: %+v", len(got), got)
	}
	if got[0].Ref != "DP-alpha-20260101-aaaa" || got[0].Kind != "data" {
		t.Fatalf("unexpected first deliverable: %+v", got[0])
	}
	if got[1].Kind != "code" {
		t.Fatalf("unexpected second deliverable: %+v", got[1])
	}
}

func TestDecodeDeliverables_NotFrontmatterShaped(t *testing.T) {
	t.Parallel()
	if _, err := DecodeDeliverables([]byte("no frontmatter here")); err == nil {
		t.Fatal("want an error for non-frontmatter-shaped input")
	}
}
