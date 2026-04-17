// Package logbuf implements a bounded in-memory line ring for the
// marvel daemon. The daemon tees its stderr through a Buffer so
// recent log lines are retrievable over the RPC without having to
// read /tmp or ~/.marvel/log/daemon.log on the remote host.
//
// Lines are delimited on '\n'. A single Write may contain multiple
// lines; the buffer splits them. Lines exceeding the capacity are
// silently truncated to the capacity — that is, the buffer stays
// bounded in memory regardless of input.
package logbuf

import (
	"strings"
	"sync"
)

// Buffer is a goroutine-safe ring of the most recent log lines.
// Zero value is not usable — call New.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

// New returns a Buffer that retains the most recent max lines.
func New(max int) *Buffer {
	if max < 1 {
		max = 1
	}
	return &Buffer{max: max}
}

// Write implements io.Writer. It splits the input on newlines and
// appends each non-empty line to the ring. Partial trailing lines
// (no newline) are still recorded — this matches the "tee stderr"
// use case where writes often arrive one log line at a time.
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
		b.lines = append(b.lines, line)
	}
	if len(b.lines) > b.max {
		// Drop from the head; keep the tail.
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	return n, nil
}

// Tail returns the most recent n lines (chronological order). If n
// exceeds the buffered count, returns everything. n<=0 returns empty.
func (b *Buffer) Tail(n int) []string {
	if n <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	start := 0
	if len(b.lines) > n {
		start = len(b.lines) - n
	}
	out := make([]string, len(b.lines)-start)
	copy(out, b.lines[start:])
	return out
}

// Len returns the number of lines currently buffered.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Cap returns the configured maximum number of lines.
func (b *Buffer) Cap() int { return b.max }
