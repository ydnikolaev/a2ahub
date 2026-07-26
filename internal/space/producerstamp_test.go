package space

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

const stampTestEvent = "schema: event/v1\nevent: 01J40A7M9P1S3V5W7Y9A1C3E5G\nspace: getvisa\n" +
	"subject: XQ-axon-20260726-ab12\ntransition: submit\n" +
	"actor: {kind: agent, name: bot, system: axon}\nat: 2026-07-26T10:00:00Z\n"

// TestStampProducerIsGatedOnTheSpaceFloor is the whole mechanism in one table,
// and the FLOOR cases are the load-bearing ones.
//
// event/v1 is additionalProperties:false and a space's CI validator is pinned by
// the space, so a stamped event written to a space whose validator predates the
// field would be REJECTED — every write refused over an unknown field, which is
// worse than the stale-binary case the stamp exists to catch. Keying on the
// floor makes the order self-enforcing: `a2a space update` re-pins the validator
// and raises the floor in ONE pull request that carries no events, so the field
// cannot appear before the validator that accepts it.
//
// Get this wrong in the permissive direction and every space that has not
// migrated breaks on the next write.
func TestStampProducerIsGatedOnTheSpaceFloor(t *testing.T) {
	t.Parallel()

	eventPath := "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml"
	cases := []struct {
		name      string
		floor     string
		wantStamp bool
	}{
		{"floor at the stamping release: stamped", version.ProducerStampFloor, true},
		{"floor above it: stamped", "0.11.0", true},
		{"floor below it: NOT stamped — the space's validator would refuse the field", "0.9.1", false},
		{"no floor at all: NOT stamped — every space written before the field", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := StampProducer([]FileWrite{{Path: eventPath, Content: []byte(stampTestEvent)}}, "0.10.0", tc.floor)
			if err != nil {
				t.Fatalf("StampProducer: %v", err)
			}
			got := strings.Contains(string(out[0].Content), "produced_by:")
			if got != tc.wantStamp {
				t.Fatalf("stamped = %v, want %v (floor %q)\n%s", got, tc.wantStamp, tc.floor, out[0].Content)
			}
		})
	}
}

// TestStampProducerStampsOnlyEvents: an envelope is left byte-identical.
//
// Every transition emits an event, so an envelope always arrives beside a
// stamped one — stamping both would record the same fact twice, with two places
// for it to disagree. And an envelope has FRONTMATTER: appending a top-level key
// to it would land the key in the body, outside the frontmatter block, where it
// means nothing.
func TestStampProducerStampsOnlyEvents(t *testing.T) {
	t.Parallel()

	envelope := "---\nschema: envelope/v1\nid: XQ-axon-20260726-ab12\n---\nbody\n"
	files := []FileWrite{
		{Path: "axon/exchanges/XQ-axon-20260726-ab12.md", Content: []byte(envelope)},
		{Path: "axon/consumes.yaml", Content: []byte("schema: consumes/v1\nsystem: axon\ndependencies: []\n")},
		{Path: "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml", Content: []byte(stampTestEvent)},
	}
	out, err := StampProducer(files, "0.10.0", version.ProducerStampFloor)
	if err != nil {
		t.Fatalf("StampProducer: %v", err)
	}
	if string(out[0].Content) != envelope {
		t.Errorf("the envelope was modified:\n%s", out[0].Content)
	}
	if strings.Contains(string(out[1].Content), "produced_by") {
		t.Errorf("consumes.yaml was stamped:\n%s", out[1].Content)
	}
	if !strings.Contains(string(out[2].Content), "produced_by:") {
		t.Errorf("the event was NOT stamped:\n%s", out[2].Content)
	}
}

// TestStampProducerIsIdempotentAndWellFormed covers the two ways appending text
// to a document goes wrong.
func TestStampProducerIsIdempotentAndWellFormed(t *testing.T) {
	t.Parallel()
	path := "axon/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml"

	t.Run("a second pass does not double-stamp", func(t *testing.T) {
		t.Parallel()
		once, err := StampProducer([]FileWrite{{Path: path, Content: []byte(stampTestEvent)}}, "0.10.0", version.ProducerStampFloor)
		if err != nil {
			t.Fatalf("StampProducer: %v", err)
		}
		twice, err := StampProducer(once, "0.10.0", version.ProducerStampFloor)
		if err != nil {
			t.Fatalf("StampProducer (second): %v", err)
		}
		if n := strings.Count(string(twice[0].Content), "produced_by:"); n != 1 {
			t.Fatalf("key appears %d times — re-submitting the same staged bytes must not double-stamp:\n%s",
				n, twice[0].Content)
		}
	})

	t.Run("a document with no trailing newline is not merged into the key", func(t *testing.T) {
		t.Parallel()
		noNewline := strings.TrimSuffix(stampTestEvent, "\n")
		out, err := StampProducer([]FileWrite{{Path: path, Content: []byte(noNewline)}}, "0.10.0", version.ProducerStampFloor)
		if err != nil {
			t.Fatalf("StampProducer: %v", err)
		}
		if strings.Contains(string(out[0].Content), "Zproduced_by") {
			t.Fatalf("the last line was merged with the key:\n%s", out[0].Content)
		}
		if !strings.Contains(string(out[0].Content), "\nproduced_by:\n") {
			t.Fatalf("the key is not on its own line:\n%s", out[0].Content)
		}
	})

	t.Run("produced_by inside a note is not mistaken for the field", func(t *testing.T) {
		t.Parallel()
		// The idempotency check is anchored to the line start for this reason:
		// an event whose free-text note happens to mention the key must still
		// get a real stamp.
		withNote := stampTestEvent + "note: \"see produced_by: in the docs\"\n"
		out, err := StampProducer([]FileWrite{{Path: path, Content: []byte(withNote)}}, "0.10.0", version.ProducerStampFloor)
		if err != nil {
			t.Fatalf("StampProducer: %v", err)
		}
		if !strings.Contains(string(out[0].Content), "\nproduced_by:\n") {
			t.Fatalf("a mention inside a note suppressed the real stamp:\n%s", out[0].Content)
		}
	})
}

// TestStampProducerFailsClosedOnAnUnreadableFloor: a floor nobody can parse is a
// space whose intent is unknown. Answering either way would decide it silently,
// and the silent answer people notice is the wrong one.
func TestStampProducerFailsClosedOnAnUnreadableFloor(t *testing.T) {
	t.Parallel()

	_, err := StampProducer(
		[]FileWrite{{Path: "axon/events/2026/x.yaml", Content: []byte(stampTestEvent)}},
		"0.10.0", "not-a-version")
	if err == nil {
		t.Fatal("an unparseable min_binary_version must be an error, not a quiet decision either way")
	}
	if !strings.Contains(err.Error(), "not-a-version") {
		t.Errorf("err = %v, want the offending value quoted so an operator can find it", err)
	}
}
