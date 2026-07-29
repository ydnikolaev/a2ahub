package notification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type fakeSource struct {
	level cache.StatuslineResult
	items []cache.Item
}

func (f fakeSource) NotificationSnapshot(context.Context) (cache.NotificationSnapshot, error) {
	return cache.NotificationSnapshot{
		Level: f.level, Items: append([]cache.Item(nil), f.items...),
	}, nil
}

type fakeProbe struct {
	available []Channel
	installed []Channel
}

type fakeComponents struct {
	fakeProbe
	fail    map[Channel]bool
	configs [][]Project
}

func (f *fakeComponents) Install(_ context.Context, channel Channel, _ ComponentOptions) (ChannelResult, error) {
	if f.fail[channel] {
		return ChannelResult{Channel: channel, Status: "failed"}, errors.New("injected install failure")
	}
	return ChannelResult{Channel: channel, Status: "installed"}, nil
}

func (f *fakeComponents) Test(_ context.Context, channel Channel, _ Project) (ChannelResult, error) {
	return ChannelResult{Channel: channel, Status: "delivered"}, nil
}

func (f *fakeComponents) Uninstall(_ context.Context, channel Channel, _ ComponentOptions, _ bool) (ChannelResult, error) {
	return ChannelResult{Channel: channel, Status: "uninstalled"}, nil
}

func (f *fakeComponents) Configure(_ context.Context, projects []Project) error {
	snapshot := make([]Project, len(projects))
	copy(snapshot, projects)
	f.configs = append(f.configs, snapshot)
	return nil
}

func (f fakeProbe) Available(context.Context) ([]Channel, error) {
	return append([]Channel(nil), f.available...), nil
}

func (f fakeProbe) Installed(context.Context) ([]Channel, error) {
	return append([]Channel(nil), f.installed...), nil
}

func TestProjectSanitizesAndQualifiesIdentity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	project := Project{ID: "01JPROJECT", Root: "/project"}
	source := fakeSource{
		level: cache.StatuslineResult{Line: "a2a: 2 new", New: 2, Severity: 10},
		items: []cache.Item{
			{Space: "one", ID: "XW-same", State: "submitted", Title: "hello\n<script>",
				Priority: "p1", Reasons: []string{"p1-or-blocking-open"}, LatestEventID: "01JEVENT", LatestEventAt: now},
			{Space: "two", ID: "XW-same", State: "submitted", Title: "other",
				Reasons: []string{"addressed-no-ack"}, LatestEventID: "01JEVENT", LatestEventAt: now},
		},
	}
	got, err := BuildProjection(context.Background(), project, source, now)
	if err != nil {
		t.Fatalf("BuildProjection: %v", err)
	}
	if len(got.Entries) != 2 || got.Entries[0].EntryID == got.Entries[1].EntryID {
		t.Fatalf("entries = %#v, want two distinct space-qualified identities", got.Entries)
	}
	if got.Level.RouteToken == "" {
		t.Fatal("non-quiet aggregate level has no trusted route token")
	}
	for _, entry := range got.Entries {
		if strings.ContainsAny(entry.Title, "\r\n\t") {
			t.Fatalf("title was not control-sanitized: %q", entry.Title)
		}
		if !strings.HasPrefix(entry.RouteToken, "rt_") {
			t.Fatalf("route token = %q", entry.RouteToken)
		}
	}
}

func TestProjectionCoalescesSameSpaceBurstDeterministically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	project := Project{ID: "01JPROJECT", Root: "/project"}
	items := []cache.Item{
		{Space: "one", ID: "XW-one", State: "submitted", Title: "ordinary",
			Reasons: []string{"addressed-no-ack"}, LatestEventID: "01JEVENT1", LatestEventAt: now},
		{Space: "one", ID: "XW-two", State: "submitted", Title: "urgent",
			Priority: "p1", Reasons: []string{"p1-or-blocking-open"}, LatestEventID: "01JEVENT2", LatestEventAt: now.Add(time.Minute)},
	}
	build := func(items []cache.Item) Projection {
		t.Helper()
		got, err := BuildProjection(context.Background(), project, fakeSource{
			level: cache.StatuslineResult{Line: "a2a: 2 new", New: 2, Severity: 20},
			items: items,
		}, now)
		if err != nil {
			t.Fatalf("BuildProjection: %v", err)
		}
		return got
	}
	first := build(items)
	reversed := []cache.Item{items[1], items[0]}
	second := build(reversed)
	if len(first.Entries) != 1 {
		t.Fatalf("entries = %+v, want one coalesced burst", first.Entries)
	}
	entry := first.Entries[0]
	if entry.Kind != "aggregate" || entry.Severity != "urgent" || entry.CoalesceKey != "one:burst" {
		t.Fatalf("coalesced entry = %+v", entry)
	}
	if second.Entries[0].EntryID != entry.EntryID || second.Entries[0].RouteToken != entry.RouteToken {
		t.Fatalf("coalescing is order-dependent: first=%+v second=%+v", entry, second.Entries[0])
	}
}

func TestProjectionKeepsDifferentSpacesSeparate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	got, err := BuildProjection(context.Background(), Project{ID: "p"}, fakeSource{
		items: []cache.Item{
			{Space: "one", ID: "XW-one", State: "submitted", Reasons: []string{"addressed-no-ack"}},
			{Space: "two", ID: "XW-two", State: "submitted", Reasons: []string{"addressed-no-ack"}},
		},
	}, now)
	if err != nil {
		t.Fatalf("BuildProjection: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %+v, want one transition per space", got.Entries)
	}
}

func TestRegistryOfferPrecedenceAndProjectIsolation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(filepath.Join(t.TempDir(), "config.yaml"), func() time.Time { return now })
	ids := []string{"01JA", "01JB"}
	registry.Entropy = func() (string, error) {
		id := ids[0]
		ids = ids[1:]
		return id, nil
	}
	aRoot, bRoot := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	a, _, err := registry.Register(aRoot)
	if err != nil {
		t.Fatalf("register a: %v", err)
	}
	b, _, err := registry.Register(bRoot)
	if err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := registry.Preference(aRoot, 0, true, false, false); err != nil {
		t.Fatalf("never a: %v", err)
	}
	aOffer, err := registry.Offer(a, []Channel{ChannelMacOS})
	if err != nil {
		t.Fatalf("offer a: %v", err)
	}
	bOffer, err := registry.Offer(b, []Channel{ChannelMacOS})
	if err != nil {
		t.Fatalf("offer b: %v", err)
	}
	if aOffer.State != "never" || bOffer.State != "ask" {
		t.Fatalf("offers a=%+v b=%+v, want project-isolated never/ask", aOffer, bOffer)
	}
	if err := registry.Preference("", 0, true, false, true); err != nil {
		t.Fatalf("global never: %v", err)
	}
	bOffer, _ = registry.Offer(b, []Channel{ChannelMacOS})
	if bOffer.State != "never" || bOffer.Scope != "global" {
		t.Fatalf("global offer = %+v", bOffer)
	}
	enrolled, err := registry.Enroll(bRoot, []Channel{ChannelVSCode})
	if err != nil {
		t.Fatalf("enroll b: %v", err)
	}
	bOffer, _ = registry.Offer(enrolled, []Channel{ChannelMacOS, ChannelVSCode})
	if bOffer.State != "enabled" {
		t.Fatalf("enrolled offer = %+v", bOffer)
	}
}

func TestInstallEnrollsOnlySuccessfulChannels(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(filepath.Join(t.TempDir(), "config.yaml"), func() time.Time { return now })
	registry.Entropy = func() (string, error) { return "01JPROJECT", nil }
	components := &fakeComponents{
		fakeProbe: fakeProbe{available: []Channel{ChannelMacOS, ChannelVSCode}},
		fail:      map[Channel]bool{ChannelMacOS: true},
	}
	controller := &Controller{
		Service:    &Service{Registry: registry},
		Components: components,
	}
	root := t.TempDir()
	result, err := controller.Install(context.Background(), InstallRequest{
		Root: root, Channels: []Channel{ChannelMacOS, ChannelVSCode},
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Failed {
		t.Fatal("partial install did not report failure")
	}
	project, err := registry.FindByRoot(root)
	if err != nil {
		t.Fatalf("FindByRoot: %v", err)
	}
	if !reflect.DeepEqual(project.Channels, []Channel{ChannelVSCode}) {
		t.Fatalf("enrolled channels = %v, want only successful vscode", project.Channels)
	}
	if len(components.configs) != 2 ||
		!reflect.DeepEqual(components.configs[0][0].Channels, []Channel{ChannelMacOS, ChannelVSCode}) ||
		!reflect.DeepEqual(components.configs[1][0].Channels, []Channel{ChannelVSCode}) {
		t.Fatalf("adapter configurations = %+v, want prospective then committed truth", components.configs)
	}
}

func TestDeliveryBaselineLeaseExpiryIndependentConsumersAndIdempotentAck(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := NewDeliveryStore(t.TempDir(), func() time.Time { return now })
	nonce := 0
	store.Mint = func() (string, error) {
		nonce++
		if nonce == 1 {
			return "01J00000000000000000000001", nil
		}
		return "01J00000000000000000000002", nil
	}
	base := []Entry{{EntryID: "base", ProjectID: "p"}}
	for _, consumer := range []string{"mac-install", "code-profile"} {
		got, err := store.Claim(context.Background(), ChannelMacOS, consumer, "p", base, 10)
		if err != nil {
			t.Fatalf("baseline %s: %v", consumer, err)
		}
		if got.Lease != nil || len(got.Entries) != 0 {
			t.Fatalf("baseline %s replayed history: %+v", consumer, got)
		}
	}

	current := make([]Entry, len(base), len(base)+1)
	copy(current, base)
	current = append(current, Entry{EntryID: "new", ProjectID: "p"})
	claim, err := store.Claim(context.Background(), ChannelMacOS, "mac-install", "p", current, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Lease == nil || len(claim.Entries) != 1 || claim.Entries[0].EntryID != "new" {
		t.Fatalf("claim = %+v", claim)
	}
	blocked, err := store.Claim(context.Background(), ChannelMacOS, "mac-install", "p", current, 10)
	if err != nil {
		t.Fatalf("concurrent claim: %v", err)
	}
	if blocked.Lease != nil || len(blocked.Entries) != 0 {
		t.Fatalf("second window stole lease: %+v", blocked)
	}
	ack, err := store.Ack(context.Background(), claim.Lease.Token, []string{"new"})
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if !reflect.DeepEqual(ack.Acked, []string{"new"}) {
		t.Fatalf("ack = %+v", ack)
	}
	again, err := store.Ack(context.Background(), claim.Lease.Token, []string{"new"})
	if err != nil {
		t.Fatalf("idempotent ack: %v", err)
	}
	if !reflect.DeepEqual(again.AlreadyAcked, []string{"new"}) {
		t.Fatalf("second ack = %+v", again)
	}

	// The other consumer/channel's baseline and cursor are independent.
	vscode, err := store.Claim(context.Background(), ChannelVSCode, "code-profile", "p", current, 10)
	if err != nil {
		t.Fatalf("vscode baseline: %v", err)
	}
	if vscode.Lease != nil {
		t.Fatalf("new channel should baseline independently: %+v", vscode)
	}
}

func TestDeliveryRedeliversAfterLevelClearsAndReturns(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := NewDeliveryStore(t.TempDir(), func() time.Time { return now })
	nonce := 0
	store.Mint = func() (string, error) {
		nonce++
		return fmt.Sprintf("01J00000000000000000000%03d", nonce), nil
	}
	entry := Entry{EntryID: "stale-edge", ProjectID: "p"}
	if _, err := store.Claim(context.Background(), ChannelMacOS, "consumer", "p", nil, 10); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	first, err := store.Claim(context.Background(), ChannelMacOS, "consumer", "p", []Entry{entry}, 10)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := store.Ack(context.Background(), first.Lease.Token, []string{entry.EntryID}); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	if _, err := store.Claim(context.Background(), ChannelMacOS, "consumer", "p", nil, 10); err != nil {
		t.Fatalf("clear level: %v", err)
	}
	second, err := store.Claim(context.Background(), ChannelMacOS, "consumer", "p", []Entry{entry}, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second.Lease == nil || len(second.Entries) != 1 || second.Entries[0].EntryID != entry.EntryID {
		t.Fatalf("second claim = %+v, want redelivery after clear", second)
	}
}

func TestFileLockReapsCrashedOwnerAfterStaleAge(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	lock := path + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	old := time.Now().Add(-staleLockAge - time.Second)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatalf("age lock: %v", err)
	}
	called := false
	if err := withFileLock(context.Background(), path, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("withFileLock: %v", err)
	}
	if !called {
		t.Fatal("stale lock was not recovered")
	}
}

func TestRouteLedgerConcurrentWritersPreserveBothRoutes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "routes.json")
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	routes := []Route{
		{Token: "rt_00000000000000000000000000000001", ProjectID: "p1", CreatedAt: now},
		{Token: "rt_00000000000000000000000000000002", ProjectID: "p2", CreatedAt: now},
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(routes))
	for _, route := range routes {
		wg.Add(1)
		go func(route Route) {
			defer wg.Done()
			errs <- saveRoutes(context.Background(), path, []Route{route})
		}(route)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("saveRoutes: %v", err)
		}
	}
	for _, want := range routes {
		got, err := resolveRoute(path, want.Token)
		if err != nil || got.ProjectID != want.ProjectID {
			t.Fatalf("resolveRoute(%s) = %+v, %v", want.Token, got, err)
		}
	}
}

func TestServiceRouteLedgerResolvesOnlyCoreMintedTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	machine := filepath.Join(t.TempDir(), "config.yaml")
	registry := NewRegistry(machine, func() time.Time { return now })
	registry.Entropy = func() (string, error) { return "01JPROJECT", nil }
	if _, err := registry.Enroll(root, []Channel{ChannelMacOS}); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	delivery := NewDeliveryStore(filepath.Join(t.TempDir(), "cache"), func() time.Time { return now })
	service := &Service{
		Registry: registry, Delivery: delivery,
		Sources: func(Project) (ProjectSource, error) {
			return fakeSource{level: cache.StatuslineResult{Line: "a2a: 1 new", New: 1, Severity: 10}}, nil
		},
		Probe: fakeProbe{available: []Channel{ChannelMacOS}, installed: []Channel{ChannelMacOS}},
	}
	status, err := service.Status(context.Background(), root, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	route, project, err := service.ResolveRoute(status.Level.RouteToken)
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		t.Fatalf("canonicalRoot: %v", err)
	}
	if route.Kind != "aggregate" || project.Root != canonical {
		t.Fatalf("route=%+v project=%+v", route, project)
	}
	if _, _, err := service.ResolveRoute("rt_00000000000000000000000000000000"); err == nil {
		t.Fatal("forged but well-shaped route unexpectedly resolved")
	}
	if _, err := os.Stat(filepath.Join(delivery.Root, "routes.json")); err != nil {
		t.Fatalf("route ledger not persisted: %v", err)
	}
}

func TestStatusDoesNotRegisterOrRouteAnUnenrolledProject(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	machine := filepath.Join(t.TempDir(), "config.yaml")
	root := t.TempDir()
	service := &Service{
		Registry: NewRegistry(machine, func() time.Time { return now }),
		Delivery: NewDeliveryStore(filepath.Join(t.TempDir(), "cache"), func() time.Time { return now }),
		Sources: func(Project) (ProjectSource, error) {
			return fakeSource{level: cache.StatuslineResult{Line: "a2a: 1 new", New: 1, Severity: 10}}, nil
		},
		Probe: fakeProbe{available: []Channel{ChannelVSCode}},
	}

	status, err := service.Status(context.Background(), root, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Project != nil || status.Level.RouteToken != "" || !status.Level.Quiet {
		t.Fatalf("unenrolled status = %+v, want nil project and no active route", status)
	}
	if status.Offer.State != "ask" {
		t.Fatalf("offer = %+v, want ask", status.Offer)
	}
	if _, err := os.Stat(machine); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("passive status mutated registry: %v", err)
	}
}

func TestRegistryManagedComponentOwnershipIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	registry := NewRegistry(path, time.Now)

	if err := registry.RememberComponent(ChannelVSCode, "Work", "/opt/code", "1.2.3"); err != nil {
		t.Fatalf("RememberComponent: %v", err)
	}
	if err := registry.RememberComponent(ChannelVSCode, "Work", "/opt/code", "1.2.4"); err != nil {
		t.Fatalf("RememberComponent repeat: %v", err)
	}
	cfg, err := space.LoadMachineConfig(path)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if len(cfg.Notifications.Components) != 1 || cfg.Notifications.Components[0].Version != "1.2.4" {
		t.Fatalf("components = %+v, want one updated ownership marker", cfg.Notifications.Components)
	}
	if err := registry.ForgetComponent(ChannelVSCode, "Work", "/opt/code"); err != nil {
		t.Fatalf("ForgetComponent: %v", err)
	}
	cfg, err = space.LoadMachineConfig(path)
	if err != nil {
		t.Fatalf("LoadMachineConfig after forget: %v", err)
	}
	if len(cfg.Notifications.Components) != 0 {
		t.Fatalf("components after forget = %+v", cfg.Notifications.Components)
	}
}

func TestExplicitPurgeRemovesSelectedMachineAndDeliveryState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	registry := NewRegistry(path, time.Now)
	registry.Entropy = func() (string, error) { return "01JPURGE", nil }
	root := t.TempDir()
	if _, err := registry.Enroll(root, []Channel{ChannelMacOS, ChannelVSCode}); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if err := registry.RememberComponent(ChannelMacOS, "", "", "1.2.3"); err != nil {
		t.Fatalf("RememberComponent macOS: %v", err)
	}
	if err := registry.RememberComponent(ChannelVSCode, "Work", "/opt/code", "1.2.3"); err != nil {
		t.Fatalf("RememberComponent VS Code: %v", err)
	}
	delivery := NewDeliveryStore(filepath.Join(t.TempDir(), "notifications"), time.Now)
	macDelivery := filepath.Join(delivery.Root, "delivery", "macos", "state.json")
	codeDelivery := filepath.Join(delivery.Root, "delivery", "vscode", "state.json")
	for _, statePath := range []string{macDelivery, codeDelivery} {
		if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := delivery.PurgeChannels([]Channel{ChannelMacOS}); err != nil {
		t.Fatalf("PurgeChannels delivery: %v", err)
	}
	if err := registry.PurgeChannels([]Channel{ChannelMacOS}); err != nil {
		t.Fatalf("PurgeChannels registry: %v", err)
	}

	if _, err := os.Stat(macDelivery); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("macOS delivery state still present: %v", err)
	}
	if _, err := os.Stat(codeDelivery); err != nil {
		t.Fatalf("VS Code delivery state removed: %v", err)
	}
	cfg, err := space.LoadMachineConfig(path)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if got := cfg.Notifications.Projects[0].Channels; !reflect.DeepEqual(got, []string{"vscode"}) {
		t.Fatalf("project channels = %v, want vscode only", got)
	}
	if len(cfg.Notifications.Components) != 1 || cfg.Notifications.Components[0].Channel != "vscode" {
		t.Fatalf("components after purge = %+v", cfg.Notifications.Components)
	}
}
