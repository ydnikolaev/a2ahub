package cache

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"github.com/ydnikolaev/a2ahub/internal/sensitive"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"gopkg.in/yaml.v3"
)

var errInvalidOperationalCheckpoint = errors.New("cache: invalid operational checkpoint")

const (
	maximumOperationalActorModelRunes = 96
	maximumOperationalSummaryRunes    = 512
	maximumOperationalWaitIDRunes     = 512
)

var (
	operationalCredentialAssignmentPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(password|passwd|token|authorization|api[-_]?key|secret)[[:space:]]*[:=]`)
	operationalBearerCredentialPattern     = regexp.MustCompile(`(?i)(^|[^a-z0-9])bearer([[:space:]]+|[:=])`)
)

// OperationalEvidence is cache's neutral, one-pass projection input. It
// deliberately carries no operational freshness precedence or ordering
// policy; internal/operational remains the sole owner of those decisions.
type OperationalEvidence struct {
	Sources       []OperationalSpaceSource
	Threads       []OperationalThread
	CommittedWork []OperationalCommittedWork
	Unavailable   []OperationalUnavailable
}

// OperationalSpaceSource describes one cache mirror used as operational evidence.
type OperationalSpaceSource struct {
	Space      string
	Revision   string
	SyncedAt   time.Time
	ObservedAt time.Time
	Freshness  string
}

// OperationalUnavailable records a bounded explanation for unavailable cache evidence.
type OperationalUnavailable struct {
	Space   string
	Code    string
	Summary string
}

// OperationalThread is the cache-level projection of one protocol thread.
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

// OperationalMilestone describes the latest meaningful protocol transition.
type OperationalMilestone struct {
	At         time.Time
	Actor      OperationalActor
	Transition string
	Subject    string
}

// OperationalActor identifies the actor that created a milestone.
type OperationalActor struct {
	Kind    string
	Name    string
	System  string
	Model   string
	Session string
}

// OperationalCommittedWork is a validated status checkpoint from a mirror.
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
			entry.Event.Actor.System == "" {
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
	frontmatter, parsed := operationalFrontmatter(member.Raw)
	if !parsed {
		return OperationalCommittedWork{}, false, nil
	}
	envelope, decoded := operationalWorkEnvelopeFromYAML(frontmatter)
	if !decoded {
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
	checkpoint := OperationalCommittedWork{
		Space: member.SpaceID, Thread: member.Env.Thread, WorkID: envelope.Work.ID,
		SubjectRef: envelope.Work.SubjectRef, Mode: envelope.Work.Mode, Summary: envelope.Work.Summary,
		Actor:      workreport.Actor{Kind: envelope.Actor.Kind, Name: envelope.Actor.Name, System: envelope.From, Model: envelope.Actor.Model, Session: envelope.Actor.Session},
		WaitingOn:  append([]workreport.WaitingOn(nil), envelope.Work.WaitingOn...),
		ReportedAt: reportedAt.UTC(), ValidUntil: validUntil.UTC(), ArtifactID: envelope.ID, CommitSequence: sequence,
	}
	return safeOperationalCheckpoint(checkpoint), true, nil
}

// safeOperationalCheckpoint is the single cache/public boundary for durable
// work-report text. Legacy mirrors remain readable, but credential-shaped
// model/summary/wait values cannot reach operational or transcript consumers,
// and an unsafe session survives only as the repository's canonical digest
// reference. Structural meaning and commit-derived ordering metadata are
// copied unchanged.
func safeOperationalCheckpoint(checkpoint OperationalCommittedWork) OperationalCommittedWork {
	checkpoint.SubjectRef = safeOperationalText(checkpoint.SubjectRef, maximumOperationalWaitIDRunes)
	checkpoint.Actor.Name = safeOperationalText(checkpoint.Actor.Name, maximumOperationalWaitIDRunes)
	checkpoint.Actor.Model = safeOperationalText(checkpoint.Actor.Model, maximumOperationalActorModelRunes)
	checkpoint.Actor.Session = provenance.SafeSessionEvidence(checkpoint.Actor.Session)
	checkpoint.Summary = safeOperationalText(checkpoint.Summary, maximumOperationalSummaryRunes)
	checkpoint.WaitingOn = append([]workreport.WaitingOn(nil), checkpoint.WaitingOn...)
	for index := range checkpoint.WaitingOn {
		checkpoint.WaitingOn[index].ID = safeOperationalText(checkpoint.WaitingOn[index].ID, maximumOperationalWaitIDRunes)
		checkpoint.WaitingOn[index].Summary = safeOperationalText(checkpoint.WaitingOn[index].Summary, maximumOperationalSummaryRunes)
	}
	return checkpoint
}

// safeOperationalText matches the established operational read-model policy:
// bounded Unicode text remains readable, control/bidi characters become data,
// and any credential-bearing field is replaced as a whole rather than partly
// exposing a legacy value.
func safeOperationalText(value string, maximum int) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	out := make([]rune, 0, min(utf8.RuneCountInString(value), maximum))
	for _, r := range value {
		if len(out) >= maximum {
			break
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			out = append(out, '�')
			continue
		}
		out = append(out, r)
	}
	result := string(out)
	if containsOperationalCredential(result) {
		return "[redacted unsafe text]"
	}
	return result
}

func containsOperationalCredential(value string) bool {
	if sensitive.ContainsContent(value) || operationalCredentialAssignmentPattern.MatchString(value) ||
		operationalBearerCredentialPattern.MatchString(value) {
		return true
	}
	tokens := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("._:-", r)
	})
	for _, token := range tokens {
		if sensitive.Identifier(token) {
			return true
		}
	}
	return false
}

func operationalFrontmatter(raw []byte) ([]byte, bool) {
	frontmatter, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, false
	}
	return frontmatter.YAML, true
}

func operationalWorkEnvelopeFromYAML(raw []byte) (operationalWorkEnvelope, bool) {
	var envelope operationalWorkEnvelope
	if err := yaml.Unmarshal(raw, &envelope); err != nil {
		return operationalWorkEnvelope{}, false
	}
	return envelope, true
}
