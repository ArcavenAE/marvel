// Package pty opens a pseudo-terminal with golang.org/x/sys and nothing else.
// It is a leaf: it starts no process, opens no socket, and imports none of
// marvel's wiring packages (guard_test.go pins that). The daemon's attach
// channel owns the process that runs on the slave. The unsafe the darwin
// ioctl needs lives here, so the daemon package keeps its ban on it.
package pty
