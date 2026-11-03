package goosewatch

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// defaultStderrCap is the file size limit when Config.StderrCap is not set.
const defaultStderrCap = 64 << 10

// stderrSink holds goose's stderr to a size limit on the write side. The head is
// written as it arrives; the last tailMax bytes are kept in memory and written
// at exit, after a marker that names how many bytes fell between the two.
type stderrSink struct {
	cfg Config

	mu       sync.Mutex
	f        *os.File
	headLeft int
	tailMax  int
	tail     []byte
	dropped  int
	lastByte byte
}

func newStderrSink(cfg Config) *stderrSink { return &stderrSink{cfg: cfg} }

func (s *stderrSink) open() error {
	limit := s.cfg.StderrCap
	if limit <= 0 {
		limit = defaultStderrCap
	}
	s.tailMax = min(tailBytes, limit/2)
	s.headLeft = limit - s.tailMax
	f, err := os.OpenFile(s.cfg.StderrFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open the stderr file: %w", err)
	}
	s.f = f
	return nil
}

func (s *stderrSink) close() {
	if s.f != nil {
		// A close error loses nothing that was not already written.
		_ = s.f.Close()
	}
}

// copyFrom reads goose's stderr until EOF.
func (s *stderrSink) copyFrom(r io.Reader) {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// write sends p to the file under the cap.
func (s *stderrSink) write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headLeft > 0 {
		n := min(s.headLeft, len(p))
		_, _ = s.f.Write(p[:n])
		s.lastByte = p[n-1]
		s.headLeft -= n
		p = p[n:]
	}
	if len(p) == 0 {
		return
	}
	s.tail = append(s.tail, p...)
	if over := len(s.tail) - s.tailMax; over > 0 {
		s.dropped += over
		s.tail = append(s.tail[:0], s.tail[over:]...)
	}
}

// own writes one of the wrapper's own error lines.
func (s *stderrSink) own(format string, args ...any) {
	s.write([]byte("goose-watch: " + fmt.Sprintf(format, args...) + "\n"))
}

// finish writes the end of the file: the max-gap line, the drop marker and the
// tail, then lastLine if there is one (the watchdog line on a kill, the
// forwarded-signal line on a pane kill), so that line is the last one.
func (s *stderrSink) finish(tr *tracker, lastLine string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []byte
	startLine := func(last byte) {
		if last != 0 && last != '\n' {
			out = append(out, '\n')
		}
	}
	startLine(s.lastByte)
	if tr != nil {
		tr.mu.Lock()
		out = fmt.Appendf(out, "watchdog: max-gap %.1fs lines %d\n", tr.maxGap.Seconds(), tr.lines)
		tr.mu.Unlock()
	}
	if s.dropped > 0 {
		out = fmt.Appendf(out, "[... %d bytes dropped]\n", s.dropped)
	}
	if len(s.tail) > 0 {
		out = append(out, s.tail...)
		startLine(s.tail[len(s.tail)-1])
	}
	if lastLine != "" {
		out = append(out, lastLine...)
		out = append(out, '\n')
	}
	_, _ = s.f.Write(out)
}
