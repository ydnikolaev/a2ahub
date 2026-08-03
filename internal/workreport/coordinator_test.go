package workreport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCoordinatorStartOrdersIdentityPreparationAndService(t *testing.T) {
	t.Parallel()

	var calls []string
	now := time.Date(2026, 8, 3, 10, 15, 0, 0, time.FixedZone("test", 2*60*60))
	ids := &coordinatorIDs{
		workID:    testIdentity().WorkID,
		sessionID: testIdentity().Actor.Session,
		calls:     &calls,
	}
	keys := coordinatorKeys{calls: &calls, key: testIdentity().LeaseKey}
	preparer := &coordinatorPreparer{calls: &calls, prepare: preparedFromRequest}
	service := &coordinatorService{calls: &calls}
	service.start = func(command StartCommand) (OperationResult, error) {
		if command.Prepared.OperationKey != preparer.lastPrepared.OperationKey ||
			command.Prepared.Action != preparer.lastPrepared.Action ||
			command.Prepared.ArtifactID != preparer.lastPrepared.ArtifactID ||
			command.Prepared.EventID != preparer.lastPrepared.EventID ||
			command.Prepared.SemanticSequence != preparer.lastPrepared.SemanticSequence ||
			command.Prepared.LocalTarget != preparer.lastPrepared.LocalTarget ||
			!bytes.Equal(command.Prepared.Prepared.Bytes(), preparer.lastPrepared.Prepared.Bytes()) {
			t.Fatal("service did not receive the final prepared operation")
		}
		return resultFromPrepared(command.Identity, command.Prepared), nil
	}
	coordinator := newTestCoordinatorWithGate(
		t, service, service, coordinatorReader{}, &coordinatorGate{floor: "0.19.0", calls: &calls},
		preparer, ids, keys, coordinatorClock{now: now, calls: &calls},
	)

	input := StartInput{
		ProjectID:      testIdentity().ProjectID,
		Space:          testIdentity().Space,
		Thread:         testIdentity().Thread,
		Actor:          Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt"},
		SubjectRef:     "XW-checkout-20260803-abcd",
		Mode:           ModeImplementing,
		Summary:        "Implementing the accepted contract",
		Recipients:     []string{"all"},
		Classification: "internal",
	}
	result, err := coordinator.Start(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(calls), "[gate clock work-id session-id lease-key prepare start]"; got != want {
		t.Fatalf("call order = %s, want %s", got, want)
	}
	request := preparer.requests[0]
	if request.Identity.WorkID != ids.workID || request.Identity.Actor.Session != ids.sessionID {
		t.Fatalf("prepared identity = %+v", request.Identity)
	}
	if request.ReportedAt != now.UTC() || request.ValidUntil != now.UTC().Add(DefaultReportValidFor) {
		t.Fatalf("preparation timestamps = (%s, %s)", request.ReportedAt, request.ValidUntil)
	}
	if request.Action != ActionStart || request.SemanticSequence != 1 || request.LocalTarget != TargetActive {
		t.Fatalf("start orchestration = %+v", request)
	}
	wantKey, err := expectedOperationKey(1, ActionStart, ids.workID)
	if err != nil {
		t.Fatal(err)
	}
	if request.OperationKey != wantKey || result.OperationKey != wantKey || result.WorkID != ids.workID || result.Session != ids.sessionID {
		t.Fatalf("unstable start identity/key: request=%+v result=%+v", request, result)
	}
	input.Recipients[0] = "mutated"
	if request.Recipients[0] != "all" {
		t.Fatal("preparation request retained caller-owned recipient storage")
	}
}

func TestCoordinatorDefaultsAudienceAndContinuationRetainsDurableAudience(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 15, 0, 0, time.UTC)
	preparer := &coordinatorPreparer{prepare: preparedFromRequest}
	service := &coordinatorService{}
	service.start = func(command StartCommand) (OperationResult, error) {
		if fmt.Sprint(command.Recipients) != "[all]" || command.Classification != DefaultClassification {
			t.Fatalf("default audience = %v/%q", command.Recipients, command.Classification)
		}
		return resultFromPrepared(command.Identity, command.Prepared), nil
	}
	coordinator := newTestCoordinator(
		t, service, service, coordinatorReader{}, preparer,
		&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
		coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: now},
	)
	if _, err := coordinator.Start(context.Background(), validStartInput()); err != nil {
		t.Fatal(err)
	}

	stored := coordinatorLease(1)
	stored.Recipients = []string{"beta"}
	stored.Classification = "restricted"
	preparer = &coordinatorPreparer{prepare: preparedFromRequest}
	service.apply = func(command SemanticCommand) (OperationResult, error) {
		return resultFromPrepared(command.Identity, command.Prepared), nil
	}
	coordinator = newTestCoordinator(
		t, service, service, coordinatorReader{lease: stored, revision: "r1"}, preparer,
		&coordinatorIDs{}, coordinatorKeys{key: stored.Identity.LeaseKey}, &fakeClock{now: stored.RenewedAt.Add(time.Minute)},
	)
	if _, err := coordinator.Checkpoint(context.Background(), CheckpointInput{
		Work: continuationInput(stored.Identity), Mode: ModeTesting, Summary: "Testing",
	}); err != nil {
		t.Fatal(err)
	}
	request := preparer.requests[0]
	if fmt.Sprint(request.Recipients) != "[beta]" || request.Classification != "restricted" {
		t.Fatalf("retained audience = %v/%q", request.Recipients, request.Classification)
	}
}

func TestCoordinatorAuthoritativeGateFloorMatrixIgnoresProducerClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		floor   string
		wantErr error
	}{
		{name: "lower floor ignores forged producer claim", floor: "0.18.9", wantErr: errCoordinatorFeatureFloorInactive},
		{name: "equal floor", floor: "0.19.0"},
		{name: "higher floor", floor: "0.20.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			gate := &coordinatorGate{
				floor: test.floor,
				// This fake intentionally ignores the self-asserted claim. The
				// production interface cannot receive one at all.
				producerClaim: "9.9.9",
				calls:         &calls,
			}
			preparer := &coordinatorPreparer{calls: &calls, prepare: preparedFromRequest}
			service := &coordinatorService{calls: &calls}
			coordinator := newTestCoordinatorWithGate(
				t, service, service, coordinatorReader{calls: &calls}, gate, preparer,
				&coordinatorIDs{
					workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session, calls: &calls,
				},
				coordinatorKeys{key: testIdentity().LeaseKey, calls: &calls},
				coordinatorClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC), calls: &calls},
			)

			_, err := coordinator.Start(context.Background(), validStartInput())
			if test.wantErr != nil {
				requireErrorIs(t, err, test.wantErr)
				if got, want := fmt.Sprint(calls), "[gate]"; got != want {
					t.Fatalf("lower-floor call order = %s, want %s", got, want)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got, want := fmt.Sprint(calls), "[gate clock work-id session-id lease-key prepare start]"; got != want {
					t.Fatalf("enabled call order = %s, want %s", got, want)
				}
			}
			if len(gate.scopes) != 1 || gate.scopes[0] != (WorkAuthoringScope{
				ProjectID: testIdentity().ProjectID,
				Space:     testIdentity().Space,
			}) {
				t.Fatalf("authoritative gate scope = %+v", gate.scopes)
			}
		})
	}
}

func TestCoordinatorAuthoritativeGateCoversEveryFreshSemanticAction(t *testing.T) {
	t.Parallel()

	actions := []struct {
		name   string
		invoke func(*Coordinator) error
	}{
		{name: "start", invoke: func(c *Coordinator) error {
			_, err := c.Start(context.Background(), validStartInput())
			return err
		}},
		{name: "checkpoint", invoke: func(c *Coordinator) error {
			_, err := c.Checkpoint(context.Background(), CheckpointInput{
				Work: continuationInput(testIdentity()), Mode: ModeTesting, Summary: "Testing",
			})
			return err
		}},
		{name: "wait", invoke: func(c *Coordinator) error {
			_, err := c.Wait(context.Background(), WaitInput{
				Work: continuationInput(testIdentity()), Summary: "Waiting",
				WaitingOn: []WaitingOn{{Kind: WaitHuman, ID: "reviewer"}},
			})
			return err
		}},
		{name: "stop", invoke: func(c *Coordinator) error {
			_, err := c.Stop(context.Background(), StopInput{
				Work: continuationInput(testIdentity()), Result: ModeFinished, Summary: "Finished",
			})
			return err
		}},
	}
	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			service := &coordinatorService{calls: &calls}
			coordinator := newTestCoordinatorWithGate(
				t, service, service, coordinatorReader{calls: &calls},
				&coordinatorGate{floor: "0.18.9", calls: &calls},
				&coordinatorPreparer{calls: &calls, prepare: preparedFromRequest},
				&coordinatorIDs{calls: &calls}, coordinatorKeys{calls: &calls},
				coordinatorClock{calls: &calls},
			)
			err := action.invoke(coordinator)
			requireErrorIs(t, err, errCoordinatorFeatureFloorInactive)
			if got, want := fmt.Sprint(calls), "[gate]"; got != want {
				t.Fatalf("call order = %s, want %s", got, want)
			}
		})
	}
}

func TestCoordinatorContinuationUsesPersistedOwnerAndNextSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		action       Action
		invoke       func(*Coordinator, WorkIdentityInput) (OperationResult, error)
		wantMode     Mode
		wantSubject  string
		wantWaiting  []WaitingOn
		wantTarget   LocalTarget
		wantValidity bool
	}{
		{
			name: "checkpoint", action: ActionCheckpoint, wantMode: ModeTesting,
			wantSubject: "XC-checkout-contract", wantTarget: TargetActive, wantValidity: true,
			invoke: func(c *Coordinator, identity WorkIdentityInput) (OperationResult, error) {
				return c.Checkpoint(context.Background(), CheckpointInput{
					Work: identity, SubjectRef: "XC-checkout-contract", Mode: ModeTesting,
					Summary: "Testing the change",
				})
			},
		},
		{
			name: "wait", action: ActionWait, wantMode: ModeWaiting,
			wantSubject: "XW-checkout-20260803-abcd", wantTarget: TargetActive, wantValidity: true,
			wantWaiting: []WaitingOn{{Kind: WaitSystem, ID: "seomatrix", Summary: "fixture review"}},
			invoke: func(c *Coordinator, identity WorkIdentityInput) (OperationResult, error) {
				return c.Wait(context.Background(), WaitInput{
					Work: identity, Summary: "Waiting for fixtures",
					WaitingOn: []WaitingOn{{Kind: WaitSystem, ID: "seomatrix", Summary: "fixture review"}},
				})
			},
		},
		{
			name: "stop", action: ActionStop, wantMode: ModeFinished,
			wantSubject: "XW-checkout-20260803-abcd", wantTarget: TargetClosing,
			invoke: func(c *Coordinator, identity WorkIdentityInput) (OperationResult, error) {
				return c.Stop(context.Background(), StopInput{Work: identity, Result: ModeFinished, Summary: "Finished"})
			},
		},
		{
			name: "pause", action: ActionStop, wantMode: ModePaused,
			wantSubject: "XW-checkout-20260803-abcd", wantTarget: TargetClosing, wantValidity: true,
			invoke: func(c *Coordinator, identity WorkIdentityInput) (OperationResult, error) {
				return c.Stop(context.Background(), StopInput{Work: identity, Result: ModePaused, Summary: "Paused"})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stored := coordinatorLease(3)
			reader := coordinatorReader{lease: stored, revision: "r7"}
			preparer := &coordinatorPreparer{prepare: preparedFromRequest}
			service := &coordinatorService{}
			service.apply = func(command SemanticCommand) (OperationResult, error) {
				if command.Identity != stored.Identity {
					t.Fatalf("service owner = %+v, want persisted %+v", command.Identity, stored.Identity)
				}
				return resultFromPrepared(command.Identity, command.Prepared), nil
			}
			coordinator := newTestCoordinator(
				t, service, service, reader, preparer, &coordinatorIDs{},
				coordinatorKeys{key: stored.Identity.LeaseKey}, &fakeClock{now: stored.RenewedAt.Add(time.Minute)},
			)
			inputIdentity := continuationInput(stored.Identity)
			inputIdentity.Actor.Kind = "human"
			inputIdentity.Actor.Model = "presentation-only-change"

			result, err := test.invoke(coordinator, inputIdentity)
			if err != nil {
				t.Fatal(err)
			}
			request := preparer.requests[0]
			wantKey, err := expectedOperationKey(4, test.action, stored.Identity.WorkID)
			if err != nil {
				t.Fatal(err)
			}
			if request.Identity != stored.Identity || request.SemanticSequence != 4 || request.Action != test.action ||
				request.OperationKey != wantKey || request.Mode != test.wantMode || request.SubjectRef != test.wantSubject ||
				request.LocalTarget != test.wantTarget || fmt.Sprint(request.WaitingOn) != fmt.Sprint(test.wantWaiting) {
				t.Fatalf("continuation request = %+v", request)
			}
			if test.wantValidity {
				if request.ValidUntil != request.ReportedAt.Add(DefaultReportValidFor) {
					t.Fatalf("continuation validity = (%s, %s)", request.ReportedAt, request.ValidUntil)
				}
			} else if !request.ValidUntil.IsZero() {
				t.Fatalf("finished continuation has valid_until %s", request.ValidUntil)
			}
			if result.SemanticSequence != 4 || result.OperationKey != wantKey {
				t.Fatalf("continuation result = %+v", result)
			}
		})
	}
}

func TestCoordinatorResumeAndHeartbeatBypassPreparation(t *testing.T) {
	t.Parallel()

	identity := testIdentity()
	rawResultSession := "token:legacy-provider-value"
	resultDigest := sha256.Sum256([]byte(rawResultSession))
	wantResultSession := fmt.Sprintf("session-sha256:%x", resultDigest)
	service := &coordinatorService{
		heartbeatResult: OperationResult{WorkID: identity.WorkID, Session: rawResultSession, LocalState: LocalActive},
		resumeResult: OperationResult{
			WorkID: identity.WorkID, Session: rawResultSession, OperationKey: "persisted-key",
			SemanticSequence: 9, LocalState: LocalClosing,
		},
	}
	preparer := &coordinatorPreparer{prepare: func(PreparationRequest) (PreparedOperation, error) {
		t.Fatal("local/recovery operation called preparer")
		return PreparedOperation{}, nil
	}}
	reader := coordinatorReader{err: errors.New("lease reader must not be used")}
	coordinator := newTestCoordinatorWithGate(
		t, service, service, reader, &coordinatorGate{floor: "0.18.9"}, preparer, &coordinatorIDs{},
		coordinatorKeys{key: identity.LeaseKey}, &fakeClock{now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)},
	)
	input := continuationInput(identity)

	heartbeat, err := coordinator.Heartbeat(context.Background(), HeartbeatInput{Work: input})
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Shared.Attempted || service.heartbeatTTL != DefaultTTL || heartbeat.Session != wantResultSession {
		t.Fatalf("heartbeat result/ttl = (%+v, %s)", heartbeat, service.heartbeatTTL)
	}
	resumed, err := coordinator.Resume(context.Background(), ResumeInput{Work: input})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.OperationKey != "persisted-key" || resumed.SemanticSequence != 9 || resumed.Session != wantResultSession {
		t.Fatalf("resume replaced persisted journal identity: %+v", resumed)
	}
	if len(preparer.requests) != 0 {
		t.Fatalf("preparer calls = %d", len(preparer.requests))
	}
}

func TestCoordinatorNormalizesSuppliedSessionsBeforeEveryDownstreamBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session string
		want    string
	}{
		{name: "safe preserved", session: "session:provider-run-42", want: "session:provider-run-42"},
		{name: "raw provider value", session: "provider run / 42"},
		{name: "credential shape", session: "Bearer abcdefghijklmnopqrstuvwxyz123456"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			want := test.want
			if want == "" {
				digest := sha256.Sum256([]byte(test.session))
				want = fmt.Sprintf("session-sha256:%x", digest)
			}
			var keyed []Identity
			var serviced Identity
			preparer := &coordinatorPreparer{prepare: preparedFromRequest}
			service := &coordinatorService{start: func(command StartCommand) (OperationResult, error) {
				serviced = command.Identity
				return resultFromPrepared(command.Identity, command.Prepared), nil
			}}
			coordinator := newTestCoordinator(
				t, service, service, coordinatorReader{}, preparer,
				&coordinatorIDs{workID: testIdentity().WorkID},
				coordinatorKeys{key: testIdentity().LeaseKey, identities: &keyed}, &fakeClock{now: time.Now()},
			)
			input := validStartInput()
			input.Actor.Session = test.session
			result, err := coordinator.Start(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if len(keyed) != 1 || keyed[0].Actor.Session != want ||
				len(preparer.requests) != 1 || preparer.requests[0].Identity.Actor.Session != want ||
				serviced.Actor.Session != want || result.Session != want {
				t.Fatalf("normalized session boundaries: key=%+v prepare=%+v service=%+v result=%+v, want %q",
					keyed, preparer.requests, serviced, result, want)
			}
			if test.session != want && (keyed[0].Actor.Session == test.session ||
				preparer.requests[0].Identity.Actor.Session == test.session || result.Session == test.session) {
				t.Fatal("unsafe supplied session crossed a downstream boundary")
			}
		})
	}
}

func TestCoordinatorRejectsMalformedGeneratedIDsBeforeKeyOrPreparation(t *testing.T) {
	t.Parallel()

	workFailure := errors.New("work entropy failed")
	sessionFailure := errors.New("session entropy failed")
	tests := []struct {
		name string
		ids  *coordinatorIDs
		want error
	}{
		{name: "work source error", ids: &coordinatorIDs{workErr: workFailure}, want: workFailure},
		{name: "malformed work id", ids: &coordinatorIDs{workID: "work:not-a-ulid"}, want: ErrInvalidLease},
		{name: "session source error", ids: &coordinatorIDs{workID: testIdentity().WorkID, sessionErr: sessionFailure}, want: sessionFailure},
		{name: "malformed session id", ids: &coordinatorIDs{workID: testIdentity().WorkID, sessionID: "provider/raw/1"}, want: ErrInvalidLease},
		{name: "credential-shaped session id", ids: &coordinatorIDs{
			workID: testIdentity().WorkID, sessionID: "Bearer abcdefghijklmnopqrstuvwxyz123456",
		}, want: ErrInvalidLease},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var keyed []Identity
			preparer := &coordinatorPreparer{prepare: preparedFromRequest}
			service := &coordinatorService{}
			coordinator := newTestCoordinator(
				t, service, service, coordinatorReader{}, preparer, test.ids,
				coordinatorKeys{key: testIdentity().LeaseKey, identities: &keyed}, &fakeClock{now: time.Now()},
			)
			result, err := coordinator.Start(context.Background(), validStartInput())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.want)
			}
			if result.Session != "" || len(keyed) != 0 || len(preparer.requests) != 0 || service.startCalls != 0 {
				t.Fatalf("malformed generated identity crossed boundary: result=%+v key=%+v prepare=%d start=%d",
					result, keyed, len(preparer.requests), service.startCalls)
			}
		})
	}
}

func TestCoordinatorLeaseKeyFailurePreservesErrorWithoutLeakingUnsafeSession(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("canonical root unavailable")
	raw := "token:super-secret-provider-value"
	digest := sha256.Sum256([]byte(raw))
	wantSession := fmt.Sprintf("session-sha256:%x", digest)
	preparer := &coordinatorPreparer{prepare: preparedFromRequest}
	service := &coordinatorService{}
	coordinator := newTestCoordinator(
		t, service, service, coordinatorReader{}, preparer,
		&coordinatorIDs{workID: testIdentity().WorkID},
		coordinatorKeys{err: wantErr}, &fakeClock{now: time.Now()},
	)
	input := validStartInput()
	input.Actor.Session = raw
	result, err := coordinator.Start(context.Background(), input)
	if !errors.Is(err, wantErr) || result.Session != wantSession || result.Session == raw ||
		len(preparer.requests) != 0 || service.startCalls != 0 {
		t.Fatalf("key failure result/error = (%+v, %v); prepare=%d start=%d",
			result, err, len(preparer.requests), service.startCalls)
	}
}

func TestCoordinatorFailureBoundariesPreserveTypedErrorsAndResults(t *testing.T) {
	t.Parallel()

	t.Run("invalid input stops before preparation", func(t *testing.T) {
		t.Parallel()
		preparer := &coordinatorPreparer{prepare: preparedFromRequest}
		service := &coordinatorService{}
		coordinator := newTestCoordinator(
			t, service, service, coordinatorReader{}, preparer,
			&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
			coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
		)
		_, err := coordinator.Start(context.Background(), StartInput{
			ProjectID: testIdentity().ProjectID, Space: testIdentity().Space, Thread: testIdentity().Thread,
			Actor: Actor{Kind: "agent", Name: "codex", System: "atlas"}, SubjectRef: "XW-test",
			Mode: ModeImplementing, Summary: " ",
		})
		requireErrorIs(t, err, ErrInvalidLease)
		if len(preparer.requests) != 0 || service.startCalls != 0 {
			t.Fatal("invalid semantic input crossed the preparation boundary")
		}
	})

	t.Run("identity source error is preserved", func(t *testing.T) {
		t.Parallel()
		want := errors.New("entropy unavailable")
		preparer := &coordinatorPreparer{prepare: preparedFromRequest}
		service := &coordinatorService{}
		coordinator := newTestCoordinator(
			t, service, service, coordinatorReader{}, preparer, &coordinatorIDs{workErr: want},
			coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
		)
		_, err := coordinator.Start(context.Background(), validStartInput())
		if !errors.Is(err, want) || len(preparer.requests) != 0 || service.startCalls != 0 {
			t.Fatalf("identity failure = %v; prepare=%d start=%d", err, len(preparer.requests), service.startCalls)
		}
	})

	t.Run("continuation load and owner errors stop before preparation", func(t *testing.T) {
		t.Parallel()
		loadErr := errors.New("local lease unavailable")
		for _, test := range []struct {
			name   string
			reader coordinatorReader
			want   error
		}{
			{name: "load", reader: coordinatorReader{err: loadErr}, want: loadErr},
			{name: "owner", reader: coordinatorReader{lease: coordinatorLease(1), revision: "r1"}, want: ErrLeaseNotOwned},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				preparer := &coordinatorPreparer{prepare: preparedFromRequest}
				service := &coordinatorService{}
				coordinator := newTestCoordinator(
					t, service, service, test.reader, preparer, &coordinatorIDs{},
					coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
				)
				input := continuationInput(testIdentity())
				if test.name == "owner" {
					input.Actor.Session = "session:OTHER"
				}
				result, err := coordinator.Checkpoint(context.Background(), CheckpointInput{
					Work: input, Mode: ModeTesting, Summary: "Testing",
				})
				if !errors.Is(err, test.want) || len(preparer.requests) != 0 || service.applyCalls != 0 {
					t.Fatalf("continuation failure = %v; prepare=%d apply=%d", err, len(preparer.requests), service.applyCalls)
				}
				if test.name == "owner" && (result.Session != input.Actor.Session || result.SemanticSequence != 0 ||
					result.OperationKey != "" || result.Shared.Attempted || result.LocalState != LocalUnchanged) {
					t.Fatalf("owner mismatch leaked stored continuation state: %+v", result)
				}
			})
		}
	})

	t.Run("preparer error is preserved and service is not called", func(t *testing.T) {
		t.Parallel()
		want := errors.New("feature floor inactive")
		preparer := &coordinatorPreparer{prepare: func(PreparationRequest) (PreparedOperation, error) {
			return PreparedOperation{}, want
		}}
		service := &coordinatorService{}
		coordinator := newTestCoordinator(
			t, service, service, coordinatorReader{}, preparer,
			&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
			coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
		)
		result, err := coordinator.Start(context.Background(), validStartInput())
		if !errors.Is(err, want) || service.startCalls != 0 || result.OperationKey == "" || result.WorkID == "" || result.Session == "" {
			t.Fatalf("prepare failure result/error = (%+v, %v); start=%d", result, err, service.startCalls)
		}
	})

	t.Run("preparer cannot change coordinator identity", func(t *testing.T) {
		t.Parallel()
		preparer := &coordinatorPreparer{prepare: func(request PreparationRequest) (PreparedOperation, error) {
			prepared, err := preparedFromRequest(request)
			prepared.SemanticSequence++
			return prepared, err
		}}
		service := &coordinatorService{}
		coordinator := newTestCoordinator(
			t, service, service, coordinatorReader{}, preparer,
			&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
			coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
		)
		_, err := coordinator.Start(context.Background(), validStartInput())
		requireErrorIs(t, err, ErrSequenceConflict)
		if service.startCalls != 0 {
			t.Fatal("preparer-owned sequence reached semantic service")
		}
	})

	t.Run("service two-axis result and typed error pass through", func(t *testing.T) {
		t.Parallel()
		want := errors.New("submit failed")
		writeResult, err := NewWriteResultJournal([]byte(`{"stage":"pr-created"}`))
		if err != nil {
			t.Fatal(err)
		}
		serviceResult := OperationResult{
			WorkID: testIdentity().WorkID, Session: testIdentity().Actor.Session,
			OperationKey: "op", SemanticSequence: 1, LocalState: LocalActive,
			Shared: PublishAttempt{Attempted: true, WriteResult: writeResult, Convergence: ConvergenceResumable},
		}
		service := &coordinatorService{startResult: serviceResult, startErr: want}
		coordinator := newTestCoordinator(
			t, service, service, coordinatorReader{}, &coordinatorPreparer{prepare: preparedFromRequest},
			&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
			coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
		)
		result, err := coordinator.Start(context.Background(), validStartInput())
		if !errors.Is(err, want) || result.LocalState != serviceResult.LocalState ||
			result.Shared.Convergence != serviceResult.Shared.Convergence ||
			fmt.Sprint(result.Shared.WriteResult.Bytes()) != fmt.Sprint(serviceResult.Shared.WriteResult.Bytes()) {
			t.Fatalf("service result/error changed: (%+v, %v)", result, err)
		}
	})
}

func TestCoordinatorContinuationPreflightRefusesBeforePreparation(t *testing.T) {
	t.Parallel()

	baseNow := time.Date(2026, 8, 3, 10, 1, 0, 0, time.UTC)
	tests := []struct {
		name  string
		lease func(*testing.T) Lease
		now   time.Time
		want  error
		state LocalState
	}{
		{
			name: "pending",
			lease: func(t *testing.T) Lease {
				lease := coordinatorLease(2)
				lease.Mode = ModeTesting
				lease.Summary = "Testing"
				lease.Pending = newPending(testPrepared(t, ActionCheckpoint, 2, "pending"))
				return lease
			},
			now: baseNow, want: ErrPendingOperation, state: LocalActive,
		},
		{
			name: "expired",
			lease: func(*testing.T) Lease {
				return coordinatorLease(1)
			},
			now: coordinatorLease(1).ExpiresAt.Add(time.Nanosecond), want: ErrLeaseExpired, state: LocalUnchanged,
		},
		{
			name: "backward clock",
			lease: func(*testing.T) Lease {
				return coordinatorLease(1)
			},
			now: coordinatorLease(1).RenewedAt.Add(-time.Nanosecond), want: ErrClockBackward, state: LocalUnchanged,
		},
		{
			name: "closing",
			lease: func(t *testing.T) Lease {
				lease := coordinatorLease(2)
				lease.Mode = ModeFinished
				lease.Summary = "Finished"
				lease.Closing = true
				lease.Pending = newPending(testPrepared(t, ActionStop, 2, "closing"))
				return lease
			},
			now: baseNow, want: ErrLeaseClosing, state: LocalClosing,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lease := test.lease(t)
			preparer := &coordinatorPreparer{prepare: preparedFromRequest}
			service := &coordinatorService{}
			coordinator := newTestCoordinator(
				t, service, service, coordinatorReader{lease: lease, revision: "r1"}, preparer,
				&coordinatorIDs{}, coordinatorKeys{key: lease.Identity.LeaseKey}, &fakeClock{now: test.now},
			)
			result, err := coordinator.Checkpoint(context.Background(), CheckpointInput{
				Work: continuationInput(lease.Identity), Mode: ModeReviewing, Summary: "Reviewing",
			})
			requireErrorIs(t, err, test.want)
			if len(preparer.requests) != 0 || service.applyCalls != 0 || result.LocalState != test.state {
				t.Fatalf("preflight result=%+v prepare=%d apply=%d", result, len(preparer.requests), service.applyCalls)
			}
		})
	}
}

func TestCoordinatorContinuationNormalizesSessionBeforeKeyOwnershipAndPreparation(t *testing.T) {
	t.Parallel()

	raw := "provider session / unsafe"
	digest := sha256.Sum256([]byte(raw))
	normalized := fmt.Sprintf("session-sha256:%x", digest)
	lease := coordinatorLease(1)
	lease.Identity.Actor.Session = normalized
	var keyed []Identity
	preparer := &coordinatorPreparer{prepare: preparedFromRequest}
	service := &coordinatorService{apply: func(command SemanticCommand) (OperationResult, error) {
		return resultFromPrepared(command.Identity, command.Prepared), nil
	}}
	coordinator := newTestCoordinator(
		t, service, service, coordinatorReader{lease: lease, revision: "r1"}, preparer,
		&coordinatorIDs{}, coordinatorKeys{key: lease.Identity.LeaseKey, identities: &keyed},
		&fakeClock{now: lease.RenewedAt.Add(time.Minute)},
	)
	input := continuationInput(lease.Identity)
	input.Actor.Session = raw
	result, err := coordinator.Checkpoint(context.Background(), CheckpointInput{
		Work: input, Mode: ModeTesting, Summary: "Testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keyed) != 1 || keyed[0].Actor.Session != normalized ||
		len(preparer.requests) != 1 || preparer.requests[0].Identity.Actor.Session != normalized ||
		result.Session != normalized {
		t.Fatalf("continuation session boundaries: key=%+v prepare=%+v result=%+v",
			keyed, preparer.requests, result)
	}
}

func TestCoordinatorReportValidityEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		validFor time.Duration
		wantErr  error
	}{
		{name: "minimum", validFor: MinimumReportValidFor},
		{name: "maximum", validFor: MaximumReportValidFor},
		{name: "below minimum", validFor: MinimumReportValidFor - time.Nanosecond, wantErr: ErrInvalidLease},
		{name: "above maximum", validFor: MaximumReportValidFor + time.Nanosecond, wantErr: ErrInvalidLease},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
			preparer := &coordinatorPreparer{prepare: preparedFromRequest}
			service := &coordinatorService{start: func(command StartCommand) (OperationResult, error) {
				return resultFromPrepared(command.Identity, command.Prepared), nil
			}}
			coordinator := newTestCoordinator(
				t, service, service, coordinatorReader{}, preparer,
				&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
				coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: now},
			)
			input := validStartInput()
			input.ReportValidFor = test.validFor
			_, err := coordinator.Start(context.Background(), input)
			if test.wantErr != nil {
				requireErrorIs(t, err, test.wantErr)
				if len(preparer.requests) != 0 || service.startCalls != 0 {
					t.Fatal("invalid validity crossed preparation boundary")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(preparer.requests) != 1 || preparer.requests[0].ValidUntil.Sub(preparer.requests[0].ReportedAt) != test.validFor {
				t.Fatalf("prepared validity = %+v, want %s", preparer.requests, test.validFor)
			}
		})
	}
}

func TestCoordinatorRejectsTamperedOrMalformedFinalPreparation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*PreparedOperation)
		want   error
	}{
		{name: "operation key", mutate: func(p *PreparedOperation) { p.OperationKey = "op-v1-tampered" }, want: ErrSequenceConflict},
		{name: "action", mutate: func(p *PreparedOperation) { p.Action = ActionStop }, want: ErrSequenceConflict},
		{name: "sequence", mutate: func(p *PreparedOperation) { p.SemanticSequence++ }, want: ErrSequenceConflict},
		{name: "target", mutate: func(p *PreparedOperation) { p.LocalTarget = TargetClosing }, want: ErrSequenceConflict},
		{name: "missing artifact", mutate: func(p *PreparedOperation) { p.ArtifactID = "" }, want: ErrInvalidLease},
		{name: "empty journal", mutate: func(p *PreparedOperation) { p.Prepared = PreparedJournal{} }, want: ErrInvalidLease},
		{name: "malformed journal", mutate: func(p *PreparedOperation) {
			p.Prepared = PreparedJournal{raw: []byte("not-json")}
		}, want: ErrInvalidLease},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			preparer := &coordinatorPreparer{prepare: func(request PreparationRequest) (PreparedOperation, error) {
				prepared, err := preparedFromRequest(request)
				if err == nil {
					test.mutate(&prepared)
				}
				return prepared, err
			}}
			service := &coordinatorService{}
			coordinator := newTestCoordinator(
				t, service, service, coordinatorReader{}, preparer,
				&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
				coordinatorKeys{key: testIdentity().LeaseKey}, &fakeClock{now: time.Now()},
			)
			_, err := coordinator.Start(context.Background(), validStartInput())
			requireErrorIs(t, err, test.want)
			if service.startCalls != 0 {
				t.Fatal("invalid final preparation reached semantic service")
			}
		})
	}
}

func TestCoordinatorCASLossNeverSubmitsUnpersistedPreparation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := newMemoryRepository()
	repository := &coordinatorConflictRepository{LeaseRepository: base}
	publisher := &fakePublisher{attempts: []PublishAttempt{testAttempt(t, ConvergenceAccepted, "start")}}
	clock := &fakeClock{now: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)}
	service, err := NewService(repository, publisher, clock)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := newTestCoordinator(
		t, service, service, repository, &coordinatorPreparer{prepare: preparedFromRequest},
		&coordinatorIDs{workID: testIdentity().WorkID, sessionID: testIdentity().Actor.Session},
		coordinatorKeys{key: testIdentity().LeaseKey}, clock,
	)
	started, err := coordinator.Start(ctx, validStartInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 1 {
		t.Fatalf("start publisher calls = %d", len(publisher.calls))
	}
	repository.failNextCAS()
	_, err = coordinator.Checkpoint(ctx, CheckpointInput{
		Work: continuationInput(Identity{
			LeaseKey: testIdentity().LeaseKey, ProjectID: testIdentity().ProjectID,
			Space: testIdentity().Space, Thread: testIdentity().Thread, WorkID: started.WorkID,
			Actor: Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt", Session: started.Session},
		}),
		Mode: ModeTesting, Summary: "Testing",
	})
	requireErrorIs(t, err, ErrSequenceConflict)
	if len(publisher.calls) != 1 {
		t.Fatalf("CAS loser submitted prepared bytes; publisher calls = %d", len(publisher.calls))
	}
}

type coordinatorIDs struct {
	workID, sessionID   string
	workErr, sessionErr error
	calls               *[]string
}

func (s *coordinatorIDs) NewWorkID(time.Time) (string, error) {
	appendCall(s.calls, "work-id")
	return s.workID, s.workErr
}

func (s *coordinatorIDs) NewSessionID(time.Time) (string, error) {
	appendCall(s.calls, "session-id")
	return s.sessionID, s.sessionErr
}

type coordinatorKeys struct {
	calls      *[]string
	identities *[]Identity
	key        string
	err        error
}

func (s coordinatorKeys) LeaseKey(identity Identity) (string, error) {
	appendCall(s.calls, "lease-key")
	if s.identities != nil {
		*s.identities = append(*s.identities, identity)
	}
	return s.key, s.err
}

type coordinatorGate struct {
	floor         string
	producerClaim string
	calls         *[]string
	scopes        []WorkAuthoringScope
}

func (g *coordinatorGate) AuthorizeWork(_ context.Context, scope WorkAuthoringScope) error {
	appendCall(g.calls, "gate")
	g.scopes = append(g.scopes, scope)
	if g.floor == "0.18.9" {
		return errCoordinatorFeatureFloorInactive
	}
	return nil
}

type coordinatorClock struct {
	now   time.Time
	calls *[]string
}

func (c coordinatorClock) Now() time.Time {
	appendCall(c.calls, "clock")
	return c.now
}

type coordinatorPreparer struct {
	calls        *[]string
	requests     []PreparationRequest
	lastPrepared PreparedOperation
	prepare      func(PreparationRequest) (PreparedOperation, error)
}

func (p *coordinatorPreparer) Prepare(_ context.Context, request PreparationRequest) (PreparedOperation, error) {
	appendCall(p.calls, "prepare")
	p.requests = append(p.requests, request)
	prepared, err := p.prepare(request)
	p.lastPrepared = prepared
	return prepared, err
}

type coordinatorReader struct {
	lease    Lease
	revision Revision
	err      error
	calls    *[]string
}

func (r coordinatorReader) Load(context.Context, string) (Lease, Revision, error) {
	appendCall(r.calls, "lease")
	return r.lease.Clone(), r.revision, r.err
}

type coordinatorService struct {
	calls           *[]string
	start           func(StartCommand) (OperationResult, error)
	apply           func(SemanticCommand) (OperationResult, error)
	startResult     OperationResult
	startErr        error
	heartbeatResult OperationResult
	resumeResult    OperationResult
	heartbeatTTL    time.Duration
	startCalls      int
	applyCalls      int
}

func (s *coordinatorService) Start(_ context.Context, command StartCommand) (OperationResult, error) {
	appendCall(s.calls, "start")
	s.startCalls++
	if s.start != nil {
		return s.start(command)
	}
	return s.startResult, s.startErr
}

func (s *coordinatorService) Apply(_ context.Context, command SemanticCommand) (OperationResult, error) {
	appendCall(s.calls, "apply")
	s.applyCalls++
	if s.apply != nil {
		return s.apply(command)
	}
	return OperationResult{}, nil
}

func (s *coordinatorService) Heartbeat(_ context.Context, _ Identity, ttl time.Duration) (OperationResult, error) {
	s.heartbeatTTL = ttl
	return s.heartbeatResult, nil
}

func (s *coordinatorService) Resume(context.Context, Identity) (OperationResult, error) {
	return s.resumeResult, nil
}

type coordinatorConflictRepository struct {
	LeaseRepository
	mu       sync.Mutex
	failNext bool
}

func (r *coordinatorConflictRepository) CompareAndSwap(
	ctx context.Context,
	key string,
	revision Revision,
	next *Lease,
) (Revision, error) {
	r.mu.Lock()
	if r.failNext {
		r.failNext = false
		r.mu.Unlock()
		return revision, ErrSequenceConflict
	}
	r.mu.Unlock()
	return r.LeaseRepository.CompareAndSwap(ctx, key, revision, next)
}

func (r *coordinatorConflictRepository) failNextCAS() {
	r.mu.Lock()
	r.failNext = true
	r.mu.Unlock()
}

func newTestCoordinator(
	t *testing.T,
	semantic SemanticWorkService,
	local LocalWorkService,
	reader LeaseReader,
	preparer Preparer,
	ids IDSource,
	keys LeaseKeySource,
	clock Clock,
) *Coordinator {
	t.Helper()
	return newTestCoordinatorWithGate(
		t, semantic, local, reader, &coordinatorGate{floor: "0.19.0"}, preparer, ids, keys, clock,
	)
}

func newTestCoordinatorWithGate(
	t *testing.T,
	semantic SemanticWorkService,
	local LocalWorkService,
	reader LeaseReader,
	gate WorkAuthoringGate,
	preparer Preparer,
	ids IDSource,
	keys LeaseKeySource,
	clock Clock,
) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(semantic, local, reader, gate, preparer, ids, keys, clock)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

var errCoordinatorFeatureFloorInactive = errors.New("feature-floor-inactive")

func validStartInput() StartInput {
	return StartInput{
		ProjectID: testIdentity().ProjectID, Space: testIdentity().Space, Thread: testIdentity().Thread,
		Actor:      Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt"},
		SubjectRef: "XW-checkout-20260803-abcd", Mode: ModeImplementing,
		Summary: "Implementing the accepted contract",
	}
}

func continuationInput(identity Identity) WorkIdentityInput {
	return WorkIdentityInput{
		ProjectID: identity.ProjectID, Space: identity.Space, Thread: identity.Thread,
		WorkID: identity.WorkID, Actor: identity.Actor,
	}
}

func coordinatorLease(sequence uint64) Lease {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	return Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-checkout-20260803-abcd",
		Mode: ModeImplementing, Summary: "Implementing", StartedAt: now,
		Recipients: []string{"all"}, Classification: DefaultClassification,
		RenewedAt: now, ExpiresAt: now.Add(DefaultTTL), HeartbeatSequence: 1, SemanticSequence: sequence,
	}
}

func preparedFromRequest(request PreparationRequest) (PreparedOperation, error) {
	journal, err := NewPreparedJournal([]byte(fmt.Sprintf(`{"action":%q,"sequence":%d}`, request.Action, request.SemanticSequence)))
	if err != nil {
		return PreparedOperation{}, err
	}
	return PreparedOperation{
		OperationKey: request.OperationKey, Action: request.Action,
		ArtifactID:       fmt.Sprintf("XA-COORDINATOR-%d", request.SemanticSequence),
		EventID:          fmt.Sprintf("01K1A2B3C4D5E6F7G8H9J0K1%02d", request.SemanticSequence),
		SemanticSequence: request.SemanticSequence, Prepared: journal, LocalTarget: request.LocalTarget,
	}, nil
}

func resultFromPrepared(identity Identity, prepared PreparedOperation) OperationResult {
	return OperationResult{
		WorkID: identity.WorkID, Session: identity.Actor.Session,
		OperationKey: prepared.OperationKey, SemanticSequence: prepared.SemanticSequence, LocalState: LocalActive,
	}
}

func appendCall(calls *[]string, call string) {
	if calls != nil {
		*calls = append(*calls, call)
	}
}
