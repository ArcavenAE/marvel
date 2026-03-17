// Package tmux provides a shell-out driver for tmux operations.
package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// Driver manages tmux sessions and panes by shelling out to the tmux binary.
type Driver struct {
	binary string
}

// NewDriver creates a tmux driver, verifying tmux is available.
func NewDriver() (*Driver, error) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return nil, fmt.Errorf("tmux not found: %w", err)
	}
	return &Driver{binary: path}, nil
}

// HasSession checks if a tmux session exists.
func (d *Driver) HasSession(name string) bool {
	cmd := exec.Command(d.binary, "has-session", "-t", name)
	return cmd.Run() == nil
}

// NewSession creates a new tmux session in detached mode.
func (d *Driver) NewSession(name string) error {
	if d.HasSession(name) {
		return nil
	}
	cmd := exec.Command(d.binary, "new-session", "-d", "-s", name)
	if out, err := cmd.CombinedOutput(); err != nil {
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

	cmd := exec.Command(d.binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("new-pane in %s: %s: %w", session, string(out), err)
	}
	paneID := strings.TrimSpace(string(out))

	// Set pane title for identification.
	if title != "" {
		setTitle := exec.Command(d.binary, "select-pane", "-t", paneID, "-T", title)
		_ = setTitle.Run()
	}

	// Ensure window closes when command exits (don't leave orphaned shells).
	setOpt := exec.Command(d.binary, "set-option", "-t", paneID, "remain-on-exit", "off")
	_ = setOpt.Run()

	return paneID, nil
}

// HasPane checks if a tmux pane still exists.
func (d *Driver) HasPane(paneID string) bool {
	cmd := exec.Command(d.binary, "display-message", "-t", paneID, "-p", "")
	return cmd.Run() == nil
}

// KillPane destroys a specific pane.
func (d *Driver) KillPane(paneID string) error {
	cmd := exec.Command(d.binary, "kill-pane", "-t", paneID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kill-pane %s: %s: %w", paneID, string(out), err)
	}
	return nil
}

// KillSession destroys an entire tmux session.
func (d *Driver) KillSession(name string) error {
	if !d.HasSession(name) {
		return nil
	}
	cmd := exec.Command(d.binary, "kill-session", "-t", name)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kill-session %s: %s: %w", name, string(out), err)
	}
	return nil
}

// ListPanes lists all panes across all windows in a session.
func (d *Driver) ListPanes(session string) ([]PaneInfo, error) {
	cmd := exec.Command(d.binary, "list-panes", "-t", session, "-s",
		"-F", "#{pane_id}\t#{pane_pid}\t#{pane_current_command}\t#{pane_title}")
	out, err := cmd.CombinedOutput()
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
