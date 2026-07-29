package notification

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

const (
	defaultLeaseTTL = 2 * time.Minute
	maxClaimLimit   = 50
)

type deliveryState struct {
	Schema    int                      `json:"schema"`
	Channel   Channel                  `json:"channel"`
	Consumer  string                   `json:"consumer"`
	Projects  map[string]*projectState `json:"projects"`
	Lease     *deliveryLease           `json:"lease,omitempty"`
	Completed map[string][]string      `json:"completed,omitempty"`
}

type projectState struct {
	Baselined bool             `json:"baselined"`
	Seen      map[string]bool  `json:"seen"`
	Pending   map[string]Entry `json:"pending"`
}

type deliveryLease struct {
	Token     string    `json:"token"`
	ProjectID string    `json:"project_id"`
	EntryIDs  []string  `json:"entry_ids"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DeliveryStore persists channel/consumer-specific at-least-once state.
type DeliveryStore struct {
	Root     string
	Now      func() time.Time
	LeaseTTL time.Duration
	Mint     func() (string, error)
}

// NewDeliveryStore constructs a delivery store rooted in disposable cache.
func NewDeliveryStore(root string, now func() time.Time) *DeliveryStore {
	return &DeliveryStore{
		Root: root, Now: now, LeaseTTL: defaultLeaseTTL,
		Mint: func() (string, error) {
			id, err := artifact.MintULIDAt(now(), rand.Reader)
			return id.String(), err
		},
	}
}

func (s *DeliveryStore) statePath(channel Channel, consumer string) (string, error) {
	if !ValidChannel(channel) || !validConsumer(consumer) {
		return "", ErrInvalidConsumer
	}
	hash := stableID("", consumer)
	return filepath.Join(s.Root, "delivery", string(channel), hash+".json"), nil
}

// Claim leases a bounded batch after establishing a no-history baseline.
func (s *DeliveryStore) Claim(ctx context.Context, channel Channel, consumer, projectID string, current []Entry, limit int) (ClaimResult, error) {
	if limit < 1 || limit > maxClaimLimit {
		return ClaimResult{}, ErrInvalidLimit
	}
	path, err := s.statePath(channel, consumer)
	if err != nil {
		return ClaimResult{}, err
	}
	result := ClaimResult{Entries: []Entry{}}
	err = withFileLock(ctx, path, func() error {
		state := deliveryState{
			Schema: SchemaVersion, Channel: channel, Consumer: consumer,
			Projects: map[string]*projectState{}, Completed: map[string][]string{},
		}
		if err := readJSON(path, &state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if state.Schema != SchemaVersion || state.Channel != channel || state.Consumer != consumer {
			return ErrDeliveryStateMismatch
		}
		if state.Projects == nil {
			state.Projects = map[string]*projectState{}
		}
		if state.Completed == nil {
			state.Completed = map[string][]string{}
		}
		project := state.Projects[projectID]
		if project == nil {
			project = &projectState{Seen: map[string]bool{}, Pending: map[string]Entry{}}
			state.Projects[projectID] = project
		}
		if project.Seen == nil {
			project.Seen = map[string]bool{}
		}
		if project.Pending == nil {
			project.Pending = map[string]Entry{}
		}

		// The first read establishes the no-history baseline. Level state is
		// still visible immediately through status; only transition toasts are
		// suppressed.
		if !project.Baselined {
			for _, entry := range current {
				project.Seen[entry.EntryID] = true
			}
			project.Baselined = true
			return writeJSON(path, state)
		}

		if state.Lease != nil && state.Lease.ExpiresAt.After(s.Now()) {
			return nil // another window/process owns this consumer's batch
		}
		state.Lease = nil
		currentIDs := make(map[string]bool, len(current))
		for _, entry := range current {
			currentIDs[entry.EntryID] = true
		}
		// Seen is edge memory, not a permanent tombstone. Once a level entry
		// clears, forget its seen identity so the same semantic condition can
		// emit again after a later clear→set transition.
		for id := range project.Seen {
			if !currentIDs[id] {
				if _, stillPending := project.Pending[id]; !stillPending {
					delete(project.Seen, id)
				}
			}
		}
		for _, entry := range current {
			if !project.Seen[entry.EntryID] {
				project.Pending[entry.EntryID] = entry
			}
		}
		ids := make([]string, 0, len(project.Pending))
		for id := range project.Pending {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) > limit {
			ids = ids[:limit]
		}
		if len(ids) == 0 {
			return writeJSON(path, state)
		}
		nonce, err := s.Mint()
		if err != nil {
			return err
		}
		hash := stableID("", consumer)
		token := strings.Join([]string{"ls", string(channel), hash, nonce}, "_")
		expires := s.Now().UTC().Add(s.LeaseTTL)
		state.Lease = &deliveryLease{
			Token: token, ProjectID: projectID, EntryIDs: ids, ExpiresAt: expires,
		}
		for _, id := range ids {
			result.Entries = append(result.Entries, project.Pending[id])
		}
		result.Lease = &Lease{
			Token: token, ConsumerID: consumer, Channel: channel,
			ProjectID: projectID, ExpiresAt: expires,
		}
		return writeJSON(path, state)
	})
	return result, err
}

// Ack idempotently accepts only entries owned by the supplied lease.
func (s *DeliveryStore) Ack(ctx context.Context, token string, entryIDs []string) (AckResult, error) {
	channel, consumerHash, err := parseLeaseToken(token)
	if err != nil {
		return AckResult{}, err
	}
	path := filepath.Join(s.Root, "delivery", string(channel), consumerHash+".json")
	result := AckResult{Acked: []string{}, AlreadyAcked: []string{}}
	err = withFileLock(ctx, path, func() error {
		var state deliveryState
		if err := readJSON(path, &state); err != nil {
			return err
		}
		if state.Lease == nil || state.Lease.Token != token {
			if completed, ok := state.Completed[token]; ok {
				allowed := make(map[string]bool, len(completed))
				for _, id := range completed {
					allowed[id] = true
				}
				for _, id := range entryIDs {
					if !allowed[id] {
						return fmt.Errorf("%w: entry %s", ErrEntryNotLeased, id)
					}
					result.AlreadyAcked = append(result.AlreadyAcked, id)
				}
				sort.Strings(result.AlreadyAcked)
				return nil
			}
			return ErrLeaseNotFound
		}
		project := state.Projects[state.Lease.ProjectID]
		if project == nil {
			return ErrDeliveryStateMismatch
		}
		leased := make(map[string]bool, len(state.Lease.EntryIDs))
		for _, id := range state.Lease.EntryIDs {
			leased[id] = true
		}
		for _, id := range entryIDs {
			if !leased[id] {
				return fmt.Errorf("%w: entry %s", ErrEntryNotLeased, id)
			}
			if project.Seen[id] {
				result.AlreadyAcked = append(result.AlreadyAcked, id)
				continue
			}
			project.Seen[id] = true
			delete(project.Pending, id)
			result.Acked = append(result.Acked, id)
		}
		sort.Strings(result.Acked)
		sort.Strings(result.AlreadyAcked)
		allDone := true
		for _, id := range state.Lease.EntryIDs {
			if !project.Seen[id] {
				allDone = false
				break
			}
		}
		if allDone {
			if state.Completed == nil {
				state.Completed = map[string][]string{}
			}
			state.Completed[token] = append([]string(nil), state.Lease.EntryIDs...)
			if len(state.Completed) > 32 {
				tokens := make([]string, 0, len(state.Completed))
				for completedToken := range state.Completed {
					tokens = append(tokens, completedToken)
				}
				sort.Strings(tokens)
				for _, completedToken := range tokens[:len(tokens)-32] {
					delete(state.Completed, completedToken)
				}
			}
			state.Lease = nil
		}
		return writeJSON(path, state)
	})
	return result, err
}

// PurgeChannels removes only disposable delivery state for explicitly selected
// channels. The fixed channel directory is derived after validation; project
// truth and the human inbox cursor are outside this root and cannot be touched.
func (s *DeliveryStore) PurgeChannels(channels []Channel) error {
	if s.Root == "" {
		return errors.New("notification delivery root is empty")
	}
	for _, channel := range channels {
		if !ValidChannel(channel) {
			return ErrInvalidChannel
		}
		if err := os.RemoveAll(filepath.Join(s.Root, "delivery", string(channel))); err != nil {
			return err
		}
	}
	if len(channels) == 2 {
		if err := os.Remove(filepath.Join(s.Root, "routes.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func validConsumer(consumer string) bool {
	if len(consumer) < 1 || len(consumer) > 128 {
		return false
	}
	return consumerPattern.MatchString(consumer)
}

var consumerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func parseLeaseToken(token string) (Channel, string, error) {
	parts := strings.Split(token, "_")
	if len(parts) != 4 || parts[0] != "ls" || len(parts[2]) != 32 || len(parts[3]) != 26 {
		return "", "", ErrLeaseNotFound
	}
	channel := Channel(parts[1])
	if !ValidChannel(channel) || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(parts[2]) {
		return "", "", ErrLeaseNotFound
	}
	return channel, parts[2], nil
}

var (
	// ErrInvalidConsumer rejects unsafe or unbounded adapter identities.
	ErrInvalidConsumer = errors.New("invalid notification consumer")
	// ErrInvalidLimit rejects unbounded adapter claims.
	ErrInvalidLimit = errors.New("notification claim limit must be between 1 and 50")
	// ErrDeliveryStateMismatch rejects state owned by another consumer/channel.
	ErrDeliveryStateMismatch = errors.New("notification delivery state identity mismatch")
	// ErrLeaseNotFound rejects unknown or expired lease identities.
	ErrLeaseNotFound = errors.New("notification lease not found")
	// ErrEntryNotLeased rejects acknowledgement outside the leased batch.
	ErrEntryNotLeased = errors.New("notification entry was not leased")
)
