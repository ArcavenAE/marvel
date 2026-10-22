package daemonrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

// DefaultMRVLPort is the default port for the mrvl:// protocol.
const DefaultMRVLPort = "6785"

// ListenNetwork returns "tcp" if the address looks like host:port,
// otherwise "unix". The host:port rule itself is paths.IsTCPAddr, which
// is the single definition shared with the client and with the socket
// length check.
func ListenNetwork(addr string) string {
	if paths.IsTCPAddr(addr) {
		return "tcp"
	}
	return "unix"
}

// IsMRVL returns true if the address is a mrvl:// URL.
func IsMRVL(addr string) bool {
	return strings.HasPrefix(addr, "mrvl://")
}

// IsSSH returns true if the address is an ssh:// URL.
func IsSSH(addr string) bool {
	return strings.HasPrefix(addr, "ssh://")
}

// DialOptions controls how the client connects to a marvel daemon.
type DialOptions struct {
	// Identity is an optional private key file used for SSH auth. When
	// set, it takes precedence over SSH_AUTH_SOCK and default key files.
	Identity string
	// TrustUnknownHost, when true, auto-adds any unknown host key to
	// ~/.marvel/known_hosts without prompting, printing its fingerprint
	// first. Used by `marvel keys trust --yes`; do not set for ordinary
	// RPC calls.
	TrustUnknownHost bool
	// StrictHostKey, when true, refuses unknown hosts without prompting.
	// Intended for non-interactive scripts. When false and the caller
	// is on a TTY, marvel prompts; when false and off-TTY, marvel
	// refuses with a pointer to `marvel keys trust --yes`.
	StrictHostKey bool
	// OfferOneKey, when true, offers exactly one key: the Identity file, or
	// marvel's own client key when Identity is empty. The SSH agent and the
	// keys under ~/.ssh are never read, so a fan-out cannot offer one
	// cluster's credentials to another. With no loadable key the dial fails
	// with a NoIdentityError before any connection.
	OfferOneKey bool
	// Timeout bounds the whole exchange when positive: one timer, armed
	// before the dial, over connect, handshake, auth, request and reply,
	// which closes the connection when it fires (see exchange). Zero keeps
	// the historical behavior of no bound; a best-effort sender sets it.
	Timeout time.Duration

	// ex is the timer of the call in progress, set by SendRequestWith.
	ex *exchange
}

// SendRequest sends a request to the daemon and returns the response,
// using default auth (SSH_AUTH_SOCK or ~/.ssh/*).
//
// Address formats:
//
//	~/.marvel/run/marvel.sock                 → Unix socket (default, local)
//	mrvl://host                               → daemon SSH server on port 6785
//	mrvl://user@host:port                     → daemon SSH server on custom port
//	ssh://user@host/path/to/marvel.sock       → tunnel through sshd to Unix socket
//	tcp://host:port                           → bare TCP (advanced use)
func SendRequest(socketPath string, req Request) (*Response, error) {
	return SendRequestWith(socketPath, req, DialOptions{})
}

// SendRequestWith sends a request using the supplied dial options. Use
// this when the caller has a per-cluster identity key or known_hosts
// file it wants to thread through.
func SendRequestWith(socketPath string, req Request, opts DialOptions) (*Response, error) {
	ex := startExchange(opts.Timeout)
	defer ex.stop()
	opts.ex = ex
	conn, err := dialDaemonWith(socketPath, opts)
	if err != nil {
		return nil, ex.wrap(err)
	}
	defer func() { _ = conn.Close() }()
	ex.enter(phaseReply, conn)

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, ex.wrap(fmt.Errorf("send request: %w", err))
	}

	var resp Response
	limit := maxReplyBytes
	if err := json.NewDecoder(newBoundedReader(conn, limit)).Decode(&resp); err != nil {
		if errors.Is(err, errReplyTooLarge) {
			return nil, &BadReplyError{Err: fmt.Errorf("read response: the reply passed the %d-byte bound", limit)}
		}
		if err := ex.wrap(err); errors.As(err, new(*TimeoutError)) {
			return nil, err
		}
		return nil, &BadReplyError{Err: fmt.Errorf("read response: %w", err)}
	}
	return &resp, nil
}

// ErrWatchUnsupported is returned by WatchEventsWith when the daemon
// predates events.watch. The caller falls back to polling the events
// method with a since_seq cursor.
var ErrWatchUnsupported = errors.New("daemon does not support events.watch; falling back to polling")

// WatchEventsWith opens an events.watch stream against the daemon and
// calls fn for every message until fn returns an error, the daemon
// closes the stream, or the dial fails. The first message is the backlog
// (see MethodEventsWatch); later messages carry live deliveries and, when
// the daemon-side watch overflowed, the gap. fn receives the stamped
// Response alongside the decoded batch so the caller can read
// DaemonHome once.
//
// Each message on the stream is held to the same byte bound as a single
// reply (maxReplyBytes); the stream as a whole is not, since it has no end.
//
// A daemon that does not know the method answers the request with an
// unknown-method error, which is returned as ErrWatchUnsupported. The
// daemon closing the connection ends the stream with an error that says
// so; a clean end is fn returning an error of its own, which is
// returned unchanged.
func WatchEventsWith(socketPath string, params json.RawMessage, opts DialOptions, fn func(*Response, EventsBatch) error) error {
	conn, err := dialDaemonWith(socketPath, opts)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if err := json.NewEncoder(conn).Encode(Request{Method: MethodEventsWatch, Params: params}); err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	limit := maxReplyBytes
	br := newBoundedReader(conn, limit)
	dec := json.NewDecoder(br)
	first := true
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			if errors.Is(err, errReplyTooLarge) {
				return &BadReplyError{Err: fmt.Errorf("read event stream: a message passed the %d-byte bound", limit)}
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return errors.New("event stream ended: the daemon closed the connection")
			}
			return fmt.Errorf("read event stream: %w", err)
		}
		br.next(dec.InputOffset())
		if resp.Error != "" {
			if first && strings.HasPrefix(resp.Error, "unknown method") {
				return ErrWatchUnsupported
			}
			return errors.New(resp.Error)
		}
		var b EventsBatch
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &b); err != nil {
				return fmt.Errorf("parse event stream: %w", err)
			}
		}
		if err := fn(&resp, b); err != nil {
			return err
		}
		first = false
	}
}

// dialDaemonWith connects to the daemon. Routes based on address scheme:
//
//	mrvl://host            → embedded SSH server on port 6785
//	mrvl://host:port       → embedded SSH server on custom port
//	ssh://host/path        → tunnel through sshd to Unix socket
//	ssh://host:port        → embedded SSH server (same as mrvl://)
//	tcp://host:port        → bare TCP (advanced)
//	/path/to/socket        → Unix socket (local)
func dialDaemonWith(addr string, opts DialOptions) (net.Conn, error) {
	if IsMRVL(addr) {
		return dialMRVL(addr, opts)
	}
	if IsSSH(addr) {
		return dialSSH(addr, opts)
	}

	// Strip tcp:// prefix for explicit bare-TCP use.
	if strings.HasPrefix(addr, "tcp://") {
		addr = strings.TrimPrefix(addr, "tcp://")
		conn, err := net.DialTimeout("tcp", addr, opts.ex.connectTimeout(0))
		if err != nil {
			return nil, fmt.Errorf("connect to daemon at %s (tcp): %w", addr, dialError(addr, err))
		}
		return conn, nil
	}

	network := ListenNetwork(addr)
	conn, err := net.DialTimeout(network, addr, opts.ex.connectTimeout(0))
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s (%s): %w", addr, network, dialError(addr, err))
	}
	return conn, nil
}

// dialMRVL connects to a daemon's embedded SSH server via the mrvl:// protocol.
// Default port is 6785 if not specified.
func dialMRVL(addr string, opts DialOptions) (net.Conn, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse mrvl address %q: %w", addr, err)
	}

	user := u.User.Username()
	if user == "" {
		user = os.Getenv("USER")
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = DefaultMRVLPort
	}

	return dialSSHDirect(user, host, port, opts)
}

// dialSSH parses an ssh:// URL and dials the daemon's socket through an
// SSH tunnel. Auth prefers opts.Identity, then SSH_AUTH_SOCK, then common
// key files (~/.ssh/id_ed25519, ~/.ssh/id_rsa).
//
// URL formats:
//
//	ssh://user@host/path/to/socket        → SSH to host:22, dial Unix socket
//	ssh://user@host:2222/path/to/socket   → SSH to host:2222, dial Unix socket
//	ssh://user@host:9090                  → SSH to host:22, dial TCP localhost:9090
//	ssh://host/path/to/socket             → SSH as current user
func dialSSH(addr string, opts DialOptions) (net.Conn, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse ssh address %q: %w", addr, err)
	}

	user := u.User.Username()
	if user == "" {
		user = os.Getenv("USER")
	}

	sshHost := u.Hostname()
	sshPort := u.Port()

	// Determine connection mode.
	remotePath := u.Path

	if remotePath != "" && remotePath != "/" {
		// Mode 1: path present → tunnel through sshd to remote Unix socket.
		// Any port in the URL is the SSH port (default 22).
		if sshPort == "" {
			sshPort = "22"
		}
		return dialSSHTunnel(user, sshHost, sshPort, "unix", remotePath, opts)
	}

	// Mode 2: no path → connect directly to daemon's embedded SSH server.
	// The port in the URL is the daemon's SSH server port.
	if sshPort == "" {
		return nil, fmt.Errorf("ssh address %q: need a port for the daemon's SSH server", addr)
	}
	return dialSSHDirect(user, sshHost, sshPort, opts)
}

// dialSSHTunnel connects through a remote sshd to a Unix or TCP socket (mode 1).
func dialSSHTunnel(user, host, sshPort, network, addr string, opts DialOptions) (net.Conn, error) {
	sshConn, err := sshDial(user, host, sshPort, opts)
	if err != nil {
		return nil, err
	}

	conn, err := sshConn.Dial(network, addr)
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh tunnel %s %s via %s: %w", network, addr, host, err)
	}

	return &sshWrappedConn{Conn: conn, sshClient: sshConn}, nil
}

// dialSSHDirect connects to the daemon's embedded SSH server (mode 2).
// Opens a session channel for JSON-RPC instead of tunneling to a socket.
func dialSSHDirect(user, host, port string, opts DialOptions) (net.Conn, error) {
	sshConn, err := sshDial(user, host, port, opts)
	if err != nil {
		return nil, err
	}

	session, err := sshConn.NewSession()
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh open session on %s: %w", host, err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh stdout pipe: %w", err)
	}

	// Start shell so the channel stays open for bidirectional I/O.
	if err := session.Shell(); err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("ssh start shell: %w", err)
	}

	return &sshSessionConn{
		Reader:    stdout,
		Writer:    stdin,
		session:   session,
		sshClient: sshConn,
	}, nil
}

// sshDial connects to an SSH server and completes the handshake. The TCP
// connect and the handshake are separate steps so that a failed connect is
// typed as unreachable or a timeout, and a handshake failure keeps the host
// key or auth error the callbacks returned.
func sshDial(user, host, port string, opts DialOptions) (*ssh.Client, error) {
	addr := net.JoinHostPort(host, port)
	offered := &offeredKeys{}
	config, closeHeld, err := sshClientConfig(user, opts, offered)
	if err != nil {
		return nil, err
	}
	// The agent socket is only needed until auth is done.
	defer closeHeld()
	config.AuthCallback = authRefusedCallback(user, addr, offered)

	tcp, err := net.DialTimeout("tcp", addr, opts.ex.connectTimeout(config.Timeout))
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s@%s:%s: %w", user, host, port, dialError(addr, err))
	}
	// The timer closes the TCP connection if it fires during the handshake
	// or auth; auth starts once the host key is accepted.
	opts.ex.enter(phaseHandshake, tcp)
	verify := config.HostKeyCallback
	config.HostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := verify(hostname, remote, key); err != nil {
			return err
		}
		opts.ex.enter(phaseAuth, tcp)
		return nil
	}
	c, chans, reqs, err := ssh.NewClientConn(tcp, addr, config)
	if err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("ssh connect %s@%s:%s: %w", user, host, port, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	opts.ex.enter(phaseReply, client)
	return client, nil
}

// offeredKeys records the fingerprint of each key handed to x/crypto to
// offer during one handshake (see AuthRefusedError.Offered). A handshake is sequential, so it needs no lock.
type offeredKeys struct {
	fingerprints []string
}

// record wraps a signer source so each signer it yields is recorded when
// x/crypto asks for it, which is when the key is offered. A nil recorder
// records nothing.
func (o *offeredKeys) record(signers func() ([]ssh.Signer, error)) ssh.AuthMethod {
	if o == nil {
		return ssh.PublicKeysCallback(signers)
	}
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		ss, err := signers()
		for _, s := range ss {
			o.fingerprints = append(o.fingerprints, ssh.FingerprintSHA256(s.PublicKey()))
		}
		return ss, err
	})
}

// staticSigners is a signer source that always yields the one signer.
func staticSigners(s ssh.Signer) func() ([]ssh.Signer, error) {
	return func() ([]ssh.Signer, error) { return []ssh.Signer{s}, nil }
}

// authRefusedCallback returns an AuthCallback that turns x/crypto's bare
// "unable to authenticate" string into an AuthRefusedError. Every method
// sshAuthMethodsFor builds is publickey, and x/crypto tries a method name
// once, so once publickey has been tried, or the server does not allow it,
// nothing is left to offer. Otherwise it returns (nil, nil), which leaves
// the choice of the next method to x/crypto.
func authRefusedCallback(user, addr string, offered *offeredKeys) ssh.ClientAuthCallback {
	return func(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
		if slices.Contains(ctx.TriedMethods, "publickey") || !slices.Contains(ctx.AllowedMethods, "publickey") {
			return nil, &AuthRefusedError{
				User:    user,
				Addr:    addr,
				Offered: slices.Clone(offered.fingerprints),
				Allowed: ctx.AllowedMethods,
			}
		}
		return nil, nil
	}
}

// sshClientConfig builds the client config. The returned func closes the
// connections building it opened (the agent socket); the caller runs it when
// auth is over. The exchange timer closes them too if it fires first.
func sshClientConfig(user string, opts DialOptions, offered *offeredKeys) (*ssh.ClientConfig, func(), error) {
	var held []io.Closer
	hold := func(c io.Closer) {
		held = append(held, c)
		opts.ex.hold(c)
	}
	closeHeld := func() {
		for _, c := range held {
			_ = c.Close()
		}
	}
	var methods []ssh.AuthMethod
	var err error
	if opts.OfferOneKey {
		methods, err = oneKeyAuthMethods(opts.Identity, offered)
	} else {
		methods, err = sshAuthMethodsFor(opts.Identity, offered, hold)
	}
	if err != nil {
		closeHeld()
		return nil, nil, err
	}
	if len(methods) == 0 {
		closeHeld()
		return nil, nil, &NoIdentityError{}
	}

	layout, err := paths.Default()
	if err != nil {
		closeHeld()
		return nil, nil, err
	}
	mode := knownhosts.ModePrompt
	if opts.TrustUnknownHost {
		mode = knownhosts.ModeTrust
	} else if opts.StrictHostKey {
		mode = knownhosts.ModeStrict
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: knownhosts.Callback(layout, mode, nil, nil),
		Timeout:         10 * time.Second,
	}, closeHeld, nil
}

// sshSessionConn wraps an SSH session's stdin/stdout as a net.Conn-like
// io.ReadWriteCloser for mode 2 (direct daemon SSH).
type sshSessionConn struct {
	io.Reader
	io.Writer
	session   *ssh.Session
	sshClient *ssh.Client
}

func (c *sshSessionConn) Close() error {
	_ = c.session.Close()
	return c.sshClient.Close()
}

// Implement net.Conn interface stubs for compatibility.
func (c *sshSessionConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *sshSessionConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (c *sshSessionConn) SetDeadline(t time.Time) error      { return nil }
func (c *sshSessionConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *sshSessionConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "ssh" }
func (dummyAddr) String() string  { return "ssh" }

// sshWrappedConn wraps an SSH-tunneled connection so that closing it
// also closes the underlying SSH client (mode 1).
type sshWrappedConn struct {
	net.Conn
	sshClient *ssh.Client
}

func (c *sshWrappedConn) Close() error {
	err := c.Conn.Close()
	_ = c.sshClient.Close()
	return err
}

// oneKeyAuthMethods is the auth for a dial that offers one key: the cluster's
// identity file, or marvel's client key when it names none. It reads neither
// the agent nor ~/.ssh, and a key that cannot be loaded is a NoIdentityError.
func oneKeyAuthMethods(identity string, offered *offeredKeys) ([]ssh.AuthMethod, error) {
	path := identity
	if path == "" {
		layout, err := paths.Default()
		if err != nil {
			return nil, err
		}
		path = layout.DefaultClientKey()
		if _, err := os.Stat(path); err != nil {
			return nil, &NoIdentityError{Reason: "the cluster names no identity and marvel has no client key"}
		}
	}
	signer, err := loadKeyFile(path, true)
	if err != nil {
		return nil, &NoIdentityError{Reason: fmt.Sprintf("the key %s cannot be loaded: %v", path, err)}
	}
	return []ssh.AuthMethod{offered.record(staticSigners(signer))}, nil
}

// sshAuthMethodsFor returns SSH auth methods for a cluster.
//
// Precedence:
//  1. identity file from the cluster config (if set)
//  2. default marvel client key (~/.marvel/keys/client_ed25519) when present
//  3. SSH_AUTH_SOCK (developer agent)
//  4. ~/.ssh/id_ed25519, ~/.ssh/id_rsa
//
// If identity is set but unreadable or has weak permissions, that is a
// hard error — callers expect the cluster's configured key to be used.
//
// The agent socket, when one is dialed, is handed to hold so the caller can
// close it; a silent agent would otherwise block auth past the exchange timer.
func sshAuthMethodsFor(identity string, offered *offeredKeys, hold func(io.Closer)) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if identity != "" {
		signer, err := loadKeyFile(identity, true)
		if err != nil {
			return nil, fmt.Errorf("cluster identity %s: %w", identity, err)
		}
		methods = append(methods, offered.record(staticSigners(signer)))
		return methods, nil
	}

	// Implicit default: marvel's own client key if it exists.
	if layout, err := paths.Default(); err == nil {
		defaultKey := layout.DefaultClientKey()
		if _, statErr := os.Stat(defaultKey); statErr == nil {
			signer, err := loadKeyFile(defaultKey, true)
			if err == nil {
				methods = append(methods, offered.record(staticSigners(signer)))
			} else {
				log.Printf("warning: %s unusable: %v", defaultKey, err)
			}
		}
	}

	// SSH agent (most common for developers).
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			if hold != nil {
				hold(conn)
			}
			methods = append(methods, offered.record(agent.NewClient(conn).Signers))
		}
	}

	// Standard ~/.ssh/ fallback.
	home, err := os.UserHomeDir()
	if err != nil {
		return methods, nil
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		signer, err := loadKeyFile(p, false)
		if err != nil {
			continue
		}
		methods = append(methods, offered.record(staticSigners(signer)))
	}
	return methods, nil
}

// loadKeyFile reads and parses a private key. When strictPerms is true,
// refuses to load keys with group- or world-accessible permissions.
func loadKeyFile(path string, strictPerms bool) (ssh.Signer, error) {
	if strictPerms {
		if err := paths.VerifyPrivateKeyMode(path); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}
