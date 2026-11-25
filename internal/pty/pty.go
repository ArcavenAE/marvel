// Package pty opens a pseudo-terminal with golang.org/x/sys and nothing else.
// It is a leaf: it starts no process, opens no socket, and imports none of
// marvel's wiring packages (guard_test.go pins that). The daemon's attach
// channel owns the process that runs on the slave.
package pty

import (
	"errors"
	"os"
)

// Pair is a pseudo-terminal: the master the caller reads and writes, and the
// slave a process gets as its terminal.
type Pair struct {
	Master, Slave *os.File
}

var errNotBuilt = errors.New("pty is not built")

// Open opens a pty. Not built yet.
func Open() (*Pair, error) {
	return nil, errNotBuilt
}

// Resize sets the window size the slave reports. Not built yet.
func (p *Pair) Resize(rows, cols uint16) error {
	return errNotBuilt
}
