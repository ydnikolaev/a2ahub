package validate

import (
	"fmt"
	"os"
	"slices"
	"testing"
)

func TestValidateManifestPolicyAuthorityMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manifest  string
		wantPaths []string
	}{
		{
			name: "valid active and historical participants",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
  - {system: legacy, section: legacy/, owners: [alice], status: left}
`,
		},
		{
			name: "duplicate systems and sections",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
  - {system: axon, section: axon/, owners: [bob], status: active}
`,
			wantPaths: []string{"participants.1.section", "participants.1.system"},
		},
		{
			name: "unclean nested section",
			manifest: `
space: demo
participants:
  - {system: axon, section: teams/axon/, owners: [alice], status: active}
`,
			wantPaths: []string{"participants.0.section"},
		},
		{
			name: "active participant without owner",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [], status: active}
`,
			wantPaths: []string{"participants.0.owners"},
		},
		{
			name: "one active login cannot own two systems",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
  - {system: matrix, section: matrix/, owners: [alice], status: active}
`,
			wantPaths: []string{"participants.1.owners.0"},
		},
		{
			name: "empty authority fields",
			manifest: `
space: demo
participants:
  - {system: " ", section: ./, owners: [" "], status: active}
`,
			wantPaths: []string{"participants.0.owners.0", "participants.0.section", "participants.0.system"},
		},
	}

	engine := mustEngine(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := engine.ValidateManifestPolicy([]byte(test.manifest))
			if err != nil {
				t.Fatalf("ValidateManifestPolicy: %v", err)
			}
			var gotPaths []string
			for _, violation := range result.Violations {
				if violation.Code != "REF-013" || violation.Class != ClassReferential || violation.Severity != SeverityReject {
					t.Fatalf("violation = %+v, want rejecting REF-013 referential violation", violation)
				}
				gotPaths = append(gotPaths, violation.Path)
			}
			slices.Sort(gotPaths)
			if !slices.Equal(gotPaths, test.wantPaths) {
				t.Fatalf("paths = %v, want %v", gotPaths, test.wantPaths)
			}
			if result.Valid != (len(test.wantPaths) == 0) {
				t.Fatalf("Valid = %v, want %v", result.Valid, len(test.wantPaths) == 0)
			}
		})
	}
}

// TestValidateManifestPolicyNotificationRoutes is space-notify-2026-08 P1's
// four policy rules (rule 4 excepted — it needs the raw bytes and is covered
// by TestValidateManifestPolicyRule4SecretShape below), exercised directly
// through ValidateManifestPolicy the way V3 runs them on every PR.
func TestValidateManifestPolicyNotificationRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manifest  string
		wantCodes []string
		wantPaths []string
	}{
		{
			name: "rule 1: for names an unknown participant",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", for: ghost, events: [human-gate]}
`,
			wantCodes: []string{"REF-022"},
			wantPaths: []string{"notification_routes.0.for"},
		},
		{
			name: "rule 1: for naming a known participant is accepted",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", for: axon, events: [human-gate]}
`,
		},
		{
			name: "rule 2: duplicate (channel, chat, topic, for) tuple",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", topic: 4, for: axon, events: [blocking]}
  - {channel: telegram, chat: "-100123", topic: 4, for: axon, events: [human-gate]}
`,
			wantCodes: []string{"REF-022"},
			wantPaths: []string{"notification_routes.1"},
		},
		{
			name: "rule 2: same chat but different topic is not a duplicate",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", topic: 4, events: [blocking]}
  - {channel: telegram, chat: "-100123", topic: 5, events: [blocking]}
`,
		},
		{
			name: "rule 3: events: [published] is legal widening, no code",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published]}
`,
		},
	}

	engine := mustEngine(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := engine.ValidateManifestPolicy([]byte(test.manifest))
			if err != nil {
				t.Fatalf("ValidateManifestPolicy: %v", err)
			}
			var gotCodes, gotPaths []string
			for _, violation := range result.Violations {
				gotCodes = append(gotCodes, violation.Code)
				gotPaths = append(gotPaths, violation.Path)
				if violation.Class != ClassReferential || violation.Severity != SeverityReject {
					t.Fatalf("violation = %+v, want a rejecting referential violation", violation)
				}
			}
			if !slices.Equal(gotCodes, test.wantCodes) {
				t.Fatalf("codes = %v, want %v", gotCodes, test.wantCodes)
			}
			if !slices.Equal(gotPaths, test.wantPaths) {
				t.Fatalf("paths = %v, want %v", gotPaths, test.wantPaths)
			}
			if result.Valid != (len(test.wantCodes) == 0) {
				t.Fatalf("Valid = %v, want %v", result.Valid, len(test.wantCodes) == 0)
			}
		})
	}
}

// TestValidateManifestPolicyRouteShape proves space-notify-2026-08 P1's
// route-shape rules that used to live in space.schema.json's now-frozen
// notification_routes item declaration (additionalProperties:false,
// required, enum, pattern, minItems/uniqueItems, maxItems). space.schema.json
// itself is byte-frozen at row 16 of schemas/published-v1.sha256 and can
// carry no such rule again, so every one of these checks is now Go policy —
// POL-021, ClassPolicy, never REF-022's ClassReferential, because none of
// these has a referent: an unknown key or a wrong-typed field is a fact
// about the route ITSELF, not about whether it resolves against the
// manifest's participants[] (that remains REF-022's job, proven above).
func TestValidateManifestPolicyRouteShape(t *testing.T) {
	t.Parallel()

	seventeenRoutes := "notification_routes:\n"
	for i := 0; i < 17; i++ {
		seventeenRoutes += fmt.Sprintf("  - {channel: telegram, chat: \"-1001%02d\", events: [blocking]}\n", i)
	}

	tests := []struct {
		name      string
		manifest  string
		wantPaths []string
	}{
		{
			name: "unknown key",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [blocking], frequency: hourly}
`,
			wantPaths: []string{"notification_routes.0.frequency"},
		},
		{
			name: "missing chat",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, events: [blocking]}
`,
			wantPaths: []string{"notification_routes.0.chat"},
		},
		{
			name: "chat as an integer, not a string",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: -100123, events: [blocking]}
`,
			wantPaths: []string{"notification_routes.0.chat"},
		},
		{
			name: "chat does not match ^-?[0-9]+$",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "abc", events: [blocking]}
`,
			wantPaths: []string{"notification_routes.0.chat"},
		},
		{
			name: "missing channel",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {chat: "-100123", events: [blocking]}
`,
			wantPaths: []string{"notification_routes.0.channel"},
		},
		{
			name: "channel outside enum",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: whatsapp, chat: "-100123", events: [blocking]}
`,
			wantPaths: []string{"notification_routes.0.channel"},
		},
		{
			name: "topic below the floor of 1",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", topic: 0, events: [blocking]}
`,
			wantPaths: []string{"notification_routes.0.topic"},
		},
		{
			name: "topic at the floor of 1 is accepted",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", topic: 1, events: [blocking]}
`,
		},
		{
			name: "missing events",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123"}
`,
			wantPaths: []string{"notification_routes.0.events"},
		},
		{
			name: "empty events",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: []}
`,
			wantPaths: []string{"notification_routes.0.events"},
		},
		{
			name: "event outside enum",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [rejected]}
`,
			wantPaths: []string{"notification_routes.0.events.0"},
		},
		{
			name: "duplicate event",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [blocking, blocking]}
`,
			wantPaths: []string{"notification_routes.0.events.1"},
		},
		{
			name: "locale outside enum",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [blocking], locale: fr}
`,
			wantPaths: []string{"notification_routes.0.locale"},
		},
		{
			name: "secret does not match ^[A-Z][A-Z0-9_]*$",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [blocking], secret: tg_bot_token}
`,
			wantPaths: []string{"notification_routes.0.secret"},
		},
		{
			name: "renderer outside enum",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [blocking], renderer: pdf}
`,
			wantPaths: []string{"notification_routes.0.renderer"},
		},
		{
			name: "17 routes exceeds maxItems 16",
			manifest: "space: demo\nparticipants:\n  - {system: axon, section: axon/, owners: [alice], status: active}\n" +
				seventeenRoutes,
			wantPaths: []string{"notification_routes"},
		},
		{
			name: "a route that is not a mapping at all",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - telegram
`,
			wantPaths: []string{"notification_routes.0"},
		},
		{
			name: "notification_routes itself is not an array",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes: telegram
`,
			wantPaths: []string{"notification_routes"},
		},
		{
			name: "every optional field set is well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", topic: 42, for: axon, events: [human-gate, blocking], locale: ru, secret: TG_BOT_TOKEN_AXON, renderer: rich}
`,
		},
		{
			name: "minimal route is well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published]}
`,
		},
		// -- P11 (answers-that-hold-2026-08 spec 11): the four selector keys.
		{
			name: "kind as a list of known fold.Kind values is well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], kind: [contract, announcement]}
`,
		},
		{
			name: "kind as a bare scalar is well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], kind: contract}
`,
		},
		{
			name: "kind outside fold's own vocabulary",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], kind: [not-a-real-kind]}
`,
			wantPaths: []string{"notification_routes.0.kind"},
		},
		{
			name: "duplicate kind value",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], kind: [contract, contract]}
`,
			wantPaths: []string{"notification_routes.0.kind"},
		},
		{
			name: "state as a list of known fold.State values is well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], state: [published, draft]}
`,
		},
		{
			name: "state outside fold's own vocabulary",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], state: [not-a-real-state]}
`,
			wantPaths: []string{"notification_routes.0.state"},
		},
		{
			name: "direction inbound/broadcast/any are each well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], direction: inbound}
  - {channel: telegram, chat: "-100124", events: [published], direction: broadcast}
  - {channel: telegram, chat: "-100125", events: [published], direction: any}
`,
		},
		{
			name: "direction outside its own enum",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], direction: sideways}
`,
			wantPaths: []string{"notification_routes.0.direction"},
		},
		{
			name: "urgency as a list of the four named predicates is well-formed",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], urgency: [human-gate, p1, blocking, overdue]}
`,
		},
		{
			name: "urgency outside its own enum",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", events: [published], urgency: [p2]}
`,
			wantPaths: []string{"notification_routes.0.urgency"},
		},
		{
			name: "all four selector dimensions together, combined with events, is well-formed (AC-4 intersection at the policy layer)",
			manifest: `
space: demo
participants:
  - {system: axon, section: axon/, owners: [alice], status: active}
notification_routes:
  - {channel: telegram, chat: "-100123", for: axon, events: [blocking, published], kind: [contract, announcement], state: [published], direction: inbound, urgency: [p1, blocking]}
`,
		},
	}

	engine := mustEngine(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := engine.ValidateManifestPolicy([]byte(test.manifest))
			if err != nil {
				t.Fatalf("ValidateManifestPolicy: %v", err)
			}
			var gotPaths []string
			for _, violation := range result.Violations {
				if violation.Code != "POL-021" || violation.Class != ClassPolicy || violation.Severity != SeverityReject {
					t.Fatalf("violation = %+v, want rejecting POL-021 policy violation", violation)
				}
				gotPaths = append(gotPaths, violation.Path)
			}
			if !slices.Equal(gotPaths, test.wantPaths) {
				t.Fatalf("paths = %v, want %v", gotPaths, test.wantPaths)
			}
			if result.Valid != (len(test.wantPaths) == 0) {
				t.Fatalf("Valid = %v, want %v", result.Valid, len(test.wantPaths) == 0)
			}
		})
	}
}

// TestValidateManifestRouteShape re-proves, on the ValidateManifest (PR-time
// schema) path, the two cases that used to be
// fixtures/invalid/space-route-unknown-key.yaml and
// space-route-empty-events.yaml before space.schema.json was restored to its
// frozen, pre-space-notify-2026-08 bytes. Those fixtures could not survive:
// golden_test.go validates fixtures/invalid/*.yaml SCHEMA-ONLY, and POL-021
// is policy-class, invisible to that path — reproduced verbatim as "expected
// EXACTLY [SCH-xxx], got []" the moment the schema stopped declaring
// notification_routes at all. TestValidateManifestPolicyRouteShape above
// proves the same rules through ValidateManifestPolicy (V3's path); this
// proves they ALSO fire through ValidateManifest (the PR-time path the
// deleted fixtures exercised), the same "both paths must agree" argument
// TestValidateManifestPolicyRule4SecretShape below makes for rule 4.
func TestValidateManifestRouteShape(t *testing.T) {
	t.Parallel()

	// A fully schema-valid manifest otherwise, so the only violation in play
	// is the route-shape one under test — not SCH-001 for an unrelated
	// missing required field.
	const manifestBase = `
schema: space/v1
space: demo
min_binary_version: 0.1.0
gates: default
participants:
  - {system: axon, org: yura, section: axon/, owners: [alice], status: active, joined: 2026-07-28}
vendored: []
`

	tests := []struct {
		name     string
		manifest string
	}{
		{
			name: "unknown route key (was space-route-unknown-key.yaml)",
			manifest: manifestBase + `
notification_routes:
  - {channel: telegram, chat: "-1002034567890", events: [human-gate], frequency: hourly}
`,
		},
		{
			name: "empty events (was space-route-empty-events.yaml)",
			manifest: manifestBase + `
notification_routes:
  - {channel: telegram, chat: "-1002034567890", events: []}
`,
		},
	}

	engine := mustEngine(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := engine.ValidateManifest([]byte(test.manifest))
			if err != nil {
				t.Fatalf("ValidateManifest: %v", err)
			}
			if result.Valid {
				t.Fatal("Valid = true, want false: manifest carries a malformed notification route")
			}
			var found bool
			for _, violation := range result.Violations {
				if violation.Code == "POL-021" && violation.Class == ClassPolicy && violation.Severity == SeverityReject {
					found = true
				}
			}
			if !found {
				t.Fatalf("violations = %+v, want a POL-021 route-shape violation", result.Violations)
			}
		})
	}
}

// TestValidateManifestPolicyRule4SecretShape is space-notify-2026-08 P1's
// rule 4: the manifest's raw bytes carry no Telegram bot-token shape
// anywhere, exercised through BOTH ValidateManifest (the PR-time schema
// path) and ValidateManifestPolicy (V3's authority-map-only path) — the
// fixture pairing convention only proves the first, and the two must agree
// or a manifest carrying a token could pass V3's authority pre-check.
func TestValidateManifestPolicyRule4SecretShape(t *testing.T) {
	t.Parallel()

	const manifestWithToken = `
space: demo
gates: default
min_binary_version: 0.1.0
participants:
  - {system: axon, org: yura, section: axon/, owners: [alice], status: active, joined: 2026-07-28}
notification_routes: []
vendored: []
# leaked in a stray comment: 123456789:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`
	engine := mustEngine(t)

	t.Run("ValidateManifestPolicy rejects", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateManifestPolicy([]byte(manifestWithToken))
		if err != nil {
			t.Fatalf("ValidateManifestPolicy: %v", err)
		}
		assertHasPOL001(t, result)
	})

	t.Run("ValidateManifest rejects", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateManifest([]byte(manifestWithToken))
		if err != nil {
			t.Fatalf("ValidateManifest: %v", err)
		}
		assertHasPOL001(t, result)
	})
}

func assertHasPOL001(t *testing.T, result Result) {
	t.Helper()
	if result.Valid {
		t.Fatal("Valid = true, want false: manifest carries a Telegram bot-token shape")
	}
	for _, violation := range result.Violations {
		if violation.Code == "POL-001" {
			return
		}
	}
	t.Fatalf("violations = %+v, want a POL-001 secret-shape violation", result.Violations)
}

// manifestWithPolicyKeys builds an otherwise schema-valid manifest carrying the
// given extra top-level lines, so the only finding in play is the one under
// test and never an unrelated SCH-001.
func manifestWithPolicyKeys(extra string) string {
	return `
schema: space/v1
space: demo
min_binary_version: 0.1.0
` + extra + `
participants:
  - {system: axon, org: yura, section: axon/, owners: [alice], status: active, joined: 2026-07-28}
vendored: []
`
}

// TestValidateManifestGatesFlowVocabulary is local-space-2026-10 spec 04 AC 1:
// every legal value of `gates` and `flow`, and absence, validates clean, and
// each out-of-vocabulary shape (an unknown word, a mapping, a list, a number, a
// wrong case) is exactly one POL-027 naming the offending key.
func TestValidateManifestGatesFlowVocabulary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		extra    string
		wantPath string // "" means the manifest must be valid
	}{
		{name: "both absent", extra: ""},
		{name: "gates default", extra: "gates: default"},
		{name: "gates confirm", extra: "gates: confirm"},
		{name: "gates autonomous", extra: "gates: autonomous"},
		{name: "flow full", extra: "flow: full"},
		{name: "flow express", extra: "flow: express"},
		{name: "all four together", extra: "gates: confirm\nflow: express"},
		{name: "gates unknown word", extra: "gates: sometimes", wantPath: "gates"},
		{name: "gates wrong case", extra: "gates: Confirm", wantPath: "gates"},
		{name: "gates mapping", extra: "gates: {G1: confirm}", wantPath: "gates"},
		{name: "gates list", extra: "gates: [confirm]", wantPath: "gates"},
		{name: "gates number", extra: "gates: 3", wantPath: "gates"},
		{name: "flow unknown word", extra: "flow: fast", wantPath: "flow"},
		{name: "flow mapping", extra: "flow: {mode: express}", wantPath: "flow"},
		{name: "flow number", extra: "flow: 1", wantPath: "flow"},
	}

	engine := mustEngine(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := engine.ValidateManifest([]byte(manifestWithPolicyKeys(test.extra)))
			if err != nil {
				t.Fatalf("ValidateManifest: %v", err)
			}
			if test.wantPath == "" {
				if !result.Valid || len(result.Violations) != 0 {
					t.Fatalf("manifest %q: Valid = %v, violations = %+v, want a clean result", test.extra, result.Valid, result.Violations)
				}
				return
			}
			if result.Valid {
				t.Fatalf("manifest %q: Valid = true, want POL-027", test.extra)
			}
			if len(result.Violations) != 1 {
				t.Fatalf("manifest %q: violations = %+v, want exactly one POL-027", test.extra, result.Violations)
			}
			got := result.Violations[0]
			if got.Code != "POL-027" || got.Class != ClassPolicy || got.Severity != SeverityReject || got.Path != test.wantPath {
				t.Fatalf("manifest %q: violation = %+v, want POL-027 / policy / reject / path %q", test.extra, got, test.wantPath)
			}
		})
	}
}

// TestValidateManifestGatesFlowVocabularyIsNotAnEveryPRRule is AC 2: the same
// bytes give POL-027 from ValidateManifest (which runs only when space.yaml
// changes) and nothing from ValidateManifestPolicy (which V3 runs on every
// pull request), so an old odd value never reds an unrelated PR.
func TestValidateManifestGatesFlowVocabularyIsNotAnEveryPRRule(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	for _, extra := range []string{"gates: sometimes", "flow: fast", "gates: {G1: autonomous}"} {
		raw := []byte(manifestWithPolicyKeys(extra))

		full, err := engine.ValidateManifest(raw)
		if err != nil {
			t.Fatalf("ValidateManifest(%q): %v", extra, err)
		}
		if got := violationCodes(full.Violations); !slices.Equal(got, []string{"POL-027"}) {
			t.Fatalf("ValidateManifest(%q) codes = %v, want [POL-027]", extra, got)
		}

		policy, err := engine.ValidateManifestPolicy(raw)
		if err != nil {
			t.Fatalf("ValidateManifestPolicy(%q): %v", extra, err)
		}
		if !policy.Valid || len(policy.Violations) != 0 {
			t.Fatalf("ValidateManifestPolicy(%q) = %+v, want clean: an old odd value must not red an unrelated PR", extra, policy)
		}
	}
}

// TestShippedManifestFixturesStayClean pins US-5: every existing GitHub
// space carries `gates: default`, which the new vocabulary must accept, and the
// valid golden fixture for the local policy keys must validate end to end.
func TestShippedManifestFixturesStayClean(t *testing.T) {
	t.Parallel()

	engine := mustEngine(t)
	for _, path := range []string{
		"../../schemas/manifest/v1/fixtures/valid/space.yaml",
		"../../schemas/manifest/v1/fixtures/valid/space-local-policy.yaml",
		"../../space-template/space.yaml",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, violation := range mustValidateManifest(t, engine, raw) {
			if violation.Code == "POL-027" || violation.Code == "POL-028" {
				t.Errorf("%s: %+v, want no gates/flow finding", path, violation)
			}
		}
	}
}

func mustValidateManifest(t *testing.T, engine *Engine, raw []byte) []Violation {
	t.Helper()
	result, err := engine.ValidateManifest(raw)
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	return result.Violations
}
