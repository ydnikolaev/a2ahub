package html

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// Assemble builds the dashboard Data from a composed Store, as of now. self is
// the ego system (the --system override); empty falls back to the Store's own
// configured system. It reads only (Store already composed the mirrors);
// consumes.yaml is walked per space mirror to derive contract-dependency edges.
// No network.
func Assemble(ctx context.Context, store *cache.Store, self string, now time.Time) (Data, error) {
	if self == "" {
		self = store.OwnSystem()
	}
	mirrors := store.SpaceMirrors()
	d := Data{GeneratedAt: now, Self: self, Nodes: []Node{}, ContractEdges: []ContractEdge{},
		ExchangeEdges: []ExchangeEdge{}, Inbox: []Item{}, Outbox: []Item{}, Contracts: []Contract{},
		Spaces: []SpaceHealth{}, Flags: []Flag{}}

	un := store.UpdateNotice()
	d.Tooling = Tooling{Current: un.Current, Latest: un.Latest, UpdateAvailable: un.UpdateAvailable,
		Required: un.Required, Floor: un.Floor, FloorSpace: un.FloorSpace}

	// Nodes (deduped across spaces) + per-space health.
	nodeIdx := map[string]*Node{}
	for _, m := range mirrors {
		readable := m.Manifest.Space != "" || len(m.Manifest.Participants) > 0
		d.Spaces = append(d.Spaces, SpaceHealth{
			ID: m.SpaceID, RepoURL: m.RepoURL,
			ParticipantCount: len(m.Manifest.Participants), Readable: readable,
			// SyncAge/Stale: not yet exposed by internal/cache (private
			// spaceSyncStale/mirrorSyncAge) — populated in a follow-up.
		})
		for _, p := range m.Manifest.Participants {
			n := nodeIdx[p.System]
			if n == nil {
				n = &Node{System: p.System, Self: p.System == self, Org: p.Org,
					Status: p.Status, Owners: p.Owners, Spaces: []string{}}
				nodeIdx[p.System] = n
			}
			n.Spaces = append(n.Spaces, m.SpaceID)
			if n.Org == "" {
				n.Org = p.Org
			}
			if p.Status == "left" { // a "left" anywhere marks the node left
				n.Status = "left"
			}
		}
	}
	for _, n := range nodeIdx {
		d.Nodes = append(d.Nodes, *n)
	}
	sort.Slice(d.Nodes, func(i, j int) bool { return d.Nodes[i].System < d.Nodes[j].System })

	// Contracts catalog + a by-id index for drift lookups.
	cinfos, err := store.Contracts(ctx, "")
	if err != nil {
		return Data{}, fmt.Errorf("html: contracts: %w", err)
	}
	byID := map[string]cache.ContractInfo{}
	for _, c := range cinfos {
		byID[c.ID] = c
	}

	// Contract-dependency edges from every space's consumes.yaml, plus the
	// per-contract consumer lists.
	consumersOf := map[string][]string{}
	for _, m := range mirrors {
		for _, p := range m.Manifest.Participants {
			sec := strings.TrimSuffix(p.Section, "/")
			if sec == "" {
				continue
			}
			raw, rErr := os.ReadFile(filepath.Join(m.Dir, sec, "consumes.yaml"))
			if rErr != nil {
				continue // absent consumes.yaml is normal
			}
			cons, pErr := space.ParseConsumes(raw)
			if pErr != nil {
				continue // malformed → skip (degrade, never fail the view)
			}
			for _, dep := range cons.Dependencies {
				provider := providerOf(dep.Contract)
				consumersOf[dep.Contract] = append(consumersOf[dep.Contract], p.System)
				edge := ContractEdge{From: p.System, To: provider, Space: m.SpaceID,
					Contract: dep.Contract, PinnedMajor: dep.Major}
				ci, ok := byID[dep.Contract]
				switch {
				case !ok || nodeStatus(nodeIdx, provider) == "left":
					edge.Drift = "dangling"
				default:
					edge.ProviderVersion = ci.Version
					edge.State = ci.State
					edge.Drift = driftOf(ci.State, dep.Major, ci.Version)
				}
				d.ContractEdges = append(d.ContractEdges, edge)
			}
		}
	}
	sort.Slice(d.ContractEdges, func(i, j int) bool {
		return d.ContractEdges[i].Contract < d.ContractEdges[j].Contract
	})

	// Contracts catalog rows (with derived consumer lists).
	for _, c := range cinfos {
		cons := consumersOf[c.ID]
		sort.Strings(cons)
		d.Contracts = append(d.Contracts, Contract{Space: c.Space, ID: c.ID, Provider: c.Provider,
			Version: c.Version, State: c.State, Consumers: dedupSorted(cons)})
	}

	// Inbox / outbox items (open only — the Store already filters to open).
	inItems, err := store.Inbox(ctx, false)
	if err != nil {
		return Data{}, fmt.Errorf("html: inbox: %w", err)
	}
	outItems, err := store.Outbox(ctx, false)
	if err != nil {
		return Data{}, fmt.Errorf("html: outbox: %w", err)
	}
	for _, it := range inItems {
		d.Inbox = append(d.Inbox, toItem(it, now))
	}
	for _, it := range outItems {
		d.Outbox = append(d.Outbox, toItem(it, now))
	}

	// Exchange overlay: aggregate open items (inbox ∪ outbox) per from→to→space.
	d.ExchangeEdges = exchangeEdges(append(append([]cache.Item{}, inItems...), outItems...), now)

	return d, nil
}

// toItem maps a cache.Item to the model Item with derived age + severity.
func toItem(it cache.Item, now time.Time) Item {
	gate := hasReason(it.Reasons, "gate-pending-on-me")
	return Item{
		Space: it.Space, ID: it.ID, Type: it.Type, Title: it.Title, From: it.From, To: it.To,
		State: it.State, Priority: it.Priority, Blocking: it.Blocking, GatePending: gate,
		Thread: it.Thread, Age: humanizeAge(now, it.LatestEventAt), New: it.New,
		Severity: severityOf(it, gate), Reasons: it.Reasons,
	}
}

// severityOf reimplements the statusline predicate: blocking (p1/blocking/
// gate-pending) → "blocking"; a stale-but-not-urgent item → "attention"; else
// "normal".
func severityOf(it cache.Item, gate bool) string {
	if it.Blocking || it.Priority == "p1" || gate {
		return "blocking"
	}
	if it.SyncStale {
		return "attention"
	}
	return "normal"
}

// exchangeEdges aggregates open items into directed per-space edges.
func exchangeEdges(items []cache.Item, now time.Time) []ExchangeEdge {
	type key struct{ from, to, space string }
	agg := map[key]*ExchangeEdge{}
	var oldest = map[key]time.Time{}
	for _, it := range items {
		for _, to := range it.To {
			k := key{it.From, to, it.Space}
			e := agg[k]
			if e == nil {
				e = &ExchangeEdge{From: it.From, To: to, Space: it.Space}
				agg[k] = e
			}
			e.Count++
			if it.Blocking {
				e.Blocking = true
			}
			e.MaxPriority = maxPriority(e.MaxPriority, it.Priority)
			if !it.LatestEventAt.IsZero() && (oldest[k].IsZero() || it.LatestEventAt.Before(oldest[k])) {
				oldest[k] = it.LatestEventAt
			}
		}
	}
	out := make([]ExchangeEdge, 0, len(agg))
	for k, e := range agg {
		if t := oldest[k]; !t.IsZero() {
			e.MaxStale = humanizeAge(now, t)
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Space != out[j].Space {
			return out[i].Space < out[j].Space
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// providerOf extracts the provider system from a contract id (XC-<provider>-<slug>).
func providerOf(contractID string) string {
	parts := strings.SplitN(contractID, "-", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func nodeStatus(idx map[string]*Node, system string) string {
	if n := idx[system]; n != nil {
		return n.Status
	}
	return ""
}

// driftOf grades a dependency: retired/deprecated states win; else a newer
// provider major than the pinned one is "behind"; else "current".
func driftOf(state string, pinnedMajor int, providerVersion string) string {
	switch state {
	case "retired":
		return "retired"
	case "deprecated":
		return "deprecated"
	}
	if pm := majorOf(providerVersion); pm > pinnedMajor {
		return "behind"
	}
	return "current"
}

func majorOf(version string) int {
	if version == "" {
		return 0
	}
	seg := version
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		seg = seg[:i]
	}
	n := 0
	for _, r := range seg {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// maxPriority returns the more-urgent of two priority strings (p1 > p2 > p3 > "").
func maxPriority(a, b string) string {
	rank := func(p string) int {
		switch p {
		case "p1":
			return 3
		case "p2":
			return 2
		case "p3":
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// humanizeAge formats now-t as a terse human age ("just now", "3m", "5h",
// "2d", "3w"). Empty for a zero time (no events yet).
func humanizeAge(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	dur := now.Sub(t)
	if dur < 0 {
		dur = 0
	}
	switch {
	case dur < time.Minute:
		return "just now"
	case dur < time.Hour:
		return fmt.Sprintf("%dm", int(dur.Minutes()))
	case dur < 24*time.Hour:
		return fmt.Sprintf("%dh", int(dur.Hours()))
	case dur < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(dur.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(dur.Hours()/(24*7)))
	}
}
