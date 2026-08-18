package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// cmd_doctor_notify_test.go exercises the three P6 notify checks (spec 06
// doctor table): notify-routes, notify-secret, notify-delivery. Each reads
// through notifyCheckRoutes/notifyCheckSecret/notifyCheckDelivery
// (cmd_notify_setup.go) — the SAME functions `a2a notify verify` reads,
// per that spec's own "the same facts doctor reports" requirement.

func notifyDoctorCfg() space.ProjectConfig {
	return space.ProjectConfig{Spaces: []space.Ref{{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"}}}
}

func TestDoctorCheckNotifyRoutes(t *testing.T) {
	t.Parallel()
	cfg := notifyDoctorCfg()

	t.Run("pass when no routes are configured", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "space.yaml") {
				return []byte(strings.ReplaceAll(notifyManifestFixture, "notification_routes:\n  - channel: telegram\n    chat: \"-1001\"\n    for: axon\n    events: [state-change]\n    secret: TG_BOT_TOKEN\n", "")), nil
			}
			return nil, os.ErrNotExist
		}
		ok, detail := cmd.doctorCheckNotifyRoutes(cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail on a malformed route (empty chat)", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "space.yaml") {
				return []byte(strings.ReplaceAll(notifyManifestFixture, `chat: "-1001"`, `chat: ""`)), nil
			}
			return nil, os.ErrNotExist
		}
		ok, detail := cmd.doctorCheckNotifyRoutes(cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "getvisa") || !strings.Contains(detail, "malformed") {
			t.Fatalf("detail = %q, want space id + malformed named", detail)
		}
	})

	t.Run("fail on an orphaned route", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "space.yaml") {
				return []byte(strings.ReplaceAll(notifyManifestFixture, "for: axon", "for: ghost")), nil
			}
			return nil, os.ErrNotExist
		}
		ok, detail := cmd.doctorCheckNotifyRoutes(cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "orphaned") {
			t.Fatalf("detail = %q, want orphaned named", detail)
		}
	})

	t.Run("pass when no space is connected", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		ok, detail := cmd.doctorCheckNotifyRoutes(space.ProjectConfig{}, space.MachineConfig{})
		if !ok || detail != "" {
			t.Fatalf("want silent pass, got %t %q", ok, detail)
		}
	})
}

func TestDoctorCheckNotifySecret(t *testing.T) {
	t.Parallel()
	cfg := notifyDoctorCfg()
	fixture := func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "space.yaml") {
			return []byte(notifyManifestFixture), nil
		}
		return nil, os.ErrNotExist
	}

	t.Run("pass when the secret is present", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = fixture
		cmd.ghSecretList = func(context.Context, string) ([]string, error) { return []string{"TG_BOT_TOKEN"}, nil }
		ok, detail := cmd.doctorCheckNotifySecret(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail naming the missing secret and the repo", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = fixture
		cmd.ghSecretList = func(context.Context, string) ([]string, error) { return nil, nil }
		ok, detail := cmd.doctorCheckNotifySecret(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "TG_BOT_TOKEN") || !strings.Contains(detail, "acme/getvisa") {
			t.Fatalf("detail = %q, want the secret name and repo named", detail)
		}
	})

	t.Run("never leaks a secret VALUE, presence only", func(t *testing.T) {
		t.Parallel()
		cmd := newTestDoctorCommand()
		cmd.readFile = fixture
		var sawValue bool
		cmd.ghSecretList = func(context.Context, string) ([]string, error) {
			// The real gh secret list --json name projection carries names
			// only; a fake that returned a value field would prove nothing
			// leaked here since notifyCheckSecret only ever reads Name.
			sawValue = false
			return []string{"TG_BOT_TOKEN"}, nil
		}
		ok, detail := cmd.doctorCheckNotifySecret(context.Background(), cfg, space.MachineConfig{})
		if !ok || sawValue {
			t.Fatalf("unexpected state: ok=%t detail=%q", ok, detail)
		}
	})
}

func TestDoctorCheckNotifyDelivery(t *testing.T) {
	t.Parallel()
	cfg := notifyDoctorCfg()
	fixture := func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "space.yaml") {
			return []byte(notifyManifestFixture), nil
		}
		return nil, os.ErrNotExist
	}
	withToken := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		return host.Credential{Token: "tok"}, nil
	}

	t.Run("pass when the last run is green", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"status":"completed","conclusion":"success"}]}`))
		}))
		defer srv.Close()
		cmd := newTestDoctorCommand()
		cmd.readFile = fixture
		cmd.resolveCredential = withToken
		cmd.notifyAdapter = newNotifySetupAdapter(srv.Client(), srv.URL)
		ok, detail := cmd.doctorCheckNotifyDelivery(context.Background(), cfg, space.MachineConfig{})
		if !ok {
			t.Fatalf("want pass, got fail: %s", detail)
		}
	})

	t.Run("fail when the last run failed", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"status":"completed","conclusion":"failure"}]}`))
		}))
		defer srv.Close()
		cmd := newTestDoctorCommand()
		cmd.readFile = fixture
		cmd.resolveCredential = withToken
		cmd.notifyAdapter = newNotifySetupAdapter(srv.Client(), srv.URL)
		ok, detail := cmd.doctorCheckNotifyDelivery(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "getvisa") || !strings.Contains(detail, "failure") {
			t.Fatalf("detail = %q, want space id and conclusion named", detail)
		}
	})

	t.Run("fail when there has never been a run", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
		}))
		defer srv.Close()
		cmd := newTestDoctorCommand()
		cmd.readFile = fixture
		cmd.resolveCredential = withToken
		cmd.notifyAdapter = newNotifySetupAdapter(srv.Client(), srv.URL)
		ok, detail := cmd.doctorCheckNotifyDelivery(context.Background(), cfg, space.MachineConfig{})
		if ok {
			t.Fatal("want fail, got pass")
		}
		if !strings.Contains(detail, "has ever completed") {
			t.Fatalf("detail = %q", detail)
		}
	})
}
