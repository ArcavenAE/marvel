package runtime

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// selfExecutable and lookPath are seams so a test can say where the running
// binary is and what the PATH resolves, without installing anything.
var (
	selfExecutable = os.Executable
	lookPath       = exec.LookPath
)

// cellarWarned holds the keg paths already reported, so a seat launch loop
// names a path once and not on every launch.
var cellarWarned sync.Map

// fallbackPath returns the running binary's own path. When that is a
// version-pinned Homebrew keg path (it has a /Cellar/ element) and no PATH link
// reached it, a seeded command that names it stops working once the keg is
// cleaned up, so it is reported, once per path, and not silently accepted.
func fallbackPath(exe string) string {
	if strings.Contains(filepath.ToSlash(exe), "/Cellar/") {
		if _, seen := cellarWarned.LoadOrStore(exe, true); !seen {
			log.Printf("warning: the marvel on the PATH is not the running binary, so a command seeded into a harness's files names the version-pinned path %s, which a package cleanup removes after an upgrade; put a marvel link that reaches this binary on the daemon's PATH", exe)
		}
	}
	return exe
}

// MarvelHookPath is the path a command seeded into a harness's files names to
// run this marvel binary. Those files outlive the daemon that wrote them, and
// os.Executable() is the versioned keg path of an install that a package
// manager's cleanup removes after an upgrade, so a hook naming it fails with
// "command not found" for the rest of the seat's life. The marvel the PATH
// resolves is a link the package manager retargets on every upgrade, so it is
// used when it is the same file as the running binary. When it is not (no
// marvel on the PATH, or another build), the running binary's own path is the
// only honest answer.
func MarvelHookPath() (string, error) {
	exe, err := selfExecutable()
	if err != nil {
		return "", err
	}
	link, err := lookPath("marvel")
	if err != nil {
		return fallbackPath(exe), nil
	}
	if link, err = filepath.Abs(link); err != nil {
		return fallbackPath(exe), nil
	}
	a, aerr := os.Stat(link)
	b, berr := os.Stat(exe)
	if aerr != nil || berr != nil || !os.SameFile(a, b) {
		return fallbackPath(exe), nil
	}
	return link, nil
}
