package main

import (
	"errors"
	"os"
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

// runAttachChild runs argv as a child in marvel's own process group with the
// given files, and waits for it. While it runs, SIGINT, SIGQUIT and SIGTSTP
// are caught and drained, so a key the terminal turns into a signal reaches
// the child and not marvel; they are never ignored, because an ignored signal
// survives exec.
func runAttachChild(argv []string, stdin, stdout, stderr *os.File) error {
	return errors.New("not implemented")
}
