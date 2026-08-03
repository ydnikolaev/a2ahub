package contract

import (
	"fmt"
	"sort"
)

// IssueKind is stable contract-domain identity. internal/validate maps these
// values to registry-backed POL-009/POL-013/REF-014 violations.
type IssueKind string

const (
	IssueUnsupportedProfile IssueKind = "unsupported-profile"
	IssueInvalidPath        IssueKind = "invalid-path"
	IssueDescriptorAlias    IssueKind = "descriptor-alias"
	IssueDuplicatePath      IssueKind = "duplicate-path"
	IssueCaseCollision      IssueKind = "case-collision"
	IssueUnknownRole        IssueKind = "unknown-role"
	IssueWrongRoot          IssueKind = "wrong-root"
	IssueInvalidMediaType   IssueKind = "invalid-media-type"
	IssueConformsRequired   IssueKind = "conforms-to-required"
	IssueConformsForbidden  IssueKind = "conforms-to-forbidden"
	IssueConformsUnresolved IssueKind = "conforms-to-unresolved"
	IssueTooFewEntries      IssueKind = "too-few-entries"
	IssueTooManyEntries     IssueKind = "too-many-entries"
	IssueRequiredRoleAbsent IssueKind = "required-role-absent"
	IssueMissingFile        IssueKind = "missing-file"
	IssueUndeclaredFile     IssueKind = "undeclared-file"
	IssueUnsafeMode         IssueKind = "unsafe-mode"
	IssueInvalidText        IssueKind = "invalid-text"
	IssueFileTooLarge       IssueKind = "file-too-large"
	IssueAggregateTooLarge  IssueKind = "aggregate-too-large"
	IssueDigestMismatch     IssueKind = "digest-mismatch"
	IssueInvalidEventSchema IssueKind = "invalid-event-schema"
	IssueProfileRequired    IssueKind = "digest-profile-required"
	IssueProfileForbidden   IssueKind = "digest-profile-forbidden"
	IssueInvalidPublication IssueKind = "invalid-publication"
)

// Issue is safe to show without verbose mode: Path is either a validated
// contract-relative value or an opaque digest label for rejected syntax,
// never an absolute mirror/project path.
type Issue struct {
	Kind   IssueKind
	Path   string
	Detail string
}

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
