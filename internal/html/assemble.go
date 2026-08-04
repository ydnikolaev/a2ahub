package html

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"gopkg.in/yaml.v3"
)

// Assemble builds the dashboard Data from a composed Store, as of now. self is
// the ego system (the --system override); empty falls back to the Store's own
// configured system. It reads only (Store already composed the mirrors);
// consumes.yaml is walked per space mirror to derive contract-dependency edges.
// No network.
func Assemble(ctx context.Context, store *cache.Store, self string, now time.Time) (Data, error) {
	return AssembleWithContractHistory(ctx, store, self, now, nil)
}

// AssembleWithContractHistory is the non-operational entry point with the
// canonical historical validator supplied by the composition root.
func AssembleWithContractHistory(ctx context.Context, store *cache.Store, self string, now time.Time, historyValidator space.ContractHistoryDocumentValidator) (Data, error) {
	empty := operational.Snapshot{
		SchemaVersion: operational.SchemaVersion, GeneratedAt: now.UTC(),
		Sources: []operational.Source{}, Timeline: []operational.TimelineRow{},
		TimelineWindow: operational.Window{}, Unavailable: []operational.Unavailable{},
	}
	return AssembleWithOperationalAndContractHistory(ctx, store, self, now, empty, historyValidator)
}

// AssembleWithOperational injects the exact snapshot produced by the shared
// operationalsource adapter. HTML may present these fields, but it never
// rebuilds protocol, work freshness, precedence, consistency or ordering from
// ThreadView.
func AssembleWithOperational(ctx context.Context, store *cache.Store, self string, now time.Time, snapshot operational.Snapshot) (Data, error) {
	return AssembleWithOperationalAndContractHistory(ctx, store, self, now, snapshot, nil)
}

// AssembleWithOperationalAndContractHistory injects both authoritative read
// models needed by the dashboard. The composition root owns the canonical
// historical validator; HTML only consumes it and never builds a second
// schema engine locally.
func AssembleWithOperationalAndContractHistory(ctx context.Context, store *cache.Store, self string, now time.Time, snapshot operational.Snapshot, historyValidator space.ContractHistoryDocumentValidator) (Data, error) {
	if self == "" {
		self = store.OwnSystem()
	}
	mirrors := store.SpaceMirrors()
	d := Data{GeneratedAt: now, Self: self, Nodes: []Node{}, ContractEdges: []ContractEdge{},
		ExchangeEdges: []ExchangeEdge{}, Threads: []Thread{}, Inbox: []Item{}, Outbox: []Item{}, Archive: []Item{}, Contracts: []Contract{},
		Spaces: []SpaceHealth{}, Flags: []Flag{}, ReleaseNotes: []ReleaseNote{}, ThreadViews: []ThreadView{},
		WorkReports: []WorkReport{}, Operational: snapshot, ArtifactDetails: []ArtifactDetail{}, Unavailable: []UnavailableFact{}}

	un := store.UpdateNotice()
	d.Tooling = Tooling{Current: un.Current, Latest: un.Latest, UpdateAvailable: un.UpdateAvailable,
		Required: un.Required, Floor: un.Floor, FloorSpace: un.FloorSpace,
		CheckedAt: un.CheckedAt, Source: un.Source, Fresh: un.Fresh, ReleaseURL: un.ReleaseURL}
	if !un.CheckedAt.IsZero() {
		d.Tooling.CacheAge = humanizeAge(now, un.CheckedAt)
	}
	ud := store.UpdateDetail()
	d.UpdateDetail = UpdateDetail{Status: string(ud.Status), Version: ud.TargetVersion, Changes: []UpdateChange{}}
	if ud.Detail != nil {
		d.UpdateDetail.Headline = ud.Detail.Headline
		for _, change := range ud.Detail.Changes {
			d.UpdateDetail.Changes = append(d.UpdateDetail.Changes, UpdateChange{
				Kind: change.Kind, Impact: change.Impact, Subject: change.Subject,
				Detail: change.Detail, Run: append([]string(nil), change.Action.Run...),
			})
		}
	}

	// Nodes (deduped across spaces) + per-space health.
	syncBySpace := make(map[string]cache.SpaceSyncInfo, len(mirrors))
	for _, fact := range store.SpaceSyncFacts(ctx) {
		syncBySpace[fact.Space] = fact
	}
	nodeIdx := map[string]*Node{}
	for _, m := range mirrors {
		readable := m.Manifest.Space != "" || len(m.Manifest.Participants) > 0
		syncFact := syncBySpace[m.SpaceID]
		workflowVersion, workflowRef := spaceWorkflowVersion(m.Dir)
		syncAge := ""
		if syncFact.Synced {
			syncAge = humanizeAge(now, now.Add(-syncFact.Age))
		}
		d.Spaces = append(d.Spaces, SpaceHealth{
			ID: m.SpaceID, RepoURL: m.RepoURL,
			ParticipantCount: len(m.Manifest.Participants), Readable: readable,
			SyncAge: syncAge, Stale: syncFact.Stale, Revision: syncFact.Revision,
			SchemaVersion: m.Manifest.Schema, MinBinaryVersion: m.Manifest.MinBinaryVersion,
			WorkflowVersion: workflowVersion, WorkflowRef: workflowRef,
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
		if len(n.Owners) > 0 {
			n.Avatar, _ = store.AvatarDataURL(n.Owners[0])
		}
		d.Nodes = append(d.Nodes, *n)
	}
	sort.Slice(d.Nodes, func(i, j int) bool { return d.Nodes[i].System < d.Nodes[j].System })

	// Contracts catalog + a by-id index for drift lookups.
	cinfos, err := store.Contracts(ctx, "")
	if err != nil {
		return Data{}, fmt.Errorf("html: contracts: %w", err)
	}
	type contractKey struct{ space, id string }
	byID := map[contractKey]cache.ContractInfo{}
	for _, c := range cinfos {
		byID[contractKey{space: c.Space, id: c.ID}] = c
	}
	contractMirrors := make(map[string]cache.SpaceMirror, len(mirrors))
	ambiguousMirror := make(map[string]bool)
	for _, mirror := range mirrors {
		if _, duplicate := contractMirrors[mirror.SpaceID]; duplicate {
			delete(contractMirrors, mirror.SpaceID)
			ambiguousMirror[mirror.SpaceID] = true
			continue
		}
		if !ambiguousMirror[mirror.SpaceID] {
			contractMirrors[mirror.SpaceID] = mirror
		}
	}
	// Contract-dependency edges from every space's consumes.yaml, plus the
	// per-contract consumer lists.
	consumersOf := map[contractKey][]string{}
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
				key := contractKey{space: m.SpaceID, id: dep.Contract}
				consumersOf[key] = append(consumersOf[key], p.System)
				edge := ContractEdge{From: p.System, To: provider, Space: m.SpaceID,
					Contract: dep.Contract, PinnedMajor: dep.Major}
				ci, ok := byID[key]
				switch {
				case !ok || nodeStatus(nodeIdx, provider) == "left":
					edge.Drift = "dangling"
				default:
					edge.ProviderVersion = ci.Version
					edge.State = ci.State
					edge.PinnedVersion, edge.PinnedState, edge.Sunset, edge.Successor,
						edge.AvailableMajors, edge.Drift = dependencyFacts(ci, dep.Major)
					edge.Description = ci.Description
				}
				d.ContractEdges = append(d.ContractEdges, edge)
			}
		}
	}
	sort.Slice(d.ContractEdges, func(i, j int) bool {
		if d.ContractEdges[i].Space != d.ContractEdges[j].Space {
			return d.ContractEdges[i].Space < d.ContractEdges[j].Space
		}
		return d.ContractEdges[i].Contract < d.ContractEdges[j].Contract
	})

	// Contracts catalog rows (with derived consumer lists).
	for _, c := range cinfos {
		cons := consumersOf[contractKey{space: c.Space, id: c.ID}]
		sort.Strings(cons)
		details, detailErr := resolveContractVersionDetails(ctx, contractMirrors, c, d.ContractEdges, historyValidator)
		if detailErr != nil {
			return Data{}, detailErr
		}
		vers := make([]ContractVersion, 0, len(c.Versions))
		for _, v := range c.Versions {
			vers = append(vers, ContractVersion{
				Version: v.Version, State: v.State, Sunset: v.Sunset,
				Successor: v.Successor, DeprecationID: v.DeprecationID, Detail: details[v.Version],
			})
		}
		d.Contracts = append(d.Contracts, Contract{Space: c.Space, ID: c.ID, Provider: c.Provider,
			Version: c.Version, State: c.State, Consumers: dedupSorted(cons), Description: c.Description,
			Versions: vers, Category: c.Category, SchemaFormat: c.SchemaFormat,
			CompatPolicy: c.CompatPolicy, GeneratedTool: c.GeneratedTool,
			SourceDigest: c.SourceDigest,
			CodeBacked:   c.GeneratedTool != "" && c.SourceDigest != "",
		})
	}
	limitContractVersionDetails(d.Contracts)

	// Exchange is an active-work surface. The ordinary inbox query also
	// contains terminal history by protocol design, so use the explicitly open
	// passive projection here; completed documents remain fully available in
	// Threads and never masquerade as work "in flight".
	inItems, err := store.OpenInboxSnapshot(ctx)
	if err != nil {
		return Data{}, fmt.Errorf("html: inbox: %w", err)
	}
	outItems, err := store.OutboxSnapshot(ctx)
	if err != nil {
		return Data{}, fmt.Errorf("html: outbox: %w", err)
	}
	archiveItems, err := store.ExchangeArchiveSnapshot(ctx)
	if err != nil {
		return Data{}, fmt.Errorf("html: exchange archive: %w", err)
	}
	// Conversations, including their first turn, with their members and the
	// document-to-document links inside them (spec 46 §T6.1). Built before the
	// rows because it also returns every artifact's open item, and an inbox row
	// carries the legal-move facts its agent prompt is assembled from.
	threads, threadViews, openItems, err := buildThreads(ctx, store, self, now)
	if err != nil {
		return Data{}, fmt.Errorf("html: threads: %w", err)
	}
	d.Threads = threads
	d.ThreadViews = threadViews
	d.WorkReports = collectWorkReports(threadViews)

	for _, it := range inItems {
		d.Inbox = append(d.Inbox, toItem(it, now, self, openItems))
	}
	for _, it := range outItems {
		d.Outbox = append(d.Outbox, toItem(it, now, self, openItems))
	}
	for _, it := range archiveItems {
		item := toItem(it, now, self, openItems)
		item.Archived = true
		// Archive is the Exchange read-model's completed surface. Preserve the
		// protocol state (for example, an announcement remains published), but
		// do not keep presenting a completed document as needing attention.
		item.Severity = "normal"
		d.Archive = append(d.Archive, item)
	}

	// Full detail records for every visible Work row. Resolve the union in one
	// cache pass: ShowMany reuses the canonical folded index, so this does not
	// create a dashboard-only artifact walk or lifecycle projection.
	detailRefs := visibleArtifactRefs(inItems, outItems, archiveItems)
	if len(detailRefs) > 0 {
		details, detailErr := store.ShowMany(ctx, detailRefs)
		if detailErr != nil {
			return Data{}, fmt.Errorf("html: artifact details: %w", detailErr)
		}
		for _, detail := range details {
			projected, projectErr := toArtifactDetail(detail)
			if projectErr != nil {
				return Data{}, fmt.Errorf("html: artifact detail %s Markdown: %w", detail.ID, projectErr)
			}
			attachLinkedArtifactEvents(&projected, threadViews)
			d.ArtifactDetails = append(d.ArtifactDetails, projected)
		}
	}

	// Exchange overlay: aggregate open items (inbox ∪ outbox) per from→to→space.
	d.ExchangeEdges = exchangeEdges(append(append([]cache.Item{}, inItems...), outItems...), now)

	// Read-health facts: committed fold violations plus any mirror files the
	// best-effort index could not decode. Both already exist in cache; the old
	// dashboard model carried Flags but never wired either source.
	protocolFlags, err := store.ProtocolFlags(ctx)
	if err != nil {
		return Data{}, fmt.Errorf("html: protocol flags: %w", err)
	}
	for _, f := range protocolFlags {
		source := f.Source
		if source == "" {
			source = "fold"
		}
		d.Flags = append(d.Flags, Flag{
			Space: f.Space, System: f.System, Code: f.Code,
			Message: f.Message, Severity: f.Severity, Source: source,
			Artifact: f.Artifact, Event: f.EventULID, Consistency: f.Consistency,
		})
	}
	skipped, err := store.AllSkippedFiles(ctx)
	if err != nil {
		return Data{}, fmt.Errorf("html: skipped files: %w", err)
	}
	for spaceID, files := range skipped {
		for _, file := range files {
			d.Flags = append(d.Flags, Flag{
				Space: spaceID, Code: "read-index-skip",
				Message:  file.Path + " (" + file.Reason + ")",
				Severity: "attention", Source: "cache-index",
			})
		}
	}
	sort.Slice(d.Flags, func(i, j int) bool {
		if d.Flags[i].Space != d.Flags[j].Space {
			return d.Flags[i].Space < d.Flags[j].Space
		}
		if d.Flags[i].Code != d.Flags[j].Code {
			return d.Flags[i].Code < d.Flags[j].Code
		}
		return d.Flags[i].Message < d.Flags[j].Message
	})

	// Release notes are already embedded in the binary and parsed by the same
	// internal/notes package as `a2a whatsnew`; the HTML view projects that
	// corpus instead of maintaining a second changelog.
	releases, err := notes.Load(releasenotes.FS)
	if err != nil {
		return Data{}, fmt.Errorf("html: release notes: %w", err)
	}
	currentIssues, err := notes.LoadCurrentKnownIssues(releasenotes.FS)
	if err != nil {
		return Data{}, fmt.Errorf("html: current known issues: %w", err)
	}
	releases = notes.AttachCurrentKnownIssues(releases, releases, currentIssues)
	d.ReleaseNotes = toReleaseNotes(releases)
	d.Unavailable = append(d.Unavailable,
		UnavailableFact{ID: "snapshot-v3-verdict", Scope: "all-spaces", Title: "Whole-snapshot V3 verdict", Reason: "No persisted validation run with timestamp, scope, inputs and codes is mounted in this static HTML read.", SourceClass: "unavailable"},
		UnavailableFact{ID: "binding-byte-match", Scope: "contracts", Title: "Current generated bytes match source digest", Reason: "generatedTool and sourceDigest are binding metadata, not a current verification result.", SourceClass: "unavailable"},
		UnavailableFact{ID: "hosted-hub-state", Scope: "product", Title: "Hosted Hub state", Reason: "The hosted Hub is a future architecture, not a shipped read surface.", SourceClass: "unavailable"},
	)

	return d, nil
}

func resolveContractVersionDetails(
	ctx context.Context,
	mirrors map[string]cache.SpaceMirror,
	info cache.ContractInfo,
	edges []ContractEdge,
	validator space.ContractHistoryDocumentValidator,
) (map[string]*ContractVersionDetail, error) {
	details := make(map[string]*ContractVersionDetail, len(info.Versions))
	setUnavailable := func(reason string) {
		for _, version := range info.Versions {
			details[version.Version] = unavailableContractVersionDetail(reason)
		}
	}
	if validator == nil {
		setUnavailable("The canonical historical package validator is unavailable for this snapshot.")
		return details, nil
	}
	mirror, ok := mirrors[info.Space]
	if !ok {
		setUnavailable("The synchronized mirror for this contract's space is unavailable or ambiguous.")
		return details, nil
	}
	requested := make([]string, 0, len(info.Versions))
	versions := make(map[string]cache.ContractVersion, len(info.Versions))
	for _, version := range info.Versions {
		requested = append(requested, version.Version)
		versions[version.Version] = version
	}
	err := space.VisitContractVersions(ctx, mirror.Dir, info.ID, requested, validator, func(resolution *space.HistoricalResolution) error {
		projectContractResolution(details, versions, edges, info, resolution)
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("html: resolve contract versions %s:%s: %w", info.Space, info.ID, err)
		}
		setUnavailable(contractVersionUnavailableReason(err))
		return details, nil
	}
	for _, version := range info.Versions {
		if details[version.Version] == nil {
			details[version.Version] = unavailableContractVersionDetail("The exact historical package for this contract version was not returned by the canonical resolver.")
		}
	}
	return details, nil
}

func projectContractResolution(
	details map[string]*ContractVersionDetail,
	versions map[string]cache.ContractVersion,
	edges []ContractEdge,
	info cache.ContractInfo,
	resolution *space.HistoricalResolution,
) {
	version, expected := versions[resolution.Version]
	if !expected {
		return
	}
	if resolution.Err != nil {
		details[resolution.Version] = unavailableContractVersionDetail(contractVersionUnavailableReason(resolution.Err))
		return
	}
	if resolution.Snapshot.ContractID != info.ID || resolution.Snapshot.Version != resolution.Version {
		details[resolution.Version] = unavailableContractVersionDetail("Historical resolution returned a different contract version, so no package facts were used.")
		return
	}
	detail, err := projectContractVersionDetail(resolution.Snapshot, contractVersionConsumerPins(edges, info.Space, info.ID, resolution.Version), version)
	if err != nil {
		details[resolution.Version] = unavailableContractVersionDetail("Historical publication evidence for this exact version could not be projected without mixing package facts.")
		return
	}
	details[resolution.Version] = detail
}

func unavailableContractVersionDetail(reason string) *ContractVersionDetail {
	return &ContractVersionDetail{Status: ContractVersionDetailUnavailable, UnavailableReason: reason}
}

func contractVersionUnavailableReason(err error) string {
	switch {
	case errors.Is(err, space.ErrContractNotPublished):
		return "No verified publication was found for this exact contract version on the synchronized authoritative history."
	case errors.Is(err, space.ErrContractObjectUnavailable):
		return "The exact Git objects required for this contract version are unavailable in the synchronized mirror."
	case errors.Is(err, space.ErrContractHistoryInvalid), errors.Is(err, space.ErrContractDigestMismatch):
		return "Historical publication evidence for this exact contract version did not pass canonical validation and digest verification."
	default:
		return "The exact historical package for this contract version could not be verified from the synchronized mirror."
	}
}

type contractVersionDescriptorProjection struct {
	ID            string `yaml:"id"`
	Version       string `yaml:"version"`
	SchemaFormat  string `yaml:"schema_format"`
	CompatPolicy  string `yaml:"compat_policy"`
	GeneratedFrom struct {
		Tool         string `yaml:"tool"`
		SourceDigest string `yaml:"source_digest"`
	} `yaml:"generated_from"`
}

type contractVersionPublishProjection struct {
	Event      string `yaml:"event"`
	Subject    string `yaml:"subject"`
	Transition string `yaml:"transition"`
	Version    string `yaml:"version"`
	At         string `yaml:"at"`
	Actor      struct {
		Name   string `yaml:"name"`
		System string `yaml:"system"`
	} `yaml:"actor"`
}

func projectContractVersionDetail(snapshot space.HistoricalSnapshot, pins []ContractVersionConsumerPin, version cache.ContractVersion) (*ContractVersionDetail, error) {
	descriptorRaw, ok := snapshot.CarriedSet.Bytes[contract.DescriptorPath]
	if !ok && snapshot.DigestProfile == contract.ProfileContractTreeV1 {
		for _, file := range snapshot.Files {
			if file.Path == contract.DescriptorPath {
				descriptorRaw = file.Raw
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("historical descriptor bytes are missing")
	}
	frontmatter, err := artifact.ParseFrontmatter(descriptorRaw)
	if err != nil {
		return nil, fmt.Errorf("parse historical descriptor: %w", err)
	}
	var descriptor contractVersionDescriptorProjection
	if err := yaml.Unmarshal(frontmatter.YAML, &descriptor); err != nil {
		return nil, fmt.Errorf("decode historical descriptor: %w", err)
	}
	if descriptor.ID != snapshot.ContractID || descriptor.Version != snapshot.Version {
		return nil, fmt.Errorf("historical descriptor identity does not match resolved version")
	}
	var published contractVersionPublishProjection
	if err := yaml.Unmarshal(snapshot.PublishEventRaw, &published); err != nil {
		return nil, fmt.Errorf("decode historical publish event: %w", err)
	}
	if published.Subject != snapshot.ContractID || published.Version != snapshot.Version || published.Transition != "publish" || published.Event == "" {
		return nil, fmt.Errorf("historical publish event identity does not match resolved version")
	}
	publishedBy := published.Actor.System
	if publishedBy == "" {
		publishedBy = published.Actor.Name
	}
	descriptorDigest := snapshot.CarriedSet.PerFileDigest[contract.DescriptorPath]
	if descriptorDigest == "" {
		descriptorDigest = artifact.Digest(descriptorRaw)
	}
	documents := make([]ContractVersionDocument, 0, len(snapshot.CarriedSet.Entries)+1)
	documents = append(documents, ContractVersionDocument{
		Path: contract.DescriptorPath, Role: "descriptor", Normative: true,
		MediaType: "text/markdown", Digest: descriptorDigest,
		SizeBytes: int64(len(descriptorRaw)), Preview: contractVersionPreview(descriptorRaw), PreviewLanguage: "markdown",
	})
	if snapshot.DigestProfile == contract.ProfileContractSetV2 && descriptorDigest != snapshot.CarriedSet.PerFileDigest[contract.DescriptorPath] {
		return nil, fmt.Errorf("historical contract-set-v2 descriptor lacks exact digest")
	}
	for _, entry := range snapshot.CarriedSet.Entries {
		raw, exists := snapshot.CarriedSet.Bytes[entry.Path]
		digest := snapshot.CarriedSet.PerFileDigest[entry.Path]
		if !exists || digest == "" {
			return nil, fmt.Errorf("historical carried file %q lacks exact bytes or digest", entry.Path)
		}
		role, normative := contractVersionDocumentRole(snapshot.DigestProfile, entry)
		documents = append(documents, ContractVersionDocument{
			Path: entry.Path, Role: role, Normative: normative,
			MediaType: entry.MediaType, ConformsTo: entry.ConformsTo,
			Digest: digest, SizeBytes: int64(len(raw)), Preview: contractVersionPreview(raw),
			PreviewLanguage: contractVersionPreviewLanguage(entry.Path, entry.MediaType),
		})
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].Path < documents[j].Path })
	provenance := &ContractVersionProvenance{
		Profile: string(snapshot.DigestProfile), CommitSHA: snapshot.CommitSHA, TreeObjectID: snapshot.TreeObjectID,
		DescriptorPath: snapshot.DescriptorPath, PublishEventPath: snapshot.PublishEventPath,
		EventSchema: snapshot.EventSchema, DigestProfile: string(snapshot.DigestProfile),
		PublishedDigest: snapshot.PublishedDigest, AggregateVerification: snapshot.AggregateVerification,
		DescriptorVerification: snapshot.DescriptorVerification,
		GeneratedTool:          descriptor.GeneratedFrom.Tool, SourceDigest: descriptor.GeneratedFrom.SourceDigest,
	}
	return &ContractVersionDetail{
		Status: ContractVersionDetailAvailable, Description: strings.TrimSpace(contractVersionPreview(frontmatter.Body)),
		SchemaFormat: descriptor.SchemaFormat, CompatPolicy: descriptor.CompatPolicy,
		PublishedAt: published.At, PublishedBy: publishedBy, ConsumerPins: pins,
		Documents: documents, History: contractVersionHistory(published, version), Provenance: provenance,
	}, nil
}

func contractVersionHistory(published contractVersionPublishProjection, version cache.ContractVersion) []ContractVersionHistory {
	publishedBy := published.Actor.System
	if publishedBy == "" {
		publishedBy = published.Actor.Name
	}
	history := []ContractVersionHistory{{
		EventID: published.Event, Transition: "publish", State: "published", Actor: publishedBy, At: published.At,
	}}
	// cache.ContractVersion is the canonical fold projection of committed
	// lifecycle events, but it intentionally does not expose their actor/time.
	// Preserve only the state transitions its final state proves; blank evidence
	// fields are deliberate and must never be filled from another version.
	if version.State == "deprecated" || version.State == "retired" {
		history = append(history, ContractVersionHistory{Transition: "deprecate", State: "deprecated"})
	}
	if version.State == "retired" {
		history = append(history, ContractVersionHistory{Transition: "retire", State: "retired"})
	}
	return history
}

func contractVersionDocumentRole(profile contract.DigestProfile, entry contract.Entry) (string, bool) {
	if profile != contract.ProfileContractTreeV1 {
		return string(entry.Role), entry.Normative
	}
	switch {
	case strings.HasPrefix(entry.Path, "schema/"):
		return string(contract.RoleSchema), true
	case strings.HasPrefix(entry.Path, "fixtures/valid/"):
		return string(contract.RoleValidFixture), true
	case strings.HasPrefix(entry.Path, "fixtures/invalid/"):
		return string(contract.RoleInvalidFixture), true
	default:
		return "", false
	}
}

func contractVersionPreview(raw []byte) string {
	const maximum = 2048
	if len(raw) > maximum {
		raw = raw[:maximum]
	}
	for len(raw) > 0 && !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return string(raw)
}

const (
	maximumEmbeddedContractDocuments         = 1024
	maximumEmbeddedContractDocumentJSONBytes = 768 << 10
	maximumEmbeddedContractPreviewJSONBytes  = 256 << 10
)

type contractVersionDocumentRef struct {
	key        string
	descriptor bool
	owner      *ContractVersionDetail
	document   ContractVersionDocument
}

type contractVersionPreviewRef struct {
	key   string
	value *string
}

// limitContractVersionDetails applies one bounded projection across every
// historical package in the dashboard. The exact total remains explicit on
// each version while only a deterministic subset of document metadata enters
// the 4 MiB a2a serve shell. A shown document keeps its exact digest and other
// metadata; omitted documents are counted, never presented as an empty set.
func limitContractVersionDetails(contracts []Contract) {
	documents := make([]contractVersionDocumentRef, 0)
	for contractIndex := range contracts {
		contractValue := &contracts[contractIndex]
		for versionIndex := range contractValue.Versions {
			versionValue := &contractValue.Versions[versionIndex]
			if versionValue.Detail == nil || versionValue.Detail.Status != ContractVersionDetailAvailable {
				continue
			}
			detail := versionValue.Detail
			observedTotal := len(detail.Documents) + detail.OmittedDocumentCount
			if observedTotal > detail.TotalDocumentCount {
				detail.TotalDocumentCount = observedTotal
			}
			if detail.TotalDocumentCount < len(detail.Documents) {
				detail.TotalDocumentCount = len(detail.Documents)
			}
			detail.OmittedDocumentCount = detail.TotalDocumentCount
			prefix := contractValue.Space + "\x00" + contractValue.ID + "\x00" + versionValue.Version + "\x00"
			for _, document := range detail.Documents {
				documents = append(documents, contractVersionDocumentRef{
					key: prefix + document.Path, descriptor: document.Path == contract.DescriptorPath,
					owner: detail, document: document,
				})
			}
			detail.Documents = nil
		}
	}
	sort.SliceStable(documents, func(i, j int) bool {
		if documents[i].descriptor != documents[j].descriptor {
			return documents[i].descriptor
		}
		return documents[i].key < documents[j].key
	})
	remainingDocumentBytes := maximumEmbeddedContractDocumentJSONBytes
	retainedDocuments := 0
	for _, candidate := range documents {
		if retainedDocuments >= maximumEmbeddedContractDocuments {
			break
		}
		cost := contractVersionDocumentJSONBytes(candidate.document)
		if cost > remainingDocumentBytes {
			continue
		}
		candidate.owner.Documents = append(candidate.owner.Documents, candidate.document)
		candidate.owner.OmittedDocumentCount--
		retainedDocuments++
		remainingDocumentBytes -= cost
	}
	for contractIndex := range contracts {
		for versionIndex := range contracts[contractIndex].Versions {
			detail := contracts[contractIndex].Versions[versionIndex].Detail
			if detail == nil || detail.Status != ContractVersionDetailAvailable {
				continue
			}
			sort.Slice(detail.Documents, func(i, j int) bool { return detail.Documents[i].Path < detail.Documents[j].Path })
		}
	}
	limitContractVersionPreviews(contracts)
}

func contractVersionDocumentJSONBytes(document ContractVersionDocument) int {
	document.Preview = ""
	encoded, err := json.Marshal(document)
	if err != nil {
		return maximumEmbeddedContractDocumentJSONBytes + 1
	}
	// Render uses indented JSON several levels deep. Reserve more whitespace
	// than this fixed-field object can add so the compact cost never rounds down.
	return len(encoded) + 256
}

func limitContractVersionPreviews(contracts []Contract) {
	refs := make([]contractVersionPreviewRef, 0)
	for contractIndex := range contracts {
		contractValue := &contracts[contractIndex]
		for versionIndex := range contractValue.Versions {
			versionValue := &contractValue.Versions[versionIndex]
			if versionValue.Detail == nil || versionValue.Detail.Status != ContractVersionDetailAvailable {
				continue
			}
			prefix := contractValue.Space + "\x00" + contractValue.ID + "\x00" + versionValue.Version + "\x00"
			refs = append(refs, contractVersionPreviewRef{key: prefix + "\x00description", value: &versionValue.Detail.Description})
			for documentIndex := range versionValue.Detail.Documents {
				document := &versionValue.Detail.Documents[documentIndex]
				refs = append(refs, contractVersionPreviewRef{key: prefix + document.Path, value: &document.Preview})
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].key < refs[j].key })
	remaining := maximumEmbeddedContractPreviewJSONBytes
	for _, ref := range refs {
		bounded := contractVersionPreview([]byte(*ref.value))
		*ref.value = contractVersionPreviewWithinJSONBudget(bounded, &remaining)
	}
}

func contractVersionPreviewWithinJSONBudget(preview string, remaining *int) string {
	if preview == "" || remaining == nil || *remaining <= 0 {
		return ""
	}
	end := 0
	for offset, char := range preview {
		cost := contractVersionPreviewRuneJSONBytes(char)
		if cost > *remaining {
			break
		}
		*remaining -= cost
		end = offset + utf8.RuneLen(char)
	}
	return preview[:end]
}

func contractVersionPreviewRuneJSONBytes(char rune) int {
	switch char {
	case '\b', '\t', '\n', '\f', '\r', '"', '\\':
		return 2
	case '<', '>', '&', '\u2028', '\u2029':
		return 6
	default:
		if char < 0x20 {
			return 6
		}
		return utf8.RuneLen(char)
	}
}

func contractVersionPreviewLanguage(name, mediaType string) string {
	extension := strings.ToLower(filepath.Ext(name))
	switch {
	case extension == ".json" || strings.Contains(mediaType, "json"):
		return "json"
	case extension == ".yaml" || extension == ".yml" || strings.Contains(mediaType, "yaml"):
		return "yaml"
	case extension == ".md" || extension == ".markdown" || mediaType == "text/markdown":
		return "markdown"
	default:
		return ""
	}
}

func contractVersionConsumerPins(edges []ContractEdge, spaceID, contractID, version string) []ContractVersionConsumerPin {
	var pins []ContractVersionConsumerPin
	for _, edge := range edges {
		if edge.Space != spaceID || edge.Contract != contractID || edge.PinnedVersion != version {
			continue
		}
		pins = append(pins, ContractVersionConsumerPin{
			System: edge.From, Space: edge.Space, PinnedMajor: edge.PinnedMajor,
			PinnedVersion: edge.PinnedVersion, PinnedState: edge.PinnedState, Drift: edge.Drift,
		})
	}
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Space != pins[j].Space {
			return pins[i].Space < pins[j].Space
		}
		return pins[i].System < pins[j].System
	})
	return pins
}

// buildThreads discovers every non-empty thread by grouping
// Store.Search's full (open + closed) item listing per (space, thread), then
// renders each qualifying group through Store.ThreadView — the SAME reader
// `a2a thread` itself uses, never a second traversal of the fold. spaceID is
// ALWAYS passed explicitly (never ""): every group is already scoped to one
// space, so this never triggers ThreadView's cross-space
// *cache.ThreadAmbiguityError path (CC-073 — a thread present in two spaces
// is two separate Data.Thread rows here, one per space, matching
// Thread.Space). A ThreadView error for one group degrades that group away
// (skip, continue) rather than failing the whole dashboard — same "degrade,
// never fail the view" convention as this file's own consumes.yaml read.
func buildThreads(ctx context.Context, store *cache.Store, self string, now time.Time) ([]Thread, []ThreadView, openItemIndex, error) {
	items, err := store.Search(ctx, "", cache.SearchFilters{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("search: %w", err)
	}

	type groupKey struct{ space, thread string }
	counts := map[groupKey]int{}
	var order []groupKey
	for _, it := range items {
		if it.Thread == "" {
			continue // threadless (pre-P46 / non-conforming) artifacts never group into a pseudo-thread
		}
		k := groupKey{it.Space, it.Thread}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}

	type row struct {
		t  Thread
		v  ThreadView
		at time.Time
	}
	var rows []row
	// Every thread is traversed, including a newly submitted document that has
	// not received a reply yet. Those one-member threads are real conversations
	// in flight: hiding them made the Threads page omit exactly the newest work.
	// ThreadView remains the one computation for both the list and open-item
	// prompts; the browser does not grow a second grouping rule.
	openItems := openItemIndex{}
	for _, k := range order {
		result, tErr := store.ThreadView(ctx, k.thread, k.space)
		if tErr != nil {
			continue
		}
		openItems.put(k.space, result.OpenItems)
		if len(result.Artifacts) == 0 {
			continue // ThreadView's own rendered member set is authoritative
		}
		th, at := toThread(result, self)
		rows = append(rows, row{t: th, v: toThreadView(result, self), at: at})
	}

	sort.Slice(rows, func(i, j int) bool {
		return threadRowComesBefore(rows[i].at, rows[i].t.Space, rows[i].t.ID, rows[j].at, rows[j].t.Space, rows[j].t.ID)
	})
	out := make([]Thread, 0, len(rows))
	views := make([]ThreadView, 0, len(rows))
	for _, r := range rows {
		r.t.LastActivity = humanizeAge(now, r.at)
		out = append(out, r.t)
		views = append(views, r.v)
	}
	return out, views, openItems, nil
}

func threadRowComesBefore(aAt time.Time, aSpace, aID string, bAt time.Time, bSpace, bID string) bool {
	if !aAt.Equal(bAt) {
		return aAt.After(bAt)
	}
	if aSpace != bSpace {
		return aSpace < bSpace
	}
	return aID < bID
}

func toThreadView(result cache.ThreadResult, self string) ThreadView {
	view := ThreadView{
		Thread: result.Thread, Space: result.Space, Order: result.Order,
		Opener:       ThreadViewOpener{ID: result.Opener.ID, Title: result.Opener.Title, From: result.Opener.From},
		Participants: append([]string(nil), result.Participants...), SyncStale: result.SyncStale,
		Artifacts: []ThreadViewArtifact{}, Transcript: []ThreadTranscriptRow{}, OpenItems: []ThreadOpenItem{},
		Flags: []ThreadViewFlag{}, Unresolved: []ThreadUnresolvedRef{},
		// Settled until an open item proves someone still owes a move.
		Settled: true,
	}
	for _, artifact := range result.Artifacts {
		refs := make([]map[string]any, 0, len(artifact.Refs))
		for _, ref := range artifact.Refs {
			refs = append(refs, map[string]any{"ref": ref.Ref})
		}
		view.Artifacts = append(view.Artifacts, ThreadViewArtifact{
			ID: artifact.ID, Type: artifact.Type, From: artifact.From, To: artifact.To,
			Title: artifact.Title, State: artifact.State, Parent: artifact.Parent, Refs: refs,
		})
	}
	for _, entry := range result.Transcript {
		row := ThreadTranscriptRow{Seq: entry.Seq, Kind: entry.Kind, At: entry.At.UTC().Format(time.RFC3339)}
		if entry.Artifact != nil {
			row.Artifact = &TranscriptArtifact{ID: entry.Artifact.ID, Type: entry.Artifact.Type, From: entry.Artifact.From, To: entry.Artifact.To, Title: entry.Artifact.Title}
			if entry.Artifact.Work != nil {
				row.Artifact.Work = toHTMLWorkReport(result.Space, result.Thread, entry.Artifact.Work)
			}
		}
		if entry.Event != nil {
			actor := map[string]any{
				"kind": entry.Event.Actor.Kind, "name": entry.Event.Actor.Name, "system": entry.Event.Actor.System,
			}
			if entry.Event.Actor.Model != "" {
				actor["model"] = entry.Event.Actor.Model
			}
			if entry.Event.Actor.Session != "" {
				actor["session"] = entry.Event.Actor.Session
			}
			row.Event = &TranscriptEvent{
				ULID: entry.Event.ULID, Subject: entry.Event.Subject, Transition: entry.Event.Transition,
				ClaimedState: entry.Event.ClaimedState, ResponseID: entry.Event.ResponseID,
				Note: entry.Event.Note, Actor: actor, ProducedBy: entry.Event.ProducedBy,
				Consistency: entry.Event.Consistency,
			}
		}
		view.Transcript = append(view.Transcript, row)
	}
	for _, item := range result.OpenItems {
		actions := make([]ThreadNextAction, 0, len(item.NextActions))
		for _, action := range item.NextActions {
			actions = append(actions, ThreadNextAction{Transition: action.Transition, By: append([]string(nil), action.By...)})
		}
		// waiting: [] not null — the page reads this list directly, and a JSON
		// null would render as "waiting on " with nothing after it.
		waiting := append([]string{}, item.WaitingOn...)
		// The document's own author is the outgoing end of it. A thread member
		// carries From, so the loop this move belongs to is known here without
		// asking the artifact store a second time.
		outgoing := false
		for _, a := range result.Artifacts {
			if a.ID == item.ID {
				outgoing = a.From == self
				break
			}
		}
		view.OpenItems = append(view.OpenItems, ThreadOpenItem{
			ID: item.ID, Type: item.Type, State: item.State, Blocking: item.Blocking,
			NeededBy: item.NeededBy, NextActions: actions, WaitingOn: waiting,
			YourMove: item.YourMove, Pending: len(waiting) > 0,
			Prompt: agentPromptOf(actions, item.Type, self, outgoing),
		})
		if len(waiting) > 0 {
			view.Settled = false
		}
	}
	for _, flag := range result.Flags {
		view.Flags = append(view.Flags, ThreadViewFlag{
			Kind: flag.Kind, Subject: flag.Subject, EventULID: flag.EventULID, Consistency: flag.Consistency,
		})
	}
	for _, unresolved := range result.Unresolved {
		view.Unresolved = append(view.Unresolved, ThreadUnresolvedRef{ID: unresolved.ID, Kind: unresolved.Kind})
	}
	// Deliveries (spec 05a AC-7): projected via ProjectDeliveries
	// (delivery.go), the last link between cache.Store.ThreadView's own
	// resolution (internal/cache/threadview.go's buildDeliveries) and the
	// rendered page. Left nil (never []Delivery{}) when result.Deliveries
	// is empty, matching ThreadView.Deliveries' own `omitempty` contract
	// (model.go) — a thread with no data deliverables emits no
	// `"deliveries"` key at all, exactly as before this wave.
	if len(result.Deliveries) > 0 {
		view.Deliveries = ProjectDeliveries(result.Deliveries)
	}
	return view
}

func toHTMLWorkReport(spaceID, threadID string, report *cache.TranscriptWorkReport) *WorkReport {
	if report == nil {
		return nil
	}
	waits := make([]WorkReportWait, 0, len(report.WaitingOn))
	for _, wait := range report.WaitingOn {
		waits = append(waits, WorkReportWait{Kind: string(wait.Kind), ID: wait.ID, Summary: wait.Summary})
	}
	validUntil := ""
	if !report.ValidUntil.IsZero() {
		validUntil = report.ValidUntil.UTC().Format(time.RFC3339Nano)
	}
	return &WorkReport{
		Space: spaceID, Thread: threadID, ArtifactID: report.ArtifactID,
		WorkID: report.WorkID, SubjectRef: report.SubjectRef, Mode: string(report.Mode), Summary: report.Summary,
		Actor: WorkReportActor{
			Kind: report.Actor.Kind, Name: report.Actor.Name, System: report.Actor.System,
			Model: report.Actor.Model, Session: report.Actor.Session,
		},
		WaitingOn: waits, ReportedAt: report.ReportedAt.UTC().Format(time.RFC3339Nano),
		ValidUntil: validUntil, CommitSequence: report.CommitSequence,
	}
}

func collectWorkReports(views []ThreadView) []WorkReport {
	reports := make([]WorkReport, 0)
	seen := make(map[string]bool)
	for _, view := range views {
		for _, row := range view.Transcript {
			if row.Artifact == nil || row.Artifact.Work == nil {
				continue
			}
			report := *row.Artifact.Work
			key := report.Space + "\x00" + report.ArtifactID
			if seen[key] {
				continue
			}
			seen[key] = true
			reports = append(reports, report)
		}
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].Space != reports[j].Space {
			return reports[i].Space < reports[j].Space
		}
		if reports[i].Thread != reports[j].Thread {
			return reports[i].Thread < reports[j].Thread
		}
		if reports[i].CommitSequence != reports[j].CommitSequence {
			return reports[i].CommitSequence < reports[j].CommitSequence
		}
		return reports[i].ArtifactID < reports[j].ArtifactID
	})
	return reports
}

// toThread projects one cache.ThreadResult into the dashboard's Thread shape
// plus its own last-activity timestamp (the sort key buildThreads uses before
// formatting it away into Thread.LastActivity).
func toThread(result cache.ThreadResult, self string) (Thread, time.Time) {
	byID := make(map[string]cache.ThreadArtifact, len(result.Artifacts))
	for _, a := range result.Artifacts {
		byID[a.ID] = a
	}

	members := make([]ThreadMember, 0, len(result.Artifacts))
	var last time.Time
	for _, entry := range result.Transcript {
		if entry.At.After(last) {
			last = entry.At
		}
		if entry.Kind != "artifact" || entry.Artifact == nil {
			continue
		}
		state := byID[entry.Artifact.ID].State
		members = append(members, ThreadMember{
			ID: entry.Artifact.ID, Type: entry.Artifact.Type, Title: entry.Artifact.Title,
			From: entry.Artifact.From, To: entry.Artifact.To, State: state, Seq: entry.Seq,
		})
	}

	var links []DocLink
	for _, a := range result.Artifacts {
		if a.Parent != "" {
			if _, ok := byID[a.Parent]; ok {
				links = append(links, DocLink{From: a.ID, To: a.Parent, Kind: "parent"})
			}
		}
		for _, r := range a.Refs {
			target := refID(r.Ref)
			if _, ok := byID[target]; ok {
				links = append(links, DocLink{From: a.ID, To: target, Kind: "ref"})
			}
		}
	}

	// A thread carries no "closed" state of its own: closure is derived. An
	// open item whose WaitingOn is empty has only the owner's escape hatches
	// left (cache strips cancel/withdraw/supersede out of WaitingOn), so it
	// owes nobody anything — counting it as pending is what made a finished
	// exchange read "waiting on others" forever.
	//
	// The names are carried too, not just the boolean. "Waiting on others" is
	// an answer nobody can act on; "waiting on seomatrix" is. Self is excluded
	// from the list because YourMove already says it, and repeating it turns
	// "you and seomatrix" into "you, you and seomatrix" on multi-item threads.
	yourMove, settled, openCount := false, true, 0
	waitingOthers := []string{}
	for _, oi := range result.OpenItems {
		if len(oi.WaitingOn) == 0 {
			continue
		}
		openCount++
		settled = false
		for _, who := range oi.WaitingOn {
			if who == self {
				yourMove = true
				continue
			}
			if !containsString(waitingOthers, who) {
				waitingOthers = append(waitingOthers, who)
			}
		}
	}
	sort.Strings(waitingOthers)

	if links == nil {
		links = []DocLink{}
	}

	return Thread{
		ID: result.Thread, Space: result.Space, Participants: result.Participants,
		MemberCount: len(result.Artifacts), OpenCount: openCount,
		Opener:   ThreadOpener{ID: result.Opener.ID, Title: result.Opener.Title},
		YourMove: yourMove, WaitingOthers: waitingOthers, Settled: settled, Members: members, Links: links,
	}, last
}

// refID extracts the bare artifact id from a §5.7 ref grammar string (`id`,
// `id@version`, `id#digest`, or a cross-space `space:id...` form) — a local,
// minimal re-projection of cache's own unexported splitRef (the "each layer
// owns its own minimal decode" idiom internal/cache/decode.go's own doc
// comment already establishes), needed because DocLink.To must be a bare id
// to join against a thread's own rendered member set.
func refID(ref string) string {
	rest := ref
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		rest = rest[i+1:]
	}
	return rest
}

func containsString(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}

// toItem maps a cache.Item to the model Item with derived age + severity.
func toItem(it cache.Item, now time.Time, self string, open openItemIndex) Item {
	gate := hasReason(it.Reasons, "gate-pending-on-me")
	createdAt := it.CreatedAt
	if createdAt.IsZero() {
		// Legacy callers constructed cache.Item before the immutable creation
		// clock was projected. Preserve a deterministic degraded view for those
		// rows; real cache reads always populate CreatedAt from the envelope.
		createdAt = it.LatestEventAt
	}
	created := ""
	if !createdAt.IsZero() {
		created = createdAt.UTC().Format(time.RFC3339)
	}
	moved := ""
	if !it.LatestEventAt.IsZero() {
		moved = it.LatestEventAt.UTC().Format(time.RFC3339)
	}
	var prompt *AgentPrompt
	if openItem, ok := open.get(it.Space, it.ID); ok {
		prompt = agentPrompt(openItem, self, it.From == self)
	}
	return Item{
		Space: it.Space, ID: it.ID, Type: it.Type, Title: it.Title, From: it.From, To: it.To,
		State: it.State, Priority: it.Priority, Blocking: it.Blocking, GatePending: gate,
		Thread: it.Thread, Age: humanizeAge(now, createdAt), NeededBy: it.NeededBy,
		CreatedAt: created, CreatedSeq: it.CreatedSeq, CreatedOrderKnown: it.CreatedOrderKnown,
		MovedAt: moved, ActivitySeq: it.LatestEventSeq, ActivityEventID: it.LatestEventID, New: it.New,
		Severity: severityOf(it, gate), Reasons: it.Reasons, PendingMerge: it.PendingMerge,
		SyncStale: it.SyncStale, YourMove: it.YourMove, Description: it.Description,
		Prompt: prompt,
	}
}

func visibleArtifactRefs(groups ...[]cache.Item) []string {
	seen := map[string]bool{}
	var refs []string
	for _, items := range groups {
		for _, item := range items {
			ref := item.Space + ":" + item.ID
			if seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	return refs
}

func toArtifactDetail(show cache.ShowResult) (ArtifactDetail, error) {
	events := make([]ArtifactDetailEvent, 0, len(show.Events))
	for _, event := range show.Events {
		at := ""
		if !event.At.IsZero() {
			at = event.At.UTC().Format(time.RFC3339Nano)
		}
		events = append(events, ArtifactDetailEvent{
			ULID: event.ULID, Subject: event.Subject, Transition: event.Transition,
			ClaimedState: event.ClaimedState, ActorKind: event.ActorKind, Actor: event.Actor,
			ActorSystem: event.ActorSystem, ActorModel: event.ActorModel, ActorSession: event.ActorSession,
			ProducedBy: event.ProducedBy, Consistency: event.Consistency, At: at, Note: event.Note,
		})
	}
	refs := make([]ArtifactDetailRef, 0, len(show.Refs))
	for _, ref := range show.Refs {
		refs = append(refs, ArtifactDetailRef{
			Ref: ref.Ref, ID: ref.ID, Version: ref.Version,
			PinnedDigest: ref.PinnedDigest, Resolved: ref.Resolved,
			ResolvedDigest: ref.ResolvedDigest, DigestMismatch: ref.DigestMismatch,
		})
	}
	envelope := show.Envelope
	if envelope == nil {
		envelope = map[string]any{}
	}
	bodyHTML, err := renderArtifactMarkdown(show.Body)
	if err != nil {
		return ArtifactDetail{}, err
	}
	return ArtifactDetail{
		SourceClass: "canonical", Space: show.Space, Path: show.Path, ID: show.ID, Type: show.Type,
		Title: show.Title, From: show.From, To: append([]string(nil), show.To...),
		State: show.State, Thread: show.Thread, Envelope: envelope, Body: show.Body, BodyHTML: bodyHTML,
		Digest: show.Digest, Events: events, Flags: append([]string(nil), show.Flags...),
		// cache.ShowResult.SyncAge is a raw time.Duration string ("139.279177ms")
		// because `a2a show --json` is a machine contract. A person reading a
		// dashboard should never see nanosecond precision, so the projection
		// re-humanises it into the same compact vocabulary every other age on
		// this page uses.
		Refs: refs, SyncStale: show.SyncStale, SyncAge: humanizeDuration(show.SyncAge),
	}, nil
}

// attachLinkedArtifactEvents restores lifecycle events that create or name the
// selected artifact without using it as the event subject. A response is the
// canonical example: the `respond` event belongs to the original request and
// carries the new response in response_id. ShowResult correctly stays scoped
// to subject events; the document timeline, however, must also show the event
// that introduced the selected response. ThreadView is the authoritative
// causal projection, so this joins that already-folded record instead of
// inventing a second event traversal for HTML.
func attachLinkedArtifactEvents(detail *ArtifactDetail, views []ThreadView) {
	seen := make(map[string]bool, len(detail.Events))
	for _, event := range detail.Events {
		seen[event.ULID] = true
	}

	for _, view := range views {
		if view.Space != detail.Space || (detail.Thread != "" && view.Thread != detail.Thread) {
			continue
		}
		for _, row := range view.Transcript {
			if row.Event == nil || row.Event.ResponseID != detail.ID || seen[row.Event.ULID] {
				continue
			}
			actor, _ := row.Event.Actor["name"].(string)
			actorSystem, _ := row.Event.Actor["system"].(string)
			detail.Events = append(detail.Events, ArtifactDetailEvent{
				ULID: row.Event.ULID, Subject: row.Event.Subject,
				Transition: row.Event.Transition, ClaimedState: row.Event.ClaimedState,
				Actor: actor, ActorSystem: actorSystem, At: row.At,
			})
			seen[row.Event.ULID] = true
		}
	}

	sort.SliceStable(detail.Events, func(i, j int) bool {
		a, aErr := time.Parse(time.RFC3339Nano, detail.Events[i].At)
		b, bErr := time.Parse(time.RFC3339Nano, detail.Events[j].At)
		if aErr == nil && bErr == nil && !a.Equal(b) {
			return a.Before(b)
		}
		if detail.Events[i].At != detail.Events[j].At {
			return detail.Events[i].At < detail.Events[j].At
		}
		return detail.Events[i].ULID < detail.Events[j].ULID
	})
}

// humanizeDuration re-formats a Go duration string into the dashboard's own
// compact age vocabulary. An unparseable value is returned unchanged rather
// than dropped: an age we cannot read is still a fact, and inventing "just now"
// for it would be a lie.
func humanizeDuration(raw string) string {
	if raw == "" {
		return ""
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		return raw
	}
	now := time.Now()
	return humanizeAge(now, now.Add(-dur))
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

// dependencyFacts answers the consumer's actual question: what is happening
// on the MAJOR line I registered, not merely what the contract's newest major
// is doing. ContractInfo.Versions is semver-ascending, so the last matching
// entry is the newest version available on that line.
func dependencyFacts(ci cache.ContractInfo, pinnedMajor int) (
	pinnedVersion, pinnedState, sunset, successor string,
	availableMajors []int, drift string,
) {
	seenMajors := map[int]bool{}
	var pinned *cache.ContractVersion
	for i := range ci.Versions {
		v := &ci.Versions[i]
		major, validMajor := parseMajor(v.Version)
		if validMajor && !seenMajors[major] {
			seenMajors[major] = true
			availableMajors = append(availableMajors, major)
		}
		if validMajor && major == pinnedMajor {
			pinned = v
		}
	}
	sort.Ints(availableMajors)
	if pinned == nil {
		if len(ci.Versions) == 0 {
			return "", ci.State, "", "", availableMajors,
				driftOf(ci.State, pinnedMajor, ci.Version)
		}
		return "", "missing", "", "", availableMajors, "missing"
	}

	pinnedVersion, pinnedState = pinned.Version, pinned.State
	sunset, successor = pinned.Sunset, pinned.Successor
	switch pinned.State {
	case "retired":
		drift = "retired"
	case "deprecated":
		drift = "deprecated"
	default:
		if majorOf(ci.Version) > pinnedMajor {
			drift = "behind"
		} else {
			drift = "current"
		}
	}
	return pinnedVersion, pinnedState, sunset, successor, availableMajors, drift
}

// spaceWorkflowVersion reads the immutable reusable-workflow ref already
// committed in a space mirror. It is a separate compatibility axis from
// space.yaml's min_binary_version. Malformed/absent workflows degrade to
// empty facts; the dashboard must label the axis unavailable, never guess it
// from the binary or the template.
func spaceWorkflowVersion(dir string) (version, ref string) {
	raw, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "a2a-validate.yml"))
	if err != nil {
		return "", ""
	}
	var workflow struct {
		Jobs map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		return "", ""
	}
	var refs []string
	for _, job := range workflow.Jobs {
		if strings.Contains(job.Uses, "a2a-validate-reusable.yml@") {
			refs = append(refs, job.Uses)
		}
	}
	if len(refs) == 0 {
		return "", ""
	}
	sort.Strings(refs)
	ref = refs[0]
	for _, candidate := range refs[1:] {
		if candidate != ref {
			return "mixed", strings.Join(refs, ", ")
		}
	}
	at := strings.LastIndexByte(ref, '@')
	if at < 0 || at == len(ref)-1 {
		return "", ref
	}
	version = strings.TrimPrefix(ref[at+1:], "v")
	return version, ref
}

func toReleaseNotes(in []notes.ReleaseNotes) []ReleaseNote {
	out := make([]ReleaseNote, 0, len(in))
	for _, release := range in {
		row := ReleaseNote{
			Version: release.Version, Released: release.Released,
			Headline: release.Headline, Changes: []ReleaseChange{},
		}
		for _, change := range release.Changes {
			row.Changes = append(row.Changes, ReleaseChange{
				ID: change.ID, Kind: change.Kind, Impact: change.Impact,
				Subject: change.Subject, Detail: change.Detail,
				Affects: append([]string(nil), change.Affects...),
				Action: ReleaseAction{
					Scope: change.Action.Scope, Why: change.Action.Why,
					Detect: append([]string(nil), change.Action.Detect...),
					Run:    append([]string(nil), change.Action.Run...),
				},
			})
		}
		out = append(out, row)
	}
	return out
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
	major, _ := parseMajor(version)
	return major
}

func parseMajor(version string) (int, bool) {
	if version == "" {
		return 0, false
	}
	seg := version
	if i := strings.IndexByte(seg, '.'); i >= 0 {
		seg = seg[:i]
	}
	if seg == "" {
		return 0, false
	}
	n := 0
	for _, r := range seg {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
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
