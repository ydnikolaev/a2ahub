// Package fakegithub is an in-process stand-in for the GitHub REST/GraphQL
// surface internal/host talks to, backed by REAL local git repositories.
//
// It exists because the exec'd `a2a` binary could not be driven against a
// host at all: cmd/a2a wired a hardcoded API root, so every host-facing
// verb — submit, every lifecycle verb, every contract sub-verb, feedback
// submit — was unreachable from a script, and the wiring closures that
// assemble them (cmd/a2a/wire.go's runSubmit/runContract/runLifecycle/
// runFeedback, resolveCredential, resolveTargetSpaceRef) were executed by
// no test at all. Point A2A_GITHUB_API at Server.URL and the whole chain
// runs for real: real config load, real credential resolution, real mirror
// clone, real `git push`, real PR open — with nothing leaving the machine.
//
// It is a TEST DOUBLE, not a GitHub emulator: it implements exactly the
// calls internal/host makes. Merges are REAL git merges into a work clone,
// not ref moves — the funnel branches from the mirror's own possibly-stale
// HEAD, so a PR is routinely not a descendant of the current base, and a
// fast-forward stand-in would rewind the space instead of merging it.
package fakegithub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// PR is one pull request the fake has seen.
type PR struct {
	Number int
	Head   string // "<branch>" for a same-repo PR, "<owner>:<branch>" from a fork
	Base   string
	Title  string
	Body   string
	State  string // "open" | "merged"
	Merged bool
	// MergeCommitSHA is recorded ONCE, at merge time, from the base branch's
	// tip immediately after this PR's own merge lands. It must not be
	// re-resolved later: the base moves on with every subsequent merge onto
	// it, and re-resolving at read time would hand a caller a LATER PR's
	// merge commit under this PR's number.
	MergeCommitSHA string
	// AutoMergeArmed tracks whether enablePullRequestAutoMerge actually
	// armed THIS PR — set inside handleGraphQL, on both the arming path and
	// the AutoMergeAlreadyClean refusal path (which must leave it false: a
	// refusal is "not armed", and getting that backwards would make the
	// refusal untestable through host.ListOpenPRs' own AutoMergeArmed
	// field). It drives the `auto_merge` field in pullPayload — GitHub
	// reports that field as an object when armed and null otherwise, and
	// internal/host.ListOpenPRs' AutoMergeArmed is exactly `auto_merge !=
	// nil`, so an omitted/always-null field here would make every open PR
	// look like auto-merge was never armed, regardless of the truth.
	AutoMergeArmed bool
}

// Server is the fake host. Construct with New, point A2A_GITHUB_API at
// URL, and inspect the recorded calls afterwards.
type Server struct {
	// URL is the API root to hand the binary via A2A_GITHUB_API.
	URL string
	// OriginDir is the bare repository PRs merge into.
	OriginDir string

	// AutoMerge, when true (the default), fast-forwards the base branch to
	// the PR's head as soon as auto-merge is enabled — the space's own CI
	// gate is not modelled here, so "auto-merge armed" means "merged".
	// Set false to leave PRs open (the pending-merge path).
	AutoMerge bool
	// CheckState/CheckConclusion are what CheckStatus reports.
	CheckState      string
	CheckConclusion string
	// CheckName is the check run's NAME — the field internal/host selects
	// on. It defaults to the post-P33 COMPOUND context a migrated space
	// really emits (`<caller-job> / <reusable-job>`), so the default rig
	// exercises the shape the fleet is moving to; set it to the flat
	// `a2a-validate` to stand in for an un-migrated space.
	CheckName string
	// ReviewApprovals are the logins whose latest review is an approval.
	ReviewApprovals []string
	// AllowAutoMerge is what GET /repos/{owner}/{name} reports as the repo's
	// `allow_auto_merge` setting (host.GitHubHost.AutoMergeAllowed). Defaults
	// to true — a correctly-configured space — so no existing test's
	// behaviour changes; set false to drive the WAVE M2 doctor FAIL path.
	AllowAutoMerge bool
	// AllowMergeCommit/AllowSquashMerge/AllowRebaseMerge are what GET
	// /repos/{owner}/{name} reports for the same three merge-method flags
	// host.GitHubHost.MergePR reads to pick an explicit merge_method
	// (WAVE M4). All default true — a freshly created repository's own
	// default — so MergePR's default preference (merge commit) is what a
	// test sees unless it narrows one of these.
	AllowMergeCommit bool
	AllowSquashMerge bool
	AllowRebaseMerge bool
	// AutoMergeAlreadyClean, when true, makes the enable-auto-merge mutation
	// answer GitHub's REAL "clean status" refusal (WAVE M4) instead of
	// arming/merging: `errors: [{"message": "... is in clean status"}]`,
	// the exact wording internal/host's autoMergeRefusal/IsAutoMergeAlreadyClean
	// match against. Reaching a merge from here therefore requires the
	// caller to land the PR itself via the merge endpoint below — this flag
	// is what makes that scenario reproducible over a real HTTP path rather
	// than only inside internal/host's own httptest-server unit tests.
	// False (default) preserves every existing test's behaviour.
	AutoMergeAlreadyClean bool
	// MergeableState is what a PR's list/get payload reports as
	// `mergeable_state`. Defaults to "clean" (GitHub's steady-state value for
	// a PR with no conflicts); set it to drive a different value in a test
	// that exercises a caller's handling of, e.g., "dirty" or "blocked".
	MergeableState string

	t    testing.TB
	mu   sync.Mutex
	srv  *httptest.Server
	prs  []*PR
	next int
	// work is the non-bare clone merges are performed in (created lazily).
	work string
	// forks maps a login to the bare repo standing in for their fork.
	forks map[string]string
	// requests records every path the binary asked for, in order — the
	// evidence that a verb really reached the host.
	requests []string
}

// New starts a fake host in front of originDir (a bare repository, e.g.
// spacefixture's own origin) and stops it when the test ends.
func New(t testing.TB, originDir string) *Server {
	t.Helper()
	s := &Server{
		OriginDir:        originDir,
		AutoMerge:        true,
		CheckState:       "completed",
		CheckConclusion:  "success",
		CheckName:        "a2a-validate / validate",
		AllowAutoMerge:   true,
		AllowMergeCommit: true,
		AllowSquashMerge: true,
		AllowRebaseMerge: true,
		MergeableState:   "clean",
		t:                t,
		forks:            make(map[string]string),
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.route))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// PRs returns the pull requests opened so far.
func (s *Server) PRs() []PR {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PR, 0, len(s.prs))
	for _, pr := range s.prs {
		out = append(out, *pr)
	}
	return out
}

// Requests returns every API path the binary called, in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// DenyPushes makes the origin refuse every push the way GitHub refuses a
// non-collaborator: a pre-receive hook that fails with GitHub's own
// wording, relayed by git as `remote: Permission to … denied to <login>`.
// This drives the REAL stderr classifier in internal/host, not a
// hand-written string.
func (s *Server) DenyPushes(login string) {
	s.t.Helper()
	hookDir := filepath.Join(s.OriginDir, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		s.t.Fatalf("fakegithub: mkdir hooks: %v", err)
	}
	hook := "#!/bin/sh\n" +
		"echo \"Permission to " + filepath.Base(s.OriginDir) + " denied to " + login + ".\" >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(hookDir, "pre-receive"), []byte(hook), 0o755); err != nil {
		s.t.Fatalf("fakegithub: write pre-receive hook: %v", err)
	}
}

// ForkDir returns the bare repo standing in for login's fork ("" if none
// was created).
func (s *Server) ForkDir(login string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forks[login]
}

// ForkLogin is the account EnsureFork forks as.
const ForkLogin = "consumer"

// Forbidden by design — do NOT add routes for these. Each is a decision only
// a real provider can make (branch protection state, repo settings, Actions
// execution, an org's repo-creation policy…), and a fake that invented an
// answer would let a provider-only scenario pass against a verdict this fake
// made up — strictly worse than the 404 the switch's default case already
// gives them, because the resulting report would read as complete rather
// than as "not exercised here":
//
//   - PUT/DELETE /repos/{o}/{n}/branches/{b}/protection  (branch protection)
//   - PATCH      /repos/{o}/{n}                          (repo settings)
//   - POST       /orgs/{org}/repos                       (repo creation)
//   - GET        /repos/{o}/{n}/actions/runs
//     POST       /repos/{o}/{n}/actions/runs/{id}/rerun
//     GET        /repos/{o}/{n}/actions/runs/{id}/jobs   (Actions runs)
//   - PUT        /repos/{o}/{n}/pulls/{num}/update-branch
//   - GET        /repos/{o}/{n}/check-runs/{id}/annotations
//   - PATCH      /repos/{o}/{n}/pulls/{num}
//
// If a scenario needs one of these, it needs a real provider, not a bigger
// fake.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	s.mu.Unlock()

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/graphql":
		s.handleGraphQL(w, r)
	// /repos/{owner}/{name}/...
	case len(parts) == 4 && parts[0] == "repos" && parts[3] == "pulls" && r.Method == http.MethodGet:
		s.handleFindPR(w, r)
	case len(parts) == 4 && parts[0] == "repos" && parts[3] == "pulls" && r.Method == http.MethodPost:
		s.handleOpenPR(w, r)
	case len(parts) == 5 && parts[0] == "repos" && parts[3] == "pulls" && r.Method == http.MethodGet:
		s.handleGetPR(w, r, parts[4])
	case len(parts) == 4 && parts[0] == "repos" && parts[3] == "forks" && r.Method == http.MethodPost:
		s.handleFork(w, r, parts[2])
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "pulls" && parts[5] == "merge" && r.Method == http.MethodPut:
		s.handleMergePR(w, r, parts[4])
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "pulls" && parts[5] == "reviews":
		s.writeJSON(w, s.reviewPayload())
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "commits" && parts[5] == "check-runs":
		// head_sha echoes the SHA the run was ASKED for, which is the
		// non-stale case. Real GitHub can answer with a run computed against
		// an older ref, and CheckStatusResult.HeadSHA exists so a caller can
		// tell the two apart; a fake that omitted the field would make every
		// hermetic caller read an empty ref and skip that comparison.
		s.writeJSON(w, map[string]any{"check_runs": []map[string]any{
			{"name": s.CheckName, "status": s.CheckState, "conclusion": s.CheckConclusion, "head_sha": parts[4]},
		}})
	case len(parts) == 3 && parts[0] == "repos" && r.Method == http.MethodGet:
		s.writeJSON(w, map[string]any{
			"name": parts[2], "allow_auto_merge": s.AllowAutoMerge,
			"allow_merge_commit": s.AllowMergeCommit,
			"allow_squash_merge": s.AllowSquashMerge,
			"allow_rebase_merge": s.AllowRebaseMerge,
		})
	case len(parts) == 4 && parts[0] == "repos" && parts[3] == "branches" && r.Method == http.MethodGet:
		s.handleBranches(w, r)
	case len(parts) >= 6 && parts[0] == "repos" && parts[3] == "git" && parts[4] == "refs" && r.Method == http.MethodDelete:
		s.handleDeleteRef(w, parts)
	case len(parts) == 5 && parts[0] == "repos" && parts[3] == "codeowners" && parts[4] == "errors" && r.Method == http.MethodGet:
		s.writeJSON(w, map[string]any{"errors": []any{}})
	case len(parts) == 1 && parts[0] == "rate_limit" && r.Method == http.MethodGet:
		// TokenScopes (internal/host/github.go) reads this endpoint only for
		// its X-OAuth-Scopes response HEADER, never its body — real GitHub
		// documents /rate_limit as not counting against the rate limit, which
		// is the whole reason it is the probe used there. This fake sets no
		// such header, the faithful answer for a fine-grained PAT / App
		// installation token (the case TokenScopes calls "reported=false").
		// The body below is shaped like GitHub's real response purely so a
		// caller that DOES decode it (none does today) sees something sane.
		s.writeJSON(w, map[string]any{
			"resources": map[string]any{
				"core": map[string]any{"limit": 5000, "remaining": 4999, "reset": 0},
			},
			"rate": map[string]any{"limit": 5000, "remaining": 4999, "reset": 0},
		})
	case len(parts) == 2 && parts[0] == "users" && r.Method == http.MethodGet:
		// FetchAvatar (internal/host/avatar.go) resolves a login here only to
		// read avatar_url; an empty string is a faithful "no avatar source"
		// answer a test can override by pre-seeding a source URL instead.
		s.writeJSON(w, map[string]any{"login": parts[1], "avatar_url": ""})
	default:
		http.Error(w, "fakegithub: unhandled "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// handleFindPR serves the unfiltered/filtered pull listing — both the write
// funnel's head-keyed idempotency lookup (`head=` set) AND
// internal/livee2e's ghClient.ListPulls (`head` absent, `state=all`, paged).
//
// `head` absent means "every PR", matching real GitHub: this fake used to
// treat an absent `head` the same as an EMPTY one, which matched nothing —
// silently returning `[]` to a caller that asked for the whole list.
func (s *Server) handleFindPR(w http.ResponseWriter, r *http.Request) {
	head := r.URL.Query().Get("head")
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open" // GitHub's own default when the param is absent
	}
	page := pageParam(r)

	// GitHub's filter is `<owner>:<branch>`; a same-repo PR is recorded
	// under the bare branch, so match either shape. Only applied when head
	// was actually asked for — see the func doc above.
	var branch string
	if head != "" {
		_, branch = splitHead(head)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	// A page past the first is always empty: every PR this fake will ever
	// have fits on page 1, and ListPulls/ListBranches (internal/livee2e) both
	// treat a short page as the last page, so this terminates their
	// pagination loop correctly without this fake inventing a row cap.
	if page == 1 {
		for _, pr := range s.prs {
			if head != "" && pr.Head != head && pr.Head != branch {
				continue
			}
			if state != "all" && prAPIState(pr) != state {
				continue
			}
			out = append(out, s.pullPayload(pr))
		}
	}
	s.writeJSON(w, out)
}

func (s *Server) handleGetPR(w http.ResponseWriter, _ *http.Request, numStr string) {
	var number int
	if _, err := fmt.Sscanf(numStr, "%d", &number); err != nil {
		http.Error(w, "fakegithub: bad pr number", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pr := range s.prs {
		if pr.Number != number {
			continue
		}
		s.writeJSON(w, s.pullPayload(pr))
		return
	}
	http.Error(w, "fakegithub: no such pr", http.StatusNotFound)
}

// pullPayload builds ONE pull request's wire shape — the single builder both
// handleFindPR (list) and handleGetPR (single) route through, so the two
// answers cannot diverge in what a PR "looks like" the way mergeByNumber is
// the one merge implementation both merge paths route through. Callers must
// already hold s.mu; this does not lock (it calls s.revParse, itself
// lock-free, the same way handleGetPR always has).
func (s *Server) pullPayload(pr *PR) map[string]any {
	// head.ref is the bare branch even for a cross-fork PR — real GitHub
	// reports the branch name alone there, while pr.Head (and hence the
	// idempotency match above) keeps the `owner:branch` form. Conflating the
	// two would make a caller matching on HeadRef see a fork owner prefix
	// that GitHub itself never sends.
	_, branch := splitHead(pr.Head)
	// auto_merge is null (Go: untyped nil, which encoding/json renders as
	// `null`) when never armed or refused, an object when armed —
	// host.ListOpenPRs' AutoMergeArmed is EXACTLY `auto_merge != nil`, and
	// the "stuck green PRs" doctor probe (internal/cli/cmd_doctor.go) skips
	// every PR that field reports as armed. A fake that always answered
	// null here would make that probe see "nobody armed auto-merge" as a
	// FACT it invented rather than the truth — the same failure class the
	// forbidden-endpoint comment on route exists to prevent, one layer in
	// rather than at the router.
	var autoMerge any
	if pr.AutoMergeArmed {
		autoMerge = map[string]any{"enabled_by": map[string]any{"login": "fakegithub"}}
	}
	return map[string]any{
		"number": pr.Number, "html_url": s.prURL(pr.Number),
		// node_id is what host.EnableAutoMerge resolves before it can run
		// the GraphQL mutation — the funnel's retry path re-arms auto-merge
		// on an existing PR, and without this the fake would make that path
		// untestable.
		"node_id": fmt.Sprintf("PR_%d", pr.Number),
		"state":   prAPIState(pr), "merged": pr.Merged,
		// body is what the write funnel's operation-key retry reads back to
		// confirm a found branch belongs to the same operation before
		// repairing its pull request. Real GitHub returns it from the list
		// endpoint; omitting it here made EVERY keyed retry refuse with
		// "operation branch metadata does not match" in every hermetic
		// test, so the repair-rather-than-duplicate property was unprovable
		// locally and could only ever have been discovered live.
		"body":             pr.Body,
		"head":             map[string]any{"ref": branch, "sha": s.revParse(headRef(pr))},
		"merge_commit_sha": pr.MergeCommitSHA,
		"mergeable_state":  s.MergeableState,
		"auto_merge":       autoMerge,
	}
}

// pageParam reads GitHub's `page` query param, defaulting to 1 the way a
// missing param does on the real API.
func pageParam(r *http.Request) int {
	raw := r.URL.Query().Get("page")
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func (s *Server) handleOpenPR(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "fakegithub: bad create body", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pr := range s.prs {
		if pr.Head == body.Head && pr.State == "open" {
			// GitHub refuses a second PR for the same head — the shape a
			// broken idempotency check would actually hit.
			http.Error(w, `{"message":"A pull request already exists for `+body.Head+`."}`, http.StatusUnprocessableEntity)
			return
		}
	}
	s.next++
	pr := &PR{Number: s.next, Head: body.Head, Base: body.Base, Title: body.Title, Body: body.Body, State: "open"}
	s.prs = append(s.prs, pr)
	s.writeJSON(w, map[string]any{
		"number": pr.Number, "html_url": s.prURL(pr.Number),
		"node_id": fmt.Sprintf("PR_%d", pr.Number), "state": "open",
	})
}

// handleGraphQL serves the one mutation internal/host issues:
// enablePullRequestAutoMerge. With AutoMerge set, arming it merges.
func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "fakegithub: bad graphql body", http.StatusBadRequest)
		return
	}
	if strings.Contains(body.Query, "enablePullRequestAutoMerge") {
		nodeID, _ := body.Variables["id"].(string)
		if s.AutoMergeAlreadyClean {
			// The mutation is REFUSED — nothing gets armed. Set this
			// explicitly rather than leaving whatever the PR's flag already
			// was, so a refusal always wins over an earlier successful arm.
			s.setAutoMergeArmed(nodeID, false)
			s.writeJSON(w, map[string]any{
				"data": map[string]any{"enablePullRequestAutoMerge": nil},
				"errors": []map[string]any{
					{"message": "Pull request Pull request is in clean status", "type": "UNPROCESSABLE"},
				},
			})
			return
		}
		// The mutation itself SUCCEEDS here regardless of s.AutoMerge — that
		// flag only controls whether this fake also fast-forwards the base
		// immediately (its stand-in for "the space's own CI already passed").
		// With AutoMerge=false the PR is armed but stays open (the
		// pending-merge case), and armed is exactly what it must report.
		s.setAutoMergeArmed(nodeID, true)
		if s.AutoMerge {
			s.mergeByNodeID(nodeID)
		}
	}
	s.writeJSON(w, map[string]any{"data": map[string]any{}})
}

// setAutoMergeArmed records whether enablePullRequestAutoMerge armed the PR
// nodeID identifies — the one field host.ListOpenPRs' AutoMergeArmed reads
// (`auto_merge != nil`), which pullPayload emits from this flag.
func (s *Server) setAutoMergeArmed(nodeID string, armed bool) {
	var number int
	if _, err := fmt.Sscanf(nodeID, "PR_%d", &number); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pr := range s.prs {
		if pr.Number == number {
			pr.AutoMergeArmed = armed
			return
		}
	}
}

func (s *Server) handleFork(w http.ResponseWriter, _ *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir, ok := s.forks[ForkLogin]
	if !ok {
		dir = filepath.Join(filepath.Dir(s.OriginDir), "fork-"+ForkLogin+".git")
		if err := s.git("", "clone", "--bare", s.OriginDir, dir); err != nil {
			http.Error(w, "fakegithub: cannot create fork: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// A fork inherits no hooks — this is the whole point: the
		// submitter can push here even when the origin refuses them.
		s.forks[ForkLogin] = dir
	}
	s.writeJSON(w, map[string]any{
		"name": name, "clone_url": dir,
		"owner": map[string]any{"login": ForkLogin},
	})
}

// handleBranches serves GET /repos/{owner}/{name}/branches — REAL git, not a
// canned answer, so a branch this fake's own merge/delete-ref path
// creates or removes is reflected immediately. Caller: ghClient.ListBranches
// (internal/livee2e).
func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	page := pageParam(r)
	s.mu.Lock()
	out, err := s.gitOutput(s.OriginDir, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "fakegithub: list branches: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := []map[string]any{}
	// Same pagination tolerance as handleFindPR: everything on page 1, an
	// empty page thereafter — every branch this fake will ever have fits on
	// page 1, and the caller treats a short page as the last one.
	if page == 1 {
		for _, name := range strings.Split(out, "\n") {
			if name == "" {
				continue
			}
			resp = append(resp, map[string]any{"name": name})
		}
	}
	s.writeJSON(w, resp)
}

// handleDeleteRef serves DELETE /repos/{owner}/{name}/git/refs/{ref...},
// where {ref...} is everything after ".../git/refs/" verbatim — this
// project's branch names contain slashes (e.g. "heads/a2a/alpha/submit/
// XQ-1"), so the ref cannot be assumed to be a fixed number of path
// segments. parts is route's own split of the request path; parts[5:] is
// the ref.
//
// `git update-ref -d` exits 0 even when the ref does not exist (verified:
// it is NOT an error there), so a real existence check comes first —
// ghClient.DeleteRef (internal/livee2e) treats 404 specifically as a no-op,
// and a wrong 204/500 here would hide that contract.
func (s *Server) handleDeleteRef(w http.ResponseWriter, parts []string) {
	ref := strings.Join(parts[5:], "/")
	full := "refs/" + ref

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revParse(full) == "" {
		http.Error(w, "fakegithub: no such ref "+full, http.StatusNotFound)
		return
	}
	if err := s.git(s.OriginDir, "update-ref", "-d", full); err != nil {
		http.Error(w, "fakegithub: delete ref: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mergeByNodeID fast-forwards the base branch to the PR's head — what
// auto-merge does to the linear branches the write funnel pushes. A
// cross-fork head is fetched from the fork's bare repo first.
func (s *Server) mergeByNodeID(nodeID string) {
	var number int
	if _, err := fmt.Sscanf(nodeID, "PR_%d", &number); err != nil {
		return
	}
	if err := s.mergeByNumber(number); err != nil {
		s.t.Errorf("%v", err)
	}
}

// handleMergePR serves PUT /repos/{owner}/{repo}/pulls/{number}/merge — the
// DIRECT merge host.GitHubHost.MergePR issues (WAVE M4). It requires an
// explicit merge_method the same way real GitHub does (a request without one
// is a caller bug, not something to paper over), and reports a merge failure
// as 405 — the one status internal/host classifies as ErrPRNotMergeable.
func (s *Server) handleMergePR(w http.ResponseWriter, r *http.Request, numStr string) {
	var number int
	if _, err := fmt.Sscanf(numStr, "%d", &number); err != nil {
		http.Error(w, "fakegithub: bad pr number", http.StatusBadRequest)
		return
	}
	var body struct {
		MergeMethod string `json:"merge_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "fakegithub: bad merge body", http.StatusBadRequest)
		return
	}
	if body.MergeMethod == "" {
		http.Error(w, `{"message":"merge_method is required"}`, http.StatusUnprocessableEntity)
		return
	}
	if err := s.mergeByNumber(number); err != nil {
		http.Error(w, `{"message":"`+strings.ReplaceAll(err.Error(), `"`, `'`)+`"}`, http.StatusMethodNotAllowed)
		return
	}
	s.writeJSON(w, map[string]any{"merged": true, "message": "Pull Request successfully merged"})
}

// mergeByNumber performs the REAL git merge (see merge's own doc) and marks
// the PR merged — the one merge implementation mergeByNodeID (auto-merge)
// and handleMergePR (direct merge) both route through, so the two paths
// cannot diverge in what "merged" means inside this fake.
func (s *Server) mergeByNumber(number int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pr := range s.prs {
		if pr.Number != number {
			continue
		}
		owner, branch := splitHead(pr.Head)
		headRepo := s.OriginDir
		if owner != "" {
			forkDir, ok := s.forks[owner]
			if !ok {
				return fmt.Errorf("fakegithub: PR %d heads from unknown fork %q", pr.Number, owner)
			}
			headRepo = forkDir
			// Keep a copy of the fork's head inside the origin so a later
			// CheckStatus can resolve the PR's head SHA.
			if err := s.git(s.OriginDir, "fetch", forkDir, "refs/heads/"+branch+":refs/fork/"+branch); err != nil {
				return fmt.Errorf("fakegithub: fetch fork head: %w", err)
			}
		}
		if err := s.merge(headRepo, branch, pr.Base); err != nil {
			return fmt.Errorf("fakegithub: merge PR %d: %w", pr.Number, err)
		}
		// Recorded HERE, immediately after the push to origin lands, and
		// never re-resolved later: pr.Base's tip moves on with every
		// subsequent merge onto it, so resolving this lazily at read time
		// would hand out a LATER PR's merge commit under this PR's number.
		pr.MergeCommitSHA = s.revParse("refs/heads/" + pr.Base)
		pr.State, pr.Merged = "merged", true
		return nil
	}
	return fmt.Errorf("fakegithub: no such pr %d", number)
}

// merge performs a REAL git merge of headRepo's branch into base and pushes
// the result to the origin — not an update-ref fast-forward.
//
// The difference matters: the write funnel branches from the mirror's own
// (possibly stale) HEAD, so a PR is routinely NOT a descendant of the
// current base. A fast-forward stand-in would silently rewind the space and
// the loss would look like a product bug; a real merge behaves the way
// GitHub does, and a genuine conflict fails loudly here.
func (s *Server) merge(headRepo, branch, base string) error {
	work, err := s.workClone()
	if err != nil {
		return err
	}
	// DenyPushes refuses the SUBMITTER; the host's own merge is not a
	// submitter, so the hook steps aside for it.
	if restore, herr := s.suspendPushDenial(); herr != nil {
		return herr
	} else if restore != nil {
		defer restore()
	}
	if err := s.git(work, "fetch", "origin"); err != nil {
		return err
	}
	if err := s.git(work, "checkout", "-B", base, "origin/"+base); err != nil {
		return err
	}
	if err := s.git(work, "fetch", headRepo, "refs/heads/"+branch); err != nil {
		return err
	}
	if err := s.git(work, "-c", "user.name=fakegithub", "-c", "user.email=fakegithub@a2ahub.invalid",
		"merge", "--no-edit", "FETCH_HEAD"); err != nil {
		return err
	}
	return s.git(work, "push", "origin", base)
}

// suspendPushDenial moves the push-denial hook aside and returns the
// restore func (nil when no denial is installed).
func (s *Server) suspendPushDenial() (restore func(), err error) {
	hook := filepath.Join(s.OriginDir, "hooks", "pre-receive")
	if _, statErr := os.Stat(hook); statErr != nil {
		return nil, nil //nolint:nilerr // reason: no hook installed is the normal case
	}
	parked := hook + ".parked"
	if err := os.Rename(hook, parked); err != nil {
		return nil, err
	}
	return func() {
		if err := os.Rename(parked, hook); err != nil {
			s.t.Errorf("fakegithub: restore push denial: %v", err)
		}
	}, nil
}

// workClone lazily creates the non-bare clone merges happen in (a bare
// repository has no work tree to merge in).
func (s *Server) workClone() (string, error) {
	if s.work != "" {
		return s.work, nil
	}
	dir := filepath.Join(filepath.Dir(s.OriginDir), "fakegithub-merge-work")
	if err := s.git("", "clone", s.OriginDir, dir); err != nil {
		return "", err
	}
	s.work = dir
	return dir, nil
}

func (s *Server) reviewPayload() []map[string]any {
	out := make([]map[string]any, 0, len(s.ReviewApprovals))
	for _, login := range s.ReviewApprovals {
		out = append(out, map[string]any{
			"state": "APPROVED", "user": map[string]any{"login": login},
		})
	}
	return out
}

func (s *Server) prURL(number int) string {
	return fmt.Sprintf("https://example.invalid/pr/%d", number)
}

// splitHead separates GitHub's `<owner>:<branch>` head reference. A
// same-repo head carries no owner, and its branch is the whole string —
// conflating the two silently produces an empty ref, which git rejects
// only at use time.
func splitHead(head string) (owner, branch string) {
	if owner, branch, found := strings.Cut(head, ":"); found {
		return owner, branch
	}
	return "", head
}

// headRef renders the ref a PR's head lives under inside OriginDir.
func headRef(pr *PR) string {
	owner, branch := splitHead(pr.Head)
	if owner != "" {
		return "refs/fork/" + branch
	}
	return "refs/heads/" + branch
}

func prAPIState(pr *PR) string {
	if pr.Merged {
		return "closed" // GitHub reports a merged PR as closed+merged
	}
	return "open"
}

func (s *Server) revParse(ref string) string {
	cmd := exec.Command("git", gitfixture.Args("-C", s.OriginDir, "rev-parse", ref)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (s *Server) git(dir string, args ...string) error {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", gitfixture.Args(full...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w: %s", full, err, out)
	}
	return nil
}

// gitOutput is s.git's read counterpart: it returns the command's trimmed
// stdout instead of only an error. Left as its own function rather than
// folding into s.git so the merge/push paths (which want CombinedOutput's
// stderr-on-failure diagnostics) are untouched.
func (s *Server) gitOutput(dir string, args ...string) (string, error) {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", gitfixture.Args(full...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v: %w", full, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Server) writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.t.Errorf("fakegithub: encode response: %v", err)
	}
}
