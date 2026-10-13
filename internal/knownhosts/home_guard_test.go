package knownhosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// TestMain gives every test in the package a scratch HOME and no MARVEL_*
// variables, so a test that forgets t.Setenv resolves ~/.marvel inside a temp
// dir and never the real one. A test that needs another home still sets its own.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "marvel-test-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "MARVEL_") {
			if err := os.Unsetenv(name); err != nil {
				panic(err)
			}
		}
	}
	m.Run()
	_ = os.RemoveAll(home)
}

// TestMarvelPathsResolveUnderATempDir fails when the paths a test reads or
// writes (known_hosts, config.yaml) would land outside the temp dir, for
// instance if TestMain is removed and HOME is the operator's.
func TestMarvelPathsResolveUnderATempDir(t *testing.T) {
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Clean(os.TempDir()) + string(filepath.Separator)
	for _, p := range []string{layout.KnownHosts(), layout.Config()} {
		if !strings.HasPrefix(filepath.Clean(p), tmp) {
			t.Errorf("%s is not under the temp dir %s; a test here could write the real marvel home", p, tmp)
		}
	}
}
