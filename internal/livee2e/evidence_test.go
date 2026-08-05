package livee2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceManifestValidRelease(t *testing.T) {
	t.Parallel()
	manifest := validEvidenceManifest()
	if err := ValidateEvidenceManifest(manifest); err != nil {
		t.Fatalf("ValidateEvidenceManifest() error = %v", err)
	}
}

func TestEvidenceManifestRejectsReleaseEvidenceDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*EvidenceManifest)
		want   string
	}{
		{"reused checkout", func(m *EvidenceManifest) { m.Candidate.ExecutionCheckout.ID = m.Candidate.VerificationCheckout.ID }, "ids must differ"},
		{"missing clean attestation", func(m *EvidenceManifest) { m.Candidate.ExecutionCheckout.UntrackedClean = nil }, "untracked_clean attestation is required"},
		{"wrong built source", func(m *EvidenceManifest) { m.Candidate.BuiltSourceSHA = strings.Repeat("b", 40) }, "built_source_sha must equal"},
		{"modified binary", func(m *EvidenceManifest) { m.Candidate.Binary.BuildInfo.Modified = boolPointer(true) }, "modified must be false"},
		{"floor mismatch", func(m *EvidenceManifest) { m.Scenarios[0].LiveManifestFloor = "0.18.0" }, "live_manifest_floor"},
		{"missing mandatory row", func(m *EvidenceManifest) {
			// LE-OC-03/baseline (index 2) is live-required under D-7: it is
			// TierLogic but carries the "sse-revision-only" ProviderAssertions
			// carve-out, so its OWN row still needs a live pass in the bundle
			// (unlike LE-OC-01/baseline, index 0, which D-7 removed from this
			// requirement entirely — its proof is now the logic-tier marker,
			// not a bundle row).
			m.Scenarios = append(append([]ScenarioEvidence(nil), m.Scenarios[:2]...), m.Scenarios[3:]...)
		}, "mandatory PASS row LE-OC-03/baseline"},
		{"missing provider row", func(m *EvidenceManifest) {
			// LE-OC-04/provider-setting (index 3) is TierProvider outright.
			// Unlike a mandatory row it may read "unverified" — but it must
			// still be PRESENT; dropping it entirely is still a release
			// blocker.
			m.Scenarios = append(append([]ScenarioEvidence(nil), m.Scenarios[:3]...), m.Scenarios[4:]...)
		}, "scenarios must include LE-OC-04/provider-setting as pass or explicitly unverified"},
		{"missing carve-out assertion", func(m *EvidenceManifest) {
			// LE-OC-03/baseline's row is present and otherwise passing, but
			// its ONE provider-owned assertion ("sse-revision-only",
			// operational_catalogue.go) never proves anything — the exact
			// shape a shrunken live tier would produce if it quietly stopped
			// exercising the one fact only GitHub can decide.
			m.Scenarios[2].Assertions = passingAssertions([]string{"snapshot-revision-equal", "operational-rows-equal", "conditional-snapshot"})
		}, "assertions must include sse-revision-only"},
		{"mandatory unverified", func(m *EvidenceManifest) {
			m.Scenarios[1].Outcome = "unverified"
			m.Scenarios[1].Limitations = []string{"provider unavailable"}
		}, "unverified is allowed only"},
		{"optional unverified lacks limitation", func(m *EvidenceManifest) {
			m.Scenarios[3].Outcome = "unverified"
			m.Scenarios[3].Limitations = []string{}
		}, "limitations must explain"},
		{"passing cleanup incomplete", func(m *EvidenceManifest) { m.Scenarios[0].CleanupStatus = "incomplete" }, "cannot be incomplete"},
		{"missing v2 assertion", func(m *EvidenceManifest) {
			m.Scenarios[1].Assertions = passingAssertions([]string{"durable-checkpoints"})
		}, "announcement-v2"},
		{"duplicate scenario log", func(m *EvidenceManifest) { m.Scenarios[1].LogPath = m.Scenarios[0].LogPath }, "duplicates another scenario log path"},
		{"reused candidate check log", func(m *EvidenceManifest) { m.Scenarios[0].LogPath = m.CandidateCheck.LogPath }, "must not reuse candidate_check.log_path"},
		{"duplicate assertion", func(m *EvidenceManifest) {
			m.Scenarios[0].Assertions = append(m.Scenarios[0].Assertions, m.Scenarios[0].Assertions[0])
		}, "duplicate id receipt"},
		{"unproven pass", func(m *EvidenceManifest) { m.Scenarios[0].Assertions[0].Outcome = "unverified" }, "must pass to prove a passing scenario"},
		{"missing scenario digest", func(m *EvidenceManifest) { m.Scenarios[0].LogSHA256 = "" }, "log_sha256 must be"},
		{"scenario path traversal", func(m *EvidenceManifest) { m.Scenarios[0].LogPath = "../outside.log" }, "clean repository-relative path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifest := validEvidenceManifest()
			tc.mutate(&manifest)
			err := ValidateEvidenceManifest(manifest)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateEvidenceManifest() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestDecodeEvidenceManifestRejectsUnsafeOrOpenContent(t *testing.T) {
	t.Parallel()
	raw := validEvidenceJSON(t)
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{"secret", []byte(strings.Replace(string(raw), `"repository_id":"public/space"`, `"repository_id":"https://user:ghp_abcdefghijklmnopqrstuvwxyz@example.com/space"`, 1)), "secret-shaped"},
		{"private payload", []byte(strings.Replace(string(raw), `"manifest_version":`, `"private_payload":{"body":"not publishable"},"manifest_version":`, 1)), "private_payload is forbidden"},
		{"unknown field", []byte(strings.Replace(string(raw), `"manifest_version":`, `"surprise":true,"manifest_version":`, 1)), "unknown field"},
		{"trailing top-level value", append(append([]byte(nil), raw...), []byte(` {"second":true}`)...), "trailing JSON value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeEvidenceManifest(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeEvidenceManifest() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateEvidenceFileVerifiesCandidateCheckLog(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		writeLog    bool
		mutate      func([]byte) []byte
		staleDigest bool
		want        string
	}{
		{name: "valid", writeLog: true},
		{name: "missing", want: "candidate_check.log_path stat"},
		{name: "tampered digest", writeLog: true, mutate: func([]byte) []byte { return []byte("tampered\n") }, staleDigest: true, want: "log_sha256 does not match"},
		{name: "fabricated green text", writeLog: true, mutate: func([]byte) []byte { return []byte("candidate check green\n") }, want: "exactly one CHECKOUT_ROOT"},
		{name: "candidate mismatch", writeLog: true, mutate: func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("CANDIDATE_SHA="+strings.Repeat("a", 40)), []byte("CANDIDATE_SHA="+strings.Repeat("f", 40)), 1)
		}, want: "CANDIDATE_SHA"},
		{name: "dirty verification checkout", writeLog: true, mutate: func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("WORKTREE_CLEAN=true"), []byte("WORKTREE_CLEAN=false"), 1)
		}, want: "WORKTREE_CLEAN"},
		{name: "failed check", writeLog: true, mutate: func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("EXIT=0"), []byte("EXIT=1"), 1)
		}, want: "EXIT"},
		{name: "over bound", writeLog: true, mutate: func([]byte) []byte { return make([]byte, maxCandidateCheckLogBytes+1) }, want: "no larger than"},
		{name: "no logic tier marker", writeLog: true, mutate: func(raw []byte) []byte {
			return bytes.Replace(raw, []byte("LOGIC_TIER_ROWS_SHA256="+logicTierRowsDigest(logicTierRowKeys(Catalogue()))+"\n"), []byte(""), 1)
		}, want: "exactly one LOGIC_TIER_ROWS_SHA256 marker"},
		{name: "logic tier marker shrunk below the catalogue", writeLog: true, mutate: func(raw []byte) []byte {
			// The exact shrunken-lane defect this marker exists to catch: the
			// logic lane's own dispatch table silently drops one row the
			// catalogue still declares TierLogic (spec 46 postmortem).
			keys := logicTierRowKeys(Catalogue())
			shrunk := logicTierRowsDigest(keys[1:])
			return bytes.Replace(raw, []byte("LOGIC_TIER_ROWS_SHA256="+logicTierRowsDigest(keys)), []byte("LOGIC_TIER_ROWS_SHA256="+shrunk), 1)
		}, want: "does not match the catalogue's own logic-tier row set"},
		{name: "logic tier marker claims an undeclared row", writeLog: true, mutate: func(raw []byte) []byte {
			keys := logicTierRowKeys(Catalogue())
			widened := logicTierRowsDigest(append(append([]string(nil), keys...), "not-a-catalogue-row/baseline"))
			return bytes.Replace(raw, []byte("LOGIC_TIER_ROWS_SHA256="+logicTierRowsDigest(keys)), []byte("LOGIC_TIER_ROWS_SHA256="+widened), 1)
		}, want: "does not match the catalogue's own logic-tier row set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			manifest := validEvidenceManifest()
			verificationRoot := filepath.Join(dir, "verification-checkout")
			manifest.Candidate.VerificationCheckout.ID = checkoutEvidenceID(verificationRoot)
			validLog := candidateCheckLog(verificationRoot, manifest.Candidate.SHA, manifest.Candidate.Tree, manifest.Candidate.Version, "0")
			logContent := validLog
			if tc.mutate != nil {
				logContent = tc.mutate(validLog)
			}
			logPath := filepath.Join(dir, "logs", "make-check.log")
			if tc.writeLog {
				if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(logPath, logContent, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeScenarioLogs(t, dir, &manifest)
			manifest.CandidateCheck.LogPath = "logs/make-check.log"
			digestInput := logContent
			if tc.staleDigest {
				digestInput = validLog
			}
			expected := sha256.Sum256(digestInput)
			manifest.CandidateCheck.LogSHA256 = fmt.Sprintf("sha256:%x", expected)
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			evidencePath := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(evidencePath, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = ValidateEvidenceFile(evidencePath)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("ValidateEvidenceFile() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateEvidenceFile() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateEvidenceFileVerifiesCanonicalScenarioLogs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string, *EvidenceManifest)
		want   string
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, dir string, manifest *EvidenceManifest) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, filepath.FromSlash(manifest.Scenarios[0].LogPath))); err != nil {
					t.Fatal(err)
				}
			},
			want: "log_path stat",
		},
		{
			name: "tampered",
			mutate: func(t *testing.T, dir string, manifest *EvidenceManifest) {
				t.Helper()
				writeFile(t, filepath.Join(dir, filepath.FromSlash(manifest.Scenarios[0].LogPath)), []byte("tampered\n"))
			},
			want: "log_sha256 does not match",
		},
		{
			name: "over bound",
			mutate: func(t *testing.T, dir string, manifest *EvidenceManifest) {
				t.Helper()
				writeFile(t, filepath.Join(dir, filepath.FromSlash(manifest.Scenarios[0].LogPath)), make([]byte, maxScenarioLogBytes+1))
			},
			want: "no larger than",
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, dir string, manifest *EvidenceManifest) {
				t.Helper()
				path := filepath.Join(dir, filepath.FromSlash(manifest.Scenarios[0].LogPath))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(dir, "scenario-target.log")
				writeFile(t, target, canonicalScenarioLog(t, manifest.Candidate, manifest.Scenarios[0]))
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "must name a regular file",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			manifest := validEvidenceManifest()
			writeEvidenceBundle(t, dir, &manifest)
			tc.mutate(t, dir, &manifest)
			writeManifest(t, dir, manifest)
			_, err := ValidateEvidenceFile(filepath.Join(dir, "manifest.json"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateEvidenceFile() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateEvidenceFileBindsEveryScenarioLogProvenanceField(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		oldFragment string
		newFragment string
	}{
		{"candidate sha", `"candidate_sha":"` + strings.Repeat("a", 40) + `"`, `"candidate_sha":"` + strings.Repeat("f", 40) + `"`},
		{"candidate tree", `"candidate_tree":"` + strings.Repeat("b", 40) + `"`, `"candidate_tree":"` + strings.Repeat("f", 40) + `"`},
		{"binary digest", `"binary_sha256":"sha256:` + strings.Repeat("d", 64) + `"`, `"binary_sha256":"sha256:` + strings.Repeat("f", 64) + `"`},
		{"scenario id", `"scenario_id":"LE-OC-01"`, `"scenario_id":"LE-OC-02"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			manifest := validEvidenceManifest()
			writeEvidenceBundle(t, dir, &manifest)
			row := &manifest.Scenarios[0]
			path := filepath.Join(dir, filepath.FromSlash(row.LogPath))
			raw := canonicalScenarioLog(t, manifest.Candidate, *row)
			raw = bytesReplaceOnce(t, raw, []byte(tc.oldFragment), []byte(tc.newFragment))
			writeFile(t, path, raw)
			row.LogSHA256 = sha256Digest(raw)
			writeManifest(t, dir, manifest)

			_, err := ValidateEvidenceFile(filepath.Join(dir, "manifest.json"))
			if err == nil || !strings.Contains(err.Error(), "not the canonical candidate-bound scenario record") {
				t.Fatalf("ValidateEvidenceFile() error = %v, want provenance mismatch", err)
			}
		})
	}
}

// TestEvidenceFileGate is the narrow runtime bridge used by
// scripts/check_live_e2e_evidence.sh. With no explicit evidence path it is a
// normal green unit test; the release target always sets the path.
func TestEvidenceFileGate(t *testing.T) {
	t.Parallel()
	file := os.Getenv("A2A_LIVE_E2E_EVIDENCE_FILE")
	if file == "" {
		return
	}
	if _, err := ValidateEvidenceFile(file); err != nil {
		t.Fatalf("live-e2e-evidence: FAIL — %v", err)
	}
}

// TestLogicTierRowsDigestBridge is the narrow runtime bridge
// scripts/tests/check_live_e2e_evidence_test.sh uses to obtain the exact
// LOGIC_TIER_ROWS_SHA256 value a fixture transcript must carry to satisfy
// verifyCandidateCheckLog (evidence.go) — the SAME canonical digest that
// function independently recomputes from Catalogue() at validation time.
// Printing only when asked, matching TestEvidenceFileGate's own convention
// immediately above, keeps this a no-op during every ordinary
// `go test ./...` and `make check` run: the real marker is emitted exactly
// once, by the logic lane itself (TestLogicMatrix), never by this bridge.
func TestLogicTierRowsDigestBridge(t *testing.T) {
	t.Parallel()
	if os.Getenv("A2A_LIVE_E2E_PRINT_LOGIC_TIER_DIGEST") == "" {
		return
	}
	fmt.Println(logicTierRowsDigest(logicTierRowKeys(Catalogue())))
}

func validEvidenceJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(validEvidenceManifest())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validEvidenceManifest() EvidenceManifest {
	sha := strings.Repeat("a", 40)
	tree := strings.Repeat("b", 40)
	manifestCommit := strings.Repeat("c", 40)
	rows := []struct {
		id         string
		branch     string
		assertions []string
	}{
		{"LE-OC-01", "baseline", requiredAssertions["LE-OC-01/baseline"]},
		{"LE-OC-02", "baseline", requiredAssertions["LE-OC-02/baseline"]},
		{"LE-OC-03", "baseline", requiredAssertions["LE-OC-03/baseline"]},
		{"LE-OC-04", "provider-setting", []string{"same-pr-retry"}},
		{"LE-OC-05", "baseline", requiredAssertions["LE-OC-05/baseline"]},
		{"LE-OC-06", "baseline", requiredAssertions["LE-OC-06/baseline"]},
		{"LE-OC-07", "baseline", requiredAssertions["LE-OC-07/baseline"]},
		{"LE-OC-08", "baseline", requiredAssertions["LE-OC-08/baseline"]},
		{"LE-OC-08", "unsupported-provider", []string{"unsupported-reported"}},
	}
	scenarios := make([]ScenarioEvidence, 0, len(rows))
	for _, row := range rows {
		scenarios = append(scenarios, ScenarioEvidence{
			ScenarioID: row.id, Branch: row.branch,
			StartedAt: "2026-08-03T10:00:00Z", EndedAt: "2026-08-03T10:01:00Z",
			SurfaceIDs: []string{"cli", "github"}, RepositoryID: "public/space",
			CommandFamily: "a2a live", Outcome: "pass",
			ArtifactRefs: []string{}, PRRefs: []string{"https://github.com/public/space/pull/12"},
			CleanupStatus: "complete", LogPath: "evidence/logs/" + strings.ToLower(row.id) + "-" + row.branch + ".log",
			LogSHA256:   "sha256:" + strings.Repeat("e", 64),
			Limitations: []string{}, LiveManifestFloor: OperationalFloor,
			LiveManifestCommit: manifestCommit, Assertions: passingAssertions(row.assertions),
		})
	}
	return EvidenceManifest{
		ManifestVersion: EvidenceManifestVersion,
		Candidate: CandidateEvidence{
			SHA: sha, Tree: tree, Version: OperationalReleaseTag,
			VerificationCheckout:  CheckoutEvidence{ID: "verification-checkout", SHA: sha, Tree: tree, Detached: boolPointer(true), IndexClean: boolPointer(true), WorktreeClean: boolPointer(true), UntrackedClean: boolPointer(true)},
			ExecutionCheckout:     CheckoutEvidence{ID: "execution-checkout", SHA: sha, Tree: tree, Detached: boolPointer(true), IndexClean: boolPointer(true), WorktreeClean: boolPointer(true), UntrackedClean: boolPointer(true)},
			BuiltSourceSHA:        sha,
			Binary:                BinaryEvidence{SHA256: "sha256:" + strings.Repeat("d", 64), Version: OperationalFloor, BuildInfo: BinaryBuildInfo{Revision: sha, Modified: boolPointer(false)}},
			EmbeddedTemplateFloor: OperationalFloor,
		},
		CandidateCheck: CandidateCheck{LogPath: "evidence/logs/make-check.log", LogSHA256: "sha256:" + strings.Repeat("e", 64), Passed: boolPointer(true)},
		Scenarios:      scenarios,
	}
}

func boolPointer(value bool) *bool { return &value }

func passingAssertions(ids []string) []AssertionEvidence {
	assertions := make([]AssertionEvidence, 0, len(ids))
	for _, id := range ids {
		assertions = append(assertions, AssertionEvidence{ID: id, Outcome: "pass"})
	}
	return assertions
}

func writeEvidenceBundle(t *testing.T, dir string, manifest *EvidenceManifest) {
	t.Helper()
	verificationRoot := filepath.Join(dir, "verification-checkout")
	manifest.Candidate.VerificationCheckout.ID = checkoutEvidenceID(verificationRoot)
	checkLog := candidateCheckLog(verificationRoot, manifest.Candidate.SHA, manifest.Candidate.Tree, manifest.Candidate.Version, "0")
	writeFile(t, filepath.Join(dir, "logs", "make-check.log"), checkLog)
	manifest.CandidateCheck.LogPath = "logs/make-check.log"
	manifest.CandidateCheck.LogSHA256 = sha256Digest(checkLog)
	writeScenarioLogs(t, dir, manifest)
	writeManifest(t, dir, *manifest)
}

func writeScenarioLogs(t *testing.T, dir string, manifest *EvidenceManifest) {
	t.Helper()
	for index := range manifest.Scenarios {
		row := &manifest.Scenarios[index]
		raw := canonicalScenarioLog(t, manifest.Candidate, *row)
		row.LogSHA256 = sha256Digest(raw)
		writeFile(t, filepath.Join(dir, filepath.FromSlash(row.LogPath)), raw)
	}
}

func canonicalScenarioLog(t *testing.T, candidate CandidateEvidence, row ScenarioEvidence) []byte {
	t.Helper()
	raw, err := json.Marshal(scenarioLogEvidence{
		EvidenceVersion: ScenarioLogVersion,
		CandidateSHA:    candidate.SHA, CandidateTree: candidate.Tree, BinarySHA256: candidate.Binary.SHA256,
		ScenarioID: row.ScenarioID, Branch: row.Branch, StartedAt: row.StartedAt, EndedAt: row.EndedAt,
		Outcome: row.Outcome, Assertions: row.Assertions,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func writeManifest(t *testing.T, dir string, manifest EvidenceManifest) {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "manifest.json"), raw)
}

func writeFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func sha256Digest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest)
}

func bytesReplaceOnce(t *testing.T, raw, old, replacement []byte) []byte {
	t.Helper()
	if !bytes.Contains(raw, old) {
		t.Fatalf("canonical scenario log does not contain %q", old)
	}
	return bytes.Replace(raw, old, replacement, 1)
}
