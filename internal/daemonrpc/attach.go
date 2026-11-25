package daemonrpc

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"golang.org/x/crypto/ssh"
)

// AttachChannelType is the SSH channel type a daemon serves for a terminal on
// one desk. The open payload carries the desk; pty-req and shell follow.
const AttachChannelType = "marvel-attach"

// ErrAttachUnsupported is returned by Attach when the daemon rejects the
// attach channel type, which an older daemon does.
var ErrAttachUnsupported = errors.New("daemon does not support remote attach")

// AttachIO is the terminal Attach connects to a remote desk.
type AttachIO struct {
	In         io.Reader
	Out        io.Writer
	Rows, Cols int
	// Resize carries each new terminal size to the remote pty. Nil means
	// the size never changes.
	Resize <-chan Size
}

// Size is a terminal size in character cells.
type Size struct {
	Rows, Cols int
}

type attachOpenPayload struct {
	Desk string
}

type attachPtyPayload struct {
	Term          string
	Cols, Rows    uint32
	Width, Height uint32
	Modes         string
}

type attachWindowPayload struct {
	Cols, Rows    uint32
	Width, Height uint32
}

// Attach opens the attach channel to the daemon at addr (an mrvl:// address)
// and bridges tio to the desk until the remote side closes it. It offers the
// one key opts names, or marvel's own client key, and never the ssh agent or
// a key under ~/.ssh, whatever opts.OfferOneKey says: a terminal on a remote
// daemon is not a place for a key the operator did not pin.
func Attach(addr, desk string, opts DialOptions, tio AttachIO) error {
	u, err := url.Parse(addr)
	if err != nil || u.Scheme != "mrvl" {
		return fmt.Errorf("attach needs an mrvl:// address, got %q", addr)
	}
	user := u.User.Username()
	if user == "" {
		user = os.Getenv("USER")
	}
	port := u.Port()
	if port == "" {
		port = DefaultMRVLPort
	}
	opts.OfferOneKey = true
	client, err := sshDial(user, u.Hostname(), port, opts)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	ch, reqs, err := client.OpenChannel(AttachChannelType, ssh.Marshal(attachOpenPayload{Desk: desk}))
	if err != nil {
		var oce *ssh.OpenChannelError
		if errors.As(err, &oce) {
			if oce.Reason == ssh.UnknownChannelType {
				return ErrAttachUnsupported
			}
			return fmt.Errorf("daemon refused the attach for desk %s: %s", desk, oce.Message)
		}
		return fmt.Errorf("open attach channel: %w", err)
	}
	go ssh.DiscardRequests(reqs)

	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm"
	}
	ptyReq := attachPtyPayload{Term: term, Cols: uint32(tio.Cols), Rows: uint32(tio.Rows)} //nolint:gosec // terminal sizes are small positive integers
	if ok, err := ch.SendRequest("pty-req", true, ssh.Marshal(ptyReq)); err != nil || !ok {
		return fmt.Errorf("daemon would not give a terminal for desk %s (accepted %v): %w", desk, ok, errOrNil(err))
	}
	if ok, err := ch.SendRequest("shell", true, nil); err != nil || !ok {
		return fmt.Errorf("daemon would not start the attach for desk %s (accepted %v): %w", desk, ok, errOrNil(err))
	}

	if tio.Resize != nil {
		go func() {
			for sz := range tio.Resize {
				wc := attachWindowPayload{Cols: uint32(sz.Cols), Rows: uint32(sz.Rows)} //nolint:gosec // terminal sizes are small positive integers
				if _, err := ch.SendRequest("window-change", false, ssh.Marshal(wc)); err != nil {
					return
				}
			}
		}()
	}
	if tio.In != nil {
		go func() { _, _ = io.Copy(ch, tio.In) }()
	}
	_, copyErr := io.Copy(tio.Out, ch)
	_ = ch.Close()
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return fmt.Errorf("read terminal output: %w", copyErr)
	}
	return nil
}

// errOrNil keeps a refusal that carries no transport error readable.
func errOrNil(err error) error {
	if err == nil {
		return errors.New("refused")
	}
	return err
}
