package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// The Layer A interim attach (design-backend-swaps.md, BT3): marvel writes a
// per-session Claude Code settings overlay and stamps its path into the pane
// environment as MARVEL_BACKEND_SETTINGS. cast-launch.sh, which owns the claude
// argv on the offline-wrapper path, passes it as `claude --settings`. A plain
// env value is a silent no-op against a settings.json that already selects a
// backend; --settings is the attach that reliably wins (measurement M2).

// MarvelBackendSettingsEnv is the env var carrying the overlay path to the
// launcher. cast-launch.sh reads it and adds --settings when it is set.
const MarvelBackendSettingsEnv = "MARVEL_BACKEND_SETTINGS"

// Reserved role-env keys the overlay writer lifts OUT of the pane environment
// and INTO the settings file as top-level pointers, rather than passing them as
// process env (they are Claude Code settings keys, not env vars). Their values
// are helper-script PATHS whose stdout is the secret, never a literal bearer.
const (
	backendAPIKeyHelperKey  = "MARVEL_BACKEND_API_KEY_HELPER"
	backendAWSCredExportKey = "MARVEL_BACKEND_AWS_CREDENTIAL_EXPORT"
)

// backendWorkspaceIDKey is the non-secret Platform-on-AWS workspace id an
// anthropic-aws role declares in its env; the builder also emits it into the
// overlay, so declaring it once here is enough.
const backendWorkspaceIDKey = "ANTHROPIC_AWS_WORKSPACE_ID"

// backendReserved reports whether a role-env key is lifted to a settings
// pointer rather than carried as a pane env var.
func backendReserved(k string) bool {
	return k == backendAPIKeyHelperKey || k == backendAWSCredExportKey
}

// backendOverlayEnabled reports whether a session's declared backend warrants a
// Layer A overlay. An empty or "default" backend does not: it accepts the
// ambient environment, which is exactly what the shakedown's mode-6 default
// case exercises. Every other mode (including subscription, which must pin
// competitors OFF) gets one.
func backendOverlayEnabled(backend string) bool {
	return api.BackendOverlayWarranted(api.Backend(backend))
}

// backendOverlayPath is the deterministic overlay file for a session, so the
// writer and the sweep agree without storing the path on the session.
func (m *Manager) backendOverlayPath(sessionKey string) string {
	name := strings.ReplaceAll(sessionKey, "/", "-") + ".json"
	return filepath.Join(m.BackendOverlayDir, name)
}

// backendOverlayFor gathers the non-secret overlay inputs a role declared. It
// is the single place that reads the reserved keys out of the role env, so the
// overlay writer and the credential-source classifier cannot drift apart on
// what a role actually asked for.
func backendOverlayFor(sess *api.Session) api.BackendOverlay {
	return api.BackendOverlay{
		Mode:                api.Backend(sess.Runtime.Backend),
		WorkspaceID:         sess.Runtime.Env[backendWorkspaceIDKey],
		APIKeyHelper:        sess.Runtime.Env[backendAPIKeyHelperKey],
		AWSCredentialExport: sess.Runtime.Env[backendAWSCredExportKey],
		Extra:               backendExtraEnv(sess.Runtime.Env),
	}
}

// applyBackendOverlay builds and writes the per-session backend overlay for a
// session whose role declared a non-default backend, stamps its path into
// plan.env, and returns the overlay's env block for the caller to classify the
// effective backend through (Layer A wins over the pane env). A nil env block
// and a nil error mean no overlay was warranted (default/undeclared backend).
// A build or write error is returned so the caller can fail the launch loudly
// rather than run on the wrong backend (the finding-166 shape; BT5 extends the
// gate with the credential and STS checks).
func (m *Manager) applyBackendOverlay(sess *api.Session, plan *launchPlan) (map[string]string, error) {
	if !backendOverlayEnabled(sess.Runtime.Backend) {
		return nil, nil
	}
	settings, err := api.BuildBackendOverlay(backendOverlayFor(sess))
	if err != nil {
		return nil, err
	}
	path := m.backendOverlayPath(sess.Key())
	if _, err := writeProjectionFile(path, settings); err != nil {
		return nil, fmt.Errorf("write backend overlay %s: %w", path, err)
	}
	if plan.env == nil {
		plan.env = map[string]string{}
	}
	plan.env[MarvelBackendSettingsEnv] = path
	// Record it on the session too, so the sweep removes the file this session
	// was actually launched with rather than one recomputed from a directory
	// that may have moved since.
	sess.BackendOverlayPath = path

	// Return the env block the harness will actually see through --settings, so
	// the caller classifies the effective backend, not the pane env alone.
	if raw, ok := settings["env"].(map[string]string); ok {
		return raw, nil
	}
	return nil, nil
}

// backendExtraEnv is the role's declared env minus the reserved settings-pointer
// keys, so operator passthrough (AWS_PROFILE, a service tier, an OTEL
// attribute) reaches the overlay.
//
// It no longer drops the keys the builder owns. Doing so filtered
// ANTHROPIC_API_KEY out before BuildBackendOverlay could refuse it, so the one
// bearer the design names first was the one key silently accepted while the
// AWS trio was refused. The builder now ignores selectors and refuses bearers
// itself, which is where that decision belongs.
func backendExtraEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range env {
		if backendReserved(k) {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// backendClassifyLookup resolves a backend-selecting variable through the layers
// in the order Claude Code applies them: the --settings overlay env wins, then
// the constructed pane env, then marvel's own process environment (which the
// pane inherits). This is the effective environment the harness launches into,
// so it is what BackendResolved must be classified against.
func backendClassifyLookup(overlayEnv, planEnv map[string]string) func(string) string {
	base := backendEnvLookup(planEnv) // pane env, then marvel's process env
	return func(k string) string {
		if v, ok := overlayEnv[k]; ok {
			return v
		}
		return base(k)
	}
}

// VerifyBackend answers, for one session, which backend it is actually on and
// whether every layer agrees (BT7). It runs daemon-side because the overlay
// directory is the manager's own state: a CLI cannot recompute a path that
// MARVEL_BACKEND_OVERLAY_DIR may have moved, and guessing it would report a
// missing overlay that is merely somewhere else.
//
// macOS cannot read a sibling process's environ, so this verifies against
// artifacts marvel controls - the settings file it wrote and the intent/actual
// pair it recorded at spawn - rather than against the live pane.
func (m *Manager) VerifyBackend(sess api.Session) api.BackendVerification {
	in := api.BackendVerifyInput{
		Session:          sess.Key(),
		Intended:         sess.BackendIntended,
		Resolved:         sess.BackendResolved,
		CredentialSource: sess.BackendCredentialSource,
	}
	var readErr error
	if m.BackendOverlayDir != "" {
		in.OverlayPath = m.backendOverlayPath(sess.Key())
		settings, err := readBackendOverlay(in.OverlayPath)
		switch {
		case err == nil:
			in.OverlaySettings = settings
		case errors.Is(err, os.ErrNotExist):
			// Absent. VerifyBackend reports that only when the declared
			// backend warranted an overlay in the first place.
		default:
			readErr = err
		}
	}
	v := api.VerifyBackend(in)
	if readErr != nil {
		// An unreadable overlay is not a pass. The file is what the harness
		// launched with, so being unable to read it means the verdict cannot
		// be vouched for either way.
		v.Problems = append(v.Problems, fmt.Sprintf("cannot read backend overlay %s: %v", in.OverlayPath, readErr))
		v.OK = false
	}
	return v
}

// readBackendOverlay reads a written overlay back as the harness would see it.
// The env block returns as map[string]any here rather than the map[string]string
// the builder produced, which is why ClassifySettingsBackend accepts both.
func readBackendOverlay(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse backend overlay %s: %w", path, err)
	}
	return settings, nil
}

// sweepBackendOverlay removes a session's overlay (design: ephemeral, swept
// with the session). Best-effort: a missing file is not an error, and a failure
// is logged rather than blocking teardown.
//
// It prefers the path recorded at spawn, for the reason the record exists: if
// MARVEL_BACKEND_OVERLAY_DIR moved between launch and delete, a recomputed path
// names a file that was never written, the ErrNotExist is swallowed by the
// best-effort contract, and the real overlay survives the session permanently.
// Read honoured the recorded path before delete did.
func (m *Manager) sweepBackendOverlay(sess api.Session) {
	path := sess.BackendOverlayPath
	if path == "" {
		if m.BackendOverlayDir == "" {
			return
		}
		path = m.backendOverlayPath(sess.Key())
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: sweep backend overlay %s: %v", path, err)
	}
}
