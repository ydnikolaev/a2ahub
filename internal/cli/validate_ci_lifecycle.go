package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// ciLifecycleCandidate couples a schema-valid event with the report entry
// that will receive its merge-time lifecycle verdict.
type ciLifecycleCandidate struct {
	path   string
	event  lifecycleEventDoc
	report int
}

// ciBaseLegalityChecker is validate's one-method legality seam backed by the
// PR head's envelopes and the merge-base's event history. The caller excludes
// every changed event path while loading baseEvents, so an added or modified
// candidate is never folded into its own prior.
type ciBaseLegalityChecker struct {
	root       string
	manifest   space.Manifest
	baseEvents []lifecycleEventDoc
}

func (c ciBaseLegalityChecker) CheckLegality(candidate validate.CandidateEvent) (validate.Verdict, error) {
	actor := fold.Actor{
		Kind:   candidate.Actor.Kind,
		Name:   candidate.Actor.Name,
		System: candidate.Actor.System,
	}
	membership := lifecycleMembership(c.manifest)

	if candidate.Transition == fold.TVerify || candidate.Transition == fold.TDispute {
		_, response, err := lifecycleLoadEnvelope(c.root, candidate.Subject)
		if err != nil {
			return 0, err
		}
		if response.Parent == "" {
			return 0, fmt.Errorf("cli: response %s carries no `parent` link", candidate.Subject)
		}
		parentEnv, _, err := lifecycleLoadEnvelope(c.root, response.Parent)
		if err != nil {
			return 0, err
		}
		prior := fold.Fold(
			parentEnv.Kind,
			parentEnv,
			lifecycleFoldEvents(c.baseEvents, response.Parent),
			membership,
		)
		substate := fold.State("")
		if prior.Responses != nil {
			substate = prior.Responses[candidate.Subject]
		}
		return mapFoldVerdict(fold.CheckLegality(
			fold.KindResponse,
			substate,
			candidate.Transition,
			parentEnv,
			actor,
			membership(actor.System),
		)), nil
	}

	env, _, err := lifecycleLoadEnvelope(c.root, candidate.Subject)
	if err != nil {
		return 0, err
	}
	events := lifecycleFoldEvents(c.baseEvents, candidate.Subject)
	prior := fold.NewResult(env.Kind)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, membership)
	}
	return mapFoldVerdict(fold.CheckCandidate(
		env.Kind,
		prior,
		candidate.Transition,
		contractCanonicalVersion(candidate.Version),
		env,
		actor,
		membership(actor.System),
	)), nil
}

// lifecycleReadBaseEvents reads all event documents from base except the event
// paths changed by this PR. Both the tree listing and every blob are bounded.
func lifecycleReadBaseEvents(ctx context.Context, root, base string, changed map[string]bool) ([]lifecycleEventDoc, error) {
	if !contractLooksLikeObjectID(base) {
		return nil, fmt.Errorf("cli: %q is not a git object id", base)
	}
	rawPaths, err := contractGitBounded(
		ctx,
		root,
		maxMirrorEventBytes,
		"ls-tree", "-r", "-z", "--name-only", base, "--",
	)
	if err != nil {
		return nil, fmt.Errorf("cli: read merge-base event tree %s: %w", base, err)
	}
	paths := strings.FieldsFunc(string(rawPaths), func(r rune) bool { return r == 0 })
	sort.Strings(paths)

	events := make([]lifecycleEventDoc, 0, len(paths))
	for _, relPath := range paths {
		relPath = filepath.ToSlash(relPath)
		if changed[relPath] || !isEventDocument(relPath) {
			continue
		}
		raw, err := contractGitBounded(
			ctx,
			root,
			maxMirrorEventBytes,
			"show", base+":"+relPath,
		)
		if err != nil {
			return nil, fmt.Errorf("cli: read merge-base event %s:%s: %w", base, relPath, err)
		}
		var event lifecycleEventDoc
		if err := yaml.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("cli: decode merge-base event %s:%s: %w", base, relPath, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func lifecycleCandidate(doc lifecycleEventDoc) validate.CandidateEvent {
	return validate.CandidateEvent{
		Subject:    doc.Subject,
		Transition: doc.Transition,
		Version:    doc.Version,
		Actor: validate.Actor{
			Kind:   doc.Actor.Kind,
			Name:   doc.Actor.Name,
			System: doc.Actor.System,
		},
	}
}
