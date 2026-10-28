package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// landCheckDetailLimit bounds LandVerdict.Detail and the stderr quoted in a
// "could not judge" error, so a write that trips hundreds of findings cannot
// make the host's refusal unreadable or unbounded.
const landCheckDetailLimit = 4096

// NewLandCheck builds the check the local host runs before it moves a base
// branch: the required check's own body, run over the candidate the host
// checked out. It calls runValidateCI, the function `a2a validate --ci` and the
// CI workflow run, in "v3-pr" mode against the tip the write lands on
// (BaseSHA) and the write's author, so a local land refuses exactly what the
// required check would refuse. It is not a copy of that logic.
//
// The verdict follows the exit code. Exit 0 with a decodable report is a pass.
// Exit 1 with a decodable report that carries at least one violation is a
// refusal listing those codes. Every other outcome is an error, never a
// verdict: runValidateCI also exits 1, with nothing on stdout, for a missing or
// unparseable space.yaml, a git failure and a manifest-policy failure, and
// calling any of those "the write is invalid" would blame the write for a
// problem in the checkout.
func NewLandCheck(engine *validate.Engine) host.LandCheckFunc {
	return func(ctx context.Context, candidate host.LandCandidate) (host.LandVerdict, error) {
		var stdout, stderr bytes.Buffer
		exit := runValidateCI(ctx, engine, candidate.CheckoutDir, gitDiffNameOnly, "v3-pr", candidate.BaseSHA, candidate.Author,
			IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
		return decodeLandCheckRun(exit, stdout.Bytes(), stderr.String())
	}
}

// decodeLandCheckRun turns one runValidateCI outcome into a verdict or a
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
