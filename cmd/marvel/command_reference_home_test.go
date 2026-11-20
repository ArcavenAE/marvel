package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

// TestCommandReferenceIgnoresTheLengthOfMarvelHome: a marvel home whose socket
// path is over the 104-byte limit makes the daemon drop its path defaults, so
// a reference generated under a long home (a temp dir under a long TMPDIR, a
// long HOME) used to differ from one generated under a short home. The
// committed pages must not depend on either.
func TestCommandReferenceIgnoresTheLengthOfMarvelHome(t *testing.T) {
	t.Setenv(paths.HomeEnv, "")
	short := commandReference(newReferenceRoot())

	long := filepath.Join(t.TempDir(), strings.Repeat("deep", 20), "mh")
	if len(filepath.Join(long, "run", "marvel.sock")) <= paths.MaxUnixSocketPath {
		t.Fatalf("test home %q is not long enough to pass the socket limit", long)
	}
	t.Setenv(paths.HomeEnv, long)
	got := commandReference(newReferenceRoot())
	if len(got) != len(short) {
		t.Fatalf("a long MARVEL_HOME changed the page count from %d to %d", len(short), len(got))
	}
	for name, page := range got {
		if page != short[name] {
			t.Errorf("page %s differs under a long MARVEL_HOME; the committed reference must not depend on its length", name)
		}
	}
}
