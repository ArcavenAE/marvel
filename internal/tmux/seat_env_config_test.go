package tmux

import (
	"os"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func newPaneArgs(t *testing.T, d *Driver, log string, envs map[string]string) string {
	t.Helper()
	if _, err := d.NewPaneAt("sess", "claude --x", "title", "", envs, false); err != nil {
		t.Fatalf("NewPaneAt: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "new-window") {
			return line
		}
	}
	t.Fatalf("no new-window call in %s", b)
	return ""
}

// seat_env.path_prepend puts its directories in front of the daemon's own PATH
// in the pane, through -e, so a seat finds tools the daemon's PATH lacks.
func TestSeatEnvPathPrependReachesThePane(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	d, log := fakeTmux(t)
	d.SetSeatEnv([]string{"/home/x/.local/bin", "/opt/tools/bin"}, nil)
	line := newPaneArgs(t, d, log, nil)
	if !strings.Contains(line, "-e PATH=/home/x/.local/bin:/opt/tools/bin:/usr/bin:/bin") {
		t.Errorf("new-window lacks the prepended PATH: %s", line)
	}
}

// A role that sets PATH itself keeps it: the cluster's seat_env does not
// override a role's own environment.
func TestSeatEnvDoesNotOverrideARolesOwnPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	d, log := fakeTmux(t)
	d.SetSeatEnv([]string{"/home/x/.local/bin"}, nil)
	line := newPaneArgs(t, d, log, map[string]string{"PATH": "/role/bin"})
	if n := strings.Count(line, "-e PATH="); n != 1 || !strings.Contains(line, "-e PATH=/role/bin") {
		t.Errorf("want exactly the role's PATH, got: %s", line)
	}
}

// No seat_env, no change: no PATH argument and the prefix is the built-in one.
func TestNoSeatEnvLeavesTheNewWindowAsItWas(t *testing.T) {
	d, log := fakeTmux(t)
	line := newPaneArgs(t, d, log, nil)
	if strings.Contains(line, "-e PATH=") {
		t.Errorf("a PATH argument with no seat_env: %s", line)
	}
	if !strings.Contains(line, seatEnvPrefix+"claude --x") {
		t.Errorf("the command is not behind the built-in prefix: %s", line)
	}
}

// seat_env.unset names are removed in the pane beside the built-in list.
func TestSeatEnvUnsetJoinsTheBuiltInPrefix(t *testing.T) {
	d, log := fakeTmux(t)
	d.SetSeatEnv(nil, []string{"AWS_PROFILE", "BEADS_DOLT_PASSWORD"})
	line := newPaneArgs(t, d, log, nil)
	for _, n := range append([]string{"AWS_PROFILE", "BEADS_DOLT_PASSWORD"}, api.InheritedSessionEnv...) {
		if !strings.Contains(line, " -u "+n+" ") {
			t.Errorf("the pane command does not unset %s: %s", n, line)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "claude --x") {
		t.Errorf("the command no longer ends the line: %s", line)
	}
}
