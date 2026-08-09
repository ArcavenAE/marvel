package api

import (
	"errors"
	"testing"
	"time"
)

func TestWorkspaceCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	w := &Workspace{Name: "test-ws", CreatedAt: time.Now().UTC()}

	// Create
	if err := s.CreateWorkspace(w); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	// Duplicate
	if err := s.CreateWorkspace(w); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	// Get
	got, err := s.GetWorkspace("test-ws")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if got.Name != "test-ws" {
		t.Fatalf("expected test-ws, got %s", got.Name)
	}

	// List
	list := s.ListWorkspaces()
	if len(list) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(list))
	}

	// Delete
	if err := s.DeleteWorkspace("test-ws"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	// Get after delete
	if _, err := s.GetWorkspace("test-ws"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSessionCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	sess := &Session{
		Name:      "agent-0",
		Workspace: "test-ws",
		Team:      "agents",
		Role:      "worker",
		Runtime:   Runtime{Name: "shell", Command: "bash"},
		State:     SessionPending,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	got, err := s.GetSession("test-ws/agent-0")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.State != SessionPending {
		t.Fatalf("expected pending, got %s", got.State)
	}

	// List by team
	teamSessions := s.ListSessionsByTeam("test-ws", "agents")
	if len(teamSessions) != 1 {
		t.Fatalf("expected 1 team session, got %d", len(teamSessions))
	}

	// Delete
	if err := s.DeleteSession("test-ws/agent-0"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetSession("test-ws/agent-0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete")
	}
}

func TestTeamCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	team := &Team{
		Name:      "agents",
		Workspace: "test-ws",
		Roles: []Role{
			{Name: "worker", Replicas: 3, Runtime: Runtime{Name: "shell", Command: "bash"}},
		},
		CreatedAt: time.Now().UTC(),
	}

	if err := s.CreateTeam(team); err != nil {
		t.Fatalf("create team: %v", err)
	}

	got, err := s.GetTeam("test-ws/agents")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	if len(got.Roles) != 1 {
		t.Fatalf("expected 1 role, got %d", len(got.Roles))
	}
	if got.Roles[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", got.Roles[0].Replicas)
	}

	teams := s.ListTeams()
	if len(teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(teams))
	}

	if err := s.DeleteTeam("test-ws/agents"); err != nil {
		t.Fatalf("delete team: %v", err)
	}
}

func TestEndpointCRUD(t *testing.T) {
	t.Parallel()
	s := NewStore()

	ep := &Endpoint{Name: "agent-svc", Workspace: "test-ws", Team: "agents"}

	if err := s.CreateEndpoint(ep); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}

	got, err := s.GetEndpoint("test-ws/agent-svc")
	if err != nil {
		t.Fatalf("get endpoint: %v", err)
	}
	if got.Team != "agents" {
		t.Fatalf("expected agents team, got %s", got.Team)
	}

	if err := s.DeleteEndpoint("test-ws/agent-svc"); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
}

func TestUpdateSessionHeartbeat(t *testing.T) {
	t.Parallel()
	s := NewStore()

	sess := &Session{
		Name:      "agent-0",
		Workspace: "test-ws",
		Team:      "agents",
		Role:      "worker",
		Runtime:   Runtime{Name: "simulator", Command: "simulator"},
		State:     SessionRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := s.UpdateSessionHeartbeat("test-ws/agent-0", "", 42.5, ""); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession("test-ws/agent-0")
	if got.ContextPercent != 42.5 {
		t.Fatalf("expected context 42.5%%, got %.1f%%", got.ContextPercent)
	}
	if got.LastHeartbeat.IsZero() {
		t.Fatal("expected non-zero heartbeat timestamp")
	}

	// Not found case
	if _, err := s.UpdateSessionHeartbeat("test-ws/nonexistent", "", 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListSessionsByTeamRole(t *testing.T) {
	t.Parallel()
	s := NewStore()

	// Create sessions with different roles
	for _, sess := range []*Session{
		{Name: "s-worker-0", Workspace: "ws", Team: "squad", Role: "worker", Runtime: Runtime{Command: "bash"}},
		{Name: "s-worker-1", Workspace: "ws", Team: "squad", Role: "worker", Runtime: Runtime{Command: "bash"}},
		{Name: "s-supervisor-0", Workspace: "ws", Team: "squad", Role: "supervisor", Runtime: Runtime{Command: "bash"}},
		{Name: "s-other-0", Workspace: "ws", Team: "other", Role: "worker", Runtime: Runtime{Command: "bash"}},
	} {
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("create session %s: %v", sess.Name, err)
		}
	}

	workers := s.ListSessionsByTeamRole("ws", "squad", "worker")
	if len(workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(workers))
	}

	supervisors := s.ListSessionsByTeamRole("ws", "squad", "supervisor")
	if len(supervisors) != 1 {
		t.Fatalf("expected 1 supervisor, got %d", len(supervisors))
	}

	// Team-level list still returns all
	all := s.ListSessionsByTeam("ws", "squad")
	if len(all) != 3 {
		t.Fatalf("expected 3 squad sessions, got %d", len(all))
	}
}

func TestListSessionsByTeamRoleGeneration(t *testing.T) {
	t.Parallel()
	s := NewStore()

	for _, sess := range []*Session{
		{Name: "s-g1-0", Workspace: "ws", Team: "squad", Role: "worker", Generation: 1, Runtime: Runtime{Command: "bash"}},
		{Name: "s-g1-1", Workspace: "ws", Team: "squad", Role: "worker", Generation: 1, Runtime: Runtime{Command: "bash"}},
		{Name: "s-g2-0", Workspace: "ws", Team: "squad", Role: "worker", Generation: 2, Runtime: Runtime{Command: "bash"}},
	} {
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("create session %s: %v", sess.Name, err)
		}
	}

	gen1 := s.ListSessionsByTeamRoleGeneration("ws", "squad", "worker", 1)
	if len(gen1) != 2 {
		t.Fatalf("expected 2 gen-1 workers, got %d", len(gen1))
	}

	gen2 := s.ListSessionsByTeamRoleGeneration("ws", "squad", "worker", 2)
	if len(gen2) != 1 {
		t.Fatalf("expected 1 gen-2 worker, got %d", len(gen2))
	}

	// All generations via role query
	all := s.ListSessionsByTeamRole("ws", "squad", "worker")
	if len(all) != 3 {
		t.Fatalf("expected 3 total workers, got %d", len(all))
	}
}

func TestUpdateSessionMetrics(t *testing.T) {
	t.Parallel()
	s := NewStore()

	sess := &Session{
		Name:      "agent-0",
		Workspace: "test-ws",
		Team:      "agents",
		Role:      "worker",
		Runtime:   Runtime{Name: "forestage", Command: "forestage"},
		State:     SessionRunning,
		PID:       4242,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	before, _ := s.GetSession("test-ws/agent-0")
	if !before.MetricsAt.IsZero() {
		t.Fatal("a session must start with no sampler reading so callers can render absence")
	}

	s.UpdateSessionMetrics("test-ws/agent-0", SessionMetrics{
		CPUPercent:   17.5,
		RSSBytes:     512 << 20,
		IOReadBytes:  2048,
		IOWriteBytes: 1024,
		IOAvailable:  true,
	})

	got, _ := s.GetSession("test-ws/agent-0")
	if got.CPUPercent != 17.5 {
		t.Errorf("CPUPercent = %v, want 17.5", got.CPUPercent)
	}
	if got.RSSBytes != 512<<20 {
		t.Errorf("RSSBytes = %d, want %d", got.RSSBytes, int64(512<<20))
	}
	if !got.IOAvailable || got.IOReadBytes != 2048 || got.IOWriteBytes != 1024 {
		t.Errorf("IO = %+v, want 2048 read / 1024 write, available", got.SessionMetrics)
	}
	if got.MetricsAt.IsZero() {
		t.Error("MetricsAt is zero after a write; the CLI cannot tell measured from unmeasured")
	}
	// Fields the sampler does not own must survive the write.
	if got.PID != 4242 || got.State != SessionRunning {
		t.Errorf("metrics write disturbed PID/State: %+v", got)
	}

	// A session deleted between the sampler's snapshot and its write is
	// not an error worth reporting.
	s.UpdateSessionMetrics("test-ws/nonexistent", SessionMetrics{CPUPercent: 1})
}

func TestUpdateSessionContext(t *testing.T) {
	t.Parallel()
	s := NewStore()

	sess := &Session{
		Name:      "agent-0",
		Workspace: "test-ws",
		Team:      "agents",
		Role:      "worker",
		Runtime:   Runtime{Name: "claude", Command: "claude"},
		State:     SessionRunning,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	s.UpdateSessionContext("test-ws/agent-0", SessionContext{
		ContextTokens:      34136,
		ContextLimit:       1_000_000,
		ContextPercent:     3.4136,
		ContextLimitSource: "stream",
		ContextModel:       "claude-fable-5[1m]",
		ContextRequests:    3,
	})

	got, err := s.GetSession("test-ws/agent-0")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.ContextTokens != 34136 || got.ContextLimit != 1_000_000 {
		t.Errorf("context = %d/%d tokens/limit, want 34136/1000000", got.ContextTokens, got.ContextLimit)
	}
	if got.ContextAt.IsZero() {
		t.Error("ContextAt not stamped; the renderer keys absence on it")
	}
	// LastHeartbeat is a liveness contract consumed by the heartbeat health
	// check and by shift readiness. A context reading is not a heartbeat.
	if !got.LastHeartbeat.IsZero() {
		t.Error("a context reading stamped LastHeartbeat, silently redefining liveness as stream activity")
	}

	// A session deleted between the accountant's snapshot and its write is
	// ignored, not reported: the reading is meaningless by then.
	s.UpdateSessionContext("test-ws/nonexistent", SessionContext{ContextTokens: 1})
}

func TestUpdateSessionHeartbeatStampsContextAt(t *testing.T) {
	t.Parallel()
	s := NewStore()
	sess := &Session{
		Name: "agent-0", Workspace: "test-ws", Team: "agents", Role: "worker",
		Runtime: Runtime{Name: "simulator", Command: "simulator"},
		State:   SessionRunning, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.UpdateSessionHeartbeat("test-ws/agent-0", "", 42.5, ""); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}
	got, _ := s.GetSession("test-ws/agent-0")
	// The cooperative heartbeat is still a producer, and the renderer keys
	// on one sentinel, so the simulator must keep lighting the column.
	if got.ContextAt.IsZero() {
		t.Error("a heartbeat did not stamp ContextAt, so CTX% would render absent")
	}
	if got.LastHeartbeat.IsZero() {
		t.Error("a heartbeat must still stamp LastHeartbeat")
	}
	if got.ContextPercent != 42.5 {
		t.Errorf("context percent = %v, want 42.5", got.ContextPercent)
	}
	// The shape this path produces, pinned because two consumers branch on
	// it: the renderer prints the percentage rather than an unresolved
	// window, and bolt rehydrate keeps the reading rather than dropping it.
	// Both key on ContextRequests, which only the accountant writes.
	if got.ContextLimit != 0 || got.ContextTokens != 0 || got.ContextRequests != 0 {
		t.Errorf("a heartbeat invented a window, a token count, or a request count: %+v", got.SessionContext)
	}
}

// TestCloneTeamCopiesBudget pins the store's snapshot contract for the two
// fields aae-orc-qiay added. Both are flat value structs, so cloneTeam's
// `out := *t` already deep-copies them — this test is what will fail loudly
// if either later grows a slice, map, or pointer and nobody extends the
// clone. See go.md rule 12 and orc finding-032.
func TestCloneTeamCopiesBudget(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if err := s.CreateWorkspace(&Workspace{Name: "fanout", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := s.CreateTeam(&Team{
		Name:      "crew",
		Workspace: "fanout",
		Roles:     []Role{{Name: "crew", Replicas: 3, Runtime: Runtime{Command: "sleep"}}},
		Budget:    Budget{MaxSessions: 6, MaxTokens: 2_000_000},
		Admission: AdmissionState{Held: true, Role: "crew", Reason: "refused"},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	snap, err := s.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team: %v", err)
	}
	snap.Budget.MaxSessions = 999
	snap.Budget.OnUnmeasured = UnmeasuredRefuse
	snap.Admission = AdmissionState{}

	live, err := s.GetTeam("fanout/crew")
	if err != nil {
		t.Fatalf("get team again: %v", err)
	}
	if live.Budget.MaxSessions != 6 || live.Budget.OnUnmeasured != "" {
		t.Errorf("mutating a snapshot changed store state: %+v", live.Budget)
	}
	if !live.Admission.Held || live.Admission.Role != "crew" {
		t.Errorf("mutating a snapshot cleared the store's admission state: %+v", live.Admission)
	}
}

// TestHeartbeatAfterUnresolvedAccountantReadingIsNotSuppressed pins the
// interaction between the two CTX% producers on ONE session.
//
// The accountant stamps ContextRequests on every reading and may have no
// window (codex carries no in-stream contextWindow, so an unresolved
// window is its expected state, per finding-007). The cooperative
// heartbeat stamps a percentage the agent computed itself, with no window
// and no request count. Neither producer clears the other's fields.
//
// The renderer in cmd/marvel keys "?" on ContextRequests > 0 when
// ContextLimit == 0. So once the accountant has touched a session with an
// unresolved window, a later heartbeat carrying a REAL percentage is
// still rendered "?" — absence printed over a measurement that exists.
// That inverts what "?" is supposed to mean: nobody knows, not "the
// accountant asked first".
//
// Reachable in one manifest line: nothing gates context_feed on runtime
// mode. SupportsStream requires Mode == headless (claude.go:19) while the
// feed only checks Runtime.ContextFeed (session/projection.go:126), and
// manifest validation checks the feed's VALUE but never its mode
// (api/manifest.go:282). A claude role declaring both gets both
// producers. See aae-orc-ibu9.
func TestHeartbeatAfterUnresolvedAccountantReadingIsNotSuppressed(t *testing.T) {
	t.Parallel()
	s := NewStore()
	sess := &Session{
		Name: "agent-0", Workspace: "test-ws", Team: "agents", Role: "worker",
		Runtime: Runtime{
			Name: "claude", Command: "claude",
			Mode: RuntimeModeHeadless, ContextFeed: ContextFeedStatusline,
		},
		State: SessionRunning, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// The accountant reads, but cannot resolve a window.
	s.UpdateSessionContext("test-ws/agent-0", SessionContext{
		ContextTokens:   120000,
		ContextRequests: 3,
		ContextLimit:    0,
	})

	// The agent then reports a percentage it computed itself.
	if _, err := s.UpdateSessionHeartbeat("test-ws/agent-0", "", 61.0, "claude-opus-5"); err != nil {
		t.Fatalf("update heartbeat: %v", err)
	}

	got, _ := s.GetSession("test-ws/agent-0")
	if got.ContextPercent != 61.0 {
		t.Fatalf("heartbeat percentage = %v, want 61", got.ContextPercent)
	}
	if got.ContextSource != ContextSourceHeartbeat {
		t.Errorf("ContextSource = %q, want %q: the producer must be declared, "+
			"not inferred from which fields are populated",
			got.ContextSource, ContextSourceHeartbeat)
	}
	// The heartbeat replaces the record rather than layering over it, so
	// no accountant leftovers remain to be misread as provenance.
	if got.ContextRequests != 0 {
		t.Errorf("ContextRequests = %d after a heartbeat, want 0: a stale "+
			"request count is what made the renderer print \"?\" over a real "+
			"cooperative reading", got.ContextRequests)
	}
	if got.ContextTokens != 0 || got.ContextLimit != 0 {
		t.Errorf("stream fields survived a heartbeat: tokens=%d limit=%d, want 0/0",
			got.ContextTokens, got.ContextLimit)
	}
}

// TestAccountantReadingDeclaresItsProducer is the other half: the
// accountant must stamp its own provenance, or the renderer cannot tell
// an unresolved window (render "?") from a cooperative percentage
// (render the number).
func TestAccountantReadingDeclaresItsProducer(t *testing.T) {
	t.Parallel()
	s := NewStore()
	sess := &Session{
		Name: "agent-0", Workspace: "test-ws", Team: "agents", Role: "worker",
		Runtime: Runtime{Name: "codex", Command: "codex", Mode: RuntimeModeHeadless},
		State:   SessionRunning, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	s.UpdateSessionContext("test-ws/agent-0", SessionContext{
		ContextSource:   ContextSourceAccountant,
		ContextTokens:   120000,
		ContextRequests: 3,
	})
	got, _ := s.GetSession("test-ws/agent-0")
	if got.ContextSource != ContextSourceAccountant {
		t.Errorf("ContextSource = %q, want %q", got.ContextSource, ContextSourceAccountant)
	}
}
