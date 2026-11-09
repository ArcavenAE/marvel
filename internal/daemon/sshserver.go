package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/keys"
	"github.com/arcavenae/marvel/internal/paths"
)

// keysEqual compares two SSH public keys by their marshaled bytes.
func keysEqual(a, b ssh.PublicKey) bool {
	return bytes.Equal(a.Marshal(), b.Marshal())
}

// SSHServer is an embedded SSH server that accepts marvel RPC connections.
// Clients authenticate with their SSH keys against ~/.marvel/authorized_keys.
// The daemon generates its own host key on first run.
type SSHServer struct {
	config   *ssh.ServerConfig
	listener net.Listener
	// addr is the address the listener bound, published once Start has it.
	addr   atomic.Pointer[net.Addr]
	daemon *Daemon
	layout paths.Layout
}

// newSSHServer creates an SSH server with the daemon's host key and
// authorized keys.
func newSSHServer(d *Daemon) (*SSHServer, error) {
	layout, err := paths.Default()
	if err != nil {
		return nil, err
	}
	if err := layout.EnsureHome(); err != nil {
		return nil, err
	}

	s := &SSHServer{daemon: d, layout: layout}

	hostKey, err := keys.LoadOrGenerateHostKey(layout)
	if err != nil {
		return nil, fmt.Errorf("host key: %w", err)
	}

	config := &ssh.ServerConfig{PublicKeyCallback: s.authorizeKey}
	config.AddHostKey(hostKey)

	s.config = config
	return s, nil
}

// Start begins accepting SSH connections on the given address.
func (s *SSHServer) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ssh listen %s: %w", addr, err)
	}
	s.listener = ln
	bound := ln.Addr()
	s.addr.Store(&bound)

	go s.acceptLoop()

	hostname, _ := os.Hostname()
	log.Printf("mrvl:// listener on %s", addr)
	if hostname != "" {
		log.Printf("remote access: --cluster <name>  (config: mrvl://%s:%s)", hostname, addrPort(addr))
	}

	return nil
}

// Addr returns the address the listener actually bound, or nil before Start.
func (s *SSHServer) Addr() net.Addr {
	if p := s.addr.Load(); p != nil {
		return *p
	}
	return nil
}

// Stop closes the SSH listener.
func (s *SSHServer) Stop() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (s *SSHServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return
			}
			log.Printf("ssh accept: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

func (s *SSHServer) handleConnection(conn net.Conn) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		log.Printf("ssh handshake failed: %v", err)
		return
	}
	defer func() { _ = sshConn.Close() }()

	fp := ""
	// The scope is the string authorizeKey recorded; an empty or unrecognized
	// value permits nothing (methodAllowedForScope fails closed), so a caller
	// only reaches methods its key was explicitly scoped for.
	var scope Scope
	if sshConn.Permissions != nil {
		fp = sshConn.Permissions.Extensions["pubkey-fp"]
		scope = Scope(sshConn.Permissions.Extensions["marvel-scope"])
	}
	clientCaller := caller{scope: scope, fingerprint: fp}
	host := conn.RemoteAddr().String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	log.Printf("ssh: client connected: %s@%s (%s)", sshConn.User(), host, fp)

	// Discard global requests (keepalive, etc.)
	go ssh.DiscardRequests(reqs)

	// Handle channels — each channel is one RPC request.
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}

		ch, chReqs, err := newCh.Accept()
		if err != nil {
			log.Printf("ssh: accept channel: %v", err)
			continue
		}

		// Handle channel requests (shell, exec, etc.) — we just need
		// a data pipe, so accept any request.
		go func() {
			for req := range chReqs {
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
			}
		}()

		// Handle the RPC on this channel as the authenticated caller. The
		// local unix socket is admin; this path carries the key's scope.
		s.daemon.handleRWCAs(ch, clientCaller)
	}
}

// authorizeKey checks if the client's public key is in authorized_keys and
// carries the key's recorded scope into the connection permissions, where the
// SSH server reads it to build the caller for dispatch.
func (s *SSHServer) authorizeKey(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	entries, err := loadAuthorizedKeys(s.layout)
	if err != nil {
		return nil, fmt.Errorf("load authorized keys: %w", err)
	}

	for _, e := range entries {
		if keysEqual(e.key, key) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"user":         conn.User(),
					"pubkey-fp":    ssh.FingerprintSHA256(key),
					"marvel-scope": string(e.scope),
				},
			}, nil
		}
	}

	return nil, fmt.Errorf("unknown key for user %s: %s", conn.User(), ssh.FingerprintSHA256(key))
}

// authorizedEntry is an authorized public key with the scope recorded beside
// it in authorized_keys (the marvel-scope option).
type authorizedEntry struct {
	key   ssh.PublicKey
	scope Scope
}

// loadAuthorizedKeys reads ~/.marvel/authorized_keys (OpenSSH format) and the
// marvel-scope option beside each key. A line whose scope option is malformed
// is skipped rather than admitted, so a typo fails closed instead of granting
// a default.
func loadAuthorizedKeys(layout paths.Layout) ([]authorizedEntry, error) {
	path := layout.AuthorizedKeys()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var result []authorizedEntry
	rest := data
	for len(rest) > 0 {
		key, _, options, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		rest = r
		scope, serr := scopeFromOptions(options)
		if serr != nil {
			log.Printf("authorized_keys: skipping key %s: %v", ssh.FingerprintSHA256(key), serr)
			continue
		}
		result = append(result, authorizedEntry{key: key, scope: scope})
	}
	return result, nil
}

// addrPort extracts the port from a host:port address.
func addrPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}

// AddAuthorizedKey appends a public key to ~/.marvel/authorized_keys with the
// given scope recorded as a marvel-scope option, so one file carries both the
// key and the authority it grants. See EnrollAuthorizedKey for what it accepts.
func AddAuthorizedKey(pubKeyData []byte, comment string, scope Scope) error {
	_, err := EnrollAuthorizedKey(pubKeyData, comment, scope)
	return err
}

// EnrollAuthorizedKey is AddAuthorizedKey that also returns the fingerprint of
// the key it wrote. The input must hold exactly one public key, optionally
// among blank and "#" comment lines, and the key line must carry no options of
// its own: an option applies to one line, so anything else in the file would
// be enrolled without the requested scope. The line written is rebuilt from
// the parsed key; the input bytes are never copied. A refused input writes
// nothing.
func EnrollAuthorizedKey(pubKeyData []byte, comment string, scope Scope) (string, error) {
	key, keyComment, err := parseEnrollmentKey(pubKeyData)
	if err != nil {
		return "", err
	}
	fp := ssh.FingerprintSHA256(key)

	layout, err := paths.Default()
	if err != nil {
		return "", err
	}
	if err := layout.EnsureHome(); err != nil {
		return "", err
	}

	path := layout.AuthorizedKeys()

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(existing) > 0 {
		rest := existing
		for len(rest) > 0 {
			k, _, _, r, err := ssh.ParseAuthorizedKey(rest)
			if err != nil {
				break
			}
			if keysEqual(k, key) {
				return "", fmt.Errorf("key already authorized: %s", fp)
			}
			rest = r
		}
	}

	// Record the scope as an authorized_keys option ahead of the key line.
	line := fmt.Sprintf("%s=%q %s", scopeOption, string(scope), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
	if keyComment != "" {
		line += " " + keyComment
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, paths.ModeAuthorized)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return "", fmt.Errorf("write key: %w", err)
	}
	// Enforce mode in case the file existed with different perms.
	_ = os.Chmod(path, paths.ModeAuthorized)

	log.Printf("authorized key added: %s scope=%s (%s)", fp, scope, comment)
	return fp, nil
}

// parseEnrollmentKey returns the one public key in data and the comment on its
// line. It refuses input with no key, with more than one key, with any option
// on the key line, or with any other text besides blank and "#" lines. The
// ssh parser skips lines it cannot read, so the lines are counted here too.
func parseEnrollmentKey(data []byte) (ssh.PublicKey, string, error) {
	var significant []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			significant = append(significant, l)
		}
	}

	var (
		key     ssh.PublicKey
		comment string
		options []string
		keys    int
	)
	for rest := data; len(rest) > 0; {
		k, c, o, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		if keys == 0 {
			key, comment, options = k, c, o
		}
		keys++
		rest = r
	}

	switch {
	case keys == 0:
		return nil, "", fmt.Errorf("invalid public key: no key found in the file")
	case keys > 1:
		return nil, "", fmt.Errorf("the file holds %d keys; authorize one key per file so each carries the requested scope", keys)
	case len(significant) != 1:
		for _, l := range significant {
			if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(l)); err != nil {
				return nil, "", fmt.Errorf("unexpected text in the key file: %q", l)
			}
		}
		return nil, "", fmt.Errorf("unexpected text in the key file besides the one key")
	case len(options) > 0:
		name, _, _ := strings.Cut(options[0], "=")
		return nil, "", fmt.Errorf("the key line carries the option %q; remove it, marvel records the scope itself", name)
	}
	return key, comment, nil
}

// ListAuthorizedKeys returns the fingerprints and comments of authorized keys.
func ListAuthorizedKeys() ([]KeyInfo, error) {
	layout, err := paths.Default()
	if err != nil {
		return nil, err
	}

	path := layout.AuthorizedKeys()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var result []KeyInfo
	rest := data
	for len(rest) > 0 {
		key, comment, options, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		rest = r
		scope, serr := scopeFromOptions(options)
		scopeStr := string(scope)
		if serr != nil {
			scopeStr = "invalid"
		}
		result = append(result, KeyInfo{
			Fingerprint: ssh.FingerprintSHA256(key),
			Type:        key.Type(),
			Comment:     comment,
			Scope:       scopeStr,
		})
	}
	return result, nil
}

// RemoveAuthorizedKey removes a key by fingerprint from authorized_keys.
func RemoveAuthorizedKey(fingerprint string) error {
	layout, err := paths.Default()
	if err != nil {
		return err
	}

	path := layout.AuthorizedKeys()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var kept []string
	removed := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			kept = append(kept, line)
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			kept = append(kept, line)
			continue
		}
		if ssh.FingerprintSHA256(key) == fingerprint {
			removed = true
			continue
		}
		kept = append(kept, line)
	}

	if !removed {
		return fmt.Errorf("no key with fingerprint %s", fingerprint)
	}

	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), paths.ModeAuthorized); err != nil {
		return err
	}
	_ = os.Chmod(path, paths.ModeAuthorized)
	return nil
}

// KeyInfo holds information about an authorized key.
type KeyInfo struct {
	Fingerprint string
	Type        string
	Comment     string
	Scope       string
}

// HostKeyFingerprint returns the daemon's host key fingerprint for display.
func HostKeyFingerprint() (string, error) {
	layout, err := paths.Default()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(layout.HostKeyPub())
	if err != nil {
		return "", fmt.Errorf("no host key found (run marvel daemon --mrvl first): %w", err)
	}

	key, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return "", fmt.Errorf("parse host key: %w", err)
	}

	return ssh.FingerprintSHA256(key), nil
}
