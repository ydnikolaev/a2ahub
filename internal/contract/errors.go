package contract

import (
	"fmt"
	"sort"
)

// IssueKind is stable contract-domain identity. internal/validate maps these
// values to registry-backed POL-009/POL-013/REF-014 violations.
// IssueKind is part of the public package API.
type IssueKind string

const (
	// IssueUnsupportedProfile is part of the public package API.
	IssueUnsupportedProfile IssueKind = "unsupported-profile"
	// IssueDescriptorMismatch is part of the public package API.
	IssueDescriptorMismatch IssueKind = "descriptor-mismatch"
	// IssueInvalidPath is part of the public package API.
	IssueInvalidPath IssueKind = "invalid-path"
	// IssueDescriptorAlias is part of the public package API.
	IssueDescriptorAlias IssueKind = "descriptor-alias"
	// IssueDuplicatePath is part of the public package API.
	IssueDuplicatePath IssueKind = "duplicate-path"
	// IssueCaseCollision is part of the public package API.
	IssueCaseCollision IssueKind = "case-collision"
	// IssueUnknownRole is part of the public package API.
	IssueUnknownRole IssueKind = "unknown-role"
	// IssueWrongRoot is part of the public package API.
	IssueWrongRoot IssueKind = "wrong-root"
	// IssueInvalidMediaType is part of the public package API.
	IssueInvalidMediaType IssueKind = "invalid-media-type"
	// IssueConformsRequired is part of the public package API.
	IssueConformsRequired IssueKind = "conforms-to-required"
	// IssueConformsForbidden is part of the public package API.
	IssueConformsForbidden IssueKind = "conforms-to-forbidden"
	// IssueConformsUnresolved is part of the public package API.
	IssueConformsUnresolved IssueKind = "conforms-to-unresolved"
	// IssueTooFewEntries is part of the public package API.
	IssueTooFewEntries IssueKind = "too-few-entries"
	// IssueTooManyEntries is part of the public package API.
	IssueTooManyEntries IssueKind = "too-many-entries"
	// IssueRequiredRoleAbsent is part of the public package API.
	IssueRequiredRoleAbsent IssueKind = "required-role-absent"
	// IssueMissingFile is part of the public package API.
	IssueMissingFile IssueKind = "missing-file"
	// IssueUndeclaredFile is part of the public package API.
	IssueUndeclaredFile IssueKind = "undeclared-file"
	// IssueUnsafeMode is part of the public package API.
	IssueUnsafeMode IssueKind = "unsafe-mode"
	// IssueInvalidText is part of the public package API.
	IssueInvalidText IssueKind = "invalid-text"
	// IssueFileTooLarge is part of the public package API.
	IssueFileTooLarge IssueKind = "file-too-large"
	// IssueAggregateTooLarge is part of the public package API.
	IssueAggregateTooLarge IssueKind = "aggregate-too-large"
	// IssueDigestMismatch is part of the public package API.
	IssueDigestMismatch IssueKind = "digest-mismatch"
	// IssueInvalidEventSchema is part of the public package API.
	IssueInvalidEventSchema IssueKind = "invalid-event-schema"
	// IssueProfileRequired is part of the public package API.
	IssueProfileRequired IssueKind = "digest-profile-required"
	// IssueProfileForbidden is part of the public package API.
	IssueProfileForbidden IssueKind = "digest-profile-forbidden"
	// IssueInvalidPublication is part of the public package API.
	IssueInvalidPublication IssueKind = "invalid-publication"
)

// Issue is safe to show without verbose mode: Path is either a validated
// contract-relative value or an opaque digest label for rejected syntax,
// never an absolute mirror/project path.
// Issue is part of the public package API.
type Issue struct {
	Kind   IssueKind
	Path   string
	Detail string
}

// Error is part of the public package API.
func (i Issue) Error() string {
	if i.Path == "" {
		return fmt.Sprintf("contract: %s: %s", i.Kind, i.Detail)
	}
	return fmt.Sprintf("contract: %s: %s: %s", i.Kind, i.Path, i.Detail)
}

func sortIssues(issues []Issue) {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		return issues[i].Detail < issues[j].Detail
	})
}
