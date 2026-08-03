package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestOpenPRPreservesCreatedPRInTypedPartialError(t *testing.T) {
	t.Parallel()

	const providerDetail = "Resource not accessible by personal access token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/space":
			_, _ = w.Write([]byte(`{"allow_squash_merge":true,"visibility":"private"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/space/pulls":
			_, _ = w.Write([]byte(`{"number":8,"html_url":"https://example.invalid/pull/8","node_id":"PR_8","state":"open"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			_, _ = w.Write([]byte(`{"errors":[{"message":"` + providerDetail + `"}]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := NewGitHubHost(srv.Client(), srv.URL)
	pr, err := h.OpenPR(context.Background(), OpenPRRequest{
		Repo:            Repo{Owner: "acme", Name: "space"},
		Head:            "a2a/axon/XQ-axon-1",
		Base:            "main",
		Title:           "question",
		ExpectedHeadSHA: "green-sha",
		Credential:      Credential{Token: "secret-token"},
	})
	if err == nil {
		t.Fatal("OpenPR error = nil, want typed partial failure")
	}
	if pr.Number != 8 || pr.URL == "" || pr.State != "open" {
		t.Fatalf("PRInfo = %+v, want the confirmed created PR", pr)
	}
	if pr.MergeMethod != MergeMethodSquash {
		t.Fatalf("PRInfo.MergeMethod = %q, want %q", pr.MergeMethod, MergeMethodSquash)
	}
	var partial *PartialWriteError
	if !errors.As(err, &partial) {
		t.Fatalf("error = %T %v, want *PartialWriteError", err, err)
	}
	if partial.Stage != PartialWriteStagePRCreated || partial.Operation != PartialWriteOperationEnableAutoMerge {
		t.Fatalf("partial = %+v, want pr-created/enable-auto-merge", partial)
	}
	if !errors.Is(err, ErrGraphQLFailed) {
		t.Fatalf("error = %v, want wrapped ErrGraphQLFailed", err)
	}
	if strings.Contains(err.Error(), providerDetail) || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("stable partial error leaked provider detail or credential: %v", err)
	}
}

func TestOpenPRRefusesBeforeCreateWhenMergePolicyIsUnavailable(t *testing.T) {
	t.Parallel()

	var writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allow_merge_commit":false,"allow_squash_merge":false,"allow_rebase_merge":false}`))
	}))
	defer srv.Close()

	pr, err := NewGitHubHost(srv.Client(), srv.URL).OpenPR(context.Background(), OpenPRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main", ExpectedHeadSHA: "green-sha",
	})
	if !errors.Is(err, ErrMergeMethodUnavailable) {
		t.Fatalf("OpenPR error = %v, want ErrMergeMethodUnavailable", err)
	}
	if pr != (PRInfo{}) {
		t.Fatalf("PRInfo = %+v, want zero before PR creation", pr)
	}
	if writes != 0 {
		t.Fatalf("write requests = %d, want zero", writes)
	}
	var partial *PartialWriteError
	if errors.As(err, &partial) {
		t.Fatalf("pre-create error classified partial: %+v", partial)
	}
}

func TestOpenPRPreCreateFailureIsNotPartial(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"allow_merge_commit":true}`))
			return
		}
		http.Error(w, `{"message":"create refused"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	pr, err := NewGitHubHost(srv.Client(), srv.URL).OpenPR(context.Background(), OpenPRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main", ExpectedHeadSHA: "green-sha",
	})
	if pr != (PRInfo{}) || !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("OpenPR = (%+v, %v), want zero PR + ErrRequestFailed", pr, err)
	}
	var partial *PartialWriteError
	if errors.As(err, &partial) {
		t.Fatalf("pre-create error classified partial: %+v", partial)
	}
}

func TestOpenPRClassifiesEveryPostCreateArmFailureAsPartial(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		nodeID      string
		graphStatus int
		graphBody   string
		wantCause   error
	}{
		{name: "created response missing node id", wantCause: ErrInvalidRequest},
		{name: "GraphQL HTTP failure", nodeID: "PR_7", graphStatus: http.StatusForbidden, graphBody: `{"message":"forbidden detail"}`, wantCause: ErrRequestFailed},
		{name: "malformed GraphQL response", nodeID: "PR_7", graphStatus: http.StatusOK, graphBody: `{"data":`, wantCause: ErrRequestFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/repos/acme/space":
					_, _ = w.Write([]byte(`{"allow_merge_commit":true}`))
				case "/repos/acme/space/pulls":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"number": 7, "html_url": "https://example.invalid/pull/7", "node_id": tc.nodeID, "state": "open",
					})
				case "/graphql":
					w.WriteHeader(tc.graphStatus)
					_, _ = w.Write([]byte(tc.graphBody))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()

			pr, err := NewGitHubHost(srv.Client(), srv.URL).OpenPR(context.Background(), OpenPRRequest{
				Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main", ExpectedHeadSHA: "green-sha",
			})
			if pr.Number != 7 || pr.URL == "" {
				t.Fatalf("PRInfo = %+v, want created PR", pr)
			}
			var partial *PartialWriteError
			if !errors.As(err, &partial) || partial.Stage != PartialWriteStagePRCreated {
				t.Fatalf("error = %T %v, want pr-created PartialWriteError", err, err)
			}
			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("error = %v, want wrapped %v", err, tc.wantCause)
			}
			if tc.graphBody != "" && strings.Contains(err.Error(), tc.graphBody) {
				t.Fatalf("stable error leaked provider body: %v", err)
			}
		})
	}
}

func TestOpenPRContextCancellationAfterCreateIsPartial(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls >= 3 {
			return nil, context.Canceled
		}
		body := `{"allow_merge_commit":true}`
		if calls == 2 {
			body = `{"number":7,"html_url":"https://example.invalid/pull/7","node_id":"PR_7","state":"open"}`
			cancel()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	h := NewGitHubHost(client, "https://example.invalid")
	h.RetryAttempts = 1

	pr, err := h.OpenPR(ctx, OpenPRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main", ExpectedHeadSHA: "green-sha",
	})
	if pr.Number != 7 || pr.URL == "" {
		t.Fatalf("PRInfo = %+v, want created PR", pr)
	}
	var partial *PartialWriteError
	if !errors.As(err, &partial) || partial.Stage != PartialWriteStagePRCreated {
		t.Fatalf("error = %T %v, want pr-created PartialWriteError", err, err)
	}
}

func TestMergePolicyIsExplicitAndSharedAcrossHostWrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		settings        string
		want            MergeMethod
		wantGraphQLEnum string
	}{
		{name: "merge", settings: `{"allow_merge_commit":true,"allow_squash_merge":true,"allow_rebase_merge":true}`, want: MergeMethodMerge, wantGraphQLEnum: "MERGE"},
		{name: "squash", settings: `{"allow_squash_merge":true,"allow_rebase_merge":true}`, want: MergeMethodSquash, wantGraphQLEnum: "SQUASH"},
		{name: "rebase", settings: `{"allow_rebase_merge":true}`, want: MergeMethodRebase, wantGraphQLEnum: "REBASE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var graphMethods, graphHeads []string
			var mergeMethod, mergeSHA string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/space":
					_, _ = w.Write([]byte(tc.settings))
				case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/space/pulls/7":
					_, _ = w.Write([]byte(`{"node_id":"PR_7","head":{"sha":"green-sha"}}`))
				case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/space/pulls":
					_, _ = w.Write([]byte(`{"number":7,"html_url":"https://example.invalid/pull/7","node_id":"PR_7","state":"open"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/graphql":
					var body struct {
						Variables map[string]any `json:"variables"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode GraphQL body: %v", err)
					}
					method, _ := body.Variables["method"].(string)
					graphMethods = append(graphMethods, method)
					head, _ := body.Variables["head"].(string)
					graphHeads = append(graphHeads, head)
					_, _ = w.Write([]byte(`{"data":{"enablePullRequestAutoMerge":{"clientMutationId":null}}}`))
				case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/space/pulls/7/merge":
					var body struct {
						Method string `json:"merge_method"`
						SHA    string `json:"sha"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode merge body: %v", err)
					}
					mergeMethod, mergeSHA = body.Method, body.SHA
					_, _ = w.Write([]byte(`{"merged":true}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusNotFound)
				}
			}))
			defer srv.Close()

			h := NewGitHubHost(srv.Client(), srv.URL)
			pr, err := h.OpenPR(context.Background(), OpenPRRequest{
				Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main", ExpectedHeadSHA: "green-sha",
			})
			if err != nil || pr.MergeMethod != tc.want {
				t.Fatalf("OpenPR = (%+v, %v), want method %q", pr, err, tc.want)
			}
			armedMethod, err := h.EnableAutoMerge(context.Background(), EnableAutoMergeRequest{
				Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha",
			})
			if err != nil || armedMethod != tc.want {
				t.Fatalf("EnableAutoMerge = (%q, %v), want (%q, nil)", armedMethod, err, tc.want)
			}
			mergedMethod, err := h.MergePR(context.Background(), MergePRRequest{
				Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha",
			})
			if err != nil || mergedMethod != tc.want {
				t.Fatalf("MergePR = (%q, %v), want (%q, nil)", mergedMethod, err, tc.want)
			}
			if len(graphMethods) != 2 || graphMethods[0] != tc.wantGraphQLEnum || graphMethods[1] != tc.wantGraphQLEnum {
				t.Fatalf("GraphQL methods = %v, want [%s %s]", graphMethods, tc.wantGraphQLEnum, tc.wantGraphQLEnum)
			}
			if len(graphHeads) != 2 || graphHeads[0] != "green-sha" || graphHeads[1] != "green-sha" {
				t.Fatalf("GraphQL expected heads = %v, want [green-sha green-sha]", graphHeads)
			}
			if mergeMethod != string(tc.want) || mergeSHA != "green-sha" {
				t.Fatalf("merge body = method %q sha %q, want %q/green-sha", mergeMethod, mergeSHA, tc.want)
			}
		})
	}
}

func TestEnableAutoMergeRefusesAChangedHeadBeforeGraphQL(t *testing.T) {
	t.Parallel()

	var graphQLCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/space":
			_, _ = w.Write([]byte(`{"allow_merge_commit":true}`))
		case "/repos/acme/space/pulls/7":
			_, _ = w.Write([]byte(`{"node_id":"PR_7","head":{"sha":"new-sha"}}`))
		case "/graphql":
			graphQLCalls++
			_, _ = w.Write([]byte(`{"data":{}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	method, err := NewGitHubHost(srv.Client(), srv.URL).EnableAutoMerge(context.Background(), EnableAutoMergeRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha",
	})
	if method != MergeMethodMerge || !errors.Is(err, ErrPRHeadMoved) {
		t.Fatalf("EnableAutoMerge = (%q, %v), want selected merge + ErrPRHeadMoved", method, err)
	}
	if graphQLCalls != 0 {
		t.Fatalf("GraphQL calls = %d, want zero for stale green result", graphQLCalls)
	}
}

func TestRepoVisibilityUsesTheSharedSettingsDecode(t *testing.T) {
	t.Parallel()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"visibility":"internal","allow_squash_merge":true,"permissions":{"pull":true}}`))
	}))
	defer srv.Close()

	visibility, err := NewGitHubHost(srv.Client(), srv.URL).RepoVisibility(context.Background(), RepoSettingsRequest{
		Repo: Repo{Owner: "acme", Name: "space"},
	})
	if err != nil || visibility != RepositoryVisibilityInternal {
		t.Fatalf("RepoVisibility = (%q, %v), want (internal, nil)", visibility, err)
	}
	if calls != 1 {
		t.Fatalf("settings calls = %d, want one", calls)
	}
}

func TestFakeHostRecordsResultBearingMergeOperations(t *testing.T) {
	t.Parallel()

	f := NewFakeHost()
	armMethod, err := f.EnableAutoMerge(context.Background(), EnableAutoMergeRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha",
	})
	if err != nil || armMethod != MergeMethodMerge {
		t.Fatalf("EnableAutoMerge = (%q, %v), want (merge, nil)", armMethod, err)
	}
	mergeMethod, err := f.MergePR(context.Background(), MergePRRequest{
		Repo: Repo{Owner: "acme", Name: "space"}, PRNumber: 7, ExpectedHeadSHA: "green-sha",
	})
	if err != nil || mergeMethod != MergeMethodMerge {
		t.Fatalf("MergePR = (%q, %v), want (merge, nil)", mergeMethod, err)
	}
	if len(f.AutoArms) != 1 || len(f.MergePRs) != 1 {
		t.Fatalf("recorded auto-arms/merges = %d/%d, want 1/1", len(f.AutoArms), len(f.MergePRs))
	}
}

func TestFakeHostPreservesConfiguredMergeResultsAndFailures(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("provider refused")
	f := NewFakeHost()
	f.EnableAutoMergeFunc = func(context.Context, EnableAutoMergeRequest) (MergeMethod, error) {
		return MergeMethodSquash, sentinel
	}
	f.MergePRFunc = func(context.Context, MergePRRequest) (MergeMethod, error) {
		return MergeMethodRebase, sentinel
	}

	armMethod, armErr := f.EnableAutoMerge(context.Background(), EnableAutoMergeRequest{PRNumber: 3})
	mergeMethod, mergeErr := f.MergePR(context.Background(), MergePRRequest{PRNumber: 3})
	if armMethod != MergeMethodSquash || !errors.Is(armErr, sentinel) {
		t.Fatalf("fake arm = (%q, %v), want squash/sentinel", armMethod, armErr)
	}
	if mergeMethod != MergeMethodRebase || !errors.Is(mergeErr, sentinel) {
		t.Fatalf("fake merge = (%q, %v), want rebase/sentinel", mergeMethod, mergeErr)
	}
	if len(f.AutoArms) != 1 || len(f.MergePRs) != 1 {
		t.Fatalf("recorded auto-arms/merges = %d/%d, want 1/1", len(f.AutoArms), len(f.MergePRs))
	}
}
