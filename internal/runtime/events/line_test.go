package events

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func newLineReader(s string) *bufio.Reader {
	return bufio.NewReaderSize(strings.NewReader(s), 64*1024)
}

// An over-long line that spans many reads must be discarded to its newline, so
// the next read returns the next line and not the tail of the long one. A line a
// little over the cap fits in one read after the crossing and hides a reader
// that stops at the crossing.
func TestReadLineDiscardsALongLineAcrossReads(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", MaxLine+100*1024)
	br := newLineReader(long + "\n" + "next\n")

	line, size, err := ReadLine(br)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if size != len(long)+1 {
		t.Errorf("size = %d, want the whole line with its newline (%d)", size, len(long)+1)
	}
	if len(line) != LongLinePrefix {
		t.Errorf("kept %d bytes, want only the %d-byte prefix", len(line), LongLinePrefix)
	}
	line, size, err = ReadLine(br)
	if !errors.Is(err, nil) || size != 0 || string(line) != "next" {
		t.Fatalf("second read = %q size %d err %v, want the next line, read whole", line, size, err)
	}
	if _, _, err := ReadLine(br); !errors.Is(err, io.EOF) {
		t.Fatalf("third read err = %v, want io.EOF", err)
	}
}

// The newline counts toward MaxLine, as it did for the bufio.Scanner limit and
// does in the goose parser: MaxLine-1 bytes plus a newline is the longest line
// read whole, and MaxLine bytes plus a newline is over.
func TestReadLineBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		content  int
		ending   string
		wantLong bool
	}{
		{"max minus one with LF", MaxLine - 1, "\n", false},
		{"exactly max with LF", MaxLine, "\n", true},
		{"max minus two with CRLF", MaxLine - 2, "\r\n", false},
		{"max minus one with CRLF", MaxLine - 1, "\r\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := strings.Repeat("y", tc.content)
			br := newLineReader(body + tc.ending + "next\n")
			line, size, err := ReadLine(br)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if tc.wantLong {
				if size != tc.content+len(tc.ending) || len(line) > LongLinePrefix {
					t.Fatalf("size %d, kept %d bytes: want over the limit with a bounded prefix", size, len(line))
				}
			} else if size != 0 || !bytes.Equal(line, []byte(body)) {
				t.Fatalf("size %d, kept %d bytes: want the line read whole", size, len(line))
			}
			if next, _, _ := ReadLine(br); string(next) != "next" {
				t.Fatalf("next line = %q", next)
			}
		})
	}
}
