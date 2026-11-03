package goosewatch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// startOwn starts a `sleep` the test owns, in its own process group, and ends it
// at cleanup. Every kill in this file targets a process started this way, from
// its exec.Cmd, and nothing is killed by pattern or by name.
func startOwn(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
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
	bystander := startOwn(t)
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
	target := startOwn(t)
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
	own := startOwn(t)
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
	// The guard's own lines start with "guard: ". The wrapper's announce-failure
	// line, which a fast exit can earn on a slow host, starts with "goose-watch:".
	for _, line := range strings.Split(r.stderr, "\n") {
		if strings.HasPrefix(line, "guard: ") {
			t.Errorf("stderr file holds a guard line: %q", r.stderr)
		}
	}
}

// When the wrapper cannot read goose's identity (Spawned fails), it cannot tell
// the guard its group and ends goose with the one raw group kill the MS-9
// exception allows. The read seam fails; goose, which the wrapper started, ends
// within the run, the wrapper exits 126, and a process in another group is
// untouched.
func TestUnannouncedGooseIsEndedByTheRawGroupKill(t *testing.T) {
	t.Parallel()
	bystander := startOwn(t)
	prober, err := childproof.NewForTest(
		func(int) (childproof.Identity, error) { return childproof.Identity{}, errors.New("no identity") },
		syscall.Kill,
	)
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "goose.pid")
	r := runSh(t, fmt.Sprintf("echo $$ > %s; exec sleep 30", pidFile), time.Minute, time.Minute, func(c *Config) {
		c.prober = prober
	})
	if r.code != ExitCannotRun {
		t.Errorf("exit = %d, want %d", r.code, ExitCannotRun)
	}
	if !strings.Contains(r.stderr, "cannot prove the goose process for the guard") {
		t.Errorf("stderr file = %q", r.stderr)
	}
	// A goose that was not ended would keep the run for the full 30 s sleep.
	if r.elapsed > 15*time.Second {
		t.Errorf("the run lasted %v: goose was not ended", r.elapsed)
	}
	if pgid := readPid(t, pidFile); pgid > 1 && !groupGone(pgid) {
		t.Errorf("goose group %d is still alive", pgid)
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("the bystander is gone: %v", err)
	}
}

// waitForPidFile holds the wrapper between goose's start and the announce until
// goose has written its pid, so the group check afterwards has a pid to look at
// and no timing decides it.
func waitForPidFile(t *testing.T, pidFile string) func(*os.File) {
	return func(*os.File) {
		waitFor(t, "goose to write its pid", 10*time.Second, func() bool { return readPid(t, pidFile) > 1 })
	}
}

// wantReasonThenStatus checks the one announce-failure line: the proof-failure
// reason first, then how goose ended, which is the KILL's effect (Signaled KILL).
func wantReasonThenStatus(t *testing.T, stderr string) {
	t.Helper()
	reason := strings.Index(stderr, "cannot prove the goose process for the guard")
	status := strings.Index(stderr, "goose: died of signal killed")
	if reason < 0 || status < reason {
		t.Errorf("stderr file = %q, want the proof-failure reason, then \"goose: died of signal killed\"", stderr)
	}
}

// When Spawned succeeds but the leader check fails, goose is ended by the one raw
// group KILL, before any wait, and the result is 126: nothing re-reads the
// identity, so when the shell's own exec lands does not matter.
func TestAnnounceFailureIsEndedByTheRawGroupKill(t *testing.T) {
	t.Parallel()
	bystander := startOwn(t)
	prober, err := childproof.NewForTest(
		func(pid int) (childproof.Identity, error) {
			id, err := childproof.ReadIdentityForTest(pid)
			id.Pgid = pid + 1 // not its own group leader
			return id, err
		},
		syscall.Kill,
	)
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "goose.pid")
	r := runSh(t, fmt.Sprintf("echo $$ > %s; exec sleep 30", pidFile), time.Minute, time.Minute, func(c *Config) {
		c.prober = prober
		c.afterStart = waitForPidFile(t, pidFile)
	})
	if r.code != ExitCannotRun {
		t.Errorf("exit = %d, want %d", r.code, ExitCannotRun)
	}
	wantReasonThenStatus(t, r.stderr)
	if r.elapsed > 15*time.Second {
		t.Errorf("the run lasted %v: goose was not ended", r.elapsed)
	}
	if pgid := readPid(t, pidFile); !groupGone(pgid) {
		t.Errorf("goose group %d is still alive", pgid)
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("the bystander is gone: %v", err)
	}
}

// The reviewer's case: the first identity read succeeds with a leader check that
// fails, and every later read errors, as it does when the identity changed or the
// process is gone. A re-proof would refuse and goose would run for its whole life;
// the raw KILL sends nothing through a re-proof, so goose ends at once.
func TestAnnounceFailureWithARefusedReproofStillEndsGoose(t *testing.T) {
	t.Parallel()
	bystander := startOwn(t)
	var mu sync.Mutex
	reads := 0
	prober, err := childproof.NewForTest(
		func(pid int) (childproof.Identity, error) {
			mu.Lock()
			reads++
			n := reads
			mu.Unlock()
			if n > 1 {
				return childproof.Identity{}, errors.New("identity changed")
			}
			id, err := childproof.ReadIdentityForTest(pid)
			id.Pgid = pid + 1
			return id, err
		},
		syscall.Kill,
	)
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "goose.pid")
	// The clock starts when goose has written its pid, so wrapper start-up under
	// load does not count against the bound.
	var gooseUp time.Time
	r := runSh(t, fmt.Sprintf("echo $$ > %s; exec sleep 3", pidFile), time.Minute, time.Minute, func(c *Config) {
		c.prober = prober
		c.afterStart = func(f *os.File) {
			waitForPidFile(t, pidFile)(f)
			gooseUp = time.Now()
		}
	})
	sinceUp := time.Since(gooseUp)
	if r.code != ExitCannotRun {
		t.Errorf("exit = %d, want %d", r.code, ExitCannotRun)
	}
	// goose is `exec sleep 3`: a goose left running would hold the run for 3s.
	if sinceUp > 2500*time.Millisecond {
		t.Errorf("the run lasted %v after goose wrote its pid: goose was left running", sinceUp)
	}
	wantReasonThenStatus(t, r.stderr)
	if pgid := readPid(t, pidFile); !groupGone(pgid) {
		t.Errorf("goose group %d is still alive", pgid)
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("the bystander is gone: %v", err)
	}
}

// failingProber reads no identity, so Spawned fails after its retry window.
func failingProber(t *testing.T) *childproof.Prober {
	t.Helper()
	p, err := childproof.NewForTest(
		func(int) (childproof.Identity, error) { return childproof.Identity{}, errors.New("no identity") },
		syscall.Kill,
	)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// waitForGooseToEnd holds the wrapper between goose's start and the guard's
// announcement until goose's stderr reads EOF. goose writes nothing to stderr,
// so EOF comes only when the process has exited, and its wait status is final
// from then on: no timing decides which case a test is in.
func waitForGooseToEnd(errR *os.File) { _, _ = io.Copy(io.Discard, errR) }

// A goose that ends before the wrapper can prove it for the guard is not a
// failed launch. The announce fails, goose is ended first and waited for second,
// and the wait status says goose finished on its own: the run continues as a
// normal one and returns goose's own code, with its stream lines, plus one line
// that says which branch ran.
func TestAGooseThatEndedBeforeTheGuardWasToldKeepsItsOwnExitCode(t *testing.T) {
	t.Parallel()
	bystander := startOwn(t)
	r := runSh(t, "printf '%s\\n' 'line one' 'line two'; exit 7", time.Minute, time.Minute, func(c *Config) {
		c.prober = failingProber(t)
		c.afterStart = waitForGooseToEnd
	})
	if r.code != 7 {
		t.Errorf("exit = %d, want goose's 7", r.code)
	}
	if !strings.HasPrefix(r.stdout, "line one\nline two\n") {
		t.Errorf("stdout = %q, want goose's lines first", r.stdout)
	}
	if !strings.Contains(r.stderr, "cannot prove the goose process for the guard") || !strings.Contains(r.stderr, "goose: exit status 7") {
		t.Errorf("stderr file = %q, want the announce-failure line with goose's status", r.stderr)
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("the bystander is gone: %v", err)
	}
}

// A goose that died of a signal other than KILL before the proof is returned as
// 128 plus the signal, as the wrapper does elsewhere.
func TestAGooseThatDiedOfAnotherSignalBeforeTheGuardWasToldReturns128PlusIt(t *testing.T) {
	t.Parallel()
	r := runSh(t, "kill -TERM $$", time.Minute, time.Minute, func(c *Config) {
		c.prober = failingProber(t)
		c.afterStart = waitForGooseToEnd
	})
	if r.code != 128+int(syscall.SIGTERM) {
		t.Errorf("exit = %d, want %d", r.code, 128+int(syscall.SIGTERM))
	}
	if !strings.Contains(r.stderr, "goose: died of signal terminated") {
		t.Errorf("stderr file = %q", r.stderr)
	}
}

// A goose still running whose proof fails is ended by the kill and refused with
// 126: marvel cannot tell its own KILL from another one, so it does not guess.
// goose here is the leader itself (exec sleep): a leader with a child can see the
// child die of the group kill and exit 0 before its own copy of the signal lands,
// which is a normal exit and is returned as goose's own.
func TestAGooseStillRunningWhoseProofFailsIsRefusedWith126(t *testing.T) {
	t.Parallel()
	bystander := startOwn(t)
	r := runSh(t, "exec sleep 30", time.Minute, time.Minute, func(c *Config) { c.prober = failingProber(t) })
	if r.code != ExitCannotRun {
		t.Errorf("exit = %d, want %d", r.code, ExitCannotRun)
	}
	if !strings.Contains(r.stderr, "goose: died of signal killed") {
		t.Errorf("stderr file = %q, want the line with the signal status", r.stderr)
	}
	if r.elapsed > 15*time.Second {
		t.Errorf("the run lasted %v: goose was not ended", r.elapsed)
	}
	if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
		t.Errorf("the bystander is gone: %v", err)
	}
}

// A guard that never receives a group exits on the wrapper's "done", or on EOF
// with no announced group, and signals nothing.
func TestAGuardThatWasNeverToldAGroupSignalsNothing(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{"done": "done\n", "EOF with nothing": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bystander := startOwn(t)
			file := filepath.Join(t.TempDir(), "stderr.log")
			if code := RunGuard(GuardConfig{In: strings.NewReader(input), Grace: 100 * time.Millisecond, StderrFile: file, Arg: "30"}); code != 0 {
				t.Errorf("exit = %d", code)
			}
			if _, err := os.Stat(file); err == nil {
				t.Errorf("the guard wrote %q", fileLines(file))
			}
			if err := syscall.Kill(bystander.Process.Pid, 0); err != nil {
				t.Errorf("the bystander was signalled: %v", err)
			}
		})
	}
}
