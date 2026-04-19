// Package session manages session lifecycle — creating and destroying
// sessions by coordinating the API store, tmux driver, and runtime adapters.
package session

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
	"github.com/arcavenae/marvel/internal/tmux"
)

// Manager creates and destroys sessions.
type Manager struct {
	store      *api.Store
	driver     *tmux.Driver
	adapters   *runtime.Registry
	SocketPath string
	// Events receives structured state-transition events. Nil is safe
	// (all emission sites use events.Emit which no-ops on nil) so tests
	// and callers that don't care about the event stream don't need to
	// wire a ring.
	Events events.Emitter
}

// NewManager creates a session manager with the default runtime adapter registry.
func NewManager(store *api.Store, driver *tmux.Driver) *Manager {
	return &Manager{store: store, driver: driver, adapters: runtime.NewRegistry()}
}

// marvelSessionPrefix is the tmux session name prefix marvel owns.
// Every tmux session named marvel-* is considered marvel-managed; a
// fresh daemon reclaims the prefix by killing any leftovers on startup.
const marvelSessionPrefix = "marvel-"

// tmuxSessionName returns the tmux session name for a workspace.
func tmuxSessionName(workspace string) string {
	return marvelSessionPrefix + workspace
}

// CleanupOrphanTmux kills every tmux session whose name starts with the
// marvel- prefix. Called at daemon startup so a fresh in-memory state
// doesn't coexist with panes and processes from a previous daemon instance.
// See ArcavenAE/marvel#13.
func (m *Manager) CleanupOrphanTmux() error {
	return m.cleanupOrphanTmuxPrefix(marvelSessionPrefix)
}

// cleanupOrphanTmuxPrefix is the prefix-parameterized core of
// CleanupOrphanTmux. Tests use a unique prefix so they don't collide
// with other tmux-using tests running in parallel packages.
func (m *Manager) cleanupOrphanTmuxPrefix(prefix string) error {
	names, err := m.driver.ListSessions()
	if err != nil {
		return fmt.Errorf("list tmux sessions: %w", err)
	}
	var killed int
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if err := m.driver.KillSession(name); err != nil {
			log.Printf("cleanup orphan tmux %s: %v", name, err)
			continue
		}
		killed++
	}
	if killed > 0 {
		log.Printf("cleanup: killed %d orphan tmux session(s) from previous daemon", killed)
	}
	return nil
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

	cmd, envs := m.resolveRuntime(sess)

	// Log the exact command line we're about to exec so post-hoc
	// debugging has the argv — operators otherwise had to guess what
	// tmux new-window was actually running when a pane died quickly.
	// See ArcavenAE/marvel#9.
	log.Printf("session %s exec: %s", sess.Key(), cmd)

	paneID, err := m.driver.NewPane(tmuxSess, cmd, sess.Name, envs)
	if err != nil {
		// Clean up store on failure.
		_ = m.store.DeleteSession(sess.Key())
		return fmt.Errorf("create pane for %s: %w", sess.Key(), err)
	}

	// Commit PaneID + State=Running to the live session under the store
	// lock. Also update the caller's *api.Session so returning pointers
	// stay consistent for any downstream emission/logging that references
	// fields on sess. Per orc finding-032, the Store is the sync boundary.
	if err := m.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.PaneID = paneID
		live.State = api.SessionRunning
		return nil
	}); err != nil {
		return fmt.Errorf("update session %s post-create: %w", sess.Key(), err)
	}
	sess.PaneID = paneID
	sess.State = api.SessionRunning
	log.Printf("session %s running in pane %s", sess.Key(), paneID)
	events.Emit(m.Events, events.Event{
		Kind:      events.KindSessionCreated,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
		Message:   fmt.Sprintf("pane %s", paneID),
	})
	return nil
}

// resolveRuntime uses the adapter registry when team/role context is available,
// falling back to direct command construction for ad-hoc sessions.
func (m *Manager) resolveRuntime(sess *api.Session) (string, map[string]string) {
	// Look up team and role for full adapter context. Store returns
	// snapshots — taking addresses of these locals is safe because the
	// adapter is read-only and the LaunchContext doesn't outlive this
	// function.
	team, teamErr := m.store.GetTeam(fmt.Sprintf("%s/%s", sess.Workspace, sess.Team))
	if teamErr != nil {
		// Ad-hoc session or team not found — use direct command.
		return m.directCommand(sess)
	}

	var role *api.Role
	for i := range team.Roles {
		if team.Roles[i].Name == sess.Role {
			role = &team.Roles[i]
			break
		}
	}
	if role == nil {
		return m.directCommand(sess)
	}

	ws, wsErr := m.store.GetWorkspace(sess.Workspace)
	if wsErr != nil {
		return m.directCommand(sess)
	}

	adapter := m.adapters.Resolve(sess.Runtime.Name)
	result, err := adapter.Prepare(&runtime.LaunchContext{
		Session:    sess,
		Role:       role,
		Team:       &team,
		Workspace:  &ws,
		SocketPath: m.SocketPath,
	})
	if err != nil {
		log.Printf("adapter %s prepare failed for %s, falling back: %v", adapter.Name(), sess.Key(), err)
		return m.directCommand(sess)
	}

	log.Printf("session %s using %s adapter", sess.Key(), adapter.Name())
	return result.Command, result.Env
}

// directCommand builds the command string directly — the pre-adapter path
// used for ad-hoc sessions or when the adapter can't resolve.
func (m *Manager) directCommand(sess *api.Session) (string, map[string]string) {
	cmd := sess.Runtime.Command
	for _, arg := range sess.Runtime.Args {
		cmd += " " + arg
	}
	envs := map[string]string{
		"MARVEL_SESSION": sess.Name,
		"MARVEL_ROLE":    sess.Role,
	}
	if m.SocketPath != "" {
		envs["MARVEL_SOCKET"] = m.SocketPath
	}
	return cmd, envs
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
	events.Emit(m.Events, events.Event{
		Kind:      events.KindSessionDeleted,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
		Message:   "session deleted",
	})
	return nil
}

// ReapedSession captures the identity of a session whose pane vanished
// and that ReapDead removed from the store. Carries the role coordinates
// so the team controller can attribute the crash to the right role for
// restart bookkeeping — the reap path is one of two converging points
// into the crash-loop backoff logic (the other is the health path). See
// ArcavenAE/marvel#11.
type ReapedSession struct {
	Key       string
	Workspace string
	Team      string
	Role      string
}

// ReapDead marks sessions whose tmux pane no longer exists as Crashed
// (keeping them in the store with PaneID cleared so operators see the
// transient via `marvel get sessions`) and returns enough identity
// information for the caller to do per-role bookkeeping.
//
// Previously this method deleted reaped sessions immediately. The
// resulting window — session gone from store, replacement not yet
// spawned because of backoff — left operators with no visible signal
// that a crash had occurred. See ArcavenAE/marvel#10, aae-orc-8ci.
//
// To keep the store bounded, each call first clears any existing
// Crashed sessions for a role before marking the newly-reaped session
// Crashed — so at most one Crashed marker exists per role at a time.
// The team controller's reconcileRole additionally clears Crashed
// markers for a role at the moment it spawns a replacement.
func (m *Manager) ReapDead() []ReapedSession {
	var reaped []ReapedSession
	sessions := m.store.ListSessions()
	for _, sess := range sessions {
		if sess.PaneID == "" {
			// Already reaped (Crashed) or never had a pane. Skip.
			continue
		}
		if !m.driver.HasPane(sess.PaneID) {
			log.Printf("session %s: pane %s gone, marking crashed", sess.Key(), sess.PaneID)
			lostPane := sess.PaneID
			m.clearStaleCrashed(sessions, sess.Workspace, sess.Team, sess.Role, sess.Key())
			if err := m.store.UpdateSession(sess.Key(), func(live *api.Session) error {
				live.State = api.SessionCrashed
				live.PaneID = ""
				return nil
			}); err != nil {
				log.Printf("warning: mark crashed %s: %v", sess.Key(), err)
				continue
			}
			reaped = append(reaped, ReapedSession{
				Key:       sess.Key(),
				Workspace: sess.Workspace,
				Team:      sess.Team,
				Role:      sess.Role,
			})
			events.Emit(m.Events, events.Event{
				Kind:      events.KindSessionCrashed,
				Severity:  events.SeverityWarning,
				Workspace: sess.Workspace,
				Team:      sess.Team,
				Role:      sess.Role,
				Session:   sess.Key(),
				Message:   fmt.Sprintf("pane %s gone", lostPane),
			})
		}
	}
	return reaped
}

// clearStaleCrashed removes any Crashed session for the given role,
// excluding the session about to be marked Crashed. Caps the store at
// one Crashed marker per role so the reap path can't accumulate ghosts
// across a saturated role's many crashes.
func (m *Manager) clearStaleCrashed(snapshot []api.Session, workspace, team, role, exceptKey string) {
	for _, other := range snapshot {
		if other.State != api.SessionCrashed {
			continue
		}
		if other.Workspace != workspace || other.Team != team || other.Role != role {
			continue
		}
		if other.Key() == exceptKey {
			continue
		}
		if err := m.store.DeleteSession(other.Key()); err != nil {
			log.Printf("warning: delete stale crashed %s: %v", other.Key(), err)
		}
	}
}

// ClearCrashedForRole deletes all Crashed marker sessions for a
// (workspace, team, role). Called by the team controller the moment it
// spawns a replacement — the crash marker has served its observability
// purpose and the fresh session is the new truth.
func (m *Manager) ClearCrashedForRole(workspace, team, role string) {
	for _, sess := range m.store.ListSessions() {
		if sess.State != api.SessionCrashed {
			continue
		}
		if sess.Workspace != workspace || sess.Team != team || sess.Role != role {
			continue
		}
		if err := m.store.DeleteSession(sess.Key()); err != nil {
			log.Printf("warning: clear crashed %s: %v", sess.Key(), err)
		}
	}
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
