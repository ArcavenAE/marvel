package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
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
	if !term.IsTerminal(fd) {
		return errNeedTerminal
	}
	return nil
}

// waitReadable blocks until fd has input, wake has a byte, or the timeout
// passes (a zero timeout waits without bound). It polls, and falls back to
// select when poll reports a descriptor invalid or fails: on darwin poll
// answers POLLNVAL for a descriptor opened from /dev/tty, and select works on
// both. An interrupted wait is retried against the time that is left.
func waitReadable(fd, wake int, timeout time.Duration) (in, woke bool, err error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		ms, left := -1, time.Duration(0)
		if timeout > 0 {
			if left = time.Until(deadline); left <= 0 {
				return false, false, nil
			}
			ms = max(int(left.Milliseconds()), 1)
		}
		fds := []unix.PollFd{
			{Fd: int32(fd), Events: unix.POLLIN},
			{Fd: int32(wake), Events: unix.POLLIN},
		}
		n, perr := pollFn(fds, ms)
		switch {
		case errors.Is(perr, unix.EINTR):
			continue
		case perr != nil, fds[0].Revents&unix.POLLNVAL != 0, fds[1].Revents&unix.POLLNVAL != 0:
			return selectReadable(fd, wake, left)
		case n == 0:
			continue // the poll timed out; the deadline check above ends it
		}
		const ready = unix.POLLIN | unix.POLLHUP | unix.POLLERR
		return fds[0].Revents&ready != 0, fds[1].Revents&ready != 0, nil
	}
}

// selectReadable is waitReadable by select, for what poll cannot wait on. A
// zero left waits without bound.
func selectReadable(fd, wake int, left time.Duration) (in, woke bool, err error) {
	if fd >= unix.FD_SETSIZE || wake >= unix.FD_SETSIZE {
		return false, false, fmt.Errorf("descriptor %d or %d is too high for select", fd, wake)
	}
	deadline := time.Now().Add(left)
	for {
		var rs unix.FdSet
		rs.Set(fd)
		rs.Set(wake)
		var tv *unix.Timeval
		if left > 0 {
			remaining := max(time.Until(deadline), time.Millisecond)
			t := unix.NsecToTimeval(remaining.Nanoseconds())
			tv = &t
		}
		_, err := selectFn(max(fd, wake)+1, &rs, nil, nil, tv)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return false, false, err
		}
		return rs.IsSet(fd), rs.IsSet(wake), nil
	}
}

// keyReader reads one byte at a time from fd and can be paused: while it is
// paused it reads nothing, so a child process that inherits the descriptor
// sees every byte typed.
//
// stdin stays blocking, because a child inherits the open file description
// and O_NONBLOCK on it would reach the child. The reader waits on stdin and a
// wake pipe, and reads a byte only when stdin is readable, so it is never
// parked inside read(). A reader holds at most the one byte it read, which is
// why keys is unbuffered: a buffer would let it hold a second one.
type keyReader struct {
	fd           int
	wakeR, wakeW int
	keys         chan byte // unbuffered on purpose; see above
	pauseReq     chan struct{}
	ack          chan struct{}
	resume       chan struct{}
	stop         chan struct{}
	done         chan struct{}
	waits        atomic.Int64 // calls to waitReadable

	mu     sync.Mutex
	paused bool
	closed bool
}

func newKeyReader(fd int) (*keyReader, error) {
	if fd < 0 {
		return nil, errors.New("no descriptor to read")
	}
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		return nil, fmt.Errorf("make the wake pipe: %w", err)
	}
	for _, f := range p {
		unix.CloseOnExec(f) // a child must not inherit the wake pipe
		if err := unix.SetNonblock(f, true); err != nil {
			_ = unix.Close(p[0])
			_ = unix.Close(p[1])
			return nil, fmt.Errorf("make the wake pipe non-blocking: %w", err)
		}
	}
	r := &keyReader{
		fd: fd, wakeR: p[0], wakeW: p[1],
		keys:     make(chan byte),
		pauseReq: make(chan struct{}, 1),
		ack:      make(chan struct{}),
		resume:   make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go r.run()
	return r, nil
}

// Keys delivers the bytes read, in order.
func (r *keyReader) Keys() <-chan byte { return r.keys }

// stdinFD is the descriptor the reader reads, for a test that checks what is
// left on it.
func (r *keyReader) stdinFD() int { return r.fd }

// Pause returns once the reader is parked outside any read, with the wake
// byte drained. A byte it had read and not yet handed over is held for
// Resume. Pause on a paused reader returns at once.
func (r *keyReader) Pause() {
	r.mu.Lock()
	if r.paused || r.closed {
		r.mu.Unlock()
		return
	}
	r.paused = true
	r.wake()
	select {
	case r.pauseReq <- struct{}{}:
	default:
	}
	r.mu.Unlock()
	select {
	case <-r.ack:
	case <-r.done:
	}
}

// Resume lets a paused reader read again. Resume on a reader that is not
// paused does nothing.
func (r *keyReader) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.paused {
		return
	}
	r.paused = false
	select {
	case r.resume <- struct{}{}:
	default:
	}
}

// Close stops the reader and releases its pipe. It does not close fd.
func (r *keyReader) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.stop)
	r.wake()
	r.mu.Unlock()
	<-r.done
	_ = unix.Close(r.wakeR)
	_ = unix.Close(r.wakeW)
}

// wake puts one byte in the wake pipe. A full pipe already says wake.
func (r *keyReader) wake() { _, _ = unix.Write(r.wakeW, []byte{1}) }

// drainWake empties the wake pipe, so the next wait blocks again.
func (r *keyReader) drainWake() {
	var buf [16]byte
	for {
		if n, err := unix.Read(r.wakeR, buf[:]); n <= 0 || err != nil {
			return
		}
	}
}

func (r *keyReader) run() {
	defer close(r.done)
	var held []byte
	for {
		if len(held) > 0 {
			switch r.deliver(held[0]) {
			case delivered:
				held = nil
			case pausedFirst:
				if !r.park() {
					return
				}
				continue
			default:
				return
			}
		}
		r.waits.Add(1)
		in, woke, err := waitReadable(r.fd, r.wakeR, 0)
		if err != nil {
			return
		}
		if woke {
			r.drainWake()
			select {
			case <-r.pauseReq:
			default:
			}
			if !r.park() {
				return
			}
			continue
		}
		if !in {
			continue
		}
		var b [1]byte
		n, err := unix.Read(r.fd, b[:])
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if n != 1 || err != nil {
			return // end of input or a failed read, as the old reader did
		}
		held = append(held[:0], b[0])
	}
}

type handOver int

const (
	delivered handOver = iota
	pausedFirst
	stopped
)

// deliver hands b to the receiver, or stops trying when a pause or Close
// arrives first. The watch loop does not receive while it hands the terminal
// over, so a reader blocked here must still be able to park.
func (r *keyReader) deliver(b byte) handOver {
	select {
	case r.keys <- b:
		return delivered
	case <-r.pauseReq:
		r.drainWake()
		return pausedFirst
	case <-r.stop:
		return stopped
	}
}

// park acknowledges a pause and waits for Resume. It returns false when the
// reader should end: Close was called.
func (r *keyReader) park() bool {
	select {
	case <-r.stop:
		return false
	default:
	}
	select {
	case r.ack <- struct{}{}:
	case <-r.stop:
		return false
	}
	select {
	case <-r.resume:
		return true
	case <-r.stop:
		return false
	}
}
