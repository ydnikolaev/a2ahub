package contract

// CandidateSnapshot is the pure collector-to-core boundary for one contract
// candidate. Descriptor carries the exact contract.md bytes and resolved leaf
// kind; Files carries every discovered entry below the carried roots. A
// filesystem or Git adapter must resolve paths and kinds without following a
// symlink before constructing this value.
//
// The core deliberately consumes this detached value rather than a reader or
// filesystem interface: validation and digest computation cannot perform I/O
// or observe different bytes during one evaluation.
type CandidateSnapshot struct {
	Descriptor CandidateFile
	Files      []CandidateFile
}

// ValidateCarriedSet is the shared snapshot validator for collectors and
// policy adapters. BuildCarriedSet remains the single owner of declared-set,
// role, media, conforms_to, exactness, text, bounds, and digest rules. This
// boundary additionally proves that the implicitly carried descriptor itself
// was resolved as the exact regular contract.md leaf rather than followed
// through a symlink.
//
// Legacy contract-tree-v1 excludes the descriptor by definition, so its
// descriptor metadata is ignored and only Files are passed to the immutable
// legacy profile builder.
func ValidateCarriedSet(profile DigestProfile, descriptor Descriptor, snapshot CandidateSnapshot) (CarriedSet, []Issue) {
	if profile != ProfileContractSetV2 {
		return BuildCarriedSet(profile, nil, descriptor, snapshot.Files)
	}

	metadataIssues := validateDescriptorCandidate(snapshot.Descriptor)
	set, issues := BuildCarriedSet(profile, snapshot.Descriptor.Raw, descriptor, snapshot.Files)
	if len(metadataIssues) == 0 {
		return set, issues
	}

	issues = append(issues, metadataIssues...)
	sortIssues(issues)
	return CarriedSet{}, issues
}

func validateDescriptorCandidate(candidate CandidateFile) []Issue {
	issues := make([]Issue, 0, 2)
	if !cleanPortablePath(candidate.Path) {
		issues = append(issues, Issue{
			Kind:   IssueInvalidPath,
			Path:   safeRejectedPath(candidate.Path),
			Detail: "descriptor path is not a clean portable relative path",
		})
	} else if candidate.Path != DescriptorPath {
		issues = append(issues, Issue{
			Kind:   IssueDescriptorAlias,
			Path:   candidate.Path,
			Detail: "descriptor snapshot must resolve the exact contract.md path",
		})
	}
	validateCandidateKind(candidate, &issues)
	return issues
}
