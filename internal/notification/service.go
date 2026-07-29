package notification

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

// ComponentProbe reports basic platform availability and installation state.
type ComponentProbe interface {
	Available(context.Context) ([]Channel, error)
	Installed(context.Context) ([]Channel, error)
}

// DetailedComponentProbe reports bounded platform-specific health.
type DetailedComponentProbe interface {
	Health(context.Context) ([]ComponentHealth, error)
}

// Service composes registry, projection, delivery, routes, and component health.
type Service struct {
	Registry *Registry
	Delivery *DeliveryStore
	Sources  SourceFactory
	Probe    ComponentProbe
}

// Status returns a non-consuming project snapshot and component health.
func (s *Service) Status(ctx context.Context, root string, probe bool) (Status, error) {
	var (
		available []Channel
		installed []Channel
		health    []ComponentHealth
		err       error
	)
	if detailed, ok := s.Probe.(DetailedComponentProbe); probe && ok {
		health, err = detailed.Health(ctx)
		if err != nil {
			return Status{}, err
		}
		for _, component := range health {
			if component.Available {
				available = appendUniqueChannel(available, component.Channel)
			}
			if component.Installed {
				installed = appendUniqueChannel(installed, component.Channel)
			}
		}
		sort.Slice(available, func(i, j int) bool { return available[i] < available[j] })
		sort.Slice(installed, func(i, j int) bool { return installed[i] < installed[j] })
	} else {
		available, installed, err = s.components(ctx)
		if err != nil {
			return Status{}, err
		}
		health = componentHealth(available, installed)
	}
	registered := true
	project, err := s.Registry.FindByRoot(root)
	if err != nil {
		if !errors.Is(err, ErrProjectNotFound) {
			return Status{}, err
		}
		registered = false
		canonical, canonicalErr := canonicalRoot(root)
		if canonicalErr != nil {
			return Status{}, canonicalErr
		}
		project = Project{Root: canonical}
	}
	source, err := s.Sources(project)
	if err != nil {
		return Status{}, err
	}
	projection, err := BuildProjection(ctx, project, source, s.Registry.Now())
	if err != nil {
		return Status{}, err
	}
	var statusProject *Project
	if registered {
		statusProject = &project
		if err := saveRoutes(ctx, s.routesPath(), projection.Routes); err != nil {
			return Status{}, err
		}
	} else {
		// A passive status probe must not register a repository or expose a
		// route that cannot later resolve through the trusted registry.
		projection.Level.RouteToken = ""
		projection.Level.Quiet = true
	}
	offer, err := s.Registry.Offer(project, available)
	if err != nil {
		return Status{}, err
	}
	return Status{
		AvailableChannels: available, InstalledChannels: installed,
		Project: statusProject, Level: projection.Level, Offer: offer,
		Components: health,
	}, nil
}

func appendUniqueChannel(channels []Channel, channel Channel) []Channel {
	if hasChannel(channels, channel) {
		return channels
	}
	return append(channels, channel)
}

func componentHealth(available, installed []Channel) []ComponentHealth {
	seen := map[Channel]bool{}
	var out []ComponentHealth
	for _, channel := range append(append([]Channel(nil), available...), installed...) {
		if seen[channel] {
			continue
		}
		seen[channel] = true
		out = append(out, ComponentHealth{
			Channel: channel, Available: hasChannel(available, channel),
			Installed: hasChannel(installed, channel), Protocol: SchemaVersion,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
}

// Claim projects and leases transition entries for one enrolled consumer.
func (s *Service) Claim(ctx context.Context, channel Channel, consumer, projectID string, limit int) (ClaimResult, error) {
	project, err := s.Registry.FindByID(projectID)
	if err != nil {
		return ClaimResult{}, err
	}
	if !hasChannel(project.Channels, channel) {
		return ClaimResult{}, fmt.Errorf("%w: project %s channel %s", ErrChannelNotEnrolled, project.ID, channel)
	}
	source, err := s.Sources(project)
	if err != nil {
		return ClaimResult{}, err
	}
	projection, err := BuildProjection(ctx, project, source, s.Registry.Now())
	if err != nil {
		return ClaimResult{}, err
	}
	if err := saveRoutes(ctx, s.routesPath(), projection.Routes); err != nil {
		return ClaimResult{}, err
	}
	return s.Delivery.Claim(ctx, channel, consumer, projectID, projection.Entries, limit)
}

// Ack advances only the delivery lease identified by token.
func (s *Service) Ack(ctx context.Context, token string, entries []string) (AckResult, error) {
	return s.Delivery.Ack(ctx, token, entries)
}

// ResolveRoute resolves a ledger-backed token and its registered project.
func (s *Service) ResolveRoute(token string) (Route, Project, error) {
	route, err := resolveRoute(s.routesPath(), token)
	if err != nil {
		return Route{}, Project{}, err
	}
	project, err := s.Registry.FindByID(route.ProjectID)
	return route, project, err
}

func (s *Service) components(ctx context.Context) ([]Channel, []Channel, error) {
	if s.Probe == nil {
		return nil, nil, nil
	}
	available, err := s.Probe.Available(ctx)
	if err != nil {
		return nil, nil, err
	}
	installed, err := s.Probe.Installed(ctx)
	if err != nil {
		return nil, nil, err
	}
	return available, installed, nil
}

func (s *Service) routesPath() string {
	return filepath.Join(s.Delivery.Root, "routes.json")
}

func hasChannel(channels []Channel, want Channel) bool {
	for _, channel := range channels {
		if channel == want {
			return true
		}
	}
	return false
}

// ErrChannelNotEnrolled rejects claims for a project-disabled channel.
var ErrChannelNotEnrolled = fmt.Errorf("notification channel not enrolled")
