package workreport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJournalsAreOpaqueClonedAndCanonical(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"b":2,"a":1}`)
	journal, err := NewPreparedJournal(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[2] = 'x'
	first := journal.Bytes()
	first[2] = 'y'
	if got := string(journal.Bytes()); got != `{"b":2,"a":1}` {
		t.Fatalf("journal mutated: %s", got)
	}
	for _, invalid := range [][]byte{nil, []byte(" {}"), []byte("[]"), []byte(`{"x":1} `), []byte(`{"x":`)} {
		if _, err := NewPreparedJournal(invalid); err == nil {
			t.Fatalf("NewPreparedJournal(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestLeaseCodecPreservesOpaqueJSONBytesExactly(t *testing.T) {
	t.Parallel()
	preparedRaw := []byte(`{"z":[3,2,1],"a":{"kept":"exact"}}`)
	resultRaw := []byte(`{"stage":"pr-created","detail":{"order":[2,1]}}`)
	prepared, err := NewPreparedJournal(preparedRaw)
	if err != nil {
		t.Fatal(err)
	}
	writeResult, err := NewWriteResultJournal(resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	operationKey, err := expectedOperationKey(1, ActionStart, testIdentity().WorkID)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-checkout-20260803-abcd",
		Mode: ModeImplementing, Summary: "Implementing", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL),
		HeartbeatSequence: 1, SemanticSequence: 1,
		Pending: &PendingOperation{
			OperationKey: operationKey, Action: ActionStart, ArtifactID: "XA-TEST-1",
			EventID: "01K1A2B3C4D5E6F7G8H9J0K1M2", SemanticSequence: 1, Prepared: prepared,
			LocalTarget: TargetActive,
			Shared:      PublishAttempt{Attempted: true, WriteResult: writeResult, Convergence: ConvergenceResumable},
		},
	}
	encoded, err := MarshalLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, preparedRaw) || !bytes.Contains(encoded, resultRaw) {
		t.Fatalf("opaque journals were rewritten: %s", encoded)
	}
	decoded, err := UnmarshalLease(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Pending.Prepared.Bytes(), preparedRaw) || !bytes.Equal(decoded.Pending.Shared.WriteResult.Bytes(), resultRaw) {
		t.Fatal("opaque journals changed across round trip")
	}
	reencoded, err := MarshalLease(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("codec is not canonical\nfirst:  %s\nsecond: %s", encoded, reencoded)
	}
}

func TestUnmarshalLeasePreservesUnknownLegacyAudienceWithoutInventingAuthority(t *testing.T) {
	t.Parallel()
	lease := coordinatorLease(1)
	encoded, err := MarshalLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, "recipients")
	delete(object, "classification")
	legacy, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalLease(legacy)
	if err != nil {
		t.Fatalf("legacy lease rejected: %v", err)
	}
	if len(decoded.Recipients) != 0 || decoded.Classification != "" {
		t.Fatalf("legacy audience was invented = %v/%q", decoded.Recipients, decoded.Classification)
	}
	rewritten, err := MarshalLease(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rewritten, []byte(`"recipients"`)) || bytes.Contains(rewritten, []byte(`"classification"`)) {
		t.Fatalf("rewritten legacy lease invented authority: %s", rewritten)
	}
	for _, field := range []string{"recipients", "classification"} {
		partial := make(map[string]json.RawMessage, len(object)+1)
		for key, value := range object {
			partial[key] = value
		}
		if field == "recipients" {
			partial[field] = json.RawMessage(`["all"]`)
		} else {
			partial[field] = json.RawMessage(`"internal"`)
		}
		raw, marshalErr := json.Marshal(partial)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, unmarshalErr := UnmarshalLease(raw); unmarshalErr == nil {
			t.Fatalf("partially persisted legacy audience accepted: field=%s", field)
		}
	}
}

func TestLeaseCodecPreservesCompletedResultReceiptExactly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	prepared := testPrepared(t, ActionStart, 1, "receipt")
	resultRaw := []byte(`{"z":9,"a":{"order":[3,1,2]}}`)
	writeResult, err := NewWriteResultJournal(resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL),
		HeartbeatSequence: 1, SemanticSequence: 1,
		LastResult: &PublicationReceipt{
			OperationKey: prepared.OperationKey, Action: prepared.Action, ArtifactID: prepared.ArtifactID,
			EventID: prepared.EventID, SemanticSequence: 1, PreparedDigest: digestPrepared(prepared.Prepared),
			WriteResult: writeResult, Convergence: ConvergenceAccepted,
		},
	}
	encoded, err := MarshalLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, resultRaw) {
		t.Fatal("completed receipt result bytes were rewritten")
	}
	decoded, err := UnmarshalLease(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.LastResult == nil || !bytes.Equal(decoded.LastResult.WriteResult.Bytes(), resultRaw) {
		t.Fatal("completed receipt result changed across round trip")
	}
}

func TestValidateLeaseBindsCanonicalOperationKeyAndRejectsSequenceOverflow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	prepared := testPrepared(t, ActionStart, 1, "one")
	base := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL), HeartbeatSequence: 1,
		SemanticSequence: 1, Pending: newPending(prepared),
	}
	tests := []struct {
		name   string
		mutate func(*Lease)
	}{
		{name: "arbitrary regex-valid key", mutate: func(lease *Lease) { lease.Pending.OperationKey = "op-v1-" + repeat("f", 64) }},
		{name: "changed work id", mutate: func(lease *Lease) { lease.Identity.WorkID = "work:01ARZ3NDEKTSV4RRFFQ69G5FAV" }},
		{name: "uint64 to int64 overflow", mutate: func(lease *Lease) {
			lease.SemanticSequence = uint64(^uint64(0)>>1) + 1
			lease.Pending.SemanticSequence = lease.SemanticSequence
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lease := base.Clone()
			test.mutate(&lease)
			if err := ValidateLease(lease); err == nil {
				t.Fatal("operation identity mismatch unexpectedly passed")
			}
		})
	}
}

func TestUnmarshalLeaseRejectsUnknownFieldsAtEveryOwnedLevel(t *testing.T) {
	t.Parallel()
	prepared := testPrepared(t, ActionStart, 1, "one")
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	lease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL), HeartbeatSequence: 1,
		SemanticSequence: 1, Pending: newPending(prepared),
	}
	encoded, err := MarshalLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "root", old: `{"schema_version":1`, new: `{"unknown":true,"schema_version":1`},
		{name: "actor", old: `"actor":{"kind":`, new: `"actor":{"unknown":true,"kind":`},
		{name: "pending", old: `"pending":{"operation_key":`, new: `"pending":{"unknown":true,"operation_key":`},
		{name: "shared", old: `"shared":{"attempted":`, new: `"shared":{"unknown":true,"attempted":`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mutated := []byte(strings.Replace(string(encoded), test.old, test.new, 1))
			if _, err := UnmarshalLease(mutated); err == nil {
				t.Fatalf("unknown %s field accepted", test.name)
			}
		})
	}
}

func TestUnmarshalLeaseRejectsMissingAndUnknownWaitingFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	prepared := testPrepared(t, ActionWait, 2, "wait")
	lease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeWaiting,
		Summary: "waiting", WaitingOn: []WaitingOn{{Kind: WaitSystem, ID: "axon"}},
		StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL), HeartbeatSequence: 1,
		SemanticSequence: 2, Pending: newPending(prepared),
	}
	encoded, err := MarshalLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []string{
		strings.Replace(string(encoded), `{"kind":"system"`, `{"unknown":true,"kind":"system"`, 1),
		strings.Replace(string(encoded), `"id":"axon"`, `"missing_id":"axon"`, 1),
		strings.Replace(string(encoded), `"waiting_on":[`, `"wrong_waiting":[`, 1),
	}
	for index, mutated := range mutations {
		if _, err := UnmarshalLease([]byte(mutated)); err == nil {
			t.Fatalf("mutation %d unexpectedly passed", index)
		}
	}
}

func TestUnmarshalLeaseRejectsDuplicateKeysAtEveryOwnedObjectLevel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	pendingLease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeWaiting,
		Summary: "waiting", WaitingOn: []WaitingOn{{Kind: WaitSystem, ID: "axon"}},
		StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL), HeartbeatSequence: 1,
		SemanticSequence: 2, Pending: newPending(testPrepared(t, ActionWait, 2, "wait")),
	}
	pendingEncoded, err := MarshalLease(pendingLease)
	if err != nil {
		t.Fatal(err)
	}
	receiptPrepared := testPrepared(t, ActionStart, 1, "receipt")
	receiptResult, err := NewWriteResultJournal([]byte(`{"stage":"accepted"}`))
	if err != nil {
		t.Fatal(err)
	}
	receiptLease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL), HeartbeatSequence: 1,
		SemanticSequence: 1, LastResult: &PublicationReceipt{
			OperationKey: receiptPrepared.OperationKey, Action: ActionStart, ArtifactID: receiptPrepared.ArtifactID,
			EventID: receiptPrepared.EventID, SemanticSequence: 1, PreparedDigest: digestPrepared(receiptPrepared.Prepared),
			WriteResult: receiptResult, Convergence: ConvergenceAccepted,
		},
	}
	receiptEncoded, err := MarshalLease(receiptLease)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		encoded []byte
		old     string
		new     string
	}{
		{name: "root", encoded: pendingEncoded, old: `{"schema_version":1`, new: `{"space":"duplicate","schema_version":1`},
		{name: "actor", encoded: pendingEncoded, old: `"actor":{"kind":`, new: `"actor":{"name":"duplicate","kind":`},
		{name: "waiting entry", encoded: pendingEncoded, old: `"waiting_on":[{"kind":`, new: `"waiting_on":[{"kind":"tool","kind":`},
		{name: "pending", encoded: pendingEncoded, old: `"pending":{"operation_key":`, new: `"pending":{"action":"stop","operation_key":`},
		{name: "shared", encoded: pendingEncoded, old: `"shared":{"attempted":`, new: `"shared":{"attempted":true,"attempted":`},
		{name: "last result", encoded: receiptEncoded, old: `"last_result":{"operation_key":`, new: `"last_result":{"convergence":"terminal","operation_key":`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mutated := []byte(strings.Replace(string(test.encoded), test.old, test.new, 1))
			if bytes.Equal(mutated, test.encoded) {
				t.Fatal("test mutation did not apply")
			}
			if _, err := UnmarshalLease(mutated); err == nil {
				t.Fatal("duplicate key unexpectedly accepted")
			}
		})
	}
}

func TestLeaseDuplicateCheckDoesNotInspectOpaqueP4Journals(t *testing.T) {
	t.Parallel()
	preparedRaw := []byte(`{"owned_by_p4":1,"owned_by_p4":2}`)
	resultRaw := []byte(`{"owned_by_p4":"first","owned_by_p4":"second"}`)
	prepared, err := NewPreparedJournal(preparedRaw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewWriteResultJournal(resultRaw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	operationKey, err := expectedOperationKey(1, ActionStart, testIdentity().WorkID)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL),
		HeartbeatSequence: 1, SemanticSequence: 1, Pending: &PendingOperation{
			OperationKey: operationKey, Action: ActionStart, ArtifactID: "XA-TEST", EventID: "XE-TEST",
			SemanticSequence: 1, Prepared: prepared, LocalTarget: TargetActive,
			Shared: PublishAttempt{Attempted: true, WriteResult: result, Convergence: ConvergenceResumable},
		},
	}
	encoded, err := MarshalLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalLease(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Pending.Prepared.Bytes(), preparedRaw) || !bytes.Equal(decoded.Pending.Shared.WriteResult.Bytes(), resultRaw) {
		t.Fatal("opaque P4 journal bytes changed")
	}
}

func TestMarshalLeaseEnforcesOuterBound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	prepared, err := NewPreparedJournal([]byte(`{"payload":"` + strings.Repeat("p", 23*1024) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	writeResult, err := NewWriteResultJournal([]byte(`{"payload":"` + strings.Repeat("r", 10*1024) + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	operationKey, err := expectedOperationKey(1, ActionStart, testIdentity().WorkID)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(DefaultTTL),
		HeartbeatSequence: 1, SemanticSequence: 1, Pending: &PendingOperation{
			OperationKey: operationKey, Action: ActionStart, ArtifactID: "XA-TEST", EventID: "XE-TEST",
			SemanticSequence: 1, Prepared: prepared, LocalTarget: TargetActive,
			Shared: PublishAttempt{Attempted: true, WriteResult: writeResult, Convergence: ConvergenceResumable},
		},
	}
	_, err = MarshalLease(lease)
	requireErrorIs(t, err, ErrLeaseTooLarge)
}
