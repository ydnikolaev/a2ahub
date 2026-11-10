package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

const doctorClientLandURL = "https://github.com/acme/space"

func doctorClientLandCommand(caps *host.Capabilities) (*DoctorCommand, space.ProjectConfig) {
	fake := host.NewFakeHost()
	fake.Caps = caps
	cmd := newTestDoctorCommand()
	cmd.open = doctorOpenByURL(map[string]host.Host{doctorClientLandURL: fake})
	return cmd, space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "acme", RepoURL: doctorClientLandURL}}}
}

// TestDoctorRowsReadTrueForASpaceLandedFromTheClient: nothing on GitHub reviews
// these writes, so a row that looks for that machinery would be a false FAIL.
func TestDoctorRowsReadTrueForASpaceLandedFromTheClient(t *testing.T) {
	t.Parallel()
	caps := &host.Capabilities{NeedsCredential: true, LandsSynchronously: true}
	rows := []struct {
		name string
		run  func(*DoctorCommand, space.ProjectConfig) (bool, string)
		want string
	}{
		{"CI presence", func(c *DoctorCommand, cfg space.ProjectConfig) (bool, string) {
			return c.doctorCheckCIPresence(cfg, space.MachineConfig{})
		}, "validated at land by the a2a binary"},
		{"auto-merge", func(c *DoctorCommand, cfg space.ProjectConfig) (bool, string) {
			return c.doctorCheckAutoMerge(context.Background(), cfg, space.MachineConfig{})
		}, "lands synchronously"},
		{"stuck PR", func(c *DoctorCommand, cfg space.ProjectConfig) (bool, string) {
			return c.doctorCheckStuckGreenPRs(context.Background(), cfg, space.MachineConfig{})
		}, "nothing waits to merge"},
		{"default branch", func(c *DoctorCommand, cfg space.ProjectConfig) (bool, string) {
			return c.doctorCheckDefaultBranchHealthy(context.Background(), cfg, space.MachineConfig{})
		}, "the a2a binary validates each write it lands"},
		{"CODEOWNERS", func(c *DoctorCommand, cfg space.ProjectConfig) (bool, string) {
			return c.doctorCheckCodeownersResolvable(context.Background(), cfg, space.MachineConfig{})
		}, "owners are GitHub logins"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			cmd, cfg := doctorClientLandCommand(caps)
			ok, detail := row.run(cmd, cfg)
			if !ok {
				t.Fatalf("FAIL on a space landed from the client: %s", detail)
			}
			if !strings.Contains(detail, row.want) {
				t.Errorf("detail = %q, want %q", detail, row.want)
			}
			if strings.Contains(detail, "local:") || strings.Contains(detail, "local space") {
				t.Errorf("detail = %q describes a space on this disk", detail)
			}
		})
	}
}

// TestDoctorLocalSpaceRowsStaySilentForASpaceOnGitHub: synchronous landing used
// to stand in for "on this disk"; these three rows inspect a bare repository.
func TestDoctorLocalSpaceRowsStaySilentForASpaceOnGitHub(t *testing.T) {
	t.Parallel()
	caps := &host.Capabilities{NeedsCredential: true, LandsSynchronously: true}
	rows := []struct {
		name string
		run  func(*DoctorCommand, space.ProjectConfig) (doctorCheckStatus, string)
	}{
		{"land guard", func(c *DoctorCommand, cfg space.ProjectConfig) (doctorCheckStatus, string) {
			return c.doctorCheckLocalLandGuard(context.Background(), cfg)
		}},
		{"history shape", func(c *DoctorCommand, cfg space.ProjectConfig) (doctorCheckStatus, string) {
			return c.doctorCheckLocalHistoryShape(context.Background(), cfg)
		}},
		{"writable", func(c *DoctorCommand, cfg space.ProjectConfig) (doctorCheckStatus, string) {
			return c.doctorCheckLocalSpaceWritable(context.Background(), cfg)
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			cmd, cfg := doctorClientLandCommand(caps)
			if status, detail := row.run(cmd, cfg); status != doctorCheckPass || detail != "" {
				t.Fatalf("status = %v, detail = %q; want a silent pass", status, detail)
			}
		})
	}
}

// TestDoctorRowsUnchangedForASpaceThatKeepsActions is the regression half: with
// the reviewing host, the new wording must not appear.
func TestDoctorRowsUnchangedForASpaceThatKeepsActions(t *testing.T) {
	t.Parallel()
	cmd, cfg := doctorClientLandCommand(&host.Capabilities{NeedsCredential: true, ReviewsWrites: true})
	cmd.readFile = func(string) ([]byte, error) { return nil, context.Canceled }
	_, detail := cmd.doctorCheckCIPresence(cfg, space.MachineConfig{})
	if strings.Contains(detail, "validated at land") {
		t.Errorf("CI presence on a reviewing host reads as landed from the client: %q", detail)
	}
	_, detail = cmd.doctorCheckCodeownersResolvable(context.Background(), cfg, space.MachineConfig{})
	if strings.Contains(detail, "CODEOWNERS is not enforced") {
		t.Errorf("CODEOWNERS on a reviewing host reads as unenforced: %q", detail)
	}
}
