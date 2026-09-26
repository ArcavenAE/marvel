package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The measured contract these encode (codex 0.153.4, 2026-09-15): CODEX_HOME
// relocates the entire state tree, a session run under a fresh one leaves
// exactly one rollout in it, and a private home with no auth.json produces
// 401 on the first model call even for a logged-in operator. So the manager
// must create the directory, link the credential in, and tell the adapter.

func homeManager(t *testing.T) *Manager {
	t.Helper()
	mgr := &Manager{
		store:          api.NewStore(),
		adapters:       runtime.NewRegistry(),
		ProjectionDir:  t.TempDir(),
		HarnessHomeDir: t.TempDir(),
		Events:         events.NewRing(16),
	}
	m, err := api.ParseManifestBytes([]byte(sessionIDManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.Apply(mgr.store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return mgr
}

func TestCodexLaunchGetsAPrivateHomeAndRecordsIt(t *testing.T) {
	t.Parallel()
	mgr := homeManager(t)
	sess := sessionFor("coder", "codex")

	plan := mgr.planLaunch(sess)

	if sess.HarnessHome == "" {
		t.Fatal("a codex launch should have been given a private harness home")
	}
	if !strings.HasPrefix(sess.HarnessHome, mgr.HarnessHomeDir) {
		t.Errorf("the private home must live under the manager's dir, got %q", sess.HarnessHome)
	}
	if fi, err := os.Stat(sess.HarnessHome); err != nil || !fi.IsDir() {
		t.Errorf("the private home should exist as a directory: %v", err)
	}
	if plan.env["CODEX_HOME"] != sess.HarnessHome {
		t.Errorf("CODEX_HOME should be the recorded home %q, got %q",
			sess.HarnessHome, plan.env["CODEX_HOME"])
	}
}

// Per session key, not per launch: a restart lands back in the same place,
// because the point is separating this agent from the others rather than
// discarding its state between respawns.
func TestPrivateHomeIsStableAcrossRelaunch(t *testing.T) {
	t.Parallel()
	mgr := homeManager(t)
	sess := sessionFor("coder", "codex")

	mgr.planLaunch(sess)
	first := sess.HarnessHome
	mgr.planLaunch(sess)
	second := sess.HarnessHome

	if first == "" || first != second {
		t.Errorf("a relaunch should reuse the same private home, got %q then %q", first, second)
	}
}

// Two sessions must not share a home, or the rollouts mix again and the
// binding this exists to provide is gone.
func TestPrivateHomesAreDistinctPerSession(t *testing.T) {
	t.Parallel()
	mgr := homeManager(t)
	a := sessionFor("coder", "codex")
	b := sessionFor("coder", "codex")
	b.Name = "squad-coder-g1-1"

	mgr.planLaunch(a)
	mgr.planLaunch(b)

	if a.HarnessHome == "" || a.HarnessHome == b.HarnessHome {
		t.Errorf("distinct sessions need distinct homes, got %q and %q", a.HarnessHome, b.HarnessHome)
	}
}

// The credential is SYMLINKED, never copied. A copy would make marvel a
// second holder of bearer authority at a third party, which SOUL section 3
// and ADR-009 put outside the custody boundary.
func TestCredentialIsLinkedNotCopied(t *testing.T) {
	// No t.Parallel: this one sets CODEX_HOME for the process.
	mgr := homeManager(t)
	source := t.TempDir()
	authSrc := filepath.Join(source, "auth.json")
	if err := os.WriteFile(authSrc, []byte(`{"token":"operator-secret"}`), 0o600); err != nil {
		t.Fatalf("seed auth: %v", err)
	}
	t.Setenv("CODEX_HOME", source)

	sess := sessionFor("coder", "codex")
	mgr.planLaunch(sess)

	link := filepath.Join(sess.HarnessHome, "auth.json")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("auth.json should have been linked into the private home: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("auth.json must be a symlink, not a copy: a copy is custody")
	}
	target, err := os.Readlink(link)
	if err != nil || target != authSrc {
		t.Errorf("the link should point at the operator's own file %q, got %q (%v)", authSrc, target, err)
	}
}

// An operator who has not logged that harness in yet is not a spawn failure.
func TestMissingCredentialDoesNotBlockTheLaunch(t *testing.T) {
	// No t.Parallel: this one sets CODEX_HOME for the process.
	mgr := homeManager(t)
	t.Setenv("CODEX_HOME", t.TempDir()) // exists, but holds no auth.json

	sess := sessionFor("coder", "codex")
	plan := mgr.planLaunch(sess)

	if sess.HarnessHome == "" {
		t.Error("a missing credential should not cost the session its private home")
	}
	if plan.command == "" {
		t.Error("a missing credential should not stop the launch being planned")
	}
	if _, err := os.Lstat(filepath.Join(sess.HarnessHome, "auth.json")); err == nil {
		t.Error("nothing should have been linked when the source has no auth.json")
	}
}

// claude keeps no single relocatable state root, so it must not be handed a
// private home it would half-use.
func TestRuntimesWithoutARelocatableHomeGetNone(t *testing.T) {
	t.Parallel()
	mgr := homeManager(t)
	sess := sessionFor("reviewer", "claude")

	plan := mgr.planLaunch(sess)

	if sess.HarnessHome != "" {
		t.Errorf("claude should share the operator's home, got %q", sess.HarnessHome)
	}
	if _, set := plan.env["CODEX_HOME"]; set {
		t.Error("a claude launch must not carry CODEX_HOME")
	}
}

// config.toml is SEEDED, never linked (marvel#308). The operator's file
// carries project trust that makes the target writable under -s read-only
// (finding-049), an approvals reviewer, and their own director identity. The
// private home gets a file marvel wrote, carrying the codex-ctx hooks and
// none of those keys.
func TestCodexConfigIsSeededNotLinked(t *testing.T) {
	// No t.Parallel: this one sets CODEX_HOME for the process.
	mgr := homeManager(t)
	source := t.TempDir()
	operator := `approvals_reviewer = "auto_review"

[projects."/work/target"]
trust_level = "trusted"

[mcp_servers.director]
command = "/opt/director/bin/director-mcp"

[mcp_servers.director.env]
DIRECTOR_AGENT_ID = "ops/operator"
`
	if err := os.WriteFile(filepath.Join(source, "config.toml"), []byte(operator), 0o600); err != nil {
		t.Fatalf("write operator config: %v", err)
	}
	t.Setenv("CODEX_HOME", source)

	sess := sessionFor("coder", "codex")
	mgr.planLaunch(sess)

	path := filepath.Join(sess.HarnessHome, "config.toml")
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("a codex launch should seed config.toml: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("config.toml must be marvel's own file, not a link to the operator's")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}
	got := string(data)
	for _, banned := range []string{"approvals_reviewer", "/work/target", "ops/operator"} {
		if strings.Contains(got, banned) {
			t.Errorf("seeded config carries the operator's %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{"codex-ctx", "SessionStart", "Stop", "PostToolUse", "/opt/director/bin/director-mcp", `"MARVEL_SESSION"`} {
		if !strings.Contains(got, want) {
			t.Errorf("seeded config lacks %q:\n%s", want, got)
		}
	}
}
