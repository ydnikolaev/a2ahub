package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// Projection is the canonical level, transition-entry, and route snapshot.
type Projection struct {
	Level   Level
	Entries []Entry
	Routes  []Route
}

// BuildProjection derives sanitized notification state from cursor-neutral cache truth.
func BuildProjection(ctx context.Context, project Project, source ProjectSource, now time.Time) (Projection, error) {
	snapshot, err := source.NotificationSnapshot(ctx)
	if err != nil {
		return Projection{}, fmt.Errorf("notification projection: snapshot: %w", err)
	}
	items := snapshot.Items
	cache.SortNotificationReasons(items)
	statusline := snapshot.Level

	aggregateToken := routeToken(project.ID, "", "", "aggregate")
	level := Level{
		Line: statusline.Line, New: statusline.New, Urgent: statusline.Urgent,
		Stale: statusline.Stale, Update: statusline.Update,
		Severity: levelSeverity(statusline.Severity), Quiet: statusline.Quiet,
	}
	var routes []Route
	if statusline.Line != "" || len(items) > 0 {
		level.Quiet = false
		level.RouteToken = aggregateToken
		routes = append(routes, Route{
			Token: aggregateToken, ProjectID: project.ID, Kind: "aggregate", CreatedAt: now.UTC(),
		})
	}

	entries := make([]Entry, 0, len(items)+1)
	for _, item := range items {
		kind := signalKind(item)
		severity := "ordinary"
		if cache.SeverityOf([]cache.Item{item}) == cache.SeverityUrgent {
			severity = "urgent"
		}
		occurredAt := item.LatestEventAt.UTC()
		if occurredAt.IsZero() {
			occurredAt = now.UTC()
		}
		fingerprint := strings.Join([]string{
			project.ID, item.Space, item.ID, item.State, item.LatestEventID,
			strings.Join(item.Reasons, ","),
		}, "\x00")
		entryID := stableID("ne_", fingerprint)
		token := routeToken(project.ID, item.Space, item.ID, entryID)
		entry := Entry{
			SchemaVersion: SchemaVersion, EntryID: entryID,
			ProjectID: project.ID, SpaceID: item.Space, ArtifactID: item.ID,
			EventID: item.LatestEventID, Kind: kind, Severity: severity,
			Title:      sanitize(item.Title, 120),
			Summary:    sanitize(fmt.Sprintf("%s · %s · %s", item.Space, item.ID, strings.Join(item.Reasons, ", ")), 240),
			OccurredAt: occurredAt, SourceRevision: item.LatestEventID,
			CoalesceKey: item.Space + ":" + kind, RouteToken: token,
		}
		entries = append(entries, entry)
		routes = append(routes, Route{
			Token: token, ProjectID: project.ID, SpaceID: item.Space,
			ArtifactID: item.ID, Kind: "item", CreatedAt: now.UTC(),
		})
	}
	entries, routes = coalesceProjectEntries(project, entries, routes, now)

	if statusline.Update != "" {
		required := snapshot.Update.Required
		kind, severity := "update_available", "ordinary"
		if required {
			kind, severity = "update_required", "urgent"
			// Statusline deliberately retains exit 0 for update-only state.
			// Native/editor presentation has its own emphasis contract.
			level.Severity = "urgent"
		} else if level.Severity == "quiet" {
			level.Severity = "ordinary"
		}
		entryID := stableID("ne_", project.ID+"\x00"+kind+"\x00"+statusline.Update)
		token := routeToken(project.ID, "", "", entryID)
		entries = append(entries, Entry{
			SchemaVersion: SchemaVersion, EntryID: entryID, ProjectID: project.ID,
			Kind: kind, Severity: severity, Title: sanitize(statusline.Update, 120),
			Summary:    "a2a update information is available in the local dashboard",
			OccurredAt: now.UTC(), SourceRevision: statusline.Update,
			CoalesceKey: "a2a:update", RouteToken: token,
		})
		routes = append(routes, Route{
			Token: token, ProjectID: project.ID, Kind: "update", CreatedAt: now.UTC(),
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].EntryID < entries[j].EntryID })
	return Projection{Level: level, Entries: entries, Routes: routes}, nil
}

// coalesceProjectEntries turns a simultaneous per-space burst into one stable
// transition. Delivery state remains consumer-specific, while projection owns
// the deterministic burst identity and highest-severity presentation.
func coalesceProjectEntries(project Project, entries []Entry, routes []Route, now time.Time) ([]Entry, []Route) {
	groups := make(map[string][]Entry)
	for _, entry := range entries {
		groups[entry.SpaceID] = append(groups[entry.SpaceID], entry)
	}
	routeByToken := make(map[string]Route, len(routes))
	for _, route := range routes {
		routeByToken[route.Token] = route
	}
	outEntries := make([]Entry, 0, len(entries))
	keptRoutes := make([]Route, 0, len(routes))
	for _, route := range routes {
		if route.Kind == "aggregate" {
			keptRoutes = append(keptRoutes, route)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, spaceID := range keys {
		group := groups[spaceID]
		sort.Slice(group, func(i, j int) bool { return group[i].EntryID < group[j].EntryID })
		if len(group) == 1 {
			outEntries = append(outEntries, group[0])
			if route, ok := routeByToken[group[0].RouteToken]; ok {
				keptRoutes = append(keptRoutes, route)
			}
			continue
		}
		memberIDs := make([]string, 0, len(group))
		severity := "ordinary"
		occurredAt := group[0].OccurredAt
		for _, entry := range group {
			memberIDs = append(memberIDs, entry.EntryID)
			if entry.Severity == "urgent" {
				severity = "urgent"
			}
			if entry.OccurredAt.After(occurredAt) {
				occurredAt = entry.OccurredAt
			}
		}
		fingerprint := strings.Join(append([]string{project.ID, spaceID}, memberIDs...), "\x00")
		entryID := stableID("ne_", fingerprint)
		token := routeToken(project.ID, spaceID, "", entryID)
		outEntries = append(outEntries, Entry{
			SchemaVersion: SchemaVersion,
			EntryID:       entryID,
			ProjectID:     project.ID,
			SpaceID:       spaceID,
			Kind:          "aggregate",
			Severity:      severity,
			Title:         sanitize(fmt.Sprintf("%d A2A items need attention", len(group)), 120),
			Summary:       sanitize(fmt.Sprintf("%d actionable items in %s", len(group), spaceID), 240),
			OccurredAt:    occurredAt,
			SourceRevision: stableID(
				"rev_", strings.Join(memberIDs, "\x00"),
			),
			CoalesceKey: spaceID + ":burst",
			RouteToken:  token,
		})
		keptRoutes = append(keptRoutes, Route{
			Token: token, ProjectID: project.ID, SpaceID: spaceID,
			Kind: "aggregate", CreatedAt: now.UTC(),
		})
	}
	return outEntries, keptRoutes
}

func levelSeverity(value int) string {
	switch {
	case value >= int(cache.SeverityUrgent):
		return "urgent"
	case value >= int(cache.SeverityItemsPending):
		return "ordinary"
	default:
		return "quiet"
	}
}

func signalKind(item cache.Item) string {
	for _, reason := range item.Reasons {
		switch reason {
		case "gate-pending-on-me":
			return "gate_pending"
		case "declined":
			return "declined"
		case "disputed", "disputed-toward-me":
			return "disputed"
		case "stale-sla":
			return "stale"
		case "needed-by-passed":
			return "needed_by_passed"
		case "responded-awaiting-verify-close":
			return "response_pending"
		}
	}
	if item.Blocking || item.Priority == "p1" {
		return "blocking"
	}
	return "actionable"
}

func stableID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + hex.EncodeToString(sum[:16])
}

func routeToken(projectID, spaceID, artifactID, identity string) string {
	return stableID("rt_", strings.Join([]string{projectID, spaceID, artifactID, identity}, "\x00"))
}

func sanitize(value string, maxRunes int) string {
	var b strings.Builder
	spacePending := false
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			spacePending = b.Len() > 0
			continue
		}
		if spacePending {
			b.WriteByte(' ')
			spacePending = false
		}
		b.WriteRune(r)
	}
	runes := []rune(strings.TrimSpace(b.String()))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes <= 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}
