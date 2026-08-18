package contractwiring

import (
	"bytes"
	"context"
	"fmt"
	"path"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// FreezePublicationCandidate resolves and freezes the exact candidate a
// contract publication will submit: the mirror's own provides/ tree at its
// current HEAD, optionally overlaid by a staging directory. It is the
// production logic cmd/a2a's contractP6Core.freezePublicationCandidate
// delegates to, moved here so cmd/a2a's own offline integration test can
// call the identical function instead of carrying a hand copy of its
// no-staging branch.
func FreezePublicationCandidate(ctx context.Context, ownSystem, projectRoot, mirrorDir, contractID, staging string) (space.ContractPublicationCandidateReader, contract.CandidateSource, error) {
	parsed, err := artifact.ParseID(contractID)
	if err != nil || parsed.Prefix != "XC" || parsed.System != ownSystem {
		return nil, contract.CandidateSource{}, fmt.Errorf("contract publication id %q is not owned by %s", contractID, ownSystem)
	}
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	contractRoot := path.Dir(layout.ProvidesContract(parsed.Slug))
	commit, err := space.ResolveContractPublicationCandidateCommit(ctx, mirrorDir)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	mirrorReader, err := space.OpenContractCandidateReader(mirrorDir, space.ContractCandidateLocation{Path: contractRoot, Source: space.ContractCandidateSourceMirror})
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	mirrorFrozen, freezeErr := space.NewFrozenContractPublicationCandidate(ctx, mirrorReader, space.ContractPublicationMirrorTree{
		RepoDir: mirrorDir, Commit: commit, Path: contractRoot,
	})
	closeErr := mirrorReader.Close()
	if freezeErr != nil {
		return nil, contract.CandidateSource{}, freezeErr
	}
	if closeErr != nil {
		return nil, contract.CandidateSource{}, closeErr
	}
	if staging == "" {
		return mirrorFrozen, mirrorFrozen.ContractPublicationCandidateSource(), nil
	}

	stagingReader, err := space.OpenContractCandidateReader(projectRoot, space.ContractCandidateLocation{Path: staging, Source: space.ContractCandidateSourceStaging})
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	stagingFrozen, freezeErr := space.NewFrozenContractPublicationCandidate(ctx, stagingReader, space.ContractPublicationMirrorTree{})
	closeErr = stagingReader.Close()
	if freezeErr != nil {
		return nil, contract.CandidateSource{}, freezeErr
	}
	if closeErr != nil {
		return nil, contract.CandidateSource{}, closeErr
	}
	overlay, err := space.NewContractPublicationStagingOverlayReader(mirrorFrozen, stagingFrozen, staging)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	snapshot, err := overlay.ReadContractPublicationCandidate(ctx)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	frozen := &fixedPublicationCandidate{
		snapshot: cloneContractCandidateSnapshot(snapshot),
		source:   contract.CandidateSource{Kind: contract.CandidateSourceStaging, Location: staging, Fingerprint: snapshot.Fingerprint},
	}
	return frozen, frozen.source, nil
}

type fixedPublicationCandidate struct {
	snapshot space.ContractCandidateSnapshot
	source   contract.CandidateSource
}

func (c *fixedPublicationCandidate) ReadContractPublicationCandidate(context.Context) (space.ContractCandidateSnapshot, error) {
	if c == nil || c.source.Fingerprint == "" {
		return space.ContractCandidateSnapshot{}, space.ErrContractPublicationInvalid
	}
	return cloneContractCandidateSnapshot(c.snapshot), nil
}

func (c *fixedPublicationCandidate) ContractPublicationCandidateSource() contract.CandidateSource {
	if c == nil {
		return contract.CandidateSource{}
	}
	return c.source
}

func cloneContractCandidateSnapshot(snapshot space.ContractCandidateSnapshot) space.ContractCandidateSnapshot {
	clone := snapshot
	clone.Files = make([]space.ContractSnapshotFile, len(snapshot.Files))
	for index := range snapshot.Files {
		clone.Files[index] = snapshot.Files[index]
		clone.Files[index].Raw = bytes.Clone(snapshot.Files[index].Raw)
	}
	return clone
}
