package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/internal/workcheckpoint"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

type workWallClock struct{}

func (workWallClock) Now() time.Time { return time.Now() }

type workManifestEngine struct{ engine *validate.Engine }

func (v workManifestEngine) ValidateManifest(_ context.Context, raw []byte) error {
	result, err := v.engine.ValidateManifest(raw)
	if err != nil {
		return err
	}
	if result.Valid {
		return nil
	}
	return fmt.Errorf("manifest validation rejected: %+v", result.Violations)
}

type workTemplateRenderer struct{}

func (workTemplateRenderer) RenderWorkAnnouncement(input space.WorkAnnouncementRenderInput) ([]byte, error) {
	return template.Render(template.Input{
		Type: input.Type, EnvelopeSchema: input.EnvelopeSchema, ID: input.ID,
		Actor:   template.Actor{Kind: input.Actor.Kind, Name: input.Actor.Name, Model: input.Actor.Model},
		Created: input.Created, Fields: input.Fields, Body: input.Body,
	})
}

type workCore struct {
	coordinator *workreport.Coordinator
	store       *space.WorkLeaseStore
	projectID   string
	spaceID     string
	system      string
}

func buildWorkCore(p paths, cfg space.ProjectConfig, machine space.MachineConfig, ref space.Ref) (workCore, error) {
	projectID, err := space.CanonicalProjectID(p.projectRoot)
	if err != nil {
		return workCore{}, err
	}
	owner, name, err := parseGitHubRepo(ref.RepoURL)
	if err != nil {
		return workCore{}, err
	}
	engine, err := newEngine()
	if err != nil {
		return workCore{}, err
	}
	mirrorDir := space.ResolveMirrorLocation(p.projectRoot, ref, machine)
	runtime, err := space.NewWorkRuntime(
		projectID, ref.ID, cfg.System, mirrorDir, ref.RepoURL,
		host.Repo{Owner: owner, Name: name},
		func(callCtx context.Context) (host.Credential, error) {
			return resolveCredential(callCtx, ref.ID, machine)
		},
		workManifestEngine{engine: engine},
	)
	if err != nil {
		return workCore{}, err
	}
	cacheRoot := cacheDirOf(p)
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return workCore{}, fmt.Errorf("work: create cache root: %w", err)
	}
	store, err := space.NewWorkLeaseStore(cacheRoot)
	if err != nil {
		return workCore{}, err
	}
	continuations, err := space.NewWorkLeaseContinuationResolver(store, projectID, ref.ID, mirrorDir)
	if err != nil {
		return workCore{}, err
	}
	facts, err := space.NewWorkCheckpointRepositoryFactsWithContinuations(mirrorDir, cfg.System, continuations)
	if err != nil {
		return workCore{}, err
	}
	genericValidator, err := workcheckpoint.NewGenericSubmit(func(context.Context) (space.SubmitValidator, error) {
		manifest, loadErr := loadManifest(mirrorDir)
		if loadErr != nil {
			return nil, loadErr
		}
		resolver := cli.NewMirrorResolver(mirrorDir, manifest)
		return cli.NewSubmitValidatorAdapter(engine, cfg.System, resolver, cli.NewLegalityAdapter(mirrorDir, cfg.System, manifest)), nil
	})
	if err != nil {
		return workCore{}, err
	}
	checkpointValidator, err := workcheckpoint.New(engine, facts, genericValidator, cfg.System)
	if err != nil {
		return workCore{}, err
	}
	submitValidator, err := space.NewWorkSubmissionValidator(checkpointValidator, projectID, ref.ID)
	if err != nil {
		return workCore{}, err
	}
	funnel := space.NewWriteFunnel(host.NewGitHubHost(http.DefaultClient, githubAPIBase()), submitValidator, version)
	publisher, err := space.NewWorkPublisher(funnel, runtime)
	if err != nil {
		return workCore{}, err
	}
	historyPublisher, err := space.NewWorkHistoryPublisher(mirrorDir, publisher)
	if err != nil {
		return workCore{}, err
	}
	clock := workWallClock{}
	service, err := workreport.NewService(store, historyPublisher, clock)
	if err != nil {
		return workCore{}, err
	}
	gate, err := space.NewWorkAuthoringGate(runtime)
	if err != nil {
		return workCore{}, err
	}
	preparer, err := space.NewWorkPreparer(rand.Reader, runtime, workTemplateRenderer{}, checkpointValidator, funnel)
	if err != nil {
		return workCore{}, err
	}
	ids, err := space.NewWorkIDSource(rand.Reader)
	if err != nil {
		return workCore{}, err
	}
	keys, err := space.NewWorkLeaseKeySource(p.projectRoot)
	if err != nil {
		return workCore{}, err
	}
	coordinator, err := workreport.NewCoordinator(service, service, store, gate, preparer, ids, keys, clock)
	if err != nil {
		return workCore{}, err
	}
	return workCore{coordinator: coordinator, store: store, projectID: projectID, spaceID: ref.ID, system: cfg.System}, nil
}

func buildWorkCommand(p paths, cfg space.ProjectConfig, machine space.MachineConfig, ref space.Ref) (*cli.WorkCommand, error) {
	core, err := buildWorkCore(p, cfg, machine, ref)
	if err != nil {
		return nil, err
	}
	return cli.NewWorkCommand(cli.WorkCommandDeps{
		Starter: core.coordinator, Progressor: core.coordinator, Local: core.coordinator, Reader: core.store,
		ResolveActor: func(flags cli.WorkActorFlags) (workreport.Actor, error) {
			actor, actorErr := actorResolver()(cli.ActorFlags{Kind: flags.Kind, Name: flags.Name, Model: flags.Model})
			if actorErr != nil {
				return workreport.Actor{}, actorErr
			}
			return workreport.Actor{Kind: actor.Kind, Name: actor.Name, Model: actor.Model, System: core.system, Session: flags.Session}, nil
		},
		ProjectID: core.projectID, Space: core.spaceID,
	})
}

func buildMCPWorkDeps(p paths, cfg space.ProjectConfig, machine space.MachineConfig) (mcp.WorkToolDeps, error) {
	bySpace := make(map[string]mcp.WorkToolDeps, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		core, err := buildWorkCore(p, cfg, machine, ref)
		if err != nil {
			return mcp.WorkToolDeps{}, err
		}
		deps := mcp.WorkToolDeps{
			Starter: core.coordinator, Progressor: core.coordinator, Local: core.coordinator, Reader: core.store,
			ResolveActor: func(input mcp.WorkActorInput) (workreport.Actor, error) {
				actor, actorErr := actorResolver()(cli.ActorFlags{Kind: input.Kind, Name: input.Name, Model: input.Model})
				if actorErr != nil {
					return workreport.Actor{}, actorErr
				}
				return workreport.Actor{Kind: actor.Kind, Name: actor.Name, Model: actor.Model, System: core.system, Session: input.Session}, nil
			},
			ProjectID: core.projectID, Space: core.spaceID,
		}
		bySpace[ref.ID] = deps
	}
	if len(bySpace) == 1 {
		for _, deps := range bySpace {
			return deps, nil
		}
	}
	projectID, err := space.CanonicalProjectID(p.projectRoot)
	if err != nil {
		return mcp.WorkToolDeps{}, err
	}
	return mcp.WorkToolDeps{ProjectID: projectID, ResolveSpace: func(id string) (mcp.WorkToolDeps, error) {
		if id == "" {
			return mcp.WorkToolDeps{}, fmt.Errorf("a2a_work: space is required when multiple spaces are connected")
		}
		deps, ok := bySpace[id]
		if !ok {
			return mcp.WorkToolDeps{}, fmt.Errorf("a2a_work: space %q is not connected", id)
		}
		return deps, nil
	}}, nil
}

func configuredWorkSpace(cfg space.ProjectConfig, requested string) (space.Ref, error) {
	if requested != "" {
		if ref, ok := findSpace(cfg, requested); ok {
			return ref, nil
		}
		return space.Ref{}, fmt.Errorf("work: space %q is not connected", requested)
	}
	if len(cfg.Spaces) == 1 {
		return cfg.Spaces[0], nil
	}
	if len(cfg.Spaces) == 0 {
		return space.Ref{}, fmt.Errorf("work: no connected space")
	}
	return space.Ref{}, fmt.Errorf("work: multiple spaces are connected; pass --space <id>")
}

// extractWorkSpace removes the config-routing flag before the thin CLI
// command sees its domain flags. The selected space is then represented by
// injected dependencies, not by a filesystem/root argument in the command.
func extractWorkSpace(args []string) (string, []string, error) {
	remaining := make([]string, 0, len(args))
	selected := ""
	for len(args) != 0 {
		arg := args[0]
		args = args[1:]
		value := ""
		switch {
		case strings.HasPrefix(arg, "--space="):
			value = strings.TrimPrefix(arg, "--space=")
		case strings.HasPrefix(arg, "-space="):
			value = strings.TrimPrefix(arg, "-space=")
		case arg == "--space" || arg == "-space":
			if len(args) == 0 {
				return "", nil, fmt.Errorf("work: --space requires an id")
			}
			value = args[0]
			args = args[1:]
		default:
			remaining = append(remaining, arg)
			continue
		}
		if strings.TrimSpace(value) == "" || (selected != "" && selected != value) {
			return "", nil, fmt.Errorf("work: --space must name exactly one connected space")
		}
		selected = value
	}
	return selected, remaining, nil
}

var _ space.ManifestValidator = workManifestEngine{}
var _ space.WorkAnnouncementRenderer = workTemplateRenderer{}
