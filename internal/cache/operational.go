package cache

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"gopkg.in/yaml.v3"
)

var errInvalidOperationalCheckpoint = errors.New("cache: invalid operational checkpoint")

// OperationalEvidence is cache's neutral, one-pass projection input. It
// deliberately carries no operational freshness precedence or ordering
// policy; internal/operational remains the sole owner of those decisions.
type OperationalEvidence struct {
	Sources       []OperationalSpaceSource
	Threads       []OperationalThread
	CommittedWork []OperationalCommittedWork
	Unavailable   []OperationalUnavailable
}

type OperationalSpaceSource struct {
	Space      string
	Revision   string
	SyncedAt   time.Time
	ObservedAt time.Time
	Freshness  string
}

type OperationalUnavailable struct {
	Space   string
	Code    string
	Summary string
}

type OperationalThread struct {
	Space           string
	Thread          string
	Title           string
	Participants    []string
	Settled         bool
	OpenCount       int
	WaitingOn       []string
	YourMove        bool
	BlockingBy      []string
	LatestMilestone *OperationalMilestone
}

type OperationalMilestone struct {
	At         time.Time
	Actor      OperationalActor
	Transition string
	Subject    string
}

type OperationalActor struct {
	Kind    string
	Name    string
	System  string
	Model   string
	Session string
}

type OperationalCommittedWork struct {
	Space          string
	Thread         string
	WorkID         string
	SubjectRef     string
	Mode           workreport.Mode
	Summary        string
	Actor          workreport.Actor
	WaitingOn      []workreport.WaitingOn
	ReportedAt     time.Time
	ValidUntil     time.Time
	ArtifactID     string
	CommitSequence uint64
}

type operationalWorkEnvelope struct {
	Schema   string `yaml:"schema"`
	ID       string `yaml:"id"`
	Type     string `yaml:"type"`
	Space    string `yaml:"space"`
	From     string `yaml:"from"`
	Thread   string `yaml:"thread"`
	Category string `yaml:"category"`
	Actor    struct {
		Kind    string `yaml:"kind"`
		Name    string `yaml:"name"`
		Model   string `yaml:"model"`
		Session string `yaml:"session"`
	} `yaml:"actor"`
	Work *struct {
		ID               string                 `yaml:"id"`
		SemanticSequence uint64                 `yaml:"semantic_sequence"`
		Mode             workreport.Mode        `yaml:"mode"`
		SubjectRef       string                 `yaml:"subject_ref"`
		Summary          string                 `yaml:"summary"`
		ReportedAt       string                 `yaml:"reported_at"`
		ValidUntil       string                 `yaml:"valid_until"`
		WaitingOn        []workreport.WaitingOn `yaml:"waiting_on"`
	} `yaml:"work"`
}

// OperationalEvidence walks every configured mirror exactly once and retains
// readable spaces when another mirror is absent or corrupt. Repository paths
// and raw parser/git errors never cross this boundary.
func (s *Store) OperationalEvidence(ctx context.Context) (OperationalEvidence, error) {
	if err := ctx.Err(); err != nil {
		return OperationalEvidence{}, err
	}
	now := s.now().UTC()
	out := OperationalEvidence{
		Sources:       []OperationalSpaceSource{},
		Threads:       []OperationalThread{},
		CommittedWork: []OperationalCommittedWork{},
		Unavailable:   []OperationalUnavailable{},
	}
	spaces := s.spaceMirrorsSnapshot()
	sort.Slice(spaces, func(i, j int) bool { return spaces[i].SpaceID < spaces[j].SpaceID })
	for _, mirror := range spaces {
		if err := ctx.Err(); err != nil {
			return OperationalEvidence{}, err
		}
		source := operationalSpaceSource(ctx, now, s.ttl, mirror)
		members, skipped, err := buildIndex(ctx, mirror.SpaceID, mirror.Dir, s.ownSystem, mirror.Manifest)
		if err != nil {
			source.Freshness = "unavailable"
			out.Sources = append(out.Sources, source)
			out.Unavailable = append(out.Unavailable, OperationalUnavailable{
				Space: mirror.SpaceID, Code: "space-index-unavailable",
				Summary: "Committed operational evidence is unavailable for this space",
			})
			continue
		}
		out.Sources = append(out.Sources, source)
		if len(skipped) > 0 {
			out.Unavailable = append(out.Unavailable, OperationalUnavailable{
				Space: mirror.SpaceID, Code: "space-evidence-skipped",
				Summary: fmt.Sprintf("%d committed document(s) could not be decoded", len(skipped)),
			})
		}
		byThread := make(map[string][]foldedArtifact)
		statusWorkIDs := make(map[string]struct{})
		for _, member := range members {
			if member.Env.Thread == "" {
				continue
			}
			byThread[member.Env.Thread] = append(byThread[member.Env.Thread], member)
			checkpoint, isCheckpoint, checkpointErr := classifyOperationalCheckpoint(member)
			if isCheckpoint && checkpoint.ArtifactID != "" {
				// Recognition is independent of successful work decoding. A
				// degraded status checkpoint remains non-protocol bookkeeping.
				statusWorkIDs[checkpoint.ArtifactID] = struct{}{}
			}
			if checkpointErr != nil {
				out.Unavailable = append(out.Unavailable, OperationalUnavailable{
					Space: mirror.SpaceID, Code: "work-checkpoint-unavailable",
					Summary: "A committed work checkpoint could not be decoded safely",
				})
			}
			if isCheckpoint && checkpointErr == nil {
				out.CommittedWork = append(out.CommittedWork, checkpoint)
			}
		}
		threadIDs := make([]string, 0, len(byThread))
		for threadID := range byThread {
			threadIDs = append(threadIDs, threadID)
		}
		sort.Strings(threadIDs)
		for _, threadID := range threadIDs {
			view, renderErr := s.renderThread(threadID, "", mirror.SpaceID, byThread[threadID])
			if renderErr != nil {
				out.Unavailable = append(out.Unavailable, OperationalUnavailable{
					Space: mirror.SpaceID, Code: "thread-projection-unavailable",
					Summary: "A committed thread could not be projected",
				})
				continue
			}
			out.Threads = append(out.Threads, operationalThreadFromView(view, statusWorkIDs))
		}
	}
	sort.Slice(out.CommittedWork, func(i, j int) bool {
		a, b := out.CommittedWork[i], out.CommittedWork[j]
		if a.Space != b.Space {
			return a.Space < b.Space
		}
		if a.Thread != b.Thread {
			return a.Thread < b.Thread
		}
		if a.CommitSequence != b.CommitSequence {
			return a.CommitSequence < b.CommitSequence
		}
		return a.ArtifactID < b.ArtifactID
	})
	return out, nil
}

func classifyOperationalCheckpoint(member foldedArtifact) (OperationalCommittedWork, bool, error) {
	checkpoint, recognized, err := decodeOperationalCheckpoint(member)
	if recognized && checkpoint.ArtifactID == "" {
		checkpoint.ArtifactID = member.Env.ID
	}
	return checkpoint, recognized, err
}

func operationalSpaceSource(ctx context.Context, now time.Time, ttl time.Duration, mirror SpaceMirror) OperationalSpaceSource {
	age, synced := mirrorSyncAge(now, mirror.Dir)
	revision, err := runGitOutput(ctx, mirror.Dir, "rev-parse", "--verify", "HEAD")
	revision = strings.TrimSpace(revision)
	source := OperationalSpaceSource{
		Space: mirror.SpaceID, Revision: revision, ObservedAt: now, Freshness: "current",
	}
	if synced {
		source.SyncedAt = now.Add(-age).UTC()
	}
	if err != nil || revision == "" || !synced {
		source.Revision = "unavailable"
		source.Freshness = "unavailable"
		return source
	}
	if age > ttl {
		source.Freshness = "stale"
	}
	return source
}

func operationalThreadFromView(view ThreadResult, statusWorkIDs map[string]struct{}) OperationalThread {
	waiting := make([]string, 0)
	blocking := make([]string, 0)
	yourMove := false
	openCount := 0
	for _, item := range view.OpenItems {
		// A committed status checkpoint is work evidence carried through the
		// announcement protocol. It must never create a user-facing protocol
		// obligation in the process it describes.
		if _, isStatusWork := statusWorkIDs[item.ID]; isStatusWork {
			continue
		}
		// ThreadView deliberately retains non-terminal members whose only legal
		// moves are owner escape hatches (cancel/withdraw/supersede), but removes
		// those moves from WaitingOn. Such a member is historical context, not an
		// obligation owed by anyone. The HTML thread projection already consumes
		// this fold-owned distinction; the shared operational projection must use
		// the same admitted fact instead of counting every OpenItems row.
		if len(item.WaitingOn) == 0 {
			continue
		}
		openCount++
		waiting = append(waiting, item.WaitingOn...)
		yourMove = yourMove || item.YourMove
		if item.Blocking {
			blocking = append(blocking, item.WaitingOn...)
		}
	}
	thread := OperationalThread{
		Space: view.Space, Thread: view.Thread, Title: view.Opener.Title,
		Participants: append([]string(nil), view.Participants...), Settled: openCount == 0,
		OpenCount: openCount, WaitingOn: dedupSorted(waiting), YourMove: yourMove,
		BlockingBy: dedupSorted(blocking),
	}
	for index := len(view.Transcript) - 1; index >= 0; index-- {
		entry := view.Transcript[index]
		if entry.Kind != "event" || entry.Event == nil || entry.At.IsZero() ||
			entry.Event.Actor.Kind == "" || entry.Event.Actor.Name == "" ||
			entry.Event.Actor.System == "" || entry.Event.Actor.Session == "" {
			continue
		}
		// Publishing a status work checkpoint is transport bookkeeping for the
		// operational projection, not a user-facing process milestone. Keep it
		// in the canonical transcript, but do not let it hide the last meaningful
		// lifecycle transition on the overview timeline.
		if entry.Event.Transition == "publish" {
			if _, isStatusWork := statusWorkIDs[entry.Event.Subject]; isStatusWork {
				continue
			}
		}
		thread.LatestMilestone = &OperationalMilestone{
			At: entry.At.UTC(), Actor: OperationalActor{
				Kind: entry.Event.Actor.Kind, Name: entry.Event.Actor.Name, System: entry.Event.Actor.System,
				Model: entry.Event.Actor.Model, Session: entry.Event.Actor.Session,
			},
			Transition: entry.Event.Transition, Subject: entry.Event.Subject,
		}
		break
	}
	return thread
}

func decodeOperationalCheckpoint(member foldedArtifact) (OperationalCommittedWork, bool, error) {
	frontmatter, err := artifact.ParseFrontmatter(member.Raw)
	if err != nil {
		return OperationalCommittedWork{}, false, nil
	}
	var envelope operationalWorkEnvelope
	if err := yaml.Unmarshal(frontmatter.YAML, &envelope); err != nil {
		return OperationalCommittedWork{}, false, nil
	}
	if envelope.Schema != "envelope/v2" || envelope.Type != "announcement" || envelope.Category != "status" || envelope.Work == nil {
		return OperationalCommittedWork{}, false, nil
	}
	reportedAt, err := time.Parse(time.RFC3339, envelope.Work.ReportedAt)
	if err != nil || envelope.ID == "" || envelope.From == "" || envelope.Thread == "" ||
		envelope.Work.ID == "" || envelope.Work.SemanticSequence == 0 || envelope.Work.SubjectRef == "" ||
		envelope.Work.Summary == "" || !envelope.Work.Mode.Valid() || envelope.Actor.Kind == "" ||
		envelope.Actor.Name == "" || envelope.Actor.Session == "" {
		return OperationalCommittedWork{}, true, errInvalidOperationalCheckpoint
	}
	var validUntil time.Time
	if envelope.Work.ValidUntil != "" {
		validUntil, err = time.Parse(time.RFC3339, envelope.Work.ValidUntil)
		if err != nil {
			return OperationalCommittedWork{}, true, errInvalidOperationalCheckpoint
		}
	}
	if envelope.Work.Mode == workreport.ModeFinished && !validUntil.IsZero() ||
		envelope.Work.Mode != workreport.ModeFinished && validUntil.IsZero() ||
		envelope.Work.Mode == workreport.ModeWaiting && len(envelope.Work.WaitingOn) == 0 ||
		envelope.Work.Mode != workreport.ModeWaiting && len(envelope.Work.WaitingOn) != 0 {
		return OperationalCommittedWork{}, true, errInvalidOperationalCheckpoint
	}
	// commitOrder is zero-based, while operational's public evidence uses zero
	// as the explicit "order unavailable" sentinel. OrderKnown is therefore
	// load-bearing: the first real Git commit becomes sequence 1 and a missing
	// history can never masquerade as that first commit.
	if !member.OrderKnown || member.Seq < 0 {
		return OperationalCommittedWork{}, true, errInvalidOperationalCheckpoint
	}
	sequence := uint64(member.Seq) + 1
	return OperationalCommittedWork{
		Space: member.SpaceID, Thread: member.Env.Thread, WorkID: envelope.Work.ID,
		SubjectRef: envelope.Work.SubjectRef, Mode: envelope.Work.Mode, Summary: envelope.Work.Summary,
		Actor:      workreport.Actor{Kind: envelope.Actor.Kind, Name: envelope.Actor.Name, System: envelope.From, Model: envelope.Actor.Model, Session: envelope.Actor.Session},
		WaitingOn:  append([]workreport.WaitingOn(nil), envelope.Work.WaitingOn...),
		ReportedAt: reportedAt.UTC(), ValidUntil: validUntil.UTC(), ArtifactID: envelope.ID, CommitSequence: sequence,
	}, true, nil
}
