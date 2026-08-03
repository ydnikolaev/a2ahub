package workreport

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStartPersistsPreparedBeforePublishAndClearsAcceptedPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	repository := newMemoryRepository()
	identity := testIdentity()
	prepared := testPrepared(t, ActionStart, 1, "start")
	publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "accepted")}}
	publisher.before = func(raw []byte) error {
		lease, _, err := repository.Load(ctx, identity.LeaseKey)
		if err != nil {
			return err
		}
		if lease.Pending == nil || !bytes.Equal(lease.Pending.Prepared.Bytes(), raw) {
			return errors.New("publisher called before exact prepared bytes were persisted")
		}
		return nil
	}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, testStart(identity, prepared))
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalState != LocalActive || result.Shared.Convergence != ConvergenceAccepted || result.SemanticSequence != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Pending != nil {
		t.Fatal("accepted non-stop operation remained pending")
	}
	if lease.LastResult == nil || lease.LastResult.OperationKey != prepared.OperationKey ||
		!bytes.Equal(lease.LastResult.WriteResult.Bytes(), result.Shared.WriteResult.Bytes()) {
		t.Fatal("accepted operation did not retain an exact idempotency receipt")
	}
	if lease.HeartbeatSequence != 1 || lease.SemanticSequence != 1 {
		t.Fatalf("unexpected sequences: heartbeat=%d semantic=%d", lease.HeartbeatSequence, lease.SemanticSequence)
	}
}

func TestPublicationFailurePersistsExactResultAndResumeUsesExactPreparedBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	prepared := testPrepared(t, ActionStart, 1, "frozen")
	failed := testAttempt(t, ConvergenceAccepted, "partial")
	failed.ErrorCode = "provider-timeout"
	succeeded := testAttempt(t, ConvergenceAccepted, "recovered")
	publisher := &fakePublisher{
		attempts: []PublishAttempt{failed, succeeded},
		errors:   []error{errors.New("lost acknowledgement"), nil},
	}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Start(ctx, testStart(identity, prepared))
	if err == nil {
		t.Fatal("publication error was hidden")
	}
	if first.Shared.Convergence != ConvergenceResumable || first.LocalState != LocalActive {
		t.Fatalf("first result = %+v", first)
	}
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Pending == nil || lease.Pending.Shared.Convergence != ConvergenceResumable || lease.Pending.LastErrorCode != "provider-timeout" {
		t.Fatalf("pending failure not preserved: %+v", lease.Pending)
	}
	clock.set(clock.Now().Add(48 * time.Hour)) // resume remains legal after expiry
	second, err := service.Resume(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if second.Shared.Convergence != ConvergenceAccepted {
		t.Fatalf("resume result = %+v", second)
	}
	if len(publisher.calls) != 2 || !bytes.Equal(publisher.calls[0], prepared.Prepared.Bytes()) || !bytes.Equal(publisher.calls[0], publisher.calls[1]) {
		t.Fatal("resume changed prepared bytes")
	}
	if len(publisher.priors) != 2 || len(publisher.priors[0]) != 0 ||
		!bytes.Equal(publisher.priors[1], first.Shared.WriteResult.Bytes()) {
		t.Fatal("resume did not pass the exact furthest prior P4 result")
	}
	lease, _, err = repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Pending != nil {
		t.Fatal("accepted resume remained pending")
	}
}

func TestStartReplayWithPersistedPendingOperationResumesInsteadOfDuplicating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	prepared := testPrepared(t, ActionStart, 1, "same")
	publisher := &fakePublisher{attempts: []PublishAttempt{
		testAttempt(t, ConvergenceResumable, "first"),
		testAttempt(t, ConvergenceAccepted, "second"),
	}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, prepared)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, prepared)); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 2 || !bytes.Equal(publisher.calls[0], publisher.calls[1]) {
		t.Fatal("start replay did not resubmit the exact persisted operation")
	}
	if _, err := service.Start(ctx, testStart(identity, prepared)); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 2 {
		t.Fatal("completed start replay called publisher")
	}
}

func TestCompletedSemanticReplayReturnsExactReceiptAndRejectsChangedBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{
		testAttempt(t, ConvergenceAccepted, "start-result"),
		testAttempt(t, ConvergenceAccepted, "checkpoint-result"),
	}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
		t.Fatal(err)
	}
	prepared := testPrepared(t, ActionCheckpoint, 2, "checkpoint")
	command := SemanticCommand{
		Identity: identity, SubjectRef: "XW-test", Mode: ModeTesting, Summary: "Testing", TTL: DefaultTTL, Prepared: prepared,
	}
	first, err := service.Apply(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	callCount := len(publisher.calls)
	replayed, err := service.Apply(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != callCount || !bytes.Equal(replayed.Shared.WriteResult.Bytes(), first.Shared.WriteResult.Bytes()) ||
		replayed.Shared.Convergence != first.Shared.Convergence {
		t.Fatal("completed replay did not return the exact stored result without republishing")
	}
	changed := command
	changed.Prepared = testPrepared(t, ActionCheckpoint, 2, "changed-bytes")
	_, err = service.Apply(ctx, changed)
	requireErrorIs(t, err, ErrSequenceConflict)
	if len(publisher.calls) != callCount {
		t.Fatal("same operation key with changed bytes reached publisher")
	}
}

func TestStopAcceptedRemainsClosingUntilTerminalThenClears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{
		testAttempt(t, ConvergenceAccepted, "start"),
		testAttempt(t, ConvergenceAccepted, "pending-merge"),
		testAttempt(t, ConvergenceTerminal, "merged"),
	}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
		t.Fatal(err)
	}
	stop := SemanticCommand{
		Identity: identity, SubjectRef: "XW-test", Mode: ModeFinished, Summary: "Finished implementation",
		TTL: DefaultTTL, Prepared: testPrepared(t, ActionStop, 2, "stop"),
	}
	result, err := service.Apply(ctx, stop)
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalState != LocalClosing {
		t.Fatalf("accepted stop local state = %s", result.LocalState)
	}
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if !lease.Closing || lease.Pending == nil {
		t.Fatal("accepted stop did not retain closing recovery journal")
	}
	if _, err := service.Heartbeat(ctx, identity, DefaultTTL); !errors.Is(err, ErrLeaseClosing) {
		t.Fatalf("heartbeat closing error = %v", err)
	}
	result, err = service.Resume(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if result.LocalState != LocalCleared {
		t.Fatalf("terminal resume local state = %s", result.LocalState)
	}
	if _, _, err := repository.Load(ctx, identity.LeaseKey); !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("terminal stop lease load = %v", err)
	}
}

func TestTerminalResultIsPersistedBeforeStopCleanupAndResumeDoesNotRepublish(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{
		testAttempt(t, ConvergenceAccepted, "start"),
		testAttempt(t, ConvergenceTerminal, "merged"),
	}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
		t.Fatal(err)
	}
	repository.failFinalizeOnce = true
	stop := SemanticCommand{
		Identity: identity, SubjectRef: "XW-test", Mode: ModeFinished, Summary: "Finished",
		TTL: DefaultTTL, Prepared: testPrepared(t, ActionStop, 2, "stop"),
	}
	result, err := service.Apply(ctx, stop)
	requireErrorIs(t, err, ErrResultPersistence)
	requireErrorIs(t, err, errInjectedFinalize)
	if result.LocalState != LocalClosing {
		t.Fatalf("failed cleanup state = %s", result.LocalState)
	}
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Pending == nil || lease.Pending.Shared.Convergence != ConvergenceTerminal ||
		!bytes.Equal(lease.Pending.Shared.WriteResult.Bytes(), result.Shared.WriteResult.Bytes()) {
		t.Fatal("terminal P4 result was not persisted before cleanup")
	}
	callCount := len(publisher.calls)
	resumed, err := service.Resume(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.LocalState != LocalCleared || len(publisher.calls) != callCount {
		t.Fatal("resume republished an already terminal operation")
	}
}

func TestHeartbeatIsLocalOnlyAndPreservesSemanticSequence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "start")}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
		t.Fatal(err)
	}
	initialCalls := len(publisher.calls)
	for sequence := uint64(2); sequence <= 50; sequence++ {
		clock.set(clock.Now().Add(time.Minute))
		result, err := service.Heartbeat(ctx, identity, DefaultTTL)
		if err != nil {
			t.Fatal(err)
		}
		if result.SemanticSequence != 1 || result.Shared.Attempted {
			t.Fatalf("heartbeat %d changed shared/semantic outcome: %+v", sequence, result)
		}
	}
	if len(publisher.calls) != initialCalls {
		t.Fatalf("heartbeat called publisher %d times", len(publisher.calls)-initialCalls)
	}
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.HeartbeatSequence != 50 || lease.SemanticSequence != 1 {
		t.Fatalf("final sequences heartbeat=%d semantic=%d", lease.HeartbeatSequence, lease.SemanticSequence)
	}
}

func TestHeartbeatOwnershipExpiryAndClockMatrix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*fakeClock, *Identity)
		want   error
	}{
		{name: "other session", mutate: func(_ *fakeClock, identity *Identity) { identity.Actor.Session = "session:OTHER" }, want: ErrLeaseNotOwned},
		{name: "expiry equality is current", mutate: func(clock *fakeClock, _ *Identity) { clock.set(base.Add(DefaultTTL)) }},
		{name: "after expiry", mutate: func(clock *fakeClock, _ *Identity) { clock.set(base.Add(DefaultTTL + time.Nanosecond)) }, want: ErrLeaseExpired},
		{name: "backward clock", mutate: func(clock *fakeClock, _ *Identity) { clock.set(base.Add(-time.Nanosecond)) }, want: ErrClockBackward},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clock := &fakeClock{now: base}
			repository := newMemoryRepository()
			identity := testIdentity()
			publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "start")}}
			service, err := NewService(repository, publisher, clock)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
				t.Fatal(err)
			}
			callIdentity := identity
			test.mutate(clock, &callIdentity)
			_, err = service.Heartbeat(ctx, callIdentity, DefaultTTL)
			if test.want == nil && err != nil {
				t.Fatal(err)
			}
			if test.want != nil {
				requireErrorIs(t, err, test.want)
			}
		})
	}
}

func TestHeartbeatCannotDecreaseExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "start")}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	command := testStart(identity, testPrepared(t, ActionStart, 1, "start"))
	command.TTL = MaximumTTL
	if _, err := service.Start(ctx, command); err != nil {
		t.Fatal(err)
	}
	clock.set(base.Add(time.Minute))
	_, err = service.Heartbeat(ctx, identity, MinimumTTL)
	requireErrorIs(t, err, ErrSequenceConflict)
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.ExpiresAt != base.Add(MaximumTTL) || lease.HeartbeatSequence != 1 {
		t.Fatalf("decreasing heartbeat mutated lease: expiry=%s sequence=%d", lease.ExpiresAt, lease.HeartbeatSequence)
	}
}

func TestOwnershipTupleIgnoresPresentationOnlyActorFields(t *testing.T) {
	t.Parallel()
	identity := testIdentity()
	other := identity
	other.Actor.Kind = "human"
	other.Actor.Model = "different-display-model"
	if !identity.Equal(other) {
		t.Fatal("actor kind/model incorrectly changed lease ownership")
	}
	other.Actor.Session = "session:OTHER"
	if identity.Equal(other) {
		t.Fatal("actor session did not change lease ownership")
	}
}

func TestSemanticActionModeMatrixRefusesBeforeCASOrPublish(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action Action
		mode   Mode
	}{
		{name: "checkpoint waiting", action: ActionCheckpoint, mode: ModeWaiting},
		{name: "checkpoint finished", action: ActionCheckpoint, mode: ModeFinished},
		{name: "wait implementing", action: ActionWait, mode: ModeImplementing},
		{name: "stop implementing", action: ActionStop, mode: ModeImplementing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newMemoryRepository()
			publisher := &fakePublisher{}
			clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
			service, err := NewService(repository, publisher, clock)
			if err != nil {
				t.Fatal(err)
			}
			identity := testIdentity()
			command := SemanticCommand{
				Identity: identity, SubjectRef: "XW-test", Mode: test.mode, Summary: "invalid combination",
				TTL: DefaultTTL, Prepared: testPrepared(t, test.action, 2, "invalid"),
			}
			_, err = service.Apply(context.Background(), command)
			requireErrorIs(t, err, ErrInvalidLease)
			if len(publisher.calls) != 0 || len(repository.entries) != 0 {
				t.Fatal("invalid action/mode combination caused a side effect")
			}
		})
	}
}

func TestStartRefusesWaitingAndTerminalModesBeforeLeaseMutation(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{ModeWaiting, ModePaused, ModeFinished} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			repository := newMemoryRepository()
			publisher := &fakePublisher{}
			clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
			service, err := NewService(repository, publisher, clock)
			if err != nil {
				t.Fatal(err)
			}
			command := testStart(testIdentity(), testPrepared(t, ActionStart, 1, "invalid-start"))
			command.Mode = mode
			if mode == ModeWaiting {
				command.WaitingOn = []WaitingOn{{Kind: WaitSystem, ID: "axon"}}
			}
			_, err = service.Start(context.Background(), command)
			requireErrorIs(t, err, ErrInvalidLease)
			if len(repository.entries) != 0 || len(publisher.calls) != 0 {
				t.Fatal("invalid start caused a side effect")
			}
		})
	}
}

func TestConcurrentHeartbeatsNeverDecreaseSequencesOrExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "start")}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
		t.Fatal(err)
	}
	clock.set(base.Add(time.Minute))
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, heartbeatErr := service.Heartbeat(ctx, identity, DefaultTTL)
			if heartbeatErr != nil && !errors.Is(heartbeatErr, ErrSequenceConflict) {
				t.Errorf("heartbeat error = %v", heartbeatErr)
			}
		}()
	}
	wait.Wait()
	lease, _, err := repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		t.Fatal(err)
	}
	if lease.HeartbeatSequence < 2 || lease.HeartbeatSequence > 65 || lease.SemanticSequence != 1 {
		t.Fatalf("sequences heartbeat=%d semantic=%d", lease.HeartbeatSequence, lease.SemanticSequence)
	}
	if lease.ExpiresAt.Before(base.Add(time.Minute + DefaultTTL)) {
		t.Fatalf("expiry moved backward: %s", lease.ExpiresAt)
	}
}

func TestNewSemanticOperationRefusesPendingAndSequenceConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	failed := testAttempt(t, ConvergenceResumable, "failed")
	publisher := &fakePublisher{attempts: []PublishAttempt{failed}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "start"))); err != nil {
		t.Fatal(err)
	}
	command := SemanticCommand{
		Identity: identity, SubjectRef: "XW-test", Mode: ModeTesting, Summary: "Testing", TTL: DefaultTTL,
		Prepared: testPrepared(t, ActionCheckpoint, 2, "checkpoint"),
	}
	_, err = service.Apply(ctx, command)
	requireErrorIs(t, err, ErrPendingOperation)
	if len(publisher.calls) != 1 {
		t.Fatalf("conflicting operation published; calls=%d", len(publisher.calls))
	}
}

func TestCASConflictNeverPublishesUnpersistedCandidate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	repository := newMemoryRepository()
	identity := testIdentity()
	publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "unused")}}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	repository.entries[identity.LeaseKey] = memoryEntry{lease: Lease{}, revision: 99}
	_, err = service.Start(ctx, testStart(identity, testPrepared(t, ActionStart, 1, "loser")))
	if err == nil {
		t.Fatal("CAS conflict unexpectedly succeeded")
	}
	if len(publisher.calls) != 0 {
		t.Fatal("publisher called after losing lease CAS")
	}
}
