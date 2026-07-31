package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// FIFO is the byte path between a harness running in a tmux pane and the
// daemon goroutine that parses its structured output. A named pipe is the
// cheapest such path available: the harness needs no marvel-aware shim
// (a shell redirection in the command string is the whole integration),
// nothing lands on disk, and back-pressure is the kernel's problem.
//
// The rendezvous is deliberate. Opening the read end blocks until a
// writer arrives, and the harness's shell blocks on its redirection
// until the reader arrives, so the manager parks a reader before it
// creates the pane. A launch that dies before exec still delivers EOF,
// because the shell opens the redirection before it execs.
type FIFO struct {
	path string
}

// fifoMode keeps the pipe private to the daemon's user. Nothing else has
// business reading an agent's transcript.
const fifoMode = 0o600

// NewFIFO creates a named pipe for one session under dir, deriving the
// filename from the session key. A leftover pipe from a crashed daemon is
// replaced rather than treated as an error — the path is derived, so a
// collision means the previous owner is gone.
func NewFIFO(dir, sessionKey string) (*FIFO, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create stream dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, fifoName(sessionKey))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("clear stale fifo %s: %w", path, err)
	}
	if err := syscall.Mkfifo(path, fifoMode); err != nil {
		return nil, fmt.Errorf("mkfifo %s: %w", path, err)
	}
	return &FIFO{path: path}, nil
}

// fifoName flattens a session key (workspace/name) into one filename.
func fifoName(sessionKey string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, sessionKey)
	return slug + ".ndjson"
}

// Path is the filesystem path handed to the adapter for redirection.
func (f *FIFO) Path() string { return f.path }

// Open blocks until the harness opens the write end, then returns the
// read side. Callers run it on a goroutine.
func (f *FIFO) Open() (*os.File, error) {
	file, err := os.OpenFile(f.path, os.O_RDONLY, os.ModeNamedPipe)
	if err != nil {
		return nil, fmt.Errorf("open fifo %s: %w", f.path, err)
	}
	return file, nil
}

// Poke releases a reader parked in Open by briefly becoming a writer. It
// is the only way to retire a stream whose harness never started: the
// blocking open is not context-cancellable. ENXIO (no reader parked)
// means there was nothing to release, which is success.
func (f *FIFO) Poke() {
	w, err := os.OpenFile(f.path, os.O_WRONLY|syscall.O_NONBLOCK, fifoMode)
	if err != nil {
		return
	}
	_ = w.Close()
}

// Remove deletes the pipe. Safe to call on an already-removed path.
func (f *FIFO) Remove() error {
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove fifo %s: %w", f.path, err)
	}
	return nil
}
