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
	Actor      struct {
		Kind   string `yaml:"kind"`
		Name   string `yaml:"name"`
		System string `yaml:"system"`
	} `yaml:"actor"`
}

// CommittedEvents reads every committed event/v1 YAML file under
// mirrorDir/system/events/<year>/*.yaml and returns the subset whose
// `subject` equals subject, as fold.Event values (ULID/Subject/
// Transition/Actor only — no ClaimedState, no CommitSeq: the identical
// shape the two former adapter-local copies produced). An absent
// events/ directory (fresh mirror, nothing committed for this system yet)
// returns (nil, nil), never an error.
func CommittedEvents(mirrorDir, system, subject string) ([]fold.Event, error) {
	dir := filepath.Join(mirrorDir, system, "events")
	years, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []fold.Event
	for _, year := range years {
		if !year.IsDir() {
			continue
		}
		yearDir := filepath.Join(dir, year.Name())
		files, err := os.ReadDir(yearDir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			raw, err := readBounded(filepath.Join(yearDir, f.Name()), maxCacheReadBytes)
			if err != nil {
				return nil, err
			}
			var ev mirrorCommittedEvent
			if err := yaml.Unmarshal(raw, &ev); err != nil {
				return nil, fmt.Errorf("cache: decode committed event %s: %w", f.Name(), err)
			}
			if ev.Subject != subject {
				continue
			}
			out = append(out, fold.Event{
				ULID:       ev.Event,
				Subject:    ev.Subject,
				Transition: ev.Transition,
				Actor:      fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
			})
		}
	}
	return out, nil
}
