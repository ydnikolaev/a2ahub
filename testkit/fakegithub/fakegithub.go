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
	"strings"
	"sync"
	"testing"
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
	// ReviewApprovals are the logins whose latest review is an approval.
	ReviewApprovals []string

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
		OriginDir:       originDir,
		AutoMerge:       true,
		CheckState:      "completed",
		CheckConclusion: "success",
		t:               t,
		forks:           make(map[string]string),
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
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "pulls" && parts[5] == "reviews":
		s.writeJSON(w, s.reviewPayload())
	case len(parts) == 6 && parts[0] == "repos" && parts[3] == "commits" && parts[5] == "check-runs":
		s.writeJSON(w, map[string]any{"check_runs": []map[string]any{
			{"status": s.CheckState, "conclusion": s.CheckConclusion},
		}})
	case len(parts) == 3 && parts[0] == "repos" && r.Method == http.MethodGet:
		s.writeJSON(w, map[string]any{"name": parts[2]})
	default:
		http.Error(w, "fakegithub: unhandled "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// handleFindPR serves the list-with-head-filter — the write funnel's
// idempotency lookup.
func (s *Server) handleFindPR(w http.ResponseWriter, r *http.Request) {
	head := r.URL.Query().Get("head")
	// GitHub's filter is `<owner>:<branch>`; a same-repo PR is recorded
	// under the bare branch, so match either shape.
	_, branch := splitHead(head)

	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, pr := range s.prs {
		if pr.Head == head || pr.Head == branch {
			out = append(out, map[string]any{
				"number": pr.Number, "html_url": s.prURL(pr.Number),
				"state": prAPIState(pr), "merged": pr.Merged,
			})
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
		sha := s.revParse(headRef(pr))
		s.writeJSON(w, map[string]any{
			"number": pr.Number, "html_url": s.prURL(pr.Number),
			"state": prAPIState(pr), "merged": pr.Merged,
			"head": map[string]any{"sha": sha},
		})
		return
	}
	http.Error(w, "fakegithub: no such pr", http.StatusNotFound)
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
	if strings.Contains(body.Query, "enablePullRequestAutoMerge") && s.AutoMerge {
		nodeID, _ := body.Variables["id"].(string)
		s.mergeByNodeID(nodeID)
	}
	s.writeJSON(w, map[string]any{"data": map[string]any{}})
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

// mergeByNodeID fast-forwards the base branch to the PR's head — what
// auto-merge does to the linear branches the write funnel pushes. A
// cross-fork head is fetched from the fork's bare repo first.
func (s *Server) mergeByNodeID(nodeID string) {
	var number int
	if _, err := fmt.Sscanf(nodeID, "PR_%d", &number); err != nil {
		return
	}
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
				s.t.Errorf("fakegithub: PR %d heads from unknown fork %q", pr.Number, owner)
				return
			}
			headRepo = forkDir
			// Keep a copy of the fork's head inside the origin so a later
			// CheckStatus can resolve the PR's head SHA.
			if err := s.git(s.OriginDir, "fetch", forkDir, "refs/heads/"+branch+":refs/fork/"+branch); err != nil {
				s.t.Errorf("fakegithub: fetch fork head: %v", err)
				return
			}
		}
		if err := s.merge(headRepo, branch, pr.Base); err != nil {
			s.t.Errorf("fakegithub: merge PR %d: %v", pr.Number, err)
			return
		}
		pr.State, pr.Merged = "merged", true
		return
	}
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
	cmd := exec.Command("git", "-C", s.OriginDir, "rev-parse", ref)
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
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w: %s", full, err, out)
	}
	return nil
}

func (s *Server) writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.t.Errorf("fakegithub: encode response: %v", err)
	}
}
