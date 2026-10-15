package shorttmp_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
	"github.com/arcavenae/marvel/internal/shorttmp"
)

// socketFor is the control socket a test would resolve for a root under dir.
func socketFor(dir string) string {
	return filepath.Join(dir, "mh", "run", "marvel.sock")
}

// base makes a short directory to stand in as TMPDIR, outside t.TempDir whose
// length is the thing under test.
func base(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mvb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestAShortTempDirIsUsed(t *testing.T) {
	b := base(t)
	t.Setenv("TMPDIR", b)
	got := shorttmp.Dir(t)
	if filepath.Dir(got) != b {
		t.Errorf("Dir() = %q, want a directory directly under the short TMPDIR %q", got, b)
	}
}

func TestALongTempDirFallsBackToTmp(t *testing.T) {
	long := filepath.Join(base(t), strings.Repeat("d", paths.MaxUnixSocketPath))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
	got := shorttmp.Dir(t)
	if filepath.Dir(got) != "/tmp" {
		t.Errorf("Dir() = %q, want a directory directly under /tmp", got)
	}
	if n := len(socketFor(got)); n > paths.MaxUnixSocketPath {
		t.Errorf("the socket path is %d bytes, over the %d-byte limit: %s", n, paths.MaxUnixSocketPath, socketFor(got))
	}
}

func TestTheSocketFitsWhateverTheTempDirIs(t *testing.T) {
	for _, n := range []int{0, 20, 40, 60, 80, 120} {
		dir := filepath.Join(base(t), strings.Repeat("d", n))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprintf("%d_extra_bytes", n), func(t *testing.T) {
			t.Setenv("TMPDIR", dir)
			got := shorttmp.Dir(t)
			if sock := socketFor(got); len(sock) > paths.MaxUnixSocketPath {
				t.Errorf("the socket path is %d bytes, over the %d-byte limit: %s", len(sock), paths.MaxUnixSocketPath, sock)
			}
		})
	}
}

func TestTheDirectoryIsRemovedWhenTheTestEnds(t *testing.T) {
	var got string
	t.Run("inner", func(t *testing.T) {
		got = shorttmp.Dir(t)
		if err := os.WriteFile(filepath.Join(got, "f"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("%s survived the test that made it (stat error: %v)", got, err)
	}
}

func TestEachCallGetsItsOwnDirectory(t *testing.T) {
	a, b := shorttmp.Dir(t), shorttmp.Dir(t)
	if a == b {
		t.Errorf("two calls returned %s", a)
	}
}
