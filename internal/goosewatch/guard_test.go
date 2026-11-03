package goosewatch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startOwn starts a command the test owns, in its own process group, and ends it
// at cleanup. Every kill in this file targets a process started this way, from
// its exec.Cmd, and nothing is killed by pattern or by name.
func startOwn(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func waitFor(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", within, what)
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

func fileLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func groupGone(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return errors.Is(err, syscall.ESRCH)
}

// wrapperChild starts the wrapper as a re-exec of the test binary, so the test
// can SIGKILL it. goose is `sh -c script`; the script writes its pid to pidFile.
func wrapperChild(t *testing.T, stderrFile string, grace time.Duration, script string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tok := newToken()
	cmd := exec.Command(exe, reexecFlag, tok, "wrapper", stderrFile, grace.String(), script)
	cmd.Env = append(os.Environ(), reexecEnv+"="+tok)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// A SIGKILL of the wrapper cannot be caught. The guard sees the pipe close and
// ends goose's group. The fake goose has a long-lived child of its own: if goose
// or that child held the pipe's write end, the guard would never see EOF and the
// group would outlive the grace, so this test is also the proof that the write
// end is close-on-exec.
func TestGuardEndsTheGroupWhenTheWrapperIsKilled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stderrFile := filepath.Join(dir, "stderr.log")
	pidFile := filepath.Join(dir, "goose.pid")
	grace := time.Second
	// goose stays the leader (the last command is not exec'd) and has a child.
	script := fmt.Sprintf("echo $$ > %s; sleep 60 & sleep 60; exit 0", pidFile)
	w := wrapperChild(t, stderrFile, grace, script)

	var pgid int
	waitFor(t, "goose to write its pid", 10*time.Second, func() bool {
		pgid = readPid(t, pidFile)
		return pgid > 1
	})
	// The wrapper tells the guard right after goose starts; allow for it.
	time.Sleep(time.Second)
	if err := w.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = w.Wait()

	waitFor(t, "the goose group to end", grace+8*time.Second, func() bool { return groupGone(pgid) })
	waitFor(t, "the guard's line", 5*time.Second, func() bool {
		l := fileLines(stderrFile)
		return len(l) > 0 && strings.HasPrefix(l[len(l)-1], "guard: ")
	})
	lines := fileLines(stderrFile)
	want := fmt.Sprintf("guard: wrapper gone, sent TERM to goose group %d; group gone within grace", pgid)
	if got := lines[len(lines)-1]; got != want {
		t.Errorf("last stderr line = %q, want %q", got, want)
	}
}

// A goose that ignores TERM is ended by the guard's KILL after the grace.
func TestGuardKillsAGooseThatIgnoresTerm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stderrFile := filepath.Join(dir, "stderr.log")
	pidFile := filepath.Join(dir, "goose.pid")
	grace := time.Second
	script := fmt.Sprintf("trap '' TERM; echo $$ > %s; sleep 60; exit 0", pidFile)
	w := wrapperChild(t, stderrFile, grace, script)

	var pgid int
	waitFor(t, "goose to write its pid", 10*time.Second, func() bool {
		pgid = readPid(t, pidFile)
		return pgid > 1
	})
	time.Sleep(time.Second)
	_ = w.Process.Kill()
	_ = w.Wait()

	waitFor(t, "the goose group to end", grace+8*time.Second, func() bool { return groupGone(pgid) })
	waitFor(t, "the guard's line", 5*time.Second, func() bool {
		l := fileLines(stderrFile)
		return len(l) > 0 && strings.HasPrefix(l[len(l)-1], "guard: ")
	})
	lines := fileLines(stderrFile)
	want := fmt.Sprintf("guard: wrapper gone, sent TERM then KILL to goose group %d", pgid)
	if got := lines[len(lines)-1]; got != want {
		t.Errorf("last stderr line = %q, want %q", got, want)
	}
}

// On a clean exit the wrapper says "done" and the guard exits, leaving a
// bystander process alone.
func TestGuardExitsOnDoneAndLeavesABystanderAlone(t *testing.T) {
	t.Parallel()
	bystander := startOwn(t, "sleep", "30")
	dir := t.TempDir()
	cfg := Config{
		Argv:       []string{"/bin/sh", "-c", "exit 0"},
		Grace:      time.Second,
		StderrFile: filepath.Join(dir, "stderr.log"),
		GuardArgv:  testGuardArgv(),
	}
	g, err := startGuard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.end()
	select {
	case <-g.done:
	default:
		t.Fatal("the guard was still running after done")
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("the bystander is gone: %v", err)
	}
	if l := fileLines(cfg.StderrFile); len(l) > 0 && l[0] != "" {
		t.Errorf("the guard wrote %q on a clean exit", l)
	}
}

// A guard that cannot start fails closed: exit 126, the reason in the stderr
// file, and goose never started.
func TestAGuardThatCannotStartMeans126AndGooseNeverStarts(t *testing.T) {
	t.Parallel()
	started := filepath.Join(t.TempDir(), "started")
	r := runSh(t, "touch "+started, 5*time.Second, 5*time.Second, func(c *Config) {
		c.GuardArgv = []string{"/nonexistent/goose-guard"}
	})
	if r.code != ExitCannotRun {
		t.Errorf("exit = %d, want %d", r.code, ExitCannotRun)
	}
	if !strings.Contains(r.stderr, "cannot start the guard") || !strings.Contains(r.stderr, "goose was not started") {
		t.Errorf("stderr file = %q", r.stderr)
	}
	if _, err := os.Stat(started); err == nil {
		t.Error("goose was started without a guard")
	}
}

// A failed proof means no kill: the guard writes its did-nothing line and the
// process it was pointed at lives.
func TestGuardDoesNothingWhenTheProofFails(t *testing.T) {
	t.Parallel()
	target := startOwn(t, "sleep", "30")
	file := filepath.Join(t.TempDir(), "stderr.log")
	in := strings.NewReader(fmt.Sprintf("pgid %d start not-its-start-time\n", target.Process.Pid))
	if code := RunGuard(GuardConfig{In: in, Grace: 200 * time.Millisecond, StderrFile: file, Arg: "30"}); code != 0 {
		t.Errorf("exit = %d", code)
	}
	l := fileLines(file)
	if len(l) != 1 || !strings.HasPrefix(l[0], fmt.Sprintf("guard: could not prove goose group %d (", target.Process.Pid)) || !strings.HasSuffix(l[0], "); did nothing") {
		t.Errorf("stderr file = %q", l)
	}
	if err := syscall.Kill(target.Process.Pid, 0); err != nil {
		t.Errorf("the target was signalled: %v", err)
	}
}

// A pgid past 32 bits would act on another process on darwin; it is refused.
func TestGuardRefusesAPgidPast32Bits(t *testing.T) {
	t.Parallel()
	own := startOwn(t, "sleep", "30")
	wide := int64(own.Process.Pid) + 1<<32
	if _, _, err := parsePgidLine(fmt.Sprintf("pgid %d start x", wide)); err == nil {
		t.Error("a pgid past 32 bits was accepted")
	}
	if _, _, err := parsePgidLine(fmt.Sprintf("pgid %d start x", own.Process.Pid)); err != nil {
		t.Errorf("a 32-bit pgid was refused: %v", err)
	}
	file := filepath.Join(t.TempDir(), "stderr.log")
	in := strings.NewReader(fmt.Sprintf("pgid %d start x\n", wide))
	if code := RunGuard(GuardConfig{In: in, Grace: 100 * time.Millisecond, StderrFile: file, Arg: "30"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if err := syscall.Kill(own.Process.Pid, 0); err != nil {
		t.Errorf("the process %d was acted on: %v", own.Process.Pid, err)
	}
	if l := fileLines(file); len(l) != 1 || !strings.Contains(l[0], "unreadable line from the wrapper") {
		t.Errorf("stderr file = %q", l)
	}
}

// A signal that arrives while the launch is still starting is held and
// forwarded once goose exists. The hook delivers a real SIGTERM to this process
// after the subscription and before anything starts; this is the one test that
// uses the real signal.Notify, so it is not parallel.
func TestASignalDuringLaunchIsNotLost(t *testing.T) {
	r := runSh(t, "sleep 30", 20*time.Second, 20*time.Second, func(c *Config) {
		c.Signals = nil
		c.beforeStart = func() {
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				t.Errorf("signal self: %v", err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	if r.code != 128+int(syscall.SIGTERM) {
		t.Errorf("exit = %d, want %d", r.code, 128+int(syscall.SIGTERM))
	}
	want := `{"type":"marvel.watchdog","reason":"signal","signal":"TERM"}`
	if got := lastLine(r.stdout); got != want {
		t.Errorf("last stdout line = %q, want %q", got, want)
	}
	if r.elapsed > 5*time.Second {
		t.Errorf("ended after %v", r.elapsed)
	}
}

// A clean run says "done" before the guard reads EOF, so the guard writes
// nothing: no guard line lands in the stderr file of a run that ended normally.
func TestACleanRunLeavesNoGuardLine(t *testing.T) {
	t.Parallel()
	r := runSh(t, "echo hi >&2; exit 0", 10*time.Second, 10*time.Second, nil)
	if r.code != 0 {
		t.Fatalf("exit = %d", r.code)
	}
	if strings.Contains(r.stderr, "guard:") {
		t.Errorf("stderr file = %q", r.stderr)
	}
}
