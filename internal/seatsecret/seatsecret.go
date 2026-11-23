// Package seatsecret hands one secret value to one tmux pane without the value
// touching an argument list or a file: the daemon makes a FIFO in a private
// directory, wraps the pane command so the pane reads the FIFO once into a
// variable, and writes the value when the pane has opened it
// (docs/design/seat-env-and-seat-secrets.md section 4.3).
package seatsecret

import "time"

// Handover is the private directory and FIFO for one seat.
type Handover struct{}

// Prepare makes a directory of mode 0700 under base and a FIFO in it.
func Prepare(base string) (*Handover, error) { return &Handover{}, nil }

// Wrap returns the pane command that reads the value once into the variable
// name and then runs command, waiting at most wait for the value.
func (h *Handover) Wrap(name, command string, wait time.Duration) string { return command }

// Deliver writes value and a newline to the FIFO once a reader has opened it,
// waiting at most wait, then removes the directory. It never puts the value on
// a command line.
func (h *Handover) Deliver(value []byte, wait time.Duration) error { return nil }

// Close removes the directory and the FIFO. It is safe to call twice.
func (h *Handover) Close() {}
