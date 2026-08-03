package operation

// ContractPublishIntent derives the retry identity for one semantic contract
// candidate before floor, baseline, and target-version selection. Callers are
// responsible for supplying the contract package's canonical selector and
// candidate-intent digest; this function owns only the exact domain-separated
// op-v1 tuple derivation.
func ContractPublishIntent(system, contractID, selector, candidateIntentDigest string) string {
	encoder := newEncoder("contract-publish-intent")
	encoder.add(system)
	encoder.add(contractID)
	encoder.add(selector)
	encoder.add(candidateIntentDigest)
	return encoder.key()
}

// ContractPublish derives the retry identity for one resolved contract target.
// Candidate bytes, selector, baseline, floor, actor, and clock are deliberately
// absent: any attempt to publish the same contract version in one repository
// must collide and prove that its immutable plan/tree is identical.
func ContractPublish(system, contractID, targetVersion string) string {
	encoder := newEncoder("contract-publish")
	encoder.add(system)
	encoder.add(contractID)
	encoder.add(targetVersion)
	return encoder.key()
}
