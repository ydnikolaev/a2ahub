package workreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// MarshalLease encodes the lease deterministically while splicing the two P4
// journals as raw JSON values. It never decodes or re-marshals those values.
// A schema-v1 lease predating audience persistence keeps both audience fields
// absent; encoding must not manufacture authorization the old record lacks.
// MarshalLease is part of the public package API.
func MarshalLease(lease Lease) ([]byte, error) {
	if err := validateLeaseStructure(lease); err != nil {
		return nil, err
	}
	return marshalLeaseUnchecked(lease)
}

func marshalLeaseUnchecked(lease Lease) ([]byte, error) {
	var out []byte
	out = append(out, '{')
	first := true
	fields := []struct {
		name  string
		value any
	}{
		{"schema_version", lease.SchemaVersion}, {"lease_key", lease.Identity.LeaseKey},
		{"project_id", lease.Identity.ProjectID}, {"space", lease.Identity.Space}, {"thread", lease.Identity.Thread},
		{"work_id", lease.Identity.WorkID}, {"subject_ref", lease.SubjectRef}, {"actor", lease.Identity.Actor},
		{"mode", lease.Mode}, {"summary", lease.Summary}, {"waiting_on", nonNilWaiting(lease.WaitingOn)},
	}
	if leaseAudienceKnown(lease) {
		fields = append(fields,
			struct {
				name  string
				value any
			}{"recipients", nonNilRecipients(lease.Recipients)},
			struct {
				name  string
				value any
			}{"classification", lease.Classification},
		)
	}
	fields = append(fields,
		struct {
			name  string
			value any
		}{"started_at", formatTime(lease.StartedAt)},
		struct {
			name  string
			value any
		}{"renewed_at", formatTime(lease.RenewedAt)},
		struct {
			name  string
			value any
		}{"expires_at", formatTime(lease.ExpiresAt)},
		struct {
			name  string
			value any
		}{"heartbeat_sequence", lease.HeartbeatSequence},
		struct {
			name  string
			value any
		}{"semantic_sequence", lease.SemanticSequence},
	)
	for _, field := range fields {
		if err := appendEncodedField(&out, &first, field.name, field.value); err != nil {
			return nil, err
		}
	}
	if lease.Pending != nil {
		pending, err := marshalPending(*lease.Pending)
		if err != nil {
			return nil, err
		}
		appendRawField(&out, &first, "pending", pending)
	}
	if lease.LastResult != nil {
		receipt, err := marshalReceipt(*lease.LastResult)
		if err != nil {
			return nil, err
		}
		appendRawField(&out, &first, "last_result", receipt)
	}
	out = append(out, '}')
	if len(out) > MaximumEncodedLease {
		return nil, fmt.Errorf("%w: encoded lease is %d bytes", ErrLeaseTooLarge, len(out))
	}
	return out, nil
}

func marshalReceipt(receipt PublicationReceipt) ([]byte, error) {
	if err := validateOpaqueJSON(receipt.WriteResult.raw, MaximumEncodedLease, "write result journal"); err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, '{')
	first := true
	fields := []struct {
		name  string
		value any
	}{
		{"operation_key", receipt.OperationKey}, {"action", receipt.Action}, {"artifact_id", receipt.ArtifactID},
		{"event_id", receipt.EventID}, {"semantic_sequence", receipt.SemanticSequence},
		{"prepared_digest", receipt.PreparedDigest},
	}
	for _, field := range fields {
		if err := appendEncodedField(&out, &first, field.name, field.value); err != nil {
			return nil, err
		}
	}
	appendRawField(&out, &first, "write_result", receipt.WriteResult.raw)
	if err := appendEncodedField(&out, &first, "convergence", receipt.Convergence); err != nil {
		return nil, err
	}
	out = append(out, '}')
	return out, nil
}

func nonNilWaiting(waiting []WaitingOn) []WaitingOn {
	if waiting == nil {
		return []WaitingOn{}
	}
	return waiting
}

func nonNilRecipients(recipients []string) []string {
	if recipients == nil {
		return []string{}
	}
	return recipients
}

func appendEncodedField(out *[]byte, first *bool, name string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode %s: %w", ErrInvalidLease, name, err)
	}
	appendRawField(out, first, name, encoded)
	return nil
}

func appendRawField(out *[]byte, first *bool, name string, value []byte) {
	if !*first {
		*out = append(*out, ',')
	}
	*first = false
	*out = strconv.AppendQuote(*out, name)
	*out = append(*out, ':')
	*out = append(*out, value...)
}

func marshalPending(pending PendingOperation) ([]byte, error) {
	if err := validateOpaqueJSON(pending.Prepared.raw, MaximumPrepared, "prepared journal"); err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, '{')
	first := true
	fields := []struct {
		name  string
		value any
	}{
		{"operation_key", pending.OperationKey}, {"action", pending.Action}, {"artifact_id", pending.ArtifactID},
		{"event_id", pending.EventID}, {"semantic_sequence", pending.SemanticSequence},
	}
	for _, field := range fields {
		if err := appendEncodedField(&out, &first, field.name, field.value); err != nil {
			return nil, err
		}
	}
	appendRawField(&out, &first, "prepared_submission", pending.Prepared.raw)
	if err := appendEncodedField(&out, &first, "local_target", pending.LocalTarget); err != nil {
		return nil, err
	}
	shared, err := marshalAttempt(pending.Shared)
	if err != nil {
		return nil, err
	}
	appendRawField(&out, &first, "shared", shared)
	if pending.LastErrorCode != "" {
		if err := appendEncodedField(&out, &first, "last_error_code", pending.LastErrorCode); err != nil {
			return nil, err
		}
	}
	out = append(out, '}')
	return out, nil
}

func marshalAttempt(attempt PublishAttempt) ([]byte, error) {
	var out []byte
	out = append(out, '{')
	first := true
	if err := appendEncodedField(&out, &first, "attempted", attempt.Attempted); err != nil {
		return nil, err
	}
	if attempt.WriteResult.Len() != 0 {
		if err := validateOpaqueJSON(attempt.WriteResult.raw, MaximumEncodedLease, "write result journal"); err != nil {
			return nil, err
		}
		appendRawField(&out, &first, "write_result", attempt.WriteResult.raw)
	}
	if attempt.Convergence != "" {
		if err := appendEncodedField(&out, &first, "convergence", attempt.Convergence); err != nil {
			return nil, err
		}
	}
	out = append(out, '}')
	return out, nil
}

// UnmarshalLease is part of the public package API.
func UnmarshalLease(encoded []byte) (Lease, error) {
	if len(encoded) == 0 || len(encoded) > MaximumEncodedLease || !json.Valid(encoded) {
		return Lease{}, fmt.Errorf("%w: malformed or oversized JSON", ErrInvalidLease)
	}
	if err := rejectDuplicateObjectKeys(encoded); err != nil {
		return Lease{}, err
	}
	if err := rejectUnknown(encoded, []string{
		"schema_version", "lease_key", "project_id", "space", "thread", "work_id", "subject_ref", "actor",
		"mode", "summary", "waiting_on", "started_at", "renewed_at", "expires_at", "heartbeat_sequence",
		"semantic_sequence", "recipients", "classification", "pending", "last_result",
	}); err != nil {
		return Lease{}, err
	}
	if err := requireFields(encoded, []string{
		"schema_version", "lease_key", "project_id", "space", "thread", "work_id", "subject_ref", "actor",
		"mode", "summary", "waiting_on", "started_at", "renewed_at", "expires_at", "heartbeat_sequence",
		"semantic_sequence",
	}); err != nil {
		return Lease{}, err
	}
	type wire struct {
		SchemaVersion     int             `json:"schema_version"`
		LeaseKey          string          `json:"lease_key"`
		ProjectID         string          `json:"project_id"`
		Space             string          `json:"space"`
		Thread            string          `json:"thread"`
		WorkID            string          `json:"work_id"`
		SubjectRef        string          `json:"subject_ref"`
		Actor             json.RawMessage `json:"actor"`
		Mode              Mode            `json:"mode"`
		Summary           string          `json:"summary"`
		WaitingOn         json.RawMessage `json:"waiting_on"`
		Recipients        []string        `json:"recipients"`
		Classification    string          `json:"classification"`
		StartedAt         string          `json:"started_at"`
		RenewedAt         string          `json:"renewed_at"`
		ExpiresAt         string          `json:"expires_at"`
		HeartbeatSequence uint64          `json:"heartbeat_sequence"`
		SemanticSequence  uint64          `json:"semantic_sequence"`
		Pending           json.RawMessage `json:"pending"`
		LastResult        json.RawMessage `json:"last_result"`
	}
	var decoded wire
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return Lease{}, fmt.Errorf("%w: %w", ErrInvalidLease, err)
	}
	if err := rejectDuplicateObjectKeys(decoded.Actor); err != nil {
		return Lease{}, err
	}
	if err := rejectUnknown(decoded.Actor, []string{"kind", "name", "system", "model", "session"}); err != nil {
		return Lease{}, err
	}
	var actor Actor
	if err := json.Unmarshal(decoded.Actor, &actor); err != nil {
		return Lease{}, fmt.Errorf("%w: invalid actor", ErrInvalidLease)
	}
	waitingOn, err := unmarshalWaiting(decoded.WaitingOn)
	if err != nil {
		return Lease{}, err
	}
	startedAt, err := parseTime(decoded.StartedAt)
	if err != nil {
		return Lease{}, err
	}
	renewedAt, err := parseTime(decoded.RenewedAt)
	if err != nil {
		return Lease{}, err
	}
	expiresAt, err := parseTime(decoded.ExpiresAt)
	if err != nil {
		return Lease{}, err
	}
	lease := Lease{
		SchemaVersion: decoded.SchemaVersion,
		Identity: Identity{
			LeaseKey: decoded.LeaseKey, ProjectID: decoded.ProjectID, Space: decoded.Space,
			Thread: decoded.Thread, WorkID: decoded.WorkID, Actor: actor,
		},
		SubjectRef: decoded.SubjectRef, Mode: decoded.Mode, Summary: decoded.Summary,
		WaitingOn: waitingOn, StartedAt: startedAt, RenewedAt: renewedAt, ExpiresAt: expiresAt,
		Recipients: append([]string(nil), decoded.Recipients...), Classification: decoded.Classification,
		HeartbeatSequence: decoded.HeartbeatSequence, SemanticSequence: decoded.SemanticSequence,
	}
	if len(decoded.Pending) != 0 && !bytes.Equal(decoded.Pending, []byte("null")) {
		pending, err := unmarshalPending(decoded.Pending)
		if err != nil {
			return Lease{}, err
		}
		lease.Pending = &pending
		lease.Closing = pending.LocalTarget == TargetClosing
	}
	if len(decoded.LastResult) != 0 && !bytes.Equal(decoded.LastResult, []byte("null")) {
		receipt, err := unmarshalReceipt(decoded.LastResult)
		if err != nil {
			return Lease{}, err
		}
		lease.LastResult = &receipt
	}
	if err := ValidateLease(lease); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func unmarshalReceipt(encoded []byte) (PublicationReceipt, error) {
	fields := []string{
		"operation_key", "action", "artifact_id", "event_id", "semantic_sequence", "prepared_digest", "write_result", "convergence",
	}
	if err := rejectDuplicateObjectKeys(encoded); err != nil {
		return PublicationReceipt{}, err
	}
	if err := rejectUnknown(encoded, fields); err != nil {
		return PublicationReceipt{}, err
	}
	if err := requireFields(encoded, fields); err != nil {
		return PublicationReceipt{}, err
	}
	type wire struct {
		OperationKey     string          `json:"operation_key"`
		Action           Action          `json:"action"`
		ArtifactID       string          `json:"artifact_id"`
		EventID          string          `json:"event_id"`
		SemanticSequence uint64          `json:"semantic_sequence"`
		PreparedDigest   string          `json:"prepared_digest"`
		WriteResult      json.RawMessage `json:"write_result"`
		Convergence      Convergence     `json:"convergence"`
	}
	var decoded wire
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return PublicationReceipt{}, fmt.Errorf("%w: invalid result receipt", ErrInvalidLease)
	}
	result, err := NewWriteResultJournal(decoded.WriteResult)
	if err != nil {
		return PublicationReceipt{}, err
	}
	return PublicationReceipt{
		OperationKey: decoded.OperationKey, Action: decoded.Action, ArtifactID: decoded.ArtifactID,
		EventID: decoded.EventID, SemanticSequence: decoded.SemanticSequence, PreparedDigest: decoded.PreparedDigest,
		WriteResult: result, Convergence: decoded.Convergence,
	}, nil
}

func unmarshalPending(encoded []byte) (PendingOperation, error) {
	if err := rejectDuplicateObjectKeys(encoded); err != nil {
		return PendingOperation{}, err
	}
	if err := rejectUnknown(encoded, []string{
		"operation_key", "action", "artifact_id", "event_id", "semantic_sequence", "prepared_submission",
		"local_target", "shared", "last_error_code",
	}); err != nil {
		return PendingOperation{}, err
	}
	if err := requireFields(encoded, []string{
		"operation_key", "action", "artifact_id", "event_id", "semantic_sequence", "prepared_submission", "local_target", "shared",
	}); err != nil {
		return PendingOperation{}, err
	}
	type wire struct {
		OperationKey       string          `json:"operation_key"`
		Action             Action          `json:"action"`
		ArtifactID         string          `json:"artifact_id"`
		EventID            string          `json:"event_id"`
		SemanticSequence   uint64          `json:"semantic_sequence"`
		PreparedSubmission json.RawMessage `json:"prepared_submission"`
		LocalTarget        LocalTarget     `json:"local_target"`
		Shared             json.RawMessage `json:"shared"`
		LastErrorCode      string          `json:"last_error_code"`
	}
	var decoded wire
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return PendingOperation{}, fmt.Errorf("%w: invalid pending operation", ErrInvalidLease)
	}
	prepared, err := NewPreparedJournal(decoded.PreparedSubmission)
	if err != nil {
		return PendingOperation{}, err
	}
	attempt, err := unmarshalAttempt(decoded.Shared)
	if err != nil {
		return PendingOperation{}, err
	}
	attempt.ErrorCode = decoded.LastErrorCode
	return PendingOperation{
		OperationKey: decoded.OperationKey, Action: decoded.Action, ArtifactID: decoded.ArtifactID,
		EventID: decoded.EventID, SemanticSequence: decoded.SemanticSequence, Prepared: prepared,
		LocalTarget: decoded.LocalTarget, Shared: attempt, LastErrorCode: decoded.LastErrorCode,
	}, nil
}

func unmarshalAttempt(encoded []byte) (PublishAttempt, error) {
	if err := rejectDuplicateObjectKeys(encoded); err != nil {
		return PublishAttempt{}, err
	}
	if err := rejectUnknown(encoded, []string{"attempted", "write_result", "convergence"}); err != nil {
		return PublishAttempt{}, err
	}
	if err := requireFields(encoded, []string{"attempted"}); err != nil {
		return PublishAttempt{}, err
	}
	type wire struct {
		Attempted   bool            `json:"attempted"`
		WriteResult json.RawMessage `json:"write_result"`
		Convergence Convergence     `json:"convergence"`
	}
	var decoded wire
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return PublishAttempt{}, fmt.Errorf("%w: invalid shared attempt", ErrInvalidLease)
	}
	attempt := PublishAttempt{Attempted: decoded.Attempted, Convergence: decoded.Convergence}
	if len(decoded.WriteResult) != 0 && !bytes.Equal(decoded.WriteResult, []byte("null")) {
		result, err := NewWriteResultJournal(decoded.WriteResult)
		if err != nil {
			return PublishAttempt{}, err
		}
		attempt.WriteResult = result
	}
	return attempt, nil
}

func rejectUnknown(encoded []byte, allowed []string) error {
	var object map[string]json.RawMessage
	if len(encoded) == 0 || json.Unmarshal(encoded, &object) != nil || object == nil {
		return fmt.Errorf("%w: expected JSON object", ErrInvalidLease)
	}
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range object {
		if _, ok := set[key]; !ok {
			return fmt.Errorf("%w: unknown field %q", ErrInvalidLease, key)
		}
	}
	return nil
}

func requireFields(encoded []byte, required []string) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(encoded, &object) != nil || object == nil {
		return fmt.Errorf("%w: expected JSON object", ErrInvalidLease)
	}
	for _, key := range required {
		if _, ok := object[key]; !ok {
			return fmt.Errorf("%w: missing field %q", ErrInvalidLease, key)
		}
	}
	return nil
}

func unmarshalWaiting(encoded []byte) ([]WaitingOn, error) {
	if len(encoded) == 0 || bytes.Equal(encoded, []byte("null")) {
		return nil, fmt.Errorf("%w: waiting_on must be an array", ErrInvalidLease)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(encoded, &entries); err != nil || entries == nil {
		return nil, fmt.Errorf("%w: waiting_on must be an array", ErrInvalidLease)
	}
	waiting := make([]WaitingOn, 0, len(entries))
	for _, entry := range entries {
		if err := rejectDuplicateObjectKeys(entry); err != nil {
			return nil, err
		}
		if err := rejectUnknown(entry, []string{"kind", "id", "summary"}); err != nil {
			return nil, err
		}
		if err := requireFields(entry, []string{"kind", "id"}); err != nil {
			return nil, err
		}
		var decoded WaitingOn
		if err := json.Unmarshal(entry, &decoded); err != nil {
			return nil, fmt.Errorf("%w: invalid waiting reason", ErrInvalidLease)
		}
		waiting = append(waiting, decoded)
	}
	return waiting, nil
}

// rejectDuplicateObjectKeys checks only the keys of the supplied object. It
// deliberately skips nested value contents so opaque P4 journals remain under
// the strict adapter's codec rather than acquiring a second parser here.
func rejectDuplicateObjectKeys(encoded []byte) error {
	position := skipJSONSpace(encoded, 0)
	if position >= len(encoded) || encoded[position] != '{' {
		return fmt.Errorf("%w: expected JSON object", ErrInvalidLease)
	}
	position++
	seen := make(map[string]struct{})
	for {
		position = skipJSONSpace(encoded, position)
		if position >= len(encoded) {
			return fmt.Errorf("%w: unterminated JSON object", ErrInvalidLease)
		}
		if encoded[position] == '}' {
			return nil
		}
		keyStart := position
		keyEnd, err := skipJSONString(encoded, position)
		if err != nil {
			return err
		}
		var key string
		if err := json.Unmarshal(encoded[keyStart:keyEnd], &key); err != nil {
			return fmt.Errorf("%w: invalid object key", ErrInvalidLease)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate field %q", ErrInvalidLease, key)
		}
		seen[key] = struct{}{}
		position = skipJSONSpace(encoded, keyEnd)
		if position >= len(encoded) || encoded[position] != ':' {
			return fmt.Errorf("%w: missing object colon", ErrInvalidLease)
		}
		position = skipJSONSpace(encoded, position+1)
		position, err = skipJSONValue(encoded, position)
		if err != nil {
			return err
		}
		position = skipJSONSpace(encoded, position)
		if position >= len(encoded) {
			return fmt.Errorf("%w: unterminated JSON object", ErrInvalidLease)
		}
		switch encoded[position] {
		case ',':
			position++
		case '}':
			return nil
		default:
			return fmt.Errorf("%w: malformed JSON object", ErrInvalidLease)
		}
	}
}

func skipJSONSpace(encoded []byte, position int) int {
	for position < len(encoded) {
		switch encoded[position] {
		case ' ', '\t', '\r', '\n':
			position++
		default:
			return position
		}
	}
	return position
}

func skipJSONString(encoded []byte, position int) (int, error) {
	if position >= len(encoded) || encoded[position] != '"' {
		return 0, fmt.Errorf("%w: expected JSON string", ErrInvalidLease)
	}
	position++
	for position < len(encoded) {
		switch encoded[position] {
		case '"':
			return position + 1, nil
		case '\\':
			position += 2
		default:
			position++
		}
	}
	return 0, fmt.Errorf("%w: unterminated JSON string", ErrInvalidLease)
}

func skipJSONValue(encoded []byte, position int) (int, error) {
	if position >= len(encoded) {
		return 0, fmt.Errorf("%w: missing JSON value", ErrInvalidLease)
	}
	if encoded[position] == '"' {
		return skipJSONString(encoded, position)
	}
	if encoded[position] != '{' && encoded[position] != '[' {
		for position < len(encoded) && encoded[position] != ',' && encoded[position] != '}' {
			position++
		}
		return position, nil
	}
	stack := []byte{encoded[position]}
	position++
	for position < len(encoded) && len(stack) > 0 {
		switch encoded[position] {
		case '"':
			next, err := skipJSONString(encoded, position)
			if err != nil {
				return 0, err
			}
			position = next
			continue
		case '{', '[':
			stack = append(stack, encoded[position])
		case '}':
			if stack[len(stack)-1] != '{' {
				return 0, fmt.Errorf("%w: mismatched JSON delimiter", ErrInvalidLease)
			}
			stack = stack[:len(stack)-1]
		case ']':
			if stack[len(stack)-1] != '[' {
				return 0, fmt.Errorf("%w: mismatched JSON delimiter", ErrInvalidLease)
			}
			stack = stack[:len(stack)-1]
		}
		position++
	}
	if len(stack) != 0 {
		return 0, fmt.Errorf("%w: unterminated JSON value", ErrInvalidLease)
	}
	return position, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	_, offset := parsed.Zone()
	if err != nil || offset != 0 {
		return time.Time{}, fmt.Errorf("%w: timestamp must be RFC3339 UTC", ErrInvalidLease)
	}
	return parsed.UTC(), nil
}
