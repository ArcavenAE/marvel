package tmux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

// TestNewSessionRaisesHistoryLimit guards the finding-005 fix (aae-orc-22sz):
// marvel-created sessions must not inherit tmux's 2000-line history-limit
// default, which starved capture-pane scrapes to ~20% coverage. NewSession
// raises it to DefaultHistoryLimit; the created session must report that.
func TestNewSessionRaisesHistoryLimit(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-history-limit"
	t.Cleanup(func() { _ = d.KillSession(sessionName) })
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	got, err := d.ShowOption(sessionName, "history-limit")
	if err != nil {
		t.Fatalf("show-options history-limit: %v", err)
	}
	if got == "2000" {
		t.Fatal("session inherited tmux default history-limit 2000; capture-pane scrapes would lose ~80% of scrollback (finding-005)")
	}
	if want := strconv.Itoa(DefaultHistoryLimit); got != want {
		t.Fatalf("history-limit = %q, want %q", got, want)
	}
}

func TestListSessions(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-list-sessions"
	t.Cleanup(func() {
		_ = d.KillSession(sessionName)
	})
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	names, err := d.ListSessions()
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	var found bool
	for _, n := range names {
		if n == sessionName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q in %v", sessionName, names)
	}
}

func TestHasPane(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-haspane"
	t.Cleanup(func() {
		_ = d.KillSession(sessionName)
	})
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	paneID, err := d.NewPane(sessionName, "sleep 60", "haspane-test", nil)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}

	if !d.HasPane(paneID) {
		t.Fatalf("expected HasPane(%s)=true for live pane", paneID)
	}

	// Regression guard — ArcavenAE/marvel#10. Kill the pane; HasPane
	// must report false. The old implementation used display-message -p
	// which returned exit 0 even for dead panes, so ReapDead never
	// reaped and orphan sessions stayed 'running/unknown' forever.
	if err := d.KillPane(paneID); err != nil {
		t.Fatalf("kill pane: %v", err)
	}
	if d.HasPane(paneID) {
		t.Fatalf("HasPane(%s)=true after kill-pane — should be false", paneID)
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

// TestSendKeysConcurrentInjectNoInterleave drives many goroutines
// injecting distinct markers into ONE pane at once, each as a literal
// text + Enter. Before the fix, SendKeys issued text and Enter as two
// separate tmux processes with no per-pane serialization, so a concurrent
// inject could land its text between another call's text and Enter,
// merging two markers onto one line. With text+Enter in a single
// invocation and a per-pane lock, every marker must land on its own line.
//
// The pane runs cat, which echoes each completed line, so a marker shows
// up on its echoed input line and on cat's output line; both must carry
// exactly one marker.
func TestSendKeysConcurrentInjectNoInterleave(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-inject-race"
	t.Cleanup(func() { _ = d.KillSession(sessionName) })
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}
	paneID, err := d.NewPane(sessionName, "cat", "inject-race", nil)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}

	const workers = 16
	markers := make([]string, workers)
	for i := range markers {
		markers[i] = fmt.Sprintf("MK%03d", i)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.SendKeys(paneID, markers[i], true, true); err != nil {
				errCh <- fmt.Errorf("inject %s: %w", markers[i], err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent inject failed: %v", err)
	}

	// Wait until every marker has been echoed back at least once.
	var content string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if !d.HasPane(paneID) {
			t.Fatalf("pane %s vanished before capture", paneID)
		}
		c, cerr := d.CapturePaneRange(paneID, -1000, 1000)
		if cerr == nil && allPresent(c, markers) {
			content = c
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if content == "" {
		last, _ := d.CapturePaneRange(paneID, -1000, 1000)
		t.Fatalf("not all markers appeared within deadline; last capture:\n%s", last)
	}

	// No captured line may carry more than one marker — that is the
	// interleave the fix prevents.
	for _, line := range strings.Split(content, "\n") {
		if n := countMarkers(line, markers); n > 1 {
			t.Errorf("line %q carries %d markers, want at most 1 (text/Enter interleaved)", line, n)
		}
	}
}

func allPresent(content string, markers []string) bool {
	for _, m := range markers {
		if !strings.Contains(content, m) {
			return false
		}
	}
	return true
}

func countMarkers(line string, markers []string) int {
	n := 0
	for _, m := range markers {
		n += strings.Count(line, m)
	}
	return n
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

	// Poll until content is actually rendered instead of sleep-then-
	// capture: tmux send-keys returning isn't a guarantee the shell has
	// executed, and a blanket sleep hides real problems. If the pane
	// disappears mid-wait (shell exited) surface that as a fatal — the
	// test's job is to exercise capture on a live pane.
	var content string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !d.HasPane(paneID) {
			t.Fatalf("pane %s vanished before capture — investigate shell behavior", paneID)
		}
		c, err := d.CapturePaneRange(paneID, 0, 4)
		if err == nil && strings.Contains(c, "LINE_TEST") {
			content = c
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if content == "" {
		_, err := d.CapturePaneRange(paneID, 0, 4)
		t.Fatalf("capture-pane range did not see LINE_TEST within deadline: lastErr=%v", err)
	}
}

// TestDriverConcurrentUse exercises the production pattern: many
// goroutines sharing one Driver and one tmux server, each owning its
// own session and performing the full create-inject-capture-teardown
// motion. Proves the driver (and tmux itself) serializes safely under
// concurrent use — not a 'tests don't race each other' isolation fix
// but a 'marvel daemon handles N concurrent RPCs' guarantee.
//
// Each goroutine owns its own session; no goroutine touches another's
// pane IDs. Failures here indicate a real driver-level concurrency
// bug, not a test flake.
func TestDriverConcurrentUse(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	const workers = 8
	const opsPerWorker = 3

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			sessionName := fmt.Sprintf("marvel-test-concurrent-%d", w)
			defer func() { _ = d.KillSession(sessionName) }()

			if err := d.NewSession(sessionName); err != nil {
				errCh <- fmt.Errorf("worker %d new-session: %w", w, err)
				return
			}

			for op := 0; op < opsPerWorker; op++ {
				paneID, err := d.NewPane(sessionName,
					fmt.Sprintf("sleep %d", 30+op), // long-running so pane stays alive
					fmt.Sprintf("worker-%d-op-%d", w, op), nil)
				if err != nil {
					errCh <- fmt.Errorf("worker %d op %d new-pane: %w", w, op, err)
					return
				}
				if !d.HasPane(paneID) {
					errCh <- fmt.Errorf("worker %d op %d HasPane(%s)=false right after NewPane", w, op, paneID)
					return
				}
				// Inject something and immediately read it back.
				marker := fmt.Sprintf("CONCURRENT-%d-%d", w, op)
				if err := d.SendKeys(paneID, marker, true, false); err != nil {
					errCh <- fmt.Errorf("worker %d op %d send-keys: %w", w, op, err)
					return
				}
				if _, err := d.CapturePane(paneID); err != nil {
					errCh <- fmt.Errorf("worker %d op %d capture: %w", w, op, err)
					return
				}
				// Kill our own pane. Production pattern: sessions clean
				// up their own panes; we do not reach into another
				// worker's sessions.
				if err := d.KillPane(paneID); err != nil {
					errCh <- fmt.Errorf("worker %d op %d kill-pane: %w", w, op, err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	var failures []string
	for err := range errCh {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		t.Fatalf("concurrent use produced %d failures:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
}

func TestPanePID(t *testing.T) {
	skipIfNoTmux(t)
	d, err := NewDriver()
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}

	sessionName := "marvel-test-panepid"
	t.Cleanup(func() { _ = d.KillSession(sessionName) })
	if err := d.NewSession(sessionName); err != nil {
		t.Fatalf("new session: %v", err)
	}

	paneID, err := d.NewPane(sessionName, "sleep 300", "panepid-test", nil)
	if err != nil {
		t.Fatalf("new pane: %v", err)
	}

	pid, err := d.PanePID(paneID)
	if err != nil {
		t.Fatalf("pane pid: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("pane pid = %d, want a positive pid", pid)
	}

	// The same value ListPanes reports, since both read pane_pid.
	panes, err := d.ListPanes(sessionName)
	if err != nil {
		t.Fatalf("list panes: %v", err)
	}
	var listed string
	for _, p := range panes {
		if p.ID == paneID {
			listed = p.PID
		}
	}
	if listed != fmt.Sprintf("%d", pid) {
		t.Errorf("PanePID reported %d, ListPanes reported %q", pid, listed)
	}

	if err := d.KillPane(paneID); err != nil {
		t.Fatalf("kill pane: %v", err)
	}
	if _, err := d.PanePID(paneID); err == nil {
		t.Error("PanePID succeeded for a killed pane, want error")
	}
}
