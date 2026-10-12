package daemon

import (
	"errors"

	"golang.org/x/crypto/ssh"
)

// HostPortOf returns the "host:port" pair a direct mrvl:// or ssh://host:port
// address dials, with the default port filled in.
func HostPortOf(addr string) (string, error) {
	return "", errors.New("not implemented")
}

// ProbeHostKey connects to addr far enough to read the host key it presents,
// then drops the connection. It records nothing and sends no credentials.
func ProbeHostKey(addr string) (ssh.PublicKey, error) {
	return nil, errors.New("not implemented")
}
