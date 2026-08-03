package operational

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

func CanonicalJSON(snapshot Snapshot) ([]byte, error) {
	return json.Marshal(snapshot)
}

func semanticRevision(snapshot Snapshot) (string, error) {
	copy := snapshot
	copy.GeneratedAt = time.Time{}
	copy.Revision = ""
	copy.Sources = append([]Source(nil), snapshot.Sources...)
	for i := range copy.Sources {
		copy.Sources[i].ObservedAt = time.Time{}
	}
	copy.Timeline = append([]TimelineRow(nil), snapshot.Timeline...)
	for i := range copy.Timeline {
		copy.Timeline[i].Work = append([]Work(nil), snapshot.Timeline[i].Work...)
		for j := range copy.Timeline[i].Work {
			// A lease-store read observation is poll metadata, not a work
			// transition. Keep it in response bytes for the UI but exclude it
			// from the semantic revision just like source observation time.
			copy.Timeline[i].Work[j].ObservedAt = nil
		}
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", fmt.Errorf("operational: encode revision content: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
