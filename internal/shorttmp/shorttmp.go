// Package shorttmp gives a test a scratch directory short enough to hold a
// marvel layout root.
//
// The layout puts the control socket at <root>/run/marvel.sock, and a unix
// socket path is capped at paths.MaxUnixSocketPath bytes. t.TempDir carries
// the test's name under the system temp dir, so on macOS, where the default
// TMPDIR is a long /var/folders path, a root built from it can pass the cap.
package shorttmp

import (
	"os"
	"testing"
)

// maxTempDir is the longest system temp dir Dir will use. It leaves room for
// the random name MkdirTemp adds ("/mv" and up to ten digits) and for the
// "/mh/run/marvel.sock" a root made from the result carries, inside the
// 104-byte socket path cap.
const maxTempDir = 64

// Dir returns a new directory that is removed when the test ends and is short
// enough for <Dir>/mh/run/marvel.sock to fit in a socket path. It lives under
// the system temp dir when that is short, so a run that points TMPDIR at a
// scratch directory stays inside it, and under /tmp when it is not.
func Dir(t testing.TB) string {
	t.Helper()
	parent := os.TempDir()
	if len(parent) > maxTempDir {
		parent = "/tmp"
	}
	dir, err := os.MkdirTemp(parent, "mv")
	if err != nil {
		t.Fatalf("shorttmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
