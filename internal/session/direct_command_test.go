package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// tmux hands new-window's command string to the shell, so the string
// directCommand builds must give the program the args the role declared.
// The adapter path quotes each arg; the ad-hoc path joined them bare, so an
// arg with a space split in two and an apostrophe left the quote open and
// the shell refused the line (marvel docs/design/cli-read-back-and-run-parity.md).
func TestDirectCommandPassesEachArgIntact(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "show.sh")
	body := "#!/bin/sh\n{ echo \"n=$#\"; for a in \"$@\"; do echo \"[$a]\"; done; } > '" + out + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"a b", "", "it's", "$HOME", "x;y", "plain"}
	sess := &api.Session{Name: "s", Role: "r", Runtime: api.Runtime{Command: script, Args: args}}

	cmd, _ := (&Manager{}).directCommand(sess)
	if b, err := exec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("the shell rejected %q: %v: %s", cmd, err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the program never ran for %q: %v", cmd, err)
	}
	want := "n=6\n[a b]\n[]\n[it's]\n[$HOME]\n[x;y]\n[plain]\n"
	if string(got) != want {
		t.Errorf("program saw\n%s\nwant\n%s\ncommand: %q", got, want, cmd)
	}
}

// A command with no args stays the bare command string.
func TestDirectCommandWithoutArgsIsTheCommand(t *testing.T) {
	sess := &api.Session{Name: "s", Role: "r", Runtime: api.Runtime{Command: "claude"}}
	if cmd, _ := (&Manager{}).directCommand(sess); cmd != "claude" {
		t.Errorf("command = %q, want %q", cmd, "claude")
	}
}

// marvel run --env K=V records the variables on the session, and the run
// handler documents them as merged over the constructed environment. The
// ad-hoc path must hand them to the process, through the same refusals the
// role env gets for the credentials that prove identity and for the variables
// that say where the seat reaches the bus and the daemon (marvel#1450).
func TestDirectCommandCarriesTheRunEnv(t *testing.T) {
	sess := &api.Session{Name: "s", Role: "r", Runtime: api.Runtime{
		Command: "claude",
		Env: map[string]string{
			"TWIN_CWD":      "/work",
			"MARVEL_ROLE":   "chosen-role",
			"NATS_URL":      "nats://elsewhere:4222",
			"MARVEL_SOCKET": "/elsewhere.sock",
		},
	}}
	_, env := (&Manager{}).directCommand(sess)

	if got := env["TWIN_CWD"]; got != "/work" {
		t.Fatalf("TWIN_CWD = %q, want %q: --env values reach the process", got, "/work")
	}
	if got := env["MARVEL_ROLE"]; got != "chosen-role" {
		t.Errorf("MARVEL_ROLE = %q, want the --env value, as the role env overrides a constructed value", got)
	}
	if v, ok := env["NATS_URL"]; ok {
		t.Errorf("NATS_URL = %q, want it refused: marvel owns where a session reaches the bus", v)
	}
	if v, ok := env["MARVEL_SOCKET"]; ok {
		t.Errorf("MARVEL_SOCKET = %q, want it refused: marvel owns where a session reaches the daemon", v)
	}
}

// The credentials that decide who a session is never come from --env.
func TestDirectCommandRefusesCredentialKeysInTheRunEnv(t *testing.T) {
	keys := api.CredentialKeys()
	if len(keys) == 0 {
		t.Fatal("api.CredentialKeys is empty")
	}
	env := map[string]string{"TWIN_CWD": "/work"}
	for _, k := range keys {
		env[k] = "a-literal-value"
	}
	sess := &api.Session{Name: "s", Role: "r", Runtime: api.Runtime{Command: "claude", Env: env}}
	_, got := (&Manager{}).directCommand(sess)

	if got["TWIN_CWD"] != "/work" {
		t.Fatalf("TWIN_CWD = %q, want %q: the allowed key must arrive for this check to mean anything", got["TWIN_CWD"], "/work")
	}
	for _, k := range keys {
		if v, ok := got[k]; ok {
			t.Errorf("%s = %q, want it refused: it proves who the session is", k, v)
		}
	}
}
