package bus

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestBrokerEnvironmentExcludesOperatorSecrets: the daemon's environment
// carries whatever the operator's shell exported (the bd client password,
// a heartbeat token). The broker is spawned through workload.Start with the
// message-bus allowlist, so none of it reaches the broker; the minted seed
// still does (services-list.md section 3.4, test 3; aae-orc-oo62t).
func TestBrokerEnvironmentExcludesOperatorSecrets(t *testing.T) {
	t.Setenv("BEADS_DOLT_PASSWORD", "operator-secret")
	t.Setenv("MARVEL_HEARTBEAT_TOKEN", "operator-token")
	s, _, _ := newTestSupervisor(t, "")
	s.Env = func() []string { return []string{"MARVEL_TEST_MINTED=1"} }
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ps", "-E", "-o", "command=", "-p", strconv.Itoa(s.Status().PID)).Output()
	if err != nil || !strings.Contains(string(out), "=") {
		t.Skipf("cannot read the broker's environment from the kernel here (%v)", err)
	}
	env := string(out)
	if !strings.Contains(env, "MARVEL_TEST_MINTED=1") {
		t.Errorf("broker environment lacks the minted variable:\n%s", env)
	}
	for _, k := range []string{"BEADS_DOLT_PASSWORD", "MARVEL_HEARTBEAT_TOKEN"} {
		if strings.Contains(env, k+"=") {
			t.Errorf("broker environment carries %s:\n%s", k, env)
		}
	}
}
