package notification

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// StatusRequest selects one project and optional channel health view.
type StatusRequest struct {
	Root    string
	Channel Channel
	Probe   bool
}

// InstallRequest enrolls a project and installs selected components.
type InstallRequest struct {
	Root     string
	Channels []Channel
	All      bool
	Profile  string
	CodePath string
}

// TestRequest asks selected adapters for a synthetic readiness proof.
type TestRequest struct {
	Root     string
	Channels []Channel
	All      bool
}

// UninstallRequest removes selected managed components.
type UninstallRequest struct {
	Root     string
	Channels []Channel
	All      bool
	Profile  string
	CodePath string
	Purge    bool
	Yes      bool
}

// PreferenceRequest changes project or global setup-offer policy.
type PreferenceRequest struct {
	Root         string
	RemindInDays int
	Never        bool
	Reset        bool
	Global       bool
}

// ClaimRequest is the adapter-only transition delivery request.
type ClaimRequest struct {
	Channel   Channel
	Consumer  string
	ProjectID string
	Limit     int
}

// AckRequest acknowledges accepted entries under one lease.
type AckRequest struct {
	LeaseToken string
	EntryIDs   []string
}

// OpenRequest carries a core-minted trusted route token.
type OpenRequest struct {
	RouteToken string
}

// ChannelResult reports one component lifecycle outcome.
type ChannelResult struct {
	Channel Channel `json:"channel"`
	Status  string  `json:"status"`
	Reason  string  `json:"reason,omitempty"`
}

// OperationResult aggregates deterministic per-channel lifecycle outcomes.
type OperationResult struct {
	Project *Project        `json:"project"`
	Results []ChannelResult `json:"results"`
	Failed  bool            `json:"failed"`
}

// PreferenceResult returns the effective setup-offer state.
type PreferenceResult struct {
	Project *Project `json:"project"`
	Offer   Offer    `json:"offer"`
}

// OpenResult reports the project-local destination opened for a route.
type OpenResult struct {
	ProjectID  string `json:"project_id"`
	Root       string `json:"root"`
	SpaceID    string `json:"space_id,omitempty"`
	ArtifactID string `json:"artifact_id,omitempty"`
	Kind       string `json:"kind"`
}

// ComponentOptions identifies an exact VS Code installation/profile.
type ComponentOptions struct {
	Profile  string
	CodePath string
}

// ComponentManager owns platform application state. It never owns project
// semantics or delivery cursors.
type ComponentManager interface {
	ComponentProbe
	Install(context.Context, Channel, ComponentOptions) (ChannelResult, error)
	Test(context.Context, Channel, Project) (ChannelResult, error)
	Uninstall(context.Context, Channel, ComponentOptions, bool) (ChannelResult, error)
}

// ProjectConfigurator refreshes adapter-owned project configuration.
type ProjectConfigurator interface {
	Configure(context.Context, []Project) error
}

// RouteOpener opens a previously resolved trusted local route.
type RouteOpener func(context.Context, Route, Project) error

// Controller is the application service behind the thin CLI family.
type Controller struct {
	Service    *Service
	Components ComponentManager
	OpenRoute  RouteOpener
}

// Status returns project signals and bounded platform component health.
func (c *Controller) Status(ctx context.Context, request StatusRequest) (Status, error) {
	status, err := c.Service.Status(ctx, request.Root, request.Probe)
	if err != nil {
		return Status{}, err
	}
	if request.Channel != "" {
		if !ValidChannel(request.Channel) {
			return Status{}, ErrInvalidChannel
		}
		status.AvailableChannels = filterChannels(status.AvailableChannels, request.Channel)
		status.InstalledChannels = filterChannels(status.InstalledChannels, request.Channel)
		filtered := status.Components[:0]
		for _, component := range status.Components {
			if component.Channel == request.Channel {
				filtered = append(filtered, component)
			}
		}
		status.Components = filtered
	}
	return status, nil
}

// Install prepares adapter config, repairs components, then enrolls only
// channels whose component mutation succeeded. A failed install therefore
// cannot leave registry truth claiming that a channel is enabled.
func (c *Controller) Install(ctx context.Context, request InstallRequest) (OperationResult, error) {
	if c.Components == nil {
		return OperationResult{}, ErrComponentsUnavailable
	}
	channels, err := c.resolveChannels(ctx, request.Channels, request.All)
	if err != nil {
		return OperationResult{}, err
	}
	project, _, err := c.Service.Registry.Register(request.Root)
	if err != nil {
		return OperationResult{}, err
	}
	result := OperationResult{Project: &project, Results: []ChannelResult{}}
	if configurator, ok := c.Components.(ProjectConfigurator); ok {
		projects, projectsErr := c.Service.Registry.Projects()
		if projectsErr != nil {
			return OperationResult{}, projectsErr
		}
		for i := range projects {
			if projects[i].ID != project.ID {
				continue
			}
			for _, channel := range channels {
				projects[i].Channels = appendUniqueChannel(projects[i].Channels, channel)
			}
			sort.Slice(projects[i].Channels, func(a, b int) bool {
				return projects[i].Channels[a] < projects[i].Channels[b]
			})
			break
		}
		if configureErr := configurator.Configure(ctx, projects); configureErr != nil {
			return OperationResult{}, configureErr
		}
	}
	successful := make([]Channel, 0, len(channels))
	for _, channel := range channels {
		channelResult, installErr := c.Components.Install(ctx, channel, ComponentOptions{
			Profile: request.Profile, CodePath: request.CodePath,
		})
		if installErr != nil {
			channelResult.Channel = channel
			channelResult.Status = "failed"
			if channelResult.Reason == "" {
				channelResult.Reason = installErr.Error()
			} else {
				channelResult.Reason += ": " + installErr.Error()
			}
		}
		result.Results = append(result.Results, channelResult)
		if channelResult.Status == "failed" {
			result.Failed = true
		} else {
			successful = append(successful, channel)
		}
	}
	if len(successful) > 0 {
		project, err = c.Service.Registry.Enroll(request.Root, successful)
		if err != nil {
			return OperationResult{}, err
		}
		result.Project = &project
	}
	if configurator, ok := c.Components.(ProjectConfigurator); ok {
		projects, projectsErr := c.Service.Registry.Projects()
		if projectsErr != nil {
			return OperationResult{}, projectsErr
		}
		if configureErr := configurator.Configure(ctx, projects); configureErr != nil {
			return OperationResult{}, configureErr
		}
	}
	sortChannelResults(result.Results)
	return result, nil
}

// Test sends a synthetic readiness request without advancing delivery state.
func (c *Controller) Test(ctx context.Context, request TestRequest) (OperationResult, error) {
	if c.Components == nil {
		return OperationResult{}, ErrComponentsUnavailable
	}
	project, err := c.Service.Registry.FindByRoot(request.Root)
	if err != nil {
		return OperationResult{}, err
	}
	channels, err := c.resolveChannels(ctx, request.Channels, request.All)
	if err != nil {
		return OperationResult{}, err
	}
	result := OperationResult{Project: &project, Results: []ChannelResult{}}
	for _, channel := range channels {
		item, testErr := c.Components.Test(ctx, channel, project)
		if testErr != nil {
			item.Channel = channel
			item.Status = "failed"
			if item.Reason == "" {
				item.Reason = testErr.Error()
			} else {
				item.Reason += ": " + testErr.Error()
			}
		}
		result.Results = append(result.Results, item)
		if item.Status == "failed" {
			result.Failed = true
		}
	}
	sortChannelResults(result.Results)
	return result, nil
}

// Uninstall removes components and optionally purges selected machine state.
func (c *Controller) Uninstall(ctx context.Context, request UninstallRequest) (OperationResult, error) {
	if c.Components == nil {
		return OperationResult{}, ErrComponentsUnavailable
	}
	channels, err := c.resolveChannels(ctx, request.Channels, request.All)
	if err != nil {
		return OperationResult{}, err
	}
	if !request.Yes {
		current, currentErr := canonicalRoot(request.Root)
		if currentErr != nil {
			return OperationResult{}, currentErr
		}
		projects, projectsErr := c.Service.Registry.Projects()
		if projectsErr != nil {
			return OperationResult{}, projectsErr
		}
		var affected []string
		for _, project := range projects {
			if filepath.Clean(project.Root) == current {
				continue
			}
			for _, channel := range channels {
				if hasChannel(project.Channels, channel) {
					affected = append(affected, project.Root)
					break
				}
			}
		}
		if len(affected) > 0 {
			sort.Strings(affected)
			return OperationResult{}, fmt.Errorf(
				"%w: %d other enrolled project(s) use the selected channel(s): %s; rerun with --yes",
				ErrUninstallAffectsProjects, len(affected), strings.Join(affected, ", "),
			)
		}
	}
	result := OperationResult{Results: []ChannelResult{}}
	for _, channel := range channels {
		item, uninstallErr := c.Components.Uninstall(ctx, channel, ComponentOptions{
			Profile: request.Profile, CodePath: request.CodePath,
		}, request.Purge)
		if uninstallErr != nil {
			item.Channel = channel
			item.Status = "failed"
			if item.Reason == "" {
				item.Reason = uninstallErr.Error()
			} else {
				item.Reason += ": " + uninstallErr.Error()
			}
		}
		result.Results = append(result.Results, item)
		if item.Status == "failed" {
			result.Failed = true
		}
	}
	if request.Purge && !result.Failed {
		if err := c.Service.Delivery.PurgeChannels(channels); err != nil {
			return result, fmt.Errorf("purge notification delivery state: %w", err)
		}
		if err := c.Service.Registry.PurgeChannels(channels); err != nil {
			return result, fmt.Errorf("purge notification registry: %w", err)
		}
		for i := range result.Results {
			if result.Results[i].Reason != "" {
				result.Results[i].Reason += "; "
			}
			result.Results[i].Reason += fmt.Sprintf(
				"purged machine registry %s and delivery state %s",
				c.Service.Registry.Path,
				filepath.Join(c.Service.Delivery.Root, "delivery", string(result.Results[i].Channel)),
			)
		}
	}
	sortChannelResults(result.Results)
	return result, nil
}

// Preference applies one validated setup-offer transition.
func (c *Controller) Preference(ctx context.Context, request PreferenceRequest) (PreferenceResult, error) {
	if err := c.Service.Registry.Preference(request.Root, request.RemindInDays, request.Never, request.Reset, request.Global); err != nil {
		return PreferenceResult{}, err
	}
	if request.Global {
		state := "ask"
		if request.Never {
			state = "never"
		}
		return PreferenceResult{Offer: Offer{State: state, Scope: "global"}}, nil
	}
	project, err := c.Service.Registry.FindByRoot(request.Root)
	if err != nil {
		return PreferenceResult{}, err
	}
	available, _, err := c.Service.components(ctx)
	if err != nil {
		return PreferenceResult{}, err
	}
	offer, err := c.Service.Registry.Offer(project, available)
	if err != nil {
		return PreferenceResult{}, err
	}
	return PreferenceResult{Project: &project, Offer: offer}, nil
}

// Claim leases one adapter consumer's bounded transition batch.
func (c *Controller) Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error) {
	return c.Service.Claim(ctx, request.Channel, request.Consumer, request.ProjectID, request.Limit)
}

// Ack accepts only entries covered by a live or completed lease.
func (c *Controller) Ack(ctx context.Context, request AckRequest) (AckResult, error) {
	if request.LeaseToken == "" || len(request.EntryIDs) == 0 {
		return AckResult{}, ErrInvalidAck
	}
	return c.Service.Ack(ctx, request.LeaseToken, request.EntryIDs)
}

// Open resolves a core-minted route and invokes the local HTML opener.
func (c *Controller) Open(ctx context.Context, request OpenRequest) (OpenResult, error) {
	route, project, err := c.Service.ResolveRoute(request.RouteToken)
	if err != nil {
		return OpenResult{}, err
	}
	if c.OpenRoute == nil {
		return OpenResult{}, errors.New("notification route opener is not configured")
	}
	if err := c.OpenRoute(ctx, route, project); err != nil {
		return OpenResult{}, err
	}
	return OpenResult{
		ProjectID: project.ID, Root: project.Root, SpaceID: route.SpaceID,
		ArtifactID: route.ArtifactID, Kind: route.Kind,
	}, nil
}

func (c *Controller) resolveChannels(ctx context.Context, requested []Channel, all bool) ([]Channel, error) {
	if all {
		if len(requested) > 0 {
			return nil, ErrInvalidChannel
		}
		available, err := c.Components.Available(ctx)
		if err != nil {
			return nil, err
		}
		if len(available) == 0 {
			return nil, ErrComponentsUnavailable
		}
		sort.Slice(available, func(i, j int) bool { return available[i] < available[j] })
		return available, nil
	}
	if len(requested) == 0 {
		return nil, ErrInvalidChannel
	}
	seen := map[Channel]bool{}
	out := make([]Channel, 0, len(requested))
	for _, channel := range requested {
		if !ValidChannel(channel) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidChannel, channel)
		}
		if !seen[channel] {
			out = append(out, channel)
			seen[channel] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func filterChannels(channels []Channel, want Channel) []Channel {
	if hasChannel(channels, want) {
		return []Channel{want}
	}
	return []Channel{}
}

func sortChannelResults(results []ChannelResult) {
	sort.Slice(results, func(i, j int) bool { return results[i].Channel < results[j].Channel })
}

var (
	// ErrComponentsUnavailable means no platform component manager was wired.
	ErrComponentsUnavailable = errors.New("notification components unavailable")
	// ErrInvalidAck rejects incomplete acknowledgement requests.
	ErrInvalidAck = errors.New("notification ack requires lease and entries")
	// ErrUninstallAffectsProjects requires confirmation for global impact.
	ErrUninstallAffectsProjects = errors.New("notification uninstall affects other projects")
)
