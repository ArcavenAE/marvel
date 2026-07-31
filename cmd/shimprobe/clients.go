package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var spewLine = regexp.MustCompile(`SPEW (\d{6})`)

// runSink is the marvel-side consumer for signal 2. It reads the stream
// socket to EOF and reports how many spew lines arrived, in what order, and
// whether the terminator was seen.
func runSink(args []string) error {
	fs := flag.NewFlagSet("sink", flag.ExitOnError)
	sock := fs.String("s", "", "stream socket path")
	slow := fs.Duration("slow", 0, "sleep this long per read, to simulate a lagging consumer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sock == "" {
		return fmt.Errorf("-s is required")
	}
	conn, err := net.Dial("unix", *sock)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var (
		total    int
		seen     = map[int]bool{}
		lastSeq  int
		outOrder int
		expected int
		bytes    int
	)
	for sc.Scan() {
		if *slow > 0 {
			time.Sleep(*slow)
		}
		line := sc.Text()
		bytes += len(line) + 1
		total++
		if m := spewLine.FindStringSubmatch(line); m != nil {
			seq, _ := strconv.Atoi(m[1])
			if seen[seq] {
				continue
			}
			seen[seq] = true
			if seq < lastSeq {
				outOrder++
			}
			lastSeq = seq
		}
		if strings.Contains(line, "SPEWDONE") {
			fields := strings.Fields(strings.TrimSpace(line))
			expected, _ = strconv.Atoi(fields[len(fields)-1])
			break
		}
	}
	missing := 0
	for i := 1; i <= expected; i++ {
		if !seen[i] {
			missing++
		}
	}
	fmt.Printf("SINK lines=%d bytes=%d unique_spew=%d expected=%d missing=%d out_of_order=%d scan_err=%v\n",
		total, bytes, len(seen), expected, missing, outOrder, sc.Err())
	if expected > 0 && missing == 0 && outOrder == 0 {
		fmt.Println("SINK verdict=PASS")
	} else {
		fmt.Println("SINK verdict=FAIL")
	}
	return nil
}

// runCtl is the supervisor stand-in for signals 3 and 4.
func runCtl(args []string) error {
	fs := flag.NewFlagSet("ctl", flag.ExitOnError)
	sock := fs.String("c", "", "control socket path")
	cmd := fs.String("cmd", "status", "status|signal|stop|inject")
	sig := fs.String("signal", "", "signal name for -cmd signal")
	data := fs.String("data", "", "payload for -cmd inject (\\n and \\r are expanded)")
	repeat := fs.Int("repeat", 1, "send the command this many times")
	gap := fs.Duration("gap", 0, "pause between repeats")
	hold := fs.Duration("hold", 0, "keep the connection open this long after the last reply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sock == "" {
		return fmt.Errorf("-c is required")
	}
	conn, err := net.Dial("unix", *sock)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	payload := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t").Replace(*data)
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	for i := 0; i < *repeat; i++ {
		req := map[string]string{"cmd": *cmd}
		if *sig != "" {
			req["signal"] = *sig
		}
		if payload != "" {
			req["data"] = payload
		}
		if err := enc.Encode(req); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(os.Stdout, "CTL %s\n", string(raw))
		if *gap > 0 {
			time.Sleep(*gap)
		}
	}
	if *hold > 0 {
		time.Sleep(*hold)
	}
	return nil
}
