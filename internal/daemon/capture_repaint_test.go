package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const winchSeat = `trap 'n=$((n+1)); echo painted-$n' WINCH
n=0
echo ready
while :; do sleep 0.1; done
`

func winchManifest(script string) string {
	return `
[workspace]
name = "repaintws"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 1

    [team.role.runtime]
    command = "sh"
    args = ["` + script + `"]
`
}

func captureOf(t *testing.T, d *Daemon, key string, repaint bool) (content, status, target string) {
	t.Helper()
	p := map[string]any{"session_key": key}
	if repaint {
		p["repaint"] = true
	}
	resp := d.handleCapture(mustMarshal(t, p))
	if resp.Error != "" {
		t.Fatalf("capture: %s", resp.Error)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out["content"], out["repaint"], out["repaint_target"]
}

func waitCaptureHas(t *testing.T, d *Daemon, key, want string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if c, _, _ := captureOf(t, d, key, false); strings.Contains(c, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never showed %q", want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// capture --repaint reads what the program redraws on a resize signal; a plain
// capture of the same pane does not signal it, and reports nothing about repaint.
func TestCaptureRepaintShowsWhatAPlainCaptureDoesNot(t *testing.T) {
	d := newHandlerDaemon(t)
	script := filepath.Join(t.TempDir(), "winch.sh")
	if err := os.WriteFile(script, []byte(winchSeat), 0o700); err != nil {
		t.Fatal(err)
	}
	if resp := applyManifest(t, d, winchManifest(script)); resp.Error != "" {
		t.Fatalf("apply: %s", resp.Error)
	}
	key := ""
	for _, s := range d.store.ListSessions() {
		if s.Workspace == "repaintws" {
			key = s.Key()
		}
	}
	if key == "" {
		t.Fatal("no session")
	}
	waitCaptureHas(t, d, key, "ready")

	plain, status, target := captureOf(t, d, key, false)
	if strings.Contains(plain, "painted-") || status != "" || target != "" {
		t.Fatalf("a plain capture signalled the pane or reported a repaint: %q status=%q target=%q", plain, status, target)
	}
	got, status, target := captureOf(t, d, key, true)
	if !strings.Contains(got, "painted-1") {
		t.Errorf("repaint capture did not show the redraw:\n%s", got)
	}
	if status != "signalled" || target != "sh" {
		t.Errorf("repaint = %q target = %q, want signalled and sh", status, target)
	}
}

func TestRepaintStatusNamesTheOutcome(t *testing.T) {
	t.Parallel()
	if got := repaintStatus(nil); got != "signalled" {
		t.Errorf("repaintStatus(nil) = %q, want signalled", got)
	}
	got := repaintStatus(errors.New("no foreground process group"))
	if got != "unavailable: no foreground process group" {
		t.Errorf("repaintStatus(err) = %q, want unavailable: <reason>", got)
	}
}
