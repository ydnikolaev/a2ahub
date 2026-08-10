// Package operation derives stable, private idempotency keys from semantic
// write intent. Keys identify an operation across retries; they are not
// protocol artifact IDs and never replace the date-bearing public ID grammar.
package operation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

const prefix = "op-v1-"

// BranchName renders the deterministic write branch shared by planning and
// the space write funnel. Keeping the protocol string here prevents a pure
// planner and the I/O adapter from deriving different retry heads.
// BranchName is part of the public package API.
func BranchName(system, verb, operationID string) string {
	return fmt.Sprintf("a2a/%s/%s/%s", system, verb, operationID)
}

const (
	// WorkActionStart is part of the public package API.
	WorkActionStart = "start"
	// WorkActionCheckpoint is part of the public package API.
	WorkActionCheckpoint = "checkpoint"
	// WorkActionWait is part of the public package API.
	WorkActionWait = "wait"
	// WorkActionStop is part of the public package API.
	WorkActionStop = "stop"
)

var (
	// ErrInvalidWorkOperation marks work identity inputs that cannot name a
	// durable semantic step.
	// ErrInvalidWorkOperation is part of the public package API.
	ErrInvalidWorkOperation = errors.New("operation: invalid work operation")

	workIDPattern = regexp.MustCompile(`^work:[0-9A-HJKMNP-TV-Z]{26}$`)
)

// Work derives the private retry identity for one durable work-report step.
// The domain, work ID, fixed-width semantic sequence, and action are all
// independently length-framed by the package's canonical v1 encoder. Local
// heartbeat activity deliberately has no action and therefore no key here.
// Work is part of the public package API.
func Work(workID string, semanticSequence int64, action string) (string, error) {
	if !workIDPattern.MatchString(workID) {
		return "", fmt.Errorf("%w: work id must be work:<ULID>", ErrInvalidWorkOperation)
	}
	if semanticSequence < 1 {
		return "", fmt.Errorf("%w: semantic sequence must be at least 1", ErrInvalidWorkOperation)
	}
	if !validWorkAction(action) {
		return "", fmt.Errorf("%w: action %q is not semantic", ErrInvalidWorkOperation, action)
	}

	encoder := newEncoder("work")
	encoder.add(workID)
	var sequence [8]byte
	binary.BigEndian.PutUint64(sequence[:], uint64(semanticSequence))
	encoder.addBytes(sequence[:])
	encoder.add(action)
	return encoder.key(), nil
}

func validWorkAction(action string) bool {
	switch action {
	case WorkActionStart, WorkActionCheckpoint, WorkActionWait, WorkActionStop:
		return true
	default:
		return false
	}
}

// Respond derives the operation key for one respond invocation. Parent order
// and field map iteration cannot affect the result; body bytes are represented
// only by their digest.
//
// refs are the envelope `refs[]` entries the response carries (`a2a respond
// --ref`), and they are encoded IN GIVEN ORDER, unlike parents and field keys.
// That asymmetry is deliberate: parents and fields are sets whose order is not
// on the wire, while `refs[]` is an ordered array in the envelope, so two calls
// differing only in ref order produce two different artifacts and must not
// dedup onto one key.
//
// They are an explicit parameter rather than a synthetic entry in `fields`.
// The first implementation smuggled them through as `fields["__respond_refs"]`
// to avoid widening this signature, and that is the wrong shape twice over: it
// hides part of what the operation IS inside a map documented as the caller's
// schema-field overrides, and it makes the magic key's uniqueness a thing
// somebody has to keep being right about.
// Respond is part of the public package API.
func Respond(system, actorKind, actorName string, parentIDs []string, result string, fields map[string]string, refs []string, body []byte) string {
	parents := append([]string(nil), parentIDs...)
	sort.Strings(parents)
	fieldKeys := make([]string, 0, len(fields))
	for key := range fields {
		fieldKeys = append(fieldKeys, key)
	}
	sort.Strings(fieldKeys)
	bodyDigest := sha256.Sum256(body)

	encoder := newEncoder("respond")
	encoder.add(system)
	encoder.add(actorKind)
	encoder.add(actorName)
	for _, parent := range parents {
		encoder.add(parent)
	}
	encoder.add(result)
	for _, key := range fieldKeys {
		encoder.add(key)
		encoder.add(fields[key])
	}
	encoder.addBytes(bodyDigest[:])
	// refs go AFTER the body digest, and the position is the disambiguation
	// rather than a stylistic choice. Every entry is length-prefixed, which
	// stops two strings from running together, but it does NOT separate two
	// variable-length SECTIONS: with refs written inside the fields region,
	// `fields{"X": "Y"}` and `refs["X", "Y"]` produce byte-identical input
	// and therefore one key. The first version of this did exactly that, and
	// the assertion below caught it. Nothing can follow the fields loop into
	// this position except refs, because the digest is a fixed 32 bytes that
	// always separates them.
	//
	// It also keeps every pre-existing key BYTE-IDENTICAL: an empty refs
	// writes nothing at all, so every caller that has no refs to give — MCP's
	// respond, the conformance driver, every historical operation — derives
	// exactly the key it derived before this parameter existed.
	for _, ref := range refs {
		encoder.add(ref)
	}
	return encoder.key()
}

// ContractDeprecate derives the operation key for deprecating one resolved
// contract version. Successor is intentionally included: changing the
// migration target is a different semantic write even though the legacy
// announcement-ID seed did not distinguish it.
// ContractDeprecate is part of the public package API.
func ContractDeprecate(system, contractID, version, successor, sunset string) string {
	encoder := newEncoder("contract-deprecate")
	encoder.add(system)
	encoder.add(contractID)
	encoder.add(version)
	encoder.add(successor)
	encoder.add(sunset)
	return encoder.key()
}

// Valid reports whether key is the canonical v1 full-SHA-256 representation.
func Valid(key string) bool {
	if len(key) != len(prefix)+sha256.Size*2 || key[:len(prefix)] != prefix {
		return false
	}
	_, err := hex.DecodeString(key[len(prefix):])
	return err == nil
}

type encoder struct {
	hash [sha256.Size]byte
	data []byte
}

func newEncoder(kind string) *encoder {
	encoder := &encoder{}
	encoder.add("a2ahub-operation-v1")
	encoder.add(kind)
	return encoder
}

func (e *encoder) add(value string) {
	e.addBytes([]byte(value))
}

func (e *encoder) addBytes(value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	e.data = append(e.data, length[:]...)
	e.data = append(e.data, value...)
}

func (e *encoder) key() string {
	e.hash = sha256.Sum256(e.data)
	return prefix + hex.EncodeToString(e.hash[:])
}
