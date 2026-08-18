package validate

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"gopkg.in/yaml.v3"
)

// ValidateManifest validates a space's own `space.yaml` against the embedded
// `space/v1` schema.
//
// # Why this exists, given that a validator for it already did
//
// It did, twice over, and neither was reachable. `space.LoadManifest` takes a
// `space.ManifestValidator` seam, and `cli.ManifestValidatorAdapter` implements
// that seam against this very corpus — and on 2026-07-26 a grep established
// that `LoadManifest` has ZERO production callers and
// `NewManifestValidatorAdapter` is constructed nowhere outside its own tests.
// Every production path reads the manifest through `space.ParseManifest`, which
// is a shape-only decode by design and says so.
//
// So the schema's `required` list, its `space` id pattern, and its
// `min_binary_version` grammar were enforced by nothing at runtime. Proven, not
// inferred: a hand-written manifest whose participant omits the schema-REQUIRED
// `org` and `joined` fields was accepted by `a2a connect`, by `a2a doctor`, and
// by `a2a validate --ci --mode=v3-full-repo`, which answered `"valid": true` —
// while the corpus, asked directly, answers `participants.0: required`.
//
// That is worse than an ordinary unvalidated file. `space.yaml` is the document
// that decides WHO MAY WRITE WHERE: diff-authz reads its participants and
// sections to authorise every PR. A manifest can therefore be merged in a shape
// the schema forbids, and the guard that depends on it goes on reading it.
//
// # Where it is enforced, and where it deliberately is not
//
// At the CI gate, on the pull request — never in `ParseManifest`. The read path
// (`doctor`, `connect`, `inbox`, the statusline) calls ParseManifest, and making
// THAT strict would mean a space whose manifest drifted stops being READABLE:
// a participant who did nothing wrong gets a hard failure on every verb. An
// unvalidated manifest is a better product than an unreadable space. The gate
// belongs where a change is proposed.
//
// Shape and reporting mirror ValidateConsumes exactly — same Result, same
// Violation codes, same one-cycle version window — because every consumer of
// this engine parses one wire contract (D-011).
func (e *Engine) ValidateManifest(raw []byte) (Result, error) {
	const op = "ValidateManifest"

	instance, probe, parseable := decodeManifest(raw)
	if !parseable {
		return newResult(V2, "", []Violation{malformedManifestViolation()}), nil
	}

	// space.schema.json's own `schema` const is literally "space/v1", not
	// "manifest/v1" — a naming tension the schema file documents and this
	// function inherits rather than resolves.
	against := "space/v1"
	if probe.Schema != "" {
		n, ok := schema.ParseVersion(probe.Schema)
		if !ok || !schema.AcceptsManifestVersion(n) {
			return newResult(V2, probe.Space, []Violation{{
				Code:     "POL-005",
				Class:    ClassPolicy,
				Path:     "schema",
				Message:  unreadableOrUnacceptedSchemaMessage("space.yaml", "space", probe.Schema),
				CCRef:    "CC-005",
				Severity: SeverityReject,
			}}), nil
		}
		against = probe.Schema
	}

	fieldViolations, serr := e.corpus.ValidateManifest(against, instance)
	if serr != nil {
		return Result{}, &Error{Op: op, Err: serr}
	}
	violations, merr := mapSchemaViolations(fieldViolations)
	if merr != nil {
		return Result{}, &Error{Op: op, Err: merr}
	}
	violations = append(violations, checkManifestPolicy(probe)...)
	// Rule 4 (space-notify-2026-08 P1): the manifest's raw bytes carry no
	// Telegram bot-token shape anywhere — not in a route, not in a
	// comment, not in a stray key. Reuses POL-001/scanForSecrets rather
	// than a new code: this is the SAME fact ("content matches a
	// forbidden secret/credential pattern") this file already scans
	// envelopes and consumes-events for (engine.go:116), just applied to
	// space.yaml's own raw bytes.
	violations = append(violations, scanForSecrets(raw)...)
	return newResult(V2, probe.Space, violations), nil
}

// ValidateManifestPolicy runs the authority-map half of manifest validation
// without repeating the schema check. V3 uses it on every PR before consulting
// the manifest for diff authorization: an old schema-only defect may remain a
// changed-file tripwire, but an ambiguous authority map must never grant write
// permission just because space.yaml itself was not changed by this PR.
func (e *Engine) ValidateManifestPolicy(raw []byte) (Result, error) {
	_, probe, parseable := decodeManifest(raw)
	if !parseable {
		// The structural/schema path owns malformed YAML. Callers cannot have
		// parsed a Manifest from these bytes in the first place, so returning
		// that second verdict here would only duplicate POL-002.
		return newResult(V2, "", nil), nil
	}
	violations := checkManifestPolicy(probe)
	// Rule 4, mirrored from ValidateManifest above: V3's authority-map-only
	// caller must see the same secret-shape refusal PR-time schema
	// validation would have seen, or the PR gate and the merge gate
	// disagree about a manifest that carries a live token.
	violations = append(violations, scanForSecrets(raw)...)
	return newResult(V2, probe.Space, violations), nil
}

// manifestProbe is validate's own minimal projection of the authority-bearing
// manifest fields. Keeping it here preserves ADR-001: validate owns policy and
// does not import the I/O-facing space package.
type manifestProbe struct {
	Schema       string                     `yaml:"schema"`
	Space        string                     `yaml:"space"`
	Participants []manifestParticipantProbe `yaml:"participants"`
	// NotificationRoutes decodes as `any`, not `[]manifestRouteParsed` or even
	// `[]any` — space.schema.json is byte-frozen (row 16 of
	// schemas/published-v1.sha256) and no longer declares notification_routes
	// at all, so nothing upstream guarantees this key is even an array before
	// this package sees it. A typed decode target that yaml.v3 cannot satisfy
	// (a mapping, a bare scalar) would fail the WHOLE manifest decode,
	// collapsing every finding into one undifferentiated POL-002 — exactly
	// the "decode error that swallows the violation" failure mode P1's own
	// spec warns against. checkNotificationRoutes below type-asserts this
	// value itself, and each of its elements, reporting the malformation
	// rather than going blind to it.
	NotificationRoutes any `yaml:"notification_routes"`
}

type manifestParticipantProbe struct {
	System  string   `yaml:"system"`
	Section string   `yaml:"section"`
	Owners  []string `yaml:"owners"`
	Status  string   `yaml:"status"`
}

// manifestRouteParsed is the referential half's best-effort typed view of
// one notification_routes[] entry (space-notify-2026-08 P1), built by
// checkRouteShape alongside its shape violations. Topic is a pointer
// because "absent" (the chat's general thread) is a distinct value from any
// concrete thread id, including the shape floor of 1. ok is false when the
// route did not even decode as a mapping — such a route participates in
// neither the `for`-resolution nor the tuple-dedup rule below, because there
// is nothing well-formed enough to compare.
type manifestRouteParsed struct {
	ok      bool
	Channel string
	Chat    string
	Topic   *int
	For     string
}

// decodeManifest decodes raw twice — once as a schema instance, once into the
// version/space probe — and reports ok=false when the document is not YAML at
// all.
//
// It returns a BOOL rather than an error deliberately. "This file is not YAML"
// is a verdict about the CONTENT, which belongs in the Result as a POL-002
// violation; an error returned from ValidateManifest means the ENGINE failed,
// which is a different thing a caller handles differently (validateCIConsumes'
// twin reports it as `Error` on the report, not as a violation). Letting the
// decode error cross that boundary and then discarding it is what a linter
// correctly objects to, and silencing the linter would have hidden a real
// distinction rather than a false positive.
//
// schema.DecodeYAMLInstance, NOT a plain yaml.Unmarshal into `any`, and the
// difference is not stylistic: a manifest carries `joined: 2026-07-28`, and
// yaml.v3 auto-types an unquoted date as a time.Time — which the JSON-schema
// validator cannot represent, so it aborts with "unmapped schema-class keyword
// … InvalidJsonValue" instead of validating anything. Caught by this package's
// own test against the FIXTURE manifest before it could reach a real space; the
// failure read as schema/registry drift and would have sent the next reader
// hunting in the corpus.
func decodeManifest(raw []byte) (instance any, probe manifestProbe, ok bool) {
	decoded, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		return nil, manifestProbe{}, false
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil, manifestProbe{}, false
	}
	return decoded, probe, true
}

// malformedManifestViolation is the space.yaml twin of
// malformedConsumesViolation, and reuses POL-002 for the same reason: it is the
// registry's one "this document is not valid YAML" code. Its title names
// frontmatter because artifacts were the only YAML the engine saw when the
// registry was authored; the substance is identical.
func malformedManifestViolation() Violation {
	return Violation{
		Code:     "POL-002",
		Class:    ClassPolicy,
		Path:     "",
		Message:  "space.yaml is not valid YAML",
		CCRef:    "CC-001",
		Severity: SeverityReject,
	}
}

// checkManifestPolicy is the one canonical authority-map policy check. REF-013
// deliberately groups the invariant as one stable machine code: every branch
// has the same consequence — the map is ambiguous and therefore cannot
// authorize a write — while Path and Message identify the repair.
func checkManifestPolicy(probe manifestProbe) []Violation {
	var violations []Violation
	systems := make(map[string]int, len(probe.Participants))
	sections := make(map[string]int, len(probe.Participants))
	activeOwners := make(map[string]string)

	for i, participant := range probe.Participants {
		base := "participants." + strconv.Itoa(i)
		system := strings.TrimSpace(participant.System)
		if system == "" {
			violations = append(violations, manifestAuthorityViolation(base+".system", "participant system must be non-empty"))
		} else if prior, exists := systems[system]; exists {
			violations = append(violations, manifestAuthorityViolation(
				base+".system",
				"participant system duplicates participants."+strconv.Itoa(prior)+".system",
			))
		} else {
			systems[system] = i
		}

		section, clean := cleanTopLevelSection(participant.Section)
		if !clean {
			violations = append(violations, manifestAuthorityViolation(
				base+".section",
				"participant section must be one clean relative top-level directory",
			))
		} else if prior, exists := sections[section]; exists {
			violations = append(violations, manifestAuthorityViolation(
				base+".section",
				"participant section overlaps participants."+strconv.Itoa(prior)+".section",
			))
		} else {
			sections[section] = i
		}

		// A participant that left remains historical manifest data, but it
		// grants no current authority and its owners are not considered in
		// the active login map.
		if participant.Status != "active" {
			continue
		}
		if len(participant.Owners) == 0 {
			violations = append(violations, manifestAuthorityViolation(
				base+".owners",
				"active participant must name at least one owner",
			))
		}
		for ownerIndex, rawOwner := range participant.Owners {
			owner := strings.TrimSpace(rawOwner)
			ownerPath := base + ".owners." + strconv.Itoa(ownerIndex)
			if owner == "" {
				violations = append(violations, manifestAuthorityViolation(ownerPath, "active owner login must be non-empty"))
				continue
			}
			if priorSystem, exists := activeOwners[owner]; exists && priorSystem != system {
				violations = append(violations, manifestAuthorityViolation(
					ownerPath,
					"active owner login is already assigned to system "+priorSystem,
				))
				continue
			}
			activeOwners[owner] = system
		}
	}
	violations = append(violations, checkNotificationRoutes(probe)...)
	return violations
}

// maxNotificationRoutes is the array's own maxItems floor, moved here from
// space.schema.json's now-restored (frozen) bytes per space-notify-2026-08
// P1 §7.
const maxNotificationRoutes = 16

// knownRouteFields is the closed key set a notification_routes[] entry may
// carry, moved here from the route object's additionalProperties:false
// (space-notify-2026-08 P1 §7) — an unknown key is a typo, and a typo in a
// notification route is silence.
var knownRouteFields = map[string]bool{
	"channel":  true,
	"chat":     true,
	"topic":    true,
	"for":      true,
	"events":   true,
	"locale":   true,
	"secret":   true,
	"renderer": true,
}

// chatPattern and secretPattern are moved verbatim from
// space.schema.json's frozen `chat`/`secret` patterns (P1 §7).
var (
	chatPattern   = regexp.MustCompile(`^-?[0-9]+$`)
	secretPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// validNotificationEvent mirrors the frozen schema's `events[]` item enum
// (P1 §7), bound to §11.3's chat rows plus the `published` widening.
func validNotificationEvent(v string) bool {
	switch v {
	case "human-gate", "blocking", "published":
		return true
	}
	return false
}

// checkNotificationRoutes is space-notify-2026-08 P1's full route
// validation: the shape half the frozen schema.json used to own
// (additionalProperties:false, required, enum, pattern, minItems/
// uniqueItems, maxItems — POL-021, ClassPolicy, one code per the same
// grouping argument REF-013/REF-022 already make: every shape branch has
// the same consequence, a route this package cannot trust to even BE a
// route) plus rules 1-2 (rule 3 is a no-op by design — `events: [published]`
// is legal widening and gets no code; rule 4 lives beside the raw bytes in
// ValidateManifest / ValidateManifestPolicy, not here). Rules 1-2 keep
// their own code, REF-022 — a shape violation has no referent, so folding
// it into REF-022 would make that code's own registered title
// ("not well-formed against its own manifest CONTEXT") false.
func checkNotificationRoutes(probe manifestProbe) []Violation {
	var violations []Violation

	var rawRoutes []any
	switch v := probe.NotificationRoutes.(type) {
	case nil:
		// Absent: schema default `[]`, nothing to check.
	case []any:
		rawRoutes = v
	default:
		violations = append(violations, routeShapeViolation(
			"notification_routes", "notification_routes must be an array",
		))
	}

	if len(rawRoutes) > maxNotificationRoutes {
		violations = append(violations, routeShapeViolation(
			"notification_routes",
			fmt.Sprintf("at most %d notification routes are allowed, found %d", maxNotificationRoutes, len(rawRoutes)),
		))
	}

	participantSystems := make(map[string]bool, len(probe.Participants))
	for _, participant := range probe.Participants {
		if system := strings.TrimSpace(participant.System); system != "" {
			participantSystems[system] = true
		}
	}

	parsed := make([]manifestRouteParsed, len(rawRoutes))
	for i, raw := range rawRoutes {
		base := "notification_routes." + strconv.Itoa(i)
		route, ok := raw.(map[string]any)
		if !ok {
			violations = append(violations, routeShapeViolation(base, "notification route must be a mapping"))
			continue
		}
		shapeViolations, p := checkRouteShape(base, route)
		violations = append(violations, shapeViolations...)
		parsed[i] = p
	}

	seenTuples := make(map[string]int, len(parsed))
	for i, route := range parsed {
		if !route.ok {
			continue
		}
		base := "notification_routes." + strconv.Itoa(i)

		if route.For != "" && !participantSystems[route.For] {
			violations = append(violations, notificationRouteViolation(
				base+".for",
				"route `for` names a participant absent from participants[]",
			))
		}

		topicKey := ""
		if route.Topic != nil {
			topicKey = strconv.Itoa(*route.Topic)
		}
		tuple := route.Channel + "\x00" + route.Chat + "\x00" + topicKey + "\x00" + route.For
		if prior, exists := seenTuples[tuple]; exists {
			violations = append(violations, notificationRouteViolation(
				base,
				"route duplicates notification_routes."+strconv.Itoa(prior)+"'s (channel, chat, topic, for) tuple",
			))
		} else {
			seenTuples[tuple] = i
		}
	}
	return violations
}

// checkRouteShape validates one decoded route mapping against every rule
// space.schema.json's frozen notification_routes item declaration used to
// enforce (P1 §7), returning both the violations and a best-effort typed
// projection for the referential rules in checkNotificationRoutes above.
// The projection is built from whatever DID validate — a route with a
// malformed `topic` still contributes its (valid) `for` to the dedup rule,
// the same way a schema-shaped route always could.
func checkRouteShape(base string, route map[string]any) ([]Violation, manifestRouteParsed) {
	var violations []Violation
	parsed := manifestRouteParsed{ok: true}

	for key := range route {
		if !knownRouteFields[key] {
			violations = append(violations, routeShapeViolation(base+"."+key, "unknown notification route field"))
		}
	}

	if channelRaw, present := route["channel"]; !present {
		violations = append(violations, routeShapeViolation(base+".channel", "channel is required"))
	} else if channel, ok := channelRaw.(string); !ok || channel != "telegram" {
		violations = append(violations, routeShapeViolation(base+".channel", `channel must be "telegram"`))
	} else {
		parsed.Channel = channel
	}

	if chatRaw, present := route["chat"]; !present {
		violations = append(violations, routeShapeViolation(base+".chat", "chat is required"))
	} else if chat, ok := chatRaw.(string); !ok {
		violations = append(violations, routeShapeViolation(base+".chat", "chat must be a string matching ^-?[0-9]+$"))
	} else if !chatPattern.MatchString(chat) {
		violations = append(violations, routeShapeViolation(base+".chat", "chat must match ^-?[0-9]+$"))
	} else {
		parsed.Chat = chat
	}

	if topicRaw, present := route["topic"]; present {
		topic, ok := topicRaw.(int)
		if !ok || topic < 1 {
			violations = append(violations, routeShapeViolation(base+".topic", "topic must be an integer >= 1"))
		} else {
			parsed.Topic = &topic
		}
	}

	if forRaw, present := route["for"]; present {
		forVal, ok := forRaw.(string)
		if !ok {
			violations = append(violations, routeShapeViolation(base+".for", "for must be a string"))
		} else {
			parsed.For = forVal
		}
	}

	if eventsRaw, present := route["events"]; !present {
		violations = append(violations, routeShapeViolation(base+".events", "events is required"))
	} else if events, ok := eventsRaw.([]any); !ok {
		violations = append(violations, routeShapeViolation(base+".events", "events must be an array"))
	} else if len(events) < 1 {
		violations = append(violations, routeShapeViolation(base+".events", "events must contain at least 1 item"))
	} else {
		seen := make(map[string]bool, len(events))
		for j, evRaw := range events {
			evPath := base + ".events." + strconv.Itoa(j)
			ev, ok := evRaw.(string)
			switch {
			case !ok || !validNotificationEvent(ev):
				violations = append(violations, routeShapeViolation(evPath, "event must be one of human-gate, blocking, published"))
			case seen[ev]:
				violations = append(violations, routeShapeViolation(evPath, `duplicate event "`+ev+`"`))
			default:
				seen[ev] = true
			}
		}
	}

	if localeRaw, present := route["locale"]; present {
		locale, ok := localeRaw.(string)
		if !ok || (locale != "ru" && locale != "en") {
			violations = append(violations, routeShapeViolation(base+".locale", `locale must be "ru" or "en"`))
		}
	}

	if secretRaw, present := route["secret"]; present {
		secret, ok := secretRaw.(string)
		if !ok || !secretPattern.MatchString(secret) {
			violations = append(violations, routeShapeViolation(base+".secret", "secret must match ^[A-Z][A-Z0-9_]*$"))
		}
	}

	if rendererRaw, present := route["renderer"]; present {
		renderer, ok := rendererRaw.(string)
		if !ok || (renderer != "html" && renderer != "rich") {
			violations = append(violations, routeShapeViolation(base+".renderer", `renderer must be "html" or "rich"`))
		}
	}

	return violations, parsed
}

func notificationRouteViolation(path, message string) Violation {
	return Violation{
		Code:     "REF-022",
		Class:    ClassReferential,
		Path:     path,
		Message:  message,
		Severity: SeverityReject,
	}
}

// routeShapeViolation is POL-021: a notification_routes[] entry is not
// well-formed against its own declared shape (space-notify-2026-08 P1 §7)
// — an unknown key, a missing required field, a value outside its enum or
// pattern, or the array exceeding its own maxItems. Class is ClassPolicy,
// not ClassReferential: REF-022 above means "well-formed, but does not
// resolve against the manifest's own context (participants[], sibling
// routes)"; these checks have no referent at all; a route can be malformed
// in this way with an empty, single-participant manifest. ClassSchema is
// not used either — that class is reserved for internal/schema's own JSON-
// Schema corpus mapper (schema_class.go), and this rule is hand-written Go
// checking a shape the frozen schema.json can no longer declare, the same
// reason malformedManifestViolation (POL-002) above is ClassPolicy despite
// also being, in substance, a shape fact.
func routeShapeViolation(path, message string) Violation {
	return Violation{
		Code:     "POL-021",
		Class:    ClassPolicy,
		Path:     path,
		Message:  message,
		Severity: SeverityReject,
	}
}

func cleanTopLevelSection(raw string) (string, bool) {
	if raw == "" || strings.Contains(raw, `\`) {
		return "", false
	}
	trimmed := strings.TrimSuffix(raw, "/")
	if trimmed == "" || strings.HasSuffix(trimmed, "/") || path.IsAbs(trimmed) || strings.Contains(trimmed, "/") {
		return "", false
	}
	cleaned := path.Clean(trimmed)
	return cleaned, cleaned == trimmed && cleaned != "." && cleaned != ".."
}

func manifestAuthorityViolation(path, message string) Violation {
	return Violation{
		Code:     "REF-013",
		Class:    ClassReferential,
		Path:     path,
		Message:  message,
		Severity: SeverityReject,
	}
}
