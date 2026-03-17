package api

import (
	"fmt"
	"sync"
	"time"
)

// ErrNotFound is returned when a resource is not found in the store.
var ErrNotFound = fmt.Errorf("resource not found")

// ErrAlreadyExists is returned when a resource already exists in the store.
var ErrAlreadyExists = fmt.Errorf("resource already exists")

// Store holds all marvel resources in memory.
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

// Workspace operations

func (s *Store) CreateWorkspace(w *Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[w.Key()]; ok {
		return fmt.Errorf("workspace %s: %w", w.Key(), ErrAlreadyExists)
	}
	s.workspaces[w.Key()] = w
	return nil
}

func (s *Store) GetWorkspace(name string) (*Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.workspaces[name]
	if !ok {
		return nil, fmt.Errorf("workspace %s: %w", name, ErrNotFound)
	}
	return w, nil
}

func (s *Store) ListWorkspaces() []*Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Workspace, 0, len(s.workspaces))
	for _, w := range s.workspaces {
		result = append(result, w)
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

func (s *Store) CreateSession(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sess.Key()]; ok {
		return fmt.Errorf("session %s: %w", sess.Key(), ErrAlreadyExists)
	}
	s.sessions[sess.Key()] = sess
	return nil
}

func (s *Store) GetSession(key string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[key]
	if !ok {
		return nil, fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	return sess, nil
}

func (s *Store) ListSessions() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		result = append(result, sess)
	}
	return result
}

func (s *Store) ListSessionsByTeam(workspace, team string) []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Session
	for _, sess := range s.sessions {
		if sess.Workspace == workspace && sess.Team == team {
			result = append(result, sess)
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

// Team operations

func (s *Store) CreateTeam(t *Team) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.teams[t.Key()]; ok {
		return fmt.Errorf("team %s: %w", t.Key(), ErrAlreadyExists)
	}
	s.teams[t.Key()] = t
	return nil
}

func (s *Store) GetTeam(key string) (*Team, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.teams[key]
	if !ok {
		return nil, fmt.Errorf("team %s: %w", key, ErrNotFound)
	}
	return t, nil
}

func (s *Store) ListTeams() []*Team {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Team, 0, len(s.teams))
	for _, t := range s.teams {
		result = append(result, t)
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

// Endpoint operations

func (s *Store) CreateEndpoint(e *Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.endpoints[e.Key()]; ok {
		return fmt.Errorf("endpoint %s: %w", e.Key(), ErrAlreadyExists)
	}
	s.endpoints[e.Key()] = e
	return nil
}

func (s *Store) GetEndpoint(key string) (*Endpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.endpoints[key]
	if !ok {
		return nil, fmt.Errorf("endpoint %s: %w", key, ErrNotFound)
	}
	return e, nil
}

func (s *Store) ListEndpoints() []*Endpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Endpoint, 0, len(s.endpoints))
	for _, e := range s.endpoints {
		result = append(result, e)
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

// UpdateSessionHeartbeat updates a session's context pressure and heartbeat timestamp.
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
