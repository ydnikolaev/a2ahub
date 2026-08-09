package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	schemas "github.com/ydnikolaev/a2ahub/schemas"
)

// mirroredReasonCode names the two places one vocabulary is written down, and
// why there are two.
//
// `blocked_by.reason_code` on envelope/v2/response deliberately reuses the
// event schema's vocabulary — the same words mean the same things whether a
// system is declining a request or naming what blocks a partial answer, and
// inventing a second list would be the substitution this epic exists to end.
//
// It is a COPY rather than a `$ref`, and that is a real limitation rather
// than a preference: the corpus resolves cross-document references only for
// the per-version base schema, so a per-type schema in the envelope family
// has no resolvable path to the event family. A copy is correct the day it is
// written and silently wrong the day one side is widened — which is exactly
// how `canceled`, spelled with one `l`, once sat in a dashboard list matching
// nothing the protocol could produce.
//
// So the drift is refused here instead. If a `$ref` becomes reachable, delete
// this test with the copy.
var mirroredReasonCode = struct {
	sourcePath string
	sourcePtr  []string
	mirrorPath string
	mirrorPtr  []string
}{
	sourcePath: "event/v2/event.schema.json",
	sourcePtr:  []string{"properties", "reason_code", "enum"},
	mirrorPath: "envelope/v2/response.schema.json",
	mirrorPtr:  []string{"properties", "blocked_by", "properties", "reason_code", "enum"},
}

// TestReasonCodeMirrorMatchesTheEventSchema refuses a widened vocabulary on
// one side only.
func TestReasonCodeMirrorMatchesTheEventSchema(t *testing.T) {
	t.Parallel()

	source := enumAt(t, mirroredReasonCode.sourcePath, mirroredReasonCode.sourcePtr)
	mirror := enumAt(t, mirroredReasonCode.mirrorPath, mirroredReasonCode.mirrorPtr)

	if len(source) == 0 {
		t.Fatalf("%s carries no reason_code enum at %v — this gate is watching nothing, which is worse than a red one: either the field moved (point this test at it) or the vocabulary was deleted",
			mirroredReasonCode.sourcePath, mirroredReasonCode.sourcePtr)
	}
	if !reflect.DeepEqual(source, mirror) {
		t.Fatalf("reason_code has drifted between the two documents that carry it.\n  %s: %v\n  %s: %v\nOne vocabulary, written twice because a $ref cannot reach across families — widen both or neither.",
			mirroredReasonCode.sourcePath, source,
			mirroredReasonCode.mirrorPath, mirror)
	}
}

// enumAt reads a JSON string-enum out of an embedded schema by pointer path.
func enumAt(t *testing.T, path string, ptr []string) []string {
	t.Helper()

	raw, err := schemas.FS.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	cur := doc
	for _, key := range ptr {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: %v is not an object at %q", path, ptr, key)
		}
		cur, ok = m[key]
		if !ok {
			t.Fatalf("%s: no %q under %v", path, key, ptr)
		}
	}
	list, ok := cur.([]any)
	if !ok {
		t.Fatalf("%s: %v is not an array", path, ptr)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: %v carries a non-string member %v", path, ptr, v)
		}
		out = append(out, s)
	}
	return out
}
