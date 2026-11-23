package daemon

import (
	"strings"
	"testing"
)

// A tracked copy of a manifest can carry <value> where a secret was redacted.
// Applied raw, the literal reaches the seat's environment and nothing says so.
// Apply refuses it, and names the role and the key. The roles are parked
// (replicas 0) so no tmux is needed.
func envManifest(roleA, roleB string) string {
	return `
workspace:
  name: envph
teams:
  - name: crew
    roles:
      - name: a
        replicas: 0
        runtime:
          command: "sleep 5"
          env:
` + roleA + `
      - name: b
        replicas: 0
        runtime:
          command: "sleep 5"
          env:
` + roleB + `
`
}

func TestApplyRefusesALiteralValuePlaceholderInRoleEnv(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyWithRoot(t, d, envManifest(
		`            API_TOKEN: "<value>"`,
		`            LOG_LEVEL: "debug"`), "")
	if resp.Error == "" {
		t.Fatal("apply of a role env holding the literal <value> was accepted, want a refusal")
	}
	for _, want := range []string{"<value>", "role[0=a]", "API_TOKEN"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("apply error = %q, want it to contain %q", resp.Error, want)
		}
	}
	if strings.Contains(resp.Error, "LOG_LEVEL") {
		t.Errorf("apply error = %q, a real value must not be reported", resp.Error)
	}
}

func TestApplyNamesEveryRoleHoldingAPlaceholder(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyWithRoot(t, d, envManifest(
		`            API_TOKEN: "<value>"`,
		`            DB_PASSWORD: "<value>"`), "")
	if resp.Error == "" {
		t.Fatal("apply of two roles whose env held the literal <value> was accepted, want a refusal naming both")
	}
	for _, want := range []string{"role[0=a]", "API_TOKEN", "role[1=b]", "DB_PASSWORD"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("apply error = %q, want it to contain %q", resp.Error, want)
		}
	}
}

func TestApplyAcceptsRoleEnvWithRealValues(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := applyWithRoot(t, d, envManifest(
		`            API_TOKEN: "abc123"`,
		`            LOG_LEVEL: "debug"`), "")
	if resp.Error != "" {
		t.Fatalf("apply of real env values: %s", resp.Error)
	}
}
