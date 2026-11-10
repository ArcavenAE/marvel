package tmux

import (
	"os"
	"strings"
	"testing"
)

// aae-orc-xs6om: a tmux new-window argument list is readable from the process
// table by every account on the host, so the driver refuses the heartbeat
// token by value. The file-path variables are fine.
func TestNewPaneAtNeverPutsASecretOnTheCommandLine(t *testing.T) {
	t.Parallel()
	const secret = "s3cr3t-value-0042"
	for _, key := range []string{"MARVEL_HEARTBEAT_TOKEN"} {
		d, log := fakeTmux(t)

		_, err := d.NewPaneAt("sess", "true", "title", "", map[string]string{key: secret}, false)

		if err == nil {
			t.Errorf("%s: NewPaneAt accepted a secret-bearing key", key)
		}
		if b, rerr := os.ReadFile(log); rerr == nil && strings.Contains(string(b), secret) {
			t.Errorf("%s: the secret reached the tmux argument list: %s", key, b)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the refusal repeats the secret value", key)
		}
	}
}

func TestNewPaneAtStillPassesTheFilePathVariables(t *testing.T) {
	t.Parallel()
	d, log := fakeTmux(t)
	envs := map[string]string{
		"MARVEL_HEARTBEAT_TOKEN_FILE": "/run/tokens/a.token",
		"DIRECTOR_NATS_PASS_FILE":     "/run/tokens/a.pass",
		"DIRECTOR_NATS_USER":          "squad",
	}

	if _, err := d.NewPaneAt("sess", "true", "title", "", envs, false); err != nil {
		t.Fatalf("NewPaneAt: %v", err)
	}

	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range envs {
		if !strings.Contains(string(b), k+"="+v) {
			t.Errorf("tmux argument list lacks %s=%s: %s", k, v, b)
		}
	}
}
