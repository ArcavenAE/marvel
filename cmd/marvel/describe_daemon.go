package main

import (
	"fmt"
	"io"
)

// describeDaemon prints what `marvel describe daemon` shows: the address
// this client resolved, the rung that chose it, and the daemon's own
// status record from one daemon.status call.
func describeDaemon(w io.Writer) error {
	_, err := fmt.Fprintln(w, "describe daemon is not built yet")
	return err
}
