package spacenotify

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/agentprompt"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// Mode selects how Render determines the candidate artifact set (T1's
// three flags: --base, --all, --only).
type Mode string

const (
	// ModePush takes the changed artifact set for a push range (T1's
	// --base): Options.Changed is the repo-relative path list a
	// gitChangedFilesFunc seam already produced — this package never
	// shells out to git itself.
	ModePush Mode = "push"
	// ModeAll takes every qualifying artifact in the space, ignoring any
	// push range (T1's --all).
	ModeAll Mode = "all"
	// ModeOnly takes exactly the named ids (T1's --only), bypassing the
	// push range, the per-route event filter and step 5's digest
	// coalescing — see keepForRoute and Render's own doc comment.
	ModeOnly Mode = "only"
)

// Options is Render's one input beyond the checkout itself.
type Options struct {
	Mode Mode
	// Changed is the push range's own changed-file set (repo-relative
	// paths, exactly gitChangedFilesFunc's own shape) — read ONLY in
	// ModePush.
	Changed []string
	// OnlyIDs is the explicit artifact id list — read ONLY in ModeOnly.
	OnlyIDs []string
	// Limit is step 5's coalescing threshold. Render never defaults it:
	// spec 03 T1 says the default lives in the CLI "and only there" — a
	// caller (cmd_notify.go) must always pass a positive value.
	Limit int
	// Now is the instant Overdue/determinism are measured against —
	// injected rather than read from time.Now() (rails: no buried clock).
	Now time.Time
}

// Render turns dir (a space-repo checkout) plus manifest into P3's
// ordered message array (spec 03's six classification/filtering steps).
// It calls internal/cache.BuildNotifyIndex exactly once — the fold this
// package performs — and never walks the tree itself.
func Render(ctx context.Context, dir string, manifest space.Manifest, opts Options) ([]Message, error) {
	// Step 6: refuse before anything is emitted.
	if err := refuseUndeclaredSecrets(manifest.NotificationRoutes); err != nil {
		return nil, err
	}

	idx, _, err := cache.BuildNotifyIndex(ctx, manifest.Space, dir, manifest, opts.Now)
	if err != nil {
		return nil, fmt.Errorf("notify render: %w", err)
	}

	classified := make([]classifiedArtifact, len(idx))
	byID := make(map[string]int, len(idx))
	for i, fa := range idx {
		classified[i] = classifiedArtifact{fa: fa, class: classify(fa)}
		byID[fa.ID] = i
	}

	candidates, err := selectCandidates(classified, byID, opts)
	if err != nil {
		return nil, err
	}

	var messages []Message
	for _, route := range manifest.NotificationRoutes {
		kept := keepForRoute(candidates, route, opts.Mode == ModeOnly)
		if opts.Mode != ModeOnly && opts.Limit > 0 && len(kept) > opts.Limit {
			messages = append(messages, buildDigest(route, kept))
			continue
		}
		for _, c := range kept {
			messages = append(messages, buildMessage(route, c))
		}
	}
	sortMessages(messages)
	return messages, nil
}

// classifiedArtifact pairs one projected artifact with its (global,
// route-independent) class.
type classifiedArtifact struct {
	fa    cache.NotifyArtifact
	class string
}

// selectCandidates resolves the push-range/all/only candidate set (spec
// 03 step 1, and T1's --only note). An unknown --only id is a refusal
// naming the id — the function returns before any candidate is kept, so
// Render never emits a partial result (AC14).
func selectCandidates(classified []classifiedArtifact, byID map[string]int, opts Options) ([]classifiedArtifact, error) {
	switch opts.Mode {
	case ModeAll:
		return classified, nil
	case ModeOnly:
		var out []classifiedArtifact
		seen := make(map[string]bool, len(opts.OnlyIDs))
		for _, id := range opts.OnlyIDs {
			i, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("notify render --only: unknown artifact id %q", id)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, classified[i])
		}
		return out, nil
	case ModePush:
		changed := make(map[string]bool, len(opts.Changed))
		for _, p := range opts.Changed {
			changed[p] = true
		}
		var out []classifiedArtifact
		for _, c := range classified {
			if changed[c.fa.RelPath] {
				out = append(out, c)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("notify render: unknown mode %q", opts.Mode)
	}
}

// keepForRoute applies step 4's two conditions (class in route.Events,
// receiving participant matches route.For) — bypassEvents (ModeOnly) skips
// the FIRST condition only, per T1's own note: `--only` bypasses the push
// range, the event filter and step 5's coalescing, but a route's `for`
// still scopes which channel an artifact reaches.
func keepForRoute(candidates []classifiedArtifact, route space.NotificationRoute, bypassEvents bool) []classifiedArtifact {
	var kept []classifiedArtifact
	for _, c := range candidates {
		if !routeMatches(c.fa, route) {
			continue
		}
		if !bypassEvents && !slices.Contains(route.Events, c.class) {
			continue
		}
		kept = append(kept, c)
	}
	return kept
}

// buildMessage composes one per-artifact message for route.
func buildMessage(route space.NotificationRoute, c classifiedArtifact) Message {
	desc, truncated := boundAndRedact(c.fa.Body)

	actions := make([]agentprompt.Action, 0, len(c.fa.Actions))
	for _, a := range c.fa.Actions {
		actions = append(actions, agentprompt.Action{Transition: a.Transition, By: a.By})
	}
	// self is the participant THIS route serves. A route with no `for`
	// serves every participant generically, and there is then no single
	// system to compose a prompt for — agentprompt.Of already returns nil
	// when self matches no action's By set, which is exactly the honest
	// answer here (never a fabricated multi-actor prompt).
	self := route.For
	outgoing := self != "" && c.fa.From == self
	prompt := agentprompt.Of(actions, c.fa.Kind, self, outgoing)

	return Message{
		Route: routeFacts(route),
		Class: c.class,
		Artifact: &Artifact{
			ID: c.fa.ID, Type: c.fa.Kind, Title: c.fa.Title, Space: c.fa.Space, Thread: c.fa.Thread,
		},
		Parties:     &Parties{From: c.fa.From, To: c.fa.To, For: route.For},
		Turn:        &Turn{Owners: c.fa.Verdict.Owners, Expected: c.fa.Verdict.Expected, Why: c.fa.Verdict.Why, HumanGate: c.fa.Verdict.HumanGate},
		Urgency:     buildUrgency(c.fa),
		Description: desc,
		Prompt:      prompt,
		Links:       &Links{Artifact: c.fa.RelPath},
		Truncated:   truncated,
	}
}

func buildUrgency(fa cache.NotifyArtifact) *Urgency {
	if fa.Priority == "" && fa.NeededBy == "" {
		return nil
	}
	return &Urgency{Priority: fa.Priority, Deadline: fa.NeededBy, Overdue: fa.Overdue}
}

func routeFacts(r space.NotificationRoute) Route {
	locale := r.Locale
	if locale == "" {
		locale = "en"
	}
	renderer := r.Renderer
	if renderer == "" {
		renderer = "html"
	}
	secret := r.Secret
	if secret == "" {
		secret = DeclaredSecret
	}
	return Route{Chat: r.Chat, Topic: r.Topic, Locale: locale, Renderer: renderer, Secret: secret, For: r.For}
}

// buildDigest composes step 5's coalesced message: count plus a bounded
// (id, type, title) list, no per-artifact facts and no prompt.
func buildDigest(route space.NotificationRoute, kept []classifiedArtifact) Message {
	items := make([]DigestItem, 0, len(kept))
	for _, c := range kept {
		items = append(items, DigestItem{ID: c.fa.ID, Type: c.fa.Kind, Title: c.fa.Title})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return Message{
		Route:  routeFacts(route),
		Class:  ClassDigest,
		Digest: &Digest{Count: len(items), Items: items},
	}
}
