// Package resumejournal writes and reads the JSON mirror of the resume journal
// (docs/design/fleet-hibernate.md section 5). The journal itself lives in bolt;
// the mirror sits beside the daemon's socket so a person can read it with the
// daemon down and a host backup can carry it.
package resumejournal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// endKind is the value of the terminal marker's "end" field.
const endKind = "resume-journal"

// marker is the last line of a complete mirror. Entries is how many entry lines
// precede it, so a reader tells a mirror cut short from one that is whole even
// when the cut falls on a line boundary.
type marker struct {
	End     string `json:"end"`
	Entries int    `json:"entries"`
}

// MirrorPath is where the mirror sits for a daemon listening at socket: beside
// the socket, named after it. A TCP address has no file beside it, so it has no
// mirror and MirrorPath returns "".
func MirrorPath(socket string) string {
	if socket == "" || strings.Contains(socket, ":") {
		return ""
	}
	return socket + ".resume.jsonl"
}

// WriteMirror replaces the mirror with entries: one JSON line per entry, then
// the terminal marker last. It writes a temporary file beside the mirror and
// renames it, so a reader sees the old mirror or the new one.
func WriteMirror(path string, entries []api.ResumeEntry) error {
	var buf bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encode resume mirror: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	end, err := json.Marshal(marker{End: endKind, Entries: len(entries)})
	if err != nil {
		return fmt.Errorf("encode resume mirror: %w", err)
	}
	buf.Write(end)
	buf.WriteByte('\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".resume-*.jsonl")
	if err != nil {
		return fmt.Errorf("write resume mirror: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write resume mirror: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write resume mirror: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write resume mirror: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write resume mirror: %w", err)
	}
	return nil
}

// Mirror is what ReadMirror found.
type Mirror struct {
	// Entries are the entry lines that parsed, in file order.
	Entries []api.ResumeEntry
	// Complete is true only when the last non-empty line is the terminal marker
	// and its count matches the entry lines before it. A mirror that is not
	// complete is a prefix of a write, not a smaller fleet.
	Complete bool
}

// ReadMirror reads the mirror. found is false when there is none. A mirror
// without its marker is returned with Complete false and no error; an entry
// line that does not parse is an error.
func ReadMirror(path string) (m Mirror, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Mirror{}, false, nil
	}
	if err != nil {
		return Mirror{}, false, fmt.Errorf("read resume mirror: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var lines [][]byte
	for sc.Scan() {
		if b := bytes.TrimSpace(sc.Bytes()); len(b) > 0 {
			lines = append(lines, append([]byte(nil), b...))
		}
	}
	if err := sc.Err(); err != nil {
		return Mirror{}, false, fmt.Errorf("read resume mirror %s: %w", path, err)
	}
	body := lines
	var mk marker
	hasMarker := false
	if n := len(lines); n > 0 {
		if json.Unmarshal(lines[n-1], &mk) == nil && mk.End == endKind {
			hasMarker = true
			body = lines[:n-1]
		}
	}
	for i, l := range body {
		var e api.ResumeEntry
		if err := json.Unmarshal(l, &e); err != nil {
			// The last line of a mirror with no marker may be cut mid-line.
			if !hasMarker && i == len(body)-1 {
				break
			}
			return Mirror{}, false, fmt.Errorf("parse resume mirror %s line %d: %w", path, i+1, err)
		}
		m.Entries = append(m.Entries, e)
	}
	m.Complete = hasMarker && mk.Entries == len(m.Entries)
	return m, true, nil
}
