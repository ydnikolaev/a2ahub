//go:build livee2e

package livee2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

type operationalConfidenceRun struct {
	h        *harness
	floor    string
	manifest string
	cleanup  operationalCleanup

	receiptID     string
	thread        string
	workA         operationalWork
	workB         operationalWork
	contractID    string
	contractStage string
	contractPlan  contract.PublicationPlan
}

type operationalWork struct {
	ID      string
	Session string
}

type operationalCleanupEntry struct {
	owner string
	fn    func(context.Context) error
}

type operationalCleanup struct{ entries []operationalCleanupEntry }

func (c *operationalCleanup) Register(owner string, fn func(context.Context) error) {
	if fn == nil {
		fn = func(context.Context) error { return nil }
	}
	c.entries = append(c.entries, operationalCleanupEntry{owner: owner, fn: fn})
}

func (c *operationalCleanup) Run() map[string]error {
	out := map[string]error{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for index := len(c.entries) - 1; index >= 0; index-- {
		entry := c.entries[index]
		if err := operationalRunCleanup(ctx, entry.fn); err != nil {
			out[entry.owner] = errors.Join(out[entry.owner], err)
		}
	}
	return out
}

func operationalRunCleanup(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("cleanup panic: %v", recovered)
		}
	}()
	return fn(ctx)
}

type operationalProof struct {
	assertions   map[string]string
	artifacts    []string
	prs          []string
	limitations  []string
	command      string
	passEvidence string
}

func newOperationalProof(command string) operationalProof {
	return operationalProof{assertions: map[string]string{}, artifacts: []string{}, prs: []string{}, limitations: []string{}, command: command}
}

func (p *operationalProof) Pass(ids ...string) {
	for _, id := range ids {
		p.assertions[id] = "pass"
	}
}

func (p *operationalProof) Unverified(ids ...string) {
	for _, id := range ids {
		p.assertions[id] = "unverified"
	}
}

// runOperationalConfidenceScenarios drives P7 §6.2 in the specified order.
// Every body invokes the candidate binary and/or the existing provider/two-
// clone harness. The optional branches are explicit rows, never aliases of a
// hermetic or older scenario.
func runOperationalConfidenceScenarios(ctx context.Context, h *harness) []Result {
	oc := &operationalConfidenceRun{h: h}
	floor, commit, err := operationalLiveManifest(ctx, h)
	if err != nil {
		return operationalSetupFailure(err)
	}
	oc.floor, oc.manifest = floor, commit

	definitions := OperationalConfidenceCatalogue()
	body := map[string]func(context.Context, *operationalConfidenceRun) (operationalProof, Verdict, error){
		"LE-OC-05/baseline":             operationalVisibility,
		"LE-OC-01/baseline":             operationalReceipt,
		"LE-OC-02/baseline":             operationalWorkCheckpoints,
		"LE-OC-03/baseline":             operationalStaticServer,
		"LE-OC-06/baseline":             operationalContractPublish,
		"LE-OC-07/baseline":             operationalHistoricalMaterialize,
		"LE-OC-08/baseline":             operationalConformance,
		"LE-OC-08/unsupported-provider": operationalUnsupportedProvider,
		"LE-OC-04/provider-setting":     operationalProviderRetry,
	}
	order := []string{
		"LE-OC-05/baseline", "LE-OC-01/baseline", "LE-OC-02/baseline", "LE-OC-03/baseline",
		"LE-OC-06/baseline", "LE-OC-07/baseline", "LE-OC-08/baseline",
		"LE-OC-08/unsupported-provider", "LE-OC-04/provider-setting",
	}
	byKey := make(map[string]Scenario, len(definitions))
	for _, definition := range definitions {
		byKey[definition.Name+"/"+definition.Branch] = definition
	}
	results := make([]Result, 0, len(order))
	for _, key := range order {
		definition := byKey[key]
		started := time.Now().UTC()
		proof, verdict, runErr := operationalRunBody(ctx, oc, body[key])
		ended := time.Now().UTC()
		verdict, runErr = operationalRequireVerdict(verdict, runErr)
		results = append(results, operationalResult(definition, oc, proof, verdict, runErr, started, ended))
	}

	cleanupErrors := oc.cleanup.Run()
	for index := range results {
		key := results[index].Scenario + "/" + results[index].Branch
		if cleanupErr := cleanupErrors[key]; cleanupErr != nil {
			results[index].Verdict = VerdictFail
			results[index].Observed = errors.Join(errors.New(results[index].Observed), fmt.Errorf("cleanup: %w", cleanupErr)).Error()
			results[index].OperationalEvidence.CleanupStatus = "incomplete"
		} else {
			results[index].OperationalEvidence.CleanupStatus = "complete"
		}
	}
	return results
}

func operationalRunBody(ctx context.Context, oc *operationalConfidenceRun, body func(context.Context, *operationalConfidenceRun) (operationalProof, Verdict, error)) (proof operationalProof, verdict Verdict, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			verdict = VerdictFail
			err = fmt.Errorf("scenario body panic: %v", recovered)
		}
	}()
	return body(ctx, oc)
}

func operationalSetupFailure(err error) []Result {
	results := make([]Result, 0, len(OperationalConfidenceCatalogue()))
	for _, definition := range OperationalConfidenceCatalogue() {
		results = append(results, Result{
			Scenario: definition.Name, Branch: definition.Branch, System: SystemA, Surface: SurfaceCLI,
			Verdict: VerdictFail, Expected: "authoritative live space manifest is floor-active before LE-OC-01",
			Observed: err.Error(),
		})
	}
	return results
}

func operationalResult(definition Scenario, oc *operationalConfidenceRun, proof operationalProof, verdict Verdict, runErr error, started, ended time.Time) Result {
	if proof.assertions == nil {
		proof.assertions = map[string]string{}
	}
	assertions := make([]AssertionEvidence, 0, len(definition.Assertions))
	missing := make([]string, 0)
	for _, id := range definition.Assertions {
		outcome := proof.assertions[id]
		if outcome == "" {
			outcome = "fail"
			missing = append(missing, id)
		}
		assertions = append(assertions, AssertionEvidence{ID: id, Outcome: outcome})
	}
	if len(missing) > 0 && verdict == VerdictPass {
		verdict = VerdictFail
		runErr = errors.Join(runErr, fmt.Errorf("missing required assertion(s): %s", strings.Join(missing, ", ")))
	}
	if verdict == VerdictUnverified && !definition.Optional {
		verdict = VerdictFail
		runErr = errors.Join(runErr, errors.New("mandatory scenario cannot be unverified"))
	}
	res := Result{
		Scenario: definition.Name, Branch: definition.Branch, System: SystemA, Surface: SurfaceCLI, Verdict: verdict,
		Expected:     "every named P7 assertion is observed through the candidate binary and live provider",
		Detail:       strings.Join(proof.limitations, "; "),
		PassEvidence: proof.passEvidence,
		OperationalEvidence: &OperationalEvidence{
			StartedAt: started, EndedAt: ended, Assertions: assertions,
			SurfaceIDs: []string{"cli", "github"}, CommandFamily: proof.command,
			ArtifactRefs: cloneRuntimeStrings(proof.artifacts), PRRefs: cloneRuntimeStrings(proof.prs),
			CleanupStatus: "incomplete", Limitations: cloneRuntimeStrings(proof.limitations),
			LiveManifestFloor: oc.floor, LiveManifestCommit: oc.manifest,
		},
	}
	if runErr != nil {
		res.Observed = runErr.Error()
	}
	return res
}

func operationalLiveManifest(ctx context.Context, h *harness) (string, string, error) {
	if _, stderr, err := h.A.Run(ctx, "sync"); err != nil {
		return "", "", fmt.Errorf("sync authoritative manifest: %w: %s", err, strings.TrimSpace(stderr))
	}
	raw, err := readBoundedRuntimeFile(filepath.Join(h.A.MirrorDir(), "space.yaml"), maxEvidenceBytes)
	if err != nil {
		return "", "", fmt.Errorf("read authoritative space.yaml: %w", err)
	}
	var manifest struct {
		MinBinaryVersion string `yaml:"min_binary_version"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return "", "", fmt.Errorf("decode authoritative space.yaml: %w", err)
	}
	if manifest.MinBinaryVersion != OperationalFloor {
		return "", "", fmt.Errorf("live min_binary_version=%q, want release floor %s", manifest.MinBinaryVersion, OperationalFloor)
	}
	commit, err := operationalGit(ctx, h.A.MirrorDir(), "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("resolve authoritative manifest commit: %w", err)
	}
	if !looksLikeGitObjectID(commit) {
		return "", "", fmt.Errorf("resolve authoritative manifest commit: invalid object ID %q", commit)
	}
	return manifest.MinBinaryVersion, commit, nil
}

func operationalVisibility(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-05/baseline"
	proof := newOperationalProof("a2a doctor")
	oc.cleanup.Register(owner, nil)
	var settings struct {
		Visibility string `json:"visibility"`
	}
	path := "/repos/" + oc.h.Org + "/" + oc.h.Repo
	if _, err := oc.h.Prov.do(ctx, http.MethodGet, path, nil, &settings); err != nil {
		return proof, VerdictFail, err
	}
	if settings.Visibility != "public" {
		return proof, VerdictFail, fmt.Errorf("repository visibility=%q, want public controlled space", settings.Visibility)
	}
	proof.Pass("visibility-public")

	id, _, err := oc.h.A.Draft(ctx, "announcement", "--field", "classification=restricted", "--field", "title=P7 controlled visibility fixture")
	if err != nil {
		return proof, VerdictFail, err
	}
	staged := filepath.Join(oc.h.A.Dir, ".a2a", "staging", id+".md")
	raw, err := readBoundedRuntimeFile(staged, maxEvidenceBytes)
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("read controlled visibility fixture: %w", err)
	}
	fixture := filepath.Join(oc.h.A.MirrorDir(), systemAlpha, "exchanges", id+".md")
	if err := os.WriteFile(fixture, raw, 0o644); err != nil {
		return proof, VerdictFail, err
	}
	oc.cleanup.Register(owner, func(context.Context) error {
		return errors.Join(ignoreNotExist(os.Remove(fixture)), ignoreNotExist(os.Remove(staged)))
	})
	proof.artifacts = append(proof.artifacts, id)
	stdout, stderr, runErr := oc.h.A.Run(ctx, "doctor")
	combined := stdout + "\n" + stderr
	if runErr != nil && !strings.Contains(combined, "repository visibility") {
		return proof, VerdictFail, fmt.Errorf("a2a doctor: %w: %s", runErr, strings.TrimSpace(combined))
	}
	if !strings.Contains(combined, "repository visibility ["+oc.h.SpaceSlug+"]: WARN: public repository carries higher-classified material") {
		return proof, VerdictFail, fmt.Errorf("doctor did not emit verified public/higher-classification WARN: %s", strings.TrimSpace(combined))
	}
	if err := errors.Join(ignoreNotExist(os.Remove(fixture)), ignoreNotExist(os.Remove(staged))); err != nil {
		return proof, VerdictFail, fmt.Errorf("remove controlled visibility fixture: %w", err)
	}
	proof.Pass("visibility-higher-classification")
	proof.passEvidence = "provider visibility=public; candidate doctor emitted the controlled higher-classification WARN from a removable mirror fixture"
	return proof, VerdictPass, nil
}

func operationalReceipt(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-01/baseline"
	proof := newOperationalProof("a2a submit")
	sub, err := oc.h.DraftAndSubmit(ctx, oc.h.A, "requirement", "--slug", liveRunSlug("oc-p7-receipt", oc.h.PRFloor))
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalRegisterBranch(oc, owner, sub.Branch)
	proof.artifacts = append(proof.artifacts, sub.ID)
	proof.prs = append(proof.prs, operationalPR(oc.h, sub.PRNumber))
	if err := happyLandAndSync(ctx, oc.h, oc.h.A, sub.PRNumber); err != nil {
		return proof, VerdictFail, err
	}
	stdout, stderr, err := oc.h.A.Run(ctx, "show", sub.ID, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("show receipt artifact: %w: %s", err, strings.TrimSpace(stderr))
	}
	var show struct {
		State  string   `json:"state"`
		Thread string   `json:"thread"`
		Flags  []string `json:"flags"`
		Events []struct {
			ClaimedState string `json:"claimed_state"`
			ActorKind    string `json:"actor_kind"`
			Actor        string `json:"actor"`
			ActorSystem  string `json:"actor_system"`
			ProducedBy   struct {
				Tool    string `json:"tool"`
				Version string `json:"version"`
			} `json:"produced_by"`
			Consistency any `json:"consistency"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &show); err != nil || len(show.Events) == 0 {
		return proof, VerdictFail, fmt.Errorf("decode show receipt evidence: %w", err)
	}
	event := show.Events[len(show.Events)-1]
	if event.ClaimedState == "" {
		return proof, VerdictFail, errors.New("committed applicable event omitted claimed-state receipt")
	}
	proof.Pass("receipt")
	if event.ActorKind == "" || event.Actor == "" || event.ActorSystem != systemAlpha || event.ProducedBy.Tool != "a2a" || event.ProducedBy.Version != OperationalFloor {
		return proof, VerdictFail, fmt.Errorf("event provenance incomplete: actor=%s/%s/%s producer=%s/%s", event.ActorKind, event.Actor, event.ActorSystem, event.ProducedBy.Tool, event.ProducedBy.Version)
	}
	proof.Pass("provenance")
	if show.State != event.ClaimedState {
		return proof, VerdictFail, fmt.Errorf("fresh fold state=%q, receipt=%q", show.State, event.ClaimedState)
	}
	proof.Pass("fresh-fold")
	for _, flag := range show.Flags {
		if strings.Contains(flag, "state-claim-mismatch") {
			return proof, VerdictFail, fmt.Errorf("fresh fold raised mismatch flag: %s", flag)
		}
	}
	if event.Consistency != nil {
		return proof, VerdictFail, errors.New("fresh fold attached receipt mismatch evidence")
	}
	proof.Pass("no-mismatch-flag")
	oc.receiptID, oc.thread = sub.ID, show.Thread
	proof.passEvidence = fmt.Sprintf("%s folded to %s from candidate-produced receipt", sub.ID, show.State)
	return proof, VerdictPass, nil
}

type operationalWorkOutput struct {
	WorkID           string `json:"work_id"`
	Session          string `json:"session"`
	SemanticSequence uint64 `json:"semantic_sequence"`
	Local            struct {
		State string `json:"state"`
	} `json:"local"`
	Shared struct {
		Attempted   bool              `json:"attempted"`
		WriteResult space.WriteResult `json:"write_result"`
	} `json:"shared"`
}

func operationalWorkCommand(ctx context.Context, oc *operationalConfidenceRun, c *checkout, args ...string) (operationalWorkOutput, error) {
	args = append(args, "--json")
	stdout, stderr, err := c.Run(ctx, args...)
	var output operationalWorkOutput
	if decodeErr := json.Unmarshal([]byte(stdout), &output); decodeErr != nil {
		return output, fmt.Errorf("%s: decode JSON: %w (stderr=%s)", strings.Join(args, " "), decodeErr, strings.TrimSpace(stderr))
	}
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr))
	}
	if output.Shared.Attempted && output.Shared.WriteResult.PRNumber > 0 {
		if err := happyLandAndSync(ctx, oc.h, c, output.Shared.WriteResult.PRNumber); err != nil {
			return output, err
		}
	}
	return output, nil
}

func operationalWorkCheckpoints(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-02/baseline"
	proof := newOperationalProof("a2a work")
	oc.cleanup.Register(owner, nil)
	if oc.thread == "" || oc.receiptID == "" {
		return proof, VerdictFail, errors.New("LE-OC-01 did not establish a durable thread")
	}
	startA, err := operationalWorkCommand(ctx, oc, oc.h.A, "work", "start", "--thread", oc.thread, "--subject-ref", oc.receiptID, "--mode", "implementing", "--summary", "alpha started P7 work", "--ttl", "30m", "--report-valid-for", "30m", "--to", systemBravo, "--classification", "internal", "--actor-kind", "agent", "--actor-name", "oc-alpha")
	if err != nil {
		return proof, VerdictFail, err
	}
	oc.workA = operationalWork{ID: startA.WorkID, Session: startA.Session}
	operationalCollectWorkRef(oc, owner, &proof, startA)
	if _, stderr, err := oc.h.B.Run(ctx, "sync"); err != nil {
		return proof, VerdictFail, fmt.Errorf("sync A's durable start into B: %w: %s", err, strings.TrimSpace(stderr))
	}
	startB, err := operationalWorkCommand(ctx, oc, oc.h.B, "work", "start", "--thread", oc.thread, "--subject-ref", oc.receiptID, "--mode", "testing", "--summary", "bravo joined P7 work", "--ttl", "30m", "--report-valid-for", "30m", "--to", systemAlpha, "--classification", "internal", "--actor-kind", "agent", "--actor-name", "oc-bravo")
	if err != nil {
		return proof, VerdictFail, err
	}
	oc.workB = operationalWork{ID: startB.WorkID, Session: startB.Session}
	operationalCollectWorkRef(oc, owner, &proof, startB)
	if _, err := operationalWorkCommand(ctx, oc, oc.h.A, "work", "heartbeat", "--work-id", oc.workA.ID, "--session", oc.workA.Session, "--ttl", "30m"); err != nil {
		return proof, VerdictFail, err
	}
	checkpointA, err := operationalWorkCommand(ctx, oc, oc.h.A, "work", "checkpoint", "--work-id", oc.workA.ID, "--session", oc.workA.Session, "--mode", "testing", "--summary", "alpha checkpoint committed", "--report-valid-for", "30m")
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, checkpointA)
	waitB, err := operationalWorkCommand(ctx, oc, oc.h.B, "work", "wait", "--work-id", oc.workB.ID, "--session", oc.workB.Session, "--summary", "bravo waiting for alpha", "--waiting-on", "system:"+systemAlpha, "--report-valid-for", "30m")
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, waitB)
	stopA, err := operationalWorkCommand(ctx, oc, oc.h.A, "work", "stop", "--work-id", oc.workA.ID, "--session", oc.workA.Session, "--result", "paused", "--summary", "alpha paused for server proof", "--report-valid-for", "30m")
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, stopA)
	stopB, err := operationalWorkCommand(ctx, oc, oc.h.B, "work", "stop", "--work-id", oc.workB.ID, "--session", oc.workB.Session, "--result", "paused", "--summary", "bravo paused for server proof", "--report-valid-for", "30m")
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, stopB)

	if _, stderr, err := oc.h.A.Run(ctx, "sync"); err != nil {
		return proof, VerdictFail, fmt.Errorf("sync A: %w: %s", err, stderr)
	}
	if _, stderr, err := oc.h.B.Run(ctx, "sync"); err != nil {
		return proof, VerdictFail, fmt.Errorf("sync B: %w: %s", err, stderr)
	}
	announcementCount, err := operationalWorkArtifacts(oc.h.A.MirrorDir(), oc.workA.ID, oc.workB.ID)
	if err != nil || announcementCount < 6 {
		return proof, VerdictFail, fmt.Errorf("durable work documents: count=%d: %w", announcementCount, err)
	}
	proof.Pass("announcement-v2")
	proof.Pass("durable-checkpoints")
	staticA, stderr, err := oc.h.A.Run(ctx, "dashboard", "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("alpha dashboard: %w: %s", err, strings.TrimSpace(stderr))
	}
	snapshotA, err := operationalDashboardJSONSnapshot([]byte(staticA))
	if err != nil || !operationalSnapshotHasWork(snapshotA, oc.workA.ID, systemAlpha) || !operationalSnapshotHasWork(snapshotA, oc.workB.ID, systemBravo) {
		return proof, VerdictFail, fmt.Errorf("alpha projection lacks both durable actors/checkpoints: %w", err)
	}
	staticB, stderr, err := oc.h.B.Run(ctx, "dashboard", "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("bravo dashboard: %w: %s", err, strings.TrimSpace(stderr))
	}
	snapshotB, err := operationalDashboardJSONSnapshot([]byte(staticB))
	if err != nil || !operationalSnapshotHasWork(snapshotB, oc.workA.ID, systemAlpha) || !operationalSnapshotHasWork(snapshotB, oc.workB.ID, systemBravo) {
		return proof, VerdictFail, fmt.Errorf("bravo projection lacks both durable actors/checkpoints: %w", err)
	}
	proof.Pass("cross-clone-projection")
	statusA, stderr, err := oc.h.A.Run(ctx, "work", "status", "--include-expired", "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("alpha work status: %w: %s", err, strings.TrimSpace(stderr))
	}
	statusB, stderr, err := oc.h.B.Run(ctx, "work", "status", "--include-expired", "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("bravo work status: %w: %s", err, strings.TrimSpace(stderr))
	}
	if err := operationalAssertLocalHeartbeat([]byte(statusA), []byte(statusB), oc.workA.ID); err != nil {
		return proof, VerdictFail, err
	}
	proof.Pass("local-heartbeat-local-only")
	proof.passEvidence = fmt.Sprintf("%d announcement/v2 checkpoints across two clones; A heartbeat stayed local", announcementCount)
	return proof, VerdictPass, nil
}

func operationalCollectWorkRef(oc *operationalConfidenceRun, owner string, proof *operationalProof, output operationalWorkOutput) {
	write := output.Shared.WriteResult
	for _, id := range write.ArtifactIDs {
		if !containsString(proof.artifacts, id) {
			proof.artifacts = append(proof.artifacts, id)
		}
	}
	if write.Branch != "" {
		operationalRegisterBranch(oc, owner, write.Branch)
	}
	if write.PRNumber > 0 {
		ref := operationalPR(oc.h, write.PRNumber)
		if !containsString(proof.prs, ref) {
			proof.prs = append(proof.prs, ref)
		}
	}
}

func operationalWorkArtifacts(root, workA, workB string) (int, error) {
	var count int
	sequences := map[string][]uint64{workA: {}, workB: {}}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		raw, readErr := readBoundedRuntimeFile(path, maxEvidenceBytes)
		if readErr != nil {
			return readErr
		}
		if !bytes.Contains(raw, []byte(workA)) && !bytes.Contains(raw, []byte(workB)) {
			return nil
		}
		var doc struct {
			Schema string `yaml:"schema"`
			Type   string `yaml:"type"`
			Work   struct {
				ID               string `yaml:"id"`
				SemanticSequence uint64 `yaml:"semantic_sequence"`
			} `yaml:"work"`
		}
		parts := bytes.SplitN(raw, []byte("---"), 3)
		if len(parts) != 3 {
			return fmt.Errorf("work artifact %s has no closed front matter", path)
		}
		front := bytes.TrimPrefix(parts[1], []byte("\n"))
		if yaml.Unmarshal(front, &doc) != nil || doc.Schema != "envelope/v2" || doc.Type != "announcement" || doc.Work.SemanticSequence == 0 {
			return fmt.Errorf("work artifact %s is not announcement/v2 with a positive semantic sequence", path)
		}
		if _, expected := sequences[doc.Work.ID]; !expected {
			return nil
		}
		count++
		sequences[doc.Work.ID] = append(sequences[doc.Work.ID], doc.Work.SemanticSequence)
		return nil
	})
	if err != nil {
		return count, err
	}
	for workID, observed := range sequences {
		sort.Slice(observed, func(i, j int) bool { return observed[i] < observed[j] })
		if len(observed) < 3 {
			return count, fmt.Errorf("work %s has only semantic sequences %v, want at least start/checkpoint/stop", workID, observed)
		}
		for index, sequence := range observed {
			if sequence != uint64(index+1) {
				return count, fmt.Errorf("work %s semantic sequences=%v, want durable contiguous order from 1", workID, observed)
			}
		}
	}
	return count, nil
}

func operationalStaticServer(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-03/baseline"
	proof := newOperationalProof("a2a serve")
	staticPath := filepath.Join(oc.h.B.Dir, "oc-p7-dashboard.html")
	if _, stderr, err := oc.h.B.Run(ctx, "dashboard", "--out", "oc-p7-dashboard.html", "--no-open"); err != nil {
		return proof, VerdictFail, fmt.Errorf("generate static dashboard: %w: %s", err, strings.TrimSpace(stderr))
	}
	staticHTML, err := readBoundedRuntimeFile(staticPath, 16<<20)
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("read static dashboard: %w", err)
	}
	static, err := operationalDashboardSnapshot(staticHTML)
	if err != nil {
		return proof, VerdictFail, err
	}
	oc.cleanup.Register(owner, func(context.Context) error { return os.Remove(staticPath) })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return proof, VerdictFail, err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	serverCtx, cancelServer := context.WithCancel(context.Background())
	cmd := exec.CommandContext(serverCtx, oc.h.B.Bin, "serve", "--listen", address, "--refresh", "250ms", "--sync-every", "0") //nolint:gosec // reason: binary is the attested candidate and argv contains only the loopback listener allocated above.
	cmd.Dir = oc.h.B.Dir
	cmd.Env = checkoutEnv(oc.h.B.Token, oc.h.B.SpaceSlug)
	var serverOutput strings.Builder
	cmd.Stdout, cmd.Stderr = &serverOutput, &serverOutput
	if err := cmd.Start(); err != nil {
		cancelServer()
		return proof, VerdictFail, err
	}
	oc.cleanup.Register(owner, func(context.Context) error {
		cancelServer()
		wait := make(chan error, 1)
		go func() { wait <- cmd.Wait() }()
		select {
		case err := <-wait:
			if err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "signal: killed") {
				return err
			}
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("serve process did not stop within 10s")
		}
	})
	baseURL := "http://" + address
	if err := operationalAwaitHTTP(ctx, baseURL+"/api/v1/snapshot"); err != nil {
		return proof, VerdictFail, fmt.Errorf("serve startup: %w: %s", err, serverOutput.String())
	}
	initialResponse, err := http.Get(baseURL + "/api/v1/snapshot")
	if err != nil {
		return proof, VerdictFail, err
	}
	initialRaw, readErr := operationalReadBounded(initialResponse.Body, 16<<20)
	closeErr := initialResponse.Body.Close()
	if readErr != nil || closeErr != nil {
		return proof, VerdictFail, fmt.Errorf("initial snapshot read/close: %w", errors.Join(readErr, closeErr))
	}
	if initialResponse.StatusCode != http.StatusOK {
		return proof, VerdictFail, fmt.Errorf("initial snapshot status=%d, want %d", initialResponse.StatusCode, http.StatusOK)
	}
	var initial operational.Snapshot
	if err := json.Unmarshal(initialRaw, &initial); err != nil {
		return proof, VerdictFail, fmt.Errorf("decode server operational snapshot: %w", err)
	}
	if static.Revision == "" || static.Revision != initial.Revision {
		return proof, VerdictFail, fmt.Errorf("static/server revisions differ: %q/%q", static.Revision, initial.Revision)
	}
	proof.Pass("snapshot-revision-equal")
	if !reflect.DeepEqual(static.Timeline, initial.Timeline) || static.TimelineWindow != initial.TimelineWindow {
		return proof, VerdictFail, errors.New("static/server operational rows differ")
	}
	proof.Pass("operational-rows-equal")

	sseCtx, cancelSSE := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelSSE()
	sseRequest, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, baseURL+"/api/v1/events", nil)
	sseRequest.Header.Set("Last-Event-ID", initial.Revision)
	sseResponse, err := http.DefaultClient.Do(sseRequest)
	if err != nil {
		return proof, VerdictFail, err
	}
	defer func() { _ = sseResponse.Body.Close() }()
	resume, err := operationalWorkCommand(ctx, oc, oc.h.B, "work", "resume", "--work-id", oc.workB.ID, "--session", oc.workB.Session)
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, resume)
	checkpoint, err := operationalWorkCommand(ctx, oc, oc.h.B, "work", "checkpoint", "--work-id", oc.workB.ID, "--session", oc.workB.Session, "--mode", "reviewing", "--summary", "server checkpoint committed", "--report-valid-for", "30m")
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, checkpoint)
	stop, err := operationalWorkCommand(ctx, oc, oc.h.B, "work", "stop", "--work-id", oc.workB.ID, "--session", oc.workB.Session, "--result", "paused", "--summary", "server proof complete", "--report-valid-for", "30m")
	if err != nil {
		return proof, VerdictFail, err
	}
	operationalCollectWorkRef(oc, owner, &proof, stop)
	if _, stderr, err := oc.h.B.Run(ctx, "sync"); err != nil {
		return proof, VerdictFail, fmt.Errorf("sync checkpoint for server: %w: %s", err, stderr)
	}
	sseBlock, err := operationalReadRevisionSSE(sseResponse.Body)
	if err != nil || !strings.Contains(sseBlock, "event: revision") || strings.Contains(sseBlock, "timeline") {
		return proof, VerdictFail, fmt.Errorf("SSE was not revision-only: %w: %q", err, sseBlock)
	}
	proof.Pass("sse-revision-only")
	if err := operationalAwaitConditionalSnapshot(ctx, baseURL+"/api/v1/snapshot", initial.Revision, "server checkpoint committed"); err != nil {
		return proof, VerdictFail, err
	}
	proof.Pass("conditional-snapshot")
	proof.passEvidence = "static/server canonical snapshot equal; SSE carried revision only; conditional GET returned later checkpoint"
	return proof, VerdictPass, nil
}

func operationalContractPublish(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-06/baseline"
	proof := newOperationalProof("a2a contract publish")
	stage := filepath.Join(oc.h.A.Dir, "oc-p7-contract")
	id := "XC-" + systemAlpha + "-" + liveRunSlug("oc-p7-contract", oc.h.PRFloor)
	if err := operationalWriteContractCandidate(stage, id, "0.0.0", false); err != nil {
		return proof, VerdictFail, err
	}
	oc.cleanup.Register(owner, func(context.Context) error { return os.RemoveAll(stage) })
	oc.contractID, oc.contractStage = id, "oc-p7-contract"
	beforePulls, err := oc.h.runPulls(ctx)
	if err != nil {
		return proof, VerdictFail, err
	}
	beforeBranches, err := oc.h.Prov.ListBranches(ctx, oc.h.Org, oc.h.Repo)
	if err != nil {
		return proof, VerdictFail, err
	}
	beforeCommit, err := operationalGit(ctx, oc.h.A.MirrorDir(), "rev-parse", "origin/main")
	if err != nil {
		return proof, VerdictFail, err
	}
	stdout, stderr, err := oc.h.A.Run(ctx, "contract", "preflight", id, "--version", "1.0.0", "--staging", oc.contractStage, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("preflight: %w: %s", err, stderr)
	}
	var preflight space.ContractPublicationResult
	if err := json.Unmarshal([]byte(stdout), &preflight); err != nil {
		return proof, VerdictFail, err
	}
	afterPulls, err := oc.h.runPulls(ctx)
	if err != nil {
		return proof, VerdictFail, err
	}
	afterBranches, err := oc.h.Prov.ListBranches(ctx, oc.h.Org, oc.h.Repo)
	if err != nil {
		return proof, VerdictFail, err
	}
	afterCommit, err := operationalGit(ctx, oc.h.A.MirrorDir(), "rev-parse", "origin/main")
	if err != nil {
		return proof, VerdictFail, err
	}
	sort.Strings(beforeBranches)
	sort.Strings(afterBranches)
	if len(beforePulls) != len(afterPulls) || !reflect.DeepEqual(beforeBranches, afterBranches) || beforeCommit != afterCommit || preflight.Status != space.ContractPublicationPlanned {
		return proof, VerdictFail, fmt.Errorf("preflight wrote provider state: pulls %d->%d branches %v->%v main %s->%s status=%s", len(beforePulls), len(afterPulls), beforeBranches, afterBranches, beforeCommit, afterCommit, preflight.Status)
	}
	proof.Pass("preflight-write-free")
	stdout, stderr, err = oc.h.A.Run(ctx, "contract", "publish", id, "--version", "1.0.0", "--staging", oc.contractStage, "--expect-plan", preflight.Plan.PlanDigest, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("publish: %w: %s", err, stderr)
	}
	var published space.ContractPublicationResult
	if err := json.Unmarshal([]byte(stdout), &published); err != nil {
		return proof, VerdictFail, err
	}
	if published.Write.Branch != "" {
		operationalRegisterBranch(oc, owner, published.Write.Branch)
	}
	if published.Write.PRNumber <= 0 {
		return proof, VerdictFail, fmt.Errorf("publish returned no PR: %+v", published.Write)
	}
	proof.prs = append(proof.prs, operationalPR(oc.h, published.Write.PRNumber))
	proof.artifacts = append(proof.artifacts, id)
	if err := happyLandAndSync(ctx, oc.h, oc.h.A, published.Write.PRNumber); err != nil {
		return proof, VerdictFail, err
	}
	if !reflect.DeepEqual(preflight.Plan, published.Plan) {
		return proof, VerdictFail, errors.New("preflight and publish plans differ")
	}
	proof.Pass("plan-publish-equal")
	root := filepath.Join(oc.h.A.MirrorDir(), systemAlpha, "provides", strings.TrimPrefix(id, "XC-"+systemAlpha+"-"))
	descriptor, err := readBoundedRuntimeFile(filepath.Join(root, "contract.md"), maxEvidenceBytes)
	var descriptorWire struct {
		Schema string `yaml:"schema"`
	}
	parts := bytes.SplitN(descriptor, []byte("---"), 3)
	if err != nil || len(parts) != 3 || yaml.Unmarshal(parts[1], &descriptorWire) != nil || descriptorWire.Schema != "envelope/v2" {
		return proof, VerdictFail, fmt.Errorf("committed descriptor is not contract/v2: %w", err)
	}
	proof.Pass("contract-v2")
	event, err := operationalFindPublishEvent(oc.h.A.MirrorDir(), id, "1.0.0")
	if err != nil || event.Schema != "event/v2" {
		return proof, VerdictFail, fmt.Errorf("committed publish event is not event/v2: %w", err)
	}
	proof.Pass("event-v2")
	if event.Publication.IntentKey == "" || event.Publication.CandidateIntentDigest == "" || event.Publication.VersionSelector != "explicit:1.0.0" || event.Publication.OperationKey == "" {
		return proof, VerdictFail, fmt.Errorf("publication intent is incomplete: %+v", event.Publication)
	}
	proof.Pass("publication-intent")
	if event.DigestProfile != string(contract.ProfileContractSetV2) || event.Digest != published.Plan.AggregateDigest {
		return proof, VerdictFail, fmt.Errorf("event profile/digest=%s/%s plan=%s", event.DigestProfile, event.Digest, published.Plan.AggregateDigest)
	}
	proof.Pass("contract-set-v2")
	oc.contractPlan = published.Plan
	proof.passEvidence = fmt.Sprintf("%s@1.0.0 plan %s published as contract/v2 + event/v2", id, published.Plan.PlanDigest)
	return proof, VerdictPass, nil
}

type operationalPublishEvent struct {
	Schema        string                     `yaml:"schema"`
	Subject       string                     `yaml:"subject"`
	Version       string                     `yaml:"version"`
	Transition    string                     `yaml:"transition"`
	Digest        string                     `yaml:"digest"`
	DigestProfile string                     `yaml:"digest_profile"`
	Publication   contract.PublicationIntent `yaml:"publication"`
}

func operationalFindPublishEvent(root, id, version string) (operationalPublishEvent, error) {
	var found operationalPublishEvent
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".yaml") || !strings.Contains(path, string(filepath.Separator)+"events"+string(filepath.Separator)) {
			return err
		}
		raw, readErr := readBoundedRuntimeFile(path, maxEvidenceBytes)
		if readErr != nil {
			return readErr
		}
		var event operationalPublishEvent
		if yaml.Unmarshal(raw, &event) == nil && event.Subject == id && event.Version == version && event.Transition == "publish" {
			if found.Subject != "" {
				return errors.New("multiple matching publish events")
			}
			found = event
		}
		return nil
	})
	if err != nil {
		return found, err
	}
	if found.Subject == "" {
		return found, errors.New("publish event not found")
	}
	return found, nil
}

func operationalHistoricalMaterialize(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-07/baseline"
	proof := newOperationalProof("a2a contract materialize")
	if oc.contractID == "" {
		return proof, VerdictFail, errors.New("LE-OC-06 did not publish the baseline contract")
	}
	if err := operationalWriteContractCandidate(filepath.Join(oc.h.A.Dir, oc.contractStage), oc.contractID, "0.0.0", true); err != nil {
		return proof, VerdictFail, err
	}
	preflightRaw, stderr, err := oc.h.A.Run(ctx, "contract", "preflight", oc.contractID, "--version", "2.0.0", "--staging", oc.contractStage, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("second preflight: %w: %s", err, stderr)
	}
	var preflight space.ContractPublicationResult
	if err := json.Unmarshal([]byte(preflightRaw), &preflight); err != nil {
		return proof, VerdictFail, err
	}
	publishRaw, stderr, err := oc.h.A.Run(ctx, "contract", "publish", oc.contractID, "--version", "2.0.0", "--staging", oc.contractStage, "--expect-plan", preflight.Plan.PlanDigest, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("second publish: %w: %s", err, stderr)
	}
	var published space.ContractPublicationResult
	if err := json.Unmarshal([]byte(publishRaw), &published); err != nil {
		return proof, VerdictFail, err
	}
	if published.Write.Branch != "" {
		operationalRegisterBranch(oc, owner, published.Write.Branch)
	}
	if published.Write.PRNumber <= 0 {
		return proof, VerdictFail, errors.New("second publish returned no PR")
	}
	proof.prs = append(proof.prs, operationalPR(oc.h, published.Write.PRNumber))
	if err := happyLandAndSync(ctx, oc.h, oc.h.A, published.Write.PRNumber); err != nil {
		return proof, VerdictFail, err
	}
	destination := "oc-p7-materialized"
	absDestination := filepath.Join(oc.h.A.Dir, destination)
	oc.cleanup.Register(owner, func(context.Context) error { return os.RemoveAll(absDestination) })
	materializedRaw, stderr, err := oc.h.A.Run(ctx, "contract", "materialize", oc.contractID+"@1.0.0", "--to", destination, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("materialize old version: %w: %s", err, stderr)
	}
	var materialized space.ContractMaterializeResult
	if err := json.Unmarshal([]byte(materializedRaw), &materialized); err != nil {
		return proof, VerdictFail, err
	}
	if materialized.Outcome != space.ContractMaterialized || materialized.SourceCommit == "" {
		return proof, VerdictFail, fmt.Errorf("first materialize=%+v", materialized)
	}
	if err := operationalCompareHistoricalBytes(ctx, oc.h.A.MirrorDir(), materialized.SourceCommit, oc.contractID, absDestination); err != nil {
		return proof, VerdictFail, err
	}
	proof.Pass("historical-bytes-equal")
	if materialized.AggregateDigest != oc.contractPlan.AggregateDigest || materialized.DigestProfile != contract.ProfileContractSetV2 {
		return proof, VerdictFail, fmt.Errorf("historical digest/profile=%s/%s baseline=%s", materialized.AggregateDigest, materialized.DigestProfile, oc.contractPlan.AggregateDigest)
	}
	proof.Pass("historical-digest-equal")
	rerunRaw, stderr, err := oc.h.A.Run(ctx, "contract", "materialize", oc.contractID+"@1.0.0", "--to", destination, "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("identical materialize rerun: %w: %s", err, stderr)
	}
	var rerun space.ContractMaterializeResult
	if json.Unmarshal([]byte(rerunRaw), &rerun) != nil || rerun.Outcome != space.ContractAlreadyPresent {
		return proof, VerdictFail, fmt.Errorf("idempotent rerun outcome=%s", rerun.Outcome)
	}
	proof.Pass("idempotent-rerun")
	divergent := filepath.Join(absDestination, "schema", "order.schema.json")
	if err := os.WriteFile(divergent, []byte(`{"diverged":true}`), 0o644); err != nil {
		return proof, VerdictFail, err
	}
	before, _ := os.ReadFile(divergent) //nolint:gosec // reason: divergent is beneath the disposable materialization root created by this scenario.
	_, stderr, err = oc.h.A.Run(ctx, "contract", "materialize", oc.contractID+"@1.0.0", "--to", destination, "--json")
	after, _ := os.ReadFile(divergent) //nolint:gosec // reason: divergent is beneath the disposable materialization root created by this scenario.
	if err == nil || !bytes.Equal(before, after) || !strings.Contains(stderr, space.ErrContractDestinationDiverged.Error()) {
		if err == nil {
			return proof, VerdictFail, fmt.Errorf("divergent destination was not refused without overwrite: stderr=%s", stderr)
		}
		return proof, VerdictFail, fmt.Errorf("divergent destination changed or returned the wrong refusal: %w: %s", err, stderr)
	}
	proof.Pass("divergent-refusal")
	proof.artifacts = append(proof.artifacts, oc.contractID)
	proof.passEvidence = fmt.Sprintf("%s@1.0.0 bytes/digest matched commit %s; rerun already-present; divergence refused", oc.contractID, materialized.SourceCommit)
	return proof, VerdictPass, nil
}

func operationalConformance(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-08/baseline"
	proof := newOperationalProof("a2a contract check")
	payloadDir := filepath.Join(oc.h.A.Dir, "oc-p7-payloads")
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return proof, VerdictFail, err
	}
	if err := os.WriteFile(filepath.Join(payloadDir, "valid.json"), []byte(`{"id":"ok"}`), 0o644); err != nil {
		return proof, VerdictFail, err
	}
	if err := os.WriteFile(filepath.Join(payloadDir, "invalid.json"), []byte(`{}`), 0o644); err != nil {
		return proof, VerdictFail, err
	}
	oc.cleanup.Register(owner, func(context.Context) error { return os.RemoveAll(payloadDir) })
	ref := oc.contractID + "@1.0.0"
	suiteRaw, stderr, err := oc.h.A.Run(ctx, "contract", "check", ref, "--suite", "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("self-suite: %w: %s", err, stderr)
	}
	var suite contract.ConformanceResult
	if json.Unmarshal([]byte(suiteRaw), &suite) != nil || !suite.Passed || suite.Outcome != contract.ConformanceSuiteConsistent {
		return proof, VerdictFail, fmt.Errorf("self-suite result=%+v", suite)
	}
	proof.Pass("self-suite")
	validPath := filepath.ToSlash(filepath.Join("oc-p7-payloads", "valid.json"))
	validRaw, stderr, err := oc.h.A.Run(ctx, "contract", "check", ref, "--payload", validPath, "--schema", "schema/order.schema.json", "--json")
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("known-valid: %w: %s", err, stderr)
	}
	var valid contract.ConformanceResult
	if json.Unmarshal([]byte(validRaw), &valid) != nil || !valid.Passed || valid.Outcome != contract.ConformanceConformant {
		return proof, VerdictFail, fmt.Errorf("known-valid result=%+v", valid)
	}
	proof.Pass("known-valid-pass")
	invalidPath := filepath.ToSlash(filepath.Join("oc-p7-payloads", "invalid.json"))
	first, firstErr := operationalInvalidConformance(ctx, oc.h.A, ref, invalidPath)
	second, secondErr := operationalInvalidConformance(ctx, oc.h.A, ref, invalidPath)
	if firstErr != nil || secondErr != nil {
		return proof, VerdictFail, fmt.Errorf("known-invalid checks failed: %w", errors.Join(firstErr, secondErr))
	}
	if first.Passed || first.Outcome != contract.ConformanceNonconformant || !reflect.DeepEqual(first.Results, second.Results) || len(first.Results) == 0 || len(first.Results[0].Violations) == 0 {
		return proof, VerdictFail, fmt.Errorf("known-invalid unstable/nonconformant mismatch: first=%+v second=%+v", first, second)
	}
	for _, violation := range first.Results[0].Violations {
		if violation.SchemaPointer == "" {
			return proof, VerdictFail, fmt.Errorf("known-invalid violation lacks stable schema location: %+v", violation)
		}
	}
	proof.Pass("known-invalid-stable-locations")
	proof.artifacts = append(proof.artifacts, oc.contractID)
	proof.passEvidence = fmt.Sprintf("%s suite+valid pass; invalid returned stable structured locations twice", ref)
	return proof, VerdictPass, nil
}

func operationalUnsupportedProvider(_ context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-08/unsupported-provider"
	proof := newOperationalProof("a2a contract check")
	oc.cleanup.Register(owner, nil)
	proof.Unverified("unsupported-reported")
	proof.limitations = append(proof.limitations, "No separately published exact unsupported-format contract exists in the disposable space; the JSON-Schema baseline is proven and no unpublished branch is represented as historical evidence.")
	return proof, VerdictUnverified, nil
}

func operationalProviderRetry(ctx context.Context, oc *operationalConfidenceRun) (operationalProof, Verdict, error) {
	const owner = "LE-OC-04/provider-setting"
	proof := newOperationalProof("a2a submit")
	var settings struct {
		AllowAutoMerge bool `json:"allow_auto_merge"`
	}
	path := "/repos/" + oc.h.Org + "/" + oc.h.Repo
	if _, err := oc.h.Prov.do(ctx, http.MethodGet, path, nil, &settings); err != nil {
		proof.Unverified("same-pr-retry")
		proof.limitations = append(proof.limitations, "The provider credential could not read the controlled repository's auto-merge setting: "+err.Error())
		oc.cleanup.Register(owner, nil)
		return proof, VerdictUnverified, err
	}
	if !settings.AllowAutoMerge {
		proof.Unverified("same-pr-retry")
		proof.limitations = append(proof.limitations, "The controlled repository already had auto-merge disabled, so no reversible enabled-to-disabled arm-failure branch was available.")
		oc.cleanup.Register(owner, nil)
		return proof, VerdictUnverified, nil
	}
	// Register restoration before the first settings mutation.
	oc.cleanup.Register(owner, func(cleanupCtx context.Context) error {
		return operationalSetAutoMerge(cleanupCtx, oc, settings.AllowAutoMerge)
	})
	if err := operationalSetAutoMerge(ctx, oc, false); err != nil {
		proof.Unverified("same-pr-retry")
		proof.limitations = append(proof.limitations, "The provider credential could not safely disable auto-merge for the controlled repository: "+err.Error())
		return proof, VerdictUnverified, err
	}
	id, _, err := oc.h.A.Draft(ctx, "announcement", "--field", "title=P7 same PR retry")
	if err != nil {
		return proof, VerdictFail, err
	}
	branch := space.BranchName(oc.h.A.System, "submit", id)
	operationalRegisterBranch(oc, owner, branch)
	_, stderr, firstErr := oc.h.A.Run(ctx, "submit", id)
	pr, lookupErr := oc.h.pullForBranch(ctx, branch)
	if lookupErr == nil {
		operationalRegisterPRSettlement(oc, owner, pr.Number)
	}
	if lookupErr != nil || pr.Merged || pr.State != "open" {
		proof.Unverified("same-pr-retry")
		proof.limitations = append(proof.limitations, fmt.Sprintf("Provider did not expose a safe unsettled post-create PR (submit error=%v, PR lookup=%v, state=%s, merged=%t); no failure was inferred.", firstErr, lookupErr, pr.State, pr.Merged))
		return proof, VerdictUnverified, nil
	}
	if err := operationalSetAutoMerge(ctx, oc, true); err != nil {
		return proof, VerdictFail, err
	}
	if _, retryStderr, err := oc.h.A.Run(ctx, "submit", id); err != nil {
		return proof, VerdictFail, fmt.Errorf("retry submit: %w: first=%s retry=%s", err, strings.TrimSpace(stderr), strings.TrimSpace(retryStderr))
	}
	retried, err := oc.h.pullForBranch(ctx, branch)
	if err != nil {
		return proof, VerdictFail, fmt.Errorf("resolve retry PR: %w", err)
	}
	if retried.Number != pr.Number {
		return proof, VerdictFail, fmt.Errorf("retry PR=%d, first PR=%d", retried.Number, pr.Number)
	}
	if err := happyLandAndSync(ctx, oc.h, oc.h.A, retried.Number); err != nil {
		return proof, VerdictFail, err
	}
	proof.Pass("same-pr-retry")
	proof.artifacts = append(proof.artifacts, id)
	proof.prs = append(proof.prs, operationalPR(oc.h, pr.Number))
	proof.passEvidence = fmt.Sprintf("post-create state %q (submit error=%v) preserved PR #%d; retry repaired that same PR", pr.State, firstErr, pr.Number)
	return proof, VerdictPass, nil
}

func operationalRegisterBranch(oc *operationalConfidenceRun, owner, branch string) {
	if branch == "" {
		return
	}
	oc.cleanup.Register(owner, func(ctx context.Context) error {
		return oc.h.Prov.DeleteRef(ctx, oc.h.Org, oc.h.Repo, "heads/"+branch)
	})
}

func operationalRegisterPRSettlement(oc *operationalConfidenceRun, owner string, number int) {
	if number <= 0 {
		return
	}
	oc.cleanup.Register(owner, func(ctx context.Context) error {
		pull, err := oc.h.Prov.Pull(ctx, oc.h.Org, oc.h.Repo, number)
		if err != nil {
			return err
		}
		if pull.Merged || pull.State == "closed" {
			return nil
		}
		return oc.h.Prov.SetPullState(ctx, oc.h.Org, oc.h.Repo, number, "closed")
	})
}

func operationalSetAutoMerge(ctx context.Context, oc *operationalConfidenceRun, enabled bool) error {
	if err := oc.h.Prov.PatchRepo(ctx, oc.h.Org, oc.h.Repo, map[string]any{"allow_auto_merge": enabled}); err != nil {
		return err
	}
	var settings struct {
		AllowAutoMerge bool `json:"allow_auto_merge"`
	}
	path := "/repos/" + oc.h.Org + "/" + oc.h.Repo
	if _, err := oc.h.Prov.do(ctx, http.MethodGet, path, nil, &settings); err != nil {
		return err
	}
	if settings.AllowAutoMerge != enabled {
		return fmt.Errorf("provider allow_auto_merge=%t after setting %t", settings.AllowAutoMerge, enabled)
	}
	return nil
}

func operationalPR(h *harness, number int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", h.Org, h.Repo, number)
}

func operationalWriteContractCandidate(root, id, version string, changed bool) error {
	schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["id"],"properties":{"id":{"type":"string"}},"additionalProperties":false}`
	valid := `{"id":"ok"}`
	if changed {
		schema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["id","revision"],"properties":{"id":{"type":"string"},"revision":{"type":"integer"}},"additionalProperties":false}`
		valid = `{"id":"ok","revision":2}`
	}
	descriptor := fmt.Sprintf(`---
schema: envelope/v2
id: %s
type: contract
title: P7 operational confidence contract
space: %s
from: %s
to: [%s]
actor: {kind: agent, name: oc-contract}
created: 2026-08-03T10:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: %s
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:%s-20260803-ocp7
artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/order.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
---
# P7 contract
`, id, harnessSpaceSlug, systemAlpha, systemBravo, version, systemAlpha)
	files := map[string][]byte{
		"contract.md":                 []byte(descriptor),
		"schema/order.schema.json":    []byte(schema),
		"fixtures/valid/order.json":   []byte(valid),
		"fixtures/invalid/order.json": []byte(`{}`),
	}
	for name, raw := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func operationalCompareHistoricalBytes(ctx context.Context, mirror, commit, id, destination string) error {
	slug := strings.TrimPrefix(id, "XC-"+systemAlpha+"-")
	root := filepath.ToSlash(filepath.Join(systemAlpha, "provides", slug))
	return filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(destination, path)
		if err != nil {
			return err
		}
		want, err := operationalGitBytes(ctx, mirror, "show", commit+":"+root+"/"+filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		got, err := readBoundedRuntimeFile(path, maxEvidenceBytes)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("historical materialized bytes differ at %s", rel)
		}
		return nil
	})
}

func operationalInvalidConformance(ctx context.Context, c *checkout, ref, payload string) (contract.ConformanceResult, error) {
	stdout, stderr, err := c.Run(ctx, "contract", "check", ref, "--payload", payload, "--schema", "schema/order.schema.json", "--json")
	var result contract.ConformanceResult
	if decodeErr := json.Unmarshal([]byte(stdout), &result); decodeErr != nil {
		return result, decodeErr
	}
	if err == nil {
		return result, errors.New("known-invalid payload exited zero")
	}
	if strings.TrimSpace(stderr) != "" {
		return result, fmt.Errorf("known-invalid emitted transport error: %s", strings.TrimSpace(stderr))
	}
	return result, nil
}

func operationalAwaitHTTP(ctx context.Context, url string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("loopback server did not become ready")
}

func operationalAwaitConditionalSnapshot(ctx context.Context, url, priorRevision, summary string) error {
	deadline := time.Now().Add(20 * time.Second)
	var lastStatus int
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		request.Header.Set("If-None-Match", `W/"`+priorRevision+`"`)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			lastStatus = response.StatusCode
			body, readErr := operationalReadBounded(response.Body, 16<<20)
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				return fmt.Errorf("conditional snapshot read/close: %w", errors.Join(readErr, closeErr))
			}
			if response.StatusCode == http.StatusOK && bytes.Contains(body, []byte(summary)) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("conditional snapshot did not expose committed checkpoint within 20s (last status %d)", lastStatus)
}

func operationalDashboardSnapshot(page []byte) (operational.Snapshot, error) {
	const prefix = "window.A2A_DEMO="
	start := bytes.Index(page, []byte(prefix))
	if start < 0 {
		return operational.Snapshot{}, errors.New("static dashboard has no DATA payload")
	}
	start += len(prefix)
	end := bytes.Index(page[start:], []byte(";window.A2A_DOCS="))
	if end < 0 {
		return operational.Snapshot{}, errors.New("static dashboard has no DATA terminator")
	}
	var payload struct {
		Operational operational.Snapshot `json:"operational"`
	}
	if err := json.Unmarshal(page[start:start+end], &payload); err != nil {
		return operational.Snapshot{}, fmt.Errorf("decode static dashboard DATA: %w", err)
	}
	return payload.Operational, nil
}

func operationalDashboardJSONSnapshot(raw []byte) (operational.Snapshot, error) {
	var payload struct {
		Operational operational.Snapshot `json:"operational"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return operational.Snapshot{}, err
	}
	return payload.Operational, nil
}

func operationalSnapshotHasWork(snapshot operational.Snapshot, workID, system string) bool {
	for _, row := range snapshot.Timeline {
		for _, work := range row.Work {
			if work.WorkID == workID && work.Actor.System == system && work.CommittedCheckpoint != nil {
				return true
			}
		}
	}
	return false
}

func operationalAssertLocalHeartbeat(originRaw, peerRaw []byte, workID string) error {
	type item struct {
		WorkID            string `json:"work_id"`
		HeartbeatSequence uint64 `json:"heartbeat_sequence"`
	}
	type status struct {
		Items []item `json:"items"`
	}
	var origin, peer status
	if err := json.Unmarshal(originRaw, &origin); err != nil {
		return fmt.Errorf("decode originating work status: %w", err)
	}
	if err := json.Unmarshal(peerRaw, &peer); err != nil {
		return fmt.Errorf("decode peer work status: %w", err)
	}
	for _, observed := range peer.Items {
		if observed.WorkID == workID {
			return errors.New("originating work lease leaked into peer local store")
		}
	}
	for _, observed := range origin.Items {
		if observed.WorkID == workID && observed.HeartbeatSequence >= 2 {
			return nil
		}
	}
	return fmt.Errorf("originating work %s has no incremented local heartbeat", workID)
}

func operationalReadBounded(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response exceeds %d-byte limit", limit)
	}
	return raw, nil
}

func operationalReadSSE(reader io.Reader) (string, error) {
	lines := bufio.NewReader(io.LimitReader(reader, 64<<10))
	var block strings.Builder
	for {
		line, err := lines.ReadString('\n')
		block.WriteString(line)
		if line == "\n" {
			return block.String(), nil
		}
		if err != nil {
			return block.String(), err
		}
	}
}

func operationalReadRevisionSSE(reader io.Reader) (string, error) {
	var observed strings.Builder
	for {
		block, err := operationalReadSSE(reader)
		observed.WriteString(block)
		if strings.Contains(observed.String(), "timeline") {
			return observed.String(), errors.New("SSE exposed snapshot rows")
		}
		if strings.Contains(block, "event: revision") {
			return observed.String(), err
		}
		if err != nil {
			return observed.String(), err
		}
	}
}

func operationalGit(ctx context.Context, dir string, args ...string) (string, error) {
	raw, err := operationalGitBytes(ctx, dir, args...)
	return strings.TrimSpace(string(raw)), err
}

func operationalGitBytes(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // reason: executable is fixed and argv is assembled only from closed harness operations.
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return raw, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func ignoreNotExist(err error) error {
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
