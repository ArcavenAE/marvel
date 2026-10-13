package knownhosts

import (
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// probeTimeout bounds the connect and the handshake of Probe.
const probeTimeout = 10 * time.Second

// errProbeDone stops the handshake once the host key has been read.
var errProbeDone = errors.New("host key read")

// Probe connects to hostport, a "host:port" pair, far enough to read the host
// key it presents, then drops the connection. It records nothing and sends no
// credentials: the key is read by the host key callback, which runs before any
// authentication.
func Probe(hostport string) (ssh.PublicKey, error) {
	var seen ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: "probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = key
			return errProbeDone
		},
		Timeout: probeTimeout,
	}
	conn, err := net.DialTimeout("tcp", hostport, probeTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", hostport, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(probeTimeout))

	c, _, _, err := ssh.NewClientConn(conn, hostport, cfg)
	if c != nil {
		_ = c.Close()
	}
	if seen != nil {
		return seen, nil
	}
	return nil, fmt.Errorf("read the host key at %s: %w", hostport, err)
}
