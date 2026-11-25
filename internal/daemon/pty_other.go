//go:build !linux && !darwin

package daemon

import (
	"errors"
	"os"
)

// ptyPair has no implementation off linux and darwin, the platforms marvel
// ships on, so an attach there is refused instead of failing to build.
type ptyPair struct {
	master, slave *os.File
}

func openPty() (*ptyPair, error) {
	return nil, errors.New("attach needs a pty, which this platform does not provide")
}

func (p *ptyPair) resize(rows, cols uint16) error {
	return errors.New("attach needs a pty, which this platform does not provide")
}
