package html

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// Deliberate projection omissions live together so the inventory gate can
// prove that every exception still names a real exported source field and has
// a reviewable reason. Same-name fields are never exempt merely because a
// mapper forgot their assignment.
var itemFieldsThisPackageDeliberatelyDrops = map[string]string{
	"LatestEventAt":  "renamed to MovedAt by toItem; it is the activity clock, distinct from CreatedAt",
	"LatestEventSeq": "renamed to ActivitySeq by toItem; it is MovedAt's committed-order tie-breaker",
	"LatestEventID":  "renamed to ActivityEventID by toItem; it is the current activity event identity",
}

var openItemFieldsThisPackageDeliberatelyDrops = map[string]string{}

var threadResultFieldsThisPackageDeliberatelyDrops = map[string]string{
	"ResolvedFrom": "DashboardSnapshot contains threads already grouped by their direct thread identity, never query-resolved aliases",
}

var viewModelMappingAllowlists = []struct {
	name    string
	source  reflect.Type
	mapped  []string
	reasons map[string]string
}{
	{name: "toItem/cache.Item", source: reflect.TypeOf(cache.Item{}), mapped: []string{
		"Space", "ID", "Type", "Title", "From", "To", "State", "Priority", "Blocking", "NeededBy", "Thread", "New", "Reasons",
		"Overdue", "ActivationOwed", "PendingMerge", "SyncStale", "CreatedAt", "CreatedSeq", "CreatedOrderKnown", "Description", "Outcome",
		"Terminal", "StateSince", "StateBy", "StateEvent", "YourMove", "WaitingOn", "ExpectedTransition", "Why", "HumanGate", "RuleIdentity", "OperationalItems",
	}, reasons: itemFieldsThisPackageDeliberatelyDrops},
	{name: "toThreadView/cache.ThreadResult", source: reflect.TypeOf(cache.ThreadResult{}), mapped: []string{
		"Thread", "Space", "Order", "Opener", "Participants", "SyncStale", "Artifacts", "Transcript", "OpenItems", "Flags", "Unresolved", "Deliveries",
	}, reasons: threadResultFieldsThisPackageDeliberatelyDrops},
	{name: "toThreadView/cache.ThreadOpener", source: reflect.TypeOf(cache.ThreadOpener{}), mapped: []string{"ID", "Title", "From"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.ThreadArtifact", source: reflect.TypeOf(cache.ThreadArtifact{}), mapped: []string{"ID", "Type", "From", "To", "Title", "State", "Parent", "Refs"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.ThreadRef", source: reflect.TypeOf(cache.ThreadRef{}), mapped: []string{"Ref"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.TranscriptEntry", source: reflect.TypeOf(cache.TranscriptEntry{}), mapped: []string{"Seq", "Kind", "At", "Artifact", "Event", "Derived"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.TranscriptArtifact", source: reflect.TypeOf(cache.TranscriptArtifact{}), mapped: []string{"ID", "Type", "From", "To", "Title", "Work"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.TranscriptWorkReport", source: reflect.TypeOf(cache.TranscriptWorkReport{}), mapped: []string{"ArtifactID", "WorkID", "SubjectRef", "Mode", "Summary", "Actor", "WaitingOn", "ReportedAt", "ValidUntil", "CommitSequence"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.TranscriptEventActor", source: reflect.TypeOf(cache.TranscriptEventActor{}), mapped: []string{"Kind", "Name", "System", "Model", "Session"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.TranscriptEvent", source: reflect.TypeOf(cache.TranscriptEvent{}), mapped: []string{"ULID", "Subject", "Transition", "ClaimedState", "Actor", "ProducedBy", "Consistency", "ResponseID", "Version", "Note", "ReasonCode", "TransitionFree", "Verdicts"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.TranscriptDerivedAdoption", source: reflect.TypeOf(cache.TranscriptDerivedAdoption{}), mapped: []string{"ContractID", "System", "Major", "Since"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.OpenItem", source: reflect.TypeOf(cache.OpenItem{}), mapped: []string{"ID", "Type", "State", "Blocking", "NeededBy", "NextActions", "WaitingOn", "YourMove", "ExpectedTransition", "Why", "HumanGate", "Outcome", "Terminal", "StateSince", "StateBy", "StateEvent", "OperationalItems"}, reasons: openItemFieldsThisPackageDeliberatelyDrops},
	{name: "toThreadView/cache.NextAction", source: reflect.TypeOf(cache.NextAction{}), mapped: []string{"Transition", "By"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.ThreadFlag", source: reflect.TypeOf(cache.ThreadFlag{}), mapped: []string{"Kind", "Subject", "EventULID", "Consistency"}, reasons: map[string]string{}},
	{name: "toThreadView/cache.UnresolvedFact", source: reflect.TypeOf(cache.UnresolvedFact{}), mapped: []string{"Kind", "ID"}, reasons: map[string]string{}},
	{name: "mapContractDependencyEdges/cache.ContractDependencyEdge", source: reflect.TypeOf(cache.ContractDependencyEdge{}), mapped: []string{
		"From", "To", "Space", "Contract", "PinnedMajor", "PinnedVersion", "PinnedState", "ProviderVersion", "State", "AvailableMajors", "Drift", "Sunset", "Successor", "Description",
	}, reasons: map[string]string{}},
	{name: "mapContractDependencyDegradations/cache.ContractDependencyDegradation", source: reflect.TypeOf(cache.ContractDependencyDegradation{}), mapped: []string{
		"Space", "System", "Path", "Code", "Reason",
	}, reasons: map[string]string{}},
}

func TestViewModelMappingAllowlist(t *testing.T) {
	t.Parallel()

	for _, inventory := range viewModelMappingAllowlists {
		t.Run(inventory.name, func(t *testing.T) {
			t.Parallel()
			accounted := make(map[string]bool, len(inventory.mapped)+len(inventory.reasons))
			for _, field := range inventory.mapped {
				if accounted[field] {
					t.Errorf("%s maps source field %s more than once", inventory.name, field)
				}
				accounted[field] = true
				if sourceField, ok := inventory.source.FieldByName(field); !ok || !sourceField.IsExported() {
					t.Errorf("%s mapped inventory field %s does not name a current exported source field", inventory.name, field)
				}
			}
			for field, reason := range inventory.reasons {
				sourceField, ok := inventory.source.FieldByName(field)
				if !ok || !sourceField.IsExported() {
					t.Errorf("%s allowlist entry %s does not name a current exported source field", inventory.name, field)
				}
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s allowlist entry %s has a blank reason", inventory.name, field)
				}
				if accounted[field] {
					t.Errorf("%s source field %s is both mapped and allowlisted", inventory.name, field)
				}
				accounted[field] = true
			}
			for _, field := range fieldNames(inventory.source) {
				if !accounted[field] {
					t.Errorf("%s dropped source field %s: carry it and add a sentinel expectation, or allowlist it with a reason", inventory.name, field)
				}
			}
		})
	}
}
