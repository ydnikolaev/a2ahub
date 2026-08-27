package cache

// committed_events.go exports CommittedEvents: the identical
// subject-filtered committed-history read internal/cli's and
// internal/mcp's own LegalityAdapter.committedEvents each carried,
// verbatim, in both adapter files (spec agent-ops-2026-07/specs/
// 01-resolver-one-home.md §5, "also in scope, same disease"). Moved here
// rather than left duplicated a third time this phase touched the same
// files for the same reason (the resolver walk itself, this package's
// resolver_index.go).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
	"gopkg.in/yaml.v3"
)

// mirrorCommittedEvent is this package's own minimal decode of a committed
// event/v1 YAML file for CommittedEvents' subject-filtered read — the same
// ISP idiom internal/cli's own mirrorEvent doc comment names ("every layer
// in this repo owns its own minimal decode of the same underlying
// document ... rather than sharing one"), so this is a fourth independent
// copy of that shape by design, not an oversight.
type mirrorCommittedEvent struct {
	Event      string `yaml:"event"`
	Subject    string `yaml:"subject"`
	Transition string `yaml:"transition"`
	State      string `yaml:"state"`
	Version    string `yaml:"version"`
	Actor      struct {
		Kind    string `yaml:"kind"`
		Name    string `yaml:"name"`
		System  string `yaml:"system"`
		Model   string `yaml:"model"`
		Session string `yaml:"session"`
	} `yaml:"actor"`
	ProducedBy struct {
		Tool    string `yaml:"tool"`
		Version string `yaml:"version"`
	} `yaml:"produced_by"`
}

// CommittedEventHistory is the compatibility-preserving committed-event read:
// Events remains fold's minimal input, while EvidenceByULID retains bounded
// diagnostic fields that must never participate in lifecycle behavior.
type CommittedEventHistory struct {
	Events         []fold.Event
	EvidenceByULID map[string]provenance.EventEvidence
}

// CommittedEvents preserves the original []fold.Event API used by CLI/MCP
// legality adapters. It delegates to CommittedEventsWithEvidence so receipt
// decoding has one implementation; actor model/session and producer metadata
// remain outside fold.Event by construction.
func CommittedEvents(mirrorDir, system, subject string) ([]fold.Event, error) {
	history, err := CommittedEventsWithEvidence(mirrorDir, system, subject)
	if err != nil {
		return nil, err
	}
	return history.Events, nil
}

// CommittedEventsAllSections is CommittedEvents' cross-section sibling
// (no-silent-yes-2026-08 wave 2c, D-2): CommittedEvents reads exactly ONE
// participant's own <system>/events/ section, keyed by a caller-supplied
// system — the right read when the caller already knows which section a
// subject's own history is committed under (LegalityAdapter's own local
// history, keyed by the acting system). MirrorResolver.Successor's own
// need is different: an `approve` event on a successor decision is
// authored by (and therefore committed under) ANY approving participant's
// own section, never necessarily the successor id's own home system's
// section — the subject's home system and an event's own committing
// system are two different facts, and CommittedEvents alone cannot see
// past that. This reads mirrorDir/*/events/<year>/*.yaml — every
// participant's own section — and filters by subject, the SAME shape
// internal/cli's own lifecycleReadAllEvents (cmd_lifecycle.go) already
// applies for the verb path's own primary-artifact fold; this is that
// same all-sections read, moved down so BOTH internal/cli's and
// internal/mcp's MirrorResolver.Successor share ONE implementation
// (ADR-019) rather than each carrying a third, narrower copy.
//
// Composed from CommittedEventsWithEvidence per top-level directory entry
// rather than a second, independent decode loop: every per-file bounded-
// read/decode/error rule stays defined in exactly one place. A top-level
// entry that is not a real participant section (or carries no events/ dir
// at all) contributes zero events and no error, by the SAME "absent
// events/ directory is a zero history" rule CommittedEventsWithEvidence
// already documents — generalized one level up, so an absent mirrorDir
// itself is a zero history and a nil error too, never a caller-visible
// distinction from "no participant sections yet".
func CommittedEventsAllSections(mirrorDir, subject string) ([]fold.Event, error) {
	entries, err := os.ReadDir(mirrorDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var events []fold.Event
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		history, err := CommittedEventsWithEvidence(mirrorDir, entry.Name(), subject)
		if err != nil {
			return nil, err
		}
		events = append(events, history.Events...)
	}
	return events, nil
}

// CommittedEventsWithEvidence reads every committed event/v1 YAML file under
// mirrorDir/system/events/<year>/*.yaml and returns the subject-filtered fold
// inputs plus a provenance sidecar keyed by event ULID. Receipt state is also
// copied into fold.Event.ClaimedState because fold owns receipt comparison;
// model/session/producer values never enter fold input. An absent events/
// directory returns a zero history and nil error.
func CommittedEventsWithEvidence(mirrorDir, system, subject string) (CommittedEventHistory, error) {
	dir := filepath.Join(mirrorDir, system, "events")
	years, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return CommittedEventHistory{}, nil
		}
		return CommittedEventHistory{}, err
	}

	var history CommittedEventHistory
	for _, year := range years {
		if !year.IsDir() {
			continue
		}
		yearDir := filepath.Join(dir, year.Name())
		files, err := os.ReadDir(yearDir)
		if err != nil {
			return CommittedEventHistory{}, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			raw, err := readBounded(filepath.Join(yearDir, f.Name()), maxCacheReadBytes)
			if err != nil {
				return CommittedEventHistory{}, err
			}
			var ev mirrorCommittedEvent
			if err := yaml.Unmarshal(raw, &ev); err != nil {
				return CommittedEventHistory{}, fmt.Errorf("cache: decode committed event %s: %w", f.Name(), err)
			}
			if ev.Subject != subject {
				continue
			}
			history.Events = append(history.Events, fold.Event{
				ULID:         ev.Event,
				Subject:      ev.Subject,
				Transition:   ev.Transition,
				ClaimedState: fold.State(ev.State),
				Version:      canonicalEventVersion(ev.Version),
				Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
			})
			if history.EvidenceByULID == nil {
				history.EvidenceByULID = make(map[string]provenance.EventEvidence)
			}
			history.EvidenceByULID[ev.Event] = provenance.NewEventEvidence(
				ev.State,
				provenance.Actor{
					Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System,
					Model: ev.Actor.Model, Session: ev.Actor.Session,
				},
				provenance.Producer{Tool: ev.ProducedBy.Tool, Version: ev.ProducedBy.Version},
			)
		}
	}
	return history, nil
}
