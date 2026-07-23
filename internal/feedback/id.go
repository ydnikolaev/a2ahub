package feedback

import (
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

// MintFeedbackID mints a feedback report id: fb-YYYYMMDD-<6 lowercase
// hex> (feedback.schema.json's `^fb-[0-9]{8}-[0-9a-f]{6}$` pattern, §T2).
// at and entropy are caller-supplied (the artifact.MintULIDAt/
// MintExchangeIDAt idiom this repo already uses) so callers get a
// deterministic, testable seam — production passes time.Now() and
// crypto/rand.Reader.
func MintFeedbackID(at time.Time, entropy io.Reader) (string, error) {
	const op = "MintFeedbackID"
	buf := make([]byte, 3)
	if _, err := io.ReadFull(entropy, buf); err != nil {
		return "", fmt.Errorf("feedback: %s: %w", op, err)
	}
	return fmt.Sprintf("fb-%s-%s", at.UTC().Format("20060102"), hex.EncodeToString(buf)), nil
}
