package daemon

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
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

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// attachRig is a daemon SSH server with the attach seams set, and a client
// connected with a key of the given scope. spawned records each command the
// attach channel built, as socket and desk.
type attachRig struct {
	srv      *SSHServer
	client   *ssh.Client
	identity string // the client's private key, as an OpenSSH PEM file
	mu       sync.Mutex
	spawned  [][2]string
	resolve  int
}

func (r *attachRig) spawnedCommands() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.spawned...)
}

func (r *attachRig) resolveCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolve
}

// newAttachRig starts the server with a scratch HOME. command builds what the
// attach channel runs; the desk resolves to socket "marvel-test" unless it is
// 99, which the daemon's rows do not hold.
func newAttachRig(t *testing.T, scope Scope, command func(socket, desk string) *exec.Cmd) *attachRig {
	t.Helper()
	return newAttachRigGate(t, scope, true, command)
}

// newAttachRigGate is newAttachRig with the daemon's attach switch set to
// gate: the tests of the attach channel run with it on, and the gate's own
// tests run with it off.
func newAttachRigGate(t *testing.T, scope Scope, gate bool, command func(socket, desk string) *exec.Cmd) *attachRig {
	t.Helper()
	return newAttachRigDaemon(t, scope, gate, nil, command)
}

// newAttachRigDaemon is newAttachRigGate with a daemon behind the listener,
// for a test that opens an RPC channel: the RPC reader needs one to close
// cleanly. A nil daemon serves the attach channel alone.
func newAttachRigDaemon(t *testing.T, scope Scope, gate bool, d *Daemon, command func(socket, desk string) *exec.Cmd) *attachRig {
	t.Helper()
	return newAttachRigSeam(t, scope, &gate, d, command)
}

// newAttachRigSeam is newAttachRigDaemon where a nil gate leaves the
// attachGate seam unset, so the listener reads the daemon's own switch, the
// production branch of attachEnabled.
func newAttachRigSeam(t *testing.T, scope Scope, gate *bool, d *Daemon, command func(socket, desk string) *exec.Cmd) *attachRig {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	pub, signer, identity := newClientKey(t)
	if err := AddAuthorizedKey(pub, "attach-test", scope); err != nil {
		t.Fatalf("authorize key: %v", err)
	}
	srv, err := newSSHServer(d)
	if err != nil {
		t.Fatalf("newSSHServer: %v", err)
	}
	r := &attachRig{srv: srv, identity: identity}
	if gate != nil {
		on := *gate
		srv.attachGate = func() bool { return on }
	}
	srv.attachResolve = func(desk string) (string, error) {
		r.mu.Lock()
		r.resolve++
		r.mu.Unlock()
		if desk == "99" {
			return "", errors.New("no session has desk 99")
		}
		return "marvel-test", nil
	}
	srv.attachCommand = func(socket, desk string) *exec.Cmd {
		r.mu.Lock()
		r.spawned = append(r.spawned, [2]string{socket, desk})
		r.mu.Unlock()
		return command(socket, desk)
	}
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)
	cfg := &ssh.ClientConfig{
		User:            "op",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // loopback listener started by this test
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", srv.Addr().String(), cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	r.client = client
	return r
}

func shellCommand(script string) func(string, string) *exec.Cmd {
	return func(string, string) *exec.Cmd { return exec.Command("/bin/sh", "-c", script) }
}

// openAttach opens an attach channel for desk the way the client does.
func openAttach(c *ssh.Client, desk string) (ssh.Channel, <-chan *ssh.Request, error) {
	return c.OpenChannel(attachChannelType, ssh.Marshal(attachOpen{Desk: desk}))
}

type ptyReq struct {
	Term          string
	Cols, Rows    uint32
	Width, Height uint32
	Modes         string
}

type winChange struct {
	Cols, Rows    uint32
	Width, Height uint32
}

// startAttach opens the channel, asks for a pty and starts the command.
func startAttach(t *testing.T, r *attachRig) ssh.Channel {
	t.Helper()
	ch, reqs, err := openAttach(r.client, "23")
	if err != nil {
		t.Fatalf("open attach channel for desk 23: %v", err)
	}
	go ssh.DiscardRequests(reqs)
	t.Cleanup(func() { _ = ch.Close() })
	ok, err := ch.SendRequest("pty-req", true, ssh.Marshal(ptyReq{Term: "xterm", Cols: 80, Rows: 24}))
	if err != nil || !ok {
		t.Fatalf("pty-req: ok=%v err=%v, want it accepted", ok, err)
	}
	ok, err = ch.SendRequest("shell", true, nil)
	if err != nil || !ok {
		t.Fatalf("shell: ok=%v err=%v, want it accepted", ok, err)
	}
	return ch
}

// readUntil reads from ch until the output holds want or the deadline passes.
func readUntil(t *testing.T, ch io.Reader, want string, d time.Duration) {
	t.Helper()
	var mu sync.Mutex
	var got bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ch.Read(buf)
			mu.Lock()
			got.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := got.String()
		mu.Unlock()
		if strings.Contains(s, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("output %q never held %q within %v", got.String(), want, d)
}

func requireOpenRefused(t *testing.T, err error, what string) {
	t.Helper()
	var oce *ssh.OpenChannelError
	if !errors.As(err, &oce) {
		t.Fatalf("%s: open error = %v (%T), want an *ssh.OpenChannelError", what, err, err)
	}
	if oce.Reason == ssh.UnknownChannelType {
		t.Fatalf("%s: refused as an unknown channel type, want the attach channel known and this open refused on its merits (%q)", what, oce.Message)
	}
}

// A key whose scope is not admin never gets a terminal: the open is refused
// as prohibited before anything is accepted or spawned.
func TestAttachChannelRefusesAKeyThatIsNotAdmin(t *testing.T) {
	r := newAttachRig(t, ScopeCredentialPush, shellCommand("exit 0"))
	_, _, err := openAttach(r.client, "23")
	if err == nil {
		t.Fatal("a credential-push key opened an attach channel")
	}
	requireOpenRefused(t, err, "credential-push key")
	var oce *ssh.OpenChannelError
	if errors.As(err, &oce) && oce.Reason != ssh.Prohibited {
		t.Errorf("reason = %v, want Prohibited", oce.Reason)
	}
	if n := len(r.spawnedCommands()); n != 0 {
		t.Errorf("spawned %d commands for a refused key, want 0", n)
	}
	if n := r.resolveCalls(); n != 0 {
		t.Errorf("resolved %d desks for a refused key, want 0", n)
	}
}

// The desk reaches a command line, so only digits pass: anything else is
// refused at open and never reaches the resolver or the command builder.
func TestAttachChannelRefusesADeskThatIsNotDigits(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("exit 0"))
	for _, desk := range []string{"", "23;id", "-t", "%23", "2 3", "../x", "23\n"} {
		_, _, err := openAttach(r.client, desk)
		if err == nil {
			t.Errorf("desk %q opened an attach channel", desk)
			continue
		}
		requireOpenRefused(t, err, "desk "+strconv.Quote(desk))
	}
	if n := len(r.spawnedCommands()); n != 0 {
		t.Errorf("spawned %d commands for refused desks, want 0", n)
	}
	if n := r.resolveCalls(); n != 0 {
		t.Errorf("resolved %d malformed desks, want 0", n)
	}
}

// A desk the daemon's own rows do not hold is refused, and the socket the
// command gets is the daemon's, never anything the client sent.
func TestAttachChannelResolvesTheDeskAgainstTheDaemon(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("echo ready; exec cat"))
	_, _, err := openAttach(r.client, "99")
	if err == nil {
		t.Fatal("desk 99 is not a row of this daemon and opened anyway")
	}
	requireOpenRefused(t, err, "desk 99")
	if n := len(r.spawnedCommands()); n != 0 {
		t.Errorf("spawned %d commands for an unknown desk, want 0", n)
	}

	ch := startAttach(t, r)
	readUntil(t, ch, "ready", 5*time.Second)
	got := r.spawnedCommands()
	if len(got) != 1 || got[0] != [2]string{"marvel-test", "23"} {
		t.Errorf("command built for %v, want one build with socket marvel-test and desk 23", got)
	}
}

// Bytes written to the channel reach the command on its pty, and its output
// comes back.
func TestAttachChannelBridgesTerminalBytes(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("exec cat"))
	ch := startAttach(t, r)
	if _, err := ch.Write([]byte("hello-desk\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	readUntil(t, ch, "hello-desk", 5*time.Second)
}

// A window-change on the channel resizes the pty the command runs on.
func TestAttachChannelForwardsWindowChange(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("read x; stty size"))
	ch := startAttach(t, r)
	time.Sleep(300 * time.Millisecond)
	if _, err := ch.SendRequest("window-change", false, ssh.Marshal(winChange{Cols: 132, Rows: 50})); err != nil {
		t.Fatalf("window-change: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := ch.Write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	readUntil(t, ch, "50 132", 5*time.Second)
}

// Closing the channel stops the command and reaps it: the pid is gone, not a
// zombie, which kill -0 still reports as present.
func TestAttachChannelCloseLeavesNoProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := newAttachRig(t, ScopeAdmin, shellCommand("echo $$ > "+pidFile+"; exec sleep 300"))
	ch := startAttach(t, r)

	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				pid = n
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the attach command never wrote its pid")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if err := ch.Close(); err != nil {
		t.Fatalf("close channel: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d still present 5s after the channel closed", pid)
}

// One live attach does not hold up the rest of the connection: another channel
// open on the same connection is answered while the attach runs.
func TestAttachChannelDoesNotBlockTheConnection(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("exec cat"))
	startAttach(t, r)

	done := make(chan error, 1)
	go func() {
		_, _, err := r.client.OpenChannel("no-such-type", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an unknown channel type opened")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a second channel open got no answer 3s after an attach started")
	}
}

// newClientKey returns a fresh ed25519 key as an authorized_keys line, a
// signer for the client side and the path of its private key file.
func newClientKey(t *testing.T) ([]byte, ssh.Signer, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	identity := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(identity, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	return ssh.MarshalAuthorizedKey(sshPub), signer, identity
}

// openBare opens an attach channel for desk 23 without any request on it.
func openBare(t *testing.T, r *attachRig) ssh.Channel {
	t.Helper()
	ch, reqs, err := openAttach(r.client, "23")
	if err != nil {
		t.Fatalf("open attach channel for desk 23: %v", err)
	}
	go ssh.DiscardRequests(reqs)
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

// The attach channel answers only pty-req, shell and window-change. Every
// other request is refused with a reply of false, never the blanket yes the
// RPC channels give, so exec, env, a subsystem or agent forwarding never
// reaches a command.
func TestAttachChannelRefusesOtherRequests(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("exec cat"))
	ch := startAttach(t, r)
	for _, typ := range []string{"exec", "env", "subsystem", "auth-agent-req@openssh.com", "x11-req", "signal"} {
		ok, err := ch.SendRequest(typ, true, nil)
		if err != nil {
			t.Fatalf("%s: send: %v", typ, err)
		}
		if ok {
			t.Errorf("%s request was accepted on the attach channel, want a reply of false", typ)
		}
	}
	ok, err := ch.SendRequest("shell", true, nil)
	if err != nil {
		t.Fatalf("second shell: send: %v", err)
	}
	if ok {
		t.Error("a second shell request was accepted, want a reply of false")
	}
}

// The command needs its pty first: a shell request before pty-req is refused.
func TestAttachChannelRefusesShellBeforePty(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("exec cat"))
	ch := openBare(t, r)
	ok, err := ch.SendRequest("shell", true, nil)
	if err != nil {
		t.Fatalf("shell: send: %v", err)
	}
	if ok {
		t.Error("shell before pty-req was accepted, want a reply of false")
	}
	if n := len(r.spawnedCommands()); n != 0 {
		t.Errorf("spawned %d commands before a pty was granted, want 0", n)
	}
}

// The daemon-backed resolver reads the daemon's own session rows and its own
// tmux socket: a desk that is a row's pane gives the driver's socket, and a
// desk that is not a row is refused. Nothing a client sends names a socket.
func TestResolveAttachDeskReadsTheDaemonsRows(t *testing.T) {
	d := statusDaemon(t)
	if err := d.store.CreateSession(&api.Session{Workspace: "ws", Name: "s1", PaneID: "%23"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, err := d.resolveAttachDesk("23")
	if err != nil {
		t.Fatalf("resolveAttachDesk(23): %v", err)
	}
	if want := d.driver.Socket(); got != want || want == "" {
		t.Errorf("socket = %q, want the driver's %q", got, want)
	}
	if sock, err := d.resolveAttachDesk("24"); err == nil {
		t.Errorf("desk 24 is no row of this daemon and resolved to %q", sock)
	}
}

// attachOut is a goroutine-safe buffer for what the client writes to the
// terminal.
type attachOut struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *attachOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *attachOut) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *attachOut) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(o.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("client output %q never held %q within 5s", o.String(), want)
}

// runClientAttach drives daemonrpc.Attach against the rig's server and returns
// the pieces a test reads and writes.
func runClientAttach(t *testing.T, r *attachRig, resize <-chan daemonrpc.Size) (in *io.PipeWriter, out *attachOut, done <-chan error) {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	out = &attachOut{}
	res := make(chan error, 1)
	addr := "mrvl://op@" + r.srv.Addr().String()
	go func() {
		res <- daemonrpc.Attach(addr, "23",
			daemonrpc.DialOptions{Identity: r.identity, TrustUnknownHost: true},
			daemonrpc.AttachIO{In: pr, Out: out, Rows: 24, Cols: 80, Resize: resize})
	}()
	return pw, out, res
}

func waitAttachDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Attach returned %v, want nil after the command ended", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not return within 5s of the command ending")
	}
}

// End to end: the client's own pty-req and open payload are the ones the
// server reads. The command reports its pty size, then echoes what it is
// sent; Ctrl-D ends it and Attach returns nil.
func TestDaemonrpcAttachRoundTripsBytesAndInitialSize(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("echo ready; stty size; exec cat"))
	in, out, done := runClientAttach(t, r, nil)
	out.waitFor(t, "24 80")
	if _, err := in.Write([]byte("ping-desk\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out.waitFor(t, "ping-desk")
	if _, err := in.Write([]byte{4}); err != nil {
		t.Fatalf("write ctrl-d: %v", err)
	}
	waitAttachDone(t, done)
}

// End to end: a size sent on Resize reaches the pty as a window-change.
func TestDaemonrpcAttachForwardsAResize(t *testing.T) {
	r := newAttachRig(t, ScopeAdmin, shellCommand("echo ready; read x; stty size"))
	resize := make(chan daemonrpc.Size, 1)
	in, out, done := runClientAttach(t, r, resize)
	out.waitFor(t, "ready")
	resize <- daemonrpc.Size{Rows: 50, Cols: 132}
	time.Sleep(300 * time.Millisecond)
	if _, err := in.Write([]byte("\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out.waitFor(t, "50 132")
	waitAttachDone(t, done)
}

// A command that is already reaped is never signalled: the kill timer is not
// armed after the reap, and a pending one is stopped (architect K1).
func TestAttachTeardownDoesNotKillAReapedCommand(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := &attachRun{cmd: cmd}
	_ = cmd.Wait()
	r.markReaped()
	r.teardown()
	if r.kill != nil {
		t.Error("teardown armed a kill timer for a command that was already reaped")
	}

	live := exec.Command("/bin/sh", "-c", "exec sleep 30")
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Process.Kill(); _ = live.Wait() })
	r2 := &attachRun{cmd: live}
	r2.teardown()
	if r2.kill == nil {
		t.Fatal("teardown of a live command armed no kill timer")
	}
	r2.markReaped()
	if r2.kill.Stop() {
		t.Error("the pending kill timer was still armed after the command was reaped")
	}
}

// A pty granted and a channel closed before any shell: teardown closes both
// ends of the pty, not only the master (review note 1 on marvel#1617).
func TestAttachTeardownWithNoShellClosesTheSlaveToo(t *testing.T) {
	r := &attachRun{}
	if !r.grantPty(ssh.Marshal(attachPtyReq{Term: "xterm", Cols: 80, Rows: 24})) {
		t.Fatal("grantPty refused a plain pty request")
	}
	r.teardown()
	if _, err := r.pty.Slave.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("slave write after teardown = %v, want os.ErrClosed (the slave was left open)", err)
	}
}
