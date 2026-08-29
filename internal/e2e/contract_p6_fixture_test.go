package e2e

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// e2eContractP6Services is the direct-construction fixture adapter for tests
// whose subject is the CLI family composition around the P6 seams. The real
// production composition is exercised by TestHostLoopContractFamily through
// the built binary; these callbacks keep older multi-command fixtures on the
// now-required interfaces without importing cmd/a2a (package main).
type e2eContractP6Services struct {
	publishFn func(context.Context, cli.ContractPublicationRequest) (space.ContractPublicationResult, error)
	diffFn    func(context.Context, cli.ContractDiffRequest) (cli.ContractDiffResult, error)
	verifyFn  func(context.Context, cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error)
}

func (s *e2eContractP6Services) Preflight(context.Context, cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return space.ContractPublicationResult{}, fmt.Errorf("e2e: preflight is not configured for this fixture")
}

func (s *e2eContractP6Services) Publish(ctx context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	if s == nil || s.publishFn == nil {
		return space.ContractPublicationResult{}, fmt.Errorf("e2e: publish is not configured for this fixture")
	}
	return s.publishFn(ctx, request)
}

func (s *e2eContractP6Services) MaterializeContract(context.Context, cli.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return space.ContractMaterializeResult{}, fmt.Errorf("e2e: materialize is not configured for this fixture")
}

func (s *e2eContractP6Services) CheckContract(context.Context, cli.ContractCheckRequest) (contract.ConformanceResult, error) {
	return contract.ConformanceResult{}, fmt.Errorf("e2e: check is not configured for this fixture")
}

func (s *e2eContractP6Services) DiffContract(ctx context.Context, request cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
	if s == nil || s.diffFn == nil {
		return cli.ContractDiffResult{}, fmt.Errorf("e2e: diff is not configured for this fixture")
	}
	return s.diffFn(ctx, request)
}

func (s *e2eContractP6Services) VerifyContractExport(ctx context.Context, request cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
	if s == nil || s.verifyFn == nil {
		return cli.ContractVerifyExportResult{}, fmt.Errorf("e2e: verify-export is not configured for this fixture")
	}
	return s.verifyFn(ctx, request)
}

func configureE2EContractP6(command *cli.ContractCommand, services *e2eContractP6Services) {
	command.SetP6Operations(services, services, services)
	command.SetP6Inspection(services)
}

// newE2EContractPublisher retains the real WriteFunnel/FakeHost boundary used
// by the older cascade tests while adapting it to ContractPublicationOperations.
// It is fixture materialization, not a second publication planner: target
// selection is restricted to the explicit versions and major bump used by
// these tests, and P6 planning/recovery semantics remain owned by the binary
// host-loop and internal/space suites.
func newE2EContractPublisher(
	t *testing.T,
	funnel *space.WriteFunnel,
	mirrorDir, ownSystem string,
	hostConfig cli.SubmitHostConfig,
) *e2eContractP6Services {
	t.Helper()
	return &e2eContractP6Services{publishFn: func(ctx context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
		parsed, err := artifact.ParseID(request.ID)
		if err != nil || parsed.Prefix != "XC" || parsed.System != ownSystem {
			return space.ContractPublicationResult{}, fmt.Errorf("e2e: invalid owned contract id %q", request.ID)
		}
		layout, err := space.NewLayout(ownSystem)
		if err != nil {
			return space.ContractPublicationResult{}, err
		}
		descriptorPath := layout.ProvidesContract(parsed.Slug)
		descriptorRaw, err := os.ReadFile(filepath.Join(mirrorDir, filepath.FromSlash(descriptorPath)))
		if err != nil {
			return space.ContractPublicationResult{}, err
		}
		current, err := e2eContractVersion(descriptorRaw)
		if err != nil {
			return space.ContractPublicationResult{}, err
		}
		target := request.Version
		if target == "" {
			if request.Bump != "major" {
				return space.ContractPublicationResult{}, fmt.Errorf("e2e: fixture publisher supports only explicit versions or a major bump")
			}
			var major int
			if _, err := fmt.Sscanf(current, "%d.0.0", &major); err != nil {
				return space.ContractPublicationResult{}, fmt.Errorf("e2e: parse fixture version %q: %w", current, err)
			}
			target = fmt.Sprintf("%d.0.0", major+1)
		}
		finalDescriptor := []byte(strings.Replace(string(descriptorRaw), "version: \""+current+"\"", "version: \""+target+"\"", 1))
		if string(finalDescriptor) == string(descriptorRaw) && current != target {
			return space.ContractPublicationResult{}, fmt.Errorf("e2e: descriptor version replacement did not apply")
		}

		files, err := e2eContractTreeFiles(mirrorDir, filepath.ToSlash(filepath.Dir(descriptorPath)), descriptorPath, finalDescriptor)
		if err != nil {
			return space.ContractPublicationResult{}, err
		}
		// These legacy direct-construction tests seed the prospective contract
		// tree as untracked fixture input. The real funnel builds its commit
		// from origin/base through a private index and intentionally never
		// mutates those worktree paths, so consume the exact fixture files here
		// before Submit. The PR branch remains the sole durable copy and can be
		// merged cleanly by mergeBranchToMain.
		for _, file := range files {
			if err := os.Remove(filepath.Join(mirrorDir, filepath.FromSlash(file.Path))); err != nil {
				return space.ContractPublicationResult{}, fmt.Errorf("e2e: consume contract fixture %s: %w", file.Path, err)
			}
		}
		now := time.Now().UTC()
		eventID, err := artifact.MintULIDAt(now, rand.Reader)
		if err != nil {
			return space.ContractPublicationResult{}, err
		}
		eventPath := layout.EventFile(now.Format("2006"), eventID.String())
		event := fmt.Sprintf("schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: publish\nstate: published\nactor: {kind: agent, name: bot, system: %s}\nat: %s\nversion: %s\ndigest: %s\n",
			eventID.String(), request.ID, ownSystem, now.Format(time.RFC3339), target, artifact.Digest(finalDescriptor))
		files = append(files, space.FileWrite{Path: eventPath, Content: []byte(event)})

		prBody := "fixture publication through the required P6 transport seam"
		if request.Bump == "major" {
			prBody += "\n\nG2 required review"
		}
		write, err := funnel.Submit(ctx, space.SubmitRequest{
			RepoDir: mirrorDir, System: ownSystem, Verb: "contract-publish", ArtifactID: request.ID,
			ArtifactIDs: []string{request.ID}, Files: files,
			CommitMessage:    "a2a(contract): publish " + request.ID + "@" + target,
			CommitAuthorName: hostConfig.CommitAuthorName, CommitAuthorEmail: hostConfig.CommitAuthorEmail,
			RemoteURL: hostConfig.RemoteURL, Repo: hostConfig.Repo, BaseBranch: hostConfig.BaseBranch,
			PRTitle: "Publish " + request.ID + "@" + target, PRBody: prBody, Credential: hostConfig.Credential,
			MinBinaryVersion: "0.1.0",
		})
		if err != nil {
			return space.ContractPublicationResult{}, err
		}
		return space.ContractPublicationResult{
			Status: space.ContractPublicationSubmitted,
			Plan: contract.PublicationPlan{
				Contract: request.ID, CurrentVersion: current, TargetVersion: target,
				FirstPublish: current == "0.0.0", VersionSelector: e2eVersionSelector(request),
			},
			Write: write,
		}, nil
	}}
}

func e2eContractVersion(raw []byte) (string, error) {
	frontmatter, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(frontmatter.YAML), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "version:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "version:")), "\"'"), nil
		}
	}
	return "", fmt.Errorf("e2e: contract descriptor has no version")
}

func e2eContractTreeFiles(mirrorDir, contractRoot, descriptorPath string, finalDescriptor []byte) ([]space.FileWrite, error) {
	root := filepath.Join(mirrorDir, filepath.FromSlash(contractRoot))
	files := []space.FileWrite{{Path: descriptorPath, Content: finalDescriptor}}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || name == filepath.Join(mirrorDir, filepath.FromSlash(descriptorPath)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("e2e: non-regular contract fixture %s", name)
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(mirrorDir, name)
		if err != nil {
			return err
		}
		files = append(files, space.FileWrite{Path: filepath.ToSlash(rel), Content: raw})
		return nil
	})
	return files, err
}

func e2eVersionSelector(request cli.ContractPublicationRequest) string {
	if request.Version != "" {
		return "explicit:" + request.Version
	}
	return "auto:" + request.Bump
}

func newE2EContractDiffService() *e2eContractP6Services {
	return &e2eContractP6Services{diffFn: func(context.Context, cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
		return cli.ContractDiffResult{
			Added: []string{}, Removed: []string{}, Changed: []string{"schema/main.schema.json"}, FrontmatterChanged: []string{"version"},
		}, nil
	}}
}

func newE2EContractVerifyService(t *testing.T, wantDigest string) *e2eContractP6Services {
	t.Helper()
	return &e2eContractP6Services{verifyFn: func(_ context.Context, request cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
		localDigest := contractComputeDigest(t, request.Local, ".")
		matches := localDigest == wantDigest
		// Outcome, not a Matches bool: this fixture used to set the bool
		// alone and leave Outcome empty, so the verb it stands in for
		// reported "digest mismatch" over two byte-identical digests. The
		// field it set no longer exists — see cli.ContractVerifyExportResult.
		outcome := "drifted"
		if matches {
			outcome = "matched"
		}
		result := cli.ContractVerifyExportResult{
			ID: strings.Split(request.Ref, "@")[0], Outcome: outcome,
			LocalDigest: localDigest, WantDigest: wantDigest,
			Diff: cli.ContractDiffResult{Added: []string{}, Removed: []string{}, Changed: []string{}},
		}
		if !matches {
			result.Diff.Changed = []string{"schema/main.schema.json"}
		}
		return result, nil
	}}
}
