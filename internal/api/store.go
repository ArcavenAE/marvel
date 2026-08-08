package api

import (
	"fmt"
	"slices"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
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
	policies   map[string]*Policy

	// bolt is the optional L2 persistence backend. Nil means in-memory
	// only (default for tests + the legacy daemon path). Populated by
	// OpenBolt; cleared by CloseBolt. See bolt.go for the WAL discipline.
	bolt     *bolt.DB
	boltPath string
}

// NewStore creates an empty in-memory store.
func NewStore() *Store {
	return &Store{
		workspaces: make(map[string]*Workspace),
		sessions:   make(map[string]*Session),
		teams:      make(map[string]*Team),
		endpoints:  make(map[string]*Endpoint),
		policies:   make(map[string]*Policy),
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

func clonePolicy(p *Policy) Policy {
	out := *p
	out.Settings = cloneSettings(p.Settings)
	return out
}

// cloneSettings deep-copies a JSON-shaped settings tree so a snapshot is
// safe to mutate or marshal while the store keeps updating the live one.
// Settings only ever holds JSON-decoded values (map[string]any, []any,
// and scalars), so a structural walk is enough; scalars copy by value.
func cloneSettings(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneSettingsValue(v)
	}
	return out
}

func cloneSettingsValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneSettings(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneSettingsValue(e)
		}
		return out
	default:
		return t
	}
}

// Workspace operations

// CreateWorkspace clones the input into the store. The caller's pointer
// is not aliased with store state. When bolt is open, persistence is
// written before the in-memory map is updated — if the disk write fails,
// in-memory state is unchanged.
func (s *Store) CreateWorkspace(w *Workspace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[w.Key()]; ok {
		return fmt.Errorf("workspace %s: %w", w.Key(), ErrAlreadyExists)
	}
	c := *w
	if err := s.persistPut(bucketWorkspaces, c.Key(), c); err != nil {
		return err
	}
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
	if err := s.persistDelete(bucketWorkspaces, name); err != nil {
		return err
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
	if err := s.persistPut(bucketSessions, c.Key(), c); err != nil {
		return err
	}
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
	if err := s.persistDelete(bucketSessions, key); err != nil {
		return err
	}
	delete(s.sessions, key)
	return nil
}

// UpdateSession applies fn to the live session under the write lock.
// The pointer passed to fn is valid only for fn's execution — do not
// stash it. Returning an error from fn aborts the update (no state is
// rolled back; the caller is responsible for not making partial writes
// that don't make sense together).
//
// When bolt is open, persistence happens after fn returns successfully.
// If persist fails the in-memory state is already updated — the post-fn
// pre-persist window is a known spike limitation (see orc question-
// marvel-transaction-log). The reconciler's next successful update
// will re-converge state.
func (s *Store) UpdateSession(key string, fn func(*Session) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	if err := fn(sess); err != nil {
		return err
	}
	return s.persistPut(bucketSessions, sess.Key(), sess)
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
	if err := s.persistPut(bucketTeams, c.Key(), c); err != nil {
		return err
	}
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
	if err := s.persistDelete(bucketTeams, key); err != nil {
		return err
	}
	delete(s.teams, key)
	return nil
}

// UpdateTeam applies fn to the live team under the write lock. Same
// pointer-lifetime rules as UpdateSession. Persist semantics match
// UpdateSession — see that comment for the post-fn pre-persist window.
func (s *Store) UpdateTeam(key string, fn func(*Team) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[key]
	if !ok {
		return fmt.Errorf("team %s: %w", key, ErrNotFound)
	}
	if err := fn(t); err != nil {
		return err
	}
	return s.persistPut(bucketTeams, t.Key(), t)
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
	if err := s.persistPut(bucketEndpoints, c.Key(), c); err != nil {
		return err
	}
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
	if err := s.persistDelete(bucketEndpoints, key); err != nil {
		return err
	}
	delete(s.endpoints, key)
	return nil
}

// Policy operations

// CreatePolicy clones the input into the store. The caller's pointer is
// not aliased with store state; Settings is deep-copied.
func (s *Store) CreatePolicy(p *Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.policies[p.Key()]; ok {
		return fmt.Errorf("policy %s: %w", p.Key(), ErrAlreadyExists)
	}
	c := clonePolicy(p)
	if err := s.persistPut(bucketPolicies, c.Key(), c); err != nil {
		return err
	}
	s.policies[p.Key()] = &c
	return nil
}

// GetPolicy returns a snapshot of the named policy.
func (s *Store) GetPolicy(key string) (Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[key]
	if !ok {
		return Policy{}, fmt.Errorf("policy %s: %w", key, ErrNotFound)
	}
	return clonePolicy(p), nil
}

// ListPolicies returns snapshots of all policies.
func (s *Store) ListPolicies() []Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Policy, 0, len(s.policies))
	for _, p := range s.policies {
		result = append(result, clonePolicy(p))
	}
	return result
}

func (s *Store) DeletePolicy(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.policies[key]; !ok {
		return fmt.Errorf("policy %s: %w", key, ErrNotFound)
	}
	if err := s.persistDelete(bucketPolicies, key); err != nil {
		return err
	}
	delete(s.policies, key)
	return nil
}

// UpdatePolicy applies fn to the live policy under the write lock. Same
// pointer-lifetime and persist semantics as UpdateTeam.
func (s *Store) UpdatePolicy(key string, fn func(*Policy) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.policies[key]
	if !ok {
		return fmt.Errorf("policy %s: %w", key, ErrNotFound)
	}
	if err := fn(p); err != nil {
		return err
	}
	return s.persistPut(bucketPolicies, p.Key(), p)
}

// UpdateSessionHeartbeat updates a session's context pressure and
// heartbeat timestamp. Kept as a convenience helper; equivalent to
// UpdateSession with the corresponding closure.
//
// Note: heartbeat fields are classified ephemeral in finding-050's
// data-model walk — they're persisted alongside the rest of the
// session record on each heartbeat, but treated as may-be-stale on
// rehydrate. The next heartbeat after restart refreshes them. The
// frequency of heartbeat writes (one per heartbeat per session) is the
// dominant write rate for marvel's bbolt usage; if it surfaces as a
// performance issue, batch by waiting N heartbeats before persisting
// (or move heartbeat state into a separate in-memory-only path).
func (s *Store) UpdateSessionHeartbeat(key string, contextPercent float64, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return fmt.Errorf("session %s: %w", key, ErrNotFound)
	}
	// A heartbeat is a COMPLETE reading of its own shape, not a partial
	// update layered over whatever the accountant left behind. Writing
	// only the percentage used to leave the accountant's ContextRequests
	// and ContextLimit standing, and three downstream sites read those
	// leftovers as provenance: a heartbeat landing after an
	// unresolved-window accountant reading was rendered "?" even though a
	// real percentage had arrived. Replace the record, then declare who
	// wrote it. See aae-orc-ibu9.
	// ContextPeak is deliberately NOT carried across: an accountant peak
	// is a high-water mark against a resolved window, and a heartbeat
	// percentage is the agent's own figure against its own denominator.
	// Carrying it would reintroduce the cross-producer mixing this fixes.
	sess.SessionContext = SessionContext{
		ContextSource:  ContextSourceHeartbeat,
		ContextPercent: contextPercent,
		ContextModel:   sess.ContextModel,
	}
	sess.LastHeartbeat = time.Now().UTC()
	// A cooperative reporter that knows its model names it (the
	// statusline feed does); one that doesn't sends "" and any
	// prior reading stands.
	if model != "" {
		sess.ContextModel = model
	}
	// ContextAt is the single "measured" sentinel for the context column,
	// shared with the usage accountant's path below. A cooperative
	// heartbeat is a measurement too, so it stamps it.
	//
	// A percentage is ALL this path produces: the agent computed it
	// itself, so there is no token count, no window, and no request count
	// to record. That shape is what tells the two producers apart
	// downstream (bolt rehydrate keeps this one, the renderer prints it as
	// a percentage rather than as an unresolved window).
	sess.ContextAt = sess.LastHeartbeat
	return s.persistPut(bucketSessions, sess.Key(), sess)
}

// UpdateSessionContext records one context-window reading.
//
// Like UpdateSessionMetrics and unlike UpdateSessionHeartbeat this does
// not persist, and a missing session is ignored rather than reported: a
// reading is meaningless once the harness process is gone, and the
// accountant works from a live stream that can outlive a delete by one
// event.
//
// It deliberately does NOT touch LastHeartbeat. LastHeartbeat is a
// liveness contract consumed by the team controller's heartbeat health
// check and by shift readiness. Stamping it from stream activity would
// silently redefine "the agent reported in" as "the harness emitted
// bytes", marking a hung-but-still-streaming session healthy and
// declaring a shifting generation ready. That may eventually be wanted;
// it has to be a ruled decision, not a side effect of picking the
// convenient setter.
func (s *Store) UpdateSessionContext(key string, c SessionContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return
	}
	c.ContextAt = time.Now().UTC()
	sess.SessionContext = c
}

// UpdateSessionMetrics records one process-sampler reading. m.MetricsAt
// is set here so every stored reading carries the store's clock.
//
// Unlike UpdateSessionHeartbeat this does not persist. A reading is
// stale the moment the daemon stops, and the sampler refreshes every
// session within one interval of startup, so a bolt write per session
// per interval would buy a number nobody can use. Sessions missing from
// the store are ignored rather than reported: the sampler works from a
// snapshot and a session can be deleted between the snapshot and the
// write.
func (s *Store) UpdateSessionMetrics(key string, m SessionMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[key]
	if !ok {
		return
	}
	m.MetricsAt = time.Now().UTC()
	sess.SessionMetrics = m
}
