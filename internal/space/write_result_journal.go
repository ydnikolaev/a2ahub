package space

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

const writeResultJournalMaxBytes = 16 * 1024

var ErrWriteResultJournalInvalid = errors.New("space: invalid write result journal")

type writeResultJournalV1 struct {
	Version int               `json:"version"`
	Result  writeResultWireV1 `json:"result"`
}

type writeResultWireV1 struct {
	ArtifactIDs     []string         `json:"artifact_ids"`
	Branch          string           `json:"branch"`
	CommitSHA       string           `json:"commit_sha"`
	MergeMethod     host.MergeMethod `json:"merge_method"`
	Note            string           `json:"note"`
	PRNumber        int              `json:"pr_number"`
	PRURL           string           `json:"pr_url"`
	RemainingAction RemainingAction  `json:"remaining_action"`
	Stage           WriteStage       `json:"stage"`
	State           WriteState       `json:"state"`
}

// EncodeWriteResultJournal freezes the complete result/error axis without
// changing WriteResult's public JSON projection or retaining deprecated
// compatibility aliases.
func EncodeWriteResultJournal(result WriteResult) ([]byte, error) {
	if err := validateJournalWriteResult(result); err != nil {
		return nil, err
	}
	wire := writeResultJournalV1{Version: 1, Result: resultToWire(result)}
	return encodeCanonicalWriteResultJournal(wire)
}

// DecodeWriteResultJournal accepts only the exact canonical v1 object. This
// rejects missing, unknown, duplicate, reordered, nullable and trailing data
// rather than allowing Go zero values to fill an ambiguous checkpoint.
func DecodeWriteResultJournal(raw []byte) (WriteResult, error) {
	if len(raw) == 0 || len(raw) > writeResultJournalMaxBytes {
		return WriteResult{}, ErrWriteResultJournalInvalid
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return WriteResult{}, fmt.Errorf("%w: %v", ErrWriteResultJournalInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire writeResultJournalV1
	if err := decoder.Decode(&wire); err != nil || wire.Version != 1 {
		return WriteResult{}, ErrWriteResultJournalInvalid
	}
	if wire.Result.ArtifactIDs == nil {
		return WriteResult{}, ErrWriteResultJournalInvalid
	}
	canonical, err := encodeCanonicalWriteResultJournal(wire)
	if err != nil || !bytes.Equal(raw, canonical) {
		return WriteResult{}, ErrWriteResultJournalInvalid
	}
	result := WriteResult{
		ArtifactIDs: append([]string(nil), wire.Result.ArtifactIDs...), Branch: wire.Result.Branch,
		CommitSHA: wire.Result.CommitSHA, MergeMethod: wire.Result.MergeMethod, Note: wire.Result.Note,
		PRNumber: wire.Result.PRNumber, PRURL: wire.Result.PRURL, RemainingAction: wire.Result.RemainingAction,
		Stage: wire.Result.Stage, State: wire.Result.State,
	}
	if err := validateJournalWriteResult(result); err != nil {
		return WriteResult{}, err
	}
	result.AutoMergeNote = result.Note
	return result, nil
}

func resultToWire(result WriteResult) writeResultWireV1 {
	ids := append([]string(nil), result.ArtifactIDs...)
	if ids == nil {
		ids = []string{}
	}
	return writeResultWireV1{
		ArtifactIDs: ids, Branch: result.Branch, CommitSHA: result.CommitSHA,
		MergeMethod: result.MergeMethod, Note: result.Note, PRNumber: result.PRNumber, PRURL: result.PRURL,
		RemainingAction: result.RemainingAction, Stage: result.Stage, State: result.State,
	}
}

func encodeCanonicalWriteResultJournal(wire writeResultJournalV1) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWriteResultJournalInvalid, err)
	}
	raw := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if len(raw) > writeResultJournalMaxBytes {
		return nil, ErrWriteResultJournalInvalid
	}
	return append([]byte(nil), raw...), nil
}

func validateJournalWriteResult(result WriteResult) error {
	action, err := RemainingActionFor(result.Stage, result.State)
	if err != nil || action != result.RemainingAction || result.PRNumber < 0 ||
		(result.PRNumber == 0) != (result.PRURL == "") {
		return ErrWriteResultJournalInvalid
	}
	switch result.MergeMethod {
	case "", host.MergeMethodMerge, host.MergeMethodSquash, host.MergeMethodRebase:
	default:
		return ErrWriteResultJournalInvalid
	}
	if result.State == WriteStateNeedsAttention && result.Note == "" {
		return ErrWriteResultJournalInvalid
	}
	return nil
}
