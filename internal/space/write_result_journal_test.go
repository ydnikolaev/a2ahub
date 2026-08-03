package space

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

func TestWriteResultJournalCanonicalRoundTrip(t *testing.T) {
	t.Parallel()
	result := WriteResult{
		ArtifactIDs: []string{"XQ-one", "XE-two"}, Branch: "a2a/atlas/submit/op-v1-abc",
		CommitSHA: strings.Repeat("a", 40), MergeMethod: host.MergeMethodSquash,
		Note: "operator <checks> & retries \"safely\" — готово", PRNumber: 42,
		PRURL: "https://example.invalid/pr/42", Stage: WriteStagePRCreated,
		State: WriteStateNeedsAttention, RemainingAction: RemainingActionResolvePR,
		AutoMergeNote: "deprecated input must not be serialized",
	}
	raw, err := EncodeWriteResultJournal(result)
	if err != nil {
		t.Fatalf("EncodeWriteResultJournal: %v", err)
	}
	if bytes.Contains(raw, []byte(`\u003c`)) || bytes.Contains(raw, []byte("deprecated input")) {
		t.Fatalf("journal escaped or retained deprecated data: %s", raw)
	}
	decoded, err := DecodeWriteResultJournal(raw)
	if err != nil {
		t.Fatalf("DecodeWriteResultJournal: %v", err)
	}
	result.AutoMergeNote = result.Note
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("decoded = %+v, want %+v", decoded, result)
	}
}

func TestWriteResultJournalRejectsNonCanonicalAndInconsistentForms(t *testing.T) {
	t.Parallel()
	valid, err := EncodeWriteResultJournal(WriteResult{
		ArtifactIDs: []string{}, Branch: "a2a/atlas/submit/op-v1-abc", CommitSHA: strings.Repeat("a", 40),
		Stage: WriteStagePushed, RemainingAction: RemainingActionEnsurePR,
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"unknown":      bytes.Replace(valid, []byte(`"version":1`), []byte(`"unknown":0,"version":1`), 1),
		"duplicate":    bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"missing":      bytes.Replace(valid, []byte(`"note":"",`), nil, 1),
		"reordered":    bytes.Replace(valid, []byte(`"artifact_ids":[],"branch":"a2a/atlas/submit/op-v1-abc"`), []byte(`"branch":"a2a/atlas/submit/op-v1-abc","artifact_ids":[]`), 1),
		"nullable":     bytes.Replace(valid, []byte(`"artifact_ids":[]`), []byte(`"artifact_ids":null`), 1),
		"trailing":     append(append([]byte(nil), valid...), []byte("\n")...),
		"wrong action": bytes.Replace(valid, []byte(`"remaining_action":"ensure-pr"`), []byte(`"remaining_action":"retry-push"`), 1),
		"half pr":      bytes.Replace(valid, []byte(`"pr_number":0`), []byte(`"pr_number":7`), 1),
		"bad method":   bytes.Replace(valid, []byte(`"merge_method":""`), []byte(`"merge_method":"magic"`), 1),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeWriteResultJournal(raw); !errors.Is(err, ErrWriteResultJournalInvalid) {
				t.Fatalf("DecodeWriteResultJournal error = %v, want ErrWriteResultJournalInvalid; raw=%s", err, raw)
			}
		})
	}
}

func TestWriteResultJournalRejectsOversize(t *testing.T) {
	t.Parallel()
	raw := bytes.Repeat([]byte("x"), writeResultJournalMaxBytes+1)
	if _, err := DecodeWriteResultJournal(raw); !errors.Is(err, ErrWriteResultJournalInvalid) {
		t.Fatalf("DecodeWriteResultJournal error = %v, want bounded refusal", err)
	}
}
