// Package logbuf implements a bounded in-memory line ring for the
// marvel daemon. The daemon tees its stderr through a Buffer so
// recent log lines are retrievable over the RPC without having to
// read /tmp or ~/.marvel/log/daemon.log on the remote host.
//
// Lines are delimited on '\n'. A single Write may contain multiple
// lines; the buffer splits them. Lines exceeding the capacity are
// silently truncated to the capacity — that is, the buffer stays
// bounded in memory regardless of input.
//
// Cisco-style dedup: consecutive identical lines are collapsed. The
// first occurrence lands in the ring as-is; subsequent repeats bump
// an in-memory counter without consuming a slot. When a different
// line arrives, the buffer synthesizes a "last message repeated N
// times" summary before recording the new line. Tail() also
// synthesizes a "so far" summary for any still-active run so an
// operator querying in the middle of a burst sees it.
//
// Motivated by aae-orc-1d2: Skippy's desk poll loop filed one "ssh:
// client connected" line at 0.5 Hz, saturating the ring and pushing
// every interesting event out of the 5.5-hour window. Dedup turns
// a run of 1000 identical lines into 2 ring slots (one "real" + one
// summary) without hiding the rate.
package logbuf

import (
	"fmt"
	"strings"
	"sync"
)

// Buffer is a goroutine-safe ring of the most recent log lines.
// Zero value is not usable — call New.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	max   int

	// Dedup state. When a write records a line identical to lastLine,
	// the buffer bumps runCount instead of appending. When a different
	// line arrives (or Flush is called), a summary line is appended
	// before the new content. lastLine == "" means no run is active.
	lastLine string
	runCount int // 1 after initial write, 2+ when active run is accumulating
}

// New returns a Buffer that retains the most recent max lines.
func New(max int) *Buffer {
	if max < 1 {
		max = 1
	}
	return &Buffer{max: max}
}

// Write implements io.Writer. It splits the input on newlines,
// deduplicates adjacent identical lines (see package doc), and
// appends the result to the ring. Partial trailing lines (no
// newline) are still recorded — this matches the "tee stderr" use
// case where writes often arrive one log line at a time.
func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	s := string(p)

	// Split on '\n'. Strings that end with a newline will produce an
	// empty final element which we skip.
	parts := strings.Split(s, "\n")
	last := len(parts) - 1
	if parts[last] == "" {
		parts = parts[:last]
	}
	if len(parts) == 0 {
		return n, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range parts {
		// Strip carriage returns from CRLF inputs.
		line = strings.TrimRight(line, "\r")
		b.appendLocked(line)
	}
	b.trimLocked()
	return n, nil
}

// appendLocked records one line, applying dedup. Caller holds b.mu.
func (b *Buffer) appendLocked(line string) {
	if b.runCount > 0 && line == b.lastLine {
		b.runCount++
		return
	}
	// Run broke: emit a summary for the prior run before appending the
	// new line. runCount == 1 means the prior line occurred exactly
	// once — no summary needed.
	if b.runCount > 1 {
		b.lines = append(b.lines, summarize(b.runCount))
	}
	b.lines = append(b.lines, line)
	b.lastLine = line
	b.runCount = 1
}

// trimLocked enforces the capacity bound. Caller holds b.mu.
func (b *Buffer) trimLocked() {
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
}

// summarize returns the Cisco-style "last message repeated N times"
// summary line for a run of count identical lines.
func summarize(count int) string {
	return fmt.Sprintf("last message repeated %d times", count-1)
}

// Flush forces any active dedup run to emit its summary line into
// the ring. Useful for periodic flushing (a long-running identical
// message otherwise only surfaces a summary when a different line
// arrives) and for clean shutdown.
//
// Flush is idempotent: calling it twice in a row without an
// intervening Write appends nothing on the second call.
func (b *Buffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runCount > 1 {
		b.lines = append(b.lines, summarize(b.runCount))
		b.trimLocked()
		// Reset the run so subsequent identical writes start fresh —
		// the next occurrence re-records the line as a new run of 1,
		// which is the right answer: the summary's "N times" covered
		// the history up to now, and the clock starts over.
		b.lastLine = ""
		b.runCount = 0
	}
}

// Tail returns the most recent n lines (chronological order). If
// a dedup run is currently active (runCount > 1 but no summary has
// been flushed), Tail synthesizes a "... so far" summary line at
// the end so the operator sees the run without having to wait for
// a Flush. The synthesized line is read-only — it doesn't touch
// the stored buffer.
//
// If n exceeds the buffered count, returns everything. n<=0 returns empty.
func (b *Buffer) Tail(n int) []string {
	if n <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	activeSummary := ""
	if b.runCount > 1 {
		activeSummary = summarize(b.runCount) + " so far"
	}
	total := len(b.lines)
	if activeSummary != "" {
		total++
	}
	start := 0
	if total > n {
		start = total - n
	}
	out := make([]string, 0, total-start)
	for i := start; i < len(b.lines); i++ {
		out = append(out, b.lines[i])
	}
	if activeSummary != "" && (start <= len(b.lines)) {
		out = append(out, activeSummary)
	}
	return out
}

// Len returns the number of lines currently stored. Does not include
// the synthesized active-run summary that Tail may append — this is
// the raw stored count.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Cap returns the configured maximum number of lines.
func (b *Buffer) Cap() int { return b.max }
