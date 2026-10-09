package daemon

import (
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// Redaction (docs/design/describe-redaction.md, RD-1). A role's declared Env
// values must never leave the daemon on a read method, whichever method built
// the response.

const canary = "canary-1-do-not-print"

const canaryManifest = `
[workspace]
name = "redactws"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sleep"
    args = ["300"]
    env = { SERVICE_TOKEN = "` + canary + `", REGION = "us-east-1" }
`

// rawResponse is what any socket caller receives for method, as bytes.
func rawResponse(t *testing.T, d *Daemon, method string, params any) string {
	t.Helper()
	var p []byte
	if params != nil {
		p = mustMarshal(t, params)
	}
	resp := d.dispatch(Request{Method: method, Params: p})
	return string(resp.Result) + resp.Error
}

// The raw bytes of get, describe and plan carry a secret-looking key and never
// its value.
// plan is the case a verb-by-verb fix misses: a scale-down to 0 puts the whole
// session, runtime included, in RolePlan.Delete.
func TestReadMethodsNeverPrintAnEnvValue(t *testing.T) {
	d := newHandlerDaemon(t)
	if resp := applyManifest(t, d, canaryManifest); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	var sess api.Session
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "redactws" {
			sess = s
		}
	}
	if sess.Name == "" {
		t.Fatal("no session was spawned")
	}
	if err := d.store.UpdateTeam("redactws/squad", func(tm *api.Team) error {
		for i := range tm.Roles {
			tm.Roles[i].Replicas = 0
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for name, raw := range map[string]string{
		"get sessions":     rawResponse(t, d, "get", map[string]string{"resource_type": "sessions"}),
		"get teams":        rawResponse(t, d, "get", map[string]string{"resource_type": "teams"}),
		"describe session": rawResponse(t, d, "describe", map[string]string{"resource_type": "session", "name": sess.Key()}),
		"describe team":    rawResponse(t, d, "describe", map[string]string{"resource_type": "team", "name": "redactws/squad"}),
		"plan":             rawResponse(t, d, "plan", nil),
	} {
		if strings.Contains(raw, canary) {
			t.Errorf("%s prints the Env value", name)
		}
	}

	// The key is still shown, with the placeholder, so an operator can see what
	// is set. describe session is the one view that carries the whole runtime.
	got := rawResponse(t, d, "describe", map[string]string{"resource_type": "session", "name": sess.Key()})
	if !strings.Contains(got, `"SERVICE_TOKEN":"(redacted)"`) {
		t.Errorf("describe session shows no secret-looking key with (redacted):\n%s", got)
	}
	// Ruling redaction-q1-env (c): only secret-looking keys are redacted, so an
	// ordinary key's value is visible. This pins (c) and not (a).
	if !strings.Contains(got, `"REGION":"us-east-1"`) {
		t.Errorf("describe session hides an ordinary key's value:\n%s", got)
	}

	// The durable record keeps the value: redaction is a view, not a rewrite.
	stored, err := d.store.GetSession(sess.Key())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Runtime.Env["SERVICE_TOKEN"] != canary {
		t.Errorf("the stored Env value changed: %q", stored.Runtime.Env["SERVICE_TOKEN"])
	}
}
