package space

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/version"
)

// producerStampTool is the `tool` value written into every stamp. A constant
// rather than something derived: the point of the field is to identify the
// PRODUCER, and a value that varied by invocation would identify nothing.
const producerStampTool = "a2a"

// producerStampKey is the event/v1 field name.
const producerStampKey = "produced_by"

// eventPathMarker identifies an event document among a write's files, matching
// the predicate internal/cli's own submit validator uses (§4.2's
// `<system>/events/<year>/<ulid>.yaml`). Events are the only documents stamped:
// every transition emits one, so an envelope always arrives beside a stamped
// event, and stamping both would duplicate the same fact in two places with two
// chances to disagree.
const eventPathMarker = "/events/"

// StampProducer returns files with `produced_by` appended to every event
// document, when spaceFloor has reached version.ProducerStampFloor.
//
// # What this is for, and what it is NOT
//
// `min_binary_version` is checked in the WRITER's own binary (funnel step 1b).
// That binds a binary that honours the check and does nothing at all to one
// that does not: before 0.9.0 every lifecycle and contract verb wrote with an
// empty floor, so raising the floor could not gate them even in principle. The
// stamp is what lets the SPACE see, at merge time, which producer wrote an
// event — the only server-side place an old binary can be stopped.
//
// It stops ACCIDENTS, not forgery. The stamp is written by the very client the
// space is distrusting, so anything hand-crafting an event can claim any
// version. That is worth saying plainly rather than letting the field read as a
// security control: what it catches is a colleague on a stale binary, which is
// the failure that actually happened.
//
// Appending text rather than re-encoding YAML is deliberate. An event is a
// flat, machine-written document with no frontmatter, so a top-level key
// appended at the end is valid YAML and leaves every other byte untouched —
// where a decode/re-encode round trip would reformat a document some other
// layer may be comparing byte-for-byte, and would silently drop any field this
// struct does not know about.
//
// Idempotent: a document that already carries the key is returned unchanged, so
// re-submitting the same staged bytes cannot double-stamp.
func StampProducer(files []FileWrite, binaryVersion, spaceFloor string) ([]FileWrite, error) {
	stamping, err := version.ProducerStampActive(spaceFloor)
	if err != nil {
		return nil, err
	}
	if !stamping {
		return files, nil
	}

	out := make([]FileWrite, len(files))
	copy(out, files)
	for i := range out {
		if !strings.Contains(out[i].Path, eventPathMarker) {
			continue
		}
		if hasProducerStamp(out[i].Content) {
			continue
		}
		out[i].Content = appendProducerStamp(out[i].Content, binaryVersion)
	}
	return out, nil
}

// hasProducerStamp reports whether raw already carries the key at the top
// level. Anchored to the line start so a `produced_by` appearing inside a
// note's free text cannot be mistaken for the field.
func hasProducerStamp(raw []byte) bool {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if bytes.HasPrefix(line, []byte(producerStampKey+":")) {
			return true
		}
	}
	return false
}

// appendProducerStamp writes the block, ensuring the document it is appended to
// ends in a newline first — a file that did not would otherwise have its last
// line silently merged with the key.
func appendProducerStamp(raw []byte, binaryVersion string) []byte {
	var b bytes.Buffer
	b.Write(raw)
	if len(raw) > 0 && !bytes.HasSuffix(raw, []byte("\n")) {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s:\n  tool: %s\n  version: %q\n", producerStampKey, producerStampTool, binaryVersion)
	return b.Bytes()
}
