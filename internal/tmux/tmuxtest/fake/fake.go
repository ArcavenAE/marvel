// Package fake is an in-memory stand-in for *tmux.Driver. It lives under
// tmuxtest but in its own package because it imports internal/tmux for the
// value types, and internal/tmux's own tests import tmuxtest; a fake in tmuxtest
// itself would be an import cycle. It is imported by tests only.
//
// It keeps sessions and panes in maps and runs no process. A pane it creates is
// alive until KillPane, KillSession or Exit ends it. A test that needs a real
// process to start, run and exit, or needs the pane's real output, stays on real
// tmux.
package fake

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/arcavenae/marvel/internal/tmux"
)

// Driver is the in-memory driver. The zero value is not usable; call New.
type Driver struct {
	mu       sync.Mutex
	sessions map[string]bool
	panes    map[string]*pane
	next     int
}

type pane struct {
	session string
	cmd     string
	title   string
	dir     string
	env     map[string]string
	keep    bool
	pid     int
	dead    bool
	status  string
	screen  strings.Builder
}

// New returns an empty driver: no sessions, no panes.
func New() *Driver {
	return &Driver{sessions: map[string]bool{}, panes: map[string]*pane{}}
}

func (d *Driver) ListSessions() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for n := range d.sessions {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func (d *Driver) NewSession(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sessions[name] {
		return fmt.Errorf("duplicate session: %s", name)
	}
	d.sessions[name] = true
	return nil
}

func (d *Driver) KillSession(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sessions[name] {
		return fmt.Errorf("can't find session: %s", name)
	}
	delete(d.sessions, name)
	for id, p := range d.panes {
		if p.session == name {
			delete(d.panes, id)
		}
	}
	return nil
}

func (d *Driver) NewPaneAt(session, command, title, dir string, envs map[string]string, keepOnExit bool) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sessions[session] {
		return "", fmt.Errorf("can't find session: %s", session)
	}
	d.next++
	id := fmt.Sprintf("%%%d", d.next)
	d.panes[id] = &pane{session: session, cmd: command, title: title, dir: dir, env: envs, keep: keepOnExit, pid: 40000 + d.next}
	return id, nil
}

func (d *Driver) KillPane(paneID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.panes[paneID]; !ok {
		return fmt.Errorf("can't find pane: %s", paneID)
	}
	delete(d.panes, paneID)
	return nil
}

func (d *Driver) SendKeys(paneID, text string, literal, enter bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.panes[paneID]
	if !ok {
		return fmt.Errorf("can't find pane: %s", paneID)
	}
	p.screen.WriteString(text)
	if enter {
		p.screen.WriteString("\n")
	}
	return nil
}

func (d *Driver) CapturePane(paneID string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.panes[paneID]
	if !ok {
		return "", fmt.Errorf("can't find pane: %s", paneID)
	}
	return p.screen.String(), nil
}

func (d *Driver) ListPanes(session string) ([]tmux.PaneInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.sessions[session] {
		return nil, fmt.Errorf("can't find session: %s", session)
	}
	var ids []string
	for id, p := range d.panes {
		if p.session == session {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []tmux.PaneInfo
	for _, id := range ids {
		p := d.panes[id]
		out = append(out, tmux.PaneInfo{ID: id, PID: fmt.Sprint(p.pid), Command: p.cmd, Title: p.title, Created: true})
	}
	return out, nil
}

func (d *Driver) PanePID(paneID string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.panes[paneID]
	if !ok {
		return 0, fmt.Errorf("can't find pane: %s", paneID)
	}
	return p.pid, nil
}

func (d *Driver) PaneStatus(paneID string) (tmux.PaneStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.statusLocked(paneID), nil
}

func (d *Driver) PaneStatuses() (map[string]tmux.PaneStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]tmux.PaneStatus, len(d.panes))
	for id := range d.panes {
		out[id] = d.statusLocked(id)
	}
	return out, nil
}

func (d *Driver) statusLocked(paneID string) tmux.PaneStatus {
	p, ok := d.panes[paneID]
	if !ok {
		return tmux.PaneStatus{}
	}
	return tmux.PaneStatus{Exists: true, Dead: p.dead, ExitStatus: p.status, Created: true}
}

func (d *Driver) BearerPresence(session, name string) tmux.BearerPresence {
	return tmux.BearerUnknown
}

// Exit ends the pane's process with the given status and keeps the pane, as
// remain-on-exit does, so a test can read the exit the way it reads a real one.
func (d *Driver) Exit(paneID, status string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.panes[paneID]; ok {
		p.dead, p.status = true, status
	}
}

// PaneCount reports how many panes exist, for a test that checks what a call
// started or stopped.
func (d *Driver) PaneCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.panes)
}
