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
	m.sweepBackendOverlay(*sess)
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

// The sweep has to remove the file this session was actually launched with.
// Read honoured the recorded path before delete did, so an overlay written
// under one MARVEL_BACKEND_OVERLAY_DIR and swept after the directory moved
// survived the session that owned it, permanently and silently.
func TestSweepBackendOverlayHonoursTheRecordedPath(t *testing.T) {
	t.Parallel()
	launchDir := t.TempDir()
	m := &Manager{BackendOverlayDir: launchDir}
	sess := newBackendTestSession("bedrock", nil)
	plan := &launchPlan{env: map[string]string{}}
	if _, err := m.applyBackendOverlay(sess, plan); err != nil {
		t.Fatalf("applyBackendOverlay: %v", err)
	}
	path := sess.BackendOverlayPath
	if path == "" {
		t.Fatal("overlay path not recorded on the session")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("overlay not written: %v", err)
	}

	// The daemon's overlay directory moves between launch and delete.
	moved := &Manager{BackendOverlayDir: t.TempDir()}
	moved.sweepBackendOverlay(*sess)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("overlay at the recorded path survived the sweep: %v", err)
	}
}

// A session that predates the recorded field still has to be sweepable, so the
// computed path stays as the fallback rather than being replaced.
func TestSweepBackendOverlayFallsBackToTheComputedPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Manager{BackendOverlayDir: dir}
	sess := newBackendTestSession("bedrock", nil)
	plan := &launchPlan{env: map[string]string{}}
	if _, err := m.applyBackendOverlay(sess, plan); err != nil {
		t.Fatalf("applyBackendOverlay: %v", err)
	}
	path := m.backendOverlayPath(sess.Key())
	sess.BackendOverlayPath = "" // a record written before the field existed

	m.sweepBackendOverlay(*sess)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("overlay not swept via the computed fallback: %v", err)
	}
}

// A declared bearer must reach the builder and be refused there. It used to be
// filtered out of the passthrough first, so the one key the design names first
// was the one silently accepted.
func TestApplyBackendOverlayRefusesADeclaredBearer(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"ANTHROPIC_API_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_SECRET_ACCESS_KEY"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			m := &Manager{BackendOverlayDir: t.TempDir()}
			sess := newBackendTestSession("bedrock", map[string]string{key: "a-literal-credential"})
			plan := &launchPlan{env: map[string]string{}}

			_, err := m.applyBackendOverlay(sess, plan)
			if err == nil {
				t.Fatalf("expected a refusal for a declared %s", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name the offending key", err)
			}
			if strings.Contains(err.Error(), "a-literal-credential") {
				t.Errorf("error leaks the credential value: %q", err)
			}
		})
	}
}

// VerifyBackend reads back the overlay the writer just wrote, which is the
// round trip BT7 depends on: written as map[string]string, read as
// map[string]any, and it must classify the same both ways.
func TestVerifyBackendRoundTripsAWrittenOverlay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Manager{BackendOverlayDir: dir}
	sess := newBackendTestSession("bedrock", map[string]string{
		backendAWSCredExportKey: "/opt/aws-creds.sh",
	})
	plan := &launchPlan{env: map[string]string{}}
	if _, err := m.applyBackendOverlay(sess, plan); err != nil {
		t.Fatalf("applyBackendOverlay: %v", err)
	}
	sess.BackendIntended = api.Backend(sess.Runtime.Backend)
	sess.BackendResolved = api.BackendBedrock
	sess.BackendCredentialSource = api.ResolveBackendCredentialSource(backendOverlayFor(sess))

	v := m.VerifyBackend(*sess)
	if !v.OK {
		t.Fatalf("expected a clean verdict, got problems: %v", v.Problems)
	}
	if v.Overlay != api.BackendBedrock {
		t.Errorf("Overlay = %q, want %q", v.Overlay, api.BackendBedrock)
	}
	if !v.OverlayPresent {
		t.Error("OverlayPresent = false, want true")
	}
	if v.Label != "bedrock (IAM session)" {
		t.Errorf("Label = %q, want %q", v.Label, "bedrock (IAM session)")
	}
	if v.OverlayPath != plan.env[MarvelBackendSettingsEnv] {
		t.Errorf("OverlayPath = %q, want the stamped %q", v.OverlayPath, plan.env[MarvelBackendSettingsEnv])
	}
}

// A declared backend whose overlay never landed is the case the verification
// exists to catch: the harness was launched without the --settings that makes
// the backend stick.
func TestVerifyBackendReportsAMissingOverlay(t *testing.T) {
	t.Parallel()
	m := &Manager{BackendOverlayDir: t.TempDir()}
	sess := newBackendTestSession("bedrock", nil)
	sess.BackendIntended = api.BackendBedrock
	sess.BackendResolved = api.BackendBedrock
	sess.BackendCredentialSource = api.BackendCredentialAmbient

	v := m.VerifyBackend(*sess)
	if v.OK {
		t.Fatal("expected a problem for a missing overlay")
	}
	if !strings.Contains(strings.Join(v.Problems, " | "), "no backend overlay at") {
		t.Errorf("problems %v do not name the missing overlay", v.Problems)
	}
}

// An unreadable overlay is not a pass. The file is what the harness launched
// with, so a parse failure means the verdict cannot be vouched for either way.
func TestVerifyBackendReportsAnUnreadableOverlay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Manager{BackendOverlayDir: dir}
	sess := newBackendTestSession("bedrock", nil)
	sess.BackendIntended = api.BackendBedrock
	sess.BackendResolved = api.BackendBedrock
	if err := os.WriteFile(m.backendOverlayPath(sess.Key()), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	v := m.VerifyBackend(*sess)
	if v.OK {
		t.Fatal("expected a problem for an unreadable overlay")
	}
	if !strings.Contains(strings.Join(v.Problems, " | "), "cannot read backend overlay") {
		t.Errorf("problems %v do not name the unreadable overlay", v.Problems)
	}
}

// The credential source is recorded for every session, overlay or not, so a
// default-mode session reads as ambient rather than as never-classified.
func TestBackendOverlayForNamesTheDeclaredCredentialSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		backend string
		env     map[string]string
		want    api.BackendCredentialSource
	}{
		{"export helper", "bedrock", map[string]string{backendAWSCredExportKey: "/opt/creds.sh"}, api.BackendCredentialAWSExport},
		{"profile passthrough", "bedrock", map[string]string{"AWS_PROFILE": "eng"}, api.BackendCredentialAWSProfile},
		{"api key helper", "anthropic-aws", map[string]string{backendAPIKeyHelperKey: "/opt/key.sh"}, api.BackendCredentialAPIKeyHelper},
		{"nothing declared", "default", nil, api.BackendCredentialAmbient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sess := newBackendTestSession(tt.backend, tt.env)
			got := api.ResolveBackendCredentialSource(backendOverlayFor(sess))
			if got != tt.want {
				t.Errorf("credential source = %q, want %q", got, tt.want)
			}
		})
	}
}

// The overlay path is recorded on the session at spawn (design R5), and
// verification reads the recorded path rather than recomputing one. An adopted
// session whose daemon has since moved BackendOverlayDir must still verify
// against the file it was actually launched with.
func TestVerifyBackendPrefersTheRecordedOverlayPath(t *testing.T) {
	t.Parallel()
	launchDir := t.TempDir()
	m := &Manager{BackendOverlayDir: launchDir}
	sess := newBackendTestSession("bedrock", nil)
	plan := &launchPlan{env: map[string]string{}}
	if _, err := m.applyBackendOverlay(sess, plan); err != nil {
		t.Fatalf("applyBackendOverlay: %v", err)
	}
	if sess.BackendOverlayPath == "" {
		t.Fatal("overlay path not recorded on the session")
	}
	if sess.BackendOverlayPath != plan.env[MarvelBackendSettingsEnv] {
		t.Errorf("recorded path %q does not match the stamped one %q", sess.BackendOverlayPath, plan.env[MarvelBackendSettingsEnv])
	}
	sess.BackendIntended = api.BackendBedrock
	sess.BackendResolved = api.BackendBedrock
	sess.BackendCredentialSource = api.BackendCredentialAmbient

	// The daemon's overlay directory moves out from under the running session.
	moved := &Manager{BackendOverlayDir: t.TempDir()}
	v := moved.VerifyBackend(*sess)
	if !v.OK {
		t.Fatalf("expected the recorded path to still verify, got %v", v.Problems)
	}
	if v.OverlayPath != sess.BackendOverlayPath {
		t.Errorf("verified against %q, want the recorded %q", v.OverlayPath, sess.BackendOverlayPath)
	}
}

// The design wires awsAuthRefresh for both IAM modes, so a role declares it
// through a reserved key that is lifted into the settings file rather than
// carried as a pane env var.
func TestBackendOverlayForLiftsTheAuthRefreshPointer(t *testing.T) {
	t.Parallel()
	sess := newBackendTestSession("bedrock", map[string]string{
		backendAWSAuthRefreshKey: "/opt/refresh.sh",
		"AWS_PROFILE":            "eng",
	})
	ov := backendOverlayFor(sess)
	if ov.AWSAuthRefresh != "/opt/refresh.sh" {
		t.Errorf("AWSAuthRefresh = %q, want the declared pointer", ov.AWSAuthRefresh)
	}
	// The reserved key is a settings pointer, not pane env, so it must not
	// survive into the passthrough block.
	if _, ok := ov.Extra[backendAWSAuthRefreshKey]; ok {
		t.Error("the reserved auth-refresh key leaked into the overlay env")
	}
	if ov.Extra["AWS_PROFILE"] != "eng" {
		t.Errorf("AWS_PROFILE passthrough = %q, want it carried", ov.Extra["AWS_PROFILE"])
	}
	if got := api.ResolveBackendCredentialSource(ov); got != api.BackendCredentialAWSAuthRefresh {
		t.Errorf("credential source = %q, want %q", got, api.BackendCredentialAWSAuthRefresh)
	}
}
