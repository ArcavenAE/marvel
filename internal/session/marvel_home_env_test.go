package session

import (
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/runtime"
)

// The direct launch path (an ad-hoc session, or a role the adapter cannot
// resolve) builds its own environment, and it must agree with the adapter
// path: MARVEL_HOME rides beside MARVEL_SOCKET for a non-default root only.
// Not parallel: these set HOME and MARVEL_HOME.

func TestDirectCommandCarriesMarvelHomeForANonDefaultRoot(t *testing.T) {
	user := t.TempDir()
	root := filepath.Join(t.TempDir(), "mh")
	t.Setenv("HOME", user)
	sess := &api.Session{Name: "s", Runtime: api.Runtime{Command: "claude"}}
	m := &Manager{SocketPath: "/run/marvel.sock", adapters: runtime.NewRegistry()}

	t.Setenv(paths.HomeEnv, root)
	if _, env := m.directCommand(sess); env[paths.HomeEnv] != root {
		t.Errorf("%s = %q, want %q", paths.HomeEnv, env[paths.HomeEnv], root)
	}
	for name, v := range map[string]string{"unset": "", "default": filepath.Join(user, ".marvel")} {
		t.Setenv(paths.HomeEnv, v)
		if _, env := m.directCommand(sess); func() bool { _, ok := env[paths.HomeEnv]; return ok }() {
			t.Errorf("%s: %s reached a seat of a default-rooted daemon", name, paths.HomeEnv)
		}
	}
}
