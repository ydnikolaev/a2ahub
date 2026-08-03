package workreport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var errInjectedFinalize = errors.New("injected finalize failure")

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type memoryEntry struct {
	lease    Lease
	revision uint64
}

type memoryRepository struct {
	mu               sync.Mutex
	entries          map[string]memoryEntry
	casHook          func(next *Lease)
	failFinalizeOnce bool
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{entries: make(map[string]memoryEntry)}
}

func (r *memoryRepository) Load(_ context.Context, key string) (Lease, Revision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.entries[key]
	if !ok {
		return Lease{}, "", ErrLeaseNotFound
	}
	return entry.lease.Clone(), Revision(fmt.Sprintf("r%d", entry.revision)), nil
}

func (r *memoryRepository) CompareAndSwap(_ context.Context, key string, expected Revision, next *Lease) (Revision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[key]
	actual := Revision("")
	if exists {
		actual = Revision(fmt.Sprintf("r%d", entry.revision))
	}
	if actual != expected {
		return actual, ErrSequenceConflict
	}
	if r.failFinalizeOnce && (next == nil || next.Pending == nil) {
		r.failFinalizeOnce = false
		return actual, errInjectedFinalize
	}
	if r.casHook != nil {
		r.casHook(next)
	}
	if next == nil {
		delete(r.entries, key)
		return "", nil
	}
	revision := entry.revision + 1
	r.entries[key] = memoryEntry{lease: next.Clone(), revision: revision}
	return Revision(fmt.Sprintf("r%d", revision)), nil
}

type fakePublisher struct {
	mu       sync.Mutex
	calls    [][]byte
	priors   [][]byte
	attempts []PublishAttempt
	errors   []error
	before   func([]byte) error
}

func (p *fakePublisher) SubmitPrepared(_ context.Context, prepared PreparedJournal, prior WriteResultJournal) (PublishAttempt, error) {
	raw := prepared.Bytes()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, bytes.Clone(raw))
	p.priors = append(p.priors, prior.Bytes())
	if p.before != nil {
		if err := p.before(raw); err != nil {
			return PublishAttempt{}, err
		}
	}
	index := len(p.calls) - 1
	var attempt PublishAttempt
	if index < len(p.attempts) {
		attempt = p.attempts[index].clone()
	}
	var err error
	if index < len(p.errors) {
		err = p.errors[index]
	}
	return attempt, err
}

func testIdentity() Identity {
	return Identity{
		LeaseKey:  "sha256:" + repeat("a", 64),
		ProjectID: "sha256:" + repeat("b", 64),
		Space:     "checkout-core",
		Thread:    "thread:checkout-20260803-abcd",
		WorkID:    "work:01K1A2B3C4D5E6F7G8H9J0K1M2",
		Actor:     Actor{Kind: "agent", Name: "codex", System: "atlas", Model: "gpt", Session: "session:01K1A2B3C4D5E6F7G8H9J0K1M2"},
	}
}

func testPrepared(t *testing.T, action Action, sequence uint64, marker string) PreparedOperation {
	t.Helper()
	journal, err := NewPreparedJournal([]byte(`{"version":1,"marker":"` + marker + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	target := TargetActive
	if action == ActionStop {
		target = TargetClosing
	}
	operationKey, err := expectedOperationKey(sequence, action, testIdentity().WorkID)
	if err != nil {
		t.Fatal(err)
	}
	return PreparedOperation{
		OperationKey:     operationKey,
		Action:           action,
		ArtifactID:       fmt.Sprintf("XA-TEST-%d", sequence),
		EventID:          fmt.Sprintf("01K1A2B3C4D5E6F7G8H9J0K1%02d", sequence),
		SemanticSequence: sequence,
		Prepared:         journal,
		LocalTarget:      target,
	}
}

func testAttempt(t *testing.T, convergence Convergence, marker string) PublishAttempt {
	t.Helper()
	result, err := NewWriteResultJournal([]byte(`{"version":1,"marker":"` + marker + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	return PublishAttempt{Attempted: true, WriteResult: result, Convergence: convergence}
}

func testStart(identity Identity, prepared PreparedOperation) StartCommand {
	return StartCommand{
		Identity: identity, SubjectRef: "XW-checkout-20260803-abcd", Mode: ModeImplementing,
		Summary: "Implementing the accepted contract", TTL: DefaultTTL, Prepared: prepared,
	}
}

func repeat(value string, count int) string {
	var out bytes.Buffer
	for range count {
		out.WriteString(value)
	}
	return out.String()
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", err, target)
	}
}
