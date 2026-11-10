package events

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// MaxLine is the longest stream line a parser reads whole, counting the line's
// newline (and the carriage return of a CRLF ending): a line of MaxLine content
// bytes, or a CRLF line of MaxLine-1, is over the limit. This matches the
// bufio.Scanner limit the parsers had and the goose parser. A longer line is
// read to its newline without being kept, reported as one parse error with a
// bounded prefix, and the stream goes on. A bufio.Scanner would stop the parse
// at such a line and lose every later one.
const MaxLine = 4 << 20

// LongLinePrefix is how much of an over-long line the parse error quotes.
const LongLinePrefix = 256

// ReadLine reads one line. It returns the line without its newline when it is
// within MaxLine, and size 0. For a longer line it returns only the first
// LongLinePrefix bytes and the full size, and reads on to the newline without
// keeping the rest. The error is io.EOF at the end of the stream, after which
// line may still hold a final line with no newline.
func ReadLine(br *bufio.Reader) (line []byte, size int, err error) {
	var buf []byte
	long := false
	for {
		chunk, rerr := br.ReadSlice('\n')
		size += len(chunk)
		if !long {
			if size > MaxLine {
				long = true
				buf = append(buf[:0], buf[:min(len(buf), LongLinePrefix)]...)
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		buf = bytes.TrimRight(buf, "\r\n")
		if long {
			return buf[:min(len(buf), LongLinePrefix)], size, rerr
		}
		return buf, 0, rerr
	}
}

// LongLineMessage is the parse error text for an over-long line: its full size
// and a bounded prefix.
func LongLineMessage(prefix []byte, size int) string {
	return fmt.Sprintf("line over the size limit (%d bytes): %s", size, strings.ToValidUTF8(string(prefix), "?"))
}
