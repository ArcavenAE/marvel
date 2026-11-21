package procargs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Marker is the part of a process's environment that says whether it belongs to
// marvel, and nothing else. The host vitals sampler reads it for a process that
// has lost its parent, to tell a marvel orphan from any other launchd child
// (docs/design/host-vitals.md section 2.1). Reading another process's
// environment is the same exposure as reading its arguments, so the parsers
// here keep two fields and drop every other byte as they go: the value of
// MARVEL_SESSION, and a flag for a MARVEL_SOCKET or MARVEL_HOME that names a
// marvel test or harness path. No other variable, and no other MARVEL_* value,
// is ever held, returned or logged.
type Marker struct {
	// Session is the value of MARVEL_SESSION, cut at MaxSessionLen bytes.
	Session string
	// MarvelPath is true when MARVEL_SOCKET or MARVEL_HOME names a marvel test
	// or harness path (MarvelPathMarks).
	MarvelPath bool
}

// MaxSessionLen caps the MARVEL_SESSION value a Marker keeps.
const MaxSessionLen = 128

// MarvelPathMarks are the path fragments that make a MARVEL_SOCKET or
// MARVEL_HOME a marvel test or harness path: a test's scratch home and a
// harness home.
var MarvelPathMarks = []string{"marvel-test-home-", "marvel-h-", "marvel-harness-homes"}

// Marvel reports whether the process carries any marvel marker.
func (m Marker) Marvel() bool { return m.Session != "" || m.MarvelPath }

// ParseProcargs2Marker reads the marker out of a kern.procargs2 buffer: argc,
// the executable path, the arguments, then the environment. The arguments are
// skipped, not read, so text in argv never counts; only the environment is
// scanned, and only for the two variables the Marker names.
func ParseProcargs2Marker(b []byte) (Marker, error) {
	if len(b) < 4 {
		return Marker{}, errors.New("kern.procargs2 returned too few bytes")
	}
	argc := int32(binary.LittleEndian.Uint32(b))
	if argc < 1 {
		return Marker{}, fmt.Errorf("kern.procargs2 reports %d arguments", argc)
	}
	rest := b[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return Marker{}, errors.New("kern.procargs2 has no executable path")
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return Marker{}, errors.New("kern.procargs2 ends inside the arguments")
		}
		rest = rest[end+1:]
	}
	return scanEnv(rest), nil
}

// ParseEnviron reads the marker out of /proc/<pid>/environ, NUL separated.
func ParseEnviron(b []byte) Marker { return scanEnv(b) }

// scanEnv walks NUL-separated NAME=value entries and keeps the two marker
// fields. A name must match exactly: MARVEL_SESSIONS or XMARVEL_SESSION is not
// MARVEL_SESSION. An empty MARVEL_SESSION is no marker, and so is an empty
// MARVEL_SOCKET or MARVEL_HOME. Every other entry is dropped unread.
func scanEnv(b []byte) Marker {
	var m Marker
	for len(b) > 0 {
		entry := b
		if i := bytes.IndexByte(b, 0); i >= 0 {
			entry, b = b[:i], b[i+1:]
		} else {
			b = nil
		}
		name, value, ok := bytes.Cut(entry, []byte("="))
		if !ok || len(value) == 0 {
			continue
		}
		switch string(name) {
		case "MARVEL_SESSION":
			if m.Session == "" {
				m.Session = printable(value)
			}
		case "MARVEL_SOCKET", "MARVEL_HOME":
			for _, mark := range MarvelPathMarks {
				if bytes.Contains(value, []byte(mark)) {
					m.MarvelPath = true
				}
			}
		}
	}
	return m
}

// printable cuts a value at MaxSessionLen bytes and replaces every byte that
// is not printable ASCII with '?', so a hostile value cannot carry terminal
// control sequences into a table or an event.
func printable(v []byte) string {
	if len(v) > MaxSessionLen {
		v = v[:MaxSessionLen]
	}
	out := make([]byte, len(v))
	for i, c := range v {
		if c < 0x20 || c > 0x7e {
			c = '?'
		}
		out[i] = c
	}
	return string(out)
}
