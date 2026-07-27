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
	"io/fs"
	"os"
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
	ID      string   `yaml:"id"`
	Space   string   `yaml:"space"`
	From    string   `yaml:"from"`
	To      []string `yaml:"to"`
	Version string   `yaml:"version"`
	// Thread is the contract's own §3.8 thread id — spec 46 §T1 R2: the
	// deprecation announcement is DERIVED from this contract and inherits
	// it. Mirrors internal/cli's contractDescriptorProbe field.
	Thread        string `yaml:"thread"`
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

// contractReadWorkingTreeFiles mirrors internal/cli's own copy
// (cmd_contract.go, same name, same behaviour) — ADR-001: internal/mcp
// never imports internal/cli, so this package carries its own file-private
// copy rather than a shared one. Reads every regular file under
// filepath.Join(root, sub), keyed "<sub>/<path-relative-to-root>"
// (forward-slash). A missing directory is not an error — an empty map.
func contractReadWorkingTreeFiles(root, sub string) (map[string][]byte, error) {
	dir := filepath.Join(root, sub)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]byte{}, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return map[string][]byte{}, nil
	}
	out := map[string][]byte{}
	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		raw, rerr := readBoundedFile(p, maxMirrorEventBytes)
		if rerr != nil {
			return rerr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out[filepath.ToSlash(rel)] = raw
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// contractStagingOverlay mirrors internal/cli's own copy (cmd_contract.go,
// same name, same PER-FILE-not-per-directory merge, same "absence in
// staging means unchanged, never removed" rule) — see that copy's own doc
// comment for the full rationale; ADR-001 duplication, not a second source
// of truth (the equivalence suite, cmd/a2a/mcp_equivalence_test.go's
// TestEquivContractPublish, keeps the two behaviours honest).
func contractStagingOverlay(landed map[string][]byte, staged []space.FileWrite, relDir string) map[string][]byte {
	out := make(map[string][]byte, len(landed))
	for k, v := range landed {
		out[k] = v
	}
	prefix := relDir + "/"
	for _, sc := range staged {
		if !strings.HasPrefix(sc.Path, prefix) {
			continue
		}
		out[strings.TrimPrefix(sc.Path, prefix)] = sc.Content
	}
	return out
}

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
// (diff/verify-export). StagingDir is P37 Wave I's own addition: `publish`
// needs it to fold a staged schema/fixture edit into the version it is
// declaring (see newContractPublishHandler's own doc comment) — empty
// (the zero value, e.g. any existing test construction that does not set
// it) degrades to exactly the pre-wave behaviour, never a panic.
type ContractDeps struct {
	WriteDeps
	StagingDir string
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

		// P37 Wave I (parity with internal/cli's own runPublish, mirrored
		// here per ADR-001): internal/space/mirror.go's checkoutRemoteHead
		// hard-resets the mirror working tree to origin/<branch> on EVERY
		// `a2a` invocation, so by the time this handler runs the mirror can
		// never carry an author's own schema/fixture edit — only
		// deps.StagingDir (never touched by the reset) still can.
		// deps.StagingDir is empty on any construction that predates this
		// wave (the zero value), which degrades this to exactly the
		// pre-wave behaviour: overlayAll below then equals landedAll,
		// unmodified. NOTE: unlike internal/cli's own runPublish, this
		// handler does NOT run POL-009/POL-007/POL-008 locally (a filed,
		// pre-existing asymmetry backstopped by CI — not this wave's to
		// fix); the overlay/carry/digest fix below applies regardless.
		landedSchema, err := contractReadWorkingTreeFiles(filepath.Join(deps.MirrorDir, relDir), "schema")
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		landedFixtures, err := contractReadWorkingTreeFiles(filepath.Join(deps.MirrorDir, relDir), "fixtures")
		if err != nil {
			return nil, "", fmt.Errorf("contract publish: %w", err)
		}
		landedAll := make(map[string][]byte, len(landedSchema)+len(landedFixtures))
		for k, v := range landedSchema {
			landedAll[k] = v
		}
		for k, v := range landedFixtures {
			landedAll[k] = v
		}
		var stagedSidecars []space.FileWrite
		if deps.StagingDir != "" {
			parsed, perr := artifact.ParseID(in.ID)
			if perr != nil {
				return nil, "", fmt.Errorf("contract publish: %w", perr)
			}
			stagedSidecars, err = template.ContractSidecarsFromStaging(deps.StagingDir, parsed.System, parsed.Slug)
			if err != nil {
				return nil, "", fmt.Errorf("contract publish: %w", err)
			}
		}
		overlayAll := contractStagingOverlay(landedAll, stagedSidecars, relDir)

		files := []space.FileWrite{{Path: relPath, Content: newRaw}}
		files = append(files, stagedSidecars...)

		// §5.7/D-029 multi-file digest tree, computed from overlayAll — see
		// internal/cli's own runPublish for why this uses artifact.Digest +
		// artifact.CombineDigestPairs directly rather than
		// artifact.DigestTreeFS (which cannot see the staged override).
		perFileDigest := make(map[string]string, len(overlayAll))
		for k, v := range overlayAll {
			perFileDigest[k] = artifact.Digest(v)
		}
		digest := artifact.CombineDigestPairs(perFileDigest)

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

// contractDistinctPublishedVersions dedupes contractPublishedVersions' own
// per-EVENT list into the distinct SET of versions ever published (mirrors
// internal/cli's own copy — ADR-001: internal/mcp never imports
// internal/cli). contractPublishedVersions already returns its slice sorted
// ascending, so dedup-by-adjacency preserves that order.
func contractDistinctPublishedVersions(all []eventDoc, id string) []contractSemver {
	sorted := contractPublishedVersions(all, id)
	out := make([]contractSemver, 0, len(sorted))
	for i, v := range sorted {
		if i > 0 && v == sorted[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}

// contractResolveVersionOrRefuse is F4/AC-972.1 on the MCP surface: `deprecate`
// and `retire` both used to default an omitted version to the descriptor's
// CURRENT version — after a `bump: major` publish that is the NEW version, so
// the OLD one silently got no announcement at all. With exactly one distinct
// published version, defaulting to currentVersion is unambiguous and stays;
// with MORE than one and explicit == "", this REFUSES with an error listing
// every published version, oldest first, so the caller can retry with an
// explicit version (mirrors internal/cli's own copy — ADR-001: internal/mcp
// never imports internal/cli). The MCP surface returns errors rather than
// exit codes, so unlike the CLI's exit-2 usage error this is just the
// returned error.
func contractResolveVersionOrRefuse(all []eventDoc, id, explicit, currentVersion string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	versions := contractDistinctPublishedVersions(all, id)
	if len(versions) <= 1 {
		return currentVersion, nil
	}
	strs := make([]string, len(versions))
	for i, v := range versions {
		strs[i] = v.String()
	}
	return "", fmt.Errorf("mcp: version is required: %s has %d published versions (%s) — say which one", id, len(versions), strings.Join(strs, ", "))
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
		allEvents, err := readAllEvents(deps.MirrorDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}
		deprecatedVersion, err := contractResolveVersionOrRefuse(allEvents, in.ID, in.Version, probe.Version)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
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
		// F3/T4 (AC-971.1, AC-971.2): the announcement's addressees are the
		// registered-consumer set — the SAME contractFindRegisteredConsumers
		// query the retire precondition reads — not the descriptor's own
		// authoring-time `to:`. "Who blocks my retire" and "who was told"
		// become one query instead of two that can drift apart (mirrors
		// internal/cli's own copy — ADR-001: internal/mcp never imports
		// internal/cli).
		to, err := contractDeprecateAddressees(deps.MirrorDir, in.ID, probe.From, probe.To)
		if err != nil {
			return nil, "", fmt.Errorf("contract deprecate: %w", err)
		}

		// Spec 46 §T1 R2 + the threadless refusal, mirroring internal/cli's
		// ContractDeprecateCommand exactly (ADR-001: this package may never
		// import internal/cli, so the rule is written twice and
		// cmd/a2a/mcp_equivalence_test.go is what keeps the copies honest —
		// it went red the moment only the CLI half propagated, which is the
		// parity suite doing precisely its job).
		if probe.Thread == "" {
			return nil, "", fmt.Errorf(
				"contract deprecate: %s carries no thread, so its deprecation announcement has no conversation to join; "+
					"that contract predates thread propagation — reseed the space or republish it with this version", in.ID)
		}

		announcementDraft, err = contractAddFrontmatterFields(announcementDraft, map[string]any{
			// space/to/title are the template's own PLACEHOLDERS and
			// template.Render fills none of them, so every deprecation
			// announcement was authored with a literal `to:
			// [<recipient-system>]` and refused by V2 (REF-006). `to` used
			// to be filled from probe.To for the same reason — it is now
			// `to` computed above (F3), which falls back to probe.To only
			// when the registry has no registered consumers yet. Mirrors
			// internal/cli's copy (ADR-001's deliberate duplication).
			"space":         probe.Space,
			"to":            to,
			"title":         fmt.Sprintf("Deprecating %s@%s (sunset %s)", in.ID, deprecatedVersion, in.Sunset),
			"ack_requested": true,
			"deprecates":    in.ID + "@" + deprecatedVersion,
			"valid_until":   in.Sunset,
			"thread":        probe.Thread,
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
		allEvents, err := readAllEvents(deps.MirrorDir)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}
		retiredVersion, err := contractResolveVersionOrRefuse(allEvents, in.ID, in.Version, probe.Version)
		if err != nil {
			return nil, "", fmt.Errorf("contract retire: %w", err)
		}

		// P2/spec 02: same refusal as internal/cli's runRetire — mirrors
		// it exactly (mcp never imports cli, ADR-001) so a capability
		// that refuses on one surface only is not the asymmetry P43
		// exists to close. Reuses the SAME allEvents scan above.
		if v := validate.CheckRetireVersionScope(contractVersionEvents(allEvents), in.ID, retiredVersion); v != nil {
			return nil, "", fmt.Errorf("contract retire: %s: %s", v.Code, v.Message)
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
		registry, cerr := contractParseConsumesStrict(raw, m)
		if cerr != nil {
			// FAIL CLOSED — see internal/cli's own copy: an unreadable
			// registry must never round down to "consumes nothing", or a
			// retire runs out from under a subscribed system.
			return nil, cerr
		}
		for _, d := range registry.Dependencies {
			if d.Contract == contractID {
				out[registry.System] = true
			}
		}
	}
	return out, nil
}

// contractDeprecateAddressees is F3/T4 (AC-971.1, AC-971.2): who a
// deprecation announcement is addressed to. Computed from the SAME
// contractFindRegisteredConsumers query the retire precondition reads —
// "who blocks retire" and "who was told" are one query, not two that can
// silently disagree. Sorted (contractFindRegisteredConsumers returns a
// map), deduped, and excludes the contract's OWN `from` system — a producer
// does not address itself.
//
// An EMPTY registered-consumer set (nobody has adopted this contract yet)
// falls back to fallback (the descriptor's own authoring-time `to:`):
// schemas/envelope/v1/base.schema.json's own `to` requires either
// `minItems: 1` or the literal `"all"`, so `to: []` is refused by V2
// (REF-006), and is not an option. A fallback that is ALSO empty/nil is
// refused with an actionable error rather than silently authoring an
// invalid `to: null` announcement. Mirrors internal/cli's own copy —
// ADR-001: internal/mcp never imports internal/cli.
func contractDeprecateAddressees(mirrorDir, contractID, from string, fallback []string) ([]string, error) {
	consumers, err := contractFindRegisteredConsumers(mirrorDir, contractID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(consumers))
	for sys := range consumers {
		if sys == from {
			continue
		}
		out = append(out, sys)
	}
	sort.Strings(out)
	if len(out) > 0 {
		return out, nil
	}
	if len(fallback) == 0 {
		return nil, fmt.Errorf("mcp: %s has no registered consumers and no fallback recipients (descriptor `to:` is empty) — nobody to address the deprecation to", contractID)
	}
	return fallback, nil
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

// ContractAdoptInput is a2a_contract action=adopt's structured input —
// the MCP twin of `a2a contract adopt` (internal/cli's runAdopt).
type ContractAdoptInput struct {
	ID    string `json:"id"`
	Major int    `json:"major,omitempty"`
	Note  string `json:"note,omitempty"`
}

// newContractAdoptHandler registers this system as a CONSUMER of another
// system's contract: it writes the dependency into <own-system>/
// consumes.yaml (§5.2.3, D-022 — the space-visible registry a producer's
// retire has to wait for) and submits it through the same write funnel as
// every other mutation. Mirrors internal/cli's runAdopt exactly; the
// duplication is ADR-001's (internal/mcp never imports internal/cli).
func newContractAdoptHandler(deps ContractDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in ContractAdoptInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("contract adopt: invalid input: %w", err)
		}
		parsed, perr := artifact.ParseID(in.ID)
		if perr != nil || parsed.Prefix != "XC" {
			return nil, "", fmt.Errorf("contract adopt: %q is not a contract id (XC-<system>-<slug>)", in.ID)
		}
		if parsed.System == deps.OwnSystem {
			return nil, "", fmt.Errorf("contract adopt: %s is this system's OWN contract — the registry records what you consume from OTHERS (§5.2.3)", in.ID)
		}

		major := in.Major
		if major == 0 {
			_, probe, _, _, rerr := contractReadDescriptor(deps.MirrorDir, in.ID)
			if rerr != nil {
				return nil, "", fmt.Errorf("contract adopt: cannot read %s from the local mirror — sync first, or pass major: %w", in.ID, rerr)
			}
			v, verr := contractParseSemver(probe.Version)
			if verr != nil {
				return nil, "", fmt.Errorf("contract adopt: %s has no usable published version (%q): %w", in.ID, probe.Version, verr)
			}
			major = v[0]
		}

		layout, lerr := space.NewLayout(deps.OwnSystem)
		if lerr != nil {
			return nil, "", fmt.Errorf("contract adopt: %w", lerr)
		}
		relPath := layout.ConsumesYAML()

		registry := space.Consumes{Schema: "consumes/v1", System: deps.OwnSystem, Dependencies: []space.Dependency{}}
		if raw, rerr := readBoundedFile(filepath.Join(deps.MirrorDir, relPath), maxMirrorEventBytes); rerr == nil {
			parsedRegistry, cerr := space.ParseConsumes(raw)
			if cerr != nil {
				return nil, "", fmt.Errorf("contract adopt: cannot parse %s: %w", relPath, cerr)
			}
			registry = parsedRegistry
			if registry.Schema == "" {
				registry.Schema = "consumes/v1"
			}
			if registry.System == "" {
				registry.System = deps.OwnSystem
			}
		}

		updated, changed := contractUpsertDependency(registry, space.Dependency{
			Contract: in.ID, Major: major,
			Since: deps.Now().UTC().Format("2006-01-02"),
			Note:  in.Note,
		})
		if !changed {
			return contractAdoptResult{ID: in.ID, Major: major, AlreadyRegistered: true}, "", nil
		}

		raw, merr := yaml.Marshal(updated)
		if merr != nil {
			return nil, "", fmt.Errorf("contract adopt: cannot encode %s: %w", relPath, merr)
		}

		req := deps.buildRequest([]string{in.ID}, []space.FileWrite{{Path: relPath, Content: raw}}, "contract-adopt", false)
		result, err := deps.submit(ctx, req, "contract adopt", []string{in.ID})
		return result, "", err
	}
}

// contractAdoptResult is the idempotent-no-op shape (a real write returns
// the funnel's own submit result, like every other write handler).
type contractAdoptResult struct {
	ID                string `json:"id"`
	Major             int    `json:"major"`
	AlreadyRegistered bool   `json:"already_registered"`
}

// contractUpsertDependency adds or updates dep in registry, keeping the
// list sorted by contract id, and reports changed=false for the
// idempotent re-run (same contract, same major).
func contractUpsertDependency(registry space.Consumes, dep space.Dependency) (space.Consumes, bool) {
	for i, existing := range registry.Dependencies {
		if existing.Contract != dep.Contract {
			continue
		}
		if existing.Major == dep.Major && (dep.Note == "" || existing.Note == dep.Note) {
			return registry, false
		}
		// `since` records when the dependency was DECLARED: only a real
		// major change restarts it (mirrors internal/cli's own copy).
		if registry.Dependencies[i].Major != dep.Major {
			registry.Dependencies[i].Major = dep.Major
			registry.Dependencies[i].Since = dep.Since
		}
		if dep.Note != "" {
			registry.Dependencies[i].Note = dep.Note
		}
		return registry, true
	}
	registry.Dependencies = append(registry.Dependencies, dep)
	sort.Slice(registry.Dependencies, func(i, j int) bool {
		return registry.Dependencies[i].Contract < registry.Dependencies[j].Contract
	})
	return registry, true
}

// contractParseConsumesStrict parses a committed consumes.yaml and refuses
// anything that is not a real consumes/v1 registry (mirrors internal/cli's
// own copy — ADR-001: internal/mcp never imports internal/cli). A
// wrong-shaped file like `consumes: []` unmarshals cleanly into a
// zero-valued struct, which is indistinguishable from "no dependencies".
func contractParseConsumesStrict(raw []byte, path string) (space.Consumes, error) {
	registry, err := space.ParseConsumes(raw)
	if err != nil {
		return space.Consumes{}, fmt.Errorf("mcp: %s is not valid yaml: %w", path, err)
	}
	if registry.Schema != "consumes/v1" || registry.System == "" {
		return space.Consumes{}, fmt.Errorf(
			"mcp: %s is not a consumes/v1 registry (needs `schema: consumes/v1`, `system: <id>`, `dependencies: [...]`) — "+
				"refusing to treat it as \"no registered consumers\"; fix the file (or write it with contract adopt)", path)
	}
	return registry, nil
}

// contractVersionEvents projects this file's decoded events onto the neutral
// shape validate.CheckRetireVersionScope reads — see internal/cli's twin.
// The mapping is per-surface; the rule it feeds has one home.
func contractVersionEvents(all []eventDoc) []validate.ContractVersionEvent {
	out := make([]validate.ContractVersionEvent, 0, len(all))
	for _, ev := range all {
		out = append(out, validate.ContractVersionEvent{Subject: ev.Subject, Transition: ev.Transition, Version: ev.Version})
	}
	return out
}
