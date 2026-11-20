package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"
)

// terminalHandoff is what the watch does to the terminal around a child that
// runs in place: park the key reader (returns once it is outside any read),
// restore the cooked mode, clear the screen; and afterwards put the raw mode
// back and let the reader read again. The order is part of the contract.
type terminalHandoff interface {
	Park()
	Restore() error
	Clear()
	MakeRaw() error
	Unpark()
}

// isShortChild says whether a plan's command finishes at once and leaves the
// terminal alone: a switch-client or a new-window, which act on a tmux server
// and return. Every other plan is an attach that takes the terminal.
func isShortChild(argv []string) bool {
	for i := 1; i < len(argv) && i <= 3; i++ {
		if argv[i] == "switch-client" || argv[i] == "new-window" {
			return true
		}
	}
	return false
}

// drainKeysFrom returns a function that receives and discards the keys the
// parser is holding for the loop, until none arrives within a short wait. The
// parser goroutine blocks sending a key nobody takes, so a key it holds is
// received at once.
func drainKeysFrom(keys <-chan byte) func() {
	return func() {
		for {
			select {
			case <-keys:
			case <-time.After(20 * time.Millisecond):
				return
			}
		}
	}
}

// openPending runs the plan Enter resolved. A short child runs as it is. An
// in-place child gets the terminal: the reader is parked, the cooked mode and
// a clear screen are put back, the child runs, and the raw mode and the reader
// come back whatever the child did. The view then shows "refreshing..." until
// a fresh fetch answers, and the selection stays on its session by identity.
func (ws *watchSort) openPending() {
	p, label, short := ws.pending, ws.pendingLabel, ws.pendingShort
	ws.pending, ws.pendingLabel, ws.pendingShort = nil, "", ""
	hint := " (" + short + "; press i for the tmux command)"
	if p == nil {
		return
	}
	if ws.exec == nil {
		ws.status = "cannot open " + label + ": no way to run the command here" + hint
		return
	}
	if isShortChild(p.Argv) {
		if err := ws.exec(p.Argv); err != nil {
			ws.status = "cannot open " + label + ": " + err.Error() + hint
			return
		}
		ws.status = ""
		return
	}
	ws.term.Park()
	// A key typed behind the Enter may already sit in the reader or the parser.
	// It belongs to the child and cannot be put back on the terminal, so the
	// view drops it, and it does not surface after the child.
	if f, ok := ws.term.(interface{ Flush() }); ok {
		f.Flush()
	}
	if ws.drainKeys != nil {
		ws.drainKeys()
	}
	err := ws.term.Restore()
	if err == nil {
		ws.term.Clear()
		err = ws.exec(p.Argv)
	}
	rawErr := ws.term.MakeRaw()
	ws.term.Unpark()
	switch {
	case err != nil:
		ws.status = "cannot open " + label + ": " + err.Error() + hint
	case rawErr != nil:
		ws.status = "cannot restore the terminal: " + rawErr.Error()
	default:
		ws.status = ""
	}
	ws.refreshing, ws.wantRefresh = true, true
}

// runAttachChild runs argv as a child in marvel's own process group with the
// given files, and waits for it. While it runs, SIGINT, SIGQUIT and SIGTSTP
// are caught and drained, so a key the terminal turns into a signal reaches
// the child and not marvel; they are never ignored, because an ignored signal
// survives exec. The child is not put in a group of its own and is not given
// the foreground: it shares marvel's, so the terminal's signals reach both.
func runAttachChild(argv []string, stdin, stdout, stderr *os.File) error {
	if len(argv) == 0 {
		return errors.New("no command to run")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigs:
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	return err
}

// ttyHandoff is the terminalHandoff of a real terminal: the reader is the
// watch's own, and old is the mode the terminal had before the watch took it.
type ttyHandoff struct {
	fd     int
	old    *term.State
	reader *keyReader
}

func (h *ttyHandoff) Flush()         { h.reader.Flush() }
func (h *ttyHandoff) Park()          { h.reader.Pause() }
func (h *ttyHandoff) Unpark()        { h.reader.Resume() }
func (h *ttyHandoff) Restore() error { return term.Restore(h.fd, h.old) }
func (h *ttyHandoff) Clear()         { fmt.Print("\033[2J\033[H") }
func (h *ttyHandoff) MakeRaw() error { _, err := term.MakeRaw(h.fd); return err }

// openInPlace wires a watch view to a real terminal: Enter may now open a
// session, running tmux as a child that takes the terminal.
func (ws *watchSort) openInPlace(h terminalHandoff) {
	ws.term = h
	ws.exec = func(argv []string) error {
		return runAttachChild(argv, os.Stdin, os.Stdout, os.Stderr)
	}
}
