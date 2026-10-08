package daemon

import (
	"strings"
	"testing"
)

// A command the pre-flight cannot read is applied and the apply says its
// program was not checked; one it can read and cannot find is still refused.
// The roles are parked (replicas 0) so no tmux is needed.
func runtimeManifest(command string) string {
	return `
workspace:
  name: rt
teams:
  - name: crew
    roles:
      - name: r
        replicas: 0
        runtime:
          command: "` + command + `"
`
}

func TestApplyAdvisesOnShellTextItCannotCheck(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyWithRoot(t, d, runtimeManifest("FOO=1 sleep 5"), "")
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	adv := strings.Join(advisoriesOf(t, resp), "; ")
	if !strings.Contains(adv, "role r: runtime command is shell text the pre-flight does not parse; its program was not checked") {
		t.Fatalf("advisories = %q, want the shell-text advisory for role r", adv)
	}
}

func TestApplyStillRefusesAMissingProgramInTheSimpleForm(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyWithRoot(t, d, runtimeManifest("nosuchprog --x"), "")
	if resp.Error == "" || !strings.Contains(resp.Error, "runtime pre-flight failed") {
		t.Fatalf("apply error = %q, want the runtime pre-flight refusal", resp.Error)
	}
}

func TestApplyOfASimpleCommandDrawsNoRuntimeAdvisory(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyWithRoot(t, d, runtimeManifest("sleep 5"), "")
	if resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	if adv := strings.Join(advisoriesOf(t, resp), "; "); strings.Contains(adv, "shell text") {
		t.Fatalf("advisories = %q, a simple command must not draw the shell-text one", adv)
	}
}
