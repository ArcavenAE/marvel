// Package seatsecret hands one secret value to one tmux pane without the value
// touching an argument list or a file: the daemon makes a FIFO in a private
// directory, wraps the pane command so the pane reads the FIFO once into a
// variable, and writes the value when the pane has opened it
// (docs/design/seat-env-and-seat-secrets.md section 4.3).
package seatsecret

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Handover is the private directory and FIFO for one seat.
type Handover struct {
	dir  string
	fifo string
	once sync.Once
}

// Prepare makes a directory of mode 0700 under base and a FIFO in it.
func Prepare(base string) (*Handover, error) {
	dir, err := os.MkdirTemp(base, "seat-")
	if err != nil {
		return nil, fmt.Errorf("seat secret directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("seat secret directory: %w", err)
	}
	fifo := filepath.Join(dir, "v")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("seat secret fifo: %w", err)
	}
	return &Handover{dir: dir, fifo: fifo}, nil
}

// readScript runs in bash 3.2 (the macOS /bin/bash): it opens the FIFO for
// reading and writing, which never blocks, so the bound on the read holds even
// if no writer ever comes; reads one line into V; exports it under the name in
// $3 only if the read succeeded; and runs the pane's own command from $2. The
// value is data: it is never evaluated.
const readScript = `exec 3<>"$1"; if IFS= read -r -t "$4" -u 3 V; then export "$3=$V"; fi; exec 3<&-; unset V; exec /bin/sh -c "$2"`

// Wrap returns the pane command that reads the value once into the variable
// name and then runs command, waiting at most wait for the value. A pane whose
// wait ends starts the command without the variable.
func (h *Handover) Wrap(name, command string, wait time.Duration) string {
	secs := max(1, int(math.Ceil(wait.Seconds())))
	return strings.Join([]string{
		"/bin/bash", "-c", quote(readScript), "seatsecret",
		quote(h.fifo), quote(command), quote(name), fmt.Sprint(secs),
	}, " ")
}

// Deliver writes value and a newline to the FIFO once a reader has opened it,
// waiting at most wait, then removes the directory. It never puts the value on
// a command line, and no error it returns holds the value.
func (h *Handover) Deliver(value []byte, wait time.Duration) error {
	defer h.Close()
	if bytes.ContainsAny(value, "\n\r\x00") {
		return errors.New("seat secret: the value holds a newline or a NUL and cannot ride a line hand-over")
	}
	deadline := time.Now().Add(wait)
	var fd int
	for {
		var err error
		fd, err = unix.Open(h.fifo, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.ENXIO) {
			return fmt.Errorf("seat secret: open the hand-over: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("seat secret: no pane read the hand-over within %s", wait)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer unix.Close(fd) //nolint:errcheck
	line := append(append([]byte(nil), value...), '\n')
	defer func() {
		for i := range line {
			line[i] = 0
		}
	}()
	for len(line) > 0 {
		n, err := unix.Write(fd, line)
		if n > 0 {
			line = line[n:]
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) || time.Now().After(deadline) {
			return fmt.Errorf("seat secret: write the hand-over: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// Close removes the directory and the FIFO. It is safe to call twice.
func (h *Handover) Close() {
	h.once.Do(func() {
		if h.dir != "" {
			_ = os.RemoveAll(h.dir)
		}
	})
}

// quote single-quotes s for a shell.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
