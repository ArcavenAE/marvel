package tmuxtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// testServerRE matches the socket names the test binaries give their tmux
// server: marvel-test-<package>-<pid>.
var testServerRE = regexp.MustCompile(`^marvel-test-[A-Za-z0-9_]+-([0-9]+)$`)

// SweepDead stops the per-package tmux servers that earlier test runs left
// behind and returns their socket names. A test binary starts its server as
// `tmux -L marvel-test-<package>-<pid>` and stops it only after its tests
// return, so a panic, a -timeout abort or a kill leaves it running; the
// pid in the name is how a later run tells such a server from a live run's.
//
// A name is swept only when the process list shows a tmux server started with
// that exact -L name and no process has the pid in it. A reused pid keeps its
// old server alive, which errs toward leaving a server rather than killing a
// live run's. Failures are ignored: the sweep is housekeeping, not a test.
func SweepDead() []string {
	out, err := exec.Command("ps", "-axo", "pid=,args=").Output()
	if err != nil {
		return nil
	}
	return sweepNames(deadTestServerNames(string(out)))
}

// deadTestServerNames picks, from `ps -axo pid=,args=` output, the -L names of
// tmux processes that follow the test naming and whose owner pid is gone.
func deadTestServerNames(ps string) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(ps, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || filepath.Base(f[1]) != "tmux" {
			continue
		}
		name := ""
		for i := 2; i+1 < len(f); i++ {
			if f[i] == "-L" {
				name = f[i+1]
				break
			}
			if !strings.HasPrefix(f[i], "-") {
				break
			}
		}
		m := testServerRE.FindStringSubmatch(name)
		if m == nil || seen[name] {
			continue
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil || pid <= 0 || pidAlive(pid) {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func sweepNames(names []string) []string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	var swept []string
	for _, n := range names {
		// kill-server looks for the socket under this user's directory in
		// TMUX_TMPDIR, so a server this process cannot reach fails here. It is
		// then neither reported nor unlinked: only a server actually stopped
		// counts as swept.
		if err := exec.Command("tmux", "-L", n, "kill-server").Run(); err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()), n))
		swept = append(swept, n)
	}
	return swept
}
