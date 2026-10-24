package main

import (
	"errors"
	"io"

	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// handoffPut sends the handoff text on in to the daemon for the session that
// owns token. Not implemented yet.
func handoffPut(in io.Reader, out io.Writer, token string, send func(daemonrpc.Request) (*daemonrpc.Response, error)) error {
	return errors.New("handoff put: not implemented")
}
