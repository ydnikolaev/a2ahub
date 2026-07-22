package mcp

// a2a_submit (OP-205/OP-220): mirrors internal/cli's cmd_submit.go
// SubmitCommand — same submitFirstTransition entry-transition table, same
// foreign-section/multi-space refusal order, same idempotent
// already-submitted short-circuit BEFORE the funnel, same
// submitEventDoc-shaped event authored per artifact.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// submitFirstTransition maps an envelope type to the §3.4 entry
// transition `a2a_submit` emits — mirrors internal/cli's
// submitFirstTransition table exactly.
var submitFirstTransition = map[string]string{
	"contract":     fold.TPublish,
	"requirement":  fold.TPublish,
	"question":     fold.TSubmit,
	"work_request": fold.TSubmit,
	"decision":     fold.TPropose,
	"handoff":      fold.TSubmit,
	"response":     fold.TSubmit,
	"announcement": fold.TPublish,
}

// SubmitDeps bundles WriteDeps with the extra pieces `a2a_submit` alone
// needs: the staging dir (where drafts to submit are read from) and the
// LegalityAdapter's own idempotency check (HasCommittedHistory).
type SubmitDeps struct {
	WriteDeps
	StagingDir string
	Legality   *LegalityAdapter
}

// SubmitInput is a2a_submit's structured input: an id ARRAY (OP-220
// all-or-nothing batch) AND a single id both flow through the same
// field — the structured form's own array-of-one covers the single-id
// case (plan 14 Brief item 4).
type SubmitInput struct {
	IDs []string `json:"ids"`
}

func newSubmitHandler(deps SubmitDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in SubmitInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("submit: invalid input: %w", err)
		}
		if len(in.IDs) == 0 {
			return submitResult{Verb: "submit", IDs: nil, State: "nothing-to-submit"}, "", nil
		}

		items, err := loadSubmitItems(deps.ReadFile, deps.StagingDir, in.IDs)
		if err != nil {
			return nil, "", fmt.Errorf("submit: %w", err)
		}

		for _, it := range items {
			if it.env.From != deps.OwnSystem {
				return nil, "", fmt.Errorf("submit: %s: refused (CC-002 foreign-section): artifact `from` %q does not match configured own system %q", it.path, it.env.From, deps.OwnSystem)
			}
		}
		for _, it := range items {
			if it.env.Space != items[0].env.Space {
				return nil, "", fmt.Errorf("submit: refused: batch spans multiple spaces (%q vs %q) — one submit is one space", items[0].env.Space, it.env.Space)
			}
		}

		var fresh []submitItem
		var alreadyDone []string
		for _, it := range items {
			has, herr := deps.Legality.HasCommittedHistory(it.env.ID)
			if herr != nil {
				return nil, "", fmt.Errorf("submit: %s: cannot check committed history: %w", it.path, herr)
			}
			if has {
				alreadyDone = append(alreadyDone, it.env.ID)
				continue
			}
			fresh = append(fresh, it)
		}
		if len(fresh) == 0 {
			return submitResult{Verb: "submit", IDs: alreadyDone, State: "already-submitted"}, "", nil
		}

		req, ids, err := buildSubmitRequest(deps, fresh)
		if err != nil {
			return nil, "", fmt.Errorf("submit: %w", err)
		}

		result, serr := deps.submit(ctx, req, "submit", ids)
		if serr != nil {
			return nil, "", serr
		}
		if len(alreadyDone) > 0 {
			if sr, ok := result.(submitResult); ok {
				sr.IDs = append(append([]string(nil), alreadyDone...), sr.IDs...)
				return sr, "", nil
			}
		}
		return result, "", nil
	}
}

type submitEnvelopeInfo struct {
	ID    string `yaml:"id"`
	Type  string `yaml:"type"`
	From  string `yaml:"from"`
	Space string `yaml:"space"`
	Actor struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"actor"`
}

type submitItem struct {
	path string
	raw  []byte
	env  submitEnvelopeInfo
}

// loadSubmitItems resolves each id to <stagingDir>/<id>.md (or a literal
// path, mirroring internal/cli's resolveSubmitTarget) and parses its
// envelope.
func loadSubmitItems(readFile func(string) ([]byte, error), stagingDir string, ids []string) ([]submitItem, error) {
	items := make([]submitItem, 0, len(ids))
	for _, id := range ids {
		path := id
		if !strings.Contains(id, "/") && !strings.HasSuffix(id, ".md") {
			path = filepath.Join(stagingDir, id+".md")
		}
		raw, err := readFile(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", path, err)
		}
		fm, err := artifact.ParseFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var env submitEnvelopeInfo
		if err := yaml.Unmarshal(fm.YAML, &env); err != nil {
			return nil, fmt.Errorf("%s: cannot decode envelope: %w", path, err)
		}
		items = append(items, submitItem{path: path, raw: raw, env: env})
	}
	return items, nil
}

// buildSubmitRequest assembles the ONE-commit SubmitRequest (D-026) —
// mirrors internal/cli's SubmitCommand.buildRequest exactly (commit
// message convention, sorted ids, min_binary_version read).
func buildSubmitRequest(deps SubmitDeps, fresh []submitItem) (space.SubmitRequest, []string, error) {
	layout, err := space.NewLayout(deps.OwnSystem)
	if err != nil {
		return space.SubmitRequest{}, nil, err
	}

	now := deps.Now()
	var files []space.FileWrite
	var ids []string
	for _, it := range fresh {
		sectionPath, err := submitSectionPath(layout, it.env.Type, it.env.ID)
		if err != nil {
			return space.SubmitRequest{}, nil, fmt.Errorf("%s: %w", it.path, err)
		}
		files = append(files, space.FileWrite{Path: sectionPath, Content: it.raw})

		transition, ok := submitFirstTransition[it.env.Type]
		if !ok {
			return space.SubmitRequest{}, nil, fmt.Errorf("%s: unknown envelope type %q", it.path, it.env.Type)
		}
		eventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return space.SubmitRequest{}, nil, fmt.Errorf("cannot mint event id: %w", err)
		}
		evDoc := eventDoc{
			Schema: "event/v1", Event: eventID.String(),
			Space:      it.env.Space,
			Subject:    it.env.ID,
			Transition: transition,
			Actor:      eventActor{Kind: it.env.Actor.Kind, Name: it.env.Actor.Name, System: deps.OwnSystem},
			At:         now.UTC().Format(time.RFC3339),
		}
		eventRaw, err := yaml.Marshal(evDoc)
		if err != nil {
			return space.SubmitRequest{}, nil, fmt.Errorf("cannot encode event for %s: %w", it.env.ID, err)
		}
		eventPath := layout.EventFile(now.UTC().Format("2006"), eventID.String())
		files = append(files, space.FileWrite{Path: eventPath, Content: eventRaw})
		ids = append(ids, it.env.ID)
	}
	sort.Strings(ids)

	commitMsg := fmt.Sprintf("a2a(%s): %s", fresh[0].env.Type, fresh[0].env.ID)
	if len(fresh) > 1 {
		commitMsg = fmt.Sprintf("a2a(batch): %s", strings.Join(ids, ", "))
	}

	minBinaryVersion, err := readMinBinaryVersion(deps.ReadFile, deps.MirrorDir)
	if err != nil {
		return space.SubmitRequest{}, nil, fmt.Errorf("cannot read space.yaml min_binary_version pin: %w", err)
	}

	baseBranch := deps.HostCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	return space.SubmitRequest{
		RepoDir:           deps.MirrorDir,
		System:            deps.OwnSystem,
		ArtifactID:        strings.Join(ids, "+"),
		Files:             files,
		CommitMessage:     commitMsg,
		CommitAuthorName:  deps.HostCfg.CommitAuthorName,
		CommitAuthorEmail: deps.HostCfg.CommitAuthorEmail,
		RemoteURL:         deps.HostCfg.RemoteURL,
		Repo:              deps.HostCfg.Repo,
		BaseBranch:        baseBranch,
		PRTitle:           commitMsg,
		Credential:        deps.HostCfg.Credential,
		MinBinaryVersion:  minBinaryVersion,
	}, ids, nil
}

func readMinBinaryVersion(readFile func(string) ([]byte, error), mirrorDir string) (string, error) {
	raw, err := readFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		return "", err
	}
	var probe struct {
		MinBinaryVersion string `yaml:"min_binary_version"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return "", fmt.Errorf("space.yaml is not valid yaml: %w", err)
	}
	return probe.MinBinaryVersion, nil
}

// submitSectionPath resolves envType/id's committed space-relative path
// per §4.2's layout — mirrors internal/cli's submitSectionPath.
func submitSectionPath(layout space.Layout, envType, id string) (string, error) {
	switch envType {
	case "contract":
		parsed, err := artifact.ParseID(id)
		if err != nil {
			return "", err
		}
		return layout.ProvidesContract(parsed.Slug), nil
	case "requirement":
		return layout.Requires(id), nil
	case "decision":
		return space.Decision(id), nil
	case "question", "work_request", "handoff", "response", "announcement":
		return layout.Exchange(id), nil
	default:
		return "", fmt.Errorf("unknown envelope type %q", envType)
	}
}
