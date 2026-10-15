// Package shorttmp gives a test a scratch directory short enough to hold a
// marvel layout root.
//
// The layout puts the control socket at <root>/run/marvel.sock, and a unix
// socket path is capped at paths.MaxUnixSocketPath bytes. t.TempDir carries
// the test's name under the system temp dir, so on macOS, where the default
// TMPDIR is a long /var/folders path, a root built from it can pass the cap.
package shorttmp

import "testing"

// Dir returns a new directory that is removed when the test ends and is short
// enough for <Dir>/mh/run/marvel.sock to fit in a socket path.
func Dir(t testing.TB) string {
	t.Helper()
	return t.TempDir()
}
