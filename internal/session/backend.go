package session

import (
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
	switch api.Backend(backend) {
	case "", api.BackendDefaultName:
		return false
	default:
		return true
	}
}

// backendOverlayPath is the deterministic overlay file for a session, so the
// writer and the sweep agree without storing the path on the session.
func (m *Manager) backendOverlayPath(sessionKey string) string {
	name := strings.ReplaceAll(sessionKey, "/", "-") + ".json"
	return filepath.Join(m.BackendOverlayDir, name)
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
	ov := api.BackendOverlay{
		Mode:                api.Backend(sess.Runtime.Backend),
		WorkspaceID:         sess.Runtime.Env[backendWorkspaceIDKey],
		APIKeyHelper:        sess.Runtime.Env[backendAPIKeyHelperKey],
		AWSCredentialExport: sess.Runtime.Env[backendAWSCredExportKey],
		Extra:               backendExtraEnv(sess.Runtime.Env),
	}
	settings, err := api.BuildBackendOverlay(ov)
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

	// Return the env block the harness will actually see through --settings, so
	// the caller classifies the effective backend, not the pane env alone.
	if raw, ok := settings["env"].(map[string]string); ok {
		return raw, nil
	}
	return nil, nil
}

// backendExtraEnv is the role's declared env minus the reserved settings-pointer
// keys and the selector/api-key vars the builder owns, so operator passthrough
// (AWS_PROFILE, an OTEL attribute) reaches the overlay while the builder stays
// authoritative over the selector block.
func backendExtraEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range env {
		if backendReserved(k) || api.BackendBuilderOwnedEnv(k) {
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

// sweepBackendOverlay removes a session's overlay on delete (design: ephemeral,
// swept with the session). Best-effort: a missing file is not an error, and a
// failure is logged rather than blocking teardown.
func (m *Manager) sweepBackendOverlay(sessionKey string) {
	if m.BackendOverlayDir == "" {
		return
	}
	path := m.backendOverlayPath(sessionKey)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: sweep backend overlay %s: %v", path, err)
	}
}
