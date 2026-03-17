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
