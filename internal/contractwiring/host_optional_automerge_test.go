package contractwiring

import (
	"context"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// hostWithoutAutoMerge embeds the host.Host INTERFACE (not a concrete fake), so
// only Host's own methods are promoted and EnableAutoMerge is genuinely absent.
type hostWithoutAutoMerge struct {
	host.Host
}

func (hostWithoutAutoMerge) ListContractPublishHeads(context.Context, string, string, string, host.Credential) (host.RemoteHeadListing, error) {
	return host.RemoteHeadListing{}, nil
}

func (hostWithoutAutoMerge) ReadRemoteRecoveryCommit(context.Context, string, string, host.Repo, string, host.Credential) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error) {
	return host.Repo{}, "", "", "", "", "", "", nil, false, nil
}

func (hostWithoutAutoMerge) ReadRemoteRecoveryTreeFiles(context.Context, string, string, []string) (map[string]host.RemoteTreeFile, error) {
	return nil, nil
}

func (hostWithoutAutoMerge) ListRemoteRecoveryTreeFiles(context.Context, string, string, []string) (map[string]host.RemoteTreeFile, error) {
	return nil, nil
}

// A host.Host plus the four recovery readers satisfies Host without
// EnableAutoMerge: auto-merge is an optional capability asserted at its own
// call sites, not a requirement of this package's assembly.
var _ Host = hostWithoutAutoMerge{}

func TestHost_DoesNotRequireAutoMerger(t *testing.T) {
	t.Parallel()
	if _, ok := any(hostWithoutAutoMerge{}).(host.AutoMerger); ok {
		t.Fatal("fixture must lack EnableAutoMerge for the assertion above to mean anything")
	}
}
