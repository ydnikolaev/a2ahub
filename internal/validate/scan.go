package validate

// ScanSecrets is a thin exported wrapper over scanForSecrets (policy.go)
// so internal/feedback can scan a feedback report's raw bytes for a
// forbidden secret/credential pattern WITHOUT running the full V2
// envelope engine (I1, spec 25 §11 A3). Callers outside this package
// re-map each hit into their own domain's Violation shape rather than
// reusing this one verbatim.
func ScanSecrets(raw []byte) []Violation {
	return scanForSecrets(raw)
}
