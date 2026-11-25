package daemon

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/config"
)

// attachChannelType is the SSH channel type that carries a terminal to one
// desk of this daemon's tmux server. The open payload is an attachOpen.
const attachChannelType = "marvel-attach"

// attachOpen is the payload of an attach channel open.
type attachOpen struct {
	Desk string
}

// attachPtyReq is the payload of a pty-req request (RFC 4254, section 6.2).
type attachPtyReq struct {
	Term          string
	Cols, Rows    uint32
	Width, Height uint32
	Modes         string
}

// attachWindowChange is the payload of a window-change request (RFC 4254,
// section 6.7).
type attachWindowChange struct {
	Cols, Rows    uint32
	Width, Height uint32
}

// attachDeskPattern is the only shape of desk that reaches a command line:
// the digits of a tmux pane id.
var attachDeskPattern = regexp.MustCompile(`^[0-9]{1,9}$`)

// errRemoteAttachNotEnabled is the fixed reason an attach open and a pty
// request are refused with while the gate is off.
const errRemoteAttachNotEnabled = "remote attach is not enabled: set allow_remote_attach: true on this cluster's entry in the config and restart the daemon"

// attachGateEnabled reads the cluster entry's allow_remote_attach switch. No
// entry, or an unset or false switch, is off. Not read yet.
func attachGateEnabled(cl *config.Cluster) bool {
	return false
}

// loadRemoteAttach sets the daemon's attach switch from the cluster entry whose
// socket is ours. Not read yet.
func (d *Daemon) loadRemoteAttach(socketPath string) {}

// attachKillGrace is how long a command that ignores SIGHUP gets before it
// is killed.
const attachKillGrace = 2 * time.Second

// resolveAttachDesk names the tmux socket of the daemon's own server for a
// desk that is a pane id of exactly one of its session rows, and refuses any
// other. The socket is the driver's, never a value a client supplied.
func (d *Daemon) resolveAttachDesk(desk string) (string, error) {
	matches := 0
	for _, s := range d.store.ListSessions() {
		if strings.TrimPrefix(s.PaneID, "%") == desk {
			matches++
		}
	}
	switch {
	case matches == 0:
		return "", fmt.Errorf("no session has desk %s on this daemon", desk)
	case matches > 1:
		return "", fmt.Errorf("desk %s names more than one session row on this daemon", desk)
	}
	if d.driver == nil || d.driver.Socket() == "" {
		return "", errors.New("this daemon has no tmux server to attach to")
	}
	return d.driver.Socket(), nil
}

// resolveDesk answers an attach open for desk, through the test seam when it
// is set and the daemon's rows otherwise.
func (s *SSHServer) resolveDesk(desk string) (string, error) {
	if s.attachResolve != nil {
		return s.attachResolve(desk)
	}
	if s.daemon == nil {
		return "", errors.New("no daemon behind this listener")
	}
	return s.daemon.resolveAttachDesk(desk)
}

// attachCmd builds the command the channel runs on its pty.
func (s *SSHServer) attachCmd(socket, desk string) *exec.Cmd {
	if s.attachCommand != nil {
		return s.attachCommand(socket, desk)
	}
	return exec.Command("tmux", "-L", socket, "attach", "-t", "%"+desk)
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

// handleAttach answers one attach channel open. It runs in its own goroutine
// so a live terminal never holds up the rest of the connection.
//
// The scope check comes first, before the payload is read or a desk is
// resolved: only an admin key gets a terminal. The desk is digits only and
// must be a row of this daemon; the socket comes from the daemon.
func (s *SSHServer) handleAttach(newCh ssh.NewChannel, c caller) {
	if c.scope != ScopeAdmin {
		_ = newCh.Reject(ssh.Prohibited, "attach needs an admin key")
		return
	}
	var open attachOpen
	if err := ssh.Unmarshal(newCh.ExtraData(), &open); err != nil || !attachDeskPattern.MatchString(open.Desk) {
		_ = newCh.Reject(ssh.Prohibited, "attach needs a desk number")
		return
	}
	socket, err := s.resolveDesk(open.Desk)
	if err != nil {
		_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := newCh.Accept()
	if err != nil {
		log.Printf("ssh: accept attach channel: %v", err)
		return
	}
	log.Printf("ssh: attach desk %s for %s", open.Desk, c.fingerprint)
	s.runAttach(ch, reqs, socket, open.Desk)
}

// attachRun is the state of one attach channel: its pty and its command.
type attachRun struct {
	pty  *ptyPair
	term string
	cmd  *exec.Cmd
	once sync.Once
}

// teardown stops the command and closes the pty, once. Closing the master
// hangs the terminal up; the process group is signalled too, and killed after
// a grace period if it holds on. Wait runs in the waiter goroutine, so the
// process is reaped.
func (r *attachRun) teardown() {
	r.once.Do(func() {
		if r.cmd != nil && r.cmd.Process != nil {
			pid := r.cmd.Process.Pid
			_ = syscall.Kill(-pid, syscall.SIGHUP)
			time.AfterFunc(attachKillGrace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		}
		if r.pty != nil {
			_ = r.pty.master.Close()
		}
	})
}

// runAttach serves the requests of an accepted attach channel. Only pty-req,
// shell and window-change are answered; every other request gets false, so
// nothing the RPC channels accept (exec, env, a subsystem, agent forwarding)
// reaches the command.
func (s *SSHServer) runAttach(ch ssh.Channel, reqs <-chan *ssh.Request, socket, desk string) {
	run := &attachRun{}
	waited := make(chan struct{})
	defer func() {
		run.teardown()
		if run.cmd != nil {
			<-waited
		}
		_ = ch.Close()
	}()

	for req := range reqs {
		ok := false
		switch req.Type {
		case "pty-req":
			ok = run.grantPty(req.Payload)
		case "shell":
			if run.pty != nil && run.cmd == nil {
				ok = s.startAttach(ch, run, socket, desk, waited)
			}
		case "window-change":
			ok = run.windowChange(req.Payload)
		}
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
	}
}

func (r *attachRun) grantPty(payload []byte) bool {
	if r.pty != nil {
		return false
	}
	var pr attachPtyReq
	if err := ssh.Unmarshal(payload, &pr); err != nil {
		return false
	}
	p, err := openPty()
	if err != nil {
		log.Printf("ssh: attach: %v", err)
		return false
	}
	if err := p.resize(clampSize(pr.Rows), clampSize(pr.Cols)); err != nil {
		log.Printf("ssh: attach: %v", err)
		_ = p.master.Close()
		_ = p.slave.Close()
		return false
	}
	r.pty, r.term = p, pr.Term
	return true
}

func (r *attachRun) windowChange(payload []byte) bool {
	if r.pty == nil {
		return false
	}
	var wc attachWindowChange
	if err := ssh.Unmarshal(payload, &wc); err != nil {
		return false
	}
	return r.pty.resize(clampSize(wc.Rows), clampSize(wc.Cols)) == nil
}

// clampSize fits a client-sent size into the uint16 the pty holds.
func clampSize(n uint32) uint16 {
	if n > 0xffff {
		return 0xffff
	}
	return uint16(n)
}

// startAttach starts the command on the pty and bridges it to the channel.
// The channel ends when the command's output does, and the command ends when
// the channel closes or the client stops sending.
func (s *SSHServer) startAttach(ch ssh.Channel, run *attachRun, socket, desk string, waited chan<- struct{}) bool {
	cmd := s.attachCmd(socket, desk)
	if cmd.Env == nil {
		cmd.Env = attachEnviron(run.term)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = run.pty.slave, run.pty.slave, run.pty.slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		log.Printf("ssh: attach desk %s: start: %v", desk, err)
		_ = run.pty.master.Close()
		_ = run.pty.slave.Close()
		return false
	}
	_ = run.pty.slave.Close()
	run.cmd = cmd

	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		_, _ = io.Copy(ch, run.pty.master)
	}()
	go func() {
		_, _ = io.Copy(run.pty.master, ch)
		run.teardown()
	}()
	go func() {
		defer close(waited)
		_ = cmd.Wait()
		<-outDone
		_ = ch.Close()
	}()
	return true
}
