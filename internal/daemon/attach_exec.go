package daemon

import (
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/pty"
)

// This file holds every process the attach channel starts. It is the second
// file the daemon's exec guard allows to import os/exec (director ruling
// 01M88CD904, item 1), and attachCommand is the one exec call in it, with its
// arguments pinned by the guard. The channel code in sshattach.go checks the
// scope, the gate and the desk before anything here runs.

// attachCommandFunc builds the command the channel runs on its pty. A test
// replaces it; nothing else does.
type attachCommandFunc func(socket, desk string) *exec.Cmd

// attachCommand is the one process an attach channel starts: the tmux client
// for one desk of this daemon's own server. The socket is the name the
// resolver returned and the desk is digits only, so neither is a flag.
func attachCommand(socket, desk string) *exec.Cmd {
	return exec.Command("tmux", "-L", socket, "attach", "-t", "%"+desk)
}

// attachKillGrace is how long a command that ignores SIGHUP gets before it
// is killed.
const attachKillGrace = 2 * time.Second

// attachCmd builds the command the channel runs on its pty.
func (s *SSHServer) attachCmd(socket, desk string) *exec.Cmd {
	if s.attachCommand != nil {
		return s.attachCommand(socket, desk)
	}
	return attachCommand(socket, desk)
}

// attachEnvAllow is what the attach command inherits from the daemon: enough
// to find tmux and its socket directory, and nothing a seat or the bus
// carries. TMUX is absent on purpose, so the client never believes it is
// nested.
var attachEnvAllow = []string{"PATH", "HOME", "USER", "LOGNAME", "LANG", "TMUX_TMPDIR", "TMPDIR"}

func attachEnviron(term string) []string {
	var env []string
	for _, k := range attachEnvAllow {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if term == "" {
		term = "xterm"
	}
	return append(env, "TERM="+term)
}

// attachRun is the state of one attach channel: its pty and its command.
type attachRun struct {
	pty  *pty.Pair
	term string
	cmd  *exec.Cmd
	once sync.Once

	mu     sync.Mutex
	reaped bool
	kill   *time.Timer
}

// teardown stops the command and closes the pty, once. Closing the master
// makes the kernel hang the terminal up, which sends SIGHUP to the command's
// session, so no signal of ours is needed for that. A command that holds on
// is killed after a grace period, unless it has been reaped by then: a pid
// that has been reaped may belong to another process group by now.
func (r *attachRun) teardown() {
	r.once.Do(func() {
		if r.pty != nil {
			_ = r.pty.Master.Close()
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.cmd != nil && r.cmd.Process != nil && !r.reaped {
			pid := r.cmd.Process.Pid
			r.kill = time.AfterFunc(attachKillGrace, func() {
				r.mu.Lock()
				defer r.mu.Unlock()
				if !r.reaped {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
			})
		}
	})
}

// markReaped records that the command has been waited for and stops a pending
// kill, so the signal never reaches a recycled pid.
func (r *attachRun) markReaped() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reaped = true
	if r.kill != nil {
		r.kill.Stop()
	}
}

// startAttach starts the command on the pty and bridges it to the channel.
// The channel ends when the command's output does, and the command ends when
// the channel closes or the client stops sending.
func (s *SSHServer) startAttach(ch ssh.Channel, run *attachRun, socket, desk string, waited chan<- struct{}) bool {
	cmd := s.attachCmd(socket, desk)
	if cmd.Env == nil {
		cmd.Env = attachEnviron(run.term)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = run.pty.Slave, run.pty.Slave, run.pty.Slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		log.Printf("ssh: attach desk %s: start: %v", desk, err)
		_ = run.pty.Master.Close()
		_ = run.pty.Slave.Close()
		return false
	}
	_ = run.pty.Slave.Close()
	run.cmd = cmd

	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		_, _ = io.Copy(ch, run.pty.Master)
	}()
	go func() {
		_, _ = io.Copy(run.pty.Master, ch)
		run.teardown()
	}()
	go func() {
		defer close(waited)
		_ = cmd.Wait()
		run.markReaped()
		<-outDone
		_ = ch.Close()
	}()
	return true
}
