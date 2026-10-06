package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/arcavenae/marvel/internal/daemon"
)

// headerInfo is what the get sessions header is built from: the client's own
// facts (the address it resolved, the rung that chose it, the cluster name it
// asked for) and the daemon's status record from one daemon.status call.
type headerInfo struct {
	Address    string
	Rung       resolveRung
	ClientName string
	// Status is nil when the daemon could not answer, with StatusErr saying why.
	Status    *daemon.DaemonStatus
	StatusErr string
}

// stdoutIsTTY is a variable so a test can stand in for a terminal.
var stdoutIsTTY = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// wantHeader says whether get sessions prints the header: when stdout is a
// terminal, or when --header forces it for a piped run.
func wantHeader(force bool) bool {
	if force {
		return stdoutIsTTY()
	}
	return false
}

// collectHeader resolves the address and rung and makes the one
// daemon.status call.
func collectHeader() headerInfo {
	return headerInfo{Address: "not built yet"}
}

// renderHeader renders the header block, ending in a blank line.
func renderHeader(h headerInfo, now time.Time) string {
	return fmt.Sprintf("header not built yet %s %v\n", h.Address, now.IsZero())
}
