package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// projectForLaunch resolves the policy a launch's role references and, when
// the adapter exposes a settings surface, writes the projected file and
// points the launch at it via lctx.PolicyProjectionPath. Called from
// planLaunch before Prepare. Projection is best-effort relative to a
// working session: a write failure is logged and the session launches
// unprojected rather than being refused. The one carve-out is a policy that
// reads a settings_file: its file carries the role's deny rules, so a launch
// without it fails open, and the unreadable file is returned as a
// settingsFileError for the caller to hold the spawn on. Create's
// checkSettingsFile normally holds it first; this is the backstop for a file
// that goes bad between that check and this read.
func (m *Manager) projectForLaunch(lctx *runtime.LaunchContext, adapter runtime.Adapter) error {
	target, wrote, _, shadowed, err := m.projectPolicy(lctx, adapter)
	if err != nil {
		var sfe *settingsFileError
		if errors.As(err, &sfe) {
			return sfe
		}
		log.Printf("session %s: policy projection failed, launching unprojected: %v", lctx.Session.Key(), err)
		return nil
	}
	m.clearSettingsFileFailure(lctx.Session.Key())
	if !wrote {
		return nil
	}
	lctx.PolicyProjectionPath = target.Path
	m.emitProjected(lctx.Session, lctx.Role.Policy, target.Path, "projected at spawn", shadowed)
	return nil
}

// Reproject rewrites the projected settings file for every live session
// whose role references a policy, and emits a policy.projected event for
// each session whose on-disk contract actually changed. The daemon calls it
// after applying a manifest (finding-024).
//
// What a running session picks up is narrower than what is written. Claude
// Code re-reads the statusline and context-feed keys live, but resolves
// permissions at session start, so a changed permissions block reaches the
// agent only when it next spawns (marvel#313). When a rewrite changes
// permissions, the event says so rather than reading as a live change.
//
// Returns the number of files rewritten with changed content, for the
// caller's logs.
func (m *Manager) Reproject() int {
	return m.reproject(false, "re-projected after manifest change")
}

// reproject is Reproject's body. With fileBackedOnly it visits only the
// sessions whose policy reads a settings file, and detail is the event text for
// a file that changed.
func (m *Manager) reproject(fileBackedOnly bool, detail string) int {
	if m.ProjectionDir == "" {
		return 0
	}
	changedCount := 0
	sessions := m.store.ListSessions()
	m.pruneSettingsFileFailures(sessions)
	for _, sess := range sessions {
		if !sess.State.CountsAsAlive() {
			continue
		}
		lctx, adapter, ok := m.reprojectContext(sess)
		if !ok {
			continue
		}
		if fileBackedOnly && !m.policyReadsFile(lctx) {
			continue
		}
		before := projectedPermissions(adapter.ProjectionFor(lctx, m.ProjectionDir).Path)
		target, wrote, changed, shadowed, err := m.projectPolicy(lctx, adapter)
		if err != nil {
			if !m.reportSettingsFileFailure(lctx.Session, err) {
				log.Printf("session %s: re-projection failed: %v", sess.Key(), err)
			}
			continue
		}
		m.clearSettingsFileFailure(sess.Key())
		if wrote && changed {
			changedCount++
			msg := detail
			if !bytes.Equal(before, projectedPermissions(target.Path)) {
				msg += "; permissions take effect on next spawn (a running session keeps the permissions it started with)"
			}
			m.emitProjected(lctx.Session, lctx.Role.Policy, target.Path, msg, shadowed)
		}
	}
	return changedCount
}

// ReprojectFileBacked re-projects the live sessions whose policy reads a
// settings file, and reports how many files changed. The controller calls it
// on each reconcile so a rewritten file is projected without a recast.
func (m *Manager) ReprojectFileBacked() int {
	return m.reproject(true, "re-projected after the settings file changed")
}

// policyReadsFile reports whether the launch's role references a policy that
// names a settings file.
func (m *Manager) policyReadsFile(lctx *runtime.LaunchContext) bool {
	if lctx.Role.Policy == "" {
		return false
	}
	policy, err := m.store.GetPolicy(fmt.Sprintf("%s/%s", lctx.Workspace.Name, lctx.Role.Policy))
	return err == nil && policy.SettingsFile != ""
}

// reprojectContext rebuilds the adapter and launch context for a live
// session from current store state — the same team/role/workspace lookups
// planLaunch does at spawn, minus the stream sink (re-projection touches
// only the settings file, never the pane). Reports false when any resource
// is missing, in which case the session cannot be re-projected.
func (m *Manager) reprojectContext(sess api.Session) (*runtime.LaunchContext, runtime.Adapter, bool) {
	team, err := m.store.GetTeam(fmt.Sprintf("%s/%s", sess.Workspace, sess.Team))
	if err != nil {
		return nil, nil, false
	}
	var role *api.Role
	for i := range team.Roles {
		if team.Roles[i].Name == sess.Role {
			role = &team.Roles[i]
			break
		}
	}
	if role == nil {
		return nil, nil, false
	}
	ws, err := m.store.GetWorkspace(sess.Workspace)
	if err != nil {
		return nil, nil, false
	}
	// GetSession/GetTeam/GetWorkspace return value snapshots; taking
	// addresses of these locals is safe because the LaunchContext is
	// read-only and does not outlive this call chain (finding-032).
	sessCopy := sess
	return &runtime.LaunchContext{
		Session:    &sessCopy,
		Role:       role,
		Team:       &team,
		Workspace:  &ws,
		SocketPath: m.SocketPath,
	}, m.adapters.Resolve(sess.Runtime.Name), true
}

// projectPolicy is the shared spawn/re-projection core. It resolves the
// role's policy, asks the adapter where the file goes, and writes it when
// the adapter has a settings surface. Returns the projection target, whether
// a file was written, whether the written content differed from what was
// already on disk, and the feed keys the policy shadowed (empty unless a
// policy declared a key the context feed also wanted).
//
// A role with no policy and no context feed yields wrote=false, no error.
// An adapter with no settings surface logs the policy as advisory and
// yields wrote=false. A referenced policy that is not in the store is an
// error, and so is a projection path outside the projection directory
// (validation or the adapter contract should prevent both, so reaching
// here means drift worth surfacing).
//
// Policy content is still written unmodified — marvel never edits a key
// the policy declares. When the runtime opts into context_feed =
// "statusline", marvel ADDS the feed keys the adapter renders, and only
// where the policy does not define them (policy wins). A key the policy
// shadows is reported to the caller so the merge is not silent. See
// finding-011 and aae-orc-g698.
func (m *Manager) projectPolicy(lctx *runtime.LaunchContext, adapter runtime.Adapter) (runtime.ProjectionTarget, bool, bool, []string, error) {
	feed := lctx.Session.Runtime.ContextFeed == api.ContextFeedStatusline
	if m.ProjectionDir == "" || (lctx.Role.Policy == "" && !feed) {
		return runtime.ProjectionTarget{}, false, false, nil, nil
	}

	target := adapter.ProjectionFor(lctx, m.ProjectionDir)
	if !target.Supported {
		log.Printf("session %s: runtime %q has no settings surface; policy %q / context feed are advisory, not projected",
			lctx.Session.Key(), adapter.Name(), lctx.Role.Policy)
		return target, false, false, nil, nil
	}
	// The dir is marvel's to write in; anywhere else is the user's. An
	// adapter that answers with a path outside it would have marvel
	// editing a config file it does not own, so refuse rather than write.
	if !withinDir(m.ProjectionDir, target.Path) {
		return target, false, false, nil, fmt.Errorf("runtime %q projection path %s is outside projection dir %s",
			adapter.Name(), target.Path, m.ProjectionDir)
	}

	settings := map[string]any{}
	if lctx.Role.Policy != "" {
		key := fmt.Sprintf("%s/%s", lctx.Workspace.Name, lctx.Role.Policy)
		policy, err := m.store.GetPolicy(key)
		if err != nil {
			return target, false, false, nil, fmt.Errorf("resolve policy %s: %w", key, err)
		}
		if policy.SettingsFile != "" {
			fromFile, err := readSettingsFile(policy.SettingsFile)
			if err != nil {
				return target, false, false, nil, &settingsFileError{policy: lctx.Role.Policy, path: policy.SettingsFile, err: err}
			}
			settings = fromFile
		}
		for k, v := range policy.Settings {
			settings[k] = v
		}
	}
	var shadowed []string
	if feed {
		shadowed = injectStatuslineFeed(settings, adapter, lctx.Session.Key(), lctx.Role.Policy)
	}

	changed, err := writeProjectionFile(target.Path, settings)
	if err != nil {
		return target, false, false, nil, err
	}
	return target, true, changed, shadowed, nil
}

// shellQuote wraps s for a POSIX shell. The statusline value is handed to
// the harness as a "type": "command" string and is run through a shell, so
// an install path containing a space silently breaks the feed on every
// platform — and ctx-forward fails quietly by design, so the symptom is a
// CTX% column that never lights up rather than an error. See aae-orc-d6sc.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// injectStatuslineFeed adds the hooks that forward the harness's own
// context figures to the heartbeat RPC, keyed to this daemon's binary so
// the pane needs no PATH assumption.
//
// The adapter owns the schema. Marvel owns two things only: the command
// the hook runs, and the merge rule. Policy wins, so a key the settings
// document already carries is left untouched.
//
// An adapter that reads a projected settings file without implementing
// StatuslineFeeder gets no feed rather than someone else's keys, and says
// so: an operator who asked for context_feed and got nothing needs a
// reason in the log, not silence.
//
// A key the policy already declares is left to the policy (policy wins),
// and that too is reported rather than silent: it is returned as a shadowed
// key and logged here, because the same operator who asked for context_feed
// and got it only on subagent turns needs the reason in the log, not a feed
// that reads as flakiness (aae-orc-g698). The shadowed keys are returned
// sorted so the caller's event message and the log are deterministic.
func injectStatuslineFeed(settings map[string]any, adapter runtime.Adapter, sessionKey, policyName string) []string {
	feeder, ok := adapter.(runtime.StatuslineFeeder)
	if !ok {
		log.Printf("session %s: runtime %q reads a settings file but declares no context-feed schema; feed not projected",
			sessionKey, adapter.Name())
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("context feed: cannot resolve marvel binary path, feed not injected: %v", err)
		return nil
	}
	var shadowed []string
	for key, value := range feeder.StatuslineFeed(shellQuote(exe) + " ctx-forward") {
		if _, exists := settings[key]; exists {
			shadowed = append(shadowed, key)
			continue
		}
		settings[key] = value
	}
	if len(shadowed) > 0 {
		sort.Strings(shadowed)
		log.Printf("session %s: policy %q shadows context-feed key(s) %s; policy wins, so CTX%% is not fed on those turns",
			sessionKey, policyName, strings.Join(shadowed, ", "))
	}
	return shadowed
}

// projectedPermissions returns the permissions block of the projected
// settings file at path, re-encoded so key order cannot read as a change.
// A missing file or block yields nil, so adding a block counts as a change.
func projectedPermissions(path string) []byte {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	perms, ok := doc["permissions"]
	if !ok {
		return nil
	}
	out, err := json.Marshal(perms)
	if err != nil {
		return nil
	}
	return out
}

// withinDir reports whether path lies inside dir. Used to hold adapters to
// the projection directory they were handed.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// writeProjectionFile renders settings as indented JSON and writes it to
// path, creating the parent directory. It reports whether the new content
// differs from what was already there, so callers can emit an event only on
// a real contract change. Files are 0600: a settings fragment can carry an
// allow/deny list an operator would not want world-readable.
func writeProjectionFile(path string, settings map[string]any) (bool, error) {
	// A nil settings map projects an empty object rather than the JSON
	// literal null, so the harness always reads a well-formed settings file.
	if settings == nil {
		settings = map[string]any{}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal policy settings for %s: %w", path, err)
	}
	data = append(data, '\n')

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(existing, data) {
			return false, nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("read existing projection %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create projection dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, fmt.Errorf("write projection %s: %w", path, err)
	}
	return true, nil
}

// emitProjected records a policy.projected event. When the context feed
// wanted keys the policy already declared, shadowed names them in the
// message: policy-wins is correct, but a half-fed CTX% reads as flakiness,
// so the operator needs the reason on the ring, not just in the log
// (aae-orc-g698). shadowed is expected sorted by the caller.
func (m *Manager) emitProjected(sess *api.Session, policyName, path, detail string, shadowed []string) {
	msg := fmt.Sprintf("policy %q %s: %s", policyName, detail, path)
	if len(shadowed) > 0 {
		msg += fmt.Sprintf("; context feed key(s) %s shadowed by policy (policy wins, CTX%% unfed on those turns)",
			strings.Join(shadowed, ", "))
	}
	events.Emit(m.Events, events.Event{
		Kind:      events.KindPolicyProjected,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
		Message:   msg,
	})
}

// settingsFileError is a policy settings file that could not be read as a JSON
// object. It is its own type so the callers report it as an event, where any
// other projection error is only logged.
type settingsFileError struct {
	policy, path string
	err          error
}

func (e *settingsFileError) Error() string {
	return fmt.Sprintf("policy %q settings file %s: %v", e.policy, e.path, e.err)
}

func (e *settingsFileError) Unwrap() error { return e.err }

// readSettingsFile reads a policy's settings file verbatim: one JSON object,
// numbers kept as written, nothing interpreted. A leading "~/" is the home
// directory. The file is the operator's, so it is read as is; a missing file,
// anything that is not a single JSON object, and an empty file are errors,
// never an empty policy.
func readSettingsFile(path string) (map[string]any, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		path = filepath.Join(home, rest)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("not a JSON object: more than one value")
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return obj, nil
}

// settingsFileFailures remembers the last failure reported for each session, so
// a file that stays unreadable is reported once and not on every reconcile.
type settingsFileFailures struct {
	mu   sync.Mutex
	last map[string]string
}

// reportSettingsFileFailure emits a policy.projection-failed event for err when
// it is a settings file failure and differs from the last one reported for the
// session. It reports whether err was a settings file failure.
func (m *Manager) reportSettingsFileFailure(sess *api.Session, err error) bool {
	var sfe *settingsFileError
	if !errors.As(err, &sfe) {
		return false
	}
	m.emitSettingsFileFailure(sess.Key(), sess,
		fmt.Sprintf("%s; the session keeps whatever settings file it already has", sfe))
	return true
}

// emitSettingsFileFailure emits the event once per change of msg for key.
func (m *Manager) emitSettingsFileFailure(key string, sess *api.Session, msg string) {
	m.failures.mu.Lock()
	if m.failures.last == nil {
		m.failures.last = make(map[string]string)
	}
	seen := m.failures.last[key] == msg
	m.failures.last[key] = msg
	m.failures.mu.Unlock()
	if seen {
		return
	}
	ev := events.Event{
		Kind:      events.KindPolicyProjectionFailed,
		Severity:  events.SeverityError,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Message:   msg,
	}
	if !strings.HasPrefix(key, heldSpawnKeyPrefix) {
		ev.Session = key
	}
	events.Emit(m.Events, ev)
}

// heldSpawnKeyPrefix marks a failure remembered for a spawn that was held, which
// has no session record, so it is keyed by role.
const heldSpawnKeyPrefix = "role:"

func heldSpawnKey(sess *api.Session) string {
	return heldSpawnKeyPrefix + sess.Workspace + "/" + sess.Team + "/" + sess.Role
}

// checkSettingsFile holds the spawn of a role whose policy reads a settings
// file that cannot be read. A launch without the file would drop every deny rule
// the file carries, and the last good content may be stale, so the spawn waits
// and the controller retries it each reconcile. The failure is emitted once. A
// session with no team or role record, and a policy with inline settings, are
// admitted.
func (m *Manager) checkSettingsFile(sess *api.Session) error {
	policy, ok := m.settingsFilePolicy(sess)
	if !ok {
		return nil
	}
	_, err := readSettingsFile(policy.SettingsFile)
	if err == nil {
		m.clearSettingsFileFailure(heldSpawnKey(sess))
		return nil
	}
	sfe := &settingsFileError{policy: policy.Name, path: policy.SettingsFile, err: err}
	return m.holdSpawn(sess, sfe)
}

// holdSpawn reports the failure once for the role and returns the error that
// holds the spawn.
func (m *Manager) holdSpawn(sess *api.Session, sfe *settingsFileError) error {
	m.emitSettingsFileFailure(heldSpawnKey(sess), sess,
		fmt.Sprintf("%s; the spawn is held and retried until the file reads", sfe))
	return fmt.Errorf("hold session %s: %v: %w", sess.Key(), sfe, ErrSettingsFileRefused)
}

// settingsFilePolicy resolves the policy of the role a session is for, when that
// policy names a settings file.
func (m *Manager) settingsFilePolicy(sess *api.Session) (api.Policy, bool) {
	t, err := m.store.GetTeam(sess.Workspace + "/" + sess.Team)
	if err != nil {
		return api.Policy{}, false
	}
	for i := range t.Roles {
		if t.Roles[i].Name != sess.Role || t.Roles[i].Policy == "" {
			continue
		}
		p, err := m.store.GetPolicy(sess.Workspace + "/" + t.Roles[i].Policy)
		if err != nil || p.SettingsFile == "" {
			return api.Policy{}, false
		}
		return p, true
	}
	return api.Policy{}, false
}

// pruneSettingsFileFailures drops the remembered failure of every session that
// is no longer alive, and of every held role whose team or role is gone, so the
// memory does not grow with session churn.
func (m *Manager) pruneSettingsFileFailures(sessions []api.Session) {
	alive := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		if sess.State.CountsAsAlive() {
			alive[sess.Key()] = true
		}
	}
	m.failures.mu.Lock()
	defer m.failures.mu.Unlock()
	for key := range m.failures.last {
		if rest, held := strings.CutPrefix(key, heldSpawnKeyPrefix); held {
			if !m.roleExists(rest) {
				delete(m.failures.last, key)
			}
			continue
		}
		if !alive[key] {
			delete(m.failures.last, key)
		}
	}
}

// roleExists reports whether "<workspace>/<team>/<role>" names a role in the store.
func (m *Manager) roleExists(path string) bool {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return false
	}
	t, err := m.store.GetTeam(path[:i])
	if err != nil {
		return false
	}
	for _, r := range t.Roles {
		if r.Name == path[i+1:] {
			return true
		}
	}
	return false
}

// clearSettingsFileFailure forgets a session's last reported failure, so a
// later failure reports again.
func (m *Manager) clearSettingsFileFailure(key string) {
	m.failures.mu.Lock()
	delete(m.failures.last, key)
	m.failures.mu.Unlock()
}
