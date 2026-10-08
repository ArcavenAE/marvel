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
