package bus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
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

	// Every check below reports on its own, so each shows red separately.
	err := s.Reload()
	if err == nil {
		t.Error("Reload returned nil for a conf the broker refuses to reload")
	} else if !strings.Contains(err.Error(), "ServerName") {
		t.Errorf("error %q does not carry the broker's reason", err)
	}
	reloaded, refused := reloadOutcomes(ring)
	if len(refused) != 1 {
		t.Errorf("%d bus.reload-refused events, want 1; kinds %v", len(refused), kindsOf(ring))
	}
	for _, ev := range refused {
		if !strings.Contains(ev.Message, "ServerName") {
			t.Errorf("event message %q does not carry the broker's reason", ev.Message)
		}
		if ev.Severity != events.SeverityError {
			t.Errorf("severity = %v, want error", ev.Severity)
		}
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

// A refusal does not poison the next reload: the broker refuses one, then
// accepts the next, and the second is read from its own answer and not from the
// first one's line, and returns at once (the broker's answer is in its log
// within milliseconds, so the wait runs out only when the answer is missed).
func TestSupervisorReloadAfterARefusalReadsOnlyItsOwnAnswer(t *testing.T) {
	s, m, ring := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(false) })

	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "example"})
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	rewriteWithServerName(t, m, "probe-name")
	if err := s.Reload(); err == nil {
		t.Fatal("setup: the first reload was not refused")
	}

	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "crew", Workspace: "example"})
	if _, err := m.Render(); err != nil { // the conf without the name: the broker can take it
		t.Fatal(err)
	}
	start := time.Now()
	err := s.Reload()
	elapsed := time.Since(start)
	if err != nil {
		t.Errorf("the second reload = %v, want nil: the broker accepted it", err)
	}
	if elapsed > reloadVerdictWait/3 {
		t.Errorf("the second reload took %v, want well under the %v wait: it did not see its own answer", elapsed, reloadVerdictWait)
	}
	reloaded, refused := reloadOutcomes(ring)
	if len(reloaded) != 1 {
		t.Errorf("%d bus.reloaded events, want 1 for the second reload; kinds %v", len(reloaded), kindsOf(ring))
	}
	if len(refused) != 1 {
		t.Errorf("%d bus.reload-refused events, want 1 (the first reload only); kinds %v", len(refused), kindsOf(ring))
	}
}

// A refusal reaches a caller as ErrReloadRefused, so no caller matches its text.
func TestSupervisorReloadRefusalIsTheSentinel(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(false) })

	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "example"})
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	rewriteWithServerName(t, m, "probe-name")
	if err := s.Reload(); !errors.Is(err, ErrReloadRefused) {
		t.Errorf("Reload error %v does not match ErrReloadRefused", err)
	}
}

type sentinelReloader struct{}

func (sentinelReloader) Reload() error {
	return fmt.Errorf("bus: nats-server pid 1: %w", ErrReloadRefused)
}

// Regenerate wraps what Reload returns with %w, so the sentinel survives it.
func TestRegenerateKeepsTheReloadRefusalMatchable(t *testing.T) {
	t.Parallel()
	m := scratchBusManager(t)
	m.Reloader = sentinelReloader{}
	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "example"})
	changed, err := m.Regenerate()
	if !changed {
		t.Fatalf("setup: the render did not change (err %v)", err)
	}
	if !errors.Is(err, ErrReloadRefused) {
		t.Errorf("Regenerate error %v does not match ErrReloadRefused", err)
	}
}

// scratchBusManager is a managed-bus manager on a started-marked scratch port.
func scratchBusManager(t *testing.T) *Manager {
	t.Helper()
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: filepath.Join(t.TempDir(), "js")}
	m, err := NewManager(filepath.Join(t.TempDir(), "nats"), "c1", rb, &teams{{Name: "ops", Workspace: "ws1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
