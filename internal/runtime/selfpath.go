package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
)

// selfExecutable and lookPath are seams so a test can say where the running
// binary is and what the PATH resolves, without installing anything.
var (
	selfExecutable = os.Executable
	lookPath       = exec.LookPath
)

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
		return exe, nil
	}
	if link, err = filepath.Abs(link); err != nil {
		return exe, nil
	}
	a, aerr := os.Stat(link)
	b, berr := os.Stat(exe)
	if aerr != nil || berr != nil || !os.SameFile(a, b) {
		return exe, nil
	}
	return link, nil
}
