package daemonrpc

import (
	"errors"
	"io"
)

// ErrAttachUnsupported is returned by Attach when the daemon rejects the
// attach channel type, which an older daemon does.
var ErrAttachUnsupported = errors.New("daemon does not support remote attach")

// AttachIO is the terminal Attach connects to a remote desk.
type AttachIO struct {
	In         io.Reader
	Out        io.Writer
	Rows, Cols int
}

var errAttachNotBuilt = errors.New("remote attach is not built")

// Attach opens the attach channel to the daemon at addr and bridges tio to the
// desk until either side closes.
func Attach(addr, desk string, opts DialOptions, tio AttachIO) error {
	return errAttachNotBuilt
}
