package host

// Capabilities is what a host declares about itself. Every Host returns one
// from the REQUIRED Host.Capabilities method, so a caller never type-asserts a
// property and a miss cannot read as a resolved negative (ADR-019). Positive
// declarations only: no field is inferred from a missing method. Optional
// *methods* (AutoMerger, Forker, OpenPRLister, ...) stay optional interfaces,
// ADR-003's shape.
//
// There is deliberately no kind or label field: a caller that needs to print
// what a host is prints what it declares. A new consumer that needs a new
// concept adds a field here, with its GitHub value.
type Capabilities struct {
	// NeedsCredential: a write needs a credential (token).
	NeedsCredential bool
	// ReviewsWrites: a write merges only through the host's review
	// machinery: a pull request, the required V3 CI check and CODEOWNERS
	// review.
	ReviewsWrites bool
	// OwnerIdentitySelfAsserted: owner strings are not logins the host
	// authenticates.
	OwnerIdentitySelfAsserted bool
	// LandsSynchronously: OpenPR validates the write against main's tip and
	// returns it merged in the same call.
	LandsSynchronously bool
	// OnThisMachine: the repository is on this machine's disk; no network
	// party (Actions, a second operator) can reach it.
	OnThisMachine bool
}

// gitHubCapabilities is the GitHub column of the capability table. *GitHubHost
// returns it and *FakeHost defaults to it.
func gitHubCapabilities() Capabilities {
	return Capabilities{
		NeedsCredential:           true,
		ReviewsWrites:             true,
		OwnerIdentitySelfAsserted: false,
		LandsSynchronously:        false,
		OnThisMachine:             false,
	}
}
