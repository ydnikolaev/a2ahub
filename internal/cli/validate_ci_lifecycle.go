package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
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
// PR head's subject envelopes and the merge-base's event history. The caller
// excludes every changed event path while loading baseEvents, so an added or
// modified candidate is never folded into its own prior.
//
// The subject envelope and, for a decision supersede, the successor facts
// arrive ON the candidate (lifecycleCandidate below), the same two facts the
// submit path's candidate carries, so this seam judges fold's declared
// successor preconditions instead of refusing them for a nil successor.
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

	env := fold.Envelope{
		ID: candidate.Envelope.ID, Kind: fold.Kind(candidate.Envelope.Kind), From: candidate.Envelope.From,
		To: candidate.Envelope.To, RequiredApprovers: candidate.Envelope.RequiredApprovers,
	}
	prior := lifecycleBaseFold(env, c.baseEvents, membership)
	// nil stays nil: an unresolved successor is D9's refusal, never a
	// zero-valued "resolved" fact (fold.SuccessorFacts' own doc comment).
	var successor *fold.SuccessorFacts
	if candidate.SuccessorEnvelope != nil {
		successor = &fold.SuccessorFacts{
			Author: candidate.SuccessorEnvelope.Author,
			State:  fold.State(candidate.SuccessorEnvelope.State),
		}
	}
	return mapFoldVerdict(fold.CheckCandidateWithSuccessor(
		env.Kind,
		prior,
		candidate.Transition,
		contractCanonicalVersion(candidate.Version),
		env,
		actor,
		membership(actor.System),
		successor,
	)), nil
}

// lifecycleBaseFold folds env's merge-base history: the prior a candidate on
// env is judged against, and the state a decision supersede's successor is
// judged at. One fold for both, so the two sides of a supersede are read
// from the same tree.
func lifecycleBaseFold(env fold.Envelope, baseEvents []lifecycleEventDoc, membership fold.MembershipView) fold.Result {
	events := lifecycleFoldEvents(baseEvents, env.ID)
	if len(events) == 0 {
		return fold.NewResult(env.Kind)
	}
	return fold.Fold(env.Kind, env, events, membership)
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

// lifecycleCandidate builds the merge-time candidate with the same two facts
// the submit path's candidate carries (SubmitValidatorAdapter, adapters.go):
// the subject's own envelope, read from the PR head as before, and the facts
// about a decision supersede's successor that fold's declared row
// preconditions check (fold/table.go).
//
// Without either fact V3 could not judge those rows at all: a nil successor
// refuses every precondition-bearing row, and an empty envelope kind made
// checkLifecycle label that refusal LFC-002 instead of LFC-005.
func lifecycleCandidate(ctx context.Context, root, base string, manifest space.Manifest, baseEvents []lifecycleEventDoc, doc lifecycleEventDoc) (validate.CandidateEvent, error) {
	env, _, err := lifecycleLoadEnvelope(root, doc.Subject)
	if err != nil {
		return validate.CandidateEvent{}, err
	}
	candidate := validate.CandidateEvent{
		Subject:    doc.Subject,
		Transition: doc.Transition,
		Version:    doc.Version,
		Actor: validate.Actor{
			Kind:   doc.Actor.Kind,
			Name:   doc.Actor.Name,
			System: doc.Actor.System,
		},
		Envelope: validate.Envelope{
			ID: env.ID, Kind: string(env.Kind), From: env.From,
			To: env.To, RequiredApprovers: env.RequiredApprovers,
		},
	}
	successor, err := lifecycleBaseSuccessor(ctx, root, base, manifest, baseEvents, env.Kind, doc)
	if err != nil {
		return validate.CandidateEvent{}, err
	}
	candidate.SuccessorEnvelope = successor
	return candidate, nil
}

// lifecycleBaseSuccessor resolves a decision supersede's successor AT THE
// MERGE BASE: its envelope from the base tree and its state from baseEvents,
// the same tree the predecessor's prior is folded from. A successor whose
// approval rides in the same PR as the supersede is not yet a fact the space
// agreed to.
//
// This is the merge-time reading of what the CLI's pre-write gate asks
// (lifecycleResolveSuccessorFacts -> cache.SuccessorFactsForCandidate): that
// gate reads the author's synced mirror, i.e. the landed default branch,
// which is what the merge base is here. The cache reader cannot be reused
// because it reads a working directory, and in V3 the working directory is
// the PR head. The rule it applies is the same: the first ref names the
// successor, `from` is its author, and its state is fold's own fold of its
// committed history.
//
// Absent and failed are different outcomes (AGENTS.md row 22). A ref that
// names no artifact at base returns nil, D9's "unresolved", which refuses as
// LFC-005 plus LFC-006. A read that FAILED returns an error, which the caller
// reports on the event rather than calling the successor absent.
func lifecycleBaseSuccessor(ctx context.Context, root, base string, manifest space.Manifest, baseEvents []lifecycleEventDoc, kind fold.Kind, doc lifecycleEventDoc) (*validate.SuccessorEnvelope, error) {
	// cache.SuccessorFactsForCandidate's own guard: only the two
	// decision-supersede rows carry a precondition, so every other candidate
	// skips the read without changing a verdict.
	if doc.Transition != fold.TSupersede || kind != fold.KindDecision || len(doc.Refs) == 0 || doc.Refs[0].Ref == "" {
		return nil, nil
	}
	successorID := doc.Refs[0].Ref
	successorKind, relPath, named := lifecycleArtifactLocation(successorID)
	if !named {
		return nil, nil
	}
	listed, err := contractGitBounded(ctx, root, maxMirrorEventBytes, "ls-tree", "-z", "--name-only", base, "--", relPath)
	if err != nil {
		return nil, fmt.Errorf("cli: look up supersede successor %s at merge base %s: %w", successorID, base, err)
	}
	if len(listed) == 0 {
		return nil, nil
	}
	raw, err := contractGitBounded(ctx, root, maxMirrorEventBytes, "show", base+":"+relPath)
	if err != nil {
		return nil, fmt.Errorf("cli: read supersede successor %s at merge base %s: %w", successorID, base, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("cli: supersede successor %s at merge base: %w", successorID, err)
	}
	var probe lifecycleEnvelopeProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return nil, fmt.Errorf("cli: supersede successor %s at merge base: cannot decode envelope: %w", successorID, err)
	}
	env := fold.Envelope{
		ID: successorID, Kind: successorKind, From: probe.From,
		To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
	}
	prior := lifecycleBaseFold(env, baseEvents, lifecycleMembership(manifest))
	return &validate.SuccessorEnvelope{Author: probe.From, State: string(prior.State)}, nil
}

// lifecycleArtifactLocation reports the kind and committed path id names, and
// false when id names no artifact at all: outside the id grammar, an unknown
// prefix, or no layout location. That is an absence, not a failed read, which
// is why the parse errors stop here rather than travelling up as errors.
func lifecycleArtifactLocation(id string) (fold.Kind, string, bool) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return "", "", false
	}
	kind, known := lifecyclePrefixInfo[parsed.Prefix]
	if !known {
		return "", "", false
	}
	relPath, err := lifecycleArtifactPath(parsed)
	if err != nil {
		return "", "", false
	}
	return kind, relPath, true
}
