package daemon

import (
	"fmt"
	"net"
	"net/url"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// HostPortOf returns the "host:port" pair a direct mrvl:// or ssh://host:port
// address dials, with the default port filled in for mrvl://. It refuses a
// socket path, a tcp:// address, and an ssh:// address that tunnels to a
// socket, because the key those present is not the daemon's own.
func HostPortOf(addr string) (string, error) {
	if !daemonrpc.IsMRVL(addr) && !daemonrpc.IsSSH(addr) {
		return "", fmt.Errorf("address %q is not a direct mrvl:// or ssh://host:port address", addr)
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("parse address %q: %w", addr, err)
	}
	host, port := u.Hostname(), u.Port()
	if host == "" {
		return "", fmt.Errorf("address %q names no host", addr)
	}
	if daemonrpc.IsSSH(addr) {
		if u.Path != "" && u.Path != "/" {
			return "", fmt.Errorf("address %q tunnels to a socket path; only the daemon's own SSH port can be read directly", addr)
		}
		if port == "" {
			return "", fmt.Errorf("address %q needs a port for the daemon's SSH server", addr)
		}
	}
	if port == "" {
		port = DefaultMRVLPort
	}
	return net.JoinHostPort(host, port), nil
}
