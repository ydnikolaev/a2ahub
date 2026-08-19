package cache

// registered_consumers.go is P4 wave 5's ONE home (decision 8,
// 04-per-version-lifecycle.plan.md) for the D-022 union: every system with
// a `satisfied` requirement whose `target_contract` names a contract, OR a
// `consumes.yaml` entry naming it. internal/cli/cmd_contract.go and
// internal/mcp/tools_contract.go used to carry this verbatim, each its own
// copy — a mirror read, and this package already owns exactly that kind of
// scan (resolver_index.go's own doc comment: "before this, both
// internal/cli/adapters.go and internal/mcp/adapters.go carried their OWN
// copy of this walk").
//
// Edge 1 (04-per-version-lifecycle.md §4): a consumer registered on a
// DIFFERENT major must not block retiring this line forever.
// consumes.yaml's own `major` field (space.Dependency.Major) already
// carries the fact this filter needs — no registry schema change, no
// migration. The satisfied-requirement half of the union carries NO
// version at all (envelope/v1/requirement.schema.json's target_contract is
// a bare "^XC-" id, nothing more), so it is deliberately NOT filtered by
// major: every satisfied requirement counts against every major. A
// consumer whose registration cannot be versioned must never be silently
// dropped from a gate that exists to protect it.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// requirementProbe is this file's own minimal decode of a requirement's
// frontmatter (ISP idiom — see decode.go's own doc comment): just the
// fields this scan needs.
//
// `To` is here because of a defect both former per-surface copies carried
// and neither noticed. They built the fold envelope from {ID, Kind, From}
// only, and a requirement reaches `satisfied` via
// published --acknowledge(RoleTarget)--> acknowledged --satisfy--> satisfied.
// RoleTarget resolves to `env.To0()`, so with To empty the acknowledge was
// flagged unauthorized and never applied, satisfy from `published` had no
// row, and the requirement STOPPED AT `published` for every real history.
//
// The consequence was not cosmetic: the satisfied-requirement half of the
// D-022 consumer union could never fire, so a consumer who registered by
// filing a requirement never blocked `contract retire`. A gate written to
// fail closed was failing open down one of its two branches, silently,
// because the branch simply never evaluated true. Verified by folding both
// envelope shapes: without To the state ends `published` with two flags;
// with To it ends `satisfied` with none.
type requirementProbe struct {
	ID             string     `yaml:"id"`
	From           string     `yaml:"from"`
	To             envelopeTo `yaml:"to"`
	TargetContract string     `yaml:"target_contract"`
}

// envelopeTo decodes the base envelope's `to`, which the schema allows in
// two shapes (`schemas/envelope/v1/base.schema.json`): a non-empty array
// of system ids, or the scalar `all` for a broadcast. A plain []string
// field fails to decode the scalar form, which would drop the whole probe
// and silently re-create the hole this type exists to close — one shape of
// data disappearing from a gate is exactly the failure being fixed here.
type envelopeTo []string

// UnmarshalYAML accepts both shapes. `all` is carried through as a literal
// single entry rather than expanded: no system is named `all`, so a
// broadcast-addressed requirement still never resolves an acknowledge —
// the same answer as before this fix, reached deliberately instead of by
// a decode failure.
func (t *envelopeTo) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*t = envelopeTo{s}
		return nil
	}
	var xs []string
	if err := n.Decode(&xs); err != nil {
		return err
	}
	*t = xs
	return nil
}

// myDependencyContracts returns the set of contract ids ownSystem declares
// in its OWN consumes.yaml under dir, for P4's Edge 3. The second return
// is a SkippedFile to report when the registry exists but cannot be read.
//
// The failure direction here is the OPPOSITE of the retire gate's, and the
// asymmetry is deliberate rather than an inconsistency. On the gate,
// "I cannot read this registry" must fail CLOSED: it means "I cannot prove
// nobody depends on this contract", and retiring anyway destroys someone
// else's build. Here it means "I may be showing you LESS than I should",
// and refusing to render an inbox because one file is malformed is worse
// than the condition — so it degrades to the announcement's own `to:` and
// SAYS SO through the same skip channel skipped.go exists for. Silence is
// the one option neither direction allows.
//
// An absent consumes.yaml is not a skip: a system that consumes nothing is
// the ordinary case, not a degradation.
func myDependencyContracts(dir, ownSystem string) (map[string]bool, *SkippedFile) {
	out := map[string]bool{}
	if ownSystem == "" {
		return out, nil
	}
	rel := path.Join(ownSystem, "consumes.yaml")
	full := filepath.Join(dir, ownSystem, "consumes.yaml")
	raw, err := readBounded(full, maxCacheReadBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, &SkippedFile{Path: rel, Reason: SkipReasonUnreadable}
	}
	registry, cerr := parseConsumesStrict(raw, rel)
	if cerr != nil {
		return out, &SkippedFile{Path: rel, Reason: SkipReasonUndecodableYAML}
	}
	for _, d := range registry.Dependencies {
		if d.Contract != "" {
			out[d.Contract] = true
		}
	}
	return out, nil
}

// FindRegisteredConsumers returns every registered consumer of contractID,
// UNSCOPED by major — this is contractDeprecateAddressees' own query (WHO a
// deprecation announcement is addressed to; Edge 1 scopes the RETIRE gate
// only, spec 04 §4, so a deprecation's addressee set is unchanged). See
// FindRegisteredConsumersForMajor for the version-scoped variant.
func FindRegisteredConsumers(mirrorDir, contractID string) (map[string]bool, error) {
	return findRegisteredConsumers(mirrorDir, contractID, -1)
}

// FindRegisteredConsumersForMajor is the same D-022 union, scoped to major
// (Edge 1): a consumes.yaml dependency counts only when its own `major`
// equals major; a satisfied requirement counts regardless of major (see
// this file's own doc comment for why).
func FindRegisteredConsumersForMajor(mirrorDir, contractID string, major int) (map[string]bool, error) {
	return findRegisteredConsumers(mirrorDir, contractID, major)
}

func findRegisteredConsumers(mirrorDir, contractID string, major int) (map[string]bool, error) {
	out := map[string]bool{}

	reqMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "requires", "XR-*.md"))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	var matched []requirementProbe
	for _, m := range reqMatches {
		raw, rerr := readBounded(m, maxCacheReadBytes)
		if rerr != nil {
			return nil, fmt.Errorf("cache: %w", rerr)
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			continue
		}
		var probe requirementProbe
		if yaml.Unmarshal(fm.YAML, &probe) != nil || probe.TargetContract != contractID {
			continue
		}
		matched = append(matched, probe)
	}
	if len(matched) > 0 {
		// Determine each matching requirement's OWN folded state — this
		// read-only resolution is membership-agnostic (Fold's zero-events
		// fallback / table lookup does not need it; only authorization
		// checks would), so every requirement's actor resolves as a member
		// regardless of the real manifest, same as both former per-surface
		// copies.
		events, _, werr := walkEvents(mirrorDir)
		if werr != nil {
			return nil, fmt.Errorf("cache: %w", werr)
		}
		eventsBySubject := map[string][]fold.Event{}
		for _, re := range events {
			eventsBySubject[re.Ev.Subject] = append(eventsBySubject[re.Ev.Subject], fold.Event{
				ULID: re.Ev.Event, Subject: re.Ev.Subject, Transition: re.Ev.Transition,
				ClaimedState: fold.State(re.Ev.State),
				Actor:        fold.Actor{Kind: re.Ev.Actor.Kind, Name: re.Ev.Actor.Name, System: re.Ev.Actor.System},
				Version:      canonicalEventVersion(re.Ev.Version),
			})
		}
		for _, probe := range matched {
			var state fold.State
			evs := eventsBySubject[probe.ID]
			if len(evs) == 0 {
				state = fold.NewResult(fold.KindRequirement).State
			} else {
				// To is threaded through — see requirementProbe's own doc
				// comment for what omitting it silently cost.
				state = fold.Fold(fold.KindRequirement, fold.Envelope{ID: probe.ID, Kind: fold.KindRequirement, From: probe.From, To: probe.To}, evs, func(string) fold.MembershipStatus { return fold.MembershipMember }).State
			}
			if state == fold.StateSatisfied {
				out[probe.From] = true
			}
		}
	}

	consumesMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "consumes.yaml"))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	for _, m := range consumesMatches {
		raw, rerr := readBounded(m, maxCacheReadBytes)
		if rerr != nil {
			return nil, fmt.Errorf("cache: %w", rerr)
		}
		registry, cerr := parseConsumesStrict(raw, m)
		if cerr != nil {
			// FAIL CLOSED. This function's output is the retire
			// precondition's consumer list: "I could not read this
			// registry" must never round down to "this system consumes
			// nothing", or a contract gets retired out from under a system
			// that is subscribed to it.
			return nil, cerr
		}
		for _, d := range registry.Dependencies {
			if d.Contract != contractID {
				continue
			}
			if major >= 0 && d.Major != major {
				continue
			}
			out[registry.System] = true
		}
	}
	return out, nil
}

// contractOperationalDebtOwed is P5 AC1's floor-gated derivation (spec
// 05-declared-nature.md §8 AC1: "the derivation is GATED ON THE SPACE'S
// OWN FLOOR — below it, no derived row is emitted and the thread reads
// exactly as it does today"), resolved here so
// pendency.Input.OperationalDebtOwed carries a single caller-resolved
// bool: true when (a) manifest's own min_binary_version is at or above
// contract.ContractPublicationFloor — the same floor split every
// event/v2 write already mirrors (internal/cli/cmd_lifecycle.go's
// lifecycleEventSchema, which `contract activate` itself reuses) — AND
// (b) at least one system is a REGISTERED consumer
// (FindRegisteredConsumersForMajor, scoped to publishVersion's own
// major) of contractID.
//
// The fact derives from registration and publication ALONE: it never
// reads a contract descriptor's `x_operational[]`, so it is exactly as
// true for a contract that never declared the field as for one
// declaring an item `state: absent` — spec 05's P-1 constraint ("an
// undeclared operational half ... is itself the debt"; §6's own testing
// row: the derivation must "pass with every new field absent").
//
// Fails CLOSED on the floor: an unparseable/empty MinBinaryVersion or
// publishVersion degrades to "no derived row", the same direction §8
// AC1 names for a space below the floor, never to "assume above it".
// Fails OPEN on the registry read: a consumes.yaml this package cannot
// parse is treated as "no registered consumer found" here rather than
// aborting the whole read model — deliberately the OPPOSITE of
// FindRegisteredConsumers' own fail-closed contract (that function's own
// doc comment), because that contract protects the retire gate from
// destroying a dependent out from under it, and the failure mode here is
// only ever "a derived row this best-effort read path could not
// surface", the same fail-open discipline every other caller-resolved
// fact in foldedArtifact documents.
func contractOperationalDebtOwed(mirrorDir string, manifest space.Manifest, contractID, publishVersion string) bool {
	belowFloor, ferr := version.OlderThan(manifest.MinBinaryVersion, contract.ContractPublicationFloor)
	if ferr != nil || belowFloor {
		return false
	}
	major, merr := version.Major(publishVersion)
	if merr != nil {
		return false
	}
	consumers, cerr := FindRegisteredConsumersForMajor(mirrorDir, contractID, major)
	if cerr != nil {
		return false
	}
	return len(consumers) > 0
}

// parseConsumesStrict parses a committed consumes.yaml and REFUSES anything
// that is not a real consumes/v1 registry — a placeholder `consumes: []`
// file unmarshals cleanly into a zero-valued struct, indistinguishable from
// a system that genuinely consumes nothing, unless schema/system are
// checked explicitly.
func parseConsumesStrict(raw []byte, path string) (space.Consumes, error) {
	registry, err := space.ParseConsumes(raw)
	if err != nil {
		return space.Consumes{}, fmt.Errorf("cache: %s is not valid yaml: %w", path, err)
	}
	if registry.Schema != "consumes/v1" || registry.System == "" {
		return space.Consumes{}, fmt.Errorf(
			"cache: %s is not a consumes/v1 registry (needs `schema: consumes/v1`, `system: <id>`, `dependencies: [...]`) — "+
				"refusing to treat it as \"no registered consumers\"; fix the file (or write it with `a2a contract adopt`)", path)
	}
	return registry, nil
}

// UnadoptedConsumption is one contract ownSystem has verify-passed
// deliveries against without ever listing in its own consumes.yaml — see
// FindUnadoptedConsumption's own doc comment. Count is the number of
// DISTINCT resolved data-package ids (doc.ID) conforming to ContractID,
// never the number of deliverable OCCURRENCES: the same package referenced
// by more than one handoff (a resubmission, a broadcast to several
// targets) must not inflate the count past the number of packages a
// reader would actually have to look at.
type UnadoptedConsumption struct {
	ContractID string
	Count      int
}

// FindUnadoptedConsumption is FindRegisteredConsumers' own OPPOSITE
// direction (spec 06 §5's anti-duplication bullet: "the mirror-wide walk
// exists; this phase adds the opposite direction — deliveries I ACCEPTED
// vs contracts I DECLARED"), computed for a new WARN-only `a2a doctor`
// advisory (defects-fix-2026-08 P6, docs/inbox/defects/07-the-consumer-
// registered-nowhere.md). Detection only — see the paragraph below on what
// this deliberately does NOT decide.
//
// It scans every handoff addressed to ownSystem (handoff.schema.json's own
// `to` is exactly one target system) that has folded to fold.StateAccepted.
// That state is a valid proxy for "ownSystem verify-passed this delivery"
// without re-deriving the transition by hand: §3.4.5's handoffRows table
// (internal/fold/table.go) has exactly ONE row landing on StateAccepted —
// `acknowledged --verify-pass--> accepted`, RoleTarget — so a handoff
// folding to StateAccepted is, by construction, one ownSystem itself
// verify-passed (RoleTarget resolves to the envelope's own To0(), the same
// resolution requirementProbe's own doc comment above documents at length
// for the satisfied-requirement half of this file's D-022 union). The
// handoff's own fold state is the verify-pass signal used here — never
// report.json (datapackage.Report answers a DIFFERENT question, "did the
// content pass its checks", not "did the target accept the delivery").
//
// For each accepted handoff it resolves every "data" deliverable
// (datapackage.DeliverableKindData; the other four kinds — code, contract,
// config, doc — carry no data-package/v1 manifest to resolve, exactly
// delivery.go's own ResolveDeliveries scope) against that manifest via the
// same filesystem-backed resolver packageresolver.go already owns
// (mirrorPackageResolver.ResolvePackage; participants is nil because
// ResolvePackage never reads it — the producing system is parsed out of
// the ref itself, see that method's own doc comment), and reports every
// distinct contract id the manifest PINS (Document.Contract, already a
// decoded Go field — no second YAML/JSON parse of the manifest here) that
// is NEITHER already in ownSystem's own consumes.yaml (myDependencyContracts,
// this file) NOR published by ownSystem itself. A producer is never a
// consumer of its own contract: a contract id is ClassStanding
// (<PREFIX>-<system>-<slug>, internal/artifact/id.go), so
// artifact.ParseID(id).System names the producer directly, with no second
// registry to consult.
//
// This function answers Piece 1 ONLY (spec 06's own "Two pieces, and only
// the first is uncontroversial"): detection, never registration. Nothing
// it returns is written anywhere, and internal/validate/policy_retire.go
// (D-022's retire precondition) does not call it and does not change —
// that gate stays scoped to DECLARED registrations, by design, and this
// phase does not touch it. Piece 2 (should a verify-passed delivery
// auto-register?) is the epic's own open question, not decided here.
//
// The returned *SkippedFile mirrors myDependencyContracts' own asymmetry
// (that function's own doc comment): a consumes.yaml that EXISTS but
// cannot be read as a real consumes/v1 registry must not silently degrade
// to "empty declared set" here, because that would report every ALREADY-
// adopted contract as unadopted a second time over — a worse false
// advisory than simply not running. A non-nil skip means "render an
// unverified-style line instead of a contract list", never both; an
// ABSENT consumes.yaml (the live case: a real `consumes/v1` file with
// `dependencies: []`) is the ordinary case and returns a nil skip.
//
// Best-effort otherwise, unlike findRegisteredConsumers' fail-closed
// contract: one unreadable handoff, unresolvable manifest, or undecodable
// deliverables[] block in a large mirror is silently excluded from the
// count rather than aborting the whole scan or failing `a2a doctor` —
// this is a WARN-only advisory, never a gate (spec 06 T1: "consuming
// without adopting is a real risk and not an error"), and an operator is
// better served by an undercount than by no advisory at all when one file
// in a large mirror is malformed.
func FindUnadoptedConsumption(mirrorDir, ownSystem string) ([]UnadoptedConsumption, *SkippedFile, error) {
	if ownSystem == "" {
		return nil, nil, nil
	}
	myDependencies, depSkip := myDependencyContracts(mirrorDir, ownSystem)
	if depSkip != nil {
		return nil, depSkip, nil
	}

	artifacts, _, err := walkArtifacts(mirrorDir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: %w", err)
	}
	events, _, err := walkEvents(mirrorDir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: %w", err)
	}
	eventsBySubject := map[string][]fold.Event{}
	for _, re := range events {
		eventsBySubject[re.Ev.Subject] = append(eventsBySubject[re.Ev.Subject], fold.Event{
			ULID: re.Ev.Event, Subject: re.Ev.Subject, Transition: re.Ev.Transition,
			ClaimedState: fold.State(re.Ev.State),
			Actor:        fold.Actor{Kind: re.Ev.Actor.Kind, Name: re.Ev.Actor.Name, System: re.Ev.Actor.System},
			Version:      canonicalEventVersion(re.Ev.Version),
		})
	}

	resolver := newMirrorPackageResolver(mirrorDir, nil)

	// packagesByContract dedups by resolved package id (doc.ID), never by
	// deliverable occurrence — see UnadoptedConsumption's own doc comment.
	packagesByContract := map[string]map[string]bool{}
	for _, a := range artifacts {
		if a.Env.Type != string(fold.KindHandoff) {
			continue
		}
		to := normalizeTo(a.Env.To)
		if !containsString(to, ownSystem) {
			continue
		}
		// A naive subject==id-only query is the wrong default in this
		// package (buildIndex's own doc comment, mirror.go: gatherEvents
		// merges response-attached events into a PARENT's stream because a
		// question/work_request's verify/dispute are committed under the
		// RESPONSE's own subject, not the parent's). That merge is scoped
		// to exactly fold.TVerify/fold.TDispute (gatherEvents, mirror.go) —
		// the D-024 closure-model transitions on a RESPONSE. A handoff's
		// own verify-pass/verify-fail (fold.TVerifyPass/TVerifyFail,
		// table.go's handoffRows) are committed directly under the
		// HANDOFF's own subject: handoffRows carries no response/parent
		// relationship at all, so there is no second stream to merge in
		// here — the direct eventsBySubject[a.Env.ID] lookup below is
		// exactly what findRegisteredConsumers' own requirement fold above
		// already does for the same reason (a requirement has no response
		// wrapper either).
		evs := eventsBySubject[a.Env.ID]
		if len(evs) == 0 {
			continue
		}
		// Membership-agnostic, same as findRegisteredConsumers' own
		// requirement fold above: this read-only resolution never grants
		// write authority, only reads what state the real events already
		// folded to.
		state := fold.Fold(fold.KindHandoff, fold.Envelope{ID: a.Env.ID, Kind: fold.KindHandoff, From: a.Env.From, To: to},
			evs, func(string) fold.MembershipStatus { return fold.MembershipMember }).State
		if state != fold.StateAccepted {
			continue
		}

		deliverables, derr := datapackage.DecodeDeliverables(a.Raw)
		if derr != nil {
			continue
		}
		for _, d := range deliverables {
			if d.Kind != datapackage.DeliverableKindData {
				continue
			}
			doc, ok := resolver.ResolvePackage(d.Ref)
			if !ok {
				continue
			}
			contractID, _ := splitPinnedContractID(doc.Contract)
			if contractID == "" || myDependencies[contractID] {
				continue
			}
			if parsed, perr := artifact.ParseID(contractID); perr == nil && parsed.System == ownSystem {
				continue
			}
			if packagesByContract[contractID] == nil {
				packagesByContract[contractID] = map[string]bool{}
			}
			packagesByContract[contractID][doc.ID] = true
		}
	}

	if len(packagesByContract) == 0 {
		return nil, nil, nil
	}
	out := make([]UnadoptedConsumption, 0, len(packagesByContract))
	for id, packages := range packagesByContract {
		out = append(out, UnadoptedConsumption{ContractID: id, Count: len(packages)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ContractID < out[j].ContractID })
	return out, nil, nil
}

// splitPinnedContractID returns the bare contract id from a data-package/v1
// manifest's own `contract` field ("<XC-id>@<version>#<digest>",
// data-package.schema.json's pinnedContractRef). This is the same literal
// split internal/space's own splitDataContractReference (data_resolve.go),
// internal/cli/cmd_contract_p6.go and internal/mcp/tools_contract_p6.go
// each already perform independently — internal/cache may not import
// internal/cli or internal/mcp (ADR-001), and internal/space's own copy is
// unexported to its package, so this is a fourth local copy of the SAME
// shape those three already carry, never a new one — the same recorded
// idiom this file's own dataPackagePrefix-adjacent duplication already
// documents elsewhere in this package (packageresolver.go). It performs no
// validation (that is verify's job, already done before this document was
// ever committed) — only the split this caller needs.
func splitPinnedContractID(ref string) (id, version string) {
	body, _, _ := strings.Cut(ref, "#")
	id, version, _ = strings.Cut(body, "@")
	return id, version
}
