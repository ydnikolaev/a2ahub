package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// landCheckDetailLimit bounds LandVerdict.Detail and the stderr quoted in a
// "could not judge" error, so a write that trips hundreds of findings cannot
// make the host's refusal unreadable or unbounded.
const landCheckDetailLimit = 4096

// NewLandCheck builds the check the local host runs before it moves a base
// branch: the required check's own body, run over the candidate the host
// checked out. It calls runV3Check, the function `a2a validate --ci` and the
// CI workflow run, in "v3-pr" mode against the tip the write lands on
// (BaseSHA) and the write's author, so a local land refuses exactly what the
// required check would refuse. It is not a copy of that logic. hostReviews is
// false: a local host does not review writes, so `gates: autonomous` is legal
// here and POL-028 does not apply.
//
// The verdict follows the exit code. Exit 0 with a decodable report is a pass.
// Exit 1 with a decodable report that carries at least one violation is a
// refusal listing those codes. Every other outcome is an error, never a
// verdict: runV3Check also exits 1, with nothing on stdout, for a missing or
// unparseable space.yaml, a git failure and a manifest-policy failure, and
// calling any of those "the write is invalid" would blame the write for a
// problem in the checkout.
//
// When the write is otherwise valid, the approval rule runs last (spec 04
// section T1.3, landGateApproval): a write that needs a human gate in a
// `confirm` space and carries no matching A2A-Approved-Gates trailer is refused.
// A refusal that carries only the approval code still counts as a refusal and
// not as an error: it is the write's fault (it lacks an approval), not the
// checkout's, so it takes decodeLandCheckRun's violation path like any other.
func NewLandCheck(engine *validate.Engine) host.LandCheckFunc {
	return func(ctx context.Context, candidate host.LandCandidate) (host.LandVerdict, error) {
		var stdout, stderr bytes.Buffer
		exit := runV3Check(ctx, engine, candidate.CheckoutDir, gitDiffNameOnly, "v3-pr", candidate.BaseSHA, candidate.Author, false,
			IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
		verdict, err := decodeLandCheckRun(exit, stdout.Bytes(), stderr.String())
		if err != nil || !verdict.OK {
			return verdict, err
		}
		return landGateApproval(ctx, candidate)
	}
}

// landGateApproval is the approval half of the land check. It recomputes which
// human gates the write sits behind, against the TIP the write lands on, reads
// the EFFECTIVE mode from the tip's manifest (never the candidate's own, so a
// write that flips `confirm` to `autonomous` is judged under `confirm`), and
// hands both to validate.CheckGateApproval together with the gates the head
// commit's trailer approved. Any fact it cannot establish is a "could not
// judge" error, never a pass.
func landGateApproval(ctx context.Context, candidate host.LandCandidate) (host.LandVerdict, error) {
	required, err := landRequiredGates(ctx, candidate)
	if err != nil {
		return host.LandVerdict{}, fmt.Errorf("land check could not judge the write's approval gates: %w", err)
	}
	if len(required) == 0 {
		return host.LandVerdict{OK: true}, nil
	}
	mode, err := landTipGatesMode(ctx, candidate)
	if err != nil {
		return host.LandVerdict{}, fmt.Errorf("land check could not judge the write's approval gates: %w", err)
	}
	violations := validate.CheckGateApproval(mode, required, landApprovedGates(candidate.HeadMessage))
	if len(violations) == 0 {
		return host.LandVerdict{OK: true}, nil
	}
	codes, detail := summarizeCIReport(ciReport{
		Mode:  "v3-pr",
		Valid: false,
		Artifacts: []validateReport{{
			Path:   "<gate-approval>",
			Result: &validate.Result{Valid: false, InvocationPoint: validate.V3, Violations: violations},
		}},
	})
	return host.LandVerdict{Codes: codes, Detail: detail}, nil
}

// landRequiredGates is the set of human gates (G1-G4) the write sits behind,
// recomputed from the checkout the host built (the merge commit) against the
// tip. G1 and G2 are contract.PublicationGateFor's, G3 is fold.HumanGate's, G4 is
// "the write changes space.yaml". The gate rule itself lives in those two
// functions; this only gathers their inputs.
func landRequiredGates(ctx context.Context, candidate host.LandCandidate) ([]string, error) {
	changed, err := gitDiffNameOnly(ctx, candidate.CheckoutDir, candidate.BaseSHA)
	if err != nil {
		return nil, err
	}
	var required []string
	seenContracts := map[string]bool{}
	for _, p := range changed {
		if id, _, ok := space.ContractForPath(p); ok && !seenContracts[id] {
			seenContracts[id] = true
			gate, gerr := landContractGate(ctx, candidate, id)
			if gerr != nil {
				return nil, gerr
			}
			if gate != contract.GateNone {
				required = append(required, string(gate))
			}
		}
		if isEventDocument(p) {
			raw, rerr := readBoundedFile(filepath.Join(candidate.CheckoutDir, filepath.FromSlash(p)), maxMirrorEventBytes)
			if rerr != nil {
				return nil, rerr
			}
			var event lifecycleEventDoc
			if uerr := yaml.Unmarshal(raw, &event); uerr != nil {
				return nil, fmt.Errorf("event %s is not readable: %w", p, uerr)
			}
			if gate := fold.HumanGate(event.Transition); gate != "" {
				required = append(required, gate)
			}
		}
	}
	if manifestChanged(changed) {
		required = append(required, "G4")
	}
	return required, nil
}

// contractUnpublishedVersion is the version a submitted, never-published
// descriptor carries: the planner bumps from it when nothing is published
// (internal/contract/publication_plan.go).
const contractUnpublishedVersion = "0.0.0"

// landContractGate is the G1/G2 gate of one touched contract: the descriptor at
// the tip is the baseline, the descriptor in the checkout is the target, and
// contract.PublicationGateFor decides. G1 is the first PUBLISH of a contract
// (docs/the-plan/plan/03-domain.md §3.7), so a target at
// contractUnpublishedVersion is a submitted candidate that needs no gate, and a
// tip at it is no published baseline. breaking is false on purpose: this runs
// only after the V3 check was clean, and a same-major bump over breaking
// content is already a violation of that check, while a major bump is G2 on the
// version alone, so no breaking verdict can change the answer here.
func landContractGate(ctx context.Context, candidate host.LandCandidate, id string) (contract.PublicationGate, error) {
	_, target, relPath, _, err := contractReadDescriptor(candidate.CheckoutDir, id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return contract.GateNone, nil // deleted by the write: nothing publishes
		}
		return contract.GateNone, err
	}
	if target.Version == contractUnpublishedVersion {
		return contract.GateNone, nil
	}
	listing, err := contractGitBounded(ctx, candidate.CheckoutDir, maxMirrorEventBytes, "ls-tree", "--name-only", candidate.BaseSHA, "--", relPath)
	if err != nil {
		return contract.GateNone, fmt.Errorf("contract %s: cannot look up the tip's descriptor: %w", id, err)
	}
	if len(bytes.TrimSpace(listing)) == 0 {
		return landPublicationGate(false, "", target.Version)
	}
	priorRaw, err := contractGitBounded(ctx, candidate.CheckoutDir, maxMirrorEventBytes, "show", candidate.BaseSHA+":"+relPath)
	if err != nil {
		return contract.GateNone, fmt.Errorf("contract %s: cannot read the tip's descriptor: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(priorRaw)
	var prior contractDescriptorProbe
	if err == nil {
		err = yaml.Unmarshal(fm.YAML, &prior)
	}
	if err != nil {
		return contract.GateNone, fmt.Errorf("contract %s: the tip's descriptor is not parseable: %w", id, err)
	}
	return landPublicationGate(prior.Version != contractUnpublishedVersion, prior.Version, target.Version)
}

// landPublicationGate validates both versions before it calls
// contract.PublicationGateFor, which assumes well-formed semantic versions and
// indexes past a short one. A malformed version is an error here (the write
// cannot be judged), never a panic and never "no gate".
func landPublicationGate(hasBaseline bool, baseline, target string) (contract.PublicationGate, error) {
	if _, err := contractParseSemver(target); err != nil {
		return contract.GateNone, fmt.Errorf("the contract's version %q cannot be judged: %w", target, err)
	}
	if hasBaseline {
		if _, err := contractParseSemver(baseline); err != nil {
			return contract.GateNone, fmt.Errorf("the tip's contract version %q cannot be judged: %w", baseline, err)
		}
	}
	return contract.PublicationGateFor(hasBaseline, baseline, target, false), nil
}

// landTipGatesMode reads the effective gates mode from the manifest at the tip.
func landTipGatesMode(ctx context.Context, candidate host.LandCandidate) (contract.GatesMode, error) {
	raw, err := contractGitBounded(ctx, candidate.CheckoutDir, maxMirrorEventBytes, "show", candidate.BaseSHA+":space.yaml")
	if err != nil {
		return "", fmt.Errorf("the tip's space.yaml is not readable: %w", err)
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		return "", fmt.Errorf("the tip's space.yaml is not parseable: %w", err)
	}
	return manifest.EffectiveGates(false), nil
}

// landApprovedGates is the union of the gates the head commit's approval
// trailer lines name, read through host.ApprovedGatesTrailers (the same parse
// rule the land copies the trailer by). A line naming anything that is not an
// approvable gate approves nothing: it fails closed.
func landApprovedGates(message string) []string {
	var approved []string
	for _, line := range host.ApprovedGatesTrailers(message) {
		// Exactly the funnel's spelling: one space after the colon, then bare ids
		// joined by commas. Padding inside the list is not an id, so it approves
		// nothing.
		list := strings.TrimPrefix(strings.TrimPrefix(line, host.TrailerApprovedGates+":"), " ")
		if canonical, ok := contract.CanonicalApprovedGates(strings.Split(list, ",")); ok {
			approved = append(approved, canonical...)
		}
	}
	return approved
}

// decodeLandCheckRun turns one runV3Check outcome into a verdict or a
// "could not judge" error. It is separate from NewLandCheck so every exit
// shape can be tested without staging the failure that produces it.
func decodeLandCheckRun(exit int, stdout []byte, stderr string) (host.LandVerdict, error) {
	var report ciReport
	decodeErr := json.Unmarshal(stdout, &report)

	switch {
	case exit == 0 && decodeErr == nil:
		return host.LandVerdict{OK: true}, nil
	case exit == 1 && decodeErr == nil:
		codes, detail := summarizeCIReport(report)
		if len(codes) > 0 {
			return host.LandVerdict{Codes: codes, Detail: detail}, nil
		}
		return host.LandVerdict{}, fmt.Errorf("land check could not judge the write: the required check exited 1 and its report carries no violation%s", quotedStderr(stderr))
	case decodeErr != nil:
		return host.LandVerdict{}, fmt.Errorf("land check could not judge the write: the required check exited %d without a readable report%s", exit, quotedStderr(stderr))
	default:
		return host.LandVerdict{}, fmt.Errorf("land check could not judge the write: the required check exited %d%s", exit, quotedStderr(stderr))
	}
}

// summarizeCIReport lists the report's violation codes (first appearance
// order, once each) and a bounded human rendering of every finding.
func summarizeCIReport(report ciReport) ([]string, string) {
	var codes []string
	seen := map[string]bool{}
	var detail strings.Builder
	addCode := func(code string) {
		if code != "" && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	for _, artifact := range report.Artifacts {
		if artifact.Error != "" {
			fmt.Fprintf(&detail, "%s: %s\n", artifact.Path, artifact.Error)
		}
		if artifact.Result == nil {
			continue
		}
		for _, v := range artifact.Result.Violations {
			addCode(v.Code)
			fmt.Fprintf(&detail, "%s %s: %s\n", v.Code, artifact.Path, v.Message)
		}
	}
	for _, v := range report.DiffAuthz {
		addCode(v.Code)
		fmt.Fprintf(&detail, "%s %s: %s\n", v.Code, v.Path, v.Message)
	}
	return codes, truncateLandDetail(detail.String())
}

func truncateLandDetail(s string) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= landCheckDetailLimit {
		return s
	}
	return strings.ToValidUTF8(s[:landCheckDetailLimit], "") + "\n... (truncated)"
}

func quotedStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + truncateLandDetail(stderr)
}
