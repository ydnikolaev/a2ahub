package livee2e

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// maxSSEStreamBytes bounds one scenario's whole event stream, not one block.
const maxSSEStreamBytes = 64 << 10

// ErrSSEExposedSnapshotRows marks an event stream that carried projection rows
// instead of the revision token the read-only server is allowed to push.
var ErrSSEExposedSnapshotRows = errors.New("SSE exposed snapshot rows")

// sseStream reads canonical SSE blocks from ONE response body.
//
// The buffered reader is created once and reused for every block. Building a
// fresh bufio.Reader per block — which this used to do — silently discards
// whatever the previous reader had already pulled off the socket but not
// returned, so any event that arrived while the caller was busy elsewhere was
// dropped. LE-OC-03 opens the stream, spends minutes landing PRs, and only
// then reads: by that point a dozen keepalives and the revision event are all
// buffered together, which is exactly the case the old per-block reader lost.
type sseStream struct{ lines *bufio.Reader }

func newSSEStream(reader io.Reader) *sseStream {
	return &sseStream{lines: bufio.NewReader(io.LimitReader(reader, maxSSEStreamBytes))}
}

// readBlock returns the next event block, terminator included.
func (s *sseStream) readBlock() (string, error) {
	var block strings.Builder
	for {
		line, err := s.lines.ReadString('\n')
		block.WriteString(line)
		if line == "\n" {
			return block.String(), nil
		}
		if err != nil {
			return block.String(), err
		}
	}
}

// readUntilRevision consumes blocks until a revision event arrives, returning
// everything it observed so a failure can be read afterwards. Leading
// keepalives are expected, not a failure: they are how the transport holds an
// idle stream open while the caller is doing something else.
func (s *sseStream) readUntilRevision() (string, error) {
	var observed strings.Builder
	for {
		block, err := s.readBlock()
		observed.WriteString(block)
		if strings.Contains(observed.String(), "timeline") {
			return observed.String(), ErrSSEExposedSnapshotRows
		}
		if strings.Contains(block, "event: revision") {
			return observed.String(), err
		}
		if err != nil {
			return observed.String(), err
		}
	}
}
