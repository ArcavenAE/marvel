package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// runWinsize answers signal 1: does SIGWINCH reach the child through two
// stacked PTYs, and does the child's tty report the new size?
func runWinsize(args []string) error {
	fs := flag.NewFlagSet("winsize", flag.ExitOnError)
	dur := fs.Duration("for", 20*time.Second, "how long to stay alive")
	if err := fs.Parse(args); err != nil {
		return err
	}

	report := func(tag string) {
		ws, err := pty.GetsizeFull(os.Stdin)
		if err != nil {
			fmt.Printf("WINSIZE %s err=%v\n", tag, err)
			return
		}
		fmt.Printf("WINSIZE %s rows=%d cols=%d\n", tag, ws.Rows, ws.Cols)
	}

	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGWINCH)
	report("initial")

	deadline := time.After(*dur)
	for {
		select {
		case <-ch:
			report("sigwinch")
		case <-deadline:
			fmt.Println("WINSIZE done")
			return nil
		}
	}
}

// runSpew answers signal 2: it produces a known number of numbered lines with
// ANSI decoration so a consumer can verify byte-for-byte that nothing was
// dropped between the child PTY and the stream socket.
func runSpew(args []string) error {
	fs := flag.NewFlagSet("spew", flag.ExitOnError)
	n := fs.Int("n", 20000, "number of lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	w := bufio.NewWriterSize(os.Stdout, 64*1024)
	for i := 1; i <= *n; i++ {
		_, _ = fmt.Fprintf(w, "\x1b[3%dmSPEW %06d\x1b[0m padding-padding-padding\n", i%8, i)
	}
	_, _ = fmt.Fprintf(w, "SPEWDONE %d\n", *n)
	return w.Flush()
}

// runTTYProbe is the falsification harness for the Cursor-class TTY-hang. It
// writes a DA1 query, then reads stdin with a deadline. A real terminal
// answers with a CSI ? ... c report; a PTY with nothing behind it does not,
// and a harness that blocks forever on that read is the failure being
// emulated.
//
// -raw decides which failure is being measured. With raw set (what a real
// harness does) the read returns as soon as any byte arrives, so the only way
// to hang is for nothing to answer. With -raw=false the line discipline holds
// the reply until a newline that a DA1 report never contains, so the read
// hangs even though the answer arrived. Both are worth measuring, and the
// second one is not specific to the shim.
func runTTYProbe(args []string) error {
	fs := flag.NewFlagSet("ttyprobe", flag.ExitOnError)
	timeout := fs.Duration("timeout", 3*time.Second, "how long to wait for the reply")
	query := fs.String("query", "da1", "query to emit: da1 or kitty")
	raw := fs.Bool("raw", true, "put stdin in raw mode first, as a real harness would")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tty := isTTY(os.Stdin)
	fmt.Fprintf(os.Stderr, "TTYPROBE stdin-is-tty=%v raw=%v\n", tty, *raw)
	if *raw && tty {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		defer func() { _ = term.Restore(int(os.Stdin.Fd()), state) }()
	}

	var seq string
	switch *query {
	case "da1":
		seq = "\x1b[c"
	case "kitty":
		seq = "\x1b[?u"
	default:
		return fmt.Errorf("unknown query %q", *query)
	}
	if _, err := os.Stdout.WriteString(seq); err != nil {
		return err
	}

	type result struct {
		n   int
		buf []byte
		err error
	}
	res := make(chan result, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := os.Stdin.Read(buf)
		res <- result{n: n, buf: buf[:n], err: err}
	}()

	start := time.Now()
	select {
	case r := <-res:
		if r.err != nil && r.n == 0 {
			fmt.Printf("\r\nTTYPROBE result=error after=%s err=%v\r\n", time.Since(start), r.err)
			return nil
		}
		fmt.Printf("\r\nTTYPROBE result=reply after=%s bytes=%d raw=%q\r\n",
			time.Since(start), r.n, string(r.buf))
	case <-time.After(*timeout):
		fmt.Printf("\r\nTTYPROBE result=HANG after=%s (no reply within timeout)\r\n", *timeout)
	}
	return nil
}

// runEcho answers signal 3: it stamps every line it reads so concurrent
// injections can be checked for interleaving corruption at the child.
//
// -raw is not cosmetic here. In cooked mode the child's line discipline caps
// one input line at MAX_CANON (1024 bytes on Darwin) and discards the excess,
// so a large inject looks like shim loss when it is really the child's termios.
// Real harnesses set raw mode, which is what -raw reproduces.
func runEcho(args []string) error {
	fs := flag.NewFlagSet("echo", flag.ExitOnError)
	dur := fs.Duration("for", 30*time.Second, "idle lifetime cap")
	raw := fs.Bool("raw", false, "put stdin in raw mode, as a real harness would")
	if err := fs.Parse(args); err != nil {
		return err
	}
	go func() {
		time.Sleep(*dur)
		fmt.Println("ECHO timeout")
		os.Exit(0)
	}()

	if *raw && isTTY(os.Stdin) {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		defer func() { _ = term.Restore(int(os.Stdin.Fd()), state) }()
	}

	rd := bufio.NewReaderSize(os.Stdin, 256*1024)
	var line []byte
	i := 0
	for {
		b, err := rd.ReadByte()
		if err != nil {
			return nil
		}
		if b != '\n' && b != '\r' {
			line = append(line, b)
			continue
		}
		if len(line) == 0 {
			continue
		}
		i++
		s := string(line)
		line = line[:0]
		if s == "QUIT" {
			fmt.Printf("ECHO quit after=%d\r\n", i)
			return nil
		}
		fmt.Printf("ECHO %04d [%s]\r\n", i, s)
	}
}

func isTTY(f *os.File) bool {
	_, err := pty.GetsizeFull(f)
	return err == nil
}
