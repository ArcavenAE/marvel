//go:build ignore

// Synthetic output producer for the stream-attachment byte-path probe.
// Emits N numbered lines as fast as the sink accepts them, each carrying an
// ANSI SGR wrapper and a fixed-width payload so loss, reordering, and escape
// mangling are all detectable from the sink's bytes alone.
//
// Line shape: ESC[32mSEQ:<n>|<payload>|ESC[0m
// Final line: DONE:<n-emitted>
//
// Usage: producer <n> <hold-seconds> <stats-file> [rate-lines-per-sec]
// The stats file records write-side elapsed seconds, which is the sink's
// backpressure signature: a blocking sink slows the producer, a sink that
// buffers or drops does not.
//
// With a rate, output is paced and flushed per line, which is the shape a
// streaming agent actually produces. Unpaced, the run is a single burst that
// completes faster than any poller's interval and so cannot distinguish poll
// rates from each other.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	n, hold, stats := 20000, 0.0, ""
	if len(os.Args) > 1 {
		n, _ = strconv.Atoi(os.Args[1])
	}
	if len(os.Args) > 2 {
		hold, _ = strconv.ParseFloat(os.Args[2], 64)
	}
	if len(os.Args) > 3 {
		stats = os.Args[3]
	}
	rate := 0.0
	if len(os.Args) > 4 {
		rate, _ = strconv.ParseFloat(os.Args[4], 64)
	}
	payload := strings.Repeat("x", 48)
	w := bufio.NewWriterSize(os.Stdout, 1<<16)
	start := time.Now()
	for i := 1; i <= n; i++ {
		fmt.Fprintf(w, "\x1b[32mSEQ:%d|%s|\x1b[0m\n", i, payload)
		if rate > 0 {
			w.Flush()
			target := start.Add(time.Duration(float64(i) / rate * float64(time.Second)))
			if d := time.Until(target); d > 0 {
				time.Sleep(d)
			}
		}
	}
	fmt.Fprintf(w, "DONE:%d\n", n)
	w.Flush()
	elapsed := time.Since(start).Seconds()
	if stats != "" {
		os.WriteFile(stats, []byte(fmt.Sprintf("%.4f\n", elapsed)), 0o644)
	}
	// Held open so a capture-pane poller can observe the final pane state
	// instead of racing pane teardown. Zero for the streaming sinks, where
	// holding would inflate their measured wall time.
	time.Sleep(time.Duration(hold * float64(time.Second)))
}
