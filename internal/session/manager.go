// Package session manages session lifecycle — creating and destroying
// sessions by coordinating the API store, tmux driver, and runtime adapters.
package session

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/runtime"
	rtevents "github.com/arcavenae/marvel/internal/runtime/events"
	"github.com/arcavenae/marvel/internal/tmux"
	"github.com/arcavenae/marvel/internal/usage"
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
	// StreamDir holds the per-session FIFOs marvel reads agent output
	// from. Defaults to a per-layout temp directory (see daemonTempDir).
	// The pipes carry no content at rest, so nothing here needs to survive
	// a restart; NewFIFO replaces a leftover pipe rather than failing on it.
	StreamDir string
	// ProjectionDir holds the per-session projected policy settings files
	// (finding-024's contract half). Defaults to a per-layout temp
	// directory (see daemonTempDir). The path must survive a restart even
	// though the content need not: an adopted agent goes on reading the
	// file it was launched with, and re-projection has to land on that
	// same path to reach it.
	ProjectionDir string
	// Usage receives adapter events for token and context accounting.
	// Nil is safe, matching Events.
	Usage UsageObserver

	// instances tracks the live Instance per session key. Sessions
	// adopted from a previous daemon have no entry: their pane predates
	// this process, so marvel supervises them without observing them.
	// drains holds the usage tap for the same keys.
	imu       sync.Mutex
	instances map[string]*runtime.TmuxInstance
	drains    map[string]*usageDrain
}

// NewManager creates a session manager with the default runtime adapter registry.
func NewManager(store *api.Store, driver *tmux.Driver) *Manager {
	return &Manager{
		store:         store,
		driver:        driver,
		adapters:      runtime.NewRegistry(),
		StreamDir:     defaultStreamDir(),
		ProjectionDir: defaultProjectionDir(),
		instances:     make(map[string]*runtime.TmuxInstance),
		drains:        make(map[string]*usageDrain),
	}
}

// defaultStreamDir keeps one daemon's pipes away from another's.
func defaultStreamDir() string {
	return daemonTempDir("marvel-streams")
}

// defaultProjectionDir keeps one daemon's projected settings files away
// from another's.
func defaultProjectionDir() string {
	return daemonTempDir("marvel-policies")
}

// daemonTempDir names a daemon-owned scratch directory that is unique per
// HOME and stable across restarts.
//
// Both properties are load-bearing, and the process id supplied only the
// first. A session outlives the daemon that spawned it: `marvel stop`
// leaves agents running and a restarted daemon adopts them, but an adopted
// agent keeps reading the settings file whose path it was launched with,
// and marvel records that path nowhere. Under a pid-keyed directory the
// new daemon computed a different path, so Reproject rewrote a file no
// agent was reading and a policy edit appeared to apply while changing
// nothing. The same keying orphaned a directory per daemon lifetime with
// nothing to remove them: 15 projection and 17 stream directories had
// accumulated in the development machine's temp directory when this was
// measured.
//
// Layout.Tag preserves the isolation the pid provided, because concurrent
// daemons are separated by HOME here: the control socket and the tmux
// server name already draw the boundary in the same place.
func daemonTempDir(prefix string) string {
	layout, err := paths.Default()
	if err != nil {
		// A per-process directory is worse than a per-layout one, but it
		// is better than refusing to build a manager over an unresolvable
		// home directory.
		return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", prefix, os.Getpid()))
	}
	return filepath.Join(os.TempDir(), prefix+"-"+layout.Tag())
}

// marvelSessionPrefix is the tmux session name prefix marvel owns.
// Every tmux session named marvel-* is considered marvel-managed; a
// fresh daemon reclaims the prefix by killing any leftovers on startup.
const marvelSessionPrefix = "marvel-"

// tmuxSessionName returns the tmux session name for a workspace.
func tmuxSessionName(workspace string) string {
	return marvelSessionPrefix + workspace
}

// AdoptOrKill reconciles marvel-* tmux state against the recorded
// intent in the store. Called at daemon startup. For each marvel-*
// tmux session:
//
//   - If the workspace is NOT in the store, the whole tmux session is
//     killed (the pre-L2 clean-slate behavior for unrecorded workspaces).
//   - If the workspace IS in the store, panes are examined individually:
//     panes whose PaneID matches a recorded session are adopted (left
//     alive, no change to the recorded session). Panes that don't match
//     are killed.
//
// When the store has no records (in-memory-only mode, no OpenBolt
// call), no workspaces are recorded, so AdoptOrKill degenerates to
// kill-all — preserving the pre-L2 contract C12 behavior for the
// no-persistence path. This is the safety net: marvel still works
// without bolt, with the same restart semantics it had before
// Session 2 of aae-orc-k4e4 landed.
//
// The architectural pivot from finding-048 lands here: with L2 in
// use, recorded intent survives daemon restart and AdoptOrKill
// reconciles to it instead of destroying it.
//
// Returns counts of adopted and killed entities (sessions or panes)
// for observability. Logs at info on activity, errors per-entity
// without aborting the pass.
//
// See orc finding-050 (this session) and aae-orc-72u (the empirical
// orphan-pane bug that motivated the original kill-all behavior).
func (m *Manager) AdoptOrKill() (adopted, killed int, err error) {
	return m.reconcilePrefix(marvelSessionPrefix, KillUnrecorded)
}

// AdoptOrLeave adopts panes matching recorded intent and LEAVES
// everything else running, reporting it rather than destroying it.
//
// This is the default posture, ratified 2026-08-07: err on silent
// accumulation, not silent destruction. An orphaned pane costs memory
// and clutter and can be reclaimed whenever an operator notices.
// Destroyed work cannot be recovered at all.
//
// The behavior this replaced killed any marvel-* session not in the
// starting daemon's records, which destroyed a second daemon's entire
// running fleet on an ordinary action with no confirmation. Measured
// 2026-08-06, independent of the socket and independent of the starting
// daemon's own state. See aae-orc-kvcs and
// docs/design/daemon-isolation.md decision 5.
//
// Reclaiming is still available, deliberately: `marvel daemon --reclaim`
// and the reaper verb.
func (m *Manager) AdoptOrLeave() (adopted, left int, err error) {
	return m.reconcilePrefix(marvelSessionPrefix, LeaveUnrecorded)
}

// UnrecordedPolicy is what a reconcile pass does with marvel-* tmux
// state that is not in this daemon's records.
type UnrecordedPolicy int

const (
	// LeaveUnrecorded reports unrecorded state and leaves it running.
	// The default.
	LeaveUnrecorded UnrecordedPolicy = iota
	// KillUnrecorded destroys it. Reachable only through an explicit
	// operator act: `marvel daemon --reclaim` or the reaper.
	KillUnrecorded
)

// adoptOrKillPrefix preserves the kill-policy entry point for tests that
// assert the reclaim behavior against a unique prefix.
func (m *Manager) adoptOrKillPrefix(prefix string) (adopted, killed int, err error) {
	return m.reconcilePrefix(prefix, KillUnrecorded)
}

// reconcilePrefix is the prefix-parameterized core. Tests use a unique
// prefix so they don't collide with other tmux-using tests running in
// parallel packages.
func (m *Manager) reconcilePrefix(prefix string, policy UnrecordedPolicy) (adopted, acted int, err error) {
	names, err := m.driver.ListSessions()
	if err != nil {
		return 0, 0, fmt.Errorf("list tmux sessions: %w", err)
	}

	// Index recorded sessions by PaneID for O(1) lookup. Sessions whose
	// PaneID is empty (Pending, Crashed, or Failed) cannot be adopted
	// — there's nothing to match against — so they're skipped here.
	recordedByPane := make(map[string]string)
	for _, sess := range m.store.ListSessions() {
		if sess.PaneID != "" {
			recordedByPane[sess.PaneID] = sess.Key()
		}
	}

	actor := m.actorID()

	// The log lines name the policy, because the two passes differ only
	// in what they do to state they do not recognise, and a reader
	// diagnosing a missing fleet needs to know which one ran.
	verb, pastTense := "AdoptOrLeave", "left running"
	if policy == KillUnrecorded {
		verb, pastTense = "AdoptOrKill", "killed"
	}

	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		workspace := strings.TrimPrefix(name, prefix)

		// Workspace not recorded. Under the default policy this is
		// somebody else's, or our own leftovers, and either way it keeps
		// running and gets reported.
		if _, gerr := m.store.GetWorkspace(workspace); gerr != nil {
			if policy == LeaveUnrecorded {
				acted++
				log.Printf("%s[%s]: left tmux session %s running: workspace not in this daemon's records",
					verb, actor, name)
				events.Emit(m.Events, events.Event{
					Kind:      events.KindReconcileLeft,
					Severity:  events.SeverityWarning,
					Workspace: workspace,
					Actor:     actor,
					Message: fmt.Sprintf(
						"left tmux session %s running: workspace not in this daemon's records. "+
							"Reclaim with `marvel reap` if it is stale", name),
				})
				continue
			}
			if kerr := m.driver.KillSession(name); kerr != nil {
				log.Printf("%s[%s]: kill unrecorded session %s: %v", verb, actor, name, kerr)
				continue
			}
			acted++
			log.Printf("%s[%s]: killed unrecorded workspace tmux session %s", verb, actor, name)
			events.Emit(m.Events, events.Event{
				Kind:      events.KindReconcileKilled,
				Severity:  events.SeverityWarning,
				Workspace: workspace,
				Actor:     actor,
				Message: fmt.Sprintf(
					"killed tmux session %s: workspace not in this daemon's records", name),
			})
			continue
		}

		// Workspace recorded → check each pane against recorded intent.
		panes, lerr := m.driver.ListPanes(name)
		if lerr != nil {
			log.Printf("AdoptOrKill: list panes %s: %v", name, lerr)
			continue
		}
		for _, p := range panes {
			// The store is checked FIRST and without reference to the
			// Created marker. A recorded pane is ours by record, and
			// panes made by builds before the marker existed carry none:
			// fencing adoption on it would make the first restart after
			// an upgrade fail to adopt its own agents, which is the one
			// property this whole path exists to preserve.
			if sessKey, ok := recordedByPane[p.ID]; ok {
				adopted++
				m.recordPanePID(sessKey, p.PID)
				log.Printf("%s[%s]: adopted pane %s (session %s)", verb, actor, p.ID, sessKey)
				events.Emit(m.Events, events.Event{
					Kind:      events.KindReconcileAdopted,
					Workspace: workspace,
					Session:   sessKey,
					Actor:     actor,
					Message:   fmt.Sprintf("adopted pane %s", p.ID),
				})
				continue
			}

			// Unrecorded. Only panes marvel created are candidates for
			// anything past this point. tmux makes one base shell pane
			// per session and an operator can open more by hand; neither
			// is in the store, so before this fence both were reported
			// here and destroyed by the kill policy (#129). Skipped
			// silently rather than reported: a healthy fleet's own base
			// pane is not news, and reporting it is what left reap
			// unable to ever say clean.
			if !p.Created {
				continue
			}

			if policy == LeaveUnrecorded {
				acted++
				log.Printf("%s[%s]: left pane %s running in workspace %s: not in this daemon's records",
					verb, actor, p.ID, workspace)
				events.Emit(m.Events, events.Event{
					Kind:      events.KindReconcileLeft,
					Severity:  events.SeverityWarning,
					Workspace: workspace,
					Actor:     actor,
					Message: fmt.Sprintf(
						"left pane %s running: not in this daemon's records. "+
							"Reclaim with `marvel reap` if it is stale", p.ID),
				})
				continue
			}
			if kerr := m.driver.KillPane(p.ID); kerr != nil {
				log.Printf("%s[%s]: kill unrecorded pane %s: %v", verb, actor, p.ID, kerr)
				continue
			}
			acted++
			log.Printf("%s[%s]: killed unrecorded pane %s in workspace %s", verb, actor, p.ID, workspace)
			events.Emit(m.Events, events.Event{
				Kind:      events.KindReconcileKilled,
				Severity:  events.SeverityWarning,
				Workspace: workspace,
				Actor:     actor,
				Message: fmt.Sprintf(
					"killed pane %s: not in this daemon's records", p.ID),
			})
		}
	}

	if adopted > 0 || acted > 0 {
		log.Printf("%s[%s]: %d adopted, %d %s", verb, actor, adopted, acted, pastTense)
	}
	return adopted, acted, nil
}

// UnrecordedTmuxState returns the marvel-* tmux entities this daemon
// does not have records for: the ones AdoptOrLeave leaves running and
// AdoptOrKill would destroy.
//
// It exists so `marvel reap` can show an operator what they are about to
// lose before they lose it. Read-only, and deliberately computed the
// same way the reconcile pass computes it, so the preview and the action
// cannot disagree.
func (m *Manager) UnrecordedTmuxState() ([]string, error) {
	names, err := m.driver.ListSessions()
	if err != nil {
		return nil, fmt.Errorf("list tmux sessions: %w", err)
	}

	recordedByPane := make(map[string]string)
	for _, sess := range m.store.ListSessions() {
		if sess.PaneID != "" {
			recordedByPane[sess.PaneID] = sess.Key()
		}
	}

	var found []string
	for _, name := range names {
		if !strings.HasPrefix(name, marvelSessionPrefix) {
			continue
		}
		workspace := strings.TrimPrefix(name, marvelSessionPrefix)
		if _, gerr := m.store.GetWorkspace(workspace); gerr != nil {
			found = append(found, fmt.Sprintf("tmux session %s (whole workspace)", name))
			continue
		}
		panes, lerr := m.driver.ListPanes(name)
		if lerr != nil {
			continue
		}
		for _, p := range panes {
			// Same fence as reconcilePrefix: only panes marvel created
			// can be candidates. This preview must agree with the action
			// exactly, or `marvel reap` lists something `reap --confirm`
			// will not destroy, or worse the reverse.
			if !p.Created {
				continue
			}
			if _, ok := recordedByPane[p.ID]; !ok {
				found = append(found, fmt.Sprintf("pane %s in workspace %s", p.ID, workspace))
			}
		}
	}
	return found, nil
}

// actorID identifies this daemon process in the log lines and events
// AdoptOrKill produces. Two daemons on one host append to the same
// ~/.marvel/log/daemon.log by default, so an unattributed line cannot be
// traced to the process that wrote it, and the daemon whose panes are
// killed records nothing itself. pid distinguishes processes; the socket
// path distinguishes the instances an operator addresses.
//
// SocketPath is set by daemon.Start before AdoptOrKill runs. It is empty
// in tests that drive the Manager directly, which is why the socket half
// is omitted rather than rendered blank.
func (m *Manager) actorID() string {
	if m.SocketPath == "" {
		return fmt.Sprintf("pid=%d", os.Getpid())
	}
	return fmt.Sprintf("pid=%d socket=%s", os.Getpid(), m.SocketPath)
}

// recordPanePID commits an adopted pane's pid to the recorded session.
// ListPanes already carries it; before this it was parsed and dropped,
// which left Session.PID zero for every session marvel adopted across a
// daemon restart and the process sampler with nothing to walk.
func (m *Manager) recordPanePID(sessKey, panePID string) {
	pid, err := strconv.Atoi(strings.TrimSpace(panePID))
	if err != nil || pid <= 0 {
		return
	}
	if err := m.store.UpdateSession(sessKey, func(live *api.Session) error {
		live.PID = pid
		return nil
	}); err != nil {
		log.Printf("AdoptOrKill: record pid %d for %s: %v", pid, sessKey, err)
	}
}

// Create creates a new session: registers it in the store, ensures the tmux
// session exists, and spawns a pane running the runtime command.
func (m *Manager) Create(sess *api.Session) error {
	sess.State = api.SessionPending
	sess.CreatedAt = time.Now().UTC()

	// Mint the heartbeat token before the record exists, so no session is
	// ever in the store without one. The plaintext rides the caller's
	// pointer into planLaunch, where the adapter puts it in the pane's
	// environment; only the digest is stored.
	token, hash, terr := api.NewHeartbeatToken()
	if terr != nil {
		return fmt.Errorf("create session %s: %w", sess.Key(), terr)
	}
	sess.HeartbeatToken = token
	sess.HeartbeatTokenHash = hash

	if err := m.store.CreateSession(sess); err != nil {
		return fmt.Errorf("create session %s: %w", sess.Key(), err)
	}

	tmuxSess := tmuxSessionName(sess.Workspace)
	if err := m.driver.NewSession(tmuxSess); err != nil {
		return fmt.Errorf("ensure tmux session %s: %w", tmuxSess, err)
	}

	plan := m.planLaunch(sess)

	// Log the exact command line we're about to exec so post-hoc
	// debugging has the argv — operators otherwise had to guess what
	// tmux new-window was actually running when a pane died quickly.
	// See ArcavenAE/marvel#9.
	log.Printf("session %s exec: %s", sess.Key(), plan.command)

	inst := runtime.NewTmuxInstance(runtime.TmuxConfig{
		Panes:       m.driver,
		TmuxSession: tmuxSess,
		Title:       sess.Name,
		Command:     plan.command,
		Env:         plan.env,
		Stream:      plan.stream,
	})
	if err := inst.Spawn(context.Background()); err != nil {
		// Clean up store on failure.
		_ = m.store.DeleteSession(sess.Key())
		return fmt.Errorf("create pane for %s: %w", sess.Key(), err)
	}
	paneID := inst.PaneID()
	m.attachInstance(sess, inst)

	// A missing pid costs resource metrics for this session, nothing
	// else, so it is logged and not treated as a failed spawn.
	pid, perr := m.driver.PanePID(paneID)
	if perr != nil {
		log.Printf("session %s: pane pid unavailable: %v", sess.Key(), perr)
	}

	// Commit PaneID + PID + State=Running to the live session under the
	// store lock. Also update the caller's *api.Session so returning
	// pointers stay consistent for any downstream emission/logging that
	// references fields on sess. Per orc finding-032, the Store is the
	// sync boundary.
	if err := m.store.UpdateSession(sess.Key(), func(live *api.Session) error {
		live.PaneID = paneID
		live.PID = pid
		live.State = api.SessionRunning
		return nil
	}); err != nil {
		return fmt.Errorf("update session %s post-create: %w", sess.Key(), err)
	}
	sess.PaneID = paneID
	sess.PID = pid
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

// launchPlan is what one session needs to come up: the shell command,
// its environment, and (when the adapter wired one) the stream marvel
// reads its output from.
type launchPlan struct {
	command string
	env     map[string]string
	stream  *runtime.StreamSource
}

// planLaunch uses the adapter registry when team/role context is
// available, falling back to direct command construction for ad-hoc
// sessions. When the resolved adapter declares it can redirect its
// harness's structured output, planLaunch creates the sink first and
// hands the path down; the adapter reports back whether it used it.
func (m *Manager) planLaunch(sess *api.Session) launchPlan {
	// Look up team and role for full adapter context. Store returns
	// snapshots — taking addresses of these locals is safe because the
	// adapter is read-only and the LaunchContext doesn't outlive this
	// function.
	team, teamErr := m.store.GetTeam(fmt.Sprintf("%s/%s", sess.Workspace, sess.Team))
	if teamErr != nil {
		// Ad-hoc session or team not found — use direct command.
		return m.directPlan(sess)
	}

	var role *api.Role
	for i := range team.Roles {
		if team.Roles[i].Name == sess.Role {
			role = &team.Roles[i]
			break
		}
	}
	if role == nil {
		return m.directPlan(sess)
	}

	ws, wsErr := m.store.GetWorkspace(sess.Workspace)
	if wsErr != nil {
		return m.directPlan(sess)
	}

	adapter := m.adapters.Resolve(sess.Runtime.Name)
	lctx := &runtime.LaunchContext{
		Session:    sess,
		Role:       role,
		Team:       &team,
		Workspace:  &ws,
		SocketPath: m.SocketPath,
	}

	// Project the role's policy (finding-024 contract half) before Prepare
	// so the adapter can point the harness at the settings file. Sets
	// lctx.PolicyProjectionPath when the adapter supports projection and a
	// policy resolves; a no-op otherwise.
	m.projectForLaunch(lctx, adapter)

	// Ask before building: an adapter that never streams (or one asked
	// for an interactive launch) should cost no filesystem work.
	fifo := m.openSink(sess, adapter, lctx)
	if fifo != nil {
		lctx.StreamPath = fifo.Path()
	}

	result, err := adapter.Prepare(lctx)
	if err != nil {
		log.Printf("adapter %s prepare failed for %s, falling back: %v", adapter.Name(), sess.Key(), err)
		discardSink(fifo)
		return m.directPlan(sess)
	}

	plan := launchPlan{command: result.Command, env: result.Env}
	switch {
	case result.Stream != nil && fifo != nil:
		parser, perr := runtime.NewStreamParser(result.Stream.Format, runtime.StreamParserConfig{
			AgentID:   sess.Key(),
			Workspace: sess.Workspace,
		})
		if perr != nil {
			// The adapter named a format marvel cannot read. The session
			// still runs; it just runs unobserved, which is better than
			// refusing to launch over a telemetry gap.
			log.Printf("session %s: %v — launching without stream", sess.Key(), perr)
			discardSink(fifo)
			break
		}
		plan.stream = &runtime.StreamSource{FIFO: fifo, Parser: parser}
		log.Printf("session %s streaming %s via %s", sess.Key(), result.Stream.Format, result.Stream.Path)
	case fifo != nil:
		// Sink offered, adapter declined it.
		discardSink(fifo)
	}

	log.Printf("session %s using %s adapter", sess.Key(), adapter.Name())
	return plan
}

// CanStreamRole reports whether a manifest role would launch a session
// marvel can read a usage stream from. It asks the same registry, and the
// same adapter question, that openSink asks at spawn, so the apply-time
// answer cannot drift from the spawn-time one.
//
// The pre-flight caller (a token budget with no role that could report
// against it) has no session yet, so the LaunchContext carries only the
// resolved runtime. SupportsStream is documented to depend on the runtime
// and the role rather than on the sink, which is what makes that legal.
func (m *Manager) CanStreamRole(r api.ManifestRole) bool {
	rt := api.Runtime{
		Name:    r.Runtime.Image,
		Command: r.Runtime.Command,
		Args:    r.Runtime.Args,
		Script:  r.Runtime.Script,
		Mode:    r.Runtime.Mode,
		Prompt:  r.Runtime.Prompt,
	}
	if rt.Name == "" {
		rt.Name = rt.Command
	}
	streamer, ok := m.adapters.Resolve(rt.Name).(runtime.StreamCapable)
	if !ok {
		return false
	}
	return streamer.SupportsStream(&runtime.LaunchContext{
		Session: &api.Session{Runtime: rt},
		Role:    &api.Role{Name: r.Name, Replicas: r.Replicas, Runtime: rt},
	})
}

// openSink creates the FIFO for a stream-capable adapter, or returns nil.
// A sink that cannot be created is logged and skipped: an unobservable
// session is still a working session.
func (m *Manager) openSink(sess *api.Session, adapter runtime.Adapter, lctx *runtime.LaunchContext) *runtime.FIFO {
	streamer, ok := adapter.(runtime.StreamCapable)
	if !ok || m.StreamDir == "" || !streamer.SupportsStream(lctx) {
		return nil
	}
	fifo, err := runtime.NewFIFO(m.StreamDir, sess.Key())
	if err != nil {
		log.Printf("session %s: no stream sink: %v", sess.Key(), err)
		return nil
	}
	return fifo
}

func discardSink(fifo *runtime.FIFO) {
	if fifo == nil {
		return
	}
	if err := fifo.Remove(); err != nil {
		log.Printf("warning: %v", err)
	}
}

// UsageObserver receives adapter events for token and context
// accounting. Declared here beside its consumer so package session keeps
// no hard dependency on a concrete accountant, and so tests can drive the
// tap with a recorder. Nil is safe, matching Events.
//
// The event ring cannot serve this purpose: bridgeEvent flattens the
// typed payload into a one-line string clipped at 160 bytes, so token
// counts survive only as prose.
type UsageObserver interface {
	Bind(c usage.Coords, b usage.Bind)
	Observe(c usage.Coords, ev rtevents.Event)
	Forget(agentID string)
}

// usageDrain is the tap one session's drain goroutine folds through.
//
// It exists to order Forget AFTER the last fold. The events channel is
// buffered 256 deep on purpose, and stream teardown joins the parser
// goroutine only, so a session deleted mid-stream leaves a tail the drain
// is still consuming. A Forget issued beside the delete would be followed
// by folds that recreate the session's state from nothing: the team's
// retired totals would count it twice, its Partial flag would latch on the
// requestless replacement, and a live CTX% cell could be re-blanked by a
// windowless reading.
type usageDrain struct {
	done chan struct{}

	// mu serializes a fold against the retiring Forget, so the bounded
	// wait can time out on a wedged drain without reopening that window.
	// Held across one Observe only, which its contract bounds to
	// arithmetic plus one non-persisting store write.
	mu      sync.Mutex
	retired bool
}

func (d *usageDrain) observe(u UsageObserver, c usage.Coords, ev rtevents.Event) {
	if u == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.retired {
		return
	}
	u.Observe(c, ev)
}

func (d *usageDrain) retire(u UsageObserver, key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retired = true
	u.Forget(key)
}

// attachInstance records the instance and drains its event stream into
// the ring. The goroutine ends when the harness's output ends, so its
// lifetime is the session's.
//
// The accountant is called inline rather than through a tee:
// TmuxInstance.Events() is a single-sender/single-receiver channel whose
// reader owns and closes it, and fanning it out would add a second
// back-pressure path against a chain that back-pressures the harness on
// purpose. The cost is that Observe sits in that path, which is why its
// contract bounds it to arithmetic plus one non-persisting store write.
func (m *Manager) attachInstance(sess *api.Session, inst *runtime.TmuxInstance) {
	drain := &usageDrain{done: make(chan struct{})}

	m.imu.Lock()
	m.instances[sess.Key()] = inst
	m.drains[sess.Key()] = drain
	m.imu.Unlock()

	c := coords{
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
	}
	uc := usage.Coords{
		AgentID:   sess.Key(),
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
	}
	// Bind before the drain starts so the denominator is resolved from
	// launch args on the first fold, not after a round trip.
	if m.Usage != nil {
		m.Usage.Bind(uc, usage.Bind{
			Harness: sess.Runtime.Name,
			Args:    sess.Runtime.Args,
			Window:  sess.Runtime.ContextWindow,
		})
	}
	go func() {
		defer close(drain.done)
		for ev := range inst.Events() {
			drain.observe(m.Usage, uc, ev)
			events.Emit(m.Events, bridgeEvent(c, ev))
		}
	}()
}

// takeInstance removes and returns the instance for a session key, with
// the usage tap the caller must retire once the stream is down.
func (m *Manager) takeInstance(key string) (*runtime.TmuxInstance, *usageDrain) {
	m.imu.Lock()
	defer m.imu.Unlock()
	inst := m.instances[key]
	drain := m.drains[key]
	delete(m.instances, key)
	delete(m.drains, key)
	return inst, drain
}

// usageDrainGrace bounds the wait for a drain goroutine to finish the
// buffered tail of a stream. The tail is arithmetic over at most one
// channel buffer, and the reconciler is the caller, so this is a wedge
// bound rather than a working budget.
const usageDrainGrace = 2 * time.Second

// forgetUsage retires a session's accounting after its drain goroutine
// has folded the last buffered event. See usageDrain for why the order
// matters. A nil drain is an adopted pane, which never had one.
func (m *Manager) forgetUsage(key string, drain *usageDrain) {
	if m.Usage == nil {
		return
	}
	if drain == nil {
		m.Usage.Forget(key)
		return
	}
	select {
	case <-drain.done:
	case <-time.After(usageDrainGrace):
		log.Printf("warning: session %s: usage drain still running after %v, retiring its accounting anyway", key, usageDrainGrace)
	}
	drain.retire(m.Usage, key)
}

// Instance returns the live instance for a session key, or nil when
// marvel has none (an adopted pane, or a session created before this
// daemon started).
func (m *Manager) Instance(key string) runtime.Instance {
	m.imu.Lock()
	defer m.imu.Unlock()
	inst, ok := m.instances[key]
	if !ok {
		return nil
	}
	return inst
}

// instanceTeardownGrace bounds how long session teardown waits on a
// harness stream that has not noticed it is finished.
const instanceTeardownGrace = 3 * time.Second

// retireInstance kills the instance for a key if marvel holds one, and
// reports whether it did. The pane-kill error is logged rather than
// returned: by the time a session is being retired the pane is often
// already gone, and that is not a failure of the retirement.
func (m *Manager) retireInstance(key string) bool {
	inst, drain := m.takeInstance(key)
	if inst == nil {
		m.forgetUsage(key, drain)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), instanceTeardownGrace)
	defer cancel()
	if err := inst.Kill(ctx); err != nil {
		log.Printf("warning: retire instance %s: %v", key, err)
	}
	m.forgetUsage(key, drain)
	return true
}

// detachInstance retires the stream of a session whose pane already
// vanished. Distinct from retireInstance so the reap path does not log a
// kill-pane failure for a pane it knows is gone.
func (m *Manager) detachInstance(key string) {
	inst, drain := m.takeInstance(key)
	if inst == nil {
		m.forgetUsage(key, drain)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), instanceTeardownGrace)
	defer cancel()
	inst.Detach(ctx)
	m.forgetUsage(key, drain)
}

// directPlan builds the command string directly — the pre-adapter path
// used for ad-hoc sessions or when the adapter can't resolve.
func (m *Manager) directPlan(sess *api.Session) launchPlan {
	cmd, env := m.directCommand(sess)
	return launchPlan{command: cmd, env: env}
}

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
		if sess.HeartbeatToken != "" {
			envs[api.HeartbeatTokenEnv] = sess.HeartbeatToken
		}
	}
	return cmd, envs
}

// Delete destroys a session: kills the tmux pane and removes from the store.
func (m *Manager) Delete(key string) error {
	sess, err := m.store.GetSession(key)
	if err != nil {
		return err
	}

	// Prefer the instance: it kills the pane and retires the stream in
	// one step. Sessions with no instance (adopted panes) fall back to
	// the driver.
	if !m.retireInstance(key) && sess.PaneID != "" {
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
			// The harness is gone; its stream has nothing left to say.
			// Detaching here keeps a crashed session from leaving a pipe
			// and a parked reader behind.
			m.detachInstance(sess.Key())
			m.clearStaleCrashed(sessions, sess.Workspace, sess.Team, sess.Role, sess.Key())
			if err := m.store.UpdateSession(sess.Key(), func(live *api.Session) error {
				live.State = api.SessionCrashed
				live.PaneID = ""
				// The absence of the pane IS the process-alive verdict
				// (the health path defers to this one for that check), so
				// the last reading taken while the pane was alive must
				// not survive the transition. Without this a session
				// killed out of band reported state=crashed alongside
				// health=healthy. See aae-orc-4bz2.
				live.HealthState = api.HealthUnhealthy
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
