package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/daemonrpc"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/tmux"
)

// daemon.status names the tmux server the daemon's driver talks to, so a
// client can build `tmux -L <name> ...` for a remote or overridden daemon
// without recomputing the name from a HOME it does not share
// (aae-orc-lj44u). The tests decode into a struct of their own, so they check
// the wire name and not the Go field.

type tmuxSocketStatus struct {
	TmuxSocket string `json:"tmux_socket"`
}

func statusTmuxSocket(t *testing.T, d *Daemon) (string, string) {
	t.Helper()
	resp := d.dispatchAs(daemonrpc.Request{Method: "daemon.status"}, localCaller())
	if resp.Error != "" {
		t.Fatalf("daemon.status: %s", resp.Error)
	}
	var st tmuxSocketStatus
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		t.Fatalf("decode %s: %v", resp.Result, err)
	}
	return st.TmuxSocket, string(resp.Result)
}

// The reported name is the one the driver uses, whatever set it.
func TestStatusReportsTheTmuxSocketTheDriverUses(t *testing.T) {
	t.Setenv(tmux.SocketEnv, "marvel-status-test-override")
	d := statusDaemon(t)
	got, raw := statusTmuxSocket(t, d)
	if want := d.driver.Socket(); got != want {
		t.Errorf("tmux_socket = %q, want the driver's %q (result %s)", got, want, raw)
	}
	if got != "marvel-status-test-override" {
		t.Errorf("tmux_socket = %q, want the %s override", got, tmux.SocketEnv)
	}
}

// With no override the name is derived from the daemon's own layout, which a
// client sharing neither its HOME nor its environment cannot compute.
func TestStatusReportsTheDerivedTmuxSocketWithoutAnOverride(t *testing.T) {
	t.Setenv(tmux.SocketEnv, "")
	d := statusDaemon(t)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	got, raw := statusTmuxSocket(t, d)
	if want := layout.TmuxSocketName(); got != want || !strings.HasPrefix(got, "marvel-") {
		t.Errorf("tmux_socket = %q, want the layout-derived %q (result %s)", got, want, raw)
	}
}

// A daemon with no driver reports no socket: the field is left out, not empty,
// so a client cannot mistake it for a name.
func TestStatusOmitsTheTmuxSocketWhenThereIsNoDriver(t *testing.T) {
	d := statusDaemon(t)
	d.driver = nil
	got, raw := statusTmuxSocket(t, d)
	if got != "" || strings.Contains(raw, "tmux_socket") {
		t.Errorf("a daemon with no driver reported tmux_socket %q in %s", got, raw)
	}
}
