//go:build !linux && !darwin

package pty

import (
	"errors"
	"os"
)

// Pair has no implementation off linux and darwin, the platforms marvel
// ships on, so an attach there is refused instead of failing to build.
type Pair struct {
	Master, Slave *os.File
}

func Open() (*Pair, error) {
	return nil, errors.New("attach needs a pty, which this platform does not provide")
}

func (p *Pair) Resize(rows, cols uint16) error {
	return errors.New("attach needs a pty, which this platform does not provide")
}
