package html

import "github.com/ydnikolaev/a2ahub/internal/viewvocab"

// vocab.go used to be the closed data table plus its family/tone constants;
// it has moved to internal/viewvocab (spec space-notify-2026-08 P2) so a
// caller outside the presentation layer (a chat notifier, the CI plane) can
// read the same RU/EN words without importing internal/html and inverting
// the ADR-001 boundary this package's import set protects.
//
// Every identifier this file used to declare stays resolvable from package
// html, aliased to viewvocab's single source, because internal/html's own
// tests (vocab_test.go) and cmd/a2a/catalog.go read them white-box/by name
// and are outside this phase's footprint.

// VocabularyFamily constants enumerate the closed semantic families emitted by
// the dashboard vocabulary. See internal/viewvocab for the single source.
const (
	VocabularyFamilyFreshness           = viewvocab.VocabularyFamilyFreshness
	VocabularyFamilySourceFreshness     = viewvocab.VocabularyFamilySourceFreshness
	VocabularyFamilyOutcome             = viewvocab.VocabularyFamilyOutcome
	VocabularyFamilyLifecycleState      = viewvocab.VocabularyFamilyLifecycleState
	VocabularyFamilyTransition          = viewvocab.VocabularyFamilyTransition
	VocabularyFamilyReason              = viewvocab.VocabularyFamilyReason
	VocabularyFamilyGate                = viewvocab.VocabularyFamilyGate
	VocabularyFamilyWorkMode            = viewvocab.VocabularyFamilyWorkMode
	VocabularyFamilyDependencyDrift     = viewvocab.VocabularyFamilyDependencyDrift
	VocabularyFamilyConsistencySeverity = viewvocab.VocabularyFamilyConsistencySeverity
	VocabularyFamilyOperationalState    = viewvocab.VocabularyFamilyOperationalState
	VocabularyFamilyLiveTransport       = viewvocab.VocabularyFamilyLiveTransport
)

// VocabularyTone constants enumerate the closed presentation intents available
// to dashboard vocabulary entries. See internal/viewvocab for the single
// source.
const (
	VocabularyToneNeedsYou    = viewvocab.VocabularyToneNeedsYou
	VocabularyToneWaitingThem = viewvocab.VocabularyToneWaitingThem
	VocabularyToneProgressing = viewvocab.VocabularyToneProgressing
	VocabularyToneSettled     = viewvocab.VocabularyToneSettled
	VocabularyToneBroken      = viewvocab.VocabularyToneBroken
	VocabularyToneUnknown     = viewvocab.VocabularyToneUnknown
)

// toneCues is a package-local copy of viewvocab's tone→cue table, kept under
// its original unexported name because internal/html/vocab_test.go — outside
// this phase's footprint — dereferences it directly.
var toneCues = viewvocab.ToneCues()

// LiveTransport constants enumerate the client-local states of the live
// dashboard refresh protocol. See internal/viewvocab for the single source.
const (
	LiveTransportUpdated        = viewvocab.LiveTransportUpdated
	LiveTransportNewerAvailable = viewvocab.LiveTransportNewerAvailable
	LiveTransportRefreshing     = viewvocab.LiveTransportRefreshing
	LiveTransportStale          = viewvocab.LiveTransportStale
	LiveTransportUnavailable    = viewvocab.LiveTransportUnavailable
)

// VocabularyFamilies returns the closed dashboard family set in stable order.
func VocabularyFamilies() []VocabularyFamily {
	return viewvocab.VocabularyFamilies()
}

// LiveTransportStates returns every client-local transport state in stable
// order.
func LiveTransportStates() []string {
	return viewvocab.LiveTransportStates()
}

// DashboardVocabulary returns the complete bilingual dashboard dictionary.
func DashboardVocabulary() VocabularyTable {
	return viewvocab.DashboardVocabulary()
}
