package daemon

import (
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
)

// MARVEL_HOME moves marvel's own state and nothing the user owns. This pins the
// read of the user's real home that stays in this package; the ssh key fallback's
// pin moved with sshAuthMethodsFor to internal/daemonrpc. Not parallel: it sets
// HOME and MARVEL_HOME.

func TestMarvelHomeDoesNotMoveTheAccountLabelsDefaultDir(t *testing.T) {
	home, other := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MARVEL_HOME", other)
	label := func(dir string) string {
		return accountLabel(api.Session{Runtime: api.Runtime{Name: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": dir}}})
	}
	def := label("")
	if got := label(filepath.Join(home, ".claude")); got != def {
		t.Errorf("the user's ~/.claude labels %s, want the default %s", got, def)
	}
	if got := label(filepath.Join(other, ".claude")); got == def {
		t.Errorf("a .claude under MARVEL_HOME labels as the default account")
	}
}
