package mcp

// a2a_contract_* (OP-212/OP-213/OP-221 3rd clause): mirrors internal/cli's
// cmd_contract.go ContractCommand sub-verbs exactly — new (thin delegate
// to a2a_new's own draft path), publish/deprecate/retire (funnel writers,
// G1/G2/§5.4 gate awareness unchanged), diff/verify-export (read-only, no
// funnel/event — the digest-tree comparison over the local mirror).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// contractDescriptorProbe is this package's own minimal decode of a
// contract's descriptor (contract.md) fields (mirrors internal/cli's
// contractDescriptorProbe).
type contractDescriptorProbe struct {
	ID            string `yaml:"id"`
	Space         string `yaml:"space"`
	From          string `yaml:"from"`
	Version       string `yaml:"version"`
	CompatPolicy  string `yaml:"compat_policy"`
	SchemaFormat  string `yaml:"schema_format"`
	GeneratedFrom struct {
		Tool         string `yaml:"tool"`
		SourceDigest string `yaml:"source_digest"`
	} `yaml:"generated_from"`
}

func contractReadDescriptor(mirrorDir, id string) (fm artifact.Frontmatter, probe contractDescriptorProbe, relPath, relDir string, err error) {
	parsed, perr := artifact.ParseID(id)
	if perr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: %w", id, perr)
	}
	if parsed.Prefix != "XC" {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: not a contract id (XC-)", id)
	}
	layout, lerr := space.NewLayout(parsed.System)
	if lerr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", lerr
	}
	relPath = layout.ProvidesContract(parsed.Slug)
	relDir = path.Dir(relPath)
	raw, rerr := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if rerr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: cannot read %s: %w", id, rerr)
	}
	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: %w", id, ferr)
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return artifact.Frontmatter{}, contractDescriptorProbe{}, "", "", fmt.Errorf("mcp: %s: cannot decode descriptor: %w", id, err)
	}
	return fm, probe, relPath, relDir, nil
}

func contractAddFrontmatterFields(raw []byte, fields map[string]any) ([]byte, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return nil, fmt.Errorf("mcp: cannot decode frontmatter: %w", err)
	}
	for k, v := range fields {
		doc[k] = v
	}
	newYAML, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("mcp: cannot encode frontmatter: %w", err)
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body}), nil
}

// --- semver ---------------------------------------------------------------

type contractSemver [3]int

func contractParseSemver(s string) (contractSemver, error) {
	var out contractSemver
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("mcp: %q is not a valid semver (major.minor.patch)", s)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("mcp: %q is not a valid semver (major.minor.patch)", s)
		}
		out[i] = n
	}
	return out, nil
}

func (v contractSemver) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func contractBump(prior contractSemver, kind string) contractSemver {
	switch kind {
	case "major":
		return contractSemver{prior[0] + 1, 0, 0}
	case "minor":
		return contractSemver{prior[0], prior[1] + 1, 0}
	case "patch":
		return contractSemver{prior[0], prior[1], prior[2] + 1}
	default:
		return prior
	}
}

var contractDigestSubtrees = []string{"schema", "fixtures"}

func contractDeprecateSeed(contractID, version, sunset string) []byte {
	var buf bytes.Buffer
	buf.WriteString("contract=" + contractID + "\n")
	buf.WriteString("version=" + version + "\n")
	buf.WriteString("sunset=" + sunset + "\n")
	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

type contractDiffTree struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
}

func contractDiff(a, b map[string]string) contractDiffTree {
	var out contractDiffTree
	for p, da := range a {
		db, ok := b[p]
		if !ok {
			out.Removed = append(out.Removed, p)
		} else if da != db {
			out.Changed = append(out.Changed, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out.Added = append(out.Added, p)
		}
	}
	sort.Strings(out.Added)
	sort.Strings(out.Removed)
	sort.Strings(out.Changed)
	return out
}

func contractResolveVersionSHA(ctx context.Context, repoDir, descriptorPath, version string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "log", "--format=%H", "--", descriptorPath).Output()
	if err != nil {
		return "", fmt.Errorf("mcp: git log %s: %w", descriptorPath, err)
	}
	shas := strings.Fields(string(out))
	for _, sha := range shas {
		content, serr := exec.CommandContext(ctx, "git", "-C", repoDir, "show", sha+":"+descriptorPath).Output()
		if serr != nil {
			continue
		}
		fm, ferr := artifact.ParseFrontmatter(content)
		if ferr != nil {
			continue
		}
		var probe contractDescriptorProbe
		if yaml.Unmarshal(fm.YAML, &probe) == nil && probe.Version == version {
			return sha, nil
		}
	}
	return "", fmt.Errorf("mcp: no commit found where %s has version %s", descriptorPath, version)
}

func contractDigestTreeAtSHA(ctx context.Context, repoDir, sha, descriptorDir string) (map[string]string, error) {
	perFile := map[string]string{}
	for _, sub := range []string{"schema", "fixtures"} {
		dir := path.Join(descriptorDir, sub)
		out, err := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-tree", "-r", "--name-only", sha, "--", dir).Output()
		if err != nil {
			continue
		}
		for _, rel := range strings.Fields(string(out)) {
			content, serr := exec.CommandContext(ctx, "git", "-C", repoDir, "show", sha+":"+rel).Output()
			if serr != nil {
				return nil, fmt.Errorf("mcp: git show %s:%s: %w", sha, rel, serr)
			}
			relToDescriptorRoot, rerr := filepath.Rel(descriptorDir, rel)
			if rerr != nil {
				return nil, rerr
			}
			perFile[filepath.ToSlash(relToDescriptorRoot)] = artifact.Digest(content)
		}
	}
	return perFile, nil
}

// --- ContractDeps -----------------------------------------------------

// ContractDeps bundles WriteDeps for the contract family's funnel-writer
// sub-verbs (publish/deprecate/retire) and read-only sub-verbs
// (diff/verify-export).
type ContractDeps struct {
	WriteDeps
}

// ContractNewInput is a2a_contract_new's structured input: a thin delegate
// onto a2a_new's own draft path with type="contract" (mirrors
// internal/cli's runNew -> P6 NewCommand delegation).
type ContractNewInput struct {
	Slug   string            `json:"slug"`
	Fields map[string]string `json:"fields,omitempty"`
	Body   string            `json:"body,omitempty"`
	Thread string            `json:"thread,omitempty"`
	Actor  ActorInput        `json:"actor,omitempty"`
}

func newContractNewHandler(newDeps NewDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractNewInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract new: invalid input: %w", err)
		}
		if in.Slug == "" {
			return nil, "", fmt.Errorf("contract new: slug is required")
		}
		inner := NewInput{
			Items:  []NewItem{{Type: "contract", Fields: in.Fields, Body: in.Body, Slug: in.Slug, Actor: in.Actor}},
			Thread: in.Thread,
		}
		raw, merr := json.Marshal(inner)
		if merr != nil {
			return nil, "", fmt.Errorf("contract new: %w", merr)
		}
		return newNewHandler(newDeps)(ctx, raw)
	}
}

// contractPublishedVersions returns every PRIOR publish event's version
// for id, sorted ascending.
func contractPublishedVersions(all []eventDoc, id string) []contractSemver {
	var out []contractSemver
	for _, ev := range all {
		if ev.Subject != id || ev.Transition != fold.TPublish || ev.Version == "" {
			continue
		}
		v, err := contractParseSemver(ev.Version)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		for k := 0; k < 3; k++ {
			if out[i][k] != out[j][k] {
				return out[i][k] < out[j][k]
			}
		}
		return false
	})
	return out
}

// ContractPublishInput is a2a_contract_publish's structured input.
type ContractPublishInput struct {
	ID                  string     `json:"id"`
	Version             string     `json:"version,omitempty"`
	Bump                string     `json:"bump,omitempty"`
	GeneratedFromDigest string     `json:"generated_from_digest,omitempty"`
	Actor               ActorInput `json:"actor,omitempty"`
}

func newContractPublishHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractPublishInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract publish: invalid input: %w", err)
		}
		if in.ID == "" || (in.Version == "" && in.Bump == "") {
			return nil, "", fmt.Errorf("contract publish: id and one of version or bump are required")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		verdict, _, err := checkLegality(deps.MirrorDir, deps.Manifest, in.ID, fold.TPublish, actor)
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %s: %w", in.ID, err)
		}
		if verdict != fold.VerdictLegal {
			return nil, "", fmt.Errorf("contract publish: %w", verdictError(in.ID, verdict))
		}

		fm, probe, relPath, relDir, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}

		all, err := readAllEvents(deps.MirrorDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		priorVersions := contractPublishedVersions(all, in.ID)
		isFirstPublish := len(priorVersions) == 0

		baseline := contractSemver{0, 0, 0}
		if !isFirstPublish {
			baseline = priorVersions[len(priorVersions)-1]
		}

		var newVersion contractSemver
		if in.Version != "" {
			newVersion, err = contractParseSemver(in.Version)
			if err != nil {
				return nil, "", fmt.Errorf("contract publish: %w", err)
			}
		} else {
			newVersion = contractBump(baseline, in.Bump)
		}

		isMajorBump := !isFirstPublish && newVersion[0] > baseline[0]
		gated := isFirstPublish || isMajorBump

		now := deps.Now()
		eventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: cannot mint event id: %w", err)
		}

		var doc map[string]any
		if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		doc["version"] = newVersion.String()
		if in.GeneratedFromDigest != "" {
			gf, _ := doc["generated_from"].(map[string]any)
			if gf == nil {
				gf = map[string]any{"tool": probe.GeneratedFrom.Tool}
			}
			gf["source_digest"] = in.GeneratedFromDigest
			doc["generated_from"] = gf
		}
		newYAML, err := yaml.Marshal(doc)
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		newRaw := artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: newYAML, Body: fm.Body})

		files := []space.FileWrite{{Path: relPath, Content: newRaw}}

		digest, _, derr := artifact.DigestTreeFS(filepath.Join(deps.MirrorDir, relDir), contractDigestSubtrees)
		if derr != nil {
			return nil, "", fmt.Errorf("contract publish: cannot compute digest tree: %w", derr)
		}

		ev := eventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: in.ID, Transition: fold.TPublish,
			Actor:   eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:      now.UTC().Format(time.RFC3339),
			Version: newVersion.String(), Digest: digest,
		}
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			return nil, "", fmt.Errorf("contract publish: cannot encode event: %w", merr)
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})

		req := deps.buildRequest([]string{in.ID}, files, "contract-publish", gated)
		result, err := deps.submit(ctx, req, "contract publish", []string{in.ID})
		return result, "", err
	}
}

// ContractDeprecateInput is a2a_contract_deprecate's structured input.
type ContractDeprecateInput struct {
	ID        string     `json:"id"`
	Version   string     `json:"version,omitempty"`
	Successor string     `json:"successor"`
	Sunset    string     `json:"sunset"`
	Actor     ActorInput `json:"actor,omitempty"`
}

func newContractDeprecateHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractDeprecateInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract deprecate: invalid input: %w", err)
		}
		if in.ID == "" || in.Successor == "" || in.Sunset == "" {
			return nil, "", fmt.Errorf("contract deprecate: id, successor and sunset are required")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		verdict, _, err := checkLegality(deps.MirrorDir, deps.Manifest, in.ID, fold.TDeprecate, actor)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %s: %w", in.ID, err)
		}
		if verdict != fold.VerdictLegal {
			return nil, "", fmt.Errorf("contract deprecate: %w", verdictError(in.ID, verdict))
		}
		_, probe, _, _, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}
		deprecatedVersion := in.Version
		if deprecatedVersion == "" {
			deprecatedVersion = probe.Version
		}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		deprecateEventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot mint event id: %w", err)
		}
		deprecateEvent := eventDoc{
			Schema: "event/v1", Event: deprecateEventID.String(), Space: probe.Space,
			Subject: in.ID, Transition: fold.TDeprecate, Version: deprecatedVersion,
			Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
			Refs:  []refEntry{{Ref: in.Successor}},
		}
		deprecateRaw, merr := yaml.Marshal(deprecateEvent)
		if merr != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot encode event: %w", merr)
		}

		announcementSeed := contractDeprecateSeed(in.ID, deprecatedVersion, in.Sunset)
		announcementID, err := artifact.MintExchangeIDAt("XA", deps.OwnSystem, now, bytes.NewReader(announcementSeed))
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot mint announcement id: %w", err)
		}
		announcementDraft, err := template.Render(template.Input{
			Type: "announcement", ID: announcementID, Actor: resolved, Created: now,
			Fields: map[string]string{
				"from":     deps.OwnSystem,
				"category": "deprecation",
			},
		})
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: render announcement failed: %w", err)
		}
		announcementDraft, err = contractAddFrontmatterFields(announcementDraft, map[string]any{
			"ack_requested": true,
			"deprecates":    in.ID + "@" + deprecatedVersion,
			"valid_until":   in.Sunset,
		})
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		announcementPublishEventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot mint event id: %w", err)
		}
		announcementPublishEvent := eventDoc{
			Schema: "event/v1", Event: announcementPublishEventID.String(), Space: probe.Space,
			Subject: announcementID, Transition: fold.TPublish,
			Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
		}
		announcementPublishRaw, merr := yaml.Marshal(announcementPublishEvent)
		if merr != nil {
			return nil, "", fmt.Errorf("contract deprecate: cannot encode announcement publish event: %w", merr)
		}

		files := []space.FileWrite{
			{Path: layout.EventFile(now.UTC().Format("2006"), deprecateEventID.String()), Content: deprecateRaw},
			{Path: layout.Exchange(announcementID), Content: announcementDraft},
			{Path: layout.EventFile(now.UTC().Format("2006"), announcementPublishEventID.String()), Content: announcementPublishRaw},
		}

		req := deps.buildRequest([]string{in.ID, announcementID}, files, "contract-deprecate", false)
		result, err := deps.submit(ctx, req, "contract deprecate", []string{in.ID, announcementID})
		return result, "", err
	}
}

// ContractRetireInput is a2a_contract_retire's structured input.
type ContractRetireInput struct {
	ID       string     `json:"id"`
	Version  string     `json:"version,omitempty"`
	Override bool       `json:"override,omitempty"`
	Actor    ActorInput `json:"actor,omitempty"`
}

func newContractRetireHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractRetireInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract retire: invalid input: %w", err)
		}
		if in.ID == "" {
			return nil, "", fmt.Errorf("contract retire: id is required")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		verdict, _, err := checkLegality(deps.MirrorDir, deps.Manifest, in.ID, fold.TRetire, actor)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %s: %w", in.ID, err)
		}
		if verdict != fold.VerdictLegal {
			return nil, "", fmt.Errorf("contract retire: %w", verdictError(in.ID, verdict))
		}
		_, probe, _, _, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		retiredVersion := in.Version
		if retiredVersion == "" {
			retiredVersion = probe.Version
		}

		now := deps.Now()

		precondition, err := contractBuildRetirePrecondition(deps.MirrorDir, deps.Manifest, in.ID, retiredVersion, in.Override, resolved.Kind == "human", now)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		violation, overridden := validate.CheckRetirePrecondition(precondition)
		if violation != nil {
			return nil, "", fmt.Errorf("contract retire: %s: refused: %s (%s)", in.ID, violation.Message, violation.Code)
		}

		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		eventID, err := artifact.MintULIDAt(now, deps.Entropy)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: cannot mint event id: %w", err)
		}
		note := ""
		if len(overridden) > 0 {
			note = "retired-unacked: " + strings.Join(overridden, ", ")
		}
		ev := eventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: in.ID, Transition: fold.TRetire, Version: retiredVersion,
			Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
			Note:  note,
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			return nil, "", fmt.Errorf("contract retire: cannot encode event: %w", merr)
		}
		files := []space.FileWrite{{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw}}

		gated := len(overridden) > 0
		req := deps.buildRequest([]string{in.ID}, files, "contract-retire", gated)
		result, err := deps.submit(ctx, req, "contract retire", []string{in.ID})
		return result, "", err
	}
}

func contractBuildRetirePrecondition(mirrorDir string, manifest space.Manifest, contractID, version string, override, actorIsHuman bool, now time.Time) (validate.RetirePrecondition, error) {
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	announcementID, sunset, err := contractFindDeprecationAnnouncement(mirrorDir, contractID, version)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	memb := membership(manifest)
	var ackedSystems map[string]bool
	var reminderCount int
	if announcementID != "" {
		events := foldEvents(all, announcementID)
		result := fold.Fold(fold.KindAnnouncement, fold.Envelope{ID: announcementID, Kind: fold.KindAnnouncement}, events, memb)
		ackedSystems = map[string]bool{}
		for _, s := range result.AckedRecipients() {
			ackedSystems[s] = true
		}
		for _, ev := range all {
			if ev.Subject == announcementID && ev.Transition == fold.TNote {
				reminderCount++
			}
		}
	}

	consumerSystems, err := contractFindRegisteredConsumers(mirrorDir, contractID)
	if err != nil {
		return validate.RetirePrecondition{}, err
	}

	consumers := make([]validate.RegisteredConsumer, 0, len(consumerSystems))
	for sys := range consumerSystems {
		left := memb(sys) == fold.MembershipLeft
		consumers = append(consumers, validate.RegisteredConsumer{System: sys, Acked: ackedSystems[sys], Left: left})
	}
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].System < consumers[j].System })

	return validate.RetirePrecondition{
		Consumers:    consumers,
		SunsetPassed: sunset != "" && contractSunsetPassed(sunset, now),
		HasReminder:  reminderCount > 0,
		ActorIsHuman: actorIsHuman,
		Override:     override,
	}, nil
}

func contractSunsetPassed(sunset string, now time.Time) bool {
	t, err := time.Parse("2006-01-02", sunset)
	if err != nil {
		return false
	}
	return now.UTC().After(t)
}

func contractFindDeprecationAnnouncement(mirrorDir, contractID, version string) (id, sunset string, err error) {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "exchanges", "XA-*.md"))
	if err != nil {
		return "", "", err
	}
	want := contractID + "@" + version
	for _, m := range matches {
		raw, rerr := readBoundedFile(m, maxMirrorEventBytes)
		if rerr != nil {
			return "", "", rerr
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			continue
		}
		var probe struct {
			ID         string `yaml:"id"`
			Deprecates string `yaml:"deprecates"`
			ValidUntil string `yaml:"valid_until"`
		}
		if yaml.Unmarshal(fm.YAML, &probe) == nil && probe.Deprecates == want {
			return probe.ID, probe.ValidUntil, nil
		}
	}
	return "", "", nil
}

func contractFindRegisteredConsumers(mirrorDir, contractID string) (map[string]bool, error) {
	out := map[string]bool{}

	reqMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "requires", "XR-*.md"))
	if err != nil {
		return nil, err
	}
	for _, m := range reqMatches {
		raw, rerr := readBoundedFile(m, maxMirrorEventBytes)
		if rerr != nil {
			return nil, rerr
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			continue
		}
		var probe struct {
			ID             string `yaml:"id"`
			From           string `yaml:"from"`
			TargetContract string `yaml:"target_contract"`
		}
		if yaml.Unmarshal(fm.YAML, &probe) != nil || probe.TargetContract != contractID {
			continue
		}
		all, aerr := readAllEvents(mirrorDir)
		if aerr != nil {
			return nil, aerr
		}
		events := foldEvents(all, probe.ID)
		var state fold.State
		if len(events) == 0 {
			state = fold.NewResult(fold.KindRequirement).State
		} else {
			state = fold.Fold(fold.KindRequirement, fold.Envelope{ID: probe.ID, Kind: fold.KindRequirement, From: probe.From}, events, func(string) fold.MembershipStatus { return fold.MembershipMember }).State
		}
		if state == fold.StateSatisfied {
			out[probe.From] = true
		}
	}

	consumesMatches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "consumes.yaml"))
	if err != nil {
		return nil, err
	}
	for _, m := range consumesMatches {
		raw, rerr := readBoundedFile(m, maxMirrorEventBytes)
		if rerr != nil {
			return nil, rerr
		}
		var doc struct {
			System       string `yaml:"system"`
			Dependencies []struct {
				Contract string `yaml:"contract"`
			} `yaml:"dependencies"`
		}
		if yaml.Unmarshal(raw, &doc) != nil {
			continue
		}
		for _, d := range doc.Dependencies {
			if d.Contract == contractID {
				out[doc.System] = true
			}
		}
	}
	return out, nil
}

// ContractDiffInput is a2a_contract_diff's structured (read-only) input.
type ContractDiffInput struct {
	ID string `json:"id"`
	V1 string `json:"v1"`
	V2 string `json:"v2"`
}

func newContractDiffHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractDiffInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract diff: invalid input: %w", err)
		}
		if in.ID == "" || in.V1 == "" || in.V2 == "" {
			return nil, "", fmt.Errorf("contract diff: id, v1 and v2 are required")
		}
		if in.V1 == in.V2 {
			return nil, "", fmt.Errorf("contract diff: v1 and v2 are the same version")
		}

		_, _, relPath, relDir, err := contractReadDescriptor(deps.MirrorDir, in.ID)
		if err != nil {
			return nil, "", fmt.Errorf("contract diff: %w", err)
		}

		sha1, err := contractResolveVersionSHA(ctx, deps.MirrorDir, relPath, in.V1)
		if err != nil {
			return nil, "", fmt.Errorf("contract diff: %s: %w", in.V1, err)
		}
		sha2, err := contractResolveVersionSHA(ctx, deps.MirrorDir, relPath, in.V2)
		if err != nil {
			return nil, "", fmt.Errorf("contract diff: %s: %w", in.V2, err)
		}

		tree1, err := contractDigestTreeAtSHA(ctx, deps.MirrorDir, sha1, relDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract diff: %w", err)
		}
		tree2, err := contractDigestTreeAtSHA(ctx, deps.MirrorDir, sha2, relDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract diff: %w", err)
		}

		return contractDiff(tree1, tree2), "", nil
	}
}

// ContractVerifyExportInput is a2a_contract_verify_export's structured
// (read-only) input.
type ContractVerifyExportInput struct {
	Local string `json:"local"`
	Ref   string `json:"ref"`
}

// contractVerifyExportResult is the structured result.
type contractVerifyExportResult struct {
	ID          string           `json:"id"`
	Matches     bool             `json:"matches"`
	LocalDigest string           `json:"local_digest"`
	WantDigest  string           `json:"want_digest"`
	Diff        contractDiffTree `json:"diff,omitempty"`
}

func newContractVerifyExportHandler(deps ContractDeps) HandlerFunc {
	return func(_ context.Context, args json.RawMessage) (any, string, error) {
		var in ContractVerifyExportInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract verify-export: invalid input: %w", err)
		}
		if in.Local == "" || in.Ref == "" {
			return nil, "", fmt.Errorf("contract verify-export: local and ref are required")
		}
		id, version, _ := splitRefGrammar(in.Ref)

		_, probe, _, relDir, err := contractReadDescriptor(deps.MirrorDir, id)
		if err != nil {
			return nil, "", fmt.Errorf("contract verify-export: %w", err)
		}

		var wantDigest string
		if version != "" {
			all, aerr := readAllEvents(deps.MirrorDir)
			if aerr != nil {
				return nil, "", fmt.Errorf("contract verify-export: %w", aerr)
			}
			for _, ev := range all {
				if ev.Subject == id && ev.Transition == fold.TPublish && ev.Version == version {
					wantDigest = ev.Digest
				}
			}
			if wantDigest == "" {
				return nil, "", fmt.Errorf("contract verify-export: no recorded digest for %s@%s", id, version)
			}
		} else {
			wantDigest = probe.GeneratedFrom.SourceDigest
			if wantDigest == "" {
				return nil, "", fmt.Errorf("contract verify-export: %s has no generated_from.source_digest recorded", id)
			}
		}

		localDigest, localPerFile, err := artifact.DigestTreeFS(in.Local, contractDigestSubtrees)
		if err != nil {
			return nil, "", fmt.Errorf("contract verify-export: %w", err)
		}
		if localDigest == wantDigest {
			return contractVerifyExportResult{ID: id, Matches: true, LocalDigest: localDigest, WantDigest: wantDigest}, "", nil
		}

		var diff contractDiffTree
		_, spacePerFile, serr := artifact.DigestTreeFS(filepath.Join(deps.MirrorDir, relDir), contractDigestSubtrees)
		if serr == nil {
			diff = contractDiff(spacePerFile, localPerFile)
		}
		return contractVerifyExportResult{ID: id, Matches: false, LocalDigest: localDigest, WantDigest: wantDigest, Diff: diff}, "", nil
	}
}
