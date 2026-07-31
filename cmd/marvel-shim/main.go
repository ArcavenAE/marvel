// Command marvel-shim is a spike prototype of the shim-in-pane substrate
// candidate (aae-orc-e35c, feeding the kxce substrate decision).
//
// It is the OS parent of one harness process. It allocates a PTY for that
// child, inherits its own stdio from the tmux pane it runs in, and tees the
// child's output to any number of Unix-socket stream subscribers while
// serving a JSON control API on a second Unix socket.
//
// Launch shape:
//
//	tmux new-window 'marvel-shim --control C.sock --stream S.sock -- claude'
//
// Nothing here is production code. Error handling is spike-grade and the
// protocol is deliberately the smallest thing that answers the five
// pre-declared success signals.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "marvel-shim: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		controlPath = flag.String("control", "", "path for the JSON control socket (required)")
		streamPath  = flag.String("stream", "", "path for the output stream socket (required)")
		onHUP       = flag.String("on-hup", "kill", "SIGHUP handling: kill (forward to child, exit) or detach (ignore, keep child)")
		quiet       = flag.Bool("quiet", false, "suppress the shim's own banner on stderr")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: marvel-shim --control PATH --stream PATH -- COMMAND [ARG...]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	argv := flag.Args()
	if len(argv) == 0 {
		flag.Usage()
		return errors.New("no command given after --")
	}
	if *controlPath == "" || *streamPath == "" {
		flag.Usage()
		return errors.New("--control and --stream are required")
	}
	if *onHUP != "kill" && *onHUP != "detach" {
		return fmt.Errorf("--on-hup must be kill or detach, got %q", *onHUP)
	}

	for _, p := range []string{*controlPath, *streamPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		// A stale socket from a crashed shim would make Listen fail.
		_ = os.Remove(p)
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "MARVEL_SHIM=1")

	// Seed the child PTY with the pane's current size so the first frame the
	// child paints is already correct; SIGWINCH only covers later changes.
	var winsz *pty.Winsize
	if ws, err := pty.GetsizeFull(os.Stdin); err == nil {
		winsz = ws
	}
	ptmx, err := pty.StartWithSize(cmd, winsz)
	if err != nil {
		return fmt.Errorf("start child under pty: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	bc := newBroadcaster()
	sh := &shim{
		cmd:    cmd,
		ptmx:   ptmx,
		bcast:  bc,
		exited: make(chan struct{}),
	}

	ctlLn, err := listenUnix(*controlPath)
	if err != nil {
		return err
	}
	defer func() { _ = ctlLn.Close() }()
	strLn, err := listenUnix(*streamPath)
	if err != nil {
		return err
	}
	defer func() { _ = strLn.Close() }()

	go sh.serveControl(ctlLn)
	go bc.serve(strLn)

	if !*quiet {
		fmt.Fprintf(os.Stderr, "[marvel-shim] pid=%d child=%d control=%s stream=%s\n",
			os.Getpid(), cmd.Process.Pid, *controlPath, *streamPath)
	}

	// Raw mode on the shim's own tty is load-bearing, not cosmetic: without it
	// the pane's line discipline echoes and line-buffers, so a child that
	// writes a terminal query and blocks reading the reply never sees one.
	// That is the Cursor-class TTY-hang this spike must not reintroduce.
	var restore func()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("raw mode on shim stdin: %w", err)
		}
		restore = func() { _ = term.Restore(int(os.Stdin.Fd()), state) }
		defer restore()
	}

	sh.watchSignals(*onHUP)

	// Pane -> child. Runs until the pane's stdin closes.
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()

	// Child -> pane, tee'd to stream subscribers. This is the shim's main
	// loop; it returns when the child PTY hits EIO (child gone on Darwin).
	buf := make([]byte, 32*1024)
	for {
		n, rerr := ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			bc.publish(chunk)
			_, _ = os.Stdout.Write(chunk)
		}
		if rerr != nil {
			break
		}
	}

	werr := cmd.Wait()
	close(sh.exited)
	bc.close()
	if restore != nil {
		restore()
	}
	if werr != nil && cmd.ProcessState == nil {
		return werr
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		if !*quiet {
			fmt.Fprintf(os.Stderr, "\r\n[marvel-shim] child exited %d\r\n", code)
		}
		os.Exit(code)
	}
	return nil
}

type shim struct {
	cmd    *exec.Cmd
	ptmx   *os.File
	bcast  *broadcaster
	exited chan struct{}
}

// watchSignals forwards the signals a pane can deliver. SIGWINCH is the
// interesting one: the pane resize has to be re-read from the shim's own tty
// and pushed down to the child's PTY, because the two PTYs are independent
// kernel objects and the kernel does not chain the notification.
func (s *shim) watchSignals(onHUP string) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			if err := pty.InheritSize(os.Stdin, s.ptmx); err != nil {
				fmt.Fprintf(os.Stderr, "[marvel-shim] resize: %v\r\n", err)
			}
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for sig := range sigs {
			if sig == syscall.SIGHUP && onHUP == "detach" {
				// Deliberately swallowed. Whether the child then survives is
				// a property of how tmux tears the pane down, not of this
				// branch; the spike measures it rather than assuming it.
				continue
			}
			_ = s.signalChild(sig)
			if sig == syscall.SIGHUP {
				select {
				case <-s.exited:
				case <-time.After(2 * time.Second):
					_ = s.signalChild(syscall.SIGKILL)
				}
				os.Exit(129)
			}
		}
	}()
}

func (s *shim) signalChild(sig os.Signal) error {
	if s.cmd.Process == nil {
		return errors.New("no child")
	}
	return s.cmd.Process.Signal(sig)
}

// inject writes to the PTY master. The child cannot distinguish these bytes
// from bytes typed in the pane, and the kernel serialises the write, which is
// the property tmux send-keys does not give us.
func (s *shim) inject(data string) (int, error) {
	return s.ptmx.Write([]byte(data))
}
