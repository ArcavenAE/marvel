// Package tmux provides a shell-out driver for tmux operations.
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Driver manages tmux sessions and panes by shelling out to the tmux binary.
type Driver struct {
	binary string

	// socket is the tmux server socket name. Empty means the default
	// tmux server. When non-empty every tmux invocation prepends
	// -L <socket>, scoping the driver to a dedicated server.
	//
	// Set via the MARVEL_TMUX_SOCKET env var at NewDriver time. Tests
	// use this to get per-package isolation from the system-wide tmux.
	socket string
}

// NewDriver creates a tmux driver, verifying tmux is available.
//
// If MARVEL_TMUX_SOCKET is set, the driver talks to a dedicated tmux
// server at that socket name (tmux -L <socket>). Useful for test
// isolation and for running marvel alongside an unrelated tmux
// workflow on the same machine.
func NewDriver() (*Driver, error) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux not found: %w", err)
	}
	return &Driver{binary: path, socket: os.Getenv("MARVEL_TMUX_SOCKET")}, nil
}

// cmd builds an exec.Cmd for tmux with the driver's socket prefix
// applied. All Driver methods go through this helper so the socket
// scoping is enforced in exactly one place.
func (d *Driver) cmd(args ...string) *exec.Cmd {
	if d.socket != "" {
		full := make([]string, 0, len(args)+2)
		full = append(full, "-L", d.socket)
		full = append(full, args...)
		return exec.Command(d.binary, full...)
	}
	return exec.Command(d.binary, args...)
}

// Socket returns the tmux socket name the driver is scoped to, or
// empty string for the default tmux server. Used by test teardown to
// kill the right server.
func (d *Driver) Socket() string { return d.socket }

// HasSession checks if a tmux session exists.
func (d *Driver) HasSession(name string) bool {
	return d.cmd("has-session", "-t", name).Run() == nil
}

// ListSessions returns the names of every tmux session on the server.
// If no tmux server is running, returns an empty slice and no error —
// that's the same "no sessions" condition as a freshly started daemon.
func (d *Driver) ListSessions() ([]string, error) {
	out, err := d.cmd("list-sessions", "-F", "#S").Output()
	if err != nil {
		// Treat "there is no live tmux server" as zero sessions, not
		// an error. tmux reports this two different ways depending on
		// whether a server was ever started at this socket:
		//   - "no server running on <path>" (server existed, then exited)
		//   - "error connecting to <path> (No such file or directory)"
		//     (server never started — socket file absent)
		// Both mean the same thing to us.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			stderr := string(ee.Stderr)
			if strings.Contains(stderr, "no server running") ||
				strings.Contains(stderr, "No such file or directory") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("list-sessions: %w", err)
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// NewSession creates a new tmux session in detached mode.
func (d *Driver) NewSession(name string) error {
	if d.HasSession(name) {
		return nil
	}
	if out, err := d.cmd("new-session", "-d", "-s", name).CombinedOutput(); err != nil {
		return fmt.Errorf("new-session %s: %s: %w", name, string(out), err)
	}
	return nil
}

// PaneInfo holds information about a tmux pane.
type PaneInfo struct {
	ID      string
	PID     string
	Command string
	Title   string
}

// NewPane creates a new window in the given session running the specified command.
// Each agent gets its own window to avoid tmux "no space for new pane" errors.
// envs sets environment variables. Returns the pane ID (e.g., "%5").
func (d *Driver) NewPane(session, command, title string, envs map[string]string) (string, error) {
	args := []string{
		"new-window", "-t", session,
		"-d",
		"-P", "-F", "#{pane_id}",
	}
	if title != "" {
		args = append(args, "-n", title)
	}
	for k, v := range envs {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, command)

	out, err := d.cmd(args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("new-pane in %s: %s: %w", session, string(out), err)
	}
	paneID := strings.TrimSpace(string(out))

	// Set pane title for identification.
	if title != "" {
		_ = d.cmd("select-pane", "-t", paneID, "-T", title).Run()
	}

	// Ensure window closes when command exits (don't leave orphaned shells).
	_ = d.cmd("set-option", "-t", paneID, "remain-on-exit", "off").Run()

	return paneID, nil
}

// HasPane checks if a tmux pane still exists.
//
// display-message -t <paneID> -p <fmt> exits 0 even when the target
// pane is gone, so it can't be used to test pane liveness. list-panes
// validates the target against the live pane list and exits 1 for
// unknown IDs. See ArcavenAE/marvel#10 — before this change ReapDead
// never saw dead panes and sessions stayed 'running/unknown' forever.
func (d *Driver) HasPane(paneID string) bool {
	return d.cmd("list-panes", "-t", paneID, "-F", "#{pane_id}").Run() == nil
}

// KillPane destroys a specific pane.
func (d *Driver) KillPane(paneID string) error {
	if out, err := d.cmd("kill-pane", "-t", paneID).CombinedOutput(); err != nil {
		return fmt.Errorf("kill-pane %s: %s: %w", paneID, string(out), err)
	}
	return nil
}

// KillSession destroys an entire tmux session.
func (d *Driver) KillSession(name string) error {
	if !d.HasSession(name) {
		return nil
	}
	if out, err := d.cmd("kill-session", "-t", name).CombinedOutput(); err != nil {
		return fmt.Errorf("kill-session %s: %s: %w", name, string(out), err)
	}
	return nil
}

// KillServer shuts down the tmux server this driver is scoped to.
// Used by test teardown to drop a per-package tmux server at the end of
// the package's tests; safe to call when no server is running (returns
// nil). Production code should not call this — it tears down every
// tmux workload on the server.
func (d *Driver) KillServer() error {
	if out, err := d.cmd("kill-server").CombinedOutput(); err != nil {
		// kill-server exits non-zero when no server is running.
		if strings.Contains(string(out), "no server running") {
			return nil
		}
		return fmt.Errorf("kill-server: %s: %w", string(out), err)
	}
	return nil
}

// SendKeys sends keystrokes to a tmux pane. If literal is true, each key is
// sent literally (tmux send-keys -l) — no interpretation of special key names.
// If enter is true, an Enter keystroke is appended after the text.
func (d *Driver) SendKeys(paneID, text string, literal, enter bool) error {
	args := []string{"send-keys", "-t", paneID}
	if literal {
		args = append(args, "-l")
	}
	args = append(args, text)

	if out, err := d.cmd(args...).CombinedOutput(); err != nil {
		return fmt.Errorf("send-keys %s: %s: %w", paneID, string(out), err)
	}

	if enter {
		if out, err := d.cmd("send-keys", "-t", paneID, "Enter").CombinedOutput(); err != nil {
			return fmt.Errorf("send-keys Enter %s: %s: %w", paneID, string(out), err)
		}
	}
	return nil
}

// CapturePane captures the visible content of a tmux pane and returns it as a
// string. Captures the entire visible area including trailing whitespace lines.
func (d *Driver) CapturePane(paneID string) (string, error) {
	out, err := d.cmd("capture-pane", "-t", paneID, "-p").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capture-pane %s: %s: %w", paneID, string(out), err)
	}
	return string(out), nil
}

// CapturePaneRange captures pane content with explicit start and end line
// numbers. Negative values reference the scrollback buffer (e.g., -100 for
// 100 lines of history). This allows capturing scrollback beyond the visible area.
func (d *Driver) CapturePaneRange(paneID string, start, end int) (string, error) {
	out, err := d.cmd("capture-pane", "-t", paneID, "-p",
		"-S", fmt.Sprintf("%d", start),
		"-E", fmt.Sprintf("%d", end)).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capture-pane %s [%d:%d]: %s: %w", paneID, start, end, string(out), err)
	}
	return string(out), nil
}

// ListPanes lists all panes across all windows in a session.
func (d *Driver) ListPanes(session string) ([]PaneInfo, error) {
	out, err := d.cmd("list-panes", "-t", session, "-s",
		"-F", "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_title}").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list-panes %s: %s: %w", session, string(out), err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var panes []PaneInfo
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		p := PaneInfo{ID: parts[0]}
		if len(parts) > 1 {
			p.PID = parts[1]
		}
		if len(parts) > 2 {
			p.Command = parts[2]
		}
		if len(parts) > 3 {
			p.Title = parts[3]
		}
		panes = append(panes, p)
	}
	return panes, nil
}
