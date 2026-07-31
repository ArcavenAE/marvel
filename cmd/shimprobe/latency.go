package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/creack/pty"
)

// runRTT measures the round trip from "a supervisor asks for a keystroke" to
// "the supervisor sees the child's answer", once per mode, using the same echo
// child both times. The delta between modes is the cost the shim adds: one
// extra PTY pair plus two Unix-socket hops.
//
//	single: driver -> pty master -> child -> pty master -> driver
//	shim:   driver -> control uds -> shim -> pty master -> child
//	        -> pty master -> shim -> stream uds -> driver
//
// Absolute numbers here are dominated by process scheduling on a loaded
// laptop; only the difference between the two modes is worth quoting.
func runRTT(args []string) error {
	fs := flag.NewFlagSet("rtt", flag.ExitOnError)
	mode := fs.String("mode", "single", "single (one PTY, no shim) or shim (via control+stream sockets)")
	n := fs.Int("n", 50, "number of round trips")
	ctl := fs.String("c", "", "control socket path (shim mode)")
	str := fs.String("s", "", "stream socket path (shim mode)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch *mode {
	case "single":
		return rttSingle(*n)
	case "shim":
		if *ctl == "" || *str == "" {
			return fmt.Errorf("-c and -s are required in shim mode")
		}
		return rttShim(*n, *ctl, *str)
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
}

func rttSingle(n int) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "echo", "-for", "120s")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()

	rd := bufio.NewReader(ptmx)
	samples, err := pingLoop(n, func(s string) error {
		_, werr := ptmx.WriteString(s)
		return werr
	}, rd)
	if err != nil {
		return err
	}
	report("single", samples)
	return nil
}

func rttShim(n int, ctlPath, strPath string) error {
	strConn, err := net.Dial("unix", strPath)
	if err != nil {
		return err
	}
	defer func() { _ = strConn.Close() }()
	ctlConn, err := net.Dial("unix", ctlPath)
	if err != nil {
		return err
	}
	defer func() { _ = ctlConn.Close() }()

	enc := json.NewEncoder(ctlConn)
	dec := json.NewDecoder(ctlConn)
	rd := bufio.NewReader(strConn)

	samples, err := pingLoop(n, func(s string) error {
		if err := enc.Encode(map[string]string{"cmd": "inject", "data": s}); err != nil {
			return err
		}
		var raw json.RawMessage
		return dec.Decode(&raw)
	}, rd)
	if err != nil {
		return err
	}
	report("shim", samples)
	return nil
}

// pingLoop sends PING<i> through send and waits for the echo child's stamped
// reply to come back on rd. Matching on the bracketed marker rather than the
// bare token skips the tty's own echo of the input.
func pingLoop(n int, send func(string) error, rd *bufio.Reader) ([]time.Duration, error) {
	samples := make([]time.Duration, 0, n)
	for i := 1; i <= n; i++ {
		want := fmt.Sprintf("[PING%d]", i)
		start := time.Now()
		if err := send(fmt.Sprintf("PING%d\n", i)); err != nil {
			return nil, err
		}
		for {
			line, err := rd.ReadString('\n')
			if strings.Contains(line, want) {
				samples = append(samples, time.Since(start))
				break
			}
			if err != nil {
				if err == io.EOF {
					return nil, fmt.Errorf("child stream ended waiting for %s", want)
				}
				return nil, err
			}
		}
	}
	return samples, nil
}

func report(tag string, s []time.Duration) {
	if len(s) == 0 {
		fmt.Printf("RTT %s n=0\n", tag)
		return
	}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	fmt.Printf("RTT %s n=%d min=%s p50=%s p90=%s max=%s mean=%s\n",
		tag, len(s), s[0].Round(time.Microsecond),
		s[len(s)/2].Round(time.Microsecond),
		s[(len(s)*9)/10].Round(time.Microsecond),
		s[len(s)-1].Round(time.Microsecond),
		(sum / time.Duration(len(s))).Round(time.Microsecond))
}
