package knownhosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/homeguard"
	"github.com/arcavenae/marvel/internal/paths"
)

// TestMain gives every test in the package a scratch HOME and no MARVEL_*
// variables through the shared guard; see internal/homeguard.
func TestMain(m *testing.M) { os.Exit(homeguard.Run(m)) }

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
