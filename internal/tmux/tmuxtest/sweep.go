package tmuxtest

import (
	"errors"
	"net"
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
//
// It then removes the socket files of dead binaries that no server answers on
// (sweepStaleSockets), which needs no process list, so it runs where ps cannot.
func SweepDead() []string {
	var swept []string
	if out, err := exec.Command("ps", "-axo", "pid=,args=").Output(); err == nil {
		swept = sweepNames(deadTestServerNames(string(out)))
	}
	return append(swept, sweepStaleSockets(tmuxTmpDir())...)
}

// tmuxTmpDir is the directory tmux keeps its tmux-<uid> socket directory in.
func tmuxTmpDir() string {
	if dir := os.Getenv("TMUX_TMPDIR"); dir != "" {
		return dir
	}
	return "/tmp"
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
	dir := tmuxTmpDir()
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

// sweepStaleSockets removes the socket files a killed test binary left in
// dir/tmux-<uid>, and returns their names. A server whose binary was killed
// usually exits with it and leaves the file, and the process sweep above never
// sees a server that is gone. An entry goes only when all of these hold: its
// name has the exact marvel-test-<package>-<pid> shape, the pid is not alive,
// it is a socket file, and nothing accepts a connection on it. A socket a
// server still answers on is left, since removing its file would hide a
// running server from every client. Failures are ignored, as in SweepDead.
func sweepStaleSockets(dir string) []string {
	sockets := filepath.Join(dir, "tmux-"+strconv.Itoa(os.Getuid()))
	entries, err := os.ReadDir(sockets)
	if err != nil {
		return nil
	}
	var swept []string
	for _, e := range entries {
		m := testServerRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		pid, err := strconv.Atoi(m[1])
		if err != nil || pid <= 0 || pidAlive(pid) {
			continue
		}
		path := filepath.Join(sockets, e.Name())
		if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSocket == 0 {
			continue
		}
		if c, err := net.Dial("unix", path); err == nil {
			_ = c.Close()
			continue
		}
		if os.Remove(path) == nil {
			swept = append(swept, e.Name())
		}
	}
	return swept
}
