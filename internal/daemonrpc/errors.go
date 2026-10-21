package daemonrpc

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// The typed errors below name what went wrong on the way to a daemon, so a
// caller picks an exit code or an outcome with errors.As and never parses a
// message. Each keeps the text the client printed before it was typed.
// Host key failures are knownhosts.HostKeyUnknownError and
// knownhosts.HostKeyChangedError.

// UnreachableError is a dial that failed before any byte was exchanged:
// connect refused, no route, no such host, or no socket at the path.
type UnreachableError struct {
	Addr string
	Err  error
}

func (e *UnreachableError) Error() string { return e.Err.Error() }
func (e *UnreachableError) Unwrap() error { return e.Err }

// TimeoutError is an exchange that ran out of time. Phase says where:
// connect, handshake, auth or reply.
type TimeoutError struct {
	Phase string
	Err   error
}

func (e *TimeoutError) Error() string { return e.Err.Error() }
func (e *TimeoutError) Unwrap() error { return e.Err }

// BadReplyError is a daemon answer that could not be used: it did not parse.
type BadReplyError struct {
	Err error
}

func (e *BadReplyError) Error() string { return e.Err.Error() }
func (e *BadReplyError) Unwrap() error { return e.Err }

// AuthRefusedError is an SSH server that accepted none of the keys offered.
// Offered holds each offered key's SHA256 fingerprint, in
// ssh.FingerprintSHA256 form; Allowed is the methods the server named.
type AuthRefusedError struct {
	User    string
	Addr    string
	Offered []string
	Allowed []string
}

func (e *AuthRefusedError) Error() string {
	offered := "no key"
	if len(e.Offered) > 0 {
		offered = strings.Join(e.Offered, ", ")
	}
	return fmt.Sprintf("ssh: unable to authenticate as %s@%s; offered %s, server allows %v",
		e.User, e.Addr, offered, e.Allowed)
}

// NoIdentityError is a dial with no key to offer.
type NoIdentityError struct{}

func (e *NoIdentityError) Error() string {
	return "no SSH auth available: generate a key with 'marvel keys generate' or start ssh-agent"
}

// dialError types a failed connect: a timeout is a TimeoutError in the
// connect phase, anything else an UnreachableError.
func dialError(addr string, err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &TimeoutError{Phase: "connect", Err: err}
	}
	return &UnreachableError{Addr: addr, Err: err}
}
