package runtime

import (
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// A seat reaches marvel through MARVEL_SOCKET, and a seat whose daemon runs
// under another layout root needs that root too, or any marvel command it runs
// resolves the config, the keys and the default socket against the wrong one.
// Not parallel: these set HOME and MARVEL_HOME.

func TestBaseEnvCarriesMarvelHomeBesideTheSocketForANonDefaultRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mh")
	t.Setenv("HOME", t.TempDir())
	t.Setenv(paths.HomeEnv, root)

	ctx := testContext()
	ctx.SocketPath = "/run/marvel.sock"
	if got := baseEnv(ctx)[paths.HomeEnv]; got != root {
		t.Errorf("%s = %q, want %q", paths.HomeEnv, got, root)
	}
}

func TestBaseEnvOmitsMarvelHomeForTheDefaultRootAndWhenUnset(t *testing.T) {
	user := t.TempDir()
	t.Setenv("HOME", user)
	ctx := testContext()
	ctx.SocketPath = "/run/marvel.sock"

	for name, v := range map[string]string{
		"unset":   "",
		"default": filepath.Join(user, ".marvel"),
	} {
		t.Setenv(paths.HomeEnv, v)
		if got, present := baseEnv(ctx)[paths.HomeEnv]; present {
			t.Errorf("%s: %s = %q reached a seat of a default-rooted daemon", name, paths.HomeEnv, got)
		}
	}
}

func TestBaseEnvWithoutASocketCarriesNoMarvelHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(paths.HomeEnv, filepath.Join(t.TempDir(), "mh"))
	ctx := testContext()
	ctx.SocketPath = ""
	if got, present := baseEnv(ctx)[paths.HomeEnv]; present {
		t.Errorf("%s = %q with no socket to go beside", paths.HomeEnv, got)
	}
}
