// Package operationalsource composes production cache and local-lease
// evidence into the one pure operational snapshot shared by HTML and serve.
package operationalsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

var ErrInvalidConfiguration = errors.New("operationalsource: invalid configuration")

type CacheReader interface {
	OperationalEvidence(context.Context) (cache.OperationalEvidence, error)
}

type LeaseReader interface {
	ListWork(context.Context, string, string, string, bool) ([]workreport.Lease, error)
}

// PendingResult is the safe subset of P4 write evidence. The concrete strict
// journal decoder is injected from cmd/a2a so this package does not import the
// filesystem/Git-owning space package.
type PendingResult struct {
	Stage           string
	State           string
	RemainingAction string
}

type PendingDecoder interface {
	DecodePending([]byte) (PendingResult, error)
}

type SyncResult struct {
	Space   string
	Code    string
	Summary string
	Failed  bool
}

type Fetcher interface {
	Sync(context.Context) ([]SyncResult, error)
}

// CacheSyncer is the typed refresh seam exposed by cache.Store. It keeps
// owning-space identity separate from human-readable errors.
type CacheSyncer interface {
	SyncIfStaleResults(context.Context) []cache.SpaceSyncResult
}

// CacheFetcher adapts cache refresh outcomes to stable operational facts.
type CacheFetcher struct{ cache CacheSyncer }

func NewCacheFetcher(cacheSyncer CacheSyncer) (*CacheFetcher, error) {
	if cacheSyncer == nil {
		return nil, ErrInvalidConfiguration
	}
	return &CacheFetcher{cache: cacheSyncer}, nil
}

func (f *CacheFetcher) Sync(ctx context.Context) ([]SyncResult, error) {
	cacheResults := f.cache.SyncIfStaleResults(ctx)
	results := make([]SyncResult, 0, len(cacheResults))
	var failures []error
	for _, result := range cacheResults {
		mapped := SyncResult{Space: result.Space}
		if result.Err != nil {
			mapped.Failed = true
			mapped.Code = "space-sync-failed"
			mapped.Summary = "The latest fetch did not complete; cached committed evidence is retained"
			failures = append(failures, result.Err)
		}
		results = append(results, mapped)
	}
	return results, errors.Join(failures...)
}

type Config struct {
	ProjectID string
	Spaces    []string
	Limits    operational.Limits
}

type Source struct {
	config  Config
	cache   CacheReader
	leases  LeaseReader
	pending PendingDecoder
	fetcher Fetcher
	clock   operational.Clock

	// buildMu serializes projection and the small in-memory sync-degradation
	// latch. The latch is response state only (the authoritative evidence
	// remains Git/cache); serialization prevents a slower poll from clearing a
	// newer failed-sync observation with an older source revision.
	syncMu           sync.Mutex
	buildMu          sync.Mutex
	syncDegradations map[string]syncDegradation
}

type syncDegradation struct {
	result   SyncResult
	revision string
}

func New(config Config, cacheReader CacheReader, leases LeaseReader, pending PendingDecoder, fetcher Fetcher, clock operational.Clock) (*Source, error) {
	if config.ProjectID == "" || cacheReader == nil || leases == nil || pending == nil || clock == nil {
		return nil, ErrInvalidConfiguration
	}
	spaces := append([]string(nil), config.Spaces...)
	sort.Strings(spaces)
	for index, space := range spaces {
		if space == "" || index > 0 && space == spaces[index-1] {
			return nil, ErrInvalidConfiguration
		}
	}
	config.Spaces = spaces
	return &Source{
		config: config, cache: cacheReader, leases: leases, pending: pending, fetcher: fetcher, clock: clock,
		syncDegradations: make(map[string]syncDegradation),
	}, nil
}

func (s *Source) Snapshot(ctx context.Context) (operational.Snapshot, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	return s.build(ctx, nil)
}

// Sync performs only the injected fetch operation and then rebuilds through
// the exact Snapshot path. Per-space failures remain explicit evidence; they
// never discard the last readable mirror contents.
func (s *Source) Sync(ctx context.Context) (operational.Snapshot, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.fetcher == nil {
		return operational.Snapshot{}, fmt.Errorf("%w: fetcher is not configured", ErrInvalidConfiguration)
	}
	results, syncErr := s.fetcher.Sync(ctx)
	if syncErr != nil && len(results) == 0 {
		for _, spaceID := range s.config.Spaces {
			results = append(results, SyncResult{Space: spaceID, Failed: true})
		}
	}
	snapshot, buildErr := s.build(ctx, results)
	if buildErr != nil {
		return operational.Snapshot{}, buildErr
	}
	return snapshot, syncErr
}

func (s *Source) build(ctx context.Context, syncResults []SyncResult) (operational.Snapshot, error) {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()

	evidence, err := s.cache.OperationalEvidence(ctx)
	if err != nil {
		return operational.Snapshot{}, fmt.Errorf("operationalsource: read committed evidence: %w", err)
	}
	input := operational.Input{
		Sources:       make([]operational.SourceEvidence, 0, len(evidence.Sources)+1),
		Threads:       make([]operational.ThreadEvidence, 0, len(evidence.Threads)),
		CommittedWork: make([]operational.CommittedWorkEvidence, 0, len(evidence.CommittedWork)),
		LocalLeases:   []operational.LocalLeaseEvidence{},
		Unavailable:   make([]operational.Unavailable, 0, len(evidence.Unavailable)+len(syncResults)+1),
	}
	failures := s.updateSyncDegradations(syncResults, evidence.Sources)
	for _, source := range evidence.Sources {
		freshness := operational.SourceFreshness(source.Freshness)
		if failure, ok := failures[source.Space]; ok && failure.Failed && freshness != operational.SourceUnavailable {
			freshness = operational.SourceStale
		}
		syncedValue := source.SyncedAt.UTC()
		if syncedValue.IsZero() {
			syncedValue = source.ObservedAt.UTC()
		}
		input.Sources = append(input.Sources, operational.SourceEvidence{
			Kind: operational.SourceSpace, Space: source.Space, Revision: source.Revision,
			SyncedAt: &syncedValue, ObservedAt: source.ObservedAt.UTC(), Freshness: freshness,
		})
	}
	for _, unavailable := range evidence.Unavailable {
		input.Unavailable = append(input.Unavailable, operational.Unavailable{
			SourceKind: operational.SourceSpace, Space: unavailable.Space,
			Code: unavailable.Code, Summary: unavailable.Summary,
		})
	}
	for _, result := range failures {
		if !result.Failed {
			continue
		}
		input.Unavailable = append(input.Unavailable, operational.Unavailable{
			SourceKind: operational.SourceSpace, Space: result.Space,
			Code: result.Code, Summary: result.Summary,
		})
	}
	knownThreads := make(map[string]struct{}, len(evidence.Threads))
	for _, thread := range evidence.Threads {
		knownThreads[thread.Space+"\x00"+thread.Thread] = struct{}{}
		var milestone *operational.Milestone
		if thread.LatestMilestone != nil {
			milestone = &operational.Milestone{
				Kind: "event", At: thread.LatestMilestone.At.UTC(),
				Actor: operational.Actor{
					Kind: thread.LatestMilestone.Actor.Kind, Name: thread.LatestMilestone.Actor.Name,
					System: thread.LatestMilestone.Actor.System, Model: thread.LatestMilestone.Actor.Model,
					Session: thread.LatestMilestone.Actor.Session,
				},
				Transition: thread.LatestMilestone.Transition, Subject: thread.LatestMilestone.Subject,
			}
		}
		input.Threads = append(input.Threads, operational.ThreadEvidence{
			Space: thread.Space, Thread: thread.Thread, Title: thread.Title,
			Participants: append([]string(nil), thread.Participants...),
			Protocol: operational.Protocol{
				Settled: thread.Settled, OpenCount: thread.OpenCount,
				WaitingOn: append([]string(nil), thread.WaitingOn...), YourMove: thread.YourMove,
				BlockingBy: append([]string(nil), thread.BlockingBy...),
			},
			LatestMilestone: milestone,
		})
	}
	for _, checkpoint := range evidence.CommittedWork {
		input.CommittedWork = append(input.CommittedWork, operational.CommittedWorkEvidence{
			Space: checkpoint.Space, Thread: checkpoint.Thread, WorkID: checkpoint.WorkID,
			SubjectRef: checkpoint.SubjectRef, Mode: checkpoint.Mode, Summary: checkpoint.Summary,
			Actor: checkpoint.Actor, WaitingOn: append([]workreport.WaitingOn(nil), checkpoint.WaitingOn...),
			ReportedAt: checkpoint.ReportedAt.UTC(), ValidUntil: checkpoint.ValidUntil.UTC(),
			ArtifactID: checkpoint.ArtifactID, CommitSequence: checkpoint.CommitSequence,
		})
	}

	leaseBytes := make([][]byte, 0)
	var localObservedAt time.Time
	localFailures := 0
	readableSpaces := 0
	for _, spaceID := range s.config.Spaces {
		leases, listErr := s.leases.ListWork(ctx, s.config.ProjectID, spaceID, "", true)
		if listErr != nil {
			localFailures++
			input.Unavailable = append(input.Unavailable, operational.Unavailable{
				SourceKind: operational.SourceLocalWork, Code: "local-work-unavailable",
				Summary: "Local work reports could not be read",
			})
			continue
		}
		readableSpaces++
		for _, lease := range leases {
			raw, marshalErr := workreport.MarshalLease(lease)
			if marshalErr != nil {
				localFailures++
				input.Unavailable = append(input.Unavailable, operational.Unavailable{
					SourceKind: operational.SourceLocalWork, Code: "invalid-local-lease",
					Summary: "A local work report could not be decoded",
				})
				continue
			}
			leaseBytes = append(leaseBytes, raw)
			if lease.RenewedAt.After(localObservedAt) {
				localObservedAt = lease.RenewedAt.UTC()
			}
			if _, exists := knownThreads[lease.Identity.Space+"\x00"+lease.Identity.Thread]; !exists {
				localFailures++
				input.Unavailable = append(input.Unavailable, operational.Unavailable{
					SourceKind: operational.SourceLocalWork, Code: "local-work-thread-unavailable",
					Summary: "A local work report references an unavailable thread",
				})
				continue
			}
			pending, pendingErr := s.pendingProjection(lease)
			if pendingErr != nil {
				localFailures++
				input.Unavailable = append(input.Unavailable, operational.Unavailable{
					SourceKind: operational.SourceLocalWork, Code: "pending-work-unavailable",
					Summary: "A pending shared write could not be decoded safely",
				})
			}
			input.LocalLeases = append(input.LocalLeases, operational.LocalLeaseEvidence{
				Lease: lease.Clone(), ObservedAt: lease.RenewedAt.UTC(), Pending: pending,
			})
		}
	}
	if localObservedAt.IsZero() {
		localObservedAt = s.clock.Now().UTC()
	}
	localFreshness := operational.SourceCurrent
	if localFailures > 0 {
		localFreshness = operational.SourceDegraded
		if readableSpaces == 0 {
			localFreshness = operational.SourceUnavailable
		}
	}
	input.Sources = append(input.Sources, operational.SourceEvidence{
		Kind: operational.SourceLocalWork, Revision: digestLeaseSet(leaseBytes),
		ObservedAt: localObservedAt, Freshness: localFreshness,
	})
	return operational.Build(input, s.clock, s.config.Limits)
}

func (s *Source) pendingProjection(lease workreport.Lease) (*operational.SharedPending, error) {
	if lease.Pending == nil {
		return nil, nil
	}
	projection := &operational.SharedPending{
		OperationID: lease.Pending.OperationKey, Action: string(lease.Pending.Action),
		Stage: "none", State: "", RemainingAction: "retry-push",
	}
	if !lease.Pending.Shared.Attempted {
		return projection, nil
	}
	decoded, err := s.pending.DecodePending(lease.Pending.Shared.WriteResult.Bytes())
	if err != nil {
		return nil, err
	}
	projection.Stage, projection.State, projection.RemainingAction = decoded.Stage, decoded.State, decoded.RemainingAction
	return projection, nil
}

func (s *Source) updateSyncDegradations(results []SyncResult, sources []cache.OperationalSpaceSource) map[string]SyncResult {
	configured := make(map[string]struct{}, len(s.config.Spaces))
	for _, spaceID := range s.config.Spaces {
		configured[spaceID] = struct{}{}
	}
	revisions := make(map[string]string, len(sources))
	for _, source := range sources {
		revisions[source.Space] = source.Revision
	}
	touched := make(map[string]struct{}, len(results))
	for _, result := range results {
		if _, ok := configured[result.Space]; !ok || result.Space == "" {
			continue
		}
		touched[result.Space] = struct{}{}
		if !result.Failed {
			delete(s.syncDegradations, result.Space)
			continue
		}
		if result.Code == "" {
			result.Code = "space-sync-failed"
		}
		if result.Summary == "" {
			result.Summary = "The latest fetch did not complete; cached committed evidence is retained"
		}
		s.syncDegradations[result.Space] = syncDegradation{result: result, revision: revisions[result.Space]}
	}
	for spaceID, degradation := range s.syncDegradations {
		if _, wasTouched := touched[spaceID]; wasTouched {
			continue
		}
		// A changed committed revision is an independently observable source
		// transition. It supersedes the old fetch failure even when the caller
		// reached us through an ordinary static Snapshot poll.
		if revisions[spaceID] != degradation.revision {
			delete(s.syncDegradations, spaceID)
		}
	}
	out := make(map[string]SyncResult, len(s.syncDegradations))
	for spaceID, degradation := range s.syncDegradations {
		out[spaceID] = degradation.result
	}
	return out
}

func digestLeaseSet(raw [][]byte) string {
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		values = append(values, string(item))
	}
	sort.Strings(values)
	hash := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
