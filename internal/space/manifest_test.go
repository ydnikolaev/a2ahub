package space

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
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

// TestManifestEffectiveGates is local-space-2026-10 spec 04 AC 4: the shipped
// template's `gates: default` reads as `review` on a host that reviews writes
// and as `confirm` on one that does not; an unknown value fails closed to
// `confirm` locally; and `autonomous` is honoured only locally.
func TestManifestEffectiveGates(t *testing.T) {
	t.Parallel()

	template, err := os.ReadFile("../../space-template/space.yaml")
	if err != nil {
		t.Fatalf("read the shipped template: %v", err)
	}

	tests := []struct {
		name          string
		yaml          string
		reviewsWrites bool
		want          contract.GatesMode
	}{
		{"shipped template on a reviewed host", string(template), true, contract.GatesModeReview},
		{"shipped template on a local host", string(template), false, contract.GatesModeConfirm},
		{"absent on a local host", "space: demo\n", false, contract.GatesModeConfirm},
		{"absent on a reviewed host", "space: demo\n", true, contract.GatesModeReview},
		{"confirm on a local host", "gates: confirm\n", false, contract.GatesModeConfirm},
		{"autonomous on a local host", "gates: autonomous\n", false, contract.GatesModeAutonomous},
		{"autonomous on a reviewed host is ignored", "gates: autonomous\n", true, contract.GatesModeReview},
		{"unknown word fails closed locally", "gates: sometimes\n", false, contract.GatesModeConfirm},
		{"mapping fails closed locally", "gates: {G1: autonomous}\n", false, contract.GatesModeConfirm},
		{"list fails closed locally", "gates: [autonomous]\n", false, contract.GatesModeConfirm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := ParseManifest([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("ParseManifest must stay lenient about the policy keys' shape: %v", err)
			}
			if got := m.EffectiveGates(tt.reviewsWrites); got != tt.want {
				t.Fatalf("EffectiveGates(reviewsWrites=%v) = %q, want %q", tt.reviewsWrites, got, tt.want)
			}
		})
	}
}

// TestManifestFlowSetting: absent and unknown read as `full` (the read side
// never refuses, spec 04 §T1.1); `express` is read only when declared.
func TestManifestFlowSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want contract.FlowSetting
	}{
		{"absent", "space: demo\n", contract.FlowSettingFull},
		{"full", "flow: full\n", contract.FlowSettingFull},
		{"express", "flow: express\n", contract.FlowSettingExpress},
		{"unknown word", "flow: fast\n", contract.FlowSettingFull},
		{"mapping", "flow: {mode: express}\n", contract.FlowSettingFull},
		{"number", "flow: 1\n", contract.FlowSettingFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := ParseManifest([]byte(tt.yaml))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			if got := m.FlowSetting(); got != tt.want {
				t.Fatalf("FlowSetting() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestManifestPolicyKeysAreLenientOnRead: a typed decode target would make
// yaml.Unmarshal fail on a structured `gates` and take every read verb down
// with it. The policy keys decode as `any`; the verdict is validate's.
func TestManifestPolicyKeysAreLenientOnRead(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{
		validManifestYAML + "flow: {a: 1}\n",
		strings.Replace(validManifestYAML, "gates: default", "gates: {G1: confirm}", 1),
		strings.Replace(validManifestYAML, "gates: default", "gates: [confirm, autonomous]", 1),
	} {
		m, err := ParseManifest([]byte(doc))
		if err != nil {
			t.Fatalf("ParseManifest(%q) = %v, want a manifest that still reads", doc, err)
		}
		if m.Space != "getvisa" || len(m.Participants) != 2 {
			t.Fatalf("manifest = %+v, want the rest of the document intact", m)
		}
	}
}

// TestParseManifestReadsPolicyKeysThroughTheSharedDecoder is spec 04 section 10
// (P4-B carries item 8). A merge key hands `gates: autonomous` to a plain struct
// decode while a node walk (the write-time validator's reader) sees no `gates`
// key. ParseManifest must not follow the merge: the land check reads the TIP
// through it, so a manifest that merges `autonomous` in would otherwise switch
// the approval requirement off. Both sides agree, and the write is refused.
func TestParseManifestReadsPolicyKeysThroughTheSharedDecoder(t *testing.T) {
	t.Parallel()

	const mergedAutonomous = `
schema: space/v1
space: demo
min_binary_version: 0.1.0
base: &b {gates: autonomous, flow: express}
<<: *b
participants: []
vendored: []
`
	m, err := ParseManifest([]byte(mergedAutonomous))
	if err != nil {
		t.Fatalf("ParseManifest: %v, want the bytes to parse (the refusal is the validator's)", err)
	}
	if got := m.EffectiveGates(false); got != contract.GatesModeConfirm {
		t.Fatalf("EffectiveGates(false) = %q, want %q: a merge key must not supply the gates value", got, contract.GatesModeConfirm)
	}
	if got := m.FlowSetting(); got != contract.FlowSettingFull {
		t.Fatalf("FlowSetting() = %q, want %q: a merge key must not supply the flow value", got, contract.FlowSettingFull)
	}

	// An alias into the value is the same disagreement and reads as unset too.
	aliased := "schema: space/v1\nspace: demo\ng: &g autonomous\ngates: *g\n"
	m, err = ParseManifest([]byte(aliased))
	if err != nil {
		t.Fatalf("ParseManifest(alias): %v", err)
	}
	if got := m.EffectiveGates(false); got != contract.GatesModeConfirm {
		t.Fatalf("alias: EffectiveGates(false) = %q, want %q", got, contract.GatesModeConfirm)
	}

	// The plain form still reads, so the fail-closed branch is not a blanket nil.
	plain, err := ParseManifest([]byte("schema: space/v1\nspace: demo\ngates: autonomous\nflow: express\n"))
	if err != nil {
		t.Fatalf("ParseManifest(plain): %v", err)
	}
	if got := plain.EffectiveGates(false); got != contract.GatesModeAutonomous {
		t.Fatalf("plain: EffectiveGates(false) = %q, want %q", got, contract.GatesModeAutonomous)
	}
	if got := plain.FlowSetting(); got != contract.FlowSettingExpress {
		t.Fatalf("plain: FlowSetting() = %q, want %q", got, contract.FlowSettingExpress)
	}
}
