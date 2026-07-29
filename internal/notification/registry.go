package notification

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// Registry owns machine-local project enrollment and setup preferences.
type Registry struct {
	Path    string
	Now     func() time.Time
	Entropy func() (string, error)
}

// NewRegistry constructs a registry backed by typed machine config.
func NewRegistry(path string, now func() time.Time) *Registry {
	return &Registry{
		Path: path, Now: now,
		Entropy: func() (string, error) {
			id, err := artifact.MintULIDAt(now(), rand.Reader)
			return id.String(), err
		},
	}
}

func canonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), nil
}

// Register idempotently assigns a stable machine-local ID to a project root.
func (r *Registry) Register(root string) (Project, bool, error) {
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return Project{}, false, err
	}
	root, err = canonicalRoot(root)
	if err != nil {
		return Project{}, false, err
	}
	now := r.Now().UTC()
	for i := range cfg.Notifications.Projects {
		p := &cfg.Notifications.Projects[i]
		if filepath.Clean(p.Root) != root {
			continue
		}
		p.LastSeen = now
		if err := space.SaveMachineConfig(r.Path, cfg); err != nil {
			return Project{}, false, err
		}
		return projectFromConfig(*p), false, nil
	}
	id, err := r.Entropy()
	if err != nil {
		return Project{}, false, fmt.Errorf("notification registry: mint project id: %w", err)
	}
	p := space.NotificationProject{ID: id, Root: root, LastSeen: now}
	cfg.Notifications.Projects = append(cfg.Notifications.Projects, p)
	sort.Slice(cfg.Notifications.Projects, func(i, j int) bool {
		return cfg.Notifications.Projects[i].ID < cfg.Notifications.Projects[j].ID
	})
	if err := space.SaveMachineConfig(r.Path, cfg); err != nil {
		return Project{}, false, err
	}
	return projectFromConfig(p), true, nil
}

// FindByID resolves one registered project.
func (r *Registry) FindByID(id string) (Project, error) {
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return Project{}, err
	}
	for _, p := range cfg.Notifications.Projects {
		if p.ID == id {
			return projectFromConfig(p), nil
		}
	}
	return Project{}, fmt.Errorf("%w: project %q", ErrProjectNotFound, id)
}

// FindByRoot resolves one canonical registered project root.
func (r *Registry) FindByRoot(root string) (Project, error) {
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return Project{}, err
	}
	root, err = canonicalRoot(root)
	if err != nil {
		return Project{}, err
	}
	for _, p := range cfg.Notifications.Projects {
		if filepath.Clean(p.Root) == root {
			return projectFromConfig(p), nil
		}
	}
	return Project{}, fmt.Errorf("%w: root %q", ErrProjectNotFound, root)
}

// Projects returns all registrations in stable ID order.
func (r *Registry) Projects() ([]Project, error) {
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(cfg.Notifications.Projects))
	for _, project := range cfg.Notifications.Projects {
		out = append(out, projectFromConfig(project))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RememberComponent records a successfully installed a2a-managed companion.
// The channel/profile/code-path tuple is idempotent and machine-local.
func (r *Registry) RememberComponent(channel Channel, profile, codePath, productVersion string) error {
	if !ValidChannel(channel) {
		return ErrInvalidChannel
	}
	if channel == ChannelMacOS {
		profile, codePath = "", ""
	}
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return err
	}
	for i := range cfg.Notifications.Components {
		component := &cfg.Notifications.Components[i]
		if component.Channel == string(channel) && component.Profile == profile && component.CodePath == codePath {
			component.Version = productVersion
			return space.SaveMachineConfig(r.Path, cfg)
		}
	}
	cfg.Notifications.Components = append(cfg.Notifications.Components, space.NotificationComponent{
		Channel: string(channel), Profile: profile, CodePath: codePath, Version: productVersion,
	})
	sort.Slice(cfg.Notifications.Components, func(i, j int) bool {
		left, right := cfg.Notifications.Components[i], cfg.Notifications.Components[j]
		return left.Channel+"\x00"+left.Profile+"\x00"+left.CodePath < right.Channel+"\x00"+right.Profile+"\x00"+right.CodePath
	})
	return space.SaveMachineConfig(r.Path, cfg)
}

// ForgetComponent drops only the matching ownership marker; application
// uninstall and project enrollment remain separate lifecycle operations.
func (r *Registry) ForgetComponent(channel Channel, profile, codePath string) error {
	if !ValidChannel(channel) {
		return ErrInvalidChannel
	}
	if channel == ChannelMacOS {
		profile, codePath = "", ""
	}
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return err
	}
	filtered := cfg.Notifications.Components[:0]
	for _, component := range cfg.Notifications.Components {
		if component.Channel == string(channel) && component.Profile == profile && component.CodePath == codePath {
			continue
		}
		filtered = append(filtered, component)
	}
	cfg.Notifications.Components = filtered
	return space.SaveMachineConfig(r.Path, cfg)
}

// PurgeChannels deletes the selected notification registry state after an
// explicitly confirmed --purge. Purging both channels resets the whole P49
// config while preserving unrelated machine defaults and credentials.
func (r *Registry) PurgeChannels(channels []Channel) error {
	selected := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if !ValidChannel(channel) {
			return ErrInvalidChannel
		}
		selected[string(channel)] = true
	}
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return err
	}
	if selected[string(ChannelMacOS)] && selected[string(ChannelVSCode)] {
		cfg.Notifications = space.NotificationConfig{}
		return space.SaveMachineConfig(r.Path, cfg)
	}
	for i := range cfg.Notifications.Projects {
		project := &cfg.Notifications.Projects[i]
		filtered := project.Channels[:0]
		for _, channel := range project.Channels {
			if !selected[channel] {
				filtered = append(filtered, channel)
			}
		}
		project.Channels = filtered
	}
	components := cfg.Notifications.Components[:0]
	for _, component := range cfg.Notifications.Components {
		if !selected[component.Channel] {
			components = append(components, component)
		}
	}
	cfg.Notifications.Components = components
	return space.SaveMachineConfig(r.Path, cfg)
}

// Enroll idempotently enables selected channels for a project.
func (r *Registry) Enroll(root string, channels []Channel) (Project, error) {
	if len(channels) == 0 {
		return Project{}, ErrInvalidChannel
	}
	for _, channel := range channels {
		if !ValidChannel(channel) {
			return Project{}, fmt.Errorf("%w: %q", ErrInvalidChannel, channel)
		}
	}
	project, _, err := r.Register(root)
	if err != nil {
		return Project{}, err
	}
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return Project{}, err
	}
	for i := range cfg.Notifications.Projects {
		p := &cfg.Notifications.Projects[i]
		if p.ID != project.ID {
			continue
		}
		seen := map[string]bool{}
		for _, existing := range p.Channels {
			seen[existing] = true
		}
		for _, channel := range channels {
			if !seen[string(channel)] {
				p.Channels = append(p.Channels, string(channel))
				seen[string(channel)] = true
			}
		}
		sort.Strings(p.Channels)
		p.Prompt = space.NotificationProjectPrompt{}
		p.LastSeen = r.Now().UTC()
		if err := space.SaveMachineConfig(r.Path, cfg); err != nil {
			return Project{}, err
		}
		return projectFromConfig(*p), nil
	}
	return Project{}, fmt.Errorf("%w: project %q", ErrProjectNotFound, project.ID)
}

// Preference applies one project or global setup-offer policy change.
func (r *Registry) Preference(root string, remindInDays int, never, reset, global bool) error {
	selected := 0
	if remindInDays != 0 {
		selected++
	}
	if never {
		selected++
	}
	if reset {
		selected++
	}
	if selected != 1 || remindInDays < 0 || remindInDays > 365 || (remindInDays > 0 && global) {
		return ErrInvalidPreference
	}
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return err
	}
	if global {
		switch {
		case reset:
			cfg.Notifications.PromptPolicy.State = "ask"
		case never:
			cfg.Notifications.PromptPolicy.State = "never"
		default:
			return ErrInvalidPreference
		}
		return space.SaveMachineConfig(r.Path, cfg)
	}
	project, _, err := r.Register(root)
	if err != nil {
		return err
	}
	cfg, err = loadMachineForRegistry(r.Path)
	if err != nil {
		return err
	}
	for i := range cfg.Notifications.Projects {
		p := &cfg.Notifications.Projects[i]
		if p.ID != project.ID {
			continue
		}
		switch {
		case reset:
			p.Prompt = space.NotificationProjectPrompt{State: "ask"}
		case never:
			p.Prompt = space.NotificationProjectPrompt{State: "never"}
		case remindInDays >= 1 && remindInDays <= 365:
			p.Prompt = space.NotificationProjectPrompt{
				State: "snoozed", RemindAfter: r.Now().UTC().Add(time.Duration(remindInDays) * 24 * time.Hour),
			}
		default:
			return ErrInvalidPreference
		}
		return space.SaveMachineConfig(r.Path, cfg)
	}
	return fmt.Errorf("%w: project %q", ErrProjectNotFound, project.ID)
}

// Offer computes effective prompt state using project-before-global precedence.
func (r *Registry) Offer(project Project, available []Channel) (Offer, error) {
	cfg, err := loadMachineForRegistry(r.Path)
	if err != nil {
		return Offer{}, err
	}
	if len(project.Channels) > 0 {
		return Offer{State: "enabled", Scope: "project"}, nil
	}
	var raw space.NotificationProject
	for _, p := range cfg.Notifications.Projects {
		if p.ID == project.ID {
			raw = p
			break
		}
	}
	switch {
	case raw.Prompt.State == "never":
		return Offer{State: "never", Scope: "project"}, nil
	case cfg.Notifications.PromptPolicy.State == "never":
		return Offer{State: "never", Scope: "global"}, nil
	case raw.Prompt.State == "snoozed" && raw.Prompt.RemindAfter.After(r.Now()):
		at := raw.Prompt.RemindAfter.UTC()
		return Offer{State: "snoozed", Scope: "project", RemindAfter: &at}, nil
	case len(available) == 0:
		return Offer{State: "unavailable", Scope: "project"}, nil
	default:
		return Offer{State: "ask", Scope: "project"}, nil
	}
}

func projectFromConfig(p space.NotificationProject) Project {
	channels := make([]Channel, 0, len(p.Channels))
	for _, channel := range p.Channels {
		channels = append(channels, Channel(channel))
	}
	return Project{ID: p.ID, Root: p.Root, Channels: channels, LastSeen: p.LastSeen}
}

func loadMachineForRegistry(path string) (space.MachineConfig, error) {
	cfg, err := space.LoadMachineConfig(path)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return space.MachineConfig{}, nil
	}
	return space.MachineConfig{}, err
}

var (
	// ErrProjectNotFound means the requested registration does not exist.
	ErrProjectNotFound = errors.New("notification project not found")
	// ErrInvalidChannel rejects an unknown presentation channel.
	ErrInvalidChannel = errors.New("invalid notification channel")
	// ErrInvalidPreference rejects ambiguous setup-offer transitions.
	ErrInvalidPreference = errors.New("invalid notification preference")
)
