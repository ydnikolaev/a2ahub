package space

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// ErrAwaitPRNotFound means the marker's recorded branch has no pull request.
var ErrAwaitPRNotFound = errors.New("space: recorded pending PR was not found")

// ErrAwaitClosed means the pull request closed without merging.
var ErrAwaitClosed = errors.New("space: pending PR closed without merge")

// ErrAwaitCheckFailed means the required check completed unsuccessfully.
var ErrAwaitCheckFailed = errors.New("space: required check failed")

// AwaitHost is the host read surface required by the pending-write poller.
type AwaitHost interface {
	FindPRByHeadBranch(context.Context, host.FindPRRequest) (*host.PRInfo, error)
	CheckStatus(context.Context, host.StatusRequest) (host.CheckStatusResult, error)
}

// AwaitRequest identifies a pending branch and supplies post-merge cleanup.
type AwaitRequest struct {
	Branch     string
	Repo       host.Repo
	Credential host.Credential
	Refresh    func(context.Context) error
	Clear      func() error
}

// AwaitResult reports the merged pull request.
type AwaitResult struct {
	PRURL string
}

// Awaiter owns the pending-write polling state machine. It observes but never
// merges; completion requires the host to report merged, then refresh+clear.
type Awaiter struct {
	host  AwaitHost
	wait  func(context.Context) error
	build version.Build
}

// NewAwaiter constructs a pending-write poller.
func NewAwaiter(h AwaitHost, build version.Build) *Awaiter {
	return &Awaiter{
		host:  h,
		build: build,
		wait: func(ctx context.Context) error {
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

// SetWaitForTest replaces the polling delay with a deterministic test seam.
func (a *Awaiter) SetWaitForTest(wait func(context.Context) error) {
	a.wait = wait
}

// Await polls until the recorded pull request merges or reaches a refusal.
func (a *Awaiter) Await(ctx context.Context, req AwaitRequest) (AwaitResult, error) {
	if a == nil || a.host == nil || req.Branch == "" || req.Repo.Owner == "" ||
		req.Repo.Name == "" || req.Refresh == nil || req.Clear == nil {
		return AwaitResult{}, fmt.Errorf("space: await: %w", host.ErrInvalidRequest)
	}
	for {
		pr, err := a.host.FindPRByHeadBranch(ctx, host.FindPRRequest{
			Repo: req.Repo, Branch: req.Branch, Credential: req.Credential,
		})
		if err != nil {
			return AwaitResult{}, fmt.Errorf("space: await: %w", err)
		}
		if pr == nil {
			return AwaitResult{}, ErrAwaitPRNotFound
		}
		switch pr.State {
		case "merged":
			if err := req.Refresh(ctx); err != nil {
				return AwaitResult{}, fmt.Errorf("space: await: merged, but mirror refresh failed: %w", err)
			}
			if err := req.Clear(); err != nil {
				return AwaitResult{}, fmt.Errorf("space: await: merged, but marker cleanup failed: %w", err)
			}
			return AwaitResult{PRURL: pr.URL}, nil
		case "closed":
			return AwaitResult{}, fmt.Errorf("%w: %s", ErrAwaitClosed, pr.URL)
		}

		status, err := a.host.CheckStatus(ctx, host.StatusRequest{
			Repo: req.Repo, PRNumber: pr.Number, Credential: req.Credential,
		})
		if err != nil {
			return AwaitResult{}, fmt.Errorf("space: await: %w", err)
		}
		if status.State == "completed" && status.Conclusion != "success" {
			return AwaitResult{}, fmt.Errorf(
				"%w: %q completed with %s%s",
				ErrAwaitCheckFailed, status.Name, status.Conclusion, a.localBuildHint(),
			)
		}
		if err := a.wait(ctx); err != nil {
			return AwaitResult{}, err
		}
	}
}

// localBuildHint explains a refused required check to the owner of a local
// build (D-040 point 8): the space's CI judges the write with its pinned
// RELEASED validator, so a data shape newer than the last release is refused
// there until a public release ships it. It lives here, below both transports,
// so the CLI and MCP render the same text. A release build gets "".
func (a *Awaiter) localBuildHint() string {
	if !a.build.IsLocal() {
		return ""
	}
	return fmt.Sprintf(" — this write was made by a local build (%s); the space's CI judges it with its pinned released validator, "+
		"so a data shape newer than v%s is refused there until a public release ships it. "+
		"Read the check's log: if it names a shape v%s does not know, cut a release; otherwise fix the write.",
		a.localStamp(), a.build.Version, a.build.Version)
}

// localStamp is the stamp with the sha cut to 12 characters, for prose.
func (a *Awaiter) localStamp() string {
	b := a.build
	if len(b.Commit) > 12 {
		b.Commit = b.Commit[:12]
	}
	return b.Stamp()
}
