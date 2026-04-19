package api

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrNotFound is returned when a resource is not found in the store.
var ErrNotFound = fmt.Errorf("resource not found")

// ErrAlreadyExists is returned when a resource already exists in the store.
var ErrAlreadyExists = fmt.Errorf("resource already exists")

// Store holds all marvel resources in memory. The store is the
// synchronization boundary: all reads return value snapshots (decoupled
// from internal state), and all mutations go through Update* methods that
// take the write lock. Pointers to internal objects never escape the
// store. See orc finding-032.
type Store struct {
	mu         sync.RWMutex
	workspaces map[string]*Workspace
	sessions   map[string]*Session
	teams      map[string]*Team
	endpoints  map[string]*Endpoint
}

// NewStore creates an empty in-memory store.
func NewStore() *Store {
	return &Store{
		workspaces: make(map[string]*Workspace),
		sessions:   make(map[string]*Session),
		teams:      make(map[string]*Team),
		endpoints:  make(map[string]*Endpoint),
	}
}

// --- clone helpers ---

// Session/Workspace/Endpoint are either flat or contain a Runtime whose
// only aliasable field is Args. Team contains Roles (each with a
// HealthCheck pointer) and a Shift (with a Roles []string). Clone deeply
// enough that a snapshot is safe to mutate or marshal while the store
// continues to update the live objects.

func cloneRuntime(r Runtime) Runtime {
	out := r
	if len(r.Args) > 0 {
		out.Args = slices.Clone(r.Args)
	}
	return out
}

func cloneSession(s *Session) Session {
	out := *s
	out.Runtime = cloneRuntime(s.Runtime)
	return out
}

func cloneRole(r Role) Role {
	out := r
	out.Runtime = cloneRuntime(r.Runtime)
	if r.HealthCheck != nil {
		hc := *r.HealthCheck
		out.HealthCheck = &hc
	}
	return out
}

func cloneTeam(t *Team) Team {
	out := *t
	if len(t.Roles) > 0 {
		out.Roles = make([]Role, len(t.Roles))
		for i, r := range t.Roles {
			out.Roles[i] = cloneRole(r)
		}
	}
	if len(t.Shift.Roles) > 0 {
		out.Shift.Roles = slices.Clone(t.Shift.Roles)
	}
	return out
}

// Workspace operations

// CreateWorkspace clones the input into the store. The caller's pointer
// is not aliased with store state.
func (s *Store) CreateWorkspace(w *Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[w.Key()]; ok {
		return fmt.Errorf("workspace %s: %w", w.Key(), ErrAlreadyExists)
	}
	c := *w
	s.workspaces[w.Key()] = &c
	return nil
}

// GetWorkspace returns a snapshot of the named workspace.
func (s *Store) GetWorkspace(name string) (Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.workspaces[name]
	if !ok {
		return Workspace{}, fmt.Errorf("workspace %s: %w", name, ErrNotFound)
	}
	return *w, nil
}

// ListWorkspaces returns snapshots of all workspaces.
func (s *Store) ListWorkspaces() []Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Workspace, 0, len(s.workspaces))
	for _, w := range s.workspaces {
		result = append(result, *w)
	}
	return result
}

func (s *Store) DeleteWorkspace(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[name]; !ok {
		return fmt.Errorf("workspace %s: %w", name, ErrNotFound)
	}
	delete(s.workspaces, name)
	return nil
}

// Session operations

// CreateSession clones the input into the store. The caller's pointer
// is not aliased with store state; further mutation of sess does not
// affect the store. Use UpdateSession to commit subsequent changes.
func (s *Store) CreateSession(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sess.Key()]; ok {
		return fmt.Errorf("session %s: %w", sess.Key(), ErrAlreadyExists)
	}
	c := cloneSession(sess)
	s.sessions[sess.Key()] = &c
	return nil
}

// GetSession returns a snapshot of the named session.
func (s *Store) GetSession(key string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[key]
	if !ok {
		return Session{}, fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	return cloneSession(sess), nil
}

// ListSessions returns snapshots of all sessions.
func (s *Store) ListSessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		result = append(result, cloneSession(sess))
	}
	return result
}

// ListSessionsByTeam returns snapshots of sessions in the given team.
func (s *Store) ListSessionsByTeam(workspace, team string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Session
	for _, sess := range s.sessions {
		if sess.Workspace == workspace && sess.Team == team {
			result = append(result, cloneSession(sess))
		}
	}
	return result
}

// ListSessionsByTeamRole returns snapshots of sessions in the given team and role.
func (s *Store) ListSessionsByTeamRole(workspace, team, role string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Session
	for _, sess := range s.sessions {
		if sess.Workspace == workspace && sess.Team == team && sess.Role == role {
			result = append(result, cloneSession(sess))
		}
	}
	return result
}

// ListSessionsByTeamRoleGeneration returns snapshots of sessions in the
// given team, role, and generation.
func (s *Store) ListSessionsByTeamRoleGeneration(workspace, team, role string, generation int64) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Session
	for _, sess := range s.sessions {
		if sess.Workspace == workspace && sess.Team == team && sess.Role == role && sess.Generation == generation {
			result = append(result, cloneSession(sess))
		}
	}
	return result
}

func (s *Store) DeleteSession(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[key]; !ok {
		return fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	delete(s.sessions, key)
	return nil
}

// UpdateSession applies fn to the live session under the write lock.
// The pointer passed to fn is valid only for fn's execution — do not
// stash it. Returning an error from fn aborts the update (no state is
// rolled back; the caller is responsible for not making partial writes
// that don't make sense together).
func (s *Store) UpdateSession(key string, fn func(*Session) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	return fn(sess)
}

// Team operations

// CreateTeam clones the input into the store. The caller's pointer is
// not aliased with store state.
func (s *Store) CreateTeam(t *Team) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.teams[t.Key()]; ok {
		return fmt.Errorf("team %s: %w", t.Key(), ErrAlreadyExists)
	}
	c := cloneTeam(t)
	s.teams[t.Key()] = &c
	return nil
}

// GetTeam returns a snapshot of the named team.
func (s *Store) GetTeam(key string) (Team, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.teams[key]
	if !ok {
		return Team{}, fmt.Errorf("team %s: %w", key, ErrNotFound)
	}
	return cloneTeam(t), nil
}

// ListTeams returns snapshots of all teams.
func (s *Store) ListTeams() []Team {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Team, 0, len(s.teams))
	for _, t := range s.teams {
		result = append(result, cloneTeam(t))
	}
	return result
}

func (s *Store) DeleteTeam(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.teams[key]; !ok {
		return fmt.Errorf("team %s: %w", key, ErrNotFound)
	}
	delete(s.teams, key)
	return nil
}

// UpdateTeam applies fn to the live team under the write lock. Same
// pointer-lifetime rules as UpdateSession.
func (s *Store) UpdateTeam(key string, fn func(*Team) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[key]
	if !ok {
		return fmt.Errorf("team %s: %w", key, ErrNotFound)
	}
	return fn(t)
}

// Endpoint operations

// CreateEndpoint clones the input into the store.
func (s *Store) CreateEndpoint(e *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.endpoints[e.Key()]; ok {
		return fmt.Errorf("endpoint %s: %w", e.Key(), ErrAlreadyExists)
	}
	c := *e
	s.endpoints[e.Key()] = &c
	return nil
}

// GetEndpoint returns a snapshot of the named endpoint.
func (s *Store) GetEndpoint(key string) (Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.endpoints[key]
	if !ok {
		return Endpoint{}, fmt.Errorf("endpoint %s: %w", key, ErrNotFound)
	}
	return *e, nil
}

// ListEndpoints returns snapshots of all endpoints.
func (s *Store) ListEndpoints() []Endpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Endpoint, 0, len(s.endpoints))
	for _, e := range s.endpoints {
		result = append(result, *e)
	}
	return result
}

func (s *Store) DeleteEndpoint(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.endpoints[key]; !ok {
		return fmt.Errorf("endpoint %s: %w", key, ErrNotFound)
	}
	delete(s.endpoints, key)
	return nil
}

// UpdateSessionHeartbeat updates a session's context pressure and
// heartbeat timestamp. Kept as a convenience helper; equivalent to
// UpdateSession with the corresponding closure.
func (s *Store) UpdateSessionHeartbeat(key string, contextPercent float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	sess.ContextPercent = contextPercent
	sess.LastHeartbeat = time.Now().UTC()
	return nil
}
