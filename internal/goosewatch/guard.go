package goosewatch

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/arcavenae/marvel/internal/childproof"
)

// The guard protocol, one line each way over a pipe only the wrapper writes:
//
//	pgid <N> start <S>   goose's process group and its start time, from childproof
//	done                 the wrapper is ending normally; the guard exits
//
// EOF before "done" means the wrapper died, and the guard then ends the group.

// guardHandle is the wrapper's end of the guard.
type guardHandle struct {
	w       *os.File
	done    chan struct{}
	arg     string
	once    sync.Once
	prober  *childproof.Prober
	started bool
	// child is goose as childproof read it, set once Spawned has succeeded.
	child    childproof.Child
	hasChild bool
}

// startGuard starts the guard in its own session, so neither a SIGKILL of the
// wrapper nor a hangup of the pane reaches it, with the read end of a pipe as its
// fd 3. The wrapper closes its own copy of the read end, and keeps the write end
// close-on-exec, so goose does not inherit it: EOF then means the wrapper is gone
// and nothing else.
func startGuard(cfg Config) (*guardHandle, error) {
	argv := cfg.GuardArgv
	if len(argv) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("the marvel binary was not found: %w", err)
		}
		argv = []string{exe, "goose-guard"}
	}
	arg := guardArg(cfg.Argv)
	args := append(append([]string(nil), argv[1:]...),
		"--grace", cfg.Grace.String(), "--stderr-file", cfg.StderrFile, "--arg", arg)
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}
	cmd := exec.Command(argv[0], args...)
	cmd.ExtraFiles = []*os.File{pr}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, err
	}
	_ = pr.Close()
	prober := cfg.prober
	if prober == nil {
		prober = childproof.New()
	}
	g := &guardHandle{w: pw, done: make(chan struct{}), arg: arg, prober: prober, started: true}
	go func() {
		_ = cmd.Wait()
		close(g.done)
	}()
	return g, nil
}

// guardArg is the one whole argument of the goose command line the guard proves
// goose's process by: the first argument after the command, or the command
// itself when it has none.
func guardArg(argv []string) string {
	if len(argv) > 1 {
		return argv[1]
	}
	return argv[0]
}

// announce reads goose's identity from the kernel and tells the guard.
func (g *guardHandle) announce(cmd *exec.Cmd, pgid int) error {
	child, err := g.prober.Spawned(cmd)
	if err != nil {
		return err
	}
	g.child, g.hasChild = child, true
	id := child.Identity()
	if id.Pgid != pgid {
		return fmt.Errorf("goose is not the leader of its own process group")
	}
	if _, err := fmt.Fprintf(g.w, "pgid %d start %s\n", pgid, id.Start); err != nil {
		return fmt.Errorf("tell the guard: %w", err)
	}
	return nil
}

// endUnannounced ends goose when the guard could not be told its group. It runs
// before the wrapper's cmd.Wait on goose. Until goose is waited, the unreaped
// child holds its pid, so the pid and its group cannot be reused, and the target
// is the wrapper's own fresh child. With a childproof Child the kill goes
// through Prober.Signal. When Spawned itself failed there is no Child to prove,
// and the raw group kill below is the one exception to MS-9 (childproof is the
// one way to signal a managed child), for exactly that reason.
func (g *guardHandle) endUnannounced(pgid int) {
	if g.hasChild {
		_ = g.prober.Signal(g.child, syscall.SIGKILL, true)
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// end tells the guard the wrapper is ending normally, closes the pipe and waits
// a moment for the guard to exit. It is safe to call more than once.
func (g *guardHandle) end() {
	g.once.Do(func() {
		_, _ = io.WriteString(g.w, "done\n")
		_ = g.w.Close()
		select {
		case <-g.done:
		case <-time.After(2 * time.Second):
		}
	})
}

// GuardConfig is one guard run.
type GuardConfig struct {
	// In is the pipe from the wrapper.
	In io.Reader
	// Grace is how long goose has after TERM before KILL.
	Grace time.Duration
	// StderrFile is the per-launch file the guard appends its one line to.
	StderrFile string
	// Arg is the whole argument of goose's command line the proof matches.
	Arg string
	// Prober is the childproof Prober; nil means the production one.
	Prober *childproof.Prober
}

// RunGuard is the guard. It returns when the wrapper says "done", when it has
// ended the goose group after the wrapper died, or when it could not prove the
// group and did nothing.
func RunGuard(cfg GuardConfig) int {
	prober := cfg.Prober
	if prober == nil {
		prober = childproof.New()
	}
	r := bufio.NewReader(cfg.In)
	first, err := r.ReadString('\n')
	if err != nil {
		// The wrapper died, or ended, before it knew goose's group. Nothing is
		// known to end (the window the package doc names).
		return 0
	}
	first = strings.TrimSpace(first)
	if first == "done" {
		return 0
	}
	pgid, start, perr := parsePgidLine(first)
	if perr != nil {
		appendLine(cfg.StderrFile, "guard: unreadable line from the wrapper ("+perr.Error()+"); did nothing")
		return 2
	}
	if line, rerr := r.ReadString('\n'); rerr == nil && strings.TrimSpace(line) == "done" {
		return 0
	}

	// The wrapper is gone without saying "done". Prove the group before it is
	// signalled: a recycled pid, or a process that is not goose, is left alone.
	child, err := prober.ProveStart(pgid, start, cfg.Arg)
	if err != nil {
		appendLine(cfg.StderrFile, fmt.Sprintf("guard: could not prove goose group %d (%s); did nothing", pgid, proofReason(err)))
		return 0
	}
	if err := prober.Signal(child, syscall.SIGTERM, true); err != nil {
		appendLine(cfg.StderrFile, fmt.Sprintf("guard: could not signal goose group %d (%s); did nothing", pgid, proofReason(err)))
		return 0
	}
	// Each line says only what the guard observed. The leader is the proven
	// process; the group is probed with signal 0, which sends nothing. A pgid
	// cannot be reused while any member of the group lives, so "no such group"
	// is exact.
	deadline := time.Now().Add(cfg.Grace)
	leaderGone := false
	for {
		if !prober.Same(child) {
			leaderGone = true
			if !groupAlive(pgid) {
				appendLine(cfg.StderrFile, fmt.Sprintf("guard: wrapper gone, sent TERM to goose group %d; group gone within grace", pgid))
				return 0
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if leaderGone {
		appendLine(cfg.StderrFile, fmt.Sprintf("guard: wrapper gone, sent TERM to goose group %d; leader exited, KILL not sent, group children may remain", pgid))
		return 0
	}
	// The leader ignored TERM: KILL through the same proven handle.
	if err := prober.Signal(child, syscall.SIGKILL, true); err != nil {
		appendLine(cfg.StderrFile, fmt.Sprintf("guard: could not signal goose group %d (%s); did nothing", pgid, proofReason(err)))
		return 0
	}
	appendLine(cfg.StderrFile, fmt.Sprintf("guard: wrapper gone, sent TERM then KILL to goose group %d", pgid))
	return 0
}

// groupAlive reports whether any process is left in the group, by signal 0.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func parsePgidLine(line string) (pgid int, start string, err error) {
	f := strings.Fields(line)
	if len(f) != 4 || f[0] != "pgid" || f[2] != "start" {
		return 0, "", errors.New("not a pgid line")
	}
	// A pgid past 32 bits would name another process on darwin, so it is refused.
	n, perr := strconv.ParseInt(f[1], 10, 32)
	if perr != nil {
		return 0, "", errors.New("bad process group number")
	}
	return int(n), f[3], nil
}

// proofReason is the short reason a proof or signal failed, without the pid.
func proofReason(err error) string {
	var mm *childproof.MismatchError
	if errors.As(err, &mm) {
		return mm.Reason
	}
	return "refused"
}

// appendLine appends one line to the stderr file, best effort.
func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}
