package daemon

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
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
	daemon   *Daemon
	dataDir  string
}

// marvelDir returns ~/.marvel/, creating it if needed.
func marvelDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".marvel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	return dir, nil
}

// newSSHServer creates an SSH server with the daemon's host key and
// authorized keys.
func newSSHServer(d *Daemon) (*SSHServer, error) {
	dataDir, err := marvelDir()
	if err != nil {
		return nil, err
	}

	s := &SSHServer{
		daemon:  d,
		dataDir: dataDir,
	}

	hostKey, err := s.loadOrGenerateHostKey()
	if err != nil {
		return nil, fmt.Errorf("host key: %w", err)
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: s.authorizeKey,
	}
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

	go s.acceptLoop()

	hostname, _ := os.Hostname()
	log.Printf("marvel SSH server listening on %s", addr)
	if hostname != "" {
		log.Printf("remote access: --socket ssh://%s:%s", hostname, addrPort(addr))
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
	if sshConn.Permissions != nil {
		fp = sshConn.Permissions.Extensions["pubkey-fp"]
	}
	log.Printf("ssh: client connected: %s (%s)", sshConn.User(), fp)

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

		// Handle the RPC on this channel — same as a Unix socket connection.
		s.daemon.handleRWC(ch)
	}
}

// authorizeKey checks if the client's public key is in authorized_keys.
func (s *SSHServer) authorizeKey(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	authorizedKeys, err := s.loadAuthorizedKeys()
	if err != nil {
		return nil, fmt.Errorf("load authorized keys: %w", err)
	}

	for _, ak := range authorizedKeys {
		if keysEqual(ak, key) {
			return &ssh.Permissions{
				Extensions: map[string]string{
					"user":      conn.User(),
					"pubkey-fp": ssh.FingerprintSHA256(key),
				},
			}, nil
		}
	}

	return nil, fmt.Errorf("unknown key for user %s: %s", conn.User(), ssh.FingerprintSHA256(key))
}

// loadAuthorizedKeys reads ~/.marvel/authorized_keys (OpenSSH format).
func (s *SSHServer) loadAuthorizedKeys() ([]ssh.PublicKey, error) {
	path := filepath.Join(s.dataDir, "authorized_keys")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var keys []ssh.PublicKey
	rest := data
	for len(rest) > 0 {
		key, _, _, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		keys = append(keys, key)
		rest = r
	}
	return keys, nil
}

// loadOrGenerateHostKey loads the host key from ~/.marvel/ssh_host_ed25519_key,
// generating a new one if it doesn't exist.
func (s *SSHServer) loadOrGenerateHostKey() (ssh.Signer, error) {
	keyPath := filepath.Join(s.dataDir, "ssh_host_ed25519_key")

	data, err := os.ReadFile(keyPath)
	if err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse host key %s: %w", keyPath, err)
		}
		return signer, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read host key %s: %w", keyPath, err)
	}

	// Generate new ed25519 host key.
	log.Printf("generating SSH host key: %s", keyPath)
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}

	pemBlock, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return nil, fmt.Errorf("marshal host key: %w", err)
	}

	pemData := pem.EncodeToMemory(pemBlock)
	if err := os.WriteFile(keyPath, pemData, 0o600); err != nil {
		return nil, fmt.Errorf("write host key %s: %w", keyPath, err)
	}

	// Also write the public key for easy distribution.
	signer, err := ssh.ParsePrivateKey(pemData)
	if err != nil {
		return nil, fmt.Errorf("parse generated host key: %w", err)
	}

	pubPath := keyPath + ".pub"
	pubData := ssh.MarshalAuthorizedKey(signer.PublicKey())
	if err := os.WriteFile(pubPath, pubData, 0o644); err != nil {
		log.Printf("warning: could not write public key %s: %v", pubPath, err)
	}

	return signer, nil
}

// addrPort extracts the port from a host:port address.
func addrPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return port
}

// AddAuthorizedKey appends a public key to ~/.marvel/authorized_keys.
func AddAuthorizedKey(pubKeyData []byte, comment string) error {
	dataDir, err := marvelDir()
	if err != nil {
		return err
	}

	// Validate it's a real public key.
	key, _, _, _, err := ssh.ParseAuthorizedKey(pubKeyData)
	if err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	path := filepath.Join(dataDir, "authorized_keys")

	// Check for duplicates.
	existing, _ := os.ReadFile(path)
	if len(existing) > 0 {
		rest := existing
		for len(rest) > 0 {
			k, _, _, r, err := ssh.ParseAuthorizedKey(rest)
			if err != nil {
				break
			}
			if keysEqual(k, key) {
				return fmt.Errorf("key already authorized: %s", ssh.FingerprintSHA256(key))
			}
			rest = r
		}
	}

	line := strings.TrimSpace(string(pubKeyData))

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	_, err = fmt.Fprintf(f, "%s\n", line)
	if err != nil {
		return fmt.Errorf("write key: %w", err)
	}

	log.Printf("authorized key added: %s (%s)", ssh.FingerprintSHA256(key), comment)
	return nil
}

// ListAuthorizedKeys returns the fingerprints and comments of authorized keys.
func ListAuthorizedKeys() ([]KeyInfo, error) {
	dataDir, err := marvelDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dataDir, "authorized_keys")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var keys []KeyInfo
	rest := data
	for len(rest) > 0 {
		key, comment, _, r, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		keys = append(keys, KeyInfo{
			Fingerprint: ssh.FingerprintSHA256(key),
			Type:        key.Type(),
			Comment:     comment,
		})
		rest = r
	}
	return keys, nil
}

// RemoveAuthorizedKey removes a key by fingerprint from authorized_keys.
func RemoveAuthorizedKey(fingerprint string) error {
	dataDir, err := marvelDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dataDir, "authorized_keys")
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

	return os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}

// KeyInfo holds information about an authorized key.
type KeyInfo struct {
	Fingerprint string
	Type        string
	Comment     string
}

// HostKeyFingerprint returns the daemon's host key fingerprint for display.
func HostKeyFingerprint() (string, error) {
	dataDir, err := marvelDir()
	if err != nil {
		return "", err
	}

	pubPath := filepath.Join(dataDir, "ssh_host_ed25519_key.pub")
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return "", fmt.Errorf("no host key found (run marvel daemon --ssh first): %w", err)
	}

	key, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return "", fmt.Errorf("parse host key: %w", err)
	}

	return ssh.FingerprintSHA256(key), nil
}
