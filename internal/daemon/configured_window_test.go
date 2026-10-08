package daemon

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// homeWithConfig points the client config at a temp home holding body, or at
// an empty home when body is empty.
func homeWithConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if body == "" {
		return
	}
	dir := filepath.Join(home, ".marvel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The window is the config's watchdog.window, and zero (the default) when the
// config has none or has no file at all.
func TestConfiguredQuietWindowReadsTheConfig(t *testing.T) {
	homeWithConfig(t, "watchdog:\n  window: 3m\n")
	if got := configuredQuietWindow(); got != 3*time.Minute {
		t.Errorf("window = %v, want 3m", got)
	}
	homeWithConfig(t, "")
	if got := configuredQuietWindow(); got != 0 {
		t.Errorf("no config file: window = %v, want 0", got)
	}
	homeWithConfig(t, "watchdog: {}\n")
	if got := configuredQuietWindow(); got != 0 {
		t.Errorf("no watchdog.window: window = %v, want 0", got)
	}
}

// A window that is not a positive duration is dropped to the default and said
// so once in the log, for both readers alike.
func TestConfiguredQuietWindowRefusesABadValueAndSaysSo(t *testing.T) {
	homeWithConfig(t, "watchdog:\n  window: soon\n")
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if got := configuredQuietWindow(); got != 0 {
		t.Errorf("window = %v for a bad value, want 0", got)
	}
	if !strings.Contains(buf.String(), "watchdog.window") {
		t.Errorf("a bad window should be logged, got %q", buf.String())
	}
}

// New hands the controller the same window, so ACTIVE% and the watchdog cannot
// disagree about what quiet means.
func TestNewGivesTheControllerTheConfiguredWindow(t *testing.T) {
	homeWithConfig(t, "watchdog:\n  window: 4m\n")
	d := newHandlerDaemon(t)
	if got := d.teamCtrl.ClusterQuietWindow(); got != 4*time.Minute {
		t.Errorf("controller window = %v, want 4m", got)
	}
}
