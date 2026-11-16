package bus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// A reload the broker refuses must not read as a reload that worked
// (aae-orc-mqvn5). nats-server refuses a whole reload when server_name changes
// and applies none of the rest of the conf, so a user added in the same
// rewrite is not applied. Measured on nats-server v2.14.6: the log carries
// "Failed to reload server configuration: config reload not supported for
// ServerName", the process keeps running, and the new user is refused.

// reloadOutcomes reads the ring for the two reload kinds.
func reloadOutcomes(ring *events.Ring) (reloaded, refused []events.Event) {
	return ring.Snapshot(events.Filter{Kind: events.KindBusReloaded}, 0),
		ring.Snapshot(events.Filter{Kind: events.KindBusReloadRefused}, 0)
}

// rewriteWithServerName writes the manager's current conf and auth back with a
// server_name line added, the one change nats-server refuses to reload, and
// returns once the files are on disk.
func rewriteWithServerName(t *testing.T, m *Manager, name string) {
	t.Helper()
	dir := filepath.Dir(m.ConfPath())
	conf, err := os.ReadFile(filepath.Join(dir, ConfName))
	if err != nil {
		t.Fatal(err)
	}
	auth, err := os.ReadFile(filepath.Join(dir, AuthName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFiles(dir, "server_name: "+name+"\n"+string(conf), string(auth)); err != nil {
		t.Fatal(err)
	}
}

// The measured case: server_name changes together with a team added. Reload
// returns an error that names the refusal and emits bus.reload-refused, and it
// does not emit bus.reloaded for a reload that did not happen.
func TestSupervisorReloadSurfacesAReloadTheBrokerRefuses(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(false) })

	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "example"})
	if _, err := m.Render(); err != nil { // the new team's user lands in the files; Render does not reload
		t.Fatal(err)
	}
	rewriteWithServerName(t, m, "probe-name")

	err := s.Reload()
	if err == nil {
		t.Fatal("Reload returned nil for a conf the broker refuses to reload")
	}
	if !strings.Contains(err.Error(), "ServerName") {
		t.Errorf("error %q does not carry the broker's reason", err)
	}
	reloaded, refused := reloadOutcomes(ring)
	if len(refused) != 1 {
		t.Fatalf("%d bus.reload-refused events, want 1; kinds %v", len(refused), kindsOf(ring))
	}
	if !strings.Contains(refused[0].Message, "ServerName") {
		t.Errorf("event message %q does not carry the broker's reason", refused[0].Message)
	}
	if refused[0].Severity != events.SeverityError {
		t.Errorf("severity = %v, want error", refused[0].Severity)
	}
	if len(reloaded) != 0 {
		t.Errorf("bus.reloaded emitted for a reload the broker refused: %v", reloaded)
	}
}

// The control: a reload the broker accepts still returns nil and emits
// bus.reloaded, with no refusal event.
func TestSupervisorReloadStillSucceedsForACleanReload(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(false) })

	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "example"})
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload of a clean change = %v, want nil", err)
	}
	reloaded, refused := reloadOutcomes(ring)
	if len(reloaded) != 1 {
		t.Errorf("%d bus.reloaded events, want 1; kinds %v", len(reloaded), kindsOf(ring))
	}
	if len(refused) != 0 {
		t.Errorf("bus.reload-refused emitted for a clean reload: %v", refused)
	}
}
