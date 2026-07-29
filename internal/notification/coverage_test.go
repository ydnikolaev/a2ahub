package notification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

type mutableNotificationSource struct {
	snapshot cache.NotificationSnapshot
	err      error
}

func (s *mutableNotificationSource) NotificationSnapshot(context.Context) (cache.NotificationSnapshot, error) {
	if s.err != nil {
		return cache.NotificationSnapshot{}, s.err
	}
	return s.snapshot, nil
}

type coverageComponents struct {
	available    []Channel
	installed    []Channel
	health       []ComponentHealth
	availableErr error
	installedErr error
	healthErr    error
	installErr   map[Channel]error
	testErr      map[Channel]error
	uninstallErr map[Channel]error
	configureErr error
	configured   int
}

func (c *coverageComponents) Available(context.Context) ([]Channel, error) {
	return append([]Channel(nil), c.available...), c.availableErr
}

func (c *coverageComponents) Installed(context.Context) ([]Channel, error) {
	return append([]Channel(nil), c.installed...), c.installedErr
}

func (c *coverageComponents) Health(context.Context) ([]ComponentHealth, error) {
	return append([]ComponentHealth(nil), c.health...), c.healthErr
}

func (c *coverageComponents) Install(_ context.Context, channel Channel, _ ComponentOptions) (ChannelResult, error) {
	if err := c.installErr[channel]; err != nil {
		return ChannelResult{Channel: channel, Reason: "adapter"}, err
	}
	return ChannelResult{Channel: channel, Status: "installed"}, nil
}

func (c *coverageComponents) Test(_ context.Context, channel Channel, _ Project) (ChannelResult, error) {
	if err := c.testErr[channel]; err != nil {
		return ChannelResult{Channel: channel, Reason: "adapter"}, err
	}
	return ChannelResult{Channel: channel, Status: "delivered"}, nil
}

func (c *coverageComponents) Uninstall(_ context.Context, channel Channel, _ ComponentOptions, _ bool) (ChannelResult, error) {
	if err := c.uninstallErr[channel]; err != nil {
		return ChannelResult{Channel: channel, Reason: "adapter"}, err
	}
	return ChannelResult{Channel: channel, Status: "uninstalled"}, nil
}

func (c *coverageComponents) Configure(context.Context, []Project) error {
	c.configured++
	return c.configureErr
}

func newCoverageController(
	t *testing.T,
	now time.Time,
	source ProjectSource,
	components *coverageComponents,
) (*Controller, *Registry) {
	t.Helper()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config.yaml"), func() time.Time { return now })
	nextID := 0
	registry.Entropy = func() (string, error) {
		nextID++
		return fmt.Sprintf("01JCOVERAGE%02d", nextID), nil
	}
	service := &Service{
		Registry: registry,
		Delivery: NewDeliveryStore(filepath.Join(t.TempDir(), "delivery"), func() time.Time { return now }),
		Sources: func(Project) (ProjectSource, error) {
			return source, nil
		},
		Probe: components,
	}
	nonce := 0
	service.Delivery.Mint = func() (string, error) {
		nonce++
		return fmt.Sprintf("01J00000000000000000000%03d", nonce), nil
	}
	return &Controller{Service: service, Components: components}, registry
}

func TestControllerLifecycleCoversHealthDeliveryPreferencesAndPurge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	source := &mutableNotificationSource{}
	components := &coverageComponents{
		available: []Channel{ChannelVSCode, ChannelMacOS},
		installed: []Channel{ChannelMacOS},
		health: []ComponentHealth{
			{Channel: ChannelMacOS, Available: true, Installed: true, Protocol: SchemaVersion},
			{Channel: ChannelVSCode, Available: true, Installed: false, Protocol: SchemaVersion},
		},
		installErr: map[Channel]error{}, testErr: map[Channel]error{}, uninstallErr: map[Channel]error{},
	}
	controller, registry := newCoverageController(t, now, source, components)
	root := t.TempDir()

	installed, err := controller.Install(ctx, InstallRequest{Root: root, All: true, Profile: "Work", CodePath: "/opt/code"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if installed.Failed || len(installed.Results) != 2 || components.configured != 2 {
		t.Fatalf("install result=%+v configured=%d", installed, components.configured)
	}
	if got := []Channel{installed.Results[0].Channel, installed.Results[1].Channel}; !reflect.DeepEqual(got, []Channel{ChannelMacOS, ChannelVSCode}) {
		t.Fatalf("install result order = %v", got)
	}

	status, err := controller.Status(ctx, StatusRequest{Root: root, Channel: ChannelMacOS, Probe: true})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !reflect.DeepEqual(status.AvailableChannels, []Channel{ChannelMacOS}) ||
		!reflect.DeepEqual(status.InstalledChannels, []Channel{ChannelMacOS}) ||
		len(status.Components) != 1 || status.Components[0].Channel != ChannelMacOS {
		t.Fatalf("filtered detailed status = %+v", status)
	}

	tested, err := controller.Test(ctx, TestRequest{Root: root, All: true})
	if err != nil || tested.Failed || len(tested.Results) != 2 {
		t.Fatalf("Test = %+v, %v", tested, err)
	}

	project, err := registry.FindByRoot(root)
	if err != nil {
		t.Fatalf("FindByRoot: %v", err)
	}
	baseline, err := controller.Claim(ctx, ClaimRequest{
		Channel: ChannelMacOS, Consumer: "native-app", ProjectID: project.ID, Limit: 10,
	})
	if err != nil || baseline.Lease != nil {
		t.Fatalf("baseline Claim = %+v, %v", baseline, err)
	}
	source.snapshot.Items = []cache.Item{{
		Space: "alpha", ID: "XW-edge", State: "submitted", Title: "Action",
		Reasons: []string{"gate-pending-on-me"}, LatestEventID: "01JEVENT", LatestEventAt: now,
	}}
	claimed, err := controller.Claim(ctx, ClaimRequest{
		Channel: ChannelMacOS, Consumer: "native-app", ProjectID: project.ID, Limit: 10,
	})
	if err != nil || claimed.Lease == nil || len(claimed.Entries) != 1 {
		t.Fatalf("edge Claim = %+v, %v", claimed, err)
	}
	if _, err := controller.Ack(ctx, AckRequest{}); !errors.Is(err, ErrInvalidAck) {
		t.Fatalf("empty Ack error = %v", err)
	}
	acked, err := controller.Ack(ctx, AckRequest{
		LeaseToken: claimed.Lease.Token, EntryIDs: []string{claimed.Entries[0].EntryID},
	})
	if err != nil || len(acked.Acked) != 1 {
		t.Fatalf("Ack = %+v, %v", acked, err)
	}

	var opened Route
	controller.OpenRoute = func(_ context.Context, route Route, got Project) error {
		opened = route
		if got.ID != project.ID {
			return errors.New("wrong project")
		}
		return nil
	}
	openResult, err := controller.Open(ctx, OpenRequest{RouteToken: claimed.Entries[0].RouteToken})
	if err != nil || openResult.ArtifactID != "XW-edge" || opened.Token == "" {
		t.Fatalf("Open = %+v, route=%+v, err=%v", openResult, opened, err)
	}

	otherRoot := t.TempDir()
	preference, err := controller.Preference(ctx, PreferenceRequest{Root: otherRoot, RemindInDays: 7})
	if err != nil || preference.Offer.State != "snoozed" || preference.Project == nil {
		t.Fatalf("project Preference = %+v, %v", preference, err)
	}
	global, err := controller.Preference(ctx, PreferenceRequest{Never: true, Global: true})
	if err != nil || global.Offer.State != "never" || global.Offer.Scope != "global" {
		t.Fatalf("global Preference = %+v, %v", global, err)
	}
	if _, err := registry.Enroll(otherRoot, []Channel{ChannelMacOS}); err != nil {
		t.Fatalf("Enroll other: %v", err)
	}
	if _, err := controller.Uninstall(ctx, UninstallRequest{
		Root: root, Channels: []Channel{ChannelMacOS},
	}); !errors.Is(err, ErrUninstallAffectsProjects) {
		t.Fatalf("unconfirmed Uninstall error = %v", err)
	}
	uninstalled, err := controller.Uninstall(ctx, UninstallRequest{
		Root: root, Channels: []Channel{ChannelMacOS}, Yes: true, Purge: true,
	})
	if err != nil || uninstalled.Failed || len(uninstalled.Results) != 1 ||
		uninstalled.Results[0].Reason == "" {
		t.Fatalf("confirmed purge Uninstall = %+v, %v", uninstalled, err)
	}
}

func TestControllerValidationAndAdapterFailuresFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	source := &mutableNotificationSource{}
	components := &coverageComponents{
		available:    []Channel{ChannelMacOS},
		installErr:   map[Channel]error{ChannelMacOS: errors.New("install")},
		testErr:      map[Channel]error{ChannelMacOS: errors.New("test")},
		uninstallErr: map[Channel]error{ChannelMacOS: errors.New("uninstall")},
	}
	controller, registry := newCoverageController(t, now, source, components)
	root := t.TempDir()

	if _, err := controller.Install(ctx, InstallRequest{Root: root, Channels: []Channel{ChannelMacOS}, All: true}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("all+explicit error = %v", err)
	}
	if _, err := controller.Install(ctx, InstallRequest{Root: root}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("empty channel error = %v", err)
	}
	if _, err := controller.Install(ctx, InstallRequest{Root: root, Channels: []Channel{"other"}}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("unknown channel error = %v", err)
	}
	failedInstall, err := controller.Install(ctx, InstallRequest{Root: root, Channels: []Channel{ChannelMacOS, ChannelMacOS}})
	if err != nil || !failedInstall.Failed || len(failedInstall.Results) != 1 ||
		failedInstall.Results[0].Reason != "adapter: install" {
		t.Fatalf("failed Install = %+v, %v", failedInstall, err)
	}

	if _, _, err := registry.Register(root); err != nil {
		t.Fatalf("Register: %v", err)
	}
	failedTest, err := controller.Test(ctx, TestRequest{Root: root, Channels: []Channel{ChannelMacOS}})
	if err != nil || !failedTest.Failed || failedTest.Results[0].Reason != "adapter: test" {
		t.Fatalf("failed Test = %+v, %v", failedTest, err)
	}
	failedUninstall, err := controller.Uninstall(ctx, UninstallRequest{
		Root: root, Channels: []Channel{ChannelMacOS}, Yes: true,
	})
	if err != nil || !failedUninstall.Failed || failedUninstall.Results[0].Reason != "adapter: uninstall" {
		t.Fatalf("failed Uninstall = %+v, %v", failedUninstall, err)
	}
	if _, err := controller.Status(ctx, StatusRequest{Root: root, Channel: "other"}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("invalid Status channel error = %v", err)
	}
	if _, err := controller.Open(ctx, OpenRequest{RouteToken: "bad"}); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("invalid Open route error = %v", err)
	}

	controller.Components = nil
	if _, err := controller.Install(ctx, InstallRequest{}); !errors.Is(err, ErrComponentsUnavailable) {
		t.Fatalf("nil Install components error = %v", err)
	}
	if _, err := controller.Test(ctx, TestRequest{}); !errors.Is(err, ErrComponentsUnavailable) {
		t.Fatalf("nil Test components error = %v", err)
	}
	if _, err := controller.Uninstall(ctx, UninstallRequest{}); !errors.Is(err, ErrComponentsUnavailable) {
		t.Fatalf("nil Uninstall components error = %v", err)
	}
}

func TestServiceProbeAndClaimErrorsPropagate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config.yaml"), func() time.Time { return now })
	registry.Entropy = func() (string, error) { return "01JSERVICE", nil }
	project, err := registry.Enroll(root, []Channel{ChannelMacOS})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	source := &mutableNotificationSource{}
	service := &Service{
		Registry: registry,
		Delivery: NewDeliveryStore(t.TempDir(), func() time.Time { return now }),
		Sources:  func(Project) (ProjectSource, error) { return source, nil },
	}
	if status, err := service.Status(ctx, root, false); err != nil ||
		len(status.AvailableChannels) != 0 || len(status.Components) != 0 {
		t.Fatalf("nil-probe Status = %+v, %v", status, err)
	}
	if _, err := service.Claim(ctx, ChannelVSCode, "editor", project.ID, 10); !errors.Is(err, ErrChannelNotEnrolled) {
		t.Fatalf("unenrolled Claim error = %v", err)
	}

	service.Probe = &coverageComponents{availableErr: errors.New("available")}
	if _, err := service.Status(ctx, root, false); err == nil || err.Error() != "available" {
		t.Fatalf("available probe error = %v", err)
	}
	service.Probe = &coverageComponents{available: []Channel{ChannelMacOS}, installedErr: errors.New("installed")}
	if _, err := service.Status(ctx, root, false); err == nil || err.Error() != "installed" {
		t.Fatalf("installed probe error = %v", err)
	}
	service.Probe = &coverageComponents{healthErr: errors.New("health")}
	if _, err := service.Status(ctx, root, true); err == nil || err.Error() != "health" {
		t.Fatalf("health probe error = %v", err)
	}
	source.err = errors.New("snapshot")
	service.Probe = nil
	if _, err := service.Status(ctx, root, false); err == nil {
		t.Fatal("snapshot failure was swallowed")
	}
}

func TestProjectionCoversSignalKindsAndUpdateEscalation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	items := []cache.Item{
		{Space: "a", ID: "1", Reasons: []string{"gate-pending-on-me"}},
		{Space: "b", ID: "2", Reasons: []string{"declined"}},
		{Space: "c", ID: "3", Reasons: []string{"disputed-toward-me"}},
		{Space: "d", ID: "4", Reasons: []string{"stale-sla"}},
		{Space: "e", ID: "5", Reasons: []string{"needed-by-passed"}},
		{Space: "f", ID: "6", Reasons: []string{"responded-awaiting-verify-close"}},
		{Space: "g", ID: "7", Blocking: true},
		{Space: "h", ID: "8"},
	}
	source := &mutableNotificationSource{snapshot: cache.NotificationSnapshot{
		Level:  cache.StatuslineResult{Update: "update v1.0.0→v1.1.0"},
		Update: cache.UpdateNotice{Required: true},
		Items:  items,
	}}
	projection, err := BuildProjection(context.Background(), Project{ID: "p"}, source, now)
	if err != nil {
		t.Fatalf("BuildProjection: %v", err)
	}
	kinds := make([]string, 0, len(projection.Entries))
	for _, entry := range projection.Entries {
		kinds = append(kinds, entry.Kind)
		if entry.OccurredAt.IsZero() {
			t.Fatalf("zero occurred_at: %+v", entry)
		}
	}
	sort.Strings(kinds)
	want := []string{
		"actionable", "blocking", "declined", "disputed", "gate_pending",
		"needed_by_passed", "response_pending", "stale", "update_required",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(kinds, want) || projection.Level.Severity != "urgent" {
		t.Fatalf("kinds=%v severity=%q", kinds, projection.Level.Severity)
	}

	source.snapshot = cache.NotificationSnapshot{
		Level:  cache.StatuslineResult{Update: "update v1.0.0→v1.1.0"},
		Update: cache.UpdateNotice{Required: false},
	}
	available, err := BuildProjection(context.Background(), Project{ID: "p"}, source, now)
	if err != nil || len(available.Entries) != 1 ||
		available.Entries[0].Kind != "update_available" ||
		available.Level.Severity != "ordinary" {
		t.Fatalf("available update projection = %+v, %v", available, err)
	}
}

func TestDeliveryRejectsInvalidStateAndSupportsPartialLeaseAck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	store := NewDeliveryStore(t.TempDir(), func() time.Time { return now })
	store.Mint = func() (string, error) { return "01J00000000000000000000001", nil }

	for _, tc := range []struct {
		channel  Channel
		consumer string
		limit    int
		want     error
	}{
		{ChannelMacOS, "ok", 0, ErrInvalidLimit},
		{ChannelMacOS, "ok", 51, ErrInvalidLimit},
		{"other", "ok", 1, ErrInvalidConsumer},
		{ChannelMacOS, "../bad", 1, ErrInvalidConsumer},
	} {
		if _, err := store.Claim(ctx, tc.channel, tc.consumer, "p", nil, tc.limit); !errors.Is(err, tc.want) {
			t.Fatalf("Claim(%q,%q,%d) error=%v want=%v", tc.channel, tc.consumer, tc.limit, err, tc.want)
		}
	}

	if _, err := store.Claim(ctx, ChannelMacOS, "consumer", "p", nil, 1); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	current := []Entry{
		{EntryID: "a", ProjectID: "p"},
		{EntryID: "b", ProjectID: "p"},
	}
	claim, err := store.Claim(ctx, ChannelMacOS, "consumer", "p", current, 2)
	if err != nil || claim.Lease == nil || len(claim.Entries) != 2 {
		t.Fatalf("Claim = %+v, %v", claim, err)
	}
	if _, err := store.Ack(ctx, claim.Lease.Token, []string{"outside"}); !errors.Is(err, ErrEntryNotLeased) {
		t.Fatalf("outside Ack error = %v", err)
	}
	first, err := store.Ack(ctx, claim.Lease.Token, []string{"a"})
	if err != nil || !reflect.DeepEqual(first.Acked, []string{"a"}) {
		t.Fatalf("partial Ack = %+v, %v", first, err)
	}
	again, err := store.Ack(ctx, claim.Lease.Token, []string{"a"})
	if err != nil || !reflect.DeepEqual(again.AlreadyAcked, []string{"a"}) {
		t.Fatalf("live idempotent Ack = %+v, %v", again, err)
	}
	if _, err := store.Ack(ctx, claim.Lease.Token, []string{"b"}); err != nil {
		t.Fatalf("complete Ack: %v", err)
	}
	if _, err := store.Ack(ctx, "bad", []string{"a"}); !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("malformed token error = %v", err)
	}

	path, err := store.statePath(ChannelMacOS, "mismatch")
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	if err := writeJSON(path, deliveryState{
		Schema: SchemaVersion, Channel: ChannelVSCode, Consumer: "mismatch",
		Projects: map[string]*projectState{},
	}); err != nil {
		t.Fatalf("write mismatched state: %v", err)
	}
	if _, err := store.Claim(ctx, ChannelMacOS, "mismatch", "p", nil, 1); !errors.Is(err, ErrDeliveryStateMismatch) {
		t.Fatalf("mismatched state error = %v", err)
	}
}

func TestRouteLedgerPrunesOldestEntriesAtBound(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "routes.json")
	start := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	routes := make([]Route, 1026)
	for i := range routes {
		routes[i] = Route{
			Token: fmt.Sprintf("rt_%032x", i), ProjectID: "p",
			Kind: "item", CreatedAt: start.Add(time.Duration(i) * time.Second),
		}
	}
	if err := saveRoutes(context.Background(), path, routes); err != nil {
		t.Fatalf("saveRoutes: %v", err)
	}
	var ledger routeLedger
	if err := readJSON(path, &ledger); err != nil {
		t.Fatalf("readJSON: %v", err)
	}
	if len(ledger.Routes) != 1024 {
		t.Fatalf("route count = %d, want 1024", len(ledger.Routes))
	}
	if _, err := resolveRoute(path, routes[0].Token); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("oldest route error = %v", err)
	}
	if got, err := resolveRoute(path, routes[len(routes)-1].Token); err != nil || got.Token == "" {
		t.Fatalf("newest route = %+v, %v", got, err)
	}
}

func TestRegistryPreferenceBoundsDueTimeAndFullPurge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(filepath.Join(t.TempDir(), "config.yaml"), func() time.Time { return now })
	registry.Entropy = func() (string, error) { return "01JREGISTRY", nil }
	root := t.TempDir()
	project, _, err := registry.Register(root)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	for _, request := range []struct {
		days                 int
		never, reset, global bool
	}{
		{},
		{days: 366},
		{days: -1},
		{days: 7, global: true},
		{never: true, reset: true},
	} {
		if err := registry.Preference(root, request.days, request.never, request.reset, request.global); !errors.Is(err, ErrInvalidPreference) {
			t.Fatalf("Preference(%+v) error = %v", request, err)
		}
	}
	if err := registry.Preference(root, 1, false, false, false); err != nil {
		t.Fatalf("snooze: %v", err)
	}
	offer, err := registry.Offer(project, []Channel{ChannelMacOS})
	if err != nil || offer.State != "snoozed" {
		t.Fatalf("snoozed Offer = %+v, %v", offer, err)
	}
	registry.Now = func() time.Time { return now.Add(25 * time.Hour) }
	offer, err = registry.Offer(project, []Channel{ChannelMacOS})
	if err != nil || offer.State != "ask" {
		t.Fatalf("due Offer = %+v, %v", offer, err)
	}
	offer, err = registry.Offer(project, nil)
	if err != nil || offer.State != "unavailable" {
		t.Fatalf("unavailable Offer = %+v, %v", offer, err)
	}
	if err := registry.Preference("", 0, true, false, true); err != nil {
		t.Fatalf("global never: %v", err)
	}
	if err := registry.Preference("", 0, false, true, true); err != nil {
		t.Fatalf("global reset: %v", err)
	}
	if err := registry.RememberComponent("other", "", "", "1.0.0"); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("invalid RememberComponent error = %v", err)
	}
	if err := registry.ForgetComponent("other", "", ""); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("invalid ForgetComponent error = %v", err)
	}
	if _, err := registry.Enroll(root, nil); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("empty Enroll error = %v", err)
	}
	if err := registry.PurgeChannels([]Channel{"other"}); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("invalid PurgeChannels error = %v", err)
	}
	if err := registry.PurgeChannels([]Channel{ChannelMacOS, ChannelVSCode}); err != nil {
		t.Fatalf("full PurgeChannels: %v", err)
	}
	if _, err := registry.FindByID(project.ID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("purged project lookup error = %v", err)
	}
}

func TestNewStoresProvideProductionDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	registry := NewRegistry(filepath.Join(t.TempDir(), "config.yaml"), func() time.Time { return now })
	if registry.Now == nil || registry.Entropy == nil {
		t.Fatal("registry defaults are nil")
	}
	delivery := NewDeliveryStore(t.TempDir(), func() time.Time { return now })
	if delivery.Now == nil || delivery.Mint == nil || delivery.LeaseTTL != defaultLeaseTTL {
		t.Fatalf("delivery defaults = %+v", delivery)
	}
	if err := delivery.PurgeChannels(nil); err != nil {
		t.Fatalf("empty purge: %v", err)
	}
	emptyRoot := NewDeliveryStore("", func() time.Time { return now })
	if err := emptyRoot.PurgeChannels([]Channel{ChannelMacOS}); err == nil {
		t.Fatal("empty delivery root purge succeeded")
	}
	if _, err := os.Stat(filepath.Join(delivery.Root, "routes.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor unexpectedly wrote state: %v", err)
	}
}
