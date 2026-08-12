package cli

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// DoctorClassificationSummary is the complete classification fact doctor
// consumes from P1's cache projection. It deliberately contains no visibility
// policy: P1 reports the highest decoded classification and every skipped path;
// doctor alone decides how those facts relate to repository transport.
type DoctorClassificationSummary struct {
	Highest      string
	SkippedCount int
	SkippedPaths []string
}

// DoctorClassificationSummaryReader is defined by the consumer so doctor does
// not depend on cache's concrete store or scan representation. Lead wiring may
// adapt P1's cache summary after both slices land.
type DoctorClassificationSummaryReader interface {
	ClassificationSummary(ctx context.Context, spaceID string) (DoctorClassificationSummary, error)
}

type doctorVisibilityStatus string

const (
	doctorVisibilityPASS       doctorVisibilityStatus = "PASS"
	doctorVisibilityWARN       doctorVisibilityStatus = "WARN"
	doctorVisibilityUNVERIFIED doctorVisibilityStatus = "UNVERIFIED"
)

type doctorVisibilityFacts struct {
	Visibility             host.RepositoryVisibility
	VisibilityVerified     bool
	Classification         string
	ClassificationVerified bool
	SkippedCount           int
	SkippedPaths           []string
	BilateralProofSource   string
}

type doctorVisibilityDecision struct {
	Status doctorVisibilityStatus
	Detail string
}

type doctorVisibilityRow struct {
	SpaceID string
	doctorVisibilityDecision
}

// doctorDecideVisibility is P4 §5.2's pure advisory matrix. It never grants
// confidentiality or changes write authority.
func doctorDecideVisibility(facts doctorVisibilityFacts) doctorVisibilityDecision {
	if !facts.ClassificationVerified {
		return doctorVisibilityDecision{
			Status: doctorVisibilityUNVERIFIED,
			Detail: "classification summary could not be verified",
		}
	}

	if facts.SkippedCount > 0 || len(facts.SkippedPaths) > 0 {
		count := facts.SkippedCount
		if count < len(facts.SkippedPaths) {
			count = len(facts.SkippedPaths)
		}
		safe := doctorSafeVisibilityPaths(facts.SkippedPaths)
		detail := fmt.Sprintf("classification scan incomplete · %d skipped", count)
		if len(safe) > 0 {
			detail += ": " + strings.Join(safe, ", ")
		} else {
			detail += ": no safe local path was available to display"
		}
		if omitted := count - len(safe); omitted > 0 {
			detail += fmt.Sprintf(" · %d path(s) omitted from display", omitted)
		}
		return doctorVisibilityDecision{Status: doctorVisibilityUNVERIFIED, Detail: detail}
	}

	if !facts.VisibilityVerified {
		return doctorVisibilityDecision{
			Status: doctorVisibilityUNVERIFIED,
			Detail: "visibility could not be verified",
		}
	}

	classification := strings.ToLower(strings.TrimSpace(facts.Classification))
	if classification != "public" && classification != "internal" && classification != "restricted" {
		return doctorVisibilityDecision{
			Status: doctorVisibilityUNVERIFIED,
			Detail: "classification summary could not be verified",
		}
	}

	switch facts.Visibility {
	case host.RepositoryVisibilityPublic:
		if classification == "public" {
			return doctorVisibilityDecision{Status: doctorVisibilityPASS, Detail: "transport matches classification"}
		}
		return doctorVisibilityDecision{Status: doctorVisibilityWARN, Detail: "public repository carries higher-classified material"}
	case host.RepositoryVisibilityPrivate, host.RepositoryVisibilityInternal:
		if classification != "restricted" {
			return doctorVisibilityDecision{Status: doctorVisibilityPASS, Detail: "no observed mismatch"}
		}
		proofSource := strings.TrimSpace(facts.BilateralProofSource)
		if proofSource != "" && !strings.ContainsFunc(proofSource, unicode.IsControl) {
			return doctorVisibilityDecision{Status: doctorVisibilityPASS, Detail: "restricted audience proof source: " + proofSource}
		}
		return doctorVisibilityDecision{
			Status: doctorVisibilityUNVERIFIED,
			Detail: "repository privacy alone does not prove intended pairwise audience",
		}
	default:
		return doctorVisibilityDecision{
			Status: doctorVisibilityUNVERIFIED,
			Detail: "visibility could not be verified",
		}
	}
}

// doctorFilterDataPackageReadmeSkips removes a data package's own README.md
// skip entries from paths (and the parallel count) before
// doctorDecideVisibility ever sees them. Such a file is not an unverified
// classification gap — it IS classifiable: `a2a data pack` writes it WITHOUT
// frontmatter BY DESIGN (spec 04 §11's 2026-08-09 AC8 amendment), and its
// bytes are sealed by the package's own manifest.json digest
// (datapackage.BuildEntrySet) — no owner can make it frontmatter-shaped
// without breaking that seal, so naming it in the "classification scan
// incomplete" remediation line (fb-20260812-f9cfac) points at an action
// nobody can take. artifact.IsDataPackageReadmePath is the SAME predicate
// internal/cache's walkArtifacts uses to silently skip this exact file in
// the first place (internal/artifact/paths.go, fb-20260812-d31acb's own
// fix) — one rule, both readers, rather than a second copy that could drift.
//
// count may legitimately exceed len(paths) (doctorDecideVisibility's own
// "count := max(SkippedCount, len(SkippedPaths))" defends against a
// truncated path list upstream), so this only ever subtracts the number of
// PATHS it actually recognised and removed, never more, and never below
// zero.
func doctorFilterDataPackageReadmeSkips(paths []string, count int) ([]string, int) {
	filtered := make([]string, 0, len(paths))
	removed := 0
	for _, p := range paths {
		if artifact.IsDataPackageReadmePath(p) {
			removed++
			continue
		}
		filtered = append(filtered, p)
	}
	count -= removed
	if count < 0 {
		count = 0
	}
	return filtered, count
}

func doctorSafeVisibilityPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		candidate := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
		if candidate == "" || strings.ContainsFunc(candidate, unicode.IsControl) {
			continue
		}
		candidate = path.Clean(candidate)
		if candidate == "." || candidate == ".." || strings.HasPrefix(candidate, "../") || strings.HasPrefix(candidate, "/") {
			continue
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

// doctorVisibilityRows joins the two read-only evidence providers. No current
// production source proves bilateral restricted audiences, so the proof field
// remains empty and restricted classifications stay UNVERIFIED until lead
// wiring can name a genuinely authoritative source.
func (c *DoctorCommand) doctorVisibilityRows(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig) []doctorVisibilityRow {
	refs := append([]space.Ref(nil), cfg.Spaces...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	rows := make([]doctorVisibilityRow, 0, len(refs))
	visibilityReader, canReadVisibility := c.h.(host.RepoVisibilityReader)

	for _, ref := range refs {
		facts := doctorVisibilityFacts{}
		if c.ClassificationSummaryReader != nil {
			summary, err := c.ClassificationSummaryReader.ClassificationSummary(ctx, ref.ID)
			if err == nil {
				facts.Classification = summary.Highest
				facts.ClassificationVerified = true
				facts.SkippedCount = summary.SkippedCount
				facts.SkippedPaths = append([]string(nil), summary.SkippedPaths...)
				facts.SkippedPaths, facts.SkippedCount = doctorFilterDataPackageReadmeSkips(facts.SkippedPaths, facts.SkippedCount)
			}
		}

		if canReadVisibility {
			owner, name, err := doctorRepoOwnerName(ref.RepoURL)
			if err == nil {
				var parsedRef space.CredentialReference
				if raw, present := machine.Credentials[ref.ID]; present {
					if parsed, parseErr := space.ParseCredentialReference(raw); parseErr == nil {
						parsedRef = parsed
					}
				}
				credential, credentialErr := c.resolveCredential(ctx, space.CredentialEnvVar(ref.ID), parsedRef)
				if credentialErr == nil {
					visibility, visibilityErr := visibilityReader.RepoVisibility(ctx, host.RepoSettingsRequest{
						Repo: host.Repo{Owner: owner, Name: name}, Credential: credential,
					})
					if visibilityErr == nil {
						facts.Visibility = visibility
						facts.VisibilityVerified = true
					}
				}
			}
		}

		rows = append(rows, doctorVisibilityRow{
			SpaceID: ref.ID, doctorVisibilityDecision: doctorDecideVisibility(facts),
		})
	}
	return rows
}
