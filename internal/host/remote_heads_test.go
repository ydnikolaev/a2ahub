package host

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestListContractPublishHeadsReadsTheExactRemoteNamespace(t *testing.T) {
	t.Parallel()

	fixture := newRemoteRecoveryFixture(t)
	runGit(t, fixture.source, recoveryGitIdentity(), "commit", "--allow-empty", "-m", "base")
	prefix := "a2a/atlas/contract-publish/op-v1-"
	wantBranch := prefix + strings.Repeat("a", 64)
	fixture.push(wantBranch)
	fixture.push("a2a/atlas/submit/op-v1-" + strings.Repeat("b", 64))

	listing, err := NewGitHubHost(nil, "").ListContractPublishHeads(
		context.Background(), fixture.probe, fixture.remote, prefix, Credential{},
	)
	if err != nil {
		t.Fatalf("ListContractPublishHeads: %v", err)
	}
	if !listing.Exhaustive || listing.Observed != 1 || len(listing.Heads) != 1 || listing.Heads[0].Branch != wantBranch {
		t.Fatalf("listing = %+v, want exact one-head namespace", listing)
	}
}

func TestParseContractPublishHeadListingProvesExhaustionAndSorts(t *testing.T) {
	t.Parallel()

	prefix := "a2a/atlas/contract-publish/op-v1-"
	raw := []byte(
		strings.Repeat("b", 40) + "\trefs/heads/" + prefix + strings.Repeat("2", 64) + "\n" +
			strings.Repeat("a", 40) + "\trefs/heads/" + prefix + strings.Repeat("1", 64) + "\n",
	)
	listing, err := parseContractPublishHeadListing(raw, prefix, MaxContractPublishRemoteHeads)
	if err != nil {
		t.Fatalf("parseContractPublishHeadListing: %v", err)
	}
	if !listing.Exhaustive || listing.Observed != 2 || len(listing.Heads) != 2 {
		t.Fatalf("listing = %+v, want exhaustive two-head proof", listing)
	}
	if listing.Heads[0].Branch >= listing.Heads[1].Branch {
		t.Fatalf("heads are not bytewise sorted: %+v", listing.Heads)
	}
}

func TestParseContractPublishHeadListingRefusesOrMarksEveryIncompleteShape(t *testing.T) {
	t.Parallel()

	prefix := "a2a/atlas/contract-publish/op-v1-"
	row := func(index int) string {
		return fmt.Sprintf("%040x\trefs/heads/%s%064x\n", index+1, prefix, index+1)
	}
	var over strings.Builder
	for index := 0; index < MaxContractPublishRemoteHeads+1; index++ {
		over.WriteString(row(index))
	}
	listing, err := parseContractPublishHeadListing([]byte(over.String()), prefix, MaxContractPublishRemoteHeads)
	if err != nil {
		t.Fatalf("257-head listing: %v", err)
	}
	if listing.Exhaustive || listing.Observed != 257 || len(listing.Heads) != 256 {
		t.Fatalf("257-head listing = %+v, want explicit bounded proof", listing)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{name: "unterminated", raw: strings.TrimSuffix(row(0), "\n")},
		{name: "wrong namespace", raw: strings.Replace(row(0), "a2a/atlas/", "a2a/other/", 1)},
		{name: "noncanonical key", raw: strings.Replace(row(0), strings.Repeat("0", 63)+"1", strings.Repeat("z", 64), 1)},
		{name: "duplicate", raw: row(0) + row(0)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseContractPublishHeadListing([]byte(test.raw), prefix, MaxContractPublishRemoteHeads); err == nil {
				t.Fatal("parseContractPublishHeadListing error = nil")
			}
		})
	}
}

func TestContractPublishNamespaceIsClosed(t *testing.T) {
	t.Parallel()

	valid := "a2a/atlas/contract-publish/op-v1-"
	if !validContractPublishNamespace(valid) {
		t.Fatalf("valid namespace %q rejected", valid)
	}
	for _, invalid := range []string{
		"atlas/contract-publish/op-v1-",
		"a2a/atlas/submit/op-v1-",
		"a2a/atlas/contract-publish/",
		"a2a/atlas/contract-publish/op-v1-*",
		"a2a/atlas/contract-publish/op-v1-\n",
	} {
		if validContractPublishNamespace(invalid) {
			t.Fatalf("invalid namespace %q accepted", invalid)
		}
	}
}
