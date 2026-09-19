package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

func newBackendTestSession(backend string, env map[string]string) *api.Session {
	return &api.Session{
		Name:      "squad-worker-g1-0",
		Workspace: "ws",
		Team:      "squad",
		Role:      "worker",
		Runtime:   api.Runtime{Name: "claude", Command: "claude", Backend: backend, Env: env},
	}
}

func TestApplyBackendOverlayWritesAndStamps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Manager{BackendOverlayDir: dir}
	sess := newBackendTestSession("bedrock", nil)
	plan := &launchPlan{env: map[string]string{}}

	overlayEnv, err := m.applyBackendOverlay(sess, plan)
	if err != nil {
		t.Fatalf("applyBackendOverlay: %v", err)
	}
	// The path is stamped for cast-launch.sh.
	path := plan.env[MarvelBackendSettingsEnv]
	if path == "" {
		t.Fatal("MARVEL_BACKEND_SETTINGS not stamped")
	}
	// The file exists and is 0600 (a settings file can carry a secret).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat overlay: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("overlay mode = %o, want 600", perm)
	}
	// The returned env classifies to the intended backend, so the recorded
	// resolved matches the intent.
	if got := api.ResolveBackend(backendClassifyLookup(overlayEnv, plan.env)); got != api.BackendBedrock {
		t.Errorf("resolved = %q, want bedrock", got)
	}
	if !api.BackendMatches(api.Backend(sess.Runtime.Backend), api.ResolveBackend(backendClassifyLookup(overlayEnv, plan.env))) {
		t.Error("intended bedrock should match its own overlay")
	}

	// Sweep removes it.
	m.sweepBackendOverlay(sess.Key())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("overlay not swept: %v", err)
	}
}

func TestApplyBackendOverlayDefaultWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Manager{BackendOverlayDir: dir}

	for _, backend := range []string{"", "default"} {
		sess := newBackendTestSession(backend, nil)
		plan := &launchPlan{env: map[string]string{}}
		overlayEnv, err := m.applyBackendOverlay(sess, plan)
		if err != nil {
			t.Fatalf("backend %q: %v", backend, err)
		}
		if overlayEnv != nil {
			t.Errorf("backend %q: expected no overlay env", backend)
		}
		if _, ok := plan.env[MarvelBackendSettingsEnv]; ok {
			t.Errorf("backend %q: MARVEL_BACKEND_SETTINGS stamped for a default backend", backend)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("backend %q: wrote %d overlay files, want 0", backend, len(entries))
		}
	}
}

func TestApplyBackendOverlayMisconfigFailsLoud(t *testing.T) {
	t.Parallel()
	m := &Manager{BackendOverlayDir: t.TempDir()}
	// anthropic-aws with no workspace id is the mode-2 red: the builder refuses,
	// so the launch fails rather than running misconfigured.
	sess := newBackendTestSession("anthropic-aws", nil)
	plan := &launchPlan{env: map[string]string{}}
	if _, err := m.applyBackendOverlay(sess, plan); err == nil {
		t.Fatal("expected a loud error for anthropic-aws with no workspace id")
	}
}

func TestApplyBackendOverlayExtraAndReservedKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Manager{BackendOverlayDir: dir}
	sess := newBackendTestSession("anthropic-aws", map[string]string{
		backendWorkspaceIDKey:   "ws-123",
		"AWS_PROFILE":           "profile-under-test",
		backendAWSCredExportKey: "/helpers/aws.sh",
		// A selector declared in env must NOT override the builder's block.
		"CLAUDE_CODE_USE_BEDROCK": "1",
	})
	plan := &launchPlan{env: map[string]string{}}
	overlayEnv, err := m.applyBackendOverlay(sess, plan)
	if err != nil {
		t.Fatalf("applyBackendOverlay: %v", err)
	}
	// Reserved pointer key is lifted to a settings key, not carried as env.
	if _, ok := overlayEnv[backendAWSCredExportKey]; ok {
		t.Error("reserved cred-export key leaked into the overlay env block")
	}
	// Operator passthrough survives.
	if overlayEnv["AWS_PROFILE"] != "profile-under-test" {
		t.Errorf("AWS_PROFILE = %q, want passthrough", overlayEnv["AWS_PROFILE"])
	}
	// The builder owns the selector block: a leaked bedrock selector is pinned
	// back OFF, so the effective backend is anthropic-aws, not bedrock.
	if got := api.ResolveBackend(backendClassifyLookup(overlayEnv, plan.env)); got != api.BackendAnthropicAWS {
		t.Errorf("resolved = %q, want anthropic-aws (builder must win over a declared selector)", got)
	}
	// Read the file back to confirm the cred-export pointer landed as a settings
	// key.
	data, err := os.ReadFile(filepath.Join(dir, "ws-squad-worker-g1-0.json"))
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	if !strings.Contains(string(data), "awsCredentialExport") {
		t.Error("overlay missing awsCredentialExport pointer")
	}
}
