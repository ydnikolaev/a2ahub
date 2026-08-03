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
func BranchName(system, verb, operationID string) string {
	return fmt.Sprintf("a2a/%s/%s/%s", system, verb, operationID)
}

const (
	WorkActionStart      = "start"
	WorkActionCheckpoint = "checkpoint"
	WorkActionWait       = "wait"
	WorkActionStop       = "stop"
)

var (
	// ErrInvalidWorkOperation marks work identity inputs that cannot name a
	// durable semantic step.
	ErrInvalidWorkOperation = errors.New("operation: invalid work operation")

	workIDPattern = regexp.MustCompile(`^work:[0-9A-HJKMNP-TV-Z]{26}$`)
)

// Work derives the private retry identity for one durable work-report step.
// The domain, work ID, fixed-width semantic sequence, and action are all
// independently length-framed by the package's canonical v1 encoder. Local
// heartbeat activity deliberately has no action and therefore no key here.
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
func Respond(system, actorKind, actorName string, parentIDs []string, result string, fields map[string]string, body []byte) string {
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
	return encoder.key()
}

// ContractDeprecate derives the operation key for deprecating one resolved
// contract version. Successor is intentionally included: changing the
// migration target is a different semantic write even though the legacy
// announcement-ID seed did not distinguish it.
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
