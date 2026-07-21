package artifact

import (
	"crypto/sha256"
	"encoding/hex"
)

// Digest returns the SHA-256 digest of raw's bytes as-committed in its
// string form `sha256:<full-hex>` (§5.7, D-029). The string form is never
// truncated — display MAY truncate downstream, storage MUST NOT. Digests
// are computed on demand; this package never persists one into an
// artifact.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
