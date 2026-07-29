// Package operation derives stable, private idempotency keys from semantic
// write intent. Keys identify an operation across retries; they are not
// protocol artifact IDs and never replace the date-bearing public ID grammar.
package operation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
)

const prefix = "op-v1-"

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
