package bus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/requiretool"
)

// A render puts server_name into the conf only where the broker that reads it
// will not refuse (aae-orc-a7h5h). nats-server declines a whole reload when
// server_name changes, so a name added to the conf of a broker that is running
// without one would also hold back every user rendered with it (measured,
// aae-orc-mqvn5). The name goes in when no broker is running, which is the
// next start's conf, or when the running broker already holds it. A broker
// whose name cannot be read, or that may be running unseen, gets none.

type namedBroker struct {
	name    string
	running bool
	err     error
}

func (f namedBroker) ServerName() (string, bool, error) { return f.name, f.running, f.err }

// renderedConf renders m once and returns the conf text.
func renderedConf(t *testing.T, m *Manager) string {
	t.Helper()
	if _, err := m.Render(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(m.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func confHasName(conf, name string) bool {
	for _, l := range strings.Split(conf, "\n") {
		if strings.TrimSpace(l) == "server_name: "+name {
			return true
		}
	}
	return false
}

func confHasAnyName(conf string) bool {
	for _, l := range strings.Split(conf, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "server_name") {
			return true
		}
	}
	return false
}

// scratchManager is a managed-bus manager on a started-marked scratch port.
func scratchManager(t *testing.T) *Manager {
	t.Helper()
	listen := "127.0.0.1:" + strconv.Itoa(freePort(t))
	rb := config.ResolvedBus{Managed: true, Listen: listen, URL: "nats://" + listen, StoreDir: filepath.Join(t.TempDir(), "js")}
	m, err := NewManager(filepath.Join(t.TempDir(), "nats"), "c1", rb, &teams{{Name: "ops", Workspace: "ws1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRenderOmitsServerNameWhileARunningBrokerHasNone(t *testing.T) {
	t.Parallel()
	m := scratchManager(t)
	m.Broker = namedBroker{running: true, name: ""}
	if conf := renderedConf(t, m); confHasAnyName(conf) {
		t.Errorf("a running broker with no name was handed a server_name:\n%s", conf)
	}
}

func TestRenderOmitsServerNameWhenTheRunningNameCannotBeRead(t *testing.T) {
	t.Parallel()
	m := scratchManager(t)
	m.Broker = namedBroker{running: true, err: errors.New("monitor unreachable")}
	if conf := renderedConf(t, m); confHasAnyName(conf) {
		t.Errorf("an unreadable answer still rendered a server_name:\n%s", conf)
	}
}

func TestRenderKeepsServerNameWhenTheRunningBrokerHoldsIt(t *testing.T) {
	t.Parallel()
	m := scratchManager(t)
	m.Broker = namedBroker{running: true, name: "c1"}
	if conf := renderedConf(t, m); !confHasName(conf, "c1") {
		t.Errorf("a running broker that holds the name lost it from the conf:\n%s", conf)
	}
}

// A running broker with a different name is not told to rename itself either.
func TestRenderOmitsServerNameWhenTheRunningBrokerHoldsAnother(t *testing.T) {
	t.Parallel()
	m := scratchManager(t)
	m.Broker = namedBroker{running: true, name: "NB4XYZ"}
	if conf := renderedConf(t, m); confHasAnyName(conf) {
		t.Errorf("a running broker with another name was handed a server_name:\n%s", conf)
	}
}

// Control: with no broker running, or no supervision at all, the next start
// reads the conf, so it carries the name.
func TestRenderEmitsServerNameWhenNoBrokerIsRunning(t *testing.T) {
	t.Parallel()
	for name, b := range map[string]BrokerNamer{
		"no supervision":    nil,
		"nothing running":   namedBroker{running: false},
		"a stopped broker":  namedBroker{running: false, name: "c1"},
		"stopped, bad read": namedBroker{running: false, err: errors.New("ignored")},
	} {
		m := scratchManager(t)
		m.Broker = b
		if conf := renderedConf(t, m); !confHasName(conf, "c1") {
			t.Errorf("%s: no server_name in the conf:\n%s", name, conf)
		}
	}
}

// With no broker of ours running but a pidfile left behind, a broker may be
// running that this daemon cannot prove, so "cannot tell" renders no name.
func TestRenderOmitsServerNameBesideAStalePidfile(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	m.Broker = s
	writePidfile(t, s, reapedPid(t))
	if conf := renderedConf(t, m); confHasAnyName(conf) {
		t.Errorf("a stale pidfile with nothing running still rendered a server_name:\n%s", conf)
	}
}

// varzName reads the broker's own name from its loopback monitor.
func varzName(t *testing.T, listen string) string {
	t.Helper()
	mon, err := MonitorAddr(listen)
	if err != nil {
		t.Fatal(err)
	}
	c := http.Client{Timeout: 2 * time.Second}
	r, err := c.Get("http://" + mon + "/varz")
	if err != nil {
		t.Fatalf("varz: %v", err)
	}
	defer func() { _ = r.Body.Close() }()
	var v struct {
		ServerName string `json:"server_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v.ServerName
}

// stripServerName writes the manager's conf back without its server_name line,
// as a conf rendered before this change reads.
func stripServerName(t *testing.T, m *Manager) {
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
	var out []string
	for _, l := range strings.Split(string(conf), "\n") {
		if !strings.HasPrefix(l, "server_name") {
			out = append(out, l)
		}
	}
	if _, err := WriteFiles(dir, strings.Join(out, "\n"), string(auth)); err != nil {
		t.Fatal(err)
	}
}

// A broker started over a conf with no name takes the name at its start: the
// start renders for the process it is about to launch.
func TestStartRendersTheServerNameForTheBrokerItStarts(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	m.Broker = s
	stripServerName(t, m)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(false) })
	if got := varzName(t, m.bus.Listen); got != m.domain {
		t.Errorf("broker name after a start over a name-less conf = %q, want the cluster %q", got, m.domain)
	}
}

// A restart is the step that turns the name on for a broker that was running
// without it.
func TestRestartAppliesTheServerNameToABrokerThatHadNone(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	m.Broker = s
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(false) })
	stripServerName(t, m)
	if err := s.Restart("test"); err != nil {
		t.Fatal(err)
	}
	if got := varzName(t, m.bus.Listen); got != m.domain {
		t.Errorf("broker name after a restart = %q, want the cluster %q", got, m.domain)
	}
}

// sighupReloader reloads a plain broker the way the supervisor does and reports
// a refusal from the broker's log, so this test needs no supervisor.
type sighupReloader struct {
	pid int
	log string
}

func (r sighupReloader) Reload() error {
	before, _ := os.ReadFile(r.log)
	if err := syscall.Kill(r.pid, syscall.SIGHUP); err != nil {
		return err
	}
	for i := 0; i < 80; i++ {
		time.Sleep(25 * time.Millisecond)
		now, _ := os.ReadFile(r.log)
		tail := string(now[len(before):])
		if strings.Contains(tail, "Failed to reload server configuration") {
			return errors.New(strings.TrimSpace(tail[strings.Index(tail, "Failed to reload"):]))
		}
		if strings.Contains(tail, "Reloaded server configuration") {
			return nil
		}
	}
	return errors.New("no answer from the broker")
}

// The measured hazard: a broker running with no name, a team added, and the
// conf must reload cleanly so the new user is in force.
func TestACredentialRenderNeverRidesAReloadTheBrokerRefuses(t *testing.T) {
	bin := requiretool.Path(t, "nats-server")
	m := scratchManager(t)
	m.Broker = namedBroker{running: true, name: ""}
	if _, err := m.Regenerate(); err != nil {
		t.Fatal(err)
	}
	stripServerName(t, m) // the conf the old broker was started from
	logf := filepath.Join(t.TempDir(), "nats.log")
	cmd := exec.Command(bin, "-c", m.ConfPath(), "-l", logf)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for i := 0; i < 80 && varzNameQuiet(m.bus.Listen) == "ERR"; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	m.Reloader = sighupReloader{pid: cmd.Process.Pid, log: logf}

	*m.teams.(*teams) = append(*m.teams.(*teams), api.Team{Name: "fleet", Workspace: "ws1"})
	if _, err := m.Regenerate(); err != nil {
		t.Fatalf("Regenerate with a team added = %v, want the reload to land", err)
	}
	user, pw, ok := m.Credential("fleet", "x")
	if !ok {
		t.Fatal("no credential for the added team")
	}
	nc, err := nats.Connect("nats://"+m.bus.Listen, nats.UserInfo(user, pw), nats.MaxReconnects(0), nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("the added team's user is not in force: %v", err)
	}
	nc.Close()
}

// varzNameQuiet is varzName without a test: "ERR" while the monitor is down.
func varzNameQuiet(listen string) string {
	mon, err := MonitorAddr(listen)
	if err != nil {
		return "ERR"
	}
	c := http.Client{Timeout: time.Second}
	r, err := c.Get("http://" + mon + "/varz")
	if err != nil {
		return "ERR"
	}
	_ = r.Body.Close()
	return "up"
}

// A Supervisor built for a Manager is the Manager's BrokerNamer, with no step
// for a caller to remember: without it a render goes back to naming the server
// whatever the running broker holds, which is the refusal the omission exists
// to avoid (aae-orc-a7h5h). The test never sets Manager.Broker itself.
func TestNewSupervisorIsTheManagersBrokerNamer(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	writePidfile(t, s, reapedPid(t))
	if conf := renderedConf(t, m); confHasAnyName(conf) {
		t.Errorf("a manager whose supervisor was built still rendered a server_name beside a stale pidfile:\n%s", conf)
	}
}
