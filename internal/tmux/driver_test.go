package tmux

import (
	"os/exec"
	"testing"
)

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
}

func TestNewDriver(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	if d.binary == "" {
		t.Fatal("expected binary path")
	}
}

func TestSessionLifecycle(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-lifecycle"
	t.Cleanup(func() {
		_ = d.KillSession(sessionName)
	})

	// Create session
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if !d.HasSession(sessionName) {
		t.Fatal("session should exist after creation")
	}

	// Idempotent create
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("idempotent new session: %v", err)
	}

	// Create a pane
	paneID, err := d.NewPane(sessionName, "sleep 300", "test-agent", map[string]string{
		"MARVEL_SESSION": "test-agent",
		"MARVEL_SOCKET":  "/tmp/test.sock",
	})
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}
	if paneID == "" {
		t.Fatal("expected pane ID")
	}

	// List panes
	panes, err := d.ListPanes(sessionName)
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	if len(panes) < 2 {
		t.Fatalf("expected at least 2 panes (initial + created), got %d", len(panes))
	}

	// Find our pane
	found := false
	for _, p := range panes {
		if p.ID == paneID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created pane %s not found in list", paneID)
	}

	// Kill pane
	if err := d.KillPane(paneID); err != nil {
		t.Fatalf("kill pane: %v", err)
	}

	// Kill session
	if err := d.KillSession(sessionName); err != nil {
		t.Fatalf("kill session: %v", err)
	}
	if d.HasSession(sessionName) {
		t.Fatal("session should not exist after kill")
	}
}

func TestSendKeys(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-sendkeys"
	t.Cleanup(func() {
		_ = d.KillSession(sessionName)
	})

	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	// Create a pane running cat (will echo what we send)
	paneID, err := d.NewPane(sessionName, "cat", "sendkeys-test", nil)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}

	// Send literal text without enter — should not error
	if err := d.SendKeys(paneID, "hello marvel", true, false); err != nil {
		t.Fatalf("send-keys literal: %v", err)
	}

	// Send with enter
	if err := d.SendKeys(paneID, "world", true, true); err != nil {
		t.Fatalf("send-keys with enter: %v", err)
	}

	// Send non-literal (interpreted) — e.g., special key name
	if err := d.SendKeys(paneID, "Enter", false, false); err != nil {
		t.Fatalf("send-keys interpreted: %v", err)
	}

	// Send to non-existent pane should fail
	if err := d.SendKeys("%99999", "test", true, false); err == nil {
		t.Fatal("expected error sending to non-existent pane")
	}
}

func TestCapturePane(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-capture"
	t.Cleanup(func() {
		_ = d.KillSession(sessionName)
	})

	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	// Create a pane with a shell that stays alive, then echo into it
	paneID, err := d.NewPane(sessionName, "sh", "capture-test", nil)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}

	// Send a command that produces known output
	if err := d.SendKeys(paneID, "echo 'MARVEL_CAPTURE_TEST'", true, true); err != nil {
		t.Fatalf("send echo command: %v", err)
	}

	// Give the command time to execute and render
	_ = exec.Command("sleep", "0.3").Run()

	// Capture pane content
	content, err := d.CapturePane(paneID)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}

	if content == "" {
		t.Fatal("expected non-empty pane content")
	}

	// Capture non-existent pane should fail
	if _, err := d.CapturePane("%99999"); err == nil {
		t.Fatal("expected error capturing non-existent pane")
	}
}

func TestCapturePaneRange(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-capture-range"
	t.Cleanup(func() {
		_ = d.KillSession(sessionName)
	})

	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	paneID, err := d.NewPane(sessionName, "sh", "range-test", nil)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}

	if err := d.SendKeys(paneID, "echo 'LINE_TEST'", true, true); err != nil {
		t.Fatalf("send echo: %v", err)
	}

	_ = exec.Command("sleep", "0.3").Run()

	// Capture first 5 lines of visible area
	content, err := d.CapturePaneRange(paneID, 0, 4)
	if err != nil {
		t.Fatalf("capture-pane range: %v", err)
	}

	if content == "" {
		t.Fatal("expected non-empty range content")
	}
}
