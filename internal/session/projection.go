package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// projectForLaunch resolves the policy a launch's role references and, when
// the adapter exposes a settings surface, writes the projected file and
// points the launch at it via lctx.PolicyProjectionPath. Called from
// planLaunch before Prepare. Projection is best-effort relative to a
// working session: a write failure is logged and the session launches
// unprojected rather than being refused.
func (m *Manager) projectForLaunch(lctx *runtime.LaunchContext, adapter runtime.Adapter) {
	target, wrote, _, err := m.projectPolicy(lctx, adapter)
	if err != nil {
		log.Printf("session %s: policy projection failed, launching unprojected: %v", lctx.Session.Key(), err)
		return
	}
	if !wrote {
		return
	}
	lctx.PolicyProjectionPath = target.Path
	m.emitProjected(lctx.Session, lctx.Role.Policy, target.Path, "projected at spawn")
}

// Reproject rewrites the projected settings file for every live session
// whose role references a policy, and emits a policy.projected event for
// each session whose on-disk contract actually changed. This is the live
// re-projection beat of finding-024: the daemon calls it after applying a
// manifest, an edited policy's new settings land in the running agents'
// files, and Claude Code's file watcher picks them up without a restart.
//
// Returns the number of files rewritten with changed content, for the
// caller's logs.
func (m *Manager) Reproject() int {
	if m.ProjectionDir == "" {
		return 0
	}
	changedCount := 0
	for _, sess := range m.store.ListSessions() {
		if !sess.State.CountsAsAlive() {
			continue
		}
		lctx, adapter, ok := m.reprojectContext(sess)
		if !ok {
			continue
		}
		target, wrote, changed, err := m.projectPolicy(lctx, adapter)
		if err != nil {
			log.Printf("session %s: re-projection failed: %v", sess.Key(), err)
			continue
		}
		if wrote && changed {
			changedCount++
			m.emitProjected(lctx.Session, lctx.Role.Policy, target.Path, "re-projected after manifest change")
		}
	}
	return changedCount
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
// a file was written, and whether the written content differed from what
// was already on disk.
//
// A role with no policy and no context feed yields wrote=false, no error.
// An adapter with no settings surface logs the policy as advisory and
// yields wrote=false. A referenced policy that is not in the store is an
// error (validation should prevent it, so reaching here means drift worth
// surfacing).
//
// Policy content is still written unmodified — marvel never edits a key
// the policy declares. When the runtime opts into context_feed =
// "statusline", marvel ADDS its own statusLine/subagentStatusLine keys,
// and only where the policy does not define them (policy wins). See
// finding-011.
func (m *Manager) projectPolicy(lctx *runtime.LaunchContext, adapter runtime.Adapter) (runtime.ProjectionTarget, bool, bool, error) {
	feed := lctx.Session.Runtime.ContextFeed == api.ContextFeedStatusline
	if m.ProjectionDir == "" || (lctx.Role.Policy == "" && !feed) {
		return runtime.ProjectionTarget{}, false, false, nil
	}

	target := adapter.ProjectionFor(lctx, m.ProjectionDir)
	if !target.Supported {
		log.Printf("session %s: runtime %q has no settings surface; policy %q / context feed are advisory, not projected",
			lctx.Session.Key(), adapter.Name(), lctx.Role.Policy)
		return target, false, false, nil
	}

	settings := map[string]any{}
	if lctx.Role.Policy != "" {
		key := fmt.Sprintf("%s/%s", lctx.Workspace.Name, lctx.Role.Policy)
		policy, err := m.store.GetPolicy(key)
		if err != nil {
			return target, false, false, fmt.Errorf("resolve policy %s: %w", key, err)
		}
		for k, v := range policy.Settings {
			settings[k] = v
		}
	}
	if feed {
		injectStatuslineFeed(settings)
	}

	changed, err := writeProjectionFile(target.Path, settings)
	if err != nil {
		return target, false, false, err
	}
	return target, true, changed, nil
}

// writeProjectionFile renders settings as indented JSON and writes it to
// path, creating the parent directory. It reports whether the new content
// differs from what was already there, so callers can emit an event only on
// a real contract change. Files are 0600: a settings fragment can carry an
// allow/deny list an operator would not want world-readable.
// injectStatuslineFeed adds the statusLine/subagentStatusLine hooks that
// forward the harness's own context figures to the heartbeat RPC, keyed to
// this daemon's binary so the pane needs no PATH assumption. Policy wins:
// a key the settings document already carries is left untouched.
//
// refreshInterval keeps the feed beating while the session idles —
// statusline updates are event-driven and go quiet between prompts, which
// would otherwise starve a heartbeat healthcheck watching this session.
func injectStatuslineFeed(settings map[string]any) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("context feed: cannot resolve marvel binary path, feed not injected: %v", err)
		return
	}
	hook := map[string]any{
		"type":            "command",
		"command":         exe + " ctx-forward",
		"refreshInterval": 15,
	}
	if _, ok := settings["statusLine"]; !ok {
		settings["statusLine"] = hook
	}
	if _, ok := settings["subagentStatusLine"]; !ok {
		settings["subagentStatusLine"] = map[string]any{
			"type":    "command",
			"command": exe + " ctx-forward",
		}
	}
}

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

func (m *Manager) emitProjected(sess *api.Session, policyName, path, detail string) {
	events.Emit(m.Events, events.Event{
		Kind:      events.KindPolicyProjected,
		Workspace: sess.Workspace,
		Team:      sess.Team,
		Role:      sess.Role,
		Session:   sess.Key(),
		Message:   fmt.Sprintf("policy %q %s: %s", policyName, detail, path),
	})
}
