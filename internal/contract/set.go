package contract

import (
	"bytes"
	"fmt"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+*-]*/[a-z0-9][a-z0-9!#$&^_.+*-]*$`)

// BuildCarriedSet is the single pure carried-set builder for every immutable
// digest profile. descriptorRaw and descriptor are used by contract-set-v2;
// legacy contract-tree-v1 ignores them and retains the historical fixed
// schema/** + fixtures/** scope.
//
// No digest is computed when an issue exists. The returned issue order is
// deterministic and independent of descriptor/candidate enumeration order.
func BuildCarriedSet(profile DigestProfile, descriptorRaw []byte, descriptor Descriptor, candidateFiles []CandidateFile) (CarriedSet, []Issue) {
	switch profile {
	case ProfileContractSetV2:
		return buildDeclaredSet(descriptorRaw, descriptor, candidateFiles)
	case ProfileContractTreeV1:
		return buildLegacySet(candidateFiles)
	default:
		return CarriedSet{}, []Issue{{
			Kind:   IssueUnsupportedProfile,
			Detail: fmt.Sprintf("unregistered digest profile %q", profile),
		}}
	}
}

func buildDeclaredSet(descriptorRaw []byte, descriptor Descriptor, candidates []CandidateFile) (CarriedSet, []Issue) {
	issues := make([]Issue, 0)
	validateText(DescriptorPath, descriptorRaw, &issues)

	if len(descriptor.Artifacts) == 0 {
		issues = append(issues, Issue{Kind: IssueTooFewEntries, Detail: "artifacts must contain at least one entry"})
	}
	if len(descriptor.Artifacts) > MaxExplicitFiles {
		issues = append(issues, Issue{
			Kind:   IssueTooManyEntries,
			Detail: fmt.Sprintf("artifacts contains %d entries; maximum is %d", len(descriptor.Artifacts), MaxExplicitFiles),
		})
	}

	declared := make(map[string]Entry, min(len(descriptor.Artifacts), MaxExplicitFiles))
	roles := make(map[Role]int)
	for _, entry := range descriptor.Artifacts {
		entryIssues := validateEntryShape(descriptor.SchemaFormat, entry)
		issues = append(issues, entryIssues...)
		if !cleanPortablePath(entry.Path) {
			continue
		}
		if strings.EqualFold(entry.Path, DescriptorPath) {
			issues = append(issues, Issue{Kind: IssueDescriptorAlias, Path: entry.Path, Detail: "contract.md is implicit and must not be declared"})
		}
		if _, exists := declared[entry.Path]; exists {
			issues = append(issues, Issue{Kind: IssueDuplicatePath, Path: entry.Path, Detail: "path is declared more than once"})
			continue
		}
		declared[entry.Path] = entry
		if knownRole(entry.Role) {
			roles[entry.Role]++
		}
	}
	declaredPaths := make([]string, 0, len(declared)+1)
	declaredPaths = append(declaredPaths, DescriptorPath)
	for path := range declared {
		declaredPaths = append(declaredPaths, path)
	}
	issues = append(issues, caseCollisionIssues(declaredPaths, "declared path")...)
	declaredLower := canonicalLowerPaths(declaredPaths)

	for _, role := range []Role{RoleSchema, RoleValidFixture, RoleInvalidFixture} {
		if roles[role] == 0 {
			issues = append(issues, Issue{
				Kind:   IssueRequiredRoleAbsent,
				Detail: fmt.Sprintf("required role %q is absent", role),
			})
		}
	}

	for _, entry := range declared {
		if !fixtureRole(entry.Role) || entry.ConformsTo == "" || !cleanPortablePath(entry.ConformsTo) {
			continue
		}
		target, exists := declared[entry.ConformsTo]
		if !exists || target.Role != RoleSchema {
			issues = append(issues, Issue{
				Kind:   IssueConformsUnresolved,
				Path:   entry.Path,
				Detail: fmt.Sprintf("conforms_to %q does not name a declared schema entry", entry.ConformsTo),
			})
		}
	}

	discovered := make(map[string]CandidateFile)
	for _, candidate := range candidates {
		if !cleanPortablePath(candidate.Path) {
			issues = append(issues, Issue{Kind: IssueInvalidPath, Path: safeRejectedPath(candidate.Path), Detail: "candidate path is not a clean portable relative path"})
			continue
		}
		if strings.EqualFold(candidate.Path, DescriptorPath) {
			issues = append(issues, Issue{Kind: IssueDescriptorAlias, Path: candidate.Path, Detail: "descriptor bytes must be supplied separately"})
			continue
		}
		if !insideCarriedRoot(candidate.Path) {
			continue
		}
		if _, exists := discovered[candidate.Path]; exists {
			issues = append(issues, Issue{Kind: IssueDuplicatePath, Path: candidate.Path, Detail: "candidate path appears more than once"})
			continue
		}
		lower := strings.ToLower(candidate.Path)
		if _, exact := declared[candidate.Path]; !exact {
			if prior, exists := declaredLower[lower]; exists {
				issues = append(issues, Issue{
					Kind:   IssueCaseCollision,
					Path:   candidate.Path,
					Detail: fmt.Sprintf("ASCII-lowercase candidate path collides with declared %q", prior),
				})
			}
		}
		validateCandidate(candidate, &issues)
		discovered[candidate.Path] = candidate
	}
	discoveredPaths := make([]string, 0, len(discovered))
	for path := range discovered {
		discoveredPaths = append(discoveredPaths, path)
	}
	issues = append(issues, caseCollisionIssues(discoveredPaths, "candidate path")...)
	if len(discovered) > MaxExplicitFiles {
		issues = append(issues, Issue{
			Kind:   IssueTooManyEntries,
			Detail: fmt.Sprintf("discovered %d carried files; maximum is %d", len(discovered), MaxExplicitFiles),
		})
	}

	for path := range declared {
		if _, exists := discovered[path]; !exists {
			issues = append(issues, Issue{Kind: IssueMissingFile, Path: path, Detail: "declared file was not supplied"})
		}
	}
	for path := range discovered {
		if _, exists := declared[path]; !exists {
			issues = append(issues, Issue{Kind: IssueUndeclaredFile, Path: path, Detail: "discovered carried-root file is not declared"})
		}
	}

	aggregateBytes := len(descriptorRaw)
	for path := range declared {
		if candidate, exists := discovered[path]; exists {
			aggregateBytes += len(candidate.Raw)
		}
	}
	if aggregateBytes > MaxAggregateBytes {
		issues = append(issues, Issue{
			Kind:   IssueAggregateTooLarge,
			Detail: fmt.Sprintf("carried bytes total %d; maximum is %d", aggregateBytes, MaxAggregateBytes),
		})
	}

	if len(issues) != 0 {
		sortIssues(issues)
		return CarriedSet{}, issues
	}

	entries := make([]Entry, 0, len(declared))
	for _, entry := range declared {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	bytesByPath := make(map[string][]byte, len(entries)+1)
	bytesByPath[DescriptorPath] = append([]byte(nil), descriptorRaw...)
	for _, entry := range entries {
		bytesByPath[entry.Path] = append([]byte(nil), discovered[entry.Path].Raw...)
	}
	perFile := digestBytes(bytesByPath)
	return CarriedSet{
		Profile:         ProfileContractSetV2,
		Descriptor:      cloneDescriptor(descriptor),
		Entries:         entries,
		PerFileDigest:   perFile,
		AggregateDigest: artifact.CombineDigestPairs(perFile),
		Bytes:           bytesByPath,
	}, nil
}

func buildLegacySet(candidates []CandidateFile) (CarriedSet, []Issue) {
	issues := make([]Issue, 0)
	discovered := make(map[string]CandidateFile)
	aggregateBytes := 0
	for _, candidate := range candidates {
		if !cleanPortablePath(candidate.Path) {
			issues = append(issues, Issue{Kind: IssueInvalidPath, Path: safeRejectedPath(candidate.Path), Detail: "candidate path is not a clean portable relative path"})
			continue
		}
		if !insideLegacyRoot(candidate.Path) {
			continue
		}
		if _, exists := discovered[candidate.Path]; exists {
			issues = append(issues, Issue{Kind: IssueDuplicatePath, Path: candidate.Path, Detail: "candidate path appears more than once"})
			continue
		}
		validateCandidate(candidate, &issues)
		discovered[candidate.Path] = candidate
		aggregateBytes += len(candidate.Raw)
	}
	discoveredPaths := make([]string, 0, len(discovered))
	for path := range discovered {
		discoveredPaths = append(discoveredPaths, path)
	}
	issues = append(issues, caseCollisionIssues(discoveredPaths, "candidate path")...)
	if len(discovered) > MaxExplicitFiles {
		issues = append(issues, Issue{
			Kind:   IssueTooManyEntries,
			Detail: fmt.Sprintf("legacy carried tree contains %d files; maximum is %d", len(discovered), MaxExplicitFiles),
		})
	}
	if aggregateBytes > MaxAggregateBytes {
		issues = append(issues, Issue{
			Kind:   IssueAggregateTooLarge,
			Detail: fmt.Sprintf("carried bytes total %d; maximum is %d", aggregateBytes, MaxAggregateBytes),
		})
	}
	if len(issues) != 0 {
		sortIssues(issues)
		return CarriedSet{}, issues
	}

	paths := make([]string, 0, len(discovered))
	for path := range discovered {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]Entry, 0, len(paths))
	bytesByPath := make(map[string][]byte, len(paths))
	for _, path := range paths {
		entries = append(entries, Entry{Path: path})
		bytesByPath[path] = append([]byte(nil), discovered[path].Raw...)
	}
	perFile := digestBytes(bytesByPath)
	return CarriedSet{
		Profile:         ProfileContractTreeV1,
		Entries:         entries,
		PerFileDigest:   perFile,
		AggregateDigest: artifact.CombineDigestPairs(perFile),
		Bytes:           bytesByPath,
	}, nil
}

// ExportSource returns export-source-v1 over finalized declared schema and
// fixture bytes. Descriptor and artifacts/** companions are excluded.
func (set CarriedSet) ExportSource() (DigestProjection, error) {
	if set.Profile != ProfileContractSetV2 {
		return DigestProjection{}, fmt.Errorf("contract: export-source-v1 requires %s set, got %s", ProfileContractSetV2, set.Profile)
	}
	perFile := make(map[string]string)
	for _, entry := range set.Entries {
		if entry.Role != RoleSchema && !fixtureRole(entry.Role) {
			continue
		}
		digest, exists := set.PerFileDigest[entry.Path]
		if !exists {
			return DigestProjection{}, fmt.Errorf("contract: carried set lacks digest for %s", entry.Path)
		}
		perFile[entry.Path] = digest
	}
	return DigestProjection{
		Profile:       ProfileExportSourceV1,
		PerFileDigest: perFile,
		Digest:        artifact.CombineDigestPairs(perFile),
	}, nil
}

// VerifyGeneratedSource checks a generated descriptor's recorded assertion
// against export-source-v1. The assertion never selects bytes or a profile;
// callers first build the exact finalized set, then compare here.
func (set CarriedSet) VerifyGeneratedSource() []Issue {
	if set.Descriptor.GeneratedFrom == nil {
		return nil
	}
	projection, err := set.ExportSource()
	if err != nil {
		return []Issue{{Kind: IssueUnsupportedProfile, Path: "generated_from.source_digest", Detail: err.Error()}}
	}
	if set.Descriptor.GeneratedFrom.SourceDigest == projection.Digest {
		return nil
	}
	return []Issue{{
		Kind:   IssueDigestMismatch,
		Path:   "generated_from.source_digest",
		Detail: fmt.Sprintf("asserted %s, computed %s using %s", set.Descriptor.GeneratedFrom.SourceDigest, projection.Digest, ProfileExportSourceV1),
	}}
}

// CompatibilityEntries returns only explicitly normative entries. Advisory
// bytes remain present in the set, publication digest, materialization, diff,
// and fixture-suite inputs.
func (set CarriedSet) CompatibilityEntries() []Entry {
	entries := make([]Entry, 0, len(set.Entries))
	for _, entry := range set.Entries {
		if entry.Normative {
			entries = append(entries, entry)
		}
	}
	return entries
}

// VerifyDigest checks a stored publication digest without changing profile
// interpretation. Historical callers must build with the event's resolved
// profile first.
func (set CarriedSet) VerifyDigest(expected string) []Issue {
	if expected == set.AggregateDigest {
		return nil
	}
	return []Issue{{
		Kind:   IssueDigestMismatch,
		Detail: fmt.Sprintf("expected %s, computed %s", expected, set.AggregateDigest),
	}}
}

func validateEntryShape(schemaFormat string, entry Entry) []Issue {
	issues := make([]Issue, 0, 3)
	issuePath := entry.Path
	if !cleanPortablePath(entry.Path) {
		issuePath = safeRejectedPath(entry.Path)
		issues = append(issues, Issue{Kind: IssueInvalidPath, Path: issuePath, Detail: "path must be clean portable ASCII POSIX relative syntax"})
	}
	root, ok := rootForRole(entry.Role)
	if !ok {
		issues = append(issues, Issue{Kind: IssueUnknownRole, Path: issuePath, Detail: "role is not registered"})
	} else if !strings.HasPrefix(entry.Path, root) {
		issues = append(issues, Issue{Kind: IssueWrongRoot, Path: issuePath, Detail: fmt.Sprintf("role %q requires root %q", entry.Role, root)})
	}
	if !validMediaType(entry.MediaType) {
		issues = append(issues, Issue{Kind: IssueInvalidMediaType, Path: issuePath, Detail: "media_type must be lowercase, parameter-free, and at most 128 bytes"})
	} else if schemaFormat == "json-schema-2020-12" {
		switch entry.Role {
		case RoleSchema:
			if entry.MediaType != "application/schema+json" && entry.MediaType != "application/json" {
				issues = append(issues, Issue{Kind: IssueInvalidMediaType, Path: issuePath, Detail: "JSON Schema entries require application/schema+json or application/json"})
			}
		case RoleValidFixture, RoleInvalidFixture:
			if entry.MediaType != "application/json" {
				issues = append(issues, Issue{Kind: IssueInvalidMediaType, Path: issuePath, Detail: "JSON fixtures require application/json"})
			}
		}
	}
	if fixtureRole(entry.Role) {
		if entry.ConformsTo == "" {
			issues = append(issues, Issue{Kind: IssueConformsRequired, Path: issuePath, Detail: "fixture role requires conforms_to"})
		} else if !cleanPortablePath(entry.ConformsTo) {
			issues = append(issues, Issue{Kind: IssueInvalidPath, Path: issuePath, Detail: "conforms_to must be a clean portable relative path"})
		}
	} else if entry.ConformsTo != "" {
		issues = append(issues, Issue{Kind: IssueConformsForbidden, Path: issuePath, Detail: "conforms_to is allowed only for fixture roles"})
	}
	return issues
}

func validateCandidate(candidate CandidateFile, issues *[]Issue) {
	if candidate.Kind != CandidateRegular {
		*issues = append(*issues, Issue{Kind: IssueUnsafeMode, Path: candidate.Path, Detail: fmt.Sprintf("candidate kind %q is not a regular file", candidate.Kind)})
	}
	validateText(candidate.Path, candidate.Raw, issues)
}

func validateText(path string, raw []byte, issues *[]Issue) {
	if len(raw) > MaxFileBytes {
		*issues = append(*issues, Issue{
			Kind:   IssueFileTooLarge,
			Path:   path,
			Detail: fmt.Sprintf("file contains %d bytes; maximum is %d", len(raw), MaxFileBytes),
		})
	}
	if !utf8.Valid(raw) {
		*issues = append(*issues, Issue{Kind: IssueInvalidText, Path: path, Detail: "file is not valid UTF-8"})
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		*issues = append(*issues, Issue{Kind: IssueInvalidText, Path: path, Detail: "file contains a NUL byte"})
	}
}

func cleanPortablePath(value string) bool {
	if value == "" || value == "." || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	if len(value) >= 2 && isASCIIAlpha(value[0]) && value[1] == ':' {
		return false
	}
	if pathpkg.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("._/-", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func knownRole(role Role) bool {
	_, ok := rootForRole(role)
	return ok
}

func rootForRole(role Role) (string, bool) {
	switch role {
	case RoleSchema:
		return "schema/", true
	case RoleValidFixture:
		return "fixtures/valid/", true
	case RoleInvalidFixture:
		return "fixtures/invalid/", true
	case RoleErrors, RoleVocabulary, RoleLimits, RoleChangelog, RoleExample, RoleOther:
		return "artifacts/", true
	default:
		return "", false
	}
}

func fixtureRole(role Role) bool {
	return role == RoleValidFixture || role == RoleInvalidFixture
}

func insideCarriedRoot(path string) bool {
	return strings.HasPrefix(path, "schema/") || strings.HasPrefix(path, "fixtures/") || strings.HasPrefix(path, "artifacts/")
}

func insideLegacyRoot(path string) bool {
	return strings.HasPrefix(path, "schema/") || strings.HasPrefix(path, "fixtures/")
}

func validMediaType(value string) bool {
	return value != "" && len(value) <= 128 && value == strings.ToLower(value) && mediaTypePattern.MatchString(value)
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func canonicalLowerPaths(paths []string) map[string]string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	canonical := make(map[string]string, len(sorted))
	for _, path := range sorted {
		lower := strings.ToLower(path)
		if _, exists := canonical[lower]; !exists {
			canonical[lower] = path
		}
	}
	return canonical
}

func caseCollisionIssues(paths []string, label string) []Issue {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	canonical := make(map[string]string, len(sorted))
	issues := make([]Issue, 0)
	for _, path := range sorted {
		lower := strings.ToLower(path)
		prior, exists := canonical[lower]
		if !exists {
			canonical[lower] = path
			continue
		}
		if prior == path {
			continue
		}
		issues = append(issues, Issue{
			Kind:   IssueCaseCollision,
			Path:   path,
			Detail: fmt.Sprintf("ASCII-lowercase %s collides with %q", label, prior),
		})
	}
	return issues
}

func safeRejectedPath(value string) string {
	return "invalid-path:" + strings.TrimPrefix(artifact.Digest([]byte(value)), "sha256:")[:16]
}

func digestBytes(bytesByPath map[string][]byte) map[string]string {
	perFile := make(map[string]string, len(bytesByPath))
	for path, raw := range bytesByPath {
		perFile[path] = artifact.Digest(raw)
	}
	return perFile
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	clone := descriptor
	clone.Artifacts = append([]Entry(nil), descriptor.Artifacts...)
	if descriptor.GeneratedFrom != nil {
		generated := *descriptor.GeneratedFrom
		clone.GeneratedFrom = &generated
	}
	return clone
}
