package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
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
	if teams := d.store.ListTeams(); len(teams) != 0 {
		t.Errorf("a refused apply stored %d teams, want none", len(teams))
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
	for _, want := range []string{"<value>", "role[0=a]", "API_TOKEN", "role[1=b]", "DB_PASSWORD"} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("apply error = %q, want it to contain %q", resp.Error, want)
		}
	}
	if teams := d.store.ListTeams(); len(teams) != 0 {
		t.Errorf("a refused apply stored %d teams, want none", len(teams))
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

// A path that still bypassed the apply guard: `marvel run` takes an env map
// straight from the request and starts a session without a manifest. It refuses
// the literal, names the key, and leaves no row.
func TestHandleRunRefusesAnEnvThatHoldsThePlaceholder(t *testing.T) {
	d := newHandlerDaemon(t)
	resp := d.handleRun(mustMarshal(t, daemonrpc.RunParams{
		Workspace: "envrun", Team: "crew", Role: "a", Name: "run-ph",
		RuntimeCommand: "sleep", RuntimeArgs: []string{"300"},
		Env: map[string]string{"API_TOKEN": api.EnvPlaceholder, "LOG_LEVEL": "debug"},
	}))
	if resp.Error == "" {
		t.Fatal("run with an env holding the literal <value> was accepted, want a refusal")
	}
	for _, want := range []string{"API_TOKEN", api.EnvPlaceholder} {
		if !strings.Contains(resp.Error, want) {
			t.Errorf("run error = %q, want it to contain %q", resp.Error, want)
		}
	}
	if strings.Contains(resp.Error, "LOG_LEVEL") {
		t.Errorf("run error = %q, a real value must not be reported", resp.Error)
	}
	if rows := d.store.ListSessionsByTeam("envrun", "crew"); len(rows) != 0 {
		t.Errorf("a refused run left %d session rows", len(rows))
	}
}
