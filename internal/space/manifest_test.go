package space

import (
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

const validManifestYAML = `
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
gates: default
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
  - system: seomatrix
    org: seomatrix
    section: seomatrix/
    owners: [misha-gh]
    status: active
    joined: 2026-07-28
vendored: []
`

func TestParseManifestValid(t *testing.T) {
	t.Parallel()

	m, err := ParseManifest([]byte(validManifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Space != "getvisa" || m.MinBinaryVersion != "0.1.0" {
		t.Fatalf("Manifest = %+v, want space=getvisa min_binary_version=0.1.0", m)
	}
	if len(m.Participants) != 2 {
		t.Fatalf("len(Participants) = %d, want 2", len(m.Participants))
	}

	sys, ok := m.SystemForLogin("ydnikolaev")
	if !ok || sys != "axon" {
		t.Fatalf("SystemForLogin(ydnikolaev) = (%q, %v), want (axon, true)", sys, ok)
	}
	if _, ok := m.SystemForLogin("nobody"); ok {
		t.Fatal("SystemForLogin(nobody) = true, want false (CC-097 unmapped identity)")
	}
}

// TestParseManifestCapabilitiesReadableWithoutAsking is P5 AC5/US-3: a
// counterparty reads a participant's declared capabilities straight off the
// parsed manifest — no separate request, no second document. axon declares
// its capabilities; seomatrix declares none, which must decode as a nil
// *Capabilities (the live UNDECLARED state), not a zero-value struct that
// would read as "declared, empty".
func TestParseManifestCapabilitiesReadableWithoutAsking(t *testing.T) {
	t.Parallel()

	const manifestYAML = `
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
    capabilities:
      delivery: [file]
      declared_by: axon
      declared_at: 2026-08-10T00:00:00Z
  - system: seomatrix
    org: seomatrix
    section: seomatrix/
    owners: [misha-gh]
    status: active
    joined: 2026-07-28
`
	m, err := ParseManifest([]byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Participants) != 2 {
		t.Fatalf("len(Participants) = %d, want 2", len(m.Participants))
	}

	axon := m.Participants[0]
	if axon.Capabilities == nil {
		t.Fatal("axon.Capabilities = nil, want a declared value")
	}
	if len(axon.Capabilities.Delivery) != 1 || axon.Capabilities.Delivery[0] != "file" {
		t.Fatalf("axon.Capabilities.Delivery = %v, want [file]", axon.Capabilities.Delivery)
	}
	if axon.Capabilities.DeclaredBy != "axon" || axon.Capabilities.DeclaredAt != "2026-08-10T00:00:00Z" {
		t.Fatalf("axon.Capabilities stamps = %+v, want declared_by=axon declared_at=2026-08-10T00:00:00Z", axon.Capabilities)
	}

	seomatrix := m.Participants[1]
	if seomatrix.Capabilities != nil {
		t.Fatalf("seomatrix.Capabilities = %+v, want nil (undeclared, not a zero-value struct)", seomatrix.Capabilities)
	}
}

// TestParseManifestNotificationRouteMissingChat is spec 01 AC8: a route
// missing the schema-required `chat` field entirely must still parse into a
// usable Manifest — ParseManifest is a structural decode only, and the
// well-formedness verdict (REF-022) belongs to the ManifestValidator seam,
// never here.
func TestParseManifestNotificationRouteMissingChat(t *testing.T) {
	t.Parallel()

	const manifestYAML = `
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
notification_routes:
  - channel: telegram
    events: [blocking]
`
	m, err := ParseManifest([]byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.NotificationRoutes) != 1 {
		t.Fatalf("len(NotificationRoutes) = %d, want 1", len(m.NotificationRoutes))
	}
	route := m.NotificationRoutes[0]
	if route.Chat != "" {
		t.Fatalf("route.Chat = %q, want empty (missing chat must still parse)", route.Chat)
	}
	if route.Channel != "telegram" {
		t.Fatalf("route.Channel = %q, want telegram", route.Channel)
	}
}

// TestParseManifestNotificationRouteUnknownKey is spec 01 AC8's second edge
// case: a route carrying a key the schema does not declare (which is a
// REJECT once the ManifestValidator seam runs, via `additionalProperties:
// false`) must still parse here — ParseManifest never enforces
// additionalProperties, so an unknown key is silently ignored rather than a
// decode failure.
func TestParseManifestNotificationRouteUnknownKey(t *testing.T) {
	t.Parallel()

	const manifestYAML = `
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
notification_routes:
  - channel: telegram
    chat: "-1002034567890"
    events: [blocking]
    made_up_field: surprise
`
	m, err := ParseManifest([]byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.NotificationRoutes) != 1 {
		t.Fatalf("len(NotificationRoutes) = %d, want 1", len(m.NotificationRoutes))
	}
	route := m.NotificationRoutes[0]
	if route.Chat != "-1002034567890" || route.Channel != "telegram" {
		t.Fatalf("route = %+v, want the known fields decoded despite the unknown key", route)
	}
}

// TestParseManifestNotificationRouteRoundTrips is spec 01 AC8/T2: a
// well-formed route with every optional field set decodes into the typed
// field with every value intact, including `chat` staying a STRING (a
// Telegram supergroup id can exceed the safe integer range of several
// YAML/JSON readers, spec 01 §T2) and `topic` distinguishing "absent" from
// the schema's own floor of 1.
func TestParseManifestNotificationRouteRoundTrips(t *testing.T) {
	t.Parallel()

	const manifestYAML = `
schema: space/v1
space: getvisa
min_binary_version: 0.1.0
participants:
  - system: axon
    org: yura
    section: axon/
    owners: [ydnikolaev]
    status: active
    joined: 2026-07-28
notification_routes:
  - channel: telegram
    chat: "-1002034567890"
    topic: 42
    for: axon
    events: [human-gate, blocking]
    locale: ru
    secret: TG_BOT_TOKEN
    renderer: rich
`
	m, err := ParseManifest([]byte(manifestYAML))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.NotificationRoutes) != 1 {
		t.Fatalf("len(NotificationRoutes) = %d, want 1", len(m.NotificationRoutes))
	}
	route := m.NotificationRoutes[0]
	if route.Channel != "telegram" {
		t.Fatalf("route.Channel = %q, want telegram", route.Channel)
	}
	if route.Chat != "-1002034567890" {
		t.Fatalf("route.Chat = %q, want -1002034567890 (must stay a string)", route.Chat)
	}
	if route.Topic == nil || *route.Topic != 42 {
		t.Fatalf("route.Topic = %v, want *42", route.Topic)
	}
	if route.For != "axon" {
		t.Fatalf("route.For = %q, want axon", route.For)
	}
	if len(route.Events) != 2 || route.Events[0] != "human-gate" || route.Events[1] != "blocking" {
		t.Fatalf("route.Events = %v, want [human-gate blocking]", route.Events)
	}
	if route.Locale != "ru" {
		t.Fatalf("route.Locale = %q, want ru", route.Locale)
	}
	if route.Secret != "TG_BOT_TOKEN" {
		t.Fatalf("route.Secret = %q, want TG_BOT_TOKEN", route.Secret)
	}
	if route.Renderer != "rich" {
		t.Fatalf("route.Renderer = %q, want rich", route.Renderer)
	}
}

func TestParseManifestInvalidYAML(t *testing.T) {
	t.Parallel()

	_, err := ParseManifest([]byte("not: [valid: yaml"))
	if !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("ParseManifest error = %v, want ErrManifestInvalid", err)
	}
}

func TestSystemForLoginFailsClosedForLeftAndAmbiguousOwners(t *testing.T) {
	t.Parallel()

	left := Manifest{Participants: []Participant{{
		System: "legacy", Owners: []string{"alice"}, Status: fold.MembershipLeft,
	}}}
	if system, ok := left.SystemForLogin("alice"); ok || system != "" {
		t.Fatalf("left SystemForLogin = (%q, %v), want no authority", system, ok)
	}

	ambiguous := Manifest{Participants: []Participant{
		{System: "axon", Owners: []string{"alice"}, Status: fold.MembershipMember},
		{System: "matrix", Owners: []string{"alice"}, Status: fold.MembershipMember},
	}}
	if system, ok := ambiguous.SystemForLogin("alice"); ok || system != "" {
		t.Fatalf("ambiguous SystemForLogin = (%q, %v), want fail closed", system, ok)
	}
}

// fakeManifestValidator is a hand-written test double for the
// ManifestValidator seam (rails: hand-written mocks, no codegen).
type fakeManifestValidator struct {
	err error
}

func (f *fakeManifestValidator) ValidateManifest(_ context.Context, _ []byte) error {
	return f.err
}

// TestLoadManifestPropagatesValidatorError exercises LoadManifest — the
// composed parse+validate operation — through the package's own code
// path, not the fake echoing itself: a validator error must surface from
// LoadManifest even though the YAML parsed fine.
func TestLoadManifestPropagatesValidatorError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("missing participant map entry for login x (CC-097 precondition)")
	v := &fakeManifestValidator{err: wantErr}

	_, err := LoadManifest(context.Background(), []byte(validManifestYAML), v)
	if !errors.Is(err, wantErr) {
		t.Fatalf("LoadManifest = %v, want wrapping %v", err, wantErr)
	}
}

// TestLoadManifestValidatorApprovesValidManifest is the success path: a
// validator that approves lets LoadManifest return the parsed Manifest.
func TestLoadManifestValidatorApprovesValidManifest(t *testing.T) {
	t.Parallel()

	v := &fakeManifestValidator{}
	m, err := LoadManifest(context.Background(), []byte(validManifestYAML), v)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Space != "getvisa" {
		t.Fatalf("Manifest.Space = %q, want getvisa", m.Space)
	}
}

// TestLoadManifestParseFailureShortCircuitsValidator confirms a
// structural YAML failure never even reaches the validator seam.
func TestLoadManifestParseFailureShortCircuitsValidator(t *testing.T) {
	t.Parallel()

	v := &fakeManifestValidator{err: errors.New("should never be called")}
	_, err := LoadManifest(context.Background(), []byte("not: [valid: yaml"), v)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("LoadManifest error = %v, want ErrManifestInvalid", err)
	}
}

var _ ManifestValidator = (*fakeManifestValidator)(nil)
