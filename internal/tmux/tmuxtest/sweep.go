package tmuxtest

import (
	"errors"
	"fmt"
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
		pid, ok := parsePid(m[1])
		if !ok || pidAlive(pid) {
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
// it is a socket file, and a connect to it is refused. A socket a
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
		pid, ok := parsePid(m[1])
		if !ok || pidAlive(pid) {
			continue
		}
		path := filepath.Join(sockets, e.Name())
		if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSocket == 0 {
			continue
		}
		c, err := dialUnix(path)
		if err == nil {
			_ = c.Close()
			continue
		}
		// Only a refused connection says nobody listens. Any other failure
		// (a sandbox that denies unix-connect gives EPERM) says nothing about
		// the server, so the file stays.
		if !errors.Is(err, syscall.ECONNREFUSED) {
			continue
		}
		if os.Remove(path) == nil {
			swept = append(swept, e.Name())
		}
	}
	return swept
}

// parsePid reads the pid in a test server name. It reports false for anything
// that is not a positive pid that fits in 32 bits: darwin takes a 32-bit pid at
// the syscall, so a wider value would act on the process whose low 32 bits
// match, and a name carrying one is not the pid it appears to be.
func parsePid(s string) (int, bool) {
	pid, err := strconv.ParseInt(s, 10, 32)
	return int(pid), err == nil && pid > 0
}

// dialUnix is how sweepStaleSockets asks whether a server answers on a socket;
// a test replaces it.
var dialUnix = func(path string) (net.Conn, error) { return net.Dial("unix", path) }

// UseScratchTmuxTmpDir points TMUX_TMPDIR at a directory of the test binaries'
// own when it is unset, and returns the directory it set ("" when it left the
// variable alone). Without it a test binary runs its server in, and sweeps, the
// operator's own /tmp/tmux-<uid>. The directory is the same on every run, so
// the next run's sweep finds what a killed run left. When no directory can be
// made it returns an error and leaves the variable unset; a caller stops
// rather than sweep and serve from the operator's directory (#1087).
func UseScratchTmuxTmpDir() (string, error) { return useScratchTmuxTmpDirUnder("/tmp") }

func useScratchTmuxTmpDirUnder(base string) (string, error) {
	if os.Getenv("TMUX_TMPDIR") != "" {
		return "", nil
	}
	dir, err := scratchTmuxTmpDir(base)
	if err != nil {
		return "", err
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		return "", fmt.Errorf("tmuxtest: set TMUX_TMPDIR: %w", err)
	}
	return dir, nil
}

// scratchTmuxTmpDir returns the per-user directory under base for test
// binaries' tmux sockets, created with mode 0700. base is shared and the name
// is predictable, so an existing entry is used only when it is a real
// directory this user owns with mode 0700; otherwise (a planted symlink, a
// directory of another user) the binary gets a fresh private directory.
func scratchTmuxTmpDir(base string) (string, error) {
	prefix := "marvel-test-tmux-" + strconv.Itoa(os.Getuid())
	dir := filepath.Join(base, prefix)
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("tmuxtest: make a scratch tmux directory: %w", err)
	}
	if fi, err := os.Lstat(dir); err == nil && fi.IsDir() && fi.Mode().Perm() == 0o700 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Getuid() {
			return dir, nil
		}
	}
	fresh, err := os.MkdirTemp(base, prefix+"-")
	if err != nil {
		return "", fmt.Errorf("tmuxtest: make a scratch tmux directory: %w", err)
	}
	return fresh, nil
}
