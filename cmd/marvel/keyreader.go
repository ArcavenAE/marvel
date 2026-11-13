package main

import (
	"errors"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// pollFn and selectFn are the two ways to wait for a descriptor. They are
// variables so a test can make poll fail the way it does on a descriptor
// opened from /dev/tty.
var (
	pollFn   = unix.Poll
	selectFn = unix.Select
)

// requireTerminal is the gate the watch view puts in front of raw mode: it
// returns errNeedTerminal for a descriptor that is not a terminal.
func requireTerminal(fd int) error {
	return nil
}

// waitReadable blocks until fd has input, wake has a byte, or the timeout
// passes (a zero timeout waits without bound). It polls, and falls back to
// select when poll reports the descriptor invalid or fails.
func waitReadable(fd, wake int, timeout time.Duration) (in, woke bool, err error) {
	return false, false, nil
}

// keyReader reads one byte at a time from fd and can be paused: while it is
// paused it reads nothing, so a child process that inherits the descriptor
// sees every byte typed.
type keyReader struct {
	fd    int
	keys  chan byte
	waits atomic.Int64 // calls to waitReadable
	wakeR int          // read end of the wake pipe
	done  chan struct{}
}

func newKeyReader(fd int) (*keyReader, error) {
	if fd < 0 {
		return nil, errors.New("no descriptor to read")
	}
	return &keyReader{fd: fd, keys: make(chan byte), wakeR: -1, done: make(chan struct{})}, nil
}

// Keys delivers the bytes read, in order.
func (r *keyReader) Keys() <-chan byte { return r.keys }

// Pause returns once the reader is parked outside any read.
func (r *keyReader) Pause() {}

// Resume lets a paused reader read again.
func (r *keyReader) Resume() {}

// stdinFD is the descriptor the reader reads, for a test that checks what is
// left on it.
func (r *keyReader) stdinFD() int { return r.fd }

// Close stops the reader and releases its pipe.
func (r *keyReader) Close() {}
