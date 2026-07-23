package feedback

import (
	"bytes"
	"regexp"
	"testing"
	"time"
)

var idPattern = regexp.MustCompile(`^fb-[0-9]{8}-[0-9a-f]{6}$`)

func TestMintFeedbackID_Pattern(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	id, err := MintFeedbackID(at, bytes.NewReader([]byte{0xde, 0xad, 0xbe}))
	if err != nil {
		t.Fatalf("MintFeedbackID: %v", err)
	}
	if !idPattern.MatchString(id) {
		t.Fatalf("MintFeedbackID = %q, does not match %s", id, idPattern)
	}
	if id != "fb-20260723-deadbe" {
		t.Fatalf("MintFeedbackID = %q, want fb-20260723-deadbe", id)
	}
}

func TestMintFeedbackID_Uniqueness(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		buf := make([]byte, 3)
		buf[0] = byte(i)
		buf[1] = byte(i * 7)
		buf[2] = byte(i * 13)
		id, err := MintFeedbackID(at, bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("MintFeedbackID: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q at i=%d", id, i)
		}
		seen[id] = true
	}
}

func TestMintFeedbackID_EntropyError(t *testing.T) {
	t.Parallel()
	at := time.Now()
	if _, err := MintFeedbackID(at, bytes.NewReader(nil)); err == nil {
		t.Fatal("expected an error when entropy is exhausted, got nil")
	}
}
