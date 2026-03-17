// Package session manages session lifecycle — creating and destroying
// sessions by coordinating the API store and tmux driver.
package session

import (
	"fmt"
	"log"
	"time"

	"github.com/arcaven/marvel/internal/api"
	"github.com/arcaven/marvel/internal/tmux"
)

// Manager creates and destroys sessions.
type Manager struct {
	store  *api.Store
	driver *tmux.Driver
}

// NewManager creates a session manager.
func NewManager(store *api.Store, driver *tmux.Driver) *Manager {
	return &Manager{store: store, driver: driver}
}

// tmuxSessionName returns the tmux session name for a workspace.
func tmuxSessionName(workspace string) string {
	return "marvel-" + workspace
}

// Create creates a new session: registers it in the store, ensures the tmux
// session exists, and spawns a pane running the runtime command.
func (m *Manager) Create(sess *api.Session) error {
	sess.State = api.SessionPending
	sess.CreatedAt = time.Now().UTC()

	if err := m.store.CreateSession(sess); err != nil {
		return fmt.Errorf("create session %s: %w", sess.Key(), err)
	}

	tmuxSess := tmuxSessionName(sess.Workspace)
	if err := m.driver.NewSession(tmuxSess); err != nil {
		return fmt.Errorf("ensure tmux session %s: %w", tmuxSess, err)
	}

	cmd := sess.Runtime.Command
	for _, arg := range sess.Runtime.Args {
		cmd += " " + arg
	}

	paneID, err := m.driver.NewPane(tmuxSess, cmd, sess.Name)
	if err != nil {
		// Clean up store on failure.
		_ = m.store.DeleteSession(sess.Key())
		return fmt.Errorf("create pane for %s: %w", sess.Key(), err)
	}

	sess.PaneID = paneID
	sess.State = api.SessionRunning
	log.Printf("session %s running in pane %s", sess.Key(), paneID)
	return nil
}

// Delete destroys a session: kills the tmux pane and removes from the store.
func (m *Manager) Delete(key string) error {
	sess, err := m.store.GetSession(key)
	if err != nil {
		return err
	}

	if sess.PaneID != "" {
		if err := m.driver.KillPane(sess.PaneID); err != nil {
			log.Printf("warning: kill pane %s: %v", sess.PaneID, err)
		}
	}

	if err := m.store.DeleteSession(key); err != nil {
		return fmt.Errorf("delete session %s from store: %w", key, err)
	}

	log.Printf("session %s deleted", key)
	return nil
}

// DeleteAllInWorkspace destroys all sessions in a workspace.
func (m *Manager) DeleteAllInWorkspace(workspace string) {
	sessions := m.store.ListSessions()
	for _, s := range sessions {
		if s.Workspace == workspace {
			if err := m.Delete(s.Key()); err != nil {
				log.Printf("warning: delete session %s: %v", s.Key(), err)
			}
		}
	}
}

// CleanupWorkspace tears down the tmux session for a workspace.
func (m *Manager) CleanupWorkspace(workspace string) error {
	m.DeleteAllInWorkspace(workspace)
	return m.driver.KillSession(tmuxSessionName(workspace))
}
