package daemon

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/pty"
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
// entry, or an unset or false switch, is off.
func attachGateEnabled(cl *config.Cluster) bool {
	return cl != nil && cl.AllowRemoteAttach
}

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
	if d.driver.Socket() == "" {
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
	if !s.attachEnabled() {
		_ = newCh.Reject(ssh.Prohibited, errRemoteAttachNotEnabled)
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
	p, err := pty.Open()
	if err != nil {
		log.Printf("ssh: attach: %v", err)
		return false
	}
	if err := p.Resize(clampSize(pr.Rows), clampSize(pr.Cols)); err != nil {
		log.Printf("ssh: attach: %v", err)
		_ = p.Master.Close()
		_ = p.Slave.Close()
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
	return r.pty.Resize(clampSize(wc.Rows), clampSize(wc.Cols)) == nil
}

// clampSize fits a client-sent size into the uint16 the pty holds.
func clampSize(n uint32) uint16 {
	if n > 0xffff {
		return 0xffff
	}
	return uint16(n)
}

// attachEnabled says whether this listener serves a terminal: the test seam
// when it is set, the daemon's switch otherwise. The switch is read once at
// daemon start, so a change needs a restart.
func (s *SSHServer) attachEnabled() bool {
	if s.attachGate != nil {
		return s.attachGate()
	}
	return s.daemon != nil && s.daemon.attachAllowed.Load()
}

// loadRemoteAttach sets the daemon's attach switch from the cluster entry whose
// socket is ours, as loadCredentialReveal does for its own switch. An
// unreadable config leaves it off.
func (d *Daemon) loadRemoteAttach(socketPath string) {
	on := false
	if cfg, err := config.Load(); configLoadUsable(err) && cfg != nil {
		on = attachGateEnabled(cfg.ClusterForSocket(socketPath))
	}
	d.attachAllowed.Store(on)
}
