package schema

import (
	"encoding/json"
	"reflect"
	"testing"

	schemas "github.com/ydnikolaev/a2ahub/schemas"
)

// reasonCodeMirrorPair names two places one vocabulary is written down, and
// why there are two.
//
// `blocked_by.reason_code` on envelope/v2/response deliberately reuses the
// event schema's vocabulary — the same words mean the same things whether a
// system is declining a request or naming what blocks a partial answer, and
// inventing a second list would be the substitution this epic exists to end.
// event/v1's own `reason_code` is the THIRD site: byte-frozen on the v1 side
// (schemas/published-v1.sha256), so a v2-only widening is the expected next
// change and the one this pair exists to catch (computed-not-listed-2026-08
// P3 §8 row 7 — "the sharper half" the spec names, because a widened v2 with
// a frozen v1 is exactly the one-sided drift a same-family mirror is best
// placed to prove).
//
// Every pair here is a COPY rather than a `$ref`, and that is a real
// limitation rather than a preference: the corpus resolves cross-document
// references only for the per-version base schema, so neither a per-type
// envelope schema nor an older event version has a resolvable path to the
// schema it mirrors. A copy is correct the day it is written and silently
// wrong the day one side is widened — which is exactly how `canceled`,
// spelled with one `l`, once sat in a dashboard list matching nothing the
// protocol could produce.
//
// So the drift is refused here instead. If a `$ref` becomes reachable for a
// given pair, delete that pair (and, if it was the last one, this file).
type reasonCodeMirrorPair struct {
	name       string
	sourcePath string
	sourcePtr  []string
	mirrorPath string
	mirrorPtr  []string
}

var reasonCodeMirrors = []reasonCodeMirrorPair{
	{
		name:       "event/v2 to envelope/v2 response.blocked_by",
		sourcePath: "event/v2/event.schema.json",
		sourcePtr:  []string{"properties", "reason_code", "enum"},
		mirrorPath: "envelope/v2/response.schema.json",
		mirrorPtr:  []string{"properties", "blocked_by", "properties", "reason_code", "enum"},
	},
	{
		// event/v1 is byte-frozen (schemas/published-v1.sha256); event/v2 is
		// not. That asymmetry is why this pair, not the v2/response pair
		// above, is "the sharper half" (spec §8 row 7's own wording): a v2
		// enum member added without a matching v1 change is the ordinary,
		// expected shape of the NEXT widening, not a hypothetical one.
		name:       "event/v1 to event/v2",
		sourcePath: "event/v1/event.schema.json",
		sourcePtr:  []string{"properties", "reason_code", "enum"},
		mirrorPath: "event/v2/event.schema.json",
		mirrorPtr:  []string{"properties", "reason_code", "enum"},
	},
}

// TestReasonCodeMirrorMatchesTheEventSchema refuses a widened vocabulary on
// one side only, for every registered mirror pair — including event/v1's,
// added by computed-not-listed-2026-08 P3 to close the "half this gate does
// not cover" the phase's own spec names (schema↔schema, as opposed to the
// vocabulary-carrier gate's schema→Go half).
func TestReasonCodeMirrorMatchesTheEventSchema(t *testing.T) {
	t.Parallel()

	for _, pair := range reasonCodeMirrors {
		t.Run(pair.name, func(t *testing.T) {
			t.Parallel()

			source := enumAt(t, pair.sourcePath, pair.sourcePtr)
			mirror := enumAt(t, pair.mirrorPath, pair.mirrorPtr)

			if len(source) == 0 {
				t.Fatalf("%s carries no reason_code enum at %v — this gate is watching nothing, which is worse than a red one: either the field moved (point this test at it) or the vocabulary was deleted",
					pair.sourcePath, pair.sourcePtr)
			}
			if err := reasonCodeEnumsAgree(source, mirror); err != nil {
				t.Fatalf("reason_code has drifted between the two documents that carry it.\n  %s: %v\n  %s: %v\n%v",
					pair.sourcePath, source, pair.mirrorPath, mirror, err)
			}
		})
	}
}

// TestReasonCodeMirrorCatchesDrift proves reasonCodeEnumsAgree — the exact
// comparison TestReasonCodeMirrorMatchesTheEventSchema runs against the real
// corpus above — actually reds on a widened enum, rather than merely
// agreeing with itself on data that happens to match today. Spec §8 row 7's
// own wording: "the three enums currently agree byte-for-byte, so the test
// must be proven by mutation, not by passing." The three enums are read
// through schemas.FS (an embed.FS baked into the test binary at compile
// time — schemas/** is outside this phase's allowlist, so this proof
// mutates in-memory JSON, never a file on disk) — this test therefore
// builds its own scratch documents rather than mutating a corpus file, and
// exercises enumFromJSON (the same pointer-walk enumAt uses) directly on
// them.
func TestReasonCodeMirrorCatchesDrift(t *testing.T) {
	t.Parallel()

	agreeing := []byte(`{"properties":{"reason_code":{"enum":["split-required","security-concern","out-of-scope","duplicate","other"]}}}`)
	// widened carries every member `agreeing` does, plus one more — the
	// exact "v2 widened, v1 frozen" shape §8 row 7 names as the expected
	// next real-world change.
	widened := []byte(`{"properties":{"reason_code":{"enum":["split-required","security-concern","out-of-scope","duplicate","other","superseded-by-newer-request"]}}}`)
	ptr := []string{"properties", "reason_code", "enum"}

	source := enumFromJSON(t, agreeing, "scratch/frozen-v1.json", ptr)
	mirror := enumFromJSON(t, widened, "scratch/widened-v2.json", ptr)

	if err := reasonCodeEnumsAgree(source, mirror); err == nil {
		t.Fatalf("FALSE GREEN: reasonCodeEnumsAgree reported no drift between %v and %v — a widened mirror must red, or this gate is watching nothing", source, mirror)
	}

	// Confirm the same two documents, unmutated, still agree — the false
	// green above would otherwise also cover an always-red comparator that
	// never actually reads its arguments.
	if err := reasonCodeEnumsAgree(source, source); err != nil {
		t.Fatalf("reasonCodeEnumsAgree reported drift comparing %v against itself: %v", source, err)
	}
}

// reasonCodeEnumsAgree is the one drift check every reasonCodeMirrorPair
// runs — extracted to a named function so
// TestReasonCodeMirrorCatchesDrift can prove it reds on real drift instead
// of only ever being exercised against data that currently agrees.
func reasonCodeEnumsAgree(source, mirror []string) error {
	if !reflect.DeepEqual(source, mirror) {
		return errReasonCodeDrift
	}
	return nil
}

var errReasonCodeDrift = errDrift("one vocabulary, written twice because a $ref cannot reach across families — widen both or neither")

// errDrift is a trivial string error, named rather than fmt.Errorf'd so
// TestReasonCodeMirrorCatchesDrift's failure message and the production
// test's failure message can share one literal without importing "errors"
// for a single sentinel.
type errDrift string

func (e errDrift) Error() string { return string(e) }

// enumAt reads a JSON string-enum out of an embedded schema by pointer path.
func enumAt(t *testing.T, path string, ptr []string) []string {
	t.Helper()

	raw, err := schemas.FS.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return enumFromJSON(t, raw, path, ptr)
}

// enumFromJSON reads a JSON string-enum out of raw document bytes by
// pointer path. Split out of enumAt so a caller proving the comparison
// logic by mutation (TestReasonCodeMirrorCatchesDrift) can walk a
// hand-built scratch document the same way enumAt walks a real embedded
// schema, without a byte ever touching schemas/** on disk.
func enumFromJSON(t *testing.T, raw []byte, label string, ptr []string) []string {
	t.Helper()

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", label, err)
	}
	cur := doc
	for _, key := range ptr {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: %v is not an object at %q", label, ptr, key)
		}
		cur, ok = m[key]
		if !ok {
			t.Fatalf("%s: no %q under %v", label, key, ptr)
		}
	}
	list, ok := cur.([]any)
	if !ok {
		t.Fatalf("%s: %v is not an array", label, ptr)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: %v carries a non-string member %v", label, ptr, v)
		}
		out = append(out, s)
	}
	return out
}
